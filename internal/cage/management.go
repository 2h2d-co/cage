package cage

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const (
	managementStatusOK              = "ok"
	managementStatusMissingFile     = "missing-file"
	managementStatusPermissionError = "permission-error"
	managementStatusMissingRef      = "missing-reference"
	managementStatusUnreadable      = "unreadable"
	managementStatusInvalidFile     = "invalid-file"
)

func (a *App) newEnvironmentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "environment",
		Aliases: []string{"env", "environments"},
		Short:   "Manage configured environments",
		Long:    "Manage cage 1Password Environment config entries.",
	}
	cmd.AddCommand(a.newEnvironmentCreateCommand())
	cmd.AddCommand(a.newEnvironmentCacheCommand())
	cmd.AddCommand(a.newEnvironmentListCommand())
	cmd.AddCommand(a.newEnvironmentInspectCommand())
	cmd.AddCommand(a.newEnvironmentDeleteCommand())
	return cmd
}

func (a *App) newEnvironmentCreateCommand() *cobra.Command {
	var providerName string
	var uuid string
	var cacheTTL string
	var cacheIdentity string
	var yes bool

	cmd := &cobra.Command{
		Use:   "create NAME --provider PROVIDER --uuid UUID",
		Short: "Create a configured 1Password Environment",
		Long:  "Create a 1Password Environment config entry and update [environments] in config.toml.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			name := args[0]
			if err := ValidateCreatedName("environment", name); err != nil {
				return err
			}
			if providerName == "" {
				return errors.New("--provider is required")
			}
			if uuid == "" {
				return errors.New("--uuid is required")
			}

			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			provider, ok := cfg.Providers[providerName]
			if !ok {
				return fmt.Errorf("unknown provider %q", providerName)
			}
			if provider.Type != ProviderType1Password {
				return fmt.Errorf("provider %q has type %q, not %q", providerName, provider.Type, ProviderType1Password)
			}
			var cache *EnvironmentCacheConfig
			if cacheTTL != "" || cacheIdentity != "" {
				if cacheTTL == "" {
					return errors.New("--cache-ttl is required when --cache-identity is set")
				}
				if cacheIdentity == "" {
					return errors.New("--cache-identity is required when --cache-ttl is set")
				}
				if _, err := parseCacheTTL(cacheTTL); err != nil {
					return fmt.Errorf("--cache-ttl: %w", err)
				}
				if _, ok := cfg.Identities[cacheIdentity]; !ok {
					return fmt.Errorf("unknown cache identity %q", cacheIdentity)
				}
				cache = &EnvironmentCacheConfig{TTL: cacheTTL, Identity: cacheIdentity}
			}
			if err := a.confirmEnvironmentOverwrite(cfg, name, yes); err != nil {
				return err
			}

			cfg.Environments[name] = EnvironmentConfig{Type: EnvironmentType1Password, Provider: providerName, UUID: uuid, Cache: cache}
			if err := cfg.Write(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.out, "created environment %s\n", name)
			return err
		},
	}
	cmd.Flags().StringVar(&providerName, "provider", "", "configured 1Password provider used to load the Environment")
	cmd.Flags().StringVar(&uuid, "uuid", "", "1Password Environment UUID")
	cmd.Flags().StringVar(&cacheTTL, "cache-ttl", "", "enable encrypted caching with this Go duration TTL, for example 15m or 1h")
	cmd.Flags().StringVar(&cacheIdentity, "cache-identity", "", "configured identity used to encrypt cached Environment values")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "answer yes to overwrite confirmations")
	return cmd
}

func (a *App) newEnvironmentListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured environments",
		Long:  "List cage-configured 1Password Environments without resolving secret values.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			return a.listConfiguredEnvironments(cfg)
		},
	}
	markSkipsStartupCleanup(cmd)
	return cmd
}

func (a *App) newEnvironmentInspectCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect NAME",
		Short: "Inspect a configured environment",
		Long:  "Show non-secret metadata and wiring for one configured 1Password Environment without resolving secret values.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			return a.inspectConfiguredEnvironment(cfg, args[0])
		},
	}
	markSkipsStartupCleanup(cmd)
	return cmd
}

func (a *App) newEnvironmentDeleteCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a configured environment",
		Long:  "Delete a cage Environment config entry after confirmation.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			name := args[0]
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			if _, ok := cfg.Environments[name]; !ok {
				return fmt.Errorf("unknown environment %q", name)
			}

			references := []string{}
			for _, profileName := range sortedMapKeys(cfg.Profiles) {
				profile := cfg.Profiles[profileName]
				for _, environmentName := range profile.Environments {
					if environmentName == name {
						references = append(references, profileName)
						break
					}
				}
			}
			if len(references) > 0 {
				return fmt.Errorf("environment %q is referenced by profiles: %s", name, strings.Join(references, ", "))
			}

			ok, err := confirm(fmt.Sprintf("Delete environment %q?", name), yes)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("delete cancelled")
			}

			delete(cfg.Environments, name)
			if err := cfg.Write(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.out, "deleted environment %s\n", name)
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "answer yes to delete confirmation")
	return cmd
}

