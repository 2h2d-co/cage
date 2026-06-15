package cage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	onepassword "github.com/1password/onepassword-sdk-go"
	"github.com/spf13/cobra"
)

// Selection contains profile and environment names selected for resolution.
type Selection struct {
	Profiles     []string
	Environments []string
}

func addSelectionFlags(cmd *cobra.Command, profiles *string, environments *string) {
	cmd.Flags().StringVar(profiles, "profiles", "", "comma-separated profile names; overrides CAGE_PROFILES")
	cmd.Flags().StringVar(environments, "environments", "", "comma-separated environment names; overrides CAGE_ENVIRONMENTS")
}

func selectionFromCommand(cmd *cobra.Command, profilesFlag, environmentsFlag string) Selection {
	profilesValue := profilesFlag
	if !cmd.Flags().Changed("profiles") {
		profilesValue = os.Getenv("CAGE_PROFILES")
	}
	environmentsValue := environmentsFlag
	if !cmd.Flags().Changed("environments") {
		environmentsValue = os.Getenv("CAGE_ENVIRONMENTS")
	}
	return Selection{
		Profiles:     parseCommaList(profilesValue),
		Environments: parseCommaList(environmentsValue),
	}
}

func parseCommaList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func (s Selection) environmentOrder(cfg *Config) ([]string, error) {
	if len(s.Profiles) == 0 && len(s.Environments) == 0 {
		return nil, fmt.Errorf("select at least one profile or environment with --profiles/--environments or CAGE_PROFILES/CAGE_ENVIRONMENTS")
	}

	ordered := []string{}
	for _, profileName := range s.Profiles {
		profile, ok := cfg.Profiles[profileName]
		if !ok {
			return nil, fmt.Errorf("unknown profile %q", profileName)
		}
		ordered = append(ordered, profile.Environments...)
	}
	for _, environmentName := range s.Environments {
		if _, ok := cfg.Environments[environmentName]; !ok {
			return nil, fmt.Errorf("unknown environment %q", environmentName)
		}
		ordered = append(ordered, environmentName)
	}
	return ordered, nil
}

func (a *App) resolveVariables(ctx context.Context, cfg *Config, selection Selection) (map[string]string, error) {
	if err := cfg.validateReferences(); err != nil {
		return nil, err
	}
	ordered, err := selection.environmentOrder(cfg)
	if err != nil {
		return nil, err
	}

	resolved := map[string]string{}
	for _, environmentName := range ordered {
		environment := cfg.Environments[environmentName]
		if environment.Type != EnvironmentType1Password {
			return nil, fmt.Errorf("environment %q has unsupported type %q", environmentName, environment.Type)
		}
		a.debugf("loading environment %s", environmentName)

		token, err := decryptProviderToken(cfg, environment.Provider)
		if err != nil {
			return nil, fmt.Errorf("provider %q for environment %q: %w", environment.Provider, environmentName, err)
		}

		client, err := onepassword.NewClient(ctx,
			onepassword.WithServiceAccountToken(token),
			onepassword.WithIntegrationInfo("cage", a.version),
		)
		if err != nil {
			return nil, fmt.Errorf("initialize 1Password SDK client for environment %q: %w", environmentName, err)
		}

		response, err := client.Environments().GetVariables(ctx, environment.UUID)
		if err != nil {
			return nil, fmt.Errorf("fetch 1Password environment %q: %w", environmentName, err)
		}

		masked := 0
		for _, variable := range response.Variables {
			if variable.Masked {
				masked++
			}
			resolved[variable.Name] = variable.Value
		}
		a.debugf("loaded environment %s: %d variables (%d masked)", environmentName, len(response.Variables), masked)
	}
	return resolved, nil
}

func decryptProviderToken(cfg *Config, providerName string) (string, error) {
	provider, ok := cfg.Providers[providerName]
	if !ok {
		return "", fmt.Errorf("unknown provider %q", providerName)
	}
	if provider.Type != ProviderType1Password {
		return "", fmt.Errorf("unsupported provider type %q", provider.Type)
	}
	identity, ok := cfg.Identities[provider.Identity]
	if !ok {
		return "", fmt.Errorf("provider references unknown identity %q", provider.Identity)
	}

	ciphertext, err := os.ReadFile(filepath.Clean(cfg.ResolveFile(provider.File)))
	if err != nil {
		return "", fmt.Errorf("read encrypted provider file: %w", err)
	}
	switch identity.Type {
	case IdentityTypeSecureEnclave:
		notifyActionNeeded(fmt.Sprintf("approve Secure Enclave access for identity %q", provider.Identity))
	case IdentityTypeYubiKey:
		notifyActionNeeded(fmt.Sprintf("touch the YubiKey for identity %q when it blinks", provider.Identity))
	}
	plaintext, err := decryptWithIdentityFile(ciphertext, cfg.ResolveFile(identity.File))
	if err != nil {
		return "", fmt.Errorf("decrypt encrypted provider file: %w", err)
	}
	token := string(trimTrailingNewlines(plaintext))
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("decrypted provider token is empty")
	}
	return token, nil
}
