package cage

import (
	"context"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

func (a *App) newExecCommand() *cobra.Command {
	var profiles string
	var environments string

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
			variables, err := a.resolveVariables(context.Background(), cfg, selection)
			if err != nil {
				return err
			}

			path, err := osexec.LookPath(args[0])
			if err != nil {
				return fmt.Errorf("find command %q: %w", args[0], err)
			}
			a.debugf("exec: %s", path)
			return unix.Exec(path, args, childEnvironment(variables))
		},
	}
	addSelectionFlags(cmd, &profiles, &environments)
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func childEnvironment(overrides map[string]string) []string {
	values := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "OP_SERVICE_ACCOUNT_TOKEN" {
			continue
		}
		values[key] = value
	}
	for key, value := range overrides {
		if key == "OP_SERVICE_ACCOUNT_TOKEN" {
			continue
		}
		values[key] = value
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}
