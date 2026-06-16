package cage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	onepassword "github.com/1password/onepassword-sdk-go"
)

func TestResolveVariablesWritesAndReadsEncryptedCache(t *testing.T) {
	setCacheXDG(t)
	cfg := cacheTestConfig(t)

	providerCalls := 0
	app := cacheTestApp("fresh", &providerCalls)
	variables, err := app.resolveVariables(context.Background(), cfg, Selection{Environments: []string{"dev"}}, cacheModeUse)
	if err != nil {
		t.Fatal(err)
	}
	if variables["SECRET"] != "fresh" {
		t.Fatalf("SECRET = %q, want fresh", variables["SECRET"])
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls = %d, want 1", providerCalls)
	}

	cachePath := cacheTestEnvironmentFile(t, cfg, "dev")
	ciphertext, err := os.ReadFile(filepath.Clean(cachePath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ciphertext), "fresh") {
		t.Fatal("cached environment file contains plaintext secret")
	}
	assertPrivateMode(t, cachePath, 0o600)
	dbPath, err := DefaultStateDBPath()
	if err != nil {
		t.Fatal(err)
	}
	assertPrivateMode(t, dbPath, 0o600)

	providerCalls = 0
	cachedApp := cacheTestApp("from-1password", &providerCalls)
	variables, err = cachedApp.resolveVariables(context.Background(), cfg, Selection{Environments: []string{"dev"}}, cacheModeUse)
	if err != nil {
		t.Fatal(err)
	}
	if variables["SECRET"] != "fresh" {
		t.Fatalf("cached SECRET = %q, want fresh", variables["SECRET"])
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls after cache hit = %d, want 0", providerCalls)
	}
}

func TestResolveVariablesSkipAndRefreshCacheModes(t *testing.T) {
	setCacheXDG(t)
	cfg := cacheTestConfig(t)

	providerCalls := 0
	if _, err := cacheTestApp("old", &providerCalls).resolveVariables(context.Background(), cfg, Selection{Environments: []string{"dev"}}, cacheModeUse); err != nil {
		t.Fatal(err)
	}

	providerCalls = 0
	variables, err := cacheTestApp("skip", &providerCalls).resolveVariables(context.Background(), cfg, Selection{Environments: []string{"dev"}}, cacheModeSkip)
	if err != nil {
		t.Fatal(err)
	}
	if variables["SECRET"] != "skip" {
		t.Fatalf("skip SECRET = %q, want skip", variables["SECRET"])
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls with skip = %d, want 1", providerCalls)
	}

	providerCalls = 0
	variables, err = cacheTestApp("unused", &providerCalls).resolveVariables(context.Background(), cfg, Selection{Environments: []string{"dev"}}, cacheModeUse)
	if err != nil {
		t.Fatal(err)
	}
	if variables["SECRET"] != "old" {
		t.Fatalf("SECRET after skip = %q, want old", variables["SECRET"])
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls after skip = %d, want 0", providerCalls)
	}

	providerCalls = 0
	variables, err = cacheTestApp("new", &providerCalls).resolveVariables(context.Background(), cfg, Selection{Environments: []string{"dev"}}, cacheModeRefresh)
	if err != nil {
		t.Fatal(err)
	}
	if variables["SECRET"] != "new" {
		t.Fatalf("refresh SECRET = %q, want new", variables["SECRET"])
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls with refresh = %d, want 1", providerCalls)
	}

	providerCalls = 0
	variables, err = cacheTestApp("unused", &providerCalls).resolveVariables(context.Background(), cfg, Selection{Environments: []string{"dev"}}, cacheModeUse)
	if err != nil {
		t.Fatal(err)
	}
	if variables["SECRET"] != "new" {
		t.Fatalf("SECRET after refresh = %q, want new", variables["SECRET"])
	}
	if providerCalls != 0 {
		t.Fatalf("provider calls after refresh = %d, want 0", providerCalls)
	}
}

func TestResolveVariablesDeletesBadCacheAndFetchesFresh(t *testing.T) {
	setCacheXDG(t)
	cfg := cacheTestConfig(t)

	providerCalls := 0
	if _, err := cacheTestApp("old", &providerCalls).resolveVariables(context.Background(), cfg, Selection{Environments: []string{"dev"}}, cacheModeUse); err != nil {
		t.Fatal(err)
	}
	cachePath := cacheTestEnvironmentFile(t, cfg, "dev")
	if err := writeSecretFile(cachePath, []byte("not an age file")); err != nil {
		t.Fatal(err)
	}

	providerCalls = 0
	variables, err := cacheTestApp("fresh", &providerCalls).resolveVariables(context.Background(), cfg, Selection{Environments: []string{"dev"}}, cacheModeUse)
	if err != nil {
		t.Fatal(err)
	}
	if variables["SECRET"] != "fresh" {
		t.Fatalf("SECRET = %q, want fresh", variables["SECRET"])
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls = %d, want 1", providerCalls)
	}
}

func TestResolveVariablesCapsCacheExpiryByCurrentConfigTTL(t *testing.T) {
	setCacheXDG(t)
	cfg := cacheTestConfig(t)

	providerCalls := 0
	if _, err := cacheTestApp("old", &providerCalls).resolveVariables(context.Background(), cfg, Selection{Environments: []string{"dev"}}, cacheModeUse); err != nil {
		t.Fatal(err)
	}

	store := openCacheStoreForTest(t, false)
	oldFetchedAt := time.Now().Add(-10 * time.Second).Unix()
	futureExpiry := time.Now().Add(time.Hour).Unix()
	if _, err := store.db.Exec(`UPDATE environment_cache_entries SET fetched_at_unix = ?, expires_at_unix = ?`, oldFetchedAt, futureExpiry); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	cfg.Environments["dev"].Cache.TTL = "1s"

	providerCalls = 0
	variables, err := cacheTestApp("fresh", &providerCalls).resolveVariables(context.Background(), cfg, Selection{Environments: []string{"dev"}}, cacheModeUse)
	if err != nil {
		t.Fatal(err)
	}
	if variables["SECRET"] != "fresh" {
		t.Fatalf("SECRET = %q, want fresh", variables["SECRET"])
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls = %d, want 1", providerCalls)
	}
}

