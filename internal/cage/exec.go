package cage

import (
	"context"
	"errors"
	"fmt"
	osexec "os/exec"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

func (a *App) newExecCommand() *cobra.Command {
	var profiles string
	var environments string
	var skipCache bool
	var refreshCache bool

	cmd := &cobra.Command{
		Use:   "exec [flags] -- COMMAND [ARG...]",
		Short: "Run a command with resolved environment variables",
		Long:  "Resolve selected cage profiles and environments, overlay them on the parent environment, remove OP_SERVICE_ACCOUNT_TOKEN, and replace cage with COMMAND where supported.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			if cmd.Flags().ArgsLenAtDash() == -1 {
				return errors.New("pass the child command after --")
			}
			if len(args) == 0 {
				return errors.New("no command passed after --")
			}

			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			selection := selectionFromCommand(cmd, profiles, environments)
			mode, err := cacheModeFromFlags(skipCache, refreshCache)
			if err != nil {
				return err
			}
			variables, err := a.resolveVariables(context.Background(), cfg, selection, mode)
			if err != nil {
				return err
			}

			path, err := osexec.LookPath(args[0])
			if err != nil {
				return fmt.Errorf("find command %q: %w", args[0], err)
			}
			env, err := childEnvironment(variables)
			if err != nil {
				return err
			}
			a.debugf("exec: %s", path)
			return unix.Exec(path, args, env)
		},
	}
	addSelectionFlags(cmd, &profiles, &environments)
	addCacheFlags(cmd, &skipCache, &refreshCache)
	cmd.Flags().SetInterspersed(false)
	return cmd
}
