package cage

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	_ "embed"

	"github.com/spf13/cobra"
)

const (
	cachePruneLaunchAgentDefaultLabel    = "co.2h2d.cage.cache-prune"
	cachePruneLaunchAgentLabelEnv        = "CAGE_CACHE_PRUNE_LAUNCHD_LABEL"
	cachePruneLaunchAgentTemplateName    = "co.2h2d.cage.cache-prune.plist.tmpl"
	defaultCachePruneLaunchAgentInterval = "1h"
)

var launchdLabelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

//go:embed launchd/co.2h2d.cage.cache-prune.plist.tmpl
var cachePruneLaunchAgentTemplate string

type executablePathFunc func() (string, error)

type launchctlRunner func(args ...string) ([]byte, error)

type cachePruneLaunchAgentConfig struct {
	Label                string
	ExecutablePath       string
	ConfigPath           string
	StartIntervalSeconds int
	LogPath              string
	ErrorLogPath         string
	PlistPath            string
	EnvironmentVariables []launchAgentEnvironmentVariable
}

type launchAgentEnvironmentVariable struct {
	Name  string
	Value string
}

func (a *App) newCacheLaunchdCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "launchd",
		Aliases: []string{"agent", "schedule"},
		Short:   "Manage periodic cache pruning with launchd",
		Long:    "Install or uninstall a per-user launchd LaunchAgent that periodically runs cage cache prune.",
	}
	cmd.AddCommand(a.newCacheLaunchdInstallCommand())
	cmd.AddCommand(a.newCacheLaunchdUninstallCommand())
	return cmd
}

func (a *App) newCacheLaunchdInstallCommand() *cobra.Command {
	var interval string
	var overwrite bool
	cmd := &cobra.Command{
		Use:     "install",
		Aliases: []string{"setup", "enable"},
		Short:   "Install the periodic cache prune LaunchAgent",
		Long:    "Render, install, enable, bootstrap, and kickstart the per-user launchd LaunchAgent that periodically runs cage cache prune.",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			if !cfg.Exists {
				return fmt.Errorf("config %s does not exist", cfg.Path)
			}
			agent, err := a.cachePruneLaunchAgentConfig(cfg, interval)
			if err != nil {
				return err
			}
			plist, err := renderCachePruneLaunchAgent(agent)
			if err != nil {
				return err
			}
			if err := installCachePruneLaunchAgentPlist(agent, plist, overwrite); err != nil {
				return err
			}

			run := a.launchctlRunner()
			target := launchdServiceTarget(agent.Label)
			if launchdServiceLoaded(run, target) {
				if err := runLaunchctlStep(run, "bootout", target); err != nil {
					return err
				}
			}
			if err := runLaunchctlStep(run, "enable", target); err != nil {
				return err
			}
			if err := runLaunchctlStep(run, "bootstrap", launchdGUIDomain(), agent.PlistPath); err != nil {
				return err
			}
			if err := runLaunchctlStep(run, "kickstart", "-k", target); err != nil {
				return err
			}

			_, err = fmt.Fprintf(a.out, "installed cache prune LaunchAgent %s\n  plist: %s\n  interval: %s\n  log: %s\n  error log: %s\n", agent.Label, agent.PlistPath, interval, agent.LogPath, agent.ErrorLogPath)
			return err
		},
	}
	cmd.Flags().StringVar(&interval, "interval", defaultCachePruneLaunchAgentInterval, "positive whole-second Go duration between cache prune runs")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace an existing cage cache prune LaunchAgent plist")
	return cmd
}

func (a *App) newCacheLaunchdUninstallCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "uninstall",
		Aliases: []string{"remove", "disable"},
		Short:   "Uninstall the periodic cache prune LaunchAgent",
		Long:    "Disable, unload, and remove the per-user launchd LaunchAgent that periodically runs cage cache prune.",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			agent, err := a.cachePruneLaunchAgentConfig(nil, defaultCachePruneLaunchAgentInterval)
			if err != nil {
				return err
			}
			run := a.launchctlRunner()
			target := launchdServiceTarget(agent.Label)
			loaded := launchdServiceLoaded(run, target)
			plistExists, err := fileExists(agent.PlistPath)
			if err != nil {
				return fmt.Errorf("stat LaunchAgent plist %s: %w", agent.PlistPath, err)
			}
			if !loaded && !plistExists {
				_, err := fmt.Fprintf(a.out, "cache prune LaunchAgent %s is not installed\n", agent.Label)
				return err
			}
			if loaded {
				if err := runLaunchctlStep(run, "bootout", target); err != nil {
					return err
				}
			}
			if err := runLaunchctlStep(run, "disable", target); err != nil {
				return err
			}
			if plistExists {
				if err := os.Remove(filepath.Clean(agent.PlistPath)); err != nil {
					return fmt.Errorf("remove LaunchAgent plist %s: %w", agent.PlistPath, err)
				}
			}
			_, err = fmt.Fprintf(a.out, "uninstalled cache prune LaunchAgent %s\n", agent.Label)
			return err
		},
	}
	return cmd
}

