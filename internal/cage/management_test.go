package cage

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/spf13/cobra"
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

func TestManagementListAndInspectCommandsReportMetadata(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cage commands are macOS-only")
	}

	path := filepath.Join(privateTempDir(t), "config.toml")
	cfg := emptyConfig(path)
	cfg.Identities["local"] = IdentityConfig{Type: IdentityTypeBasic, File: "local.identity"}
	cfg.Identities["work"] = IdentityConfig{Type: IdentityTypeYubiKey, File: "work.identity"}
	cfg.Providers["broken"] = ProviderConfig{Type: ProviderType1Password, Identity: "missing", File: "broken.1p.age"}
	cfg.Providers["project"] = ProviderConfig{Type: ProviderType1Password, Identity: "local", File: "project.1p.age"}
	cfg.Environments["broken"] = EnvironmentConfig{Type: EnvironmentType1Password, Provider: "missing-provider", UUID: "broken-uuid"}
	cfg.Environments["dev"] = EnvironmentConfig{Type: EnvironmentType1Password, Provider: "project", UUID: "dev-uuid", Cache: &EnvironmentCacheConfig{TTL: "15m", Identity: "local"}}
	cfg.Profiles["default"] = ProfileConfig{Environments: []string{"dev", "missing-env"}}
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	identityData := []byte("# public key: " + identity.Recipient().String() + "\n" + identity.String() + "\n")
	if err := writeSecretFile(cfg.ResolveFile("local.identity"), identityData); err != nil {
		t.Fatal(err)
	}
	if err := writeSecretFile(cfg.ResolveFile("project.1p.age"), []byte(ageCiphertextHeader+"\nbody\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeSecretFile(cfg.ResolveFile("broken.1p.age"), []byte("not age\n")); err != nil {
		t.Fatal(err)
	}

	identityOut := executeAppCommand(t, path, (*App).newIdentityCommand, "list")
	assertOutputContains(t, identityOut, "Configured identities:")
	assertOutputContains(t, identityOut, "local\ttype=basic\tfile=local.identity\tfile-status=present\trecipient="+identity.Recipient().String()+"\tstatus=ok")
	assertOutputContains(t, identityOut, "work\ttype=yubikey\tfile=work.identity\tfile-status=missing\trecipient=-\tstatus=missing-file")

	providerOut := executeAppCommand(t, path, (*App).newProviderCommand, "list")
	assertOutputContains(t, providerOut, "project\ttype=1password\tidentity=local\tidentity-status=present\tfile=project.1p.age\tfile-status=age-ciphertext\tstatus=ok")
	assertOutputContains(t, providerOut, "broken\ttype=1password\tidentity=missing\tidentity-status=missing\tfile=broken.1p.age\tfile-status=invalid-ciphertext\tstatus=missing-reference")

	environmentOut := executeAppCommand(t, path, (*App).newEnvironmentCommand, "list")
	assertOutputContains(t, environmentOut, "dev\ttype=1password-environment\tprovider=project\tprovider-status=present\tuuid=dev-uuid\tcache=enabled\tcache-ttl=15m\tcache-identity=local\tcache-identity-status=present\tstatus=ok")
	assertOutputContains(t, environmentOut, "broken\ttype=1password-environment\tprovider=missing-provider\tprovider-status=missing\tuuid=broken-uuid\tcache=disabled\tcache-ttl=-\tcache-identity=-\tcache-identity-status=-\tstatus=missing-reference")

	inspectOut := executeAppCommand(t, path, (*App).newEnvironmentCommand, "inspect", "dev")
	assertOutputContains(t, inspectOut, "Environment dev:")
	assertOutputContains(t, inspectOut, "provider-file-status=age-ciphertext")
	assertOutputContains(t, inspectOut, "referenced-by-profiles=default")
	assertOutputContains(t, inspectOut, "status=ok")

	profileOut := executeAppCommand(t, path, (*App).newProfileCommand, "list")
	assertOutputContains(t, profileOut, "default\tenvironments=dev,missing-env\tmissing-environments=missing-env\tstatus=missing-reference")
}

func TestManagementInspectionCommandsSkipStartupCleanup(t *testing.T) {
	app := &App{}
	commands := map[string]*cobra.Command{
		"identity list":         app.newIdentityListCommand(),
		"identity basic list":   app.newBasicListCommand(),
		"identity yubikey list": app.newYubiKeyListCommand(),
		"identity se list":      app.newSecureEnclaveListCommand(),
		"provider list":         app.newProviderListCommand(),
		"environment list":      app.newEnvironmentListCommand(),
		"environment inspect":   app.newEnvironmentInspectCommand(),
		"profile list":          app.newProfileListCommand(),
	}
	for name, cmd := range commands {
		if !commandSkipsStartupCleanup(cmd) {
			t.Fatalf("%s should skip startup cleanup", name)
		}
	}
}

func executeAppCommand(t *testing.T, configPath string, newCommand func(*App) *cobra.Command, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	app := &App{configPath: configPath, out: &out, errOut: &bytes.Buffer{}}
	cmd := newCommand(app)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("command %s: %v", strings.Join(args, " "), err)
	}
	return out.String()
}

func assertOutputContains(t *testing.T, output string, want string) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Fatalf("output missing %q:\n%s", want, output)
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
