package cage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
)

const (
	// IdentityTypeBasic is the config type for native age identities.
	IdentityTypeBasic = "basic"
	// IdentityTypeYubiKey is the config type for age-plugin-yubikey identities.
	IdentityTypeYubiKey = "yubikey"
	// IdentityTypeSecureEnclave is the config type for age-plugin-se identities.
	IdentityTypeSecureEnclave = "secure-enclave"
	// ProviderType1Password is the config type for 1Password service account providers.
	ProviderType1Password = "1password"
	// EnvironmentType1Password is the config type for 1Password Environments.
	EnvironmentType1Password = "1password-environment"
)

var createdNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Config is the full cage config file. Relative file paths are resolved from Dir.
type Config struct {
	Path         string
	Dir          string
	Exists       bool
	Identities   map[string]IdentityConfig
	Providers    map[string]ProviderConfig
	Environments map[string]EnvironmentConfig
	Profiles     map[string]ProfileConfig
}

// IdentityConfig describes one configured age identity file.
type IdentityConfig struct {
	Type string
	File string
}

// ProviderConfig describes one encrypted provider credential.
type ProviderConfig struct {
	Type     string
	Identity string
	File     string
}

// EnvironmentConfig describes one loadable secret environment.
type EnvironmentConfig struct {
	Type     string
	Provider string
	UUID     string
}

// ProfileConfig describes a flat list of environments.
type ProfileConfig struct {
	Environments []string
}

// DefaultConfigDir returns the global cage config directory.
func DefaultConfigDir() (string, error) {
	if xdgConfigHome := os.Getenv("XDG_CONFIG_HOME"); xdgConfigHome != "" {
		return filepath.Join(xdgConfigHome, "cage"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "cage"), nil
}

// DefaultConfigPath returns the global cage config path.
func DefaultConfigPath() (string, error) {
	dir, err := DefaultConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// LoadConfig loads and validates a cage config file.
func LoadConfig(path string) (*Config, error) {
	if path == "" {
		path = os.Getenv("CAGE_CONFIG")
	}
	if path == "" {
		defaultPath, err := DefaultConfigPath()
		if err != nil {
			return nil, err
		}
		path = defaultPath
	}

	expanded, err := expandPath(path)
	if err != nil {
		return nil, err
	}
	cfg := emptyConfig(expanded)
	if err := ensurePrivateDirIfExists(cfg.Dir, "config directory"); err != nil {
		return nil, err
	}

	info, err := os.Stat(filepath.Clean(expanded))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat config %s: %w", expanded, err)
	}
	if err := ensurePrivateInfo(expanded, "config file", info, false, 0o600); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Clean(expanded))
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", expanded, err)
	}
	cfg.Exists = true
	if strings.TrimSpace(string(data)) == "" {
		return cfg, nil
	}

	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", expanded, err)
	}
	if err := cfg.loadRaw(raw); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", expanded, err)
	}
	return cfg, nil
}

func emptyConfig(path string) *Config {
	return &Config{
		Path:         path,
		Dir:          filepath.Dir(path),
		Identities:   map[string]IdentityConfig{},
		Providers:    map[string]ProviderConfig{},
		Environments: map[string]EnvironmentConfig{},
		Profiles:     map[string]ProfileConfig{},
	}
}

func expandPath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand %q: %w", path, err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// ResolveFile resolves a validated config-relative file path.
func (c *Config) ResolveFile(file string) string {
	return filepath.Clean(filepath.Join(c.Dir, file))
}

func (c *Config) validateConfigFilePath(field string, file string) error {
	if strings.ContainsRune(file, '\x00') {
		return fmt.Errorf("%s must not contain NUL", field)
	}
	if filepath.IsAbs(file) {
		return fmt.Errorf("%s must be a relative path", field)
	}
	cleaned := filepath.Clean(file)
	if cleaned == "." {
		return fmt.Errorf("%s must name a file", field)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s must stay within config directory", field)
	}

	configDir, err := filepath.Abs(c.Dir)
	if err != nil {
		return fmt.Errorf("resolve config directory %s: %w", c.Dir, err)
	}
	resolved, err := filepath.Abs(c.ResolveFile(cleaned))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", field, err)
	}
	relative, err := filepath.Rel(configDir, resolved)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", field, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("%s must stay within config directory", field)
	}
	return nil
}

