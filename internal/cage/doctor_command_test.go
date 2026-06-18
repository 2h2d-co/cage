package cage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	onepassword "github.com/1password/onepassword-sdk-go"
)

func TestDoctorCommandReportsHealthyConfig(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("doctor command is macOS-only")
	}
	setCacheXDG(t)
	cfg := doctorTestConfig(t)
	environment := cfg.Environments["dev"]
	environment.Cache = nil
	cfg.Environments["dev"] = environment
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	app := &App{configPath: cfg.Path, out: &out, errOut: &bytes.Buffer{}}
	cmd := app.newDoctorCommand()
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != doctorStatusOK {
		t.Fatalf("doctor status = %s, want ok; report = %#v", report.Status, report)
	}
	if report.Failures != 0 || report.Warnings != 0 {
		t.Fatalf("doctor failures=%d warnings=%d, want zero", report.Failures, report.Warnings)
	}
	assertDoctorCheck(t, report, doctorStatusOK, "config.parse")
	assertDoctorCheck(t, report, doctorStatusOK, "provider.project.ciphertext")
}

func TestDoctorCommandReportsFailuresAndStrictWarnings(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("doctor command is macOS-only")
	}
	setCacheXDG(t)
	cfg := doctorTestConfig(t)
	providerPath := cfg.ResolveFile(cfg.Providers["project"].File)
	if err := os.WriteFile(providerPath, []byte("not age"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := cfg.Environments["dev"]
	environment.Cache = nil
	cfg.Environments["dev"] = environment
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	app := &App{configPath: cfg.Path, out: &out, errOut: &bytes.Buffer{}}
	cmd := app.newDoctorCommand()
	cmd.SetArgs([]string{"--json", "--strict"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "doctor found") {
		t.Fatalf("doctor error = %v, want issue summary", err)
	}

	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != doctorStatusFail {
		t.Fatalf("doctor status = %s, want fail", report.Status)
	}
	assertDoctorCheck(t, report, doctorStatusFail, "provider.project.ciphertext")
}

func TestDoctorStrictFailsOnWarnings(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("doctor command is macOS-only")
	}
	setCacheXDG(t)
	dir := privateTempDir(t)
	cfg := emptyConfig(filepath.Join(dir, "config.toml"))
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	app := &App{configPath: cfg.Path, out: &out, errOut: &bytes.Buffer{}}
	cmd := app.newDoctorCommand()
	cmd.SetArgs([]string{"--strict"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "doctor found") {
		t.Fatalf("doctor strict error = %v, want warning summary", err)
	}
	if !strings.Contains(out.String(), "WARN") {
		t.Fatalf("doctor strict output missing warning:\n%s", out.String())
	}
}

func TestDoctorCommandDoesNotPruneExpiredCache(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("doctor command is macOS-only")
	}
	setCacheXDG(t)
	cfg := doctorTestConfig(t)
	environment := cfg.Environments["dev"]
	environment.Cache.TTL = "1s"
	cfg.Environments["dev"] = environment
	if err := cfg.Write(); err != nil {
		t.Fatal(err)
	}
	store := openCacheStoreForTest(t, true)
	if err := store.saveEnvironment(cfg, "dev", []onepassword.EnvironmentVariable{{Name: "SECRET", Value: "expired"}}, time.Now().Add(-10*time.Second)); err != nil {
		t.Fatal(err)
	}
	cachePath := cacheTestEnvironmentFile(t, cfg)
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	assertPrivateMode(t, cachePath, 0o600)

	var out bytes.Buffer
	app := &App{configPath: cfg.Path, out: &out, errOut: &bytes.Buffer{}}
	cmd := app.newDoctorCommand()
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	assertPrivateMode(t, cachePath, 0o600)
	if got := cacheEntryCount(t); got != 1 {
		t.Fatalf("cache rows after doctor = %d, want 1", got)
	}
	if !strings.Contains(out.String(), "cache.environment.dev") {
		t.Fatalf("doctor output missing cache status:\n%s", out.String())
	}
}

func TestDoctorCommandSkipsStartupCleanup(t *testing.T) {
	cmd := (&App{}).newDoctorCommand()
	if !commandSkipsStartupCleanup(cmd) {
		t.Fatal("doctor command should skip startup cleanup")
	}
	cacheList := (&App{}).newCacheListCommand()
	if !commandSkipsStartupCleanup(cacheList) {
		t.Fatal("cache list command should skip startup cleanup")
	}
}

func doctorTestConfig(t *testing.T) *Config {
	t.Helper()
	cfg := cacheTestConfig(t)
	providerPath := cfg.ResolveFile(cfg.Providers["project"].File)
	identityPath := cfg.ResolveFile(cfg.Identities["cache"].File)
	ciphertext, err := encryptWithSingleIdentity([]byte("token"), identityPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSecretFile(providerPath, ciphertext); err != nil {
		t.Fatal(err)
	}
	cfg.Profiles["default"] = ProfileConfig{Environments: []string{"dev"}}
	return cfg
}

func assertDoctorCheck(t *testing.T, report doctorReport, status string, id string) {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			if check.Status != status {
				t.Fatalf("doctor check %s status = %s, want %s", id, check.Status, status)
			}
			return
		}
	}
	t.Fatalf("doctor check %s not found in %#v", id, report.Checks)
}

func TestCheckAgeCiphertextFileRejectsEmptyFile(t *testing.T) {
	path := filepath.Join(privateTempDir(t), "empty.age")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkAgeCiphertextFile(path); err == nil {
		t.Fatal("empty age ciphertext file was accepted")
	}
}