func (a *App) newProfileCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "profile",
		Aliases: []string{"profiles"},
		Short:   "Manage profiles",
		Long:    "Manage flat cage profiles that reference configured environments.",
	}
	cmd.AddCommand(a.newProfileCreateCommand())
	cmd.AddCommand(a.newProfileListCommand())
	cmd.AddCommand(a.newProfileDeleteCommand())
	return cmd
}

func (a *App) newProfileCreateCommand() *cobra.Command {
	var environmentsValue string
	var yes bool

	cmd := &cobra.Command{
		Use:   "create NAME --environments ENV[,ENV...]",
		Short: "Create a profile",
		Long:  "Create a flat profile from configured environments and update [profiles] in config.toml.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			name := args[0]
			if err := ValidateCreatedName("profile", name); err != nil {
				return err
			}
			environments := parseCommaList(environmentsValue)
			if len(environments) == 0 {
				return errors.New("--environments is required")
			}

			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			for _, environmentName := range environments {
				if _, ok := cfg.Environments[environmentName]; !ok {
					return fmt.Errorf("unknown environment %q", environmentName)
				}
			}
			if err := a.confirmProfileOverwrite(cfg, name, yes); err != nil {
				return err
			}

			cfg.Profiles[name] = ProfileConfig{Environments: environments}
			if err := cfg.Write(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.out, "created profile %s\n", name)
			return err
		},
	}
	cmd.Flags().StringVar(&environmentsValue, "environments", "", "comma-separated configured environment names")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "answer yes to overwrite confirmations")
	return cmd
}

func (a *App) newProfileListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List profiles",
		Long:  "List cage-configured profiles without resolving secret values.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			return a.listConfiguredProfiles(cfg)
		},
	}
	markSkipsStartupCleanup(cmd)
	return cmd
}

func (a *App) newProfileDeleteCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a profile",
		Long:  "Delete a cage profile after confirmation.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			name := args[0]
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			if _, ok := cfg.Profiles[name]; !ok {
				return fmt.Errorf("unknown profile %q", name)
			}

			ok, err := confirm(fmt.Sprintf("Delete profile %q?", name), yes)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("delete cancelled")
			}

			delete(cfg.Profiles, name)
			if err := cfg.Write(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.out, "deleted profile %s\n", name)
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "answer yes to delete confirmation")
	return cmd
}

func (a *App) confirmEnvironmentOverwrite(cfg *Config, name string, yes bool) error {
	if _, configured := cfg.Environments[name]; !configured {
		return nil
	}
	ok, err := confirm(fmt.Sprintf("Overwrite environment %q?", name), yes)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("create cancelled")
	}
	return nil
}

func (a *App) confirmProfileOverwrite(cfg *Config, name string, yes bool) error {
	if _, configured := cfg.Profiles[name]; !configured {
		return nil
	}
	ok, err := confirm(fmt.Sprintf("Overwrite profile %q?", name), yes)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("create cancelled")
	}
	return nil
}

func (a *App) listConfiguredEnvironments(cfg *Config) error {
	if _, err := fmt.Fprintln(a.out, "Configured environments:"); err != nil {
		return err
	}
	count := 0
	for _, name := range sortedMapKeys(cfg.Environments) {
		environment := cfg.Environments[name]
		count++
		providerStatus := referenceStatus(cfg.Providers, environment.Provider)
		cacheState, cacheTTL, cacheIdentity, cacheIdentityStatus := environmentCacheMetadata(cfg, environment)
		status := managementStatusOK
		if providerStatus == "missing" || cacheIdentityStatus == "missing" {
			status = managementStatusMissingRef
		}
		if _, err := fmt.Fprintf(
			a.out,
			"  %s\ttype=%s\tprovider=%s\tprovider-status=%s\tuuid=%s\tcache=%s\tcache-ttl=%s\tcache-identity=%s\tcache-identity-status=%s\tstatus=%s\n",
			name,
			environment.Type,
			environment.Provider,
			providerStatus,
			environment.UUID,
			cacheState,
			cacheTTL,
			cacheIdentity,
			cacheIdentityStatus,
			status,
		); err != nil {
			return err
		}
	}
	if count == 0 {
		_, err := fmt.Fprintln(a.out, "  (none)")
		return err
	}
	return nil
}