// ValidateCreatedName validates names used for cage-created files.
func ValidateCreatedName(kind, name string) error {
	if name == "" {
		return fmt.Errorf("%s name is required", kind)
	}
	if !createdNamePattern.MatchString(name) {
		return fmt.Errorf("invalid %s name %q: use only letters, numbers, underscore, and dash", kind, name)
	}
	return nil
}

func (c *Config) loadRaw(raw map[string]any) error {
	allowed := map[string]bool{
		"identities":   true,
		"providers":    true,
		"environments": true,
		"profiles":     true,
	}
	for key := range raw {
		if !allowed[key] {
			return fmt.Errorf("unsupported top-level table %q", key)
		}
	}

	m, err := rawTable(raw, "identities")
	if err != nil {
		return err
	}
	for name, value := range m {
		entryName := "identities." + name
		entry, err := tableValue(entryName, value)
		if err != nil {
			return err
		}
		if err := ensureAllowedKeys(entryName, entry, "type", "file"); err != nil {
			return err
		}
		identityType, err := stringField(entryName, entry, "type")
		if err != nil {
			return err
		}
		file, err := stringField(entryName, entry, "file")
		if err != nil {
			return err
		}
		identity := IdentityConfig{Type: identityType, File: file}
		if identity.Type != IdentityTypeBasic && identity.Type != IdentityTypeYubiKey && identity.Type != IdentityTypeSecureEnclave {
			return fmt.Errorf("%s has unsupported type %q", entryName, identity.Type)
		}
		if identity.File == "" {
			return fmt.Errorf("%s file is required", entryName)
		}
		if err := c.validateConfigFilePath(entryName+".file", identity.File); err != nil {
			return err
		}
		c.Identities[name] = identity
	}

	m, err = rawTable(raw, "providers")
	if err != nil {
		return err
	}
	for name, value := range m {
		entryName := "providers." + name
		entry, err := tableValue(entryName, value)
		if err != nil {
			return err
		}
		if err := ensureAllowedKeys(entryName, entry, "type", "identity", "file"); err != nil {
			return err
		}
		providerType, err := stringField(entryName, entry, "type")
		if err != nil {
			return err
		}
		identity, err := stringField(entryName, entry, "identity")
		if err != nil {
			return err
		}
		file, err := stringField(entryName, entry, "file")
		if err != nil {
			return err
		}
		provider := ProviderConfig{Type: providerType, Identity: identity, File: file}
		if provider.Type != ProviderType1Password {
			return fmt.Errorf("%s has unsupported type %q", entryName, provider.Type)
		}
		if provider.Identity == "" {
			return fmt.Errorf("%s identity is required", entryName)
		}
		if provider.File == "" {
			return fmt.Errorf("%s file is required", entryName)
		}
		if err := c.validateConfigFilePath(entryName+".file", provider.File); err != nil {
			return err
		}
		c.Providers[name] = provider
	}

	m, err = rawTable(raw, "environments")
	if err != nil {
		return err
	}
	for name, value := range m {
		entryName := "environments." + name
		entry, err := tableValue(entryName, value)
		if err != nil {
			return err
		}
		if err := ensureAllowedKeys(entryName, entry, "type", "provider", "uuid"); err != nil {
			return err
		}
		environmentType, err := stringField(entryName, entry, "type")
		if err != nil {
			return err
		}
		provider, err := stringField(entryName, entry, "provider")
		if err != nil {
			return err
		}
		uuid, err := stringField(entryName, entry, "uuid")
		if err != nil {
			return err
		}
		environment := EnvironmentConfig{Type: environmentType, Provider: provider, UUID: uuid}
		if environment.Type != EnvironmentType1Password {
			return fmt.Errorf("%s has unsupported type %q", entryName, environment.Type)
		}
		if environment.Provider == "" {
			return fmt.Errorf("%s provider is required", entryName)
		}
		if environment.UUID == "" {
			return fmt.Errorf("%s uuid is required", entryName)
		}
		c.Environments[name] = environment
	}

	m, err = rawTable(raw, "profiles")
	if err != nil {
		return err
	}
	for name, value := range m {
		environments, err := profileEnvironments("profiles."+name, value)
		if err != nil {
			return err
		}
		c.Profiles[name] = ProfileConfig{Environments: environments}
	}

	return nil
}

