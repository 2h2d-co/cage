package cage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

// App holds process-wide CLI settings shared by cage commands.
type App struct {
	configPath                 string
	verbose                    bool
	debug                      bool
	version                    string
	out                        io.Writer
	errOut                     io.Writer
	decryptProviderToken       providerTokenDecryptor
	newOnePasswordEnvironments onePasswordEnvironmentsFactory
	executablePath             executablePathFunc
	runLaunchctl               launchctlRunner
}

const skipStartupCleanupAnnotation = "cage.skipStartupCleanup"

// NewRootCommand builds the root cage command tree.
func NewRootCommand(version string) *cobra.Command {
	app := &App{
		version:                    version,
		out:                        os.Stdout,
		errOut:                     os.Stderr,
		decryptProviderToken:       decryptProviderToken,
		newOnePasswordEnvironments: newOnePasswordEnvironments,
		executablePath:             os.Executable,
		runLaunchctl:               runLaunchctl,
	}

	root := &cobra.Command{
		Use:           "cage",
		Short:         "Minimal age-backed 1Password environment secret manager",
		Long:          "cage is a minimal and opinionated macOS secret manager for loading 1Password Environments through age-protected service account identities.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(app.out)
	root.SetErr(app.errOut)
	root.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		if commandSkipsStartupCleanup(cmd) {
			return
		}
		CleanupExpiredEnvironmentCaches(app.errOut)
	}
	root.PersistentFlags().StringVar(&app.configPath, "config", "", "config file path (overrides CAGE_CONFIG; default $XDG_CONFIG_HOME/cage/config.toml or ~/.config/cage/config.toml)")
	root.PersistentFlags().BoolVarP(&app.verbose, "verbose", "v", false, "print diagnostics to stderr")
	root.PersistentFlags().BoolVar(&app.debug, "debug", false, "print diagnostics plus extra debug details to stderr")

	root.AddCommand(app.newGetCommand())
	root.AddCommand(app.newExecCommand())
	root.AddCommand(app.newCacheCommand())
	root.AddCommand(app.newIdentityCommand())
	root.AddCommand(app.newProviderCommand())
	root.AddCommand(app.newEnvironmentCommand())
	root.AddCommand(app.newProfileCommand())
	root.AddCommand(app.newDoctorCommand())
	root.AddCommand(newCompletionCommand(root))
	root.AddCommand(app.newManCommand(root))

	return root
}

func commandSkipsStartupCleanup(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if current.Annotations[skipStartupCleanupAnnotation] == "true" {
			return true
		}
	}
	return false
}

func markSkipsStartupCleanup(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[skipStartupCleanupAnnotation] = "true"
}

func (a *App) loadConfig() (*Config, error) {
	cfg, err := LoadConfig(a.configPath)
	if err != nil {
		return nil, err
	}
	a.verbosef("config: %s", cfg.Path)
	return cfg, nil
}

func (a *App) verbosef(format string, args ...any) {
	if !a.verbose && !a.debug {
		return
	}
	_, _ = fmt.Fprintf(a.errOut, "cage: "+format+"\n", args...)
}

func (a *App) debugf(format string, args ...any) {
	if !a.debug {
		return
	}
	_, _ = fmt.Fprintf(a.errOut, "debug: "+format+"\n", args...)
}

func (a *App) warnf(format string, args ...any) {
	_, _ = fmt.Fprintf(a.errOut, "cage: warning: %s\n", Redact(fmt.Sprintf(format, args...)))
}

func requireMacOS() error {
	if runtime.GOOS != "darwin" {
		return errors.New("cage currently supports macOS only")
	}
	return nil
}

func newCompletionCommand(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long:  "Generate shell completion scripts. Source the output from your shell startup file or install it in the appropriate completions directory.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(cmd.OutOrStdout(), true)
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return root.GenPowerShellCompletion(cmd.OutOrStdout())
			default:
				return fmt.Errorf("unsupported shell %q", args[0])
			}
		},
	}
	return cmd
}

func (a *App) newManCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "man DIR",
		Short: "Generate man pages",
		Long:  "Generate roff man pages for cage and its subcommands into DIR.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := os.MkdirAll(args[0], 0o750); err != nil {
				return err
			}
			header := &doc.GenManHeader{
				Title:   "CAGE",
				Section: "1",
				Source:  "cage " + a.version,
				Manual:  "Cage Manual",
			}
			return doc.GenManTree(root, header, args[0])
		},
	}
}
