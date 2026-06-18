package cage

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestEnvironmentAndProfileCommandsManageConfig(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cage commands are macOS-only")
	}

	path := filepath.Join(privateTempDir(t), "config.toml")
	cfg := emptyConfig(path)
	cfg.Identities["local"] = IdentityConfig{Type: IdentityTypeBasic, File: "local.identity"}
	cfg.Providers["project1"] = ProviderConfig{Type: ProviderType1Password, Identity: "local", File: "project1.1p.age"}
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}

	executeCage(t, path, "environment", "create", "dev", "--provider", "project1", "--uuid", "dev-uuid")
	executeCage(t, path, "environment", "create", "stage", "--provider", "project1", "--uuid", "stage-uuid")
	executeCage(t, path, "environment", "list")
	executeCage(t, path, "profile", "create", "default", "--environments", "dev,stage")
	executeCage(t, path, "profile", "list")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environments["dev"].Provider != "project1" || cfg.Environments["dev"].UUID != "dev-uuid" {
		t.Fatalf("dev environment = %#v", cfg.Environments["dev"])
	}
	if !slices.Equal(cfg.Profiles["default"].Environments, []string{"dev", "stage"}) {
		t.Fatalf("default profile = %#v", cfg.Profiles["default"].Environments)
	}

	executeCage(t, path, "environment", "create", "stage", "--provider", "project1", "--uuid", "stage-uuid-2", "--yes")
	executeCage(t, path, "profile", "create", "default", "--environments", "stage", "--yes")
	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environments["stage"].UUID != "stage-uuid-2" {
		t.Fatalf("stage UUID = %q", cfg.Environments["stage"].UUID)
	}
	if !slices.Equal(cfg.Profiles["default"].Environments, []string{"stage"}) {
		t.Fatalf("updated default profile = %#v", cfg.Profiles["default"].Environments)
	}

	err = executeCageError(t, path, "environment", "delete", "stage", "--yes")
	if err == nil || !strings.Contains(err.Error(), `environment "stage" is referenced by profiles: default`) {
		t.Fatalf("delete referenced environment error = %v", err)
	}
	executeCage(t, path, "profile", "delete", "default", "--yes")
	executeCage(t, path, "environment", "delete", "dev", "--yes")
	executeCage(t, path, "environment", "delete", "stage", "--yes")

	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Environments) != 0 {
		t.Fatalf("environments after delete = %#v", cfg.Environments)
	}
	if len(cfg.Profiles) != 0 {
		t.Fatalf("profiles after delete = %#v", cfg.Profiles)
	}
}

func TestEnvironmentCreateCommandConfiguresEncryptedCache(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "config.toml")
	cfg := emptyConfig(path)
	cfg.Identities["local"] = IdentityConfig{Type: IdentityTypeBasic, File: "local.identity"}
	cfg.Providers["project1"] = ProviderConfig{Type: ProviderType1Password, Identity: "local", File: "project1.1p.age"}
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}

	executeCage(t, path, "environment", "create", "cached", "--provider", "project1", "--uuid", "cached-uuid", "--cache-ttl", "30m", "--cache-identity", "local")
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cache := loaded.Environments["cached"].Cache
	if cache == nil || cache.TTL != "30m" || cache.Identity != "local" {
		t.Fatalf("cache = %#v, want ttl 30m identity local", cache)
	}
}

func TestEnvironmentCacheSettingsCommandsManageConfig(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cage commands are macOS-only")
	}

	path := filepath.Join(privateTempDir(t), "config.toml")
	cfg := emptyConfig(path)
	cfg.Identities["local"] = IdentityConfig{Type: IdentityTypeBasic, File: "local.identity"}
	cfg.Identities["other"] = IdentityConfig{Type: IdentityTypeBasic, File: "other.identity"}
	cfg.Providers["project1"] = ProviderConfig{Type: ProviderType1Password, Identity: "local", File: "project1.1p.age"}
	cfg.Environments["dev"] = EnvironmentConfig{Type: EnvironmentType1Password, Provider: "project1", UUID: "dev-uuid"}
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}

	executeCage(t, path, "environment", "cache", "set", "dev", "--ttl", "15m", "--identity", "local")
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cache := loaded.Environments["dev"].Cache
	if cache == nil || cache.TTL != "15m" || cache.Identity != "local" {
		t.Fatalf("cache after set = %#v, want ttl 15m identity local", cache)
	}

	err = executeCageError(t, path, "environment", "cache", "set", "dev", "--ttl", "1h", "--identity", "other")
	if err == nil || !strings.Contains(err.Error(), `use --overwrite to replace it`) {
		t.Fatalf("overwrite-required error = %v", err)
	}

	executeCage(t, path, "environment", "cache", "set", "dev", "--ttl", "1h", "--identity", "other", "--overwrite")
	loaded, err = LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cache = loaded.Environments["dev"].Cache
	if cache == nil || cache.TTL != "1h" || cache.Identity != "other" {
		t.Fatalf("cache after overwrite = %#v, want ttl 1h identity other", cache)
	}

	executeCage(t, path, "environment", "cache", "unset", "dev")
	loaded, err = LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cache = loaded.Environments["dev"].Cache; cache != nil {
		t.Fatalf("cache after unset = %#v, want nil", cache)
	}

	err = executeCageError(t, path, "environment", "cache", "set", "dev", "--ttl", "15m")
	if err == nil || !strings.Contains(err.Error(), "--identity is required") {
		t.Fatalf("missing identity error = %v", err)
	}
}

func TestEnvironmentAndProfileCommandsValidateReferences(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cage commands are macOS-only")
	}

	path := filepath.Join(privateTempDir(t), "config.toml")
	cfg := emptyConfig(path)
	cfg.Identities["local"] = IdentityConfig{Type: IdentityTypeBasic, File: "local.identity"}
	cfg.Providers["project1"] = ProviderConfig{Type: ProviderType1Password, Identity: "local", File: "project1.1p.age"}
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}

	err := executeCageError(t, path, "environment", "create", "bad", "--provider", "missing", "--uuid", "bad-uuid")
	if err == nil || !strings.Contains(err.Error(), `unknown provider "missing"`) {
		t.Fatalf("unknown provider error = %v", err)
	}

	err = executeCageError(t, path, "profile", "create", "bad", "--environments", "missing")
	if err == nil || !strings.Contains(err.Error(), `unknown environment "missing"`) {
		t.Fatalf("unknown environment error = %v", err)
	}
}

func executeCage(t *testing.T, configPath string, args ...string) {
	t.Helper()
	if err := executeCageError(t, configPath, args...); err != nil {
		t.Fatalf("cage %s: %v", strings.Join(args, " "), err)
	}
}

func executeCageError(t *testing.T, configPath string, args ...string) error {
	t.Helper()
	if os.Getenv("CAGE_TEST_CACHE_XDG") != "1" {
		dir := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
		t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	}
	cmd := NewRootCommand("test")
	cmd.SetArgs(append([]string{"--config", configPath}, args...))
	return cmd.Execute()
}
