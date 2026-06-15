package cage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	onepassword "github.com/1password/onepassword-sdk-go"
	"github.com/spf13/cobra"
)

type providerTokenDecryptor func(*Config, string) ([]byte, error)

type onePasswordEnvironmentsFactory func(context.Context, []byte, string) (onepassword.EnvironmentsAPI, error)

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

	decryptToken := a.decryptProviderToken
	if decryptToken == nil {
		decryptToken = decryptProviderToken
	}
	newEnvironments := a.newOnePasswordEnvironments
	if newEnvironments == nil {
		newEnvironments = newOnePasswordEnvironments
	}

	clients := map[string]onepassword.EnvironmentsAPI{}
	for _, environmentName := range ordered {
		environment := cfg.Environments[environmentName]
		if environment.Type != EnvironmentType1Password {
			return nil, fmt.Errorf("environment %q has unsupported type %q", environmentName, environment.Type)
		}
		if _, ok := clients[environment.Provider]; ok {
			continue
		}

		token, err := decryptToken(cfg, environment.Provider)
		if err != nil {
			return nil, fmt.Errorf("provider %q for environment %q: %w", environment.Provider, environmentName, err)
		}
		client, err := newEnvironments(ctx, token, a.version)
		zeroBytes(token)
		if err != nil {
			return nil, fmt.Errorf("initialize 1Password SDK client for provider %q: %w", environment.Provider, err)
		}
		clients[environment.Provider] = client
	}

	results := make([]environmentLoadResult, len(ordered))
	var wg sync.WaitGroup
	for i, environmentName := range ordered {
		environment := cfg.Environments[environmentName]
		client := clients[environment.Provider]
		results[i].name = environmentName
		a.debugf("loading environment %s", environmentName)

		wg.Add(1)
		go func(index int, name, uuid string, client onepassword.EnvironmentsAPI) {
			defer wg.Done()
			response, err := client.GetVariables(ctx, uuid)
			if err != nil {
				results[index].err = fmt.Errorf("fetch 1Password environment %q: %w", name, err)
				return
			}

			masked := 0
			for _, variable := range response.Variables {
				if err := validateEnvironmentVariableName(variable.Name); err != nil {
					results[index].err = fmt.Errorf("environment %q returned invalid variable name: %w", name, err)
					return
				}
				if variable.Masked {
					masked++
				}
			}
			results[index].variables = response.Variables
			results[index].masked = masked
		}(i, environmentName, environment.UUID, client)
	}
	wg.Wait()

	resolved := map[string]string{}
	for _, result := range results {
		if result.err != nil {
			return nil, result.err
		}
		for _, variable := range result.variables {
			resolved[variable.Name] = variable.Value
		}
		a.debugf("loaded environment %s: %d variables (%d masked)", result.name, len(result.variables), result.masked)
	}
	return resolved, nil
}

type onePasswordEnvironmentClient struct {
	client       *onepassword.Client
	environments onepassword.EnvironmentsAPI
}

func (c *onePasswordEnvironmentClient) GetVariables(ctx context.Context, environmentID string) (onepassword.GetVariablesResponse, error) {
	response, err := c.environments.GetVariables(ctx, environmentID)
	runtime.KeepAlive(c.client)
	return response, err
}

func newOnePasswordEnvironments(ctx context.Context, token []byte, version string) (onepassword.EnvironmentsAPI, error) {
	// The 1Password SDK currently requires a string token and retains it in its client config.
	// This is the unavoidable SDK boundary; the caller still zeros the Cage-owned []byte token.
	tokenString := string(token)
	client, err := onepassword.NewClient(ctx,
		onepassword.WithServiceAccountToken(tokenString),
		onepassword.WithIntegrationInfo("cage", version),
	)
	if err != nil {
		return nil, err
	}
	return &onePasswordEnvironmentClient{
		client:       client,
		environments: client.Environments(),
	}, nil
}

type environmentLoadResult struct {
	name      string
	variables []onepassword.EnvironmentVariable
	masked    int
	err       error
}

