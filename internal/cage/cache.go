package cage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	onepassword "github.com/1password/onepassword-sdk-go"
	_ "github.com/mattn/go-sqlite3" // register sqlite3 database driver
)

const environmentCacheDBVersion = 1

var errEnvironmentCacheStoreMissing = errors.New("environment cache store does not exist")

// DefaultCacheDir returns the global cage XDG cache directory.
func DefaultCacheDir() (string, error) {
	if xdgCacheHome := os.Getenv("XDG_CACHE_HOME"); xdgCacheHome != "" {
		return filepath.Join(xdgCacheHome, "cage"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "cage"), nil
}

// DefaultEnvironmentCacheDir returns the directory for encrypted environment cache files.
func DefaultEnvironmentCacheDir() (string, error) {
	dir, err := DefaultCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "environments"), nil
}

// DefaultStateDir returns the global cage XDG state directory.
func DefaultStateDir() (string, error) {
	if xdgStateHome := os.Getenv("XDG_STATE_HOME"); xdgStateHome != "" {
		return filepath.Join(xdgStateHome, "cage"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "cage"), nil
}

// DefaultStateDBPath returns the global cage state database path.
func DefaultStateDBPath() (string, error) {
	dir, err := DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cage.db"), nil
}

// CleanupExpiredEnvironmentCaches removes expired encrypted cache files tracked in cage.db.
func CleanupExpiredEnvironmentCaches(w io.Writer) {
	if err := cleanupExpiredEnvironmentCaches(time.Now()); err != nil && w != nil {
		_, _ = fmt.Fprintf(w, "cage: warning: cache cleanup: %s\n", Redact(err.Error()))
	}
}

func cleanupExpiredEnvironmentCaches(now time.Time) error {
	store, err := openEnvironmentCacheStore(false)
	if errors.Is(err, errEnvironmentCacheStoreMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = store.close() }()

	entries, err := store.expiredEntries(now)
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if err := store.deleteEntry(entry); err != nil {
			errs = append(errs, fmt.Errorf("delete expired cache %s: %w", entry.cacheFile, err))
		}
	}
	return errors.Join(errs...)
}

func parseCacheTTL(value string) (time.Duration, error) {
	ttl, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if ttl <= 0 {
		return 0, errors.New("must be positive")
	}
	return ttl, nil
}

type environmentCacheStore struct {
	db       *sql.DB
	cacheDir string
}

type environmentCacheEntry struct {
	cacheKey          string
	configPath        string
	environmentName   string
	providerName      string
	environmentUUID   string
	identityName      string
	identityRecipient string
	cacheFile         string
	fetchedAtUnix     int64
	expiresAtUnix     int64
}

type cachedEnvironmentPayload struct {
	Variables []onepassword.EnvironmentVariable `json:"variables"`
}

func openEnvironmentCacheStore(create bool) (*environmentCacheStore, error) {
	cacheDir, err := DefaultEnvironmentCacheDir()
	if err != nil {
		return nil, err
	}
	dbPath, err := DefaultStateDBPath()
	if err != nil {
		return nil, err
	}

	if !create {
		exists, err := fileExists(dbPath)
		if err != nil {
			return nil, fmt.Errorf("stat cache state database %s: %w", dbPath, err)
		}
		if !exists {
			return nil, errEnvironmentCacheStoreMissing
		}
		if err := ensurePrivateDirIfExists(filepath.Dir(cacheDir), "cache directory"); err != nil {
			return nil, err
		}
		if err := ensurePrivateDirIfExists(cacheDir, "environment cache directory"); err != nil {
			return nil, err
		}
		if err := ensurePrivateDirIfExists(filepath.Dir(dbPath), "state directory"); err != nil {
			return nil, err
		}
		if err := ensurePrivateFile(dbPath, "cache state database"); err != nil {
			return nil, err
		}
	} else {
		if err := ensureEnvironmentCacheStorage(cacheDir, dbPath); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite3", sqliteDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open cache state database %s: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, errors.Join(fmt.Errorf("open cache state database %s: %w", dbPath, err), db.Close())
	}
	if err := migrateEnvironmentCacheDB(db); err != nil {
		return nil, errors.Join(fmt.Errorf("migrate cache state database %s: %w", dbPath, err), db.Close())
	}
	if create {
		if err := ensurePrivateFile(dbPath, "cache state database"); err != nil {
			return nil, errors.Join(err, db.Close())
		}
	}
	return &environmentCacheStore{db: db, cacheDir: cacheDir}, nil
}