func TestCleanupExpiredEnvironmentCachesDeletesFilesAndRows(t *testing.T) {
	setCacheXDG(t)
	cfg := cacheTestConfig(t)
	cfg.Environments["dev"].Cache.TTL = "1s"

	store := openCacheStoreForTest(t, true)
	if err := store.saveEnvironment(cfg, "dev", []onepassword.EnvironmentVariable{{Name: "SECRET", Value: "expired"}}, time.Now().Add(-10*time.Second)); err != nil {
		t.Fatal(err)
	}
	cachePath := cacheTestEnvironmentFile(t, cfg, "dev")
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}

	if err := cleanupExpiredEnvironmentCaches(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("expired cache file still exists or unexpected stat error: %v", err)
	}

	store = openCacheStoreForTest(t, false)
	defer func() {
		if err := store.close(); err != nil {
			t.Fatal(err)
		}
	}()
	var rows int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM environment_cache_entries`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("cache rows = %d, want 0", rows)
	}
}

func TestDefaultCacheAndStatePathsUseXDGWithHomeFallback(t *testing.T) {
	dir := t.TempDir()
	cacheHome := filepath.Join(dir, "cache")
	stateHome := filepath.Join(dir, "state")
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("XDG_STATE_HOME", stateHome)

	cacheDir, err := DefaultEnvironmentCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cacheHome, "cage", "environments"); cacheDir != want {
		t.Fatalf("DefaultEnvironmentCacheDir = %q, want %q", cacheDir, want)
	}
	dbPath, err := DefaultStateDBPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(stateHome, "cage", "cage.db"); dbPath != want {
		t.Fatalf("DefaultStateDBPath = %q, want %q", dbPath, want)
	}

	home := filepath.Join(dir, "home")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", home)
	cacheDir, err = DefaultEnvironmentCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".cache", "cage", "environments"); cacheDir != want {
		t.Fatalf("DefaultEnvironmentCacheDir fallback = %q, want %q", cacheDir, want)
	}
	dbPath, err = DefaultStateDBPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "state", "cage", "cage.db"); dbPath != want {
		t.Fatalf("DefaultStateDBPath fallback = %q, want %q", dbPath, want)
	}
}

func assertPrivateMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != mode {
		t.Fatalf("%s mode = %04o, want %04o", path, got, mode)
	}
}

func setCacheXDG(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
}

func cacheTestConfig(t *testing.T) *Config {
	t.Helper()
	dir := privateTempDir(t)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	identityData := []byte("# public key: " + identity.Recipient().String() + "\n" + identity.String() + "\n")
	if err := writeSecretFile(filepath.Join(dir, "cache.identity"), identityData); err != nil {
		t.Fatal(err)
	}

	cfg := emptyConfig(filepath.Join(dir, "config.toml"))
	cfg.Identities["cache"] = IdentityConfig{Type: IdentityTypeBasic, File: "cache.identity"}
	cfg.Providers["project"] = ProviderConfig{Type: ProviderType1Password, Identity: "cache", File: "project.1p.age"}
	cfg.Environments["dev"] = EnvironmentConfig{
		Type:     EnvironmentType1Password,
		Provider: "project",
		UUID:     "dev-uuid",
		Cache:    &EnvironmentCacheConfig{TTL: "1h", Identity: "cache"},
	}
	return cfg
}

func cacheTestApp(value string, providerCalls *int) *App {
	api := &fakeEnvironmentsAPI{responses: map[string]onepassword.GetVariablesResponse{
		"dev-uuid": {Variables: []onepassword.EnvironmentVariable{{Name: "SECRET", Value: value, Masked: true}}},
	}}
	return &App{
		out:    io.Discard,
		errOut: io.Discard,
		decryptProviderToken: func(_ *Config, _ string) ([]byte, error) {
			*providerCalls = *providerCalls + 1
			return []byte("token"), nil
		},
		newOnePasswordEnvironments: func(context.Context, []byte, string) (onepassword.EnvironmentsAPI, error) {
			return api, nil
		},
	}
}

func cacheTestEnvironmentFile(t *testing.T, cfg *Config, environmentName string) string {
	t.Helper()
	dir, err := DefaultEnvironmentCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	cacheKey := environmentCacheKey(normalizedConfigPath(cfg.Path), environmentName)
	return filepath.Join(dir, cacheKey+".age")
}

func openCacheStoreForTest(t *testing.T, create bool) *environmentCacheStore {
	t.Helper()
	store, err := openEnvironmentCacheStore(create)
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("cache store is nil")
	}
	return store
}

func TestCacheDatabaseSchemaUserVersion(t *testing.T) {
	setCacheXDG(t)
	store := openCacheStoreForTest(t, true)
	defer func() {
		if err := store.close(); err != nil {
			t.Fatal(err)
		}
	}()

	var version int
	if err := store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != environmentCacheDBVersion {
		t.Fatalf("user_version = %d, want %d", version, environmentCacheDBVersion)
	}

	var tableName string
	if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'environment_cache_entries'`).Scan(&tableName); err != nil {
		t.Fatal(err)
	}
	if tableName != "environment_cache_entries" {
		t.Fatalf("table name = %q, want environment_cache_entries", tableName)
	}
}
