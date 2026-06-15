package cage

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func makeInsecurePermissions(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := syscall.Chmod(path, uint32(mode)); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigSupportsInlineAndTableForms(t *testing.T) {
	dir := privateTempDir(t)
	path := filepath.Join(dir, "config.toml")
	data := `
[identities]
personal = { type = "basic", file = "personal.identity" }
work1 = { type = "yubikey", file = "work1.identity" }
work2 = { type = "secure-enclave", file = "work2.identity" }

[providers]
project1 = { type = "1password", identity = "work1", file = "project1.1p.age" }
project2 = { type = "1password", identity = "work2", file = "project2.1p.age" }

[environments.dev]
type = "1password-environment"
provider = "project1"
uuid = "dev-uuid"

[environments]
stage = { type = "1password-environment", provider = "project2", uuid = "stage-uuid" }

[profiles.default]
environments = ["dev"]

[profiles]
proj2-prod = ["dev", "stage"]
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Dir != dir {
		t.Fatalf("Dir = %q, want %q", cfg.Dir, dir)
	}
	if cfg.Identities["personal"].Type != IdentityTypeBasic {
		t.Fatalf("personal type = %q", cfg.Identities["personal"].Type)
	}
	if cfg.Identities["work1"].Type != IdentityTypeYubiKey {
		t.Fatalf("work1 type = %q", cfg.Identities["work1"].Type)
	}
	if cfg.Environments["dev"].UUID != "dev-uuid" || cfg.Environments["stage"].UUID != "stage-uuid" {
		t.Fatalf("environments not parsed: %#v", cfg.Environments)
	}
	if !reflect.DeepEqual(cfg.Profiles["default"].Environments, []string{"dev"}) {
		t.Fatalf("default profile = %#v", cfg.Profiles["default"].Environments)
	}
	if !reflect.DeepEqual(cfg.Profiles["proj2-prod"].Environments, []string{"dev", "stage"}) {
		t.Fatalf("proj2-prod profile = %#v", cfg.Profiles["proj2-prod"].Environments)
	}
}

func TestDefaultConfigPathUsesXDGConfigHomeWithHomeFallback(t *testing.T) {
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)

	path, err := DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(xdg, "cage", "config.toml")
	if path != want {
		t.Fatalf("DefaultConfigPath with XDG_CONFIG_HOME = %q, want %q", path, want)
	}

	home := filepath.Join(dir, "home")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	path, err = DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Join(home, ".config", "cage", "config.toml")
	if path != want {
		t.Fatalf("DefaultConfigPath without XDG_CONFIG_HOME = %q, want %q", path, want)
	}
}

func TestLoadConfigUsesCAGEConfig(t *testing.T) {
	dir := privateTempDir(t)
	path := filepath.Join(dir, "config.toml")
	data := `
[identities]
local = { type = "basic", file = "local.identity" }
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	overridePath := filepath.Join(dir, "override.toml")
	overrideData := `
[identities]
override = { type = "basic", file = "override.identity" }
`
	if err := os.WriteFile(overridePath, []byte(overrideData), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAGE_CONFIG", path)

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != path {
		t.Fatalf("Path = %q, want %q", cfg.Path, path)
	}
	if cfg.Identities["local"].Type != IdentityTypeBasic {
		t.Fatalf("local type = %q", cfg.Identities["local"].Type)
	}

	cfg, err = LoadConfig(overridePath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != overridePath {
		t.Fatalf("Path = %q, want %q", cfg.Path, overridePath)
	}
	if cfg.Identities["override"].Type != IdentityTypeBasic {
		t.Fatalf("override type = %q", cfg.Identities["override"].Type)
	}
}

func TestLoadConfigRejectsUnsafeFilePaths(t *testing.T) {
	dir := privateTempDir(t)
	cases := map[string]string{
		"absolute identity": `
[identities]
local = { type = "basic", file = "/tmp/local.identity" }
`,
		"traversing identity": `
[identities]
local = { type = "basic", file = "../local.identity" }
`,
		"traversing provider": `
[identities]
local = { type = "basic", file = "local.identity" }
[providers]
project = { type = "1password", identity = "local", file = "../project.1p.age" }
`,
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".toml")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatal("LoadConfig accepted unsafe file path")
			}
			if !strings.Contains(err.Error(), "relative path") && !strings.Contains(err.Error(), "within config directory") {
				t.Fatalf("error = %q, want unsafe path error", err)
			}
		})
	}
}

