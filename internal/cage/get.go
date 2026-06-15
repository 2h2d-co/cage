package cage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

func (a *App) newGetCommand() *cobra.Command {
	var profiles string
	var environments string
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "get [flags] ENV_VAR|*",
		Short: "Print resolved environment variables",
		Long:  "Resolve selected cage profiles and environments, then print one environment variable value or all variables with '*'. Flags must come before ENV_VAR.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
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
			return a.printGetResult(args[0], variables, jsonOutput)
		},
	}
	addSelectionFlags(cmd, &profiles, &environments)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON object output")
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func (a *App) printGetResult(name string, variables map[string]string, jsonOutput bool) error {
	selected := map[string]string{}
	if name == "*" {
		for key, value := range variables {
			selected[key] = value
		}
	} else {
		value, ok := variables[name]
		if !ok {
			return ExitError{Code: 1, Err: fmt.Errorf("environment variable %q is not set", name)}
		}
		selected[name] = value
	}

	for key := range selected {
		if err := validateEnvironmentVariableName(key); err != nil {
			return fmt.Errorf("resolved environment: %w", err)
		}
	}

	if jsonOutput {
		encoder := json.NewEncoder(a.out)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(selected)
	}

	if name != "*" {
		_, err := fmt.Fprintln(a.out, selected[name])
		return err
	}
	keys := make([]string, 0, len(selected))
	for key := range selected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := fmt.Fprintf(a.out, "%s=%s\n", key, selected[key]); err != nil {
			return err
		}
	}
	return nil
}