func ensureEnvironmentCacheStorage(cacheDir, dbPath string) error {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return fmt.Errorf("create cache directory %s: %w", cacheDir, err)
	}
	if err := ensurePrivateDir(filepath.Dir(cacheDir), "cache directory"); err != nil {
		return err
	}
	if err := ensurePrivateDir(cacheDir, "environment cache directory"); err != nil {
		return err
	}
	stateDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create state directory %s: %w", stateDir, err)
	}
	if err := ensurePrivateDir(stateDir, "state directory"); err != nil {
		return err
	}
	if err := createPrivateFileIfMissing(dbPath); err != nil {
		return fmt.Errorf("create cache state database %s: %w", dbPath, err)
	}
	return nil
}

func createPrivateFileIfMissing(path string) error {
	cleaned := filepath.Clean(path)
	info, err := os.Lstat(cleaned)
	if err == nil {
		return ensurePrivateInfo(path, "cache state database", info, false, 0o600)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	file, err := os.OpenFile(cleaned, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(err, file.Close())
	}
	return file.Close()
}

func sqliteDSN(path string) string {
	u := url.URL{Scheme: "file", Path: filepath.Clean(path)}
	query := u.Query()
	query.Set("_busy_timeout", "5000")
	u.RawQuery = query.Encode()
	return u.String()
}

func migrateEnvironmentCacheDB(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > environmentCacheDBVersion {
		return fmt.Errorf("unsupported cache state database version %d", version)
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS environment_cache_entries (
	cache_key TEXT PRIMARY KEY,
	config_path TEXT NOT NULL,
	environment_name TEXT NOT NULL,
	provider_name TEXT NOT NULL,
	environment_uuid TEXT NOT NULL,
	identity_name TEXT NOT NULL,
	identity_recipient TEXT NOT NULL,
	cache_file TEXT NOT NULL UNIQUE,
	fetched_at_unix INTEGER NOT NULL,
	expires_at_unix INTEGER NOT NULL,
	CHECK (expires_at_unix > fetched_at_unix),
	CHECK (cache_file <> ''),
	CHECK (instr(cache_file, '/') = 0)
)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS environment_cache_entries_lookup_idx ON environment_cache_entries(config_path, environment_name)`,
		`CREATE INDEX IF NOT EXISTS environment_cache_entries_expiry_idx ON environment_cache_entries(expires_at_unix)`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	if version == 0 {
		if _, err := tx.Exec("PRAGMA user_version = 1"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *environmentCacheStore) close() error {
	return s.db.Close()
}

func (s *environmentCacheStore) expiredEntries(now time.Time) ([]environmentCacheEntry, error) {
	rows, err := s.db.Query(`SELECT cache_key, config_path, environment_name, provider_name, environment_uuid, identity_name, identity_recipient, cache_file, fetched_at_unix, expires_at_unix
FROM environment_cache_entries
WHERE expires_at_unix <= ?
ORDER BY expires_at_unix`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var entries []environmentCacheEntry
	for rows.Next() {
		entry, err := scanEnvironmentCacheEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func scanEnvironmentCacheEntry(scanner interface{ Scan(dest ...any) error }) (environmentCacheEntry, error) {
	var entry environmentCacheEntry
	err := scanner.Scan(
		&entry.cacheKey,
		&entry.configPath,
		&entry.environmentName,
		&entry.providerName,
		&entry.environmentUUID,
		&entry.identityName,
		&entry.identityRecipient,
		&entry.cacheFile,
		&entry.fetchedAtUnix,
		&entry.expiresAtUnix,
	)
	return entry, err
}

func (s *environmentCacheStore) lookupEnvironment(cfg *Config, environmentName string, now time.Time) (environmentCacheEntry, bool, bool, error) {
	configPath := normalizedConfigPath(cfg.Path)
	row := s.db.QueryRow(`SELECT cache_key, config_path, environment_name, provider_name, environment_uuid, identity_name, identity_recipient, cache_file, fetched_at_unix, expires_at_unix
FROM environment_cache_entries
WHERE config_path = ? AND environment_name = ?`, configPath, environmentName)
	entry, err := scanEnvironmentCacheEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return environmentCacheEntry{}, false, false, nil
	}
	if err != nil {
		return environmentCacheEntry{}, false, false, err
	}
	active, err := s.entryIsActive(cfg, environmentName, entry, now)
	if err != nil {
		return environmentCacheEntry{}, true, false, err
	}
	return entry, true, active, nil
}

func (s *environmentCacheStore) entryIsActive(cfg *Config, environmentName string, entry environmentCacheEntry, now time.Time) (bool, error) {
	environment := cfg.Environments[environmentName]
	if environment.Cache == nil {
		return false, nil
	}
	if entry.providerName != environment.Provider || entry.environmentUUID != environment.UUID || entry.identityName != environment.Cache.Identity {
		return false, nil
	}
	ttl, err := parseCacheTTL(environment.Cache.TTL)
	if err != nil {
		return false, err
	}
	expiresAt := time.Unix(entry.expiresAtUnix, 0)
	currentConfigExpiresAt := time.Unix(entry.fetchedAtUnix, 0).Add(ttl)
	if currentConfigExpiresAt.Before(expiresAt) {
		expiresAt = currentConfigExpiresAt
	}
	if !now.Before(expiresAt) {
		return false, nil
	}
	recipient, err := cacheIdentityRecipient(cfg, environment.Cache.Identity)
	if err != nil {
		return false, err
	}
	if entry.identityRecipient != recipient {
		return false, nil
	}
	invalidCacheFile := entry.cacheFile == "" ||
		strings.ContainsRune(entry.cacheFile, '\x00') ||
		entry.cacheFile == "." ||
		entry.cacheFile == ".." ||
		filepath.Base(entry.cacheFile) != entry.cacheFile
	if invalidCacheFile {
		return false, nil
	}
	cachePath := filepath.Join(s.cacheDir, entry.cacheFile)
	if _, err := os.Lstat(filepath.Clean(cachePath)); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("stat cached environment file %s: %w", cachePath, err)
	}
	return true, nil
}

func (s *environmentCacheStore) readEnvironment(cfg *Config, environmentName string, entry environmentCacheEntry) ([]onepassword.EnvironmentVariable, error) {
	environment := cfg.Environments[environmentName]
	if environment.Cache == nil {
		return nil, errors.New("environment cache is not configured")
	}
	identity := cfg.Identities[environment.Cache.Identity]
	if err := notifyBeforeIdentityUse(cfg, environment.Cache.Identity, identity); err != nil {
		return nil, err
	}
	cachePath, err := s.cacheFilePath(entry.cacheFile)
	if err != nil {
		return nil, err
	}
	if err := ensurePrivateFile(cachePath, "cached environment file"); err != nil {
		return nil, err
	}
	ciphertext, err := os.ReadFile(filepath.Clean(cachePath))
	if err != nil {
		return nil, fmt.Errorf("read cached environment file: %w", err)
	}
	plaintext, err := decryptWithIdentityFile(ciphertext, cfg.ResolveFile(identity.File))
	if err != nil {
		return nil, fmt.Errorf("decrypt cached environment file: %w", err)
	}
	defer zeroBytes(plaintext)

	var payload cachedEnvironmentPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return nil, fmt.Errorf("parse cached environment file: %w", err)
	}
	for _, variable := range payload.Variables {
		if err := validateEnvironmentVariableName(variable.Name); err != nil {
			return nil, fmt.Errorf("cached environment %q contains invalid variable name: %w", environmentName, err)
		}
	}
	return payload.Variables, nil
}

func (s *environmentCacheStore) saveEnvironment(cfg *Config, environmentName string, variables []onepassword.EnvironmentVariable, fetchedAt time.Time) error {
	environment := cfg.Environments[environmentName]
	if environment.Cache == nil {
		return nil
	}
	ttl, err := parseCacheTTL(environment.Cache.TTL)
	if err != nil {
		return err
	}
	recipient, err := cacheIdentityRecipient(cfg, environment.Cache.Identity)
	if err != nil {
		return err
	}
	identity := cfg.Identities[environment.Cache.Identity]
	payload, err := json.Marshal(cachedEnvironmentPayload{Variables: variables})
	if err != nil {
		return fmt.Errorf("encode cached environment: %w", err)
	}
	defer zeroBytes(payload)
	ciphertext, err := encryptWithSingleIdentity(payload, cfg.ResolveFile(identity.File))
	if err != nil {
		return fmt.Errorf("encrypt cached environment: %w", err)
	}

	configPath := normalizedConfigPath(cfg.Path)
	cacheKey := environmentCacheKey(configPath, environmentName)
	cacheFile := cacheKey + ".age"
	cachePath, err := s.cacheFilePath(cacheFile)
	if err != nil {
		return err
	}
	if err := writeSecretFile(cachePath, ciphertext); err != nil {
		return fmt.Errorf("write cached environment file: %w", err)
	}

	fetchedAtUnix := fetchedAt.Unix()
	expiresAtUnix := fetchedAt.Add(ttl).Unix()
	if expiresAtUnix <= fetchedAtUnix {
		expiresAtUnix = fetchedAtUnix + 1
	}
	_, err = s.db.Exec(`INSERT INTO environment_cache_entries (
	cache_key, config_path, environment_name, provider_name, environment_uuid, identity_name, identity_recipient, cache_file, fetched_at_unix, expires_at_unix
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(cache_key) DO UPDATE SET
	config_path = excluded.config_path,
	environment_name = excluded.environment_name,
	provider_name = excluded.provider_name,
	environment_uuid = excluded.environment_uuid,
	identity_name = excluded.identity_name,
	identity_recipient = excluded.identity_recipient,
	cache_file = excluded.cache_file,
	fetched_at_unix = excluded.fetched_at_unix,
	expires_at_unix = excluded.expires_at_unix`,
		cacheKey,
		configPath,
		environmentName,
		environment.Provider,
		environment.UUID,
		environment.Cache.Identity,
		recipient,
		cacheFile,
		fetchedAtUnix,
		expiresAtUnix,
	)
	if err != nil {
		return errors.Join(fmt.Errorf("record cached environment in state database: %w", err), removeFile(cachePath))
	}
	return nil
}

func cacheIdentityRecipient(cfg *Config, identityName string) (string, error) {
	identity, ok := cfg.Identities[identityName]
	if !ok {
		return "", fmt.Errorf("unknown cache identity %q", identityName)
	}
	recipients, err := readIdentityFilePublicRecipients(cfg.ResolveFile(identity.File))
	if err != nil {
		return "", err
	}
	if len(recipients) == 0 {
		return "", fmt.Errorf("identity %q does not include a public recipient comment", identityName)
	}
	if len(recipients) != 1 {
		return "", fmt.Errorf("identity %q includes %d public recipient comments; cache encryption expects exactly one", identityName, len(recipients))
	}
	return recipients[0], nil
}

func notifyBeforeIdentityUse(cfg *Config, identityName string, identity IdentityConfig) error {
	switch identity.Type {
	case IdentityTypeSecureEnclave:
		notifyActionNeeded(fmt.Sprintf("approve Secure Enclave access for identity %q", identityName))
	case IdentityTypeYubiKey:
		identityPath := cfg.ResolveFile(identity.File)
		preNotify, err := shouldPreNotifyYubiKeyTouch(identityPath)
		if err != nil {
			return err
		}
		if preNotify {
			notifyActionNeeded(fmt.Sprintf("touch the YubiKey for identity %q when it blinks", identityName))
		}
	}
	return nil
}

func environmentCacheKey(configPath, environmentName string) string {
	sum := sha256.Sum256([]byte(configPath + "\x00" + environmentName))
	return hex.EncodeToString(sum[:])
}

func normalizedConfigPath(path string) string {
	cleaned := filepath.Clean(path)
	absolute, err := filepath.Abs(cleaned)
	if err != nil {
		return cleaned
	}
	return absolute
}

func (s *environmentCacheStore) cacheFilePath(cacheFile string) (string, error) {
	if err := validateCacheFileName(cacheFile); err != nil {
		return "", err
	}
	return filepath.Join(s.cacheDir, cacheFile), nil
}

func validateCacheFileName(cacheFile string) error {
	if cacheFile == "" {
		return errors.New("cache file name is empty")
	}
	if strings.ContainsRune(cacheFile, '\x00') {
		return errors.New("cache file name contains NUL")
	}
	if cacheFile == "." || cacheFile == ".." || filepath.Base(cacheFile) != cacheFile {
		return fmt.Errorf("cache file %q must be a basename", cacheFile)
	}
	return nil
}

func (s *environmentCacheStore) deleteEntry(entry environmentCacheEntry) error {
	cachePath, pathErr := s.cacheFilePath(entry.cacheFile)
	if pathErr == nil {
		if err := removeFile(cachePath); err != nil {
			return err
		}
	}
	_, dbErr := s.db.Exec(`DELETE FROM environment_cache_entries WHERE cache_key = ?`, entry.cacheKey)
	return errors.Join(pathErr, dbErr)
}

func removeFile(path string) error {
	cleaned := filepath.Clean(path)
	if err := os.Remove(cleaned); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func anySelectedEnvironmentCaches(cfg *Config, ordered []string) bool {
	for _, environmentName := range ordered {
		if cfg.Environments[environmentName].Cache != nil {
			return true
		}
	}
	return false
}
