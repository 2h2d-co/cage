package cage

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadConfigSupportsInlineAndTableForms(t *testing.T) {
	dir := t.TempDir()
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
	dir := t.TempDir()
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

func TestConfigWriteUsesExpectedCreatedSchema(t *testing.T) {
	dir := t.TempDir()
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
