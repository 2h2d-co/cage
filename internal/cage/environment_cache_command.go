package cage

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func (a *App) newEnvironmentCacheCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cache",
		Aliases: []string{"caches"},
		Short:   "Manage environment cache settings",
		Long:    "Manage encrypted cache settings on configured 1Password Environment entries.",
	}
	cmd.AddCommand(a.newEnvironmentCacheSetCommand())
	cmd.AddCommand(a.newEnvironmentCacheUnsetCommand())
	return cmd
}

func (a *App) newEnvironmentCacheSetCommand() *cobra.Command {
	var ttl string
	var identityName string
	var overwrite bool
	cmd := &cobra.Command{
		Use:     "set NAME --ttl DURATION --identity IDENTITY [--overwrite]",
		Aliases: []string{"enable", "configure"},
		Short:   "Set encrypted cache settings for an environment",
		Long:    "Set encrypted cache settings for a configured 1Password Environment. Use --overwrite to replace existing cache settings.",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			if ttl == "" {
				return errors.New("--ttl is required")
			}
			if identityName == "" {
				return errors.New("--identity is required")
			}

			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			environmentName := args[0]
			environment, ok := cfg.Environments[environmentName]
			if !ok {
				return fmt.Errorf("unknown environment %q", environmentName)
			}
			if environment.Type != EnvironmentType1Password {
				return fmt.Errorf("environment %q has unsupported type %q", environmentName, environment.Type)
			}
			if environment.Cache != nil && !overwrite {
				return fmt.Errorf("cache is already configured for environment %q; use --overwrite to replace it", environmentName)
			}
			if _, err := parseCacheTTL(ttl); err != nil {
				return fmt.Errorf("--ttl: %w", err)
			}
			if _, ok := cfg.Identities[identityName]; !ok {
				return fmt.Errorf("unknown cache identity %q", identityName)
			}

			environment.Cache = &EnvironmentCacheConfig{TTL: ttl, Identity: identityName}
			cfg.Environments[environmentName] = environment
			if err := cfg.Write(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.out, "set cache for environment %s\n", environmentName)
			return err
		},
	}
	cmd.Flags().StringVar(&ttl, "ttl", "", "positive Go duration cache TTL, for example 15m or 1h")
	cmd.Flags().StringVar(&identityName, "identity", "", "configured identity used to encrypt cached Environment values")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace existing cache settings")
	return cmd
}

func (a *App) newEnvironmentCacheUnsetCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "unset NAME",
		Aliases: []string{"disable", "remove"},
		Short:   "Remove encrypted cache settings from an environment",
		Long:    "Remove encrypted cache settings from a configured 1Password Environment. Existing cache data can be removed with cage cache clear NAME.",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			environmentName := args[0]
			environment, ok := cfg.Environments[environmentName]
			if !ok {
				return fmt.Errorf("unknown environment %q", environmentName)
			}
			if environment.Type != EnvironmentType1Password {
				return fmt.Errorf("environment %q has unsupported type %q", environmentName, environment.Type)
			}
			if environment.Cache == nil {
				_, err = fmt.Fprintf(a.out, "cache already unset for environment %s\n", environmentName)
				return err
			}
			environment.Cache = nil
			cfg.Environments[environmentName] = environment
			if err := cfg.Write(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.out, "unset cache for environment %s\n", environmentName)
			return err
		},
	}
}