func (a *App) cachePruneLaunchAgentConfig(cfg *Config, interval string) (cachePruneLaunchAgentConfig, error) {
	seconds, err := parseLaunchdInterval(interval)
	if err != nil {
		return cachePruneLaunchAgentConfig{}, fmt.Errorf("--interval: %w", err)
	}
	executablePath := ""
	configPath := ""
	if cfg != nil {
		executablePath, err = a.absoluteExecutablePath()
		if err != nil {
			return cachePruneLaunchAgentConfig{}, err
		}
		configPath = normalizedConfigPath(cfg.Path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return cachePruneLaunchAgentConfig{}, fmt.Errorf("find home directory: %w", err)
	}
	label, err := cachePruneLaunchAgentLabel()
	if err != nil {
		return cachePruneLaunchAgentConfig{}, err
	}
	return cachePruneLaunchAgentConfig{
		Label:                label,
		ExecutablePath:       executablePath,
		ConfigPath:           configPath,
		StartIntervalSeconds: seconds,
		LogPath:              filepath.Join(home, "Library", "Logs", label+".log"),
		ErrorLogPath:         filepath.Join(home, "Library", "Logs", label+"-error.log"),
		PlistPath:            filepath.Join(home, "Library", "LaunchAgents", label+".plist"),
		EnvironmentVariables: cachePruneLaunchAgentEnvironment(home, cfg != nil),
	}, nil
}

func cachePruneLaunchAgentLabel() (string, error) {
	label := os.Getenv(cachePruneLaunchAgentLabelEnv)
	if label == "" {
		label = cachePruneLaunchAgentDefaultLabel
	}
	if !launchdLabelPattern.MatchString(label) {
		return "", fmt.Errorf("%s must contain only letters, numbers, dot, underscore, and dash, and must not start with punctuation", cachePruneLaunchAgentLabelEnv)
	}
	return label, nil
}

func cachePruneLaunchAgentEnvironment(home string, includeXDG bool) []launchAgentEnvironmentVariable {
	variables := []launchAgentEnvironmentVariable{{Name: "HOME", Value: home}}
	if includeXDG {
		for _, name := range []string{"XDG_CACHE_HOME", "XDG_STATE_HOME"} {
			if value := os.Getenv(name); value != "" {
				variables = append(variables, launchAgentEnvironmentVariable{Name: name, Value: value})
			}
		}
	}
	return variables
}

func (a *App) absoluteExecutablePath() (string, error) {
	executablePath := a.executablePath
	if executablePath == nil {
		executablePath = os.Executable
	}
	path, err := executablePath()
	if err != nil {
		return "", fmt.Errorf("find executable path: %w", err)
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve executable path %s: %w", path, err)
	}
	return absolute, nil
}

func (a *App) launchctlRunner() launchctlRunner {
	if a.runLaunchctl != nil {
		return a.runLaunchctl
	}
	return runLaunchctl
}

func parseLaunchdInterval(value string) (int, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if duration <= 0 {
		return 0, errors.New("must be positive")
	}
	if duration%time.Second != 0 {
		return 0, errors.New("must be a whole-second duration")
	}
	return int(duration / time.Second), nil
}

func renderCachePruneLaunchAgent(agent cachePruneLaunchAgentConfig) ([]byte, error) {
	tmpl, err := template.New(cachePruneLaunchAgentTemplateName).Funcs(template.FuncMap{"xml": xmlEscapeString}).Parse(cachePruneLaunchAgentTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse LaunchAgent template: %w", err)
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, agent); err != nil {
		return nil, fmt.Errorf("render LaunchAgent template: %w", err)
	}
	return rendered.Bytes(), nil
}

func xmlEscapeString(value string) (string, error) {
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(value)); err != nil {
		return "", err
	}
	return escaped.String(), nil
}

func installCachePruneLaunchAgentPlist(agent cachePruneLaunchAgentConfig, content []byte, overwrite bool) error {
	cleaned := filepath.Clean(agent.PlistPath)
	if existing, err := os.ReadFile(cleaned); err == nil {
		if !overwrite && !bytes.Equal(existing, content) {
			return fmt.Errorf("LaunchAgent plist %s already exists; use --overwrite to replace it", agent.PlistPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read LaunchAgent plist %s: %w", agent.PlistPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(cleaned), 0o700); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(agent.LogPath), 0o700); err != nil {
		return fmt.Errorf("create Logs directory: %w", err)
	}
	if err := atomicWriteFileMode(cleaned, content, 0o644); err != nil {
		return fmt.Errorf("write LaunchAgent plist %s: %w", agent.PlistPath, err)
	}
	return nil
}

func launchdGUIDomain() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

func launchdServiceTarget(label string) string {
	return launchdGUIDomain() + "/" + label
}

func launchdServiceLoaded(run launchctlRunner, target string) bool {
	_, err := run("print", target)
	return err == nil
}

func runLaunchctlStep(run launchctlRunner, args ...string) error {
	output, err := run(args...)
	if err == nil {
		return nil
	}
	command := "launchctl " + strings.Join(args, " ")
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return fmt.Errorf("%s: %w", command, err)
	}
	return fmt.Errorf("%s: %w: %s", command, err, trimmed)
}

func runLaunchctl(args ...string) ([]byte, error) {
	outputR, outputW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer func() { _ = outputR.Close() }()

	argv := append([]string{"launchctl"}, args...)
	process, err := os.StartProcess("/bin/launchctl", argv, &os.ProcAttr{
		Files: []*os.File{os.Stdin, outputW, outputW},
		Env:   []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"},
	})
	closeErr := outputW.Close()
	if err != nil {
		return nil, errors.Join(err, closeErr)
	}
	if closeErr != nil {
		return nil, closeErr
	}

	var output bytes.Buffer
	var outputErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, outputErr = io.Copy(&output, outputR)
	}()

	state, waitErr := process.Wait()
	wg.Wait()
	if err := errors.Join(waitErr, outputErr); err != nil {
		return output.Bytes(), err
	}
	if !state.Success() {
		return output.Bytes(), fmt.Errorf("exited with %s", state.String())
	}
	return output.Bytes(), nil
}