func TestLoadConfigAllowsSubdirectoryFilePaths(t *testing.T) {
	dir := privateTempDir(t)
	path := filepath.Join(dir, "config.toml")
	data := `
[identities]
local = { type = "basic", file = "identities/local.identity" }

[providers]
project = { type = "1password", identity = "local", file = "providers/project.1p.age" }
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "identities", "local.identity")
	if got := cfg.ResolveFile(cfg.Identities["local"].File); got != want {
		t.Fatalf("resolved identity path = %q, want %q", got, want)
	}
}

func TestLoadConfigRejectsUnknownNestedKeys(t *testing.T) {
	dir := privateTempDir(t)
	cases := map[string]string{
		"identity extra": `
[identities]
local = { type = "basic", file = "local.identity", token = "plain" }
`,
		"provider extra": `
[identities]
local = { type = "basic", file = "local.identity" }
[providers]
project = { type = "1password", identity = "local", file = "project.1p.age", token = "plain" }
`,
		"environment extra": `
[environments]
dev = { type = "1password-environment", provider = "project", uuid = "dev", token = "plain" }
`,
		"profile extra": `
[profiles.default]
environments = ["dev"]
provider = "project"
`,
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".toml")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatal("LoadConfig accepted an unknown nested key")
			}
			if !strings.Contains(err.Error(), "unsupported key") {
				t.Fatalf("error = %q, want unsupported key", err)
			}
		})
	}
}

func TestLoadConfigRejectsNonStringFields(t *testing.T) {
	dir := privateTempDir(t)
	path := filepath.Join(dir, "config.toml")
	data := `
[identities]
local = { type = "basic", file = 123 }
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig accepted a non-string field")
	}
	if !strings.Contains(err.Error(), "identities.local.file must be a string") {
		t.Fatalf("error = %q, want precise type error", err)
	}
}

func TestLoadConfigRejectsInsecureConfigPermissions(t *testing.T) {
	dir := privateTempDir(t)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	makeInsecurePermissions(t, path, 0o644)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig accepted group-readable config")
	}
	if !strings.Contains(err.Error(), "accessible by group or others") {
		t.Fatalf("error = %q, want permission error", err)
	}
}

func TestLoadConfigRejectsInsecureConfigDirectoryPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cage")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	makeInsecurePermissions(t, dir, 0o755)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig accepted searchable config directory")
	}
	if !strings.Contains(err.Error(), "accessible by group or others") {
		t.Fatalf("error = %q, want permission error", err)
	}
}

func TestConfigWriteUsesExpectedCreatedSchema(t *testing.T) {
	dir := privateTempDir(t)
	cfg := emptyConfig(filepath.Join(dir, "config.toml"))
	cfg.Identities["work1"] = IdentityConfig{Type: IdentityTypeBasic, File: "work1.identity"}
	cfg.Providers["project1"] = ProviderConfig{Type: ProviderType1Password, Identity: "work1", File: "project1.1p.age"}
	cfg.Environments["dev"] = EnvironmentConfig{Type: EnvironmentType1Password, Provider: "project1", UUID: "dev-uuid"}
	cfg.Profiles["default"] = ProfileConfig{Environments: []string{"dev"}}

	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(cfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	for _, want := range []string{
		`work1 = { type = "basic", file = "work1.identity" }`,
		`project1 = { type = "1password", identity = "work1", file = "project1.1p.age" }`,
		`dev = { type = "1password-environment", provider = "project1", uuid = "dev-uuid" }`,
		`default = ["dev"]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("written config missing %q:\n%s", want, text)
		}
	}
}

func TestSelectionEnvironmentOrder(t *testing.T) {
	cfg := emptyConfig("/tmp/config.toml")
	cfg.Environments["dev"] = EnvironmentConfig{Type: EnvironmentType1Password, Provider: "p", UUID: "dev"}
	cfg.Environments["stage"] = EnvironmentConfig{Type: EnvironmentType1Password, Provider: "p", UUID: "stage"}
	cfg.Environments["prod"] = EnvironmentConfig{Type: EnvironmentType1Password, Provider: "p", UUID: "prod"}
	cfg.Profiles["default"] = ProfileConfig{Environments: []string{"dev", "stage"}}

	got, err := (Selection{Profiles: []string{"default"}, Environments: []string{"prod"}}).environmentOrder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dev", "stage", "prod"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %#v, want %#v", got, want)
	}
}
