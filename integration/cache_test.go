//go:build integration

package integration_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3" // register sqlite3 database driver for cache state inspection
)

type cacheIntegrationFixture struct {
	t          *testing.T
	bin        string
	configPath string
	configDir  string
	cacheHome  string
	stateHome  string
	cacheEnv   []string
	provider   string
	uuid       string
	dbPath     string
	cachePath  string
}

type cacheEntry struct {
	cacheFile         string
	providerName      string
	environmentUUID   string
	identityName      string
	identityRecipient string
	fetchedAtUnix     int64
	expiresAtUnix     int64
}

type cacheCommandStatus struct {
	Environment     string `json:"environment"`
	State           string `json:"state"`
	Reason          string `json:"reason"`
	CacheIdentity   string `json:"cache_identity"`
	DBEntry         bool   `json:"db_entry"`
	CacheFileExists bool   `json:"cache_file_exists"`
}

func runEncryptedCacheIntegration(t *testing.T, bin string, configPath string) {
	t.Helper()
	t.Run("bootstrap hit mixed refresh skip exec cleanup", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")

		result := runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		entry := fixture.singleCacheEntry()
		assertEqual(t, entry.identityName, integrationCacheIdentity)
		assertFileMode(t, fixture.cachePath, 0o600)
		assertFileMode(t, fixture.dbPath, 0o600)
		assertFileNotContains(t, fixture.cachePath, "CAGE_INTEGRATION_EDGE")
		assertFileNotContains(t, fixture.cachePath, integrationEdgeValue)

		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--verbose", "--profiles", integrationProfile, "--environments", integrationOverrideEnvironment, "CAGE_INTEGRATION_ORDER")
		assertEqual(t, strings.TrimSpace(result.stdout), "explicit")
		assertContains(t, result.stderr, "loading environment "+integrationEnvironment+" from cache")
		assertContains(t, result.stderr, "loading environment "+integrationOverrideEnvironment)
		fixture.assertCacheRowCount(1)

		if err := writePrivateFile(fixture.cachePath, []byte("not an age file")); err != nil {
			t.Fatal(err)
		}
		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--verbose", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		assertContains(t, result.stderr, "discard unreadable cache for environment "+integrationEnvironment)
		assertFileMode(t, fixture.cachePath, 0o600)

		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--refresh-cache", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")

		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "exec", "--refresh-cache", "--profiles", integrationProfile, "--", "/usr/bin/env")
		execRefreshEnv := "\n" + result.stdout
		assertContains(t, execRefreshEnv, "\nCAGE_INTEGRATION_HEALTH=ok\n")
		assertContains(t, execRefreshEnv, "\nCAGE_INTEGRATION_EXEC=exec-ok\n")

		corruptProviderFiles(t, fixture.configDir)
		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--verbose", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		assertContains(t, result.stderr, "loading environment "+integrationEnvironment+" from cache")

		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "exec", "--profiles", integrationProfile, "--", "/usr/bin/env")
		execEnv := "\n" + result.stdout
		assertContains(t, execEnv, "\nCAGE_INTEGRATION_HEALTH=ok\n")
		assertContains(t, execEnv, "\nCAGE_INTEGRATION_EXEC=exec-ok\n")

		_, code := runCageFailure(t, fixture.bin, fixture.cacheEnv, "", "get", "--skip-cache", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, code, 1)

		fixture.expireCacheEntries()
		runCage(t, fixture.bin, fixture.cacheEnv, "", "completion", "bash")
		assertFileMissing(t, fixture.cachePath)
		fixture.assertCacheRowCount(0)
	})

	t.Run("skip cache does not bootstrap", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		result := runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--skip-cache", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		assertFileMissing(t, fixture.dbPath)

		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "exec", "--skip-cache", "--profiles", integrationProfile, "--", "/usr/bin/env")
		execEnv := "\n" + result.stdout
		assertContains(t, execEnv, "\nCAGE_INTEGRATION_HEALTH=ok\n")
		assertFileMissing(t, fixture.dbPath)
	})

	t.Run("cache management commands inspect prune and clear", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		result := runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		fixture.singleCacheEntry()

		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "cache", "list")
		assertContains(t, result.stdout, integrationEnvironment)
		assertContains(t, result.stdout, "state=active")
		assertContains(t, result.stdout, "cache-identity="+integrationCacheIdentity)
		assertNotContains(t, result.stdout, integrationEdgeValue)

		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "cache", "list", "--json")
		assertNotContains(t, result.stdout, integrationEdgeValue)
		statuses := decodeCacheStatusList(t, result.stdout)
		status := findCacheStatus(t, statuses, integrationEnvironment)
		assertEqual(t, status.State, "active")
		assertEqual(t, status.CacheIdentity, integrationCacheIdentity)
		assertEqual(t, status.DBEntry, true)
		assertEqual(t, status.CacheFileExists, true)

		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "cache", "status", integrationEnvironment, "--json")
		status = decodeCacheStatus(t, result.stdout)
		assertEqual(t, status.Environment, integrationEnvironment)
		assertEqual(t, status.State, "active")

		fixture.expireCacheEntries()
		runCage(t, fixture.bin, fixture.cacheEnv, "", "cache", "prune")
		assertFileMissing(t, fixture.cachePath)
		fixture.assertCacheRowCount(0)

		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		fixture.singleCacheEntry()
		runCage(t, fixture.bin, fixture.cacheEnv, "", "cache", "clear", integrationEnvironment, "--yes")
		assertFileMissing(t, fixture.cachePath)
		fixture.assertCacheRowCount(0)

		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		fixture.singleCacheEntry()
		runCage(t, fixture.bin, fixture.cacheEnv, "", "cache", "clear", "--all", "--yes")
		assertFileMissing(t, fixture.cachePath)
		fixture.assertCacheRowCount(0)
	})

	t.Run("doctor and read-only cache commands do not prune expired cache", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		result := runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		fixture.singleCacheEntry()
		fixture.expireCacheEntries()
		assertFileMode(t, fixture.cachePath, 0o600)

		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "doctor", "--json")
		assertNotContains(t, result.stdout, integrationEdgeValue)
		report := decodeDoctorReport(t, result.stdout)
		assertEqual(t, report.Status, "warn")
		check := assertDoctorIntegrationCheck(t, report, "warn", "cache.environment."+integrationEnvironment)
		assertContains(t, check.Message, "state=expired")
		assertFileMode(t, fixture.cachePath, 0o600)
		fixture.assertCacheRowCount(1)

		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "cache", "list", "--json")
		status := findCacheStatus(t, decodeCacheStatusList(t, result.stdout), integrationEnvironment)
		assertEqual(t, status.State, "expired")
		assertFileMode(t, fixture.cachePath, 0o600)
		fixture.assertCacheRowCount(1)

		result = runCage(t, fixture.bin, fixture.cacheEnv, "", "cache", "status", integrationEnvironment, "--json")
		status = decodeCacheStatus(t, result.stdout)
		assertEqual(t, status.State, "expired")
		assertFileMode(t, fixture.cachePath, 0o600)
		fixture.assertCacheRowCount(1)

		runCage(t, fixture.bin, fixture.cacheEnv, "", "completion", "bash")
		assertFileMissing(t, fixture.cachePath)
		fixture.assertCacheRowCount(0)
	})

	t.Run("launchd periodically prunes expired cache", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "10s")
		launchdLabel := "co.2h2d.cage.integration-cache-prune"
		launchdHome := privateTempDir(t)
		launchdEnv := append([]string{}, fixture.cacheEnv...)
		launchdEnv = append(launchdEnv, "HOME="+launchdHome, "CAGE_CACHE_PRUNE_LAUNCHD_LABEL="+launchdLabel)
		t.Cleanup(func() {
			result, code := runCageRaw(t, fixture.bin, launchdEnv, "", "cache", "launchd", "uninstall")
			if code != 0 {
				t.Errorf("cleanup cache launchd uninstall exited %d\nstdout:\n%s\nstderr:\n%s", code, result.stdout, result.stderr)
			}
		})

		result := runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		fixture.singleCacheEntry()
		assertFileMode(t, fixture.cachePath, 0o600)

		runCage(t, fixture.bin, launchdEnv, "", "cache", "launchd", "install", "--interval", "5s")
		plistPath := filepath.Join(launchdHome, "Library", "LaunchAgents", launchdLabel+".plist")
		assertFileContains(t, plistPath, "<string>"+launchdLabel+"</string>")
		assertFileContains(t, plistPath, "<key>XDG_CACHE_HOME</key>")
		assertFileContains(t, plistPath, "<key>XDG_STATE_HOME</key>")
		assertFileMode(t, fixture.cachePath, 0o600)

		waitForFileMissing(t, fixture.cachePath, 45*time.Second)
		fixture.assertCacheRowCount(0)
		runCage(t, fixture.bin, launchdEnv, "", "cache", "launchd", "uninstall")
		assertFileMissing(t, plistPath)
	})

	t.Run("environment cache setting commands require overwrite", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		failure, code := runCageFailure(t, fixture.bin, fixture.cacheEnv, "", "environment", "cache", "set", integrationEnvironment, "--ttl", "30m", "--identity", integrationCacheIdentity)
		assertEqual(t, code, 1)
		assertContains(t, failure.stderr, "use --overwrite to replace it")

		runCage(t, fixture.bin, fixture.cacheEnv, "", "environment", "cache", "set", integrationEnvironment, "--ttl", "30m", "--identity", integrationCacheIdentity, "--overwrite")
		assertFileContains(t, fixture.configPath, "cache = {ttl = '30m', identity = '"+integrationCacheIdentity+"'}")

		runCage(t, fixture.bin, fixture.cacheEnv, "", "environment", "cache", "unset", integrationEnvironment)
		assertFileContains(t, fixture.configPath, integrationEnvironment+" = {type = '1password-environment', provider = '"+fixture.provider+"', uuid = '"+fixture.uuid+"'}")

		runCage(t, fixture.bin, fixture.cacheEnv, "", "environment", "cache", "set", integrationEnvironment, "--ttl", "45m", "--identity", integrationCacheIdentity)
		assertFileContains(t, fixture.configPath, "cache = {ttl = '45m', identity = '"+integrationCacheIdentity+"'}")
	})

	t.Run("missing cache file repairs", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		assertEqual(t, strings.TrimSpace(runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH").stdout), "ok")
		fixture.singleCacheEntry()
		if err := os.Remove(filepath.Clean(fixture.cachePath)); err != nil {
			t.Fatal(err)
		}

		result := runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--verbose", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		assertFileMode(t, fixture.cachePath, 0o600)
		fixture.assertCacheRowCount(1)
	})

	t.Run("stale db metadata repairs", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		assertEqual(t, strings.TrimSpace(runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH").stdout), "ok")
		fixture.singleCacheEntry()
		fixture.execCacheDB(`UPDATE environment_cache_entries SET provider_name = ?, environment_uuid = ?, identity_name = ?, identity_recipient = ?`, "stale-provider", "stale-uuid", "stale-identity", "stale-recipient")

		result := runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--verbose", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		entry := fixture.singleCacheEntry()
		assertEqual(t, entry.providerName, fixture.provider)
		assertEqual(t, entry.environmentUUID, fixture.uuid)
		assertEqual(t, entry.identityName, integrationCacheIdentity)
		if entry.identityRecipient == "stale-recipient" {
			t.Fatal("stale identity recipient was not repaired")
		}
		assertFileMode(t, fixture.cachePath, 0o600)
	})

	t.Run("cache identity recipient rotation repairs", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		assertEqual(t, strings.TrimSpace(runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH").stdout), "ok")
		oldEntry := fixture.singleCacheEntry()
		runCage(t, fixture.bin, fixture.cacheEnv, "", "identity", "basic", "create", integrationCacheIdentity, "--yes")

		result := runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--verbose", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		entry := fixture.singleCacheEntry()
		if entry.identityRecipient == oldEntry.identityRecipient {
			t.Fatal("cache identity recipient did not rotate")
		}
		assertFileMode(t, fixture.cachePath, 0o600)
	})

	t.Run("current ttl cap repairs", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		assertEqual(t, strings.TrimSpace(runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH").stdout), "ok")
		fixture.singleCacheEntry()
		oldFetchedAt := time.Now().Add(-10 * time.Second).Unix()
		futureExpiry := time.Now().Add(time.Hour).Unix()
		fixture.execCacheDB(`UPDATE environment_cache_entries SET fetched_at_unix = ?, expires_at_unix = ?`, oldFetchedAt, futureExpiry)
		fixture.enableCache("1s")

		result := runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--verbose", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		entry := fixture.singleCacheEntry()
		if entry.fetchedAtUnix == oldFetchedAt {
			t.Fatalf("cache fetched_at_unix was not refreshed: %d", entry.fetchedAtUnix)
		}
		if entry.expiresAtUnix > time.Now().Add(5*time.Second).Unix() {
			t.Fatalf("cache expires_at_unix = %d, want near current ttl", entry.expiresAtUnix)
		}
	})

	t.Run("invalid cache file db value repairs with warning", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		assertEqual(t, strings.TrimSpace(runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH").stdout), "ok")
		fixture.singleCacheEntry()
		fixture.execCacheDB(`UPDATE environment_cache_entries SET cache_file = ?`, ".")

		result := runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--verbose", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		assertContains(t, result.stderr, "delete inactive cache for environment "+integrationEnvironment)
		entry := fixture.singleCacheEntry()
		if entry.cacheFile == "." {
			t.Fatal("invalid cache_file value was not repaired")
		}
	})

	t.Run("insecure cache file permissions repair", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		assertEqual(t, strings.TrimSpace(runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH").stdout), "ok")
		fixture.singleCacheEntry()
		if err := os.Chmod(filepath.Clean(fixture.cachePath), 0o644); err != nil {
			t.Fatal(err)
		}

		result := runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--verbose", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, strings.TrimSpace(result.stdout), "ok")
		assertContains(t, result.stderr, "discard unreadable cache for environment "+integrationEnvironment)
		assertFileMode(t, fixture.cachePath, 0o600)
	})

	t.Run("cache directory permissions fail resolution", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		assertEqual(t, strings.TrimSpace(runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH").stdout), "ok")
		fixture.singleCacheEntry()
		if err := os.Chmod(filepath.Dir(fixture.cachePath), 0o755); err != nil {
			t.Fatal(err)
		}

		failure, code := runCageFailure(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, code, 1)
		assertContains(t, failure.stderr, "environment cache directory")
	})

	t.Run("cache db insert failure removes written cache file", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		assertEqual(t, strings.TrimSpace(runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH").stdout), "ok")
		fixture.singleCacheEntry()
		if err := os.Remove(filepath.Clean(fixture.cachePath)); err != nil {
			t.Fatal(err)
		}
		fixture.execCacheDB(`DELETE FROM environment_cache_entries`)
		fixture.execCacheStatement(`CREATE TRIGGER fail_environment_cache_insert BEFORE INSERT ON environment_cache_entries BEGIN SELECT RAISE(FAIL, 'forced insert failure'); END`)

		failure, code := runCageFailure(t, fixture.bin, fixture.cacheEnv, "", "get", "--refresh-cache", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, code, 1)
		assertContains(t, failure.stderr, "record cached environment in state database")
		assertFileMissing(t, fixture.cachePath)
	})

	t.Run("cache identity corruption fails", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		assertEqual(t, strings.TrimSpace(runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH").stdout), "ok")
		fixture.singleCacheEntry()
		identityPath := filepath.Join(fixture.configDir, integrationCacheIdentity+".identity")
		if err := writePrivateFile(identityPath, []byte("not an age identity")); err != nil {
			t.Fatal(err)
		}

		failure, code := runCageFailure(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
		assertEqual(t, code, 1)
		assertContains(t, failure.stderr, "public recipient")
	})

	t.Run("cleanup warning for unsupported db version", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		assertEqual(t, strings.TrimSpace(runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH").stdout), "ok")
		fixture.setCacheDBUserVersion(999)

		result := runCage(t, fixture.bin, fixture.cacheEnv, "", "completion", "bash")
		assertContains(t, result.stdout, "cage")
		assertContains(t, result.stderr, "warning: cache cleanup")
	})

	t.Run("cleanup warning for corrupt db file", func(t *testing.T) {
		fixture := newCacheIntegrationFixture(t, bin, configPath, "1h")
		assertEqual(t, strings.TrimSpace(runCage(t, fixture.bin, fixture.cacheEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH").stdout), "ok")
		if err := writePrivateFile(fixture.dbPath, []byte("not a sqlite database")); err != nil {
			t.Fatal(err)
		}

		result := runCage(t, fixture.bin, fixture.cacheEnv, "", "completion", "bash")
		assertContains(t, result.stdout, "cage")
		assertContains(t, result.stderr, "warning: cache cleanup")
	})
}

func newCacheIntegrationFixture(t *testing.T, bin string, configPath string, ttl string) *cacheIntegrationFixture {
	t.Helper()
	cacheConfigPath := copyIntegrationConfig(t, configPath)
	cacheHome := filepath.Join(privateTempDir(t), "cache")
	stateHome := filepath.Join(privateTempDir(t), "state")
	fixture := &cacheIntegrationFixture{
		t:          t,
		bin:        bin,
		configPath: cacheConfigPath,
		configDir:  filepath.Dir(cacheConfigPath),
		cacheHome:  cacheHome,
		stateHome:  stateHome,
		dbPath:     filepath.Join(stateHome, "cage", "cage.db"),
		cacheEnv: []string{
			"CAGE_CONFIG=" + cacheConfigPath,
			"XDG_CACHE_HOME=" + cacheHome,
			"XDG_STATE_HOME=" + stateHome,
		},
	}

	runCage(t, fixture.bin, fixture.cacheEnv, "", "identity", "basic", "create", integrationCacheIdentity, "--yes")
	fixture.provider, fixture.uuid = configuredEnvironmentDetails(t, fixture.bin, fixture.cacheEnv, integrationEnvironment)
	fixture.enableCache(ttl)
	return fixture
}

func (f *cacheIntegrationFixture) enableCache(ttl string) {
	runCage(f.t, f.bin, f.cacheEnv, "", "environment", "create", integrationEnvironment, "--provider", f.provider, "--uuid", f.uuid, "--cache-ttl", ttl, "--cache-identity", integrationCacheIdentity, "--yes")
	assertFileContains(f.t, f.configPath, "cache = {ttl = '"+ttl+"', identity = '"+integrationCacheIdentity+"'}")
}

func copyIntegrationConfig(t *testing.T, configPath string) string {
	t.Helper()
	sourceDir := filepath.Dir(configPath)
	destinationDir := filepath.Join(privateTempDir(t), "integration-config")
	if err := copyDirectory(sourceDir, destinationDir); err != nil {
		t.Fatalf("copy integration config %s to %s: %v", sourceDir, destinationDir, err)
	}
	return filepath.Join(destinationDir, filepath.Base(configPath))
}

func copyDirectory(sourceDir string, destinationDir string) error {
	return filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(destinationDir, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse to copy symlink %s", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Clean(destination), 0o700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refuse to copy non-regular file %s", path)
		}
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Clean(destination), data, 0o600)
	})
}

func configuredEnvironmentDetails(t *testing.T, bin string, extraEnv []string, environment string) (string, string) {
	t.Helper()
	result := runCage(t, bin, extraEnv, "", "environment", "list")
	for _, line := range strings.Split(result.stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != environment {
			continue
		}
		var provider string
		var uuid string
		for _, field := range fields[1:] {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				continue
			}
			switch key {
			case "provider":
				provider = value
			case "uuid":
				uuid = value
			}
		}
		if provider == "" || uuid == "" {
			t.Fatalf("environment %s details missing provider or uuid:\n%s", environment, result.stdout)
		}
		return provider, uuid
	}
	t.Fatalf("environment %s not found:\n%s", environment, result.stdout)
	return "", ""
}

func (f *cacheIntegrationFixture) singleCacheEntry() cacheEntry {
	entry := singleCacheEntry(f.t, f.dbPath)
	f.cachePath = filepath.Join(f.cacheHome, "cage", "environments", entry.cacheFile)
	return entry
}

func singleCacheEntry(t *testing.T, dbPath string) cacheEntry {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open cache db %s: %v", dbPath, err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	rows, err := db.Query(`SELECT cache_file, provider_name, environment_uuid, identity_name, identity_recipient, fetched_at_unix, expires_at_unix FROM environment_cache_entries ORDER BY environment_name`)
	if err != nil {
		t.Fatalf("query cache db %s: %v", dbPath, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	entries := []cacheEntry{}
	for rows.Next() {
		var entry cacheEntry
		if err := rows.Scan(&entry.cacheFile, &entry.providerName, &entry.environmentUUID, &entry.identityName, &entry.identityRecipient, &entry.fetchedAtUnix, &entry.expiresAtUnix); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("cache entries = %#v, want exactly one", entries)
	}
	return entries[0]
}

func (f *cacheIntegrationFixture) expireCacheEntries() {
	now := time.Now().Unix()
	f.execCacheDB(`UPDATE environment_cache_entries SET fetched_at_unix = ?, expires_at_unix = ?`, now-10, now-1)
}

func (f *cacheIntegrationFixture) assertCacheRowCount(want int) {
	assertCacheRowCount(f.t, f.dbPath, want)
}

func assertCacheRowCount(t *testing.T, dbPath string, want int) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open cache db %s: %v", dbPath, err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM environment_cache_entries`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("cache row count = %d, want %d", got, want)
	}
}

func decodeCacheStatusList(t *testing.T, data string) []cacheCommandStatus {
	t.Helper()
	var statuses []cacheCommandStatus
	if err := json.Unmarshal([]byte(data), &statuses); err != nil {
		t.Fatalf("parse cache status list JSON: %v\n%s", err, data)
	}
	return statuses
}

func decodeCacheStatus(t *testing.T, data string) cacheCommandStatus {
	t.Helper()
	var status cacheCommandStatus
	if err := json.Unmarshal([]byte(data), &status); err != nil {
		t.Fatalf("parse cache status JSON: %v\n%s", err, data)
	}
	return status
}

func findCacheStatus(t *testing.T, statuses []cacheCommandStatus, environment string) cacheCommandStatus {
	t.Helper()
	for _, status := range statuses {
		if status.Environment == environment {
			return status
		}
	}
	t.Fatalf("cache status for environment %s not found: %#v", environment, statuses)
	return cacheCommandStatus{}
}

func waitForFileMissing(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := os.Stat(filepath.Clean(path))
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			t.Fatalf("stat %s while waiting for deletion: %v", path, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s still exists after %s", path, timeout)
}

func (f *cacheIntegrationFixture) execCacheDB(query string, args ...any) {
	execCacheDB(f.t, f.dbPath, query, args...)
}

func (f *cacheIntegrationFixture) execCacheStatement(query string, args ...any) {
	execCacheStatement(f.t, f.dbPath, query, args...)
}

func execCacheDB(t *testing.T, dbPath string, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open cache db %s: %v", dbPath, err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatalf("execute cache db query %q: %v", query, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if rows == 0 {
		t.Fatalf("cache db query %q affected no rows", query)
	}
}

func execCacheStatement(t *testing.T, dbPath string, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open cache db %s: %v", dbPath, err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("execute cache db statement %q: %v", query, err)
	}
}

func (f *cacheIntegrationFixture) setCacheDBUserVersion(version int) {
	execCacheStatement(f.t, f.dbPath, fmt.Sprintf("PRAGMA user_version = %d", version))
}

func corruptProviderFiles(t *testing.T, configDir string) {
	t.Helper()
	corrupted := 0
	err := filepath.WalkDir(configDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".1p.age") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refuse to corrupt non-regular provider file %s", path)
		}
		if err := writePrivateFile(path, []byte("corrupted encrypted provider")); err != nil {
			return err
		}
		corrupted++
		return nil
	})
	if err != nil {
		t.Fatalf("corrupt provider files in %s: %v", configDir, err)
	}
	if corrupted == 0 {
		t.Fatalf("no provider files found in %s", configDir)
	}
}

func writePrivateFile(path string, data []byte) error {
	cleaned := filepath.Clean(path)
	if err := os.WriteFile(cleaned, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(cleaned, 0o600)
}