func validateEnvironmentVariableName(name string) error {
	if name == "" {
		return fmt.Errorf("empty environment variable name")
	}
	if strings.Contains(name, "=") {
		return fmt.Errorf("environment variable name %q contains =", name)
	}
	if strings.ContainsRune(name, '\x00') {
		return fmt.Errorf("environment variable name %q contains NUL", name)
	}
	return nil
}

// decryptProviderToken returns an owned plaintext token buffer. The caller must zero it.
func decryptProviderToken(cfg *Config, providerName string) ([]byte, error) {
	provider, ok := cfg.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", providerName)
	}
	if provider.Type != ProviderType1Password {
		return nil, fmt.Errorf("unsupported provider type %q", provider.Type)
	}
	identity, ok := cfg.Identities[provider.Identity]
	if !ok {
		return nil, fmt.Errorf("provider references unknown identity %q", provider.Identity)
	}

	providerPath := cfg.ResolveFile(provider.File)
	if err := ensurePrivateFile(providerPath, "provider file"); err != nil {
		return nil, err
	}
	ciphertext, err := os.ReadFile(filepath.Clean(providerPath))
	if err != nil {
		return nil, fmt.Errorf("read encrypted provider file: %w", err)
	}
	switch identity.Type {
	case IdentityTypeSecureEnclave:
		notifyActionNeeded(fmt.Sprintf("approve Secure Enclave access for identity %q", provider.Identity))
	case IdentityTypeYubiKey:
		identityPath := cfg.ResolveFile(identity.File)
		preNotify, err := shouldPreNotifyYubiKeyTouch(identityPath)
		if err != nil {
			return nil, err
		}
		if preNotify {
			notifyActionNeeded(fmt.Sprintf("touch the YubiKey for identity %q when it blinks", provider.Identity))
		}
	}
	plaintext, err := decryptWithIdentityFile(ciphertext, cfg.ResolveFile(identity.File))
	if err != nil {
		return nil, fmt.Errorf("decrypt encrypted provider file: %w", err)
	}
	token := trimTrailingNewlines(plaintext)
	if len(bytes.TrimSpace(token)) == 0 {
		zeroBytes(plaintext)
		return nil, fmt.Errorf("decrypted provider token is empty")
	}
	return token, nil
}

func shouldPreNotifyYubiKeyTouch(identityFile string) (bool, error) {
	metadata, ok, err := readYubiKeyIdentityActionMetadata(identityFile)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	if metadata.pinAlwaysRequired {
		return false, nil
	}
	return metadata.touchRequired, nil
}

type yubiKeyIdentityActionMetadata struct {
	pinAlwaysRequired bool
	touchRequired     bool
}

func readYubiKeyIdentityActionMetadata(identityFile string) (yubiKeyIdentityActionMetadata, bool, error) {
	if err := ensurePrivateFile(identityFile, "identity file"); err != nil {
		return yubiKeyIdentityActionMetadata{}, false, err
	}
	data, err := os.ReadFile(filepath.Clean(identityFile))
	if err != nil {
		return yubiKeyIdentityActionMetadata{}, false, fmt.Errorf("read identity file %s: %w", identityFile, err)
	}
	defer zeroBytes(data)

	var metadata yubiKeyIdentityActionMetadata
	var sawPolicy bool
	remaining := data
	for len(remaining) > 0 {
		line := remaining
		if before, after, found := bytes.Cut(remaining, []byte("\n")); found {
			line = before
			remaining = after
		} else {
			remaining = nil
		}

		comment, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("#"))
		if !ok {
			continue
		}
		key, value, ok := bytes.Cut(comment, []byte(":"))
		if !ok {
			continue
		}

		switch strings.ToLower(string(bytes.TrimSpace(key))) {
		case "pin policy":
			sawPolicy = true
			metadata.pinAlwaysRequired = policyValueIs(value, "always")
		case "touch policy":
			sawPolicy = true
			metadata.touchRequired = !policyValueIs(value, "never")
		}
	}
	return metadata, sawPolicy, nil
}

func policyValueIs(value []byte, policy string) bool {
	fields := bytes.Fields(value)
	return len(fields) > 0 && strings.EqualFold(string(fields[0]), policy)
}