func (c *Config) validateReferences() error {
	for name, provider := range c.Providers {
		if _, ok := c.Identities[provider.Identity]; !ok {
			return fmt.Errorf("providers.%s references unknown identity %q", name, provider.Identity)
		}
	}
	for name, environment := range c.Environments {
		if _, ok := c.Providers[environment.Provider]; !ok {
			return fmt.Errorf("environments.%s references unknown provider %q", name, environment.Provider)
		}
	}
	for name, profile := range c.Profiles {
		for _, environment := range profile.Environments {
			if _, ok := c.Environments[environment]; !ok {
				return fmt.Errorf("profiles.%s references unknown environment %q", name, environment)
			}
		}
	}
	return nil
}

func rawTable(raw map[string]any, key string) (map[string]any, error) {
	value, ok := raw[key]
	if !ok {
		return map[string]any{}, nil
	}
	return tableValue(key, value)
}

func tableValue(name string, value any) (map[string]any, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a table", name)
	}
	return m, nil
}

func ensureAllowedKeys(name string, m map[string]any, allowedKeys ...string) error {
	allowed := map[string]bool{}
	for _, key := range allowedKeys {
		allowed[key] = true
	}
	for key := range m {
		if !allowed[key] {
			return fmt.Errorf("%s has unsupported key %q", name, key)
		}
	}
	return nil
}

func stringField(name string, m map[string]any, key string) (string, error) {
	value, ok := m[key]
	if !ok {
		return "", nil
	}
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s.%s must be a string", name, key)
	}
	return s, nil
}

func profileEnvironments(name string, value any) ([]string, error) {
	switch v := value.(type) {
	case []any:
		return stringArray(name, v)
	case map[string]any:
		if err := ensureAllowedKeys(name, v, "environments"); err != nil {
			return nil, err
		}
		environments, ok := v["environments"]
		if !ok {
			return nil, fmt.Errorf("%s environments is required", name)
		}
		items, ok := environments.([]any)
		if !ok {
			return nil, fmt.Errorf("%s environments must be an array of strings", name)
		}
		return stringArray(name+".environments", items)
	default:
		return nil, fmt.Errorf("%s must be an array of environments or a table with environments", name)
	}
}

func stringArray(name string, items []any) ([]string, error) {
	values := make([]string, 0, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be a string", name, i)
		}
		values = append(values, s)
	}
	return values, nil
}

func (c *Config) Write() error {
	var b strings.Builder
	b.WriteString("# cage global config\n")
	b.WriteString("# https://github.com/2h2d-co/cage\n\n")

	if len(c.Identities) > 0 {
		b.WriteString("[identities]\n")
		for _, name := range sortedMapKeys(c.Identities) {
			identity := c.Identities[name]
			fmt.Fprintf(&b, "%s = { type = %s, file = %s }\n", tomlKey(name), tomlString(identity.Type), tomlString(identity.File))
		}
		b.WriteString("\n")
	}

	if len(c.Providers) > 0 {
		b.WriteString("[providers]\n")
		for _, name := range sortedMapKeys(c.Providers) {
			provider := c.Providers[name]
			fmt.Fprintf(&b, "%s = { type = %s, identity = %s, file = %s }\n", tomlKey(name), tomlString(provider.Type), tomlString(provider.Identity), tomlString(provider.File))
		}
		b.WriteString("\n")
	}

	if len(c.Environments) > 0 {
		b.WriteString("[environments]\n")
		for _, name := range sortedMapKeys(c.Environments) {
			environment := c.Environments[name]
			fmt.Fprintf(&b, "%s = { type = %s, provider = %s, uuid = %s }\n", tomlKey(name), tomlString(environment.Type), tomlString(environment.Provider), tomlString(environment.UUID))
		}
		b.WriteString("\n")
	}

	if len(c.Profiles) > 0 {
		b.WriteString("[profiles]\n")
		for _, name := range sortedMapKeys(c.Profiles) {
			fmt.Fprintf(&b, "%s = %s\n", tomlKey(name), tomlStringArray(c.Profiles[name].Environments))
		}
		b.WriteString("\n")
	}

	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", c.Dir, err)
	}
	if err := ensurePrivateDir(c.Dir, "config directory"); err != nil {
		return err
	}
	if err := atomicWriteFile(c.Path, []byte(b.String())); err != nil {
		return fmt.Errorf("write config %s: %w", c.Path, err)
	}
	c.Exists = true
	return nil
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func tomlKey(key string) string {
	if createdNamePattern.MatchString(key) {
		return key
	}
	return tomlString(key)
}

func tomlStringArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, tomlString(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func tomlString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == utf8.RuneError {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