func (a *App) inspectConfiguredEnvironment(cfg *Config, name string) error {
	environment, ok := cfg.Environments[name]
	if !ok {
		return fmt.Errorf("unknown environment %q", name)
	}

	providerStatus := referenceStatus(cfg.Providers, environment.Provider)
	providerType := "-"
	providerIdentity := "-"
	providerIdentityStatus := "-"
	providerFile := "-"
	providerFileStatus := "-"
	providerFileIssueStatus := managementStatusOK
	if provider, ok := cfg.Providers[environment.Provider]; ok {
		providerType = provider.Type
		providerIdentity = provider.Identity
		providerIdentityStatus = referenceStatus(cfg.Identities, provider.Identity)
		providerFile = provider.File
		providerFileStatus, providerFileIssueStatus = inspectProviderFileMetadata(cfg.ResolveFile(provider.File))
	}

	cacheState, cacheTTL, cacheIdentity, cacheIdentityStatus := environmentCacheMetadata(cfg, environment)
	referencedByProfiles := profileReferences(cfg, name)
	status := managementStatusOK
	if providerStatus == "missing" || providerIdentityStatus == "missing" || cacheIdentityStatus == "missing" {
		status = managementStatusMissingRef
	} else if providerFileIssueStatus != managementStatusOK {
		status = providerFileIssueStatus
	}

	if _, err := fmt.Fprintf(a.out, "Environment %s:\n", name); err != nil {
		return err
	}
	fields := [][2]string{
		{"name", name},
		{"type", environment.Type},
		{"uuid", environment.UUID},
		{"provider", environment.Provider},
		{"provider-status", providerStatus},
		{"provider-type", providerType},
		{"provider-identity", providerIdentity},
		{"provider-identity-status", providerIdentityStatus},
		{"provider-file", providerFile},
		{"provider-file-status", providerFileStatus},
		{"cache", cacheState},
		{"cache-ttl", cacheTTL},
		{"cache-identity", cacheIdentity},
		{"cache-identity-status", cacheIdentityStatus},
		{"referenced-by-profiles", referencedByProfiles},
		{"status", status},
	}
	for _, field := range fields {
		if _, err := fmt.Fprintf(a.out, "  %s=%s\n", field[0], field[1]); err != nil {
			return err
		}
	}
	return nil
}

func environmentCacheMetadata(cfg *Config, environment EnvironmentConfig) (string, string, string, string) {
	if environment.Cache == nil {
		return "disabled", "-", "-", "-"
	}
	return "enabled", environment.Cache.TTL, environment.Cache.Identity, referenceStatus(cfg.Identities, environment.Cache.Identity)
}

func profileReferences(cfg *Config, environmentName string) string {
	references := []string{}
	for _, profileName := range sortedMapKeys(cfg.Profiles) {
		for _, profileEnvironmentName := range cfg.Profiles[profileName].Environments {
			if profileEnvironmentName == environmentName {
				references = append(references, profileName)
				break
			}
		}
	}
	if len(references) == 0 {
		return "-"
	}
	return strings.Join(references, ",")
}

func referenceStatus[V any](values map[string]V, name string) string {
	if _, ok := values[name]; ok {
		return "present"
	}
	return "missing"
}

func inspectProviderFileMetadata(path string) (string, string) {
	if exists, err := fileExists(path); err != nil {
		return "unreadable", managementStatusUnreadable
	} else if !exists {
		return "missing", managementStatusMissingFile
	}
	if err := ensurePrivateFile(path, "provider file"); err != nil {
		return "permission-error", managementStatusPermissionError
	}
	if err := checkAgeCiphertextFile(path); err != nil {
		return "invalid-ciphertext", managementStatusInvalidFile
	}
	return "age-ciphertext", managementStatusOK
}

func (a *App) listConfiguredProfiles(cfg *Config) error {
	if _, err := fmt.Fprintln(a.out, "Configured profiles:"); err != nil {
		return err
	}
	count := 0
	for _, name := range sortedMapKeys(cfg.Profiles) {
		profile := cfg.Profiles[name]
		count++
		environmentNames := strings.Join(profile.Environments, ",")
		if environmentNames == "" {
			environmentNames = "-"
		}
		missing := []string{}
		for _, environmentName := range profile.Environments {
			if _, ok := cfg.Environments[environmentName]; !ok {
				missing = append(missing, environmentName)
			}
		}
		missingEnvironments := "-"
		status := managementStatusOK
		if len(missing) > 0 {
			missingEnvironments = strings.Join(missing, ",")
			status = managementStatusMissingRef
		}
		if _, err := fmt.Fprintf(a.out, "  %s\tenvironments=%s\tmissing-environments=%s\tstatus=%s\n", name, environmentNames, missingEnvironments, status); err != nil {
			return err
		}
	}
	if count == 0 {
		_, err := fmt.Fprintln(a.out, "  (none)")
		return err
	}
	return nil
}
