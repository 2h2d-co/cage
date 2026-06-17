package cage

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCacheLaunchdInstallCommandWritesAndBootstrapsLaunchAgent(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd commands are macOS-only")
	}

	home := privateTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "xdg-cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "xdg-state"))
	configPath := filepath.Join(privateTempDir(t), "config.toml")
	cfg := emptyConfig(configPath)
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}

	fake := &fakeLaunchctlRunner{}
	app := &App{
		configPath: configPath,
		out:        discardWriter{},
		errOut:     discardWriter{},
		executablePath: func() (string, error) {
			return filepath.Join(home, "bin", "cage"), nil
		},
		runLaunchctl: fake.run,
	}
	cmd := app.newCacheLaunchdInstallCommand()
	cmd.SetArgs([]string{"--interval", "30m"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", cachePruneLaunchAgentDefaultLabel+".plist")
	assertPrivateMode(t, plistPath, 0o644)
	assertTestFileContains(t, plistPath, "<string>"+filepath.Join(home, "bin", "cage")+"</string>")
	assertTestFileContains(t, plistPath, "<string>"+configPath+"</string>")
	assertTestFileContains(t, plistPath, "<integer>1800</integer>")
	assertTestFileContains(t, plistPath, filepath.Join(home, "Library", "Logs", cachePruneLaunchAgentDefaultLabel+".log"))
	assertTestFileContains(t, plistPath, filepath.Join(home, "Library", "Logs", cachePruneLaunchAgentDefaultLabel+"-error.log"))
	assertTestFileContains(t, plistPath, "<key>HOME</key>")
	assertTestFileContains(t, plistPath, "<string>"+home+"</string>")
	assertTestFileContains(t, plistPath, "<key>XDG_CACHE_HOME</key>")
	assertTestFileContains(t, plistPath, "<string>"+filepath.Join(home, "xdg-cache")+"</string>")
	assertTestFileContains(t, plistPath, "<key>XDG_STATE_HOME</key>")
	assertTestFileContains(t, plistPath, "<string>"+filepath.Join(home, "xdg-state")+"</string>")

	want := [][]string{
		{"print", launchdServiceTarget(cachePruneLaunchAgentDefaultLabel)},
		{"enable", launchdServiceTarget(cachePruneLaunchAgentDefaultLabel)},
		{"bootstrap", launchdGUIDomain(), plistPath},
		{"kickstart", "-k", launchdServiceTarget(cachePruneLaunchAgentDefaultLabel)},
	}
	if !reflect.DeepEqual(fake.calls, want) {
		t.Fatalf("launchctl calls = %#v, want %#v", fake.calls, want)
	}
}

func TestCacheLaunchdInstallCommandSupportsLabelOverride(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd commands are macOS-only")
	}

	home := privateTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv(cachePruneLaunchAgentLabelEnv, "co.2h2d.cage.test-cache-prune")
	configPath := filepath.Join(privateTempDir(t), "config.toml")
	cfg := emptyConfig(configPath)
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}

	fake := &fakeLaunchctlRunner{}
	app := &App{
		configPath: configPath,
		out:        discardWriter{},
		errOut:     discardWriter{},
		executablePath: func() (string, error) {
			return filepath.Join(home, "bin", "cage"), nil
		},
		runLaunchctl: fake.run,
	}
	cmd := app.newCacheLaunchdInstallCommand()
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "co.2h2d.cage.test-cache-prune.plist")
	assertTestFileContains(t, plistPath, "<string>co.2h2d.cage.test-cache-prune</string>")
	assertTestFileContains(t, plistPath, filepath.Join(home, "Library", "Logs", "co.2h2d.cage.test-cache-prune-error.log"))
}

func TestCacheLaunchdInstallCommandRequiresOverwriteForChangedPlist(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd commands are macOS-only")
	}

	home := privateTempDir(t)
	t.Setenv("HOME", home)
	configPath := filepath.Join(privateTempDir(t), "config.toml")
	cfg := emptyConfig(configPath)
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}

	fake := &fakeLaunchctlRunner{}
	app := &App{
		configPath: configPath,
		out:        discardWriter{},
		errOut:     discardWriter{},
		executablePath: func() (string, error) {
			return filepath.Join(home, "bin", "cage"), nil
		},
		runLaunchctl: fake.run,
	}
	install := app.newCacheLaunchdInstallCommand()
	install.SetArgs([]string{"--interval", "30m"})
	if err := install.Execute(); err != nil {
		t.Fatal(err)
	}

	changed := app.newCacheLaunchdInstallCommand()
	changed.SetArgs([]string{"--interval", "45m"})
	err := changed.Execute()
	if err == nil || !strings.Contains(err.Error(), "use --overwrite") {
		t.Fatalf("changed install error = %v, want overwrite requirement", err)
	}

	overwrite := app.newCacheLaunchdInstallCommand()
	overwrite.SetArgs([]string{"--interval", "45m", "--overwrite"})
	if err := overwrite.Execute(); err != nil {
		t.Fatal(err)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", cachePruneLaunchAgentDefaultLabel+".plist")
	assertTestFileContains(t, plistPath, "<integer>2700</integer>")
}

func TestCacheLaunchdUninstallCommandDisablesBootsOutAndRemovesLaunchAgent(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd commands are macOS-only")
	}

	home := privateTempDir(t)
	t.Setenv("HOME", home)
	plistPath := filepath.Join(home, "Library", "LaunchAgents", cachePruneLaunchAgentDefaultLabel+".plist")
	if err := atomicWriteFileMode(plistPath, []byte("plist"), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeLaunchctlRunner{loaded: true}
	app := &App{
		out:          discardWriter{},
		errOut:       discardWriter{},
		runLaunchctl: fake.run,
	}
	cmd := app.newCacheLaunchdUninstallCommand()
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	assertTestFileMissing(t, plistPath)

	want := [][]string{
		{"print", launchdServiceTarget(cachePruneLaunchAgentDefaultLabel)},
		{"bootout", launchdServiceTarget(cachePruneLaunchAgentDefaultLabel)},
		{"disable", launchdServiceTarget(cachePruneLaunchAgentDefaultLabel)},
	}
	if !reflect.DeepEqual(fake.calls, want) {
		t.Fatalf("launchctl calls = %#v, want %#v", fake.calls, want)
	}
}

type fakeLaunchctlRunner struct {
	calls  [][]string
	loaded bool
}

func (f *fakeLaunchctlRunner) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, slices.Clone(args))
	if len(args) > 0 {
		switch args[0] {
		case "print":
			if !f.loaded {
				return []byte("not loaded"), errors.New("not loaded")
			}
		case "bootstrap":
			f.loaded = true
		case "bootout":
			f.loaded = false
		}
	}
	return nil, nil
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func assertTestFileContains(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("%s missing %q:\n%s", path, want, data)
	}
}

func assertTestFileMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(filepath.Clean(path))
	if err == nil {
		t.Fatalf("%s exists, want missing", path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat %s: %v", path, err)
	}
}
