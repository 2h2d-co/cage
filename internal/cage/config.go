package cage

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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
	Type string `toml:"type"`
	File string `toml:"file"`
}

// ProviderConfig describes one encrypted provider credential.
type ProviderConfig struct {
	Type     string `toml:"type"`
	Identity string `toml:"identity"`
	File     string `toml:"file"`
}

// EnvironmentConfig describes one loadable secret environment.
type EnvironmentConfig struct {
	Type     string `toml:"type"`
	Provider string `toml:"provider"`
	UUID     string `toml:"uuid"`
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

	info, err := os.Lstat(filepath.Clean(expanded))
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

	if err := cfg.loadGeneratedConfig(data); err != nil {
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
	if err := c.rejectSymlinkedConfigFilePath(field, cleaned); err != nil {
		return err
	}
	return nil
}

func (c *Config) rejectSymlinkedConfigFilePath(field string, cleaned string) error {
	current := c.Dir
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(filepath.Clean(current))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stat %s %s: %w", field, current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s must not contain symlink %s", field, current)
		}
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

type generatedConfigFile struct {
	Identities   map[string]IdentityConfig    `toml:"identities"`
	Providers    map[string]ProviderConfig    `toml:"providers"`
	Environments map[string]EnvironmentConfig `toml:"environments"`
	Profiles     map[string][]string          `toml:"profiles"`
}

func (c *Config) loadGeneratedConfig(data []byte) error {
	var file generatedConfigFile
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&file); err != nil {
		return configDecodeError(err)
	}

	for _, name := range sortedMapKeys(file.Identities) {
		identity := file.Identities[name]
		if err := c.validateIdentityConfig(name, identity); err != nil {
			return err
		}
		c.Identities[name] = identity
	}
	for _, name := range sortedMapKeys(file.Providers) {
		provider := file.Providers[name]
		if err := c.validateProviderConfig(name, provider); err != nil {
			return err
		}
		c.Providers[name] = provider
	}
	for _, name := range sortedMapKeys(file.Environments) {
		environment := file.Environments[name]
		if err := c.validateEnvironmentConfig(name, environment); err != nil {
			return err
		}
		c.Environments[name] = environment
	}
	for _, name := range sortedMapKeys(file.Profiles) {
		c.Profiles[name] = ProfileConfig{Environments: file.Profiles[name]}
	}

	return nil
}

func configDecodeError(err error) error {
	var strictErr *toml.StrictMissingError
	if !errors.As(err, &strictErr) || len(strictErr.Errors) == 0 {
		return err
	}

	keys := make([]string, 0, len(strictErr.Errors))
	for _, fieldErr := range strictErr.Errors {
		keys = append(keys, strings.Join(fieldErr.Key(), "."))
	}
	sort.Strings(keys)
	if len(keys) == 1 {
		return fmt.Errorf("unsupported config key %q", keys[0])
	}
	return fmt.Errorf("unsupported config keys: %s", strings.Join(keys, ", "))
}

func (c *Config) validateIdentityConfig(name string, identity IdentityConfig) error {
	entryName := "identities." + name
	if identity.Type == "" {
		return fmt.Errorf("%s type is required", entryName)
	}
	if identity.Type != IdentityTypeBasic && identity.Type != IdentityTypeYubiKey && identity.Type != IdentityTypeSecureEnclave {
		return fmt.Errorf("%s has unsupported type %q", entryName, identity.Type)
	}
	if identity.File == "" {
		return fmt.Errorf("%s file is required", entryName)
	}
	return c.validateConfigFilePath(entryName+".file", identity.File)
}

func (c *Config) validateProviderConfig(name string, provider ProviderConfig) error {
	entryName := "providers." + name
	if provider.Type == "" {
		return fmt.Errorf("%s type is required", entryName)
	}
	if provider.Type != ProviderType1Password {
		return fmt.Errorf("%s has unsupported type %q", entryName, provider.Type)
	}
	if provider.Identity == "" {
		return fmt.Errorf("%s identity is required", entryName)
	}
	if provider.File == "" {
		return fmt.Errorf("%s file is required", entryName)
	}
	return c.validateConfigFilePath(entryName+".file", provider.File)
}

func (c *Config) validateEnvironmentConfig(name string, environment EnvironmentConfig) error {
	entryName := "environments." + name
	if environment.Type == "" {
		return fmt.Errorf("%s type is required", entryName)
	}
	if environment.Type != EnvironmentType1Password {
		return fmt.Errorf("%s has unsupported type %q", entryName, environment.Type)
	}
	if environment.Provider == "" {
		return fmt.Errorf("%s provider is required", entryName)
	}
	if environment.UUID == "" {
		return fmt.Errorf("%s uuid is required", entryName)
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

func (c *Config) Write() error {
	var b strings.Builder
	b.WriteString("# cage global config\n")
	b.WriteString("# https://github.com/2h2d-co/cage\n\n")

	if len(c.Identities) > 0 {
		b.WriteString("[identities]\n")
		for _, name := range sortedMapKeys(c.Identities) {
			line, err := tomlEntry(name, c.Identities[name])
			if err != nil {
				return err
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteString("\n")
	}

	if len(c.Providers) > 0 {
		b.WriteString("[providers]\n")
		for _, name := range sortedMapKeys(c.Providers) {
			line, err := tomlEntry(name, c.Providers[name])
			if err != nil {
				return err
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteString("\n")
	}

	if len(c.Environments) > 0 {
		b.WriteString("[environments]\n")
		for _, name := range sortedMapKeys(c.Environments) {
			line, err := tomlEntry(name, c.Environments[name])
			if err != nil {
				return err
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteString("\n")
	}

	if len(c.Profiles) > 0 {
		b.WriteString("[profiles]\n")
		for _, name := range sortedMapKeys(c.Profiles) {
			line, err := tomlEntry(name, c.Profiles[name].Environments)
			if err != nil {
				return err
			}
			b.WriteString(line)
			b.WriteByte('\n')
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

func tomlEntry(name string, value any) (string, error) {
	var b strings.Builder
	if err := toml.NewEncoder(&b).SetTablesInline(true).Encode(map[string]any{name: value}); err != nil {
		return "", fmt.Errorf("encode config entry %q: %w", name, err)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}
