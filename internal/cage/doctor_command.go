package cage

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const ageCiphertextHeader = "age-encryption.org/v1"

const (
	doctorStatusOK   = "ok"
	doctorStatusWarn = "warn"
	doctorStatusFail = "fail"
)

type doctorReport struct {
	Status   string        `json:"status"`
	Checks   []doctorCheck `json:"checks"`
	Failures int           `json:"failures"`
	Warnings int           `json:"warnings"`
}

type doctorCheck struct {
	Status  string `json:"status"`
	Area    string `json:"area"`
	ID      string `json:"id"`
	Target  string `json:"target,omitempty"`
	Message string `json:"message"`
}

func (a *App) newDoctorCommand() *cobra.Command {
	var jsonOutput bool
	var strict bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check cage configuration and local state",
		Long:  "Check cage configuration, references, file permissions, identity/provider metadata, and encrypted cache state without decrypting provider tokens or cached Environment payloads.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			report := a.runDoctor(time.Now())
			if jsonOutput {
				if err := writeCacheJSON(a.out, report); err != nil {
					return err
				}
			} else if err := a.printDoctorReport(report); err != nil {
				return err
			}
			if report.Failures > 0 || strict && report.Warnings > 0 {
				return ExitError{Code: 1, Err: doctorExitError(report, strict)}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON output")
	cmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero when warnings are present")
	markSkipsStartupCleanup(cmd)
	return cmd
}

func (a *App) runDoctor(now time.Time) doctorReport {
	report := doctorReport{Status: doctorStatusOK}
	configPath, err := resolveConfigPath(a.configPath)
	if err != nil {
		report.add(doctorStatusFail, "config", "config.path", "", fmt.Sprintf("resolve config path: %s", err))
		report.finish()
		return report
	}
	report.add(doctorStatusOK, "config", "config.path", configPath, "config path resolved")

	configDirOK := report.checkPrivatePath("config", "config.dir", filepath.Dir(configPath), "config directory", true, 0o700, doctorStatusFail, "config directory does not exist")
	configFileOK := report.checkPrivatePath("config", "config.file", configPath, "config file", false, 0o600, doctorStatusFail, "config file does not exist")
	if !configDirOK || !configFileOK {
		report.finish()
		return report
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		report.add(doctorStatusFail, "config", "config.load", configPath, fmt.Sprintf("load config: %s", err))
		report.finish()
		return report
	}
	report.add(doctorStatusOK, "config", "config.parse", configPath, "config parses and matches the supported schema")
	if err := cfg.validateReferences(); err != nil {
		report.add(doctorStatusFail, "config", "config.references", configPath, err.Error())
	} else {
		report.add(doctorStatusOK, "config", "config.references", configPath, "all config references resolve")
	}
	if len(cfg.Identities) == 0 && len(cfg.Providers) == 0 && len(cfg.Environments) == 0 && len(cfg.Profiles) == 0 {
		report.add(doctorStatusWarn, "config", "config.empty", configPath, "config has no identities, providers, environments, or profiles")
	}

	report.checkIdentities(cfg)
	report.checkProviders(cfg)
	report.checkEnvironments(cfg)
	report.checkProfiles(cfg)
	report.checkCache(cfg, now)
	report.finish()
	return report
}

func doctorExitError(report doctorReport, strict bool) error {
	if report.Failures > 0 {
		return fmt.Errorf("doctor found %d failure(s) and %d warning(s)", report.Failures, report.Warnings)
	}
	if strict && report.Warnings > 0 {
		return fmt.Errorf("doctor found %d warning(s)", report.Warnings)
	}
	return errors.New("doctor found issues")
}

func (r *doctorReport) finish() {
	r.Failures = 0
	r.Warnings = 0
	for _, check := range r.Checks {
		switch check.Status {
		case doctorStatusFail:
			r.Failures++
		case doctorStatusWarn:
			r.Warnings++
		}
	}
	switch {
	case r.Failures > 0:
		r.Status = doctorStatusFail
	case r.Warnings > 0:
		r.Status = doctorStatusWarn
	default:
		r.Status = doctorStatusOK
	}
}

func (r *doctorReport) add(status, area, id, target, message string) {
	r.Checks = append(r.Checks, doctorCheck{Status: status, Area: area, ID: id, Target: target, Message: message})
}

func (r *doctorReport) checkPrivatePath(area, id, path, label string, wantDir bool, mode os.FileMode, missingStatus, missingMessage string) bool {
	info, err := os.Lstat(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		r.add(missingStatus, area, id, path, missingMessage)
		return missingStatus == doctorStatusOK
	}
	if err != nil {
		r.add(doctorStatusFail, area, id, path, fmt.Sprintf("stat %s: %s", label, err))
		return false
	}
	if err := ensurePrivateInfo(path, label, info, wantDir, mode); err != nil {
		r.add(doctorStatusFail, area, id, path, err.Error())
		return false
	}
	r.add(doctorStatusOK, area, id, path, fmt.Sprintf("%s is private", label))
	return true
}

func (r *doctorReport) checkIdentities(cfg *Config) {
	if len(cfg.Identities) == 0 {
		r.add(doctorStatusWarn, "identity", "identities.configured", cfg.Path, "no identities configured")
		return
	}
	cacheIdentityNames := configuredCacheIdentityNames(cfg)
	for _, name := range sortedMapKeys(cfg.Identities) {
		identity := cfg.Identities[name]
		path := cfg.ResolveFile(identity.File)
		idPrefix := "identity." + name
		r.add(doctorStatusOK, "identity", idPrefix+".type", path, fmt.Sprintf("identity type %q is supported", identity.Type))
		if !r.checkPrivatePath("identity", idPrefix+".file", path, "identity file", false, 0o600, doctorStatusFail, "identity file does not exist") {
			continue
		}
		if identity.Type == IdentityTypeYubiKey {
			r.checkTool("identity", idPrefix+".plugin", "age-plugin-yubikey")
		}
		if identity.Type == IdentityTypeSecureEnclave {
			r.checkTool("identity", idPrefix+".plugin", "age-plugin-se")
		}
		if count, err := checkIdentityFileMarkers(path); err != nil {
			r.add(doctorStatusFail, "identity", idPrefix+".markers", path, err.Error())
		} else {
			r.add(doctorStatusOK, "identity", idPrefix+".markers", path, fmt.Sprintf("identity file contains %d supported identity marker(s)", count))
		}
		recipients, err := readIdentityFilePublicRecipients(path)
		if err != nil {
			r.add(doctorStatusFail, "identity", idPrefix+".recipient", path, fmt.Sprintf("read public recipient comment: %s", err))
			continue
		}
		if len(recipients) == 1 {
			r.add(doctorStatusOK, "identity", idPrefix+".recipient", path, "identity file has one public recipient comment")
			continue
		}
		status := doctorStatusWarn
		if cacheIdentityNames[name] {
			status = doctorStatusFail
		}
		r.add(status, "identity", idPrefix+".recipient", path, fmt.Sprintf("identity file has %d public recipient comments, want exactly one", len(recipients)))
	}
}

func checkIdentityFileMarkers(path string) (int, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return 0, fmt.Errorf("read identity file: %w", err)
	}
	defer zeroBytes(data)

	count := 0
	remaining := data
	for lineNumber := 1; len(remaining) > 0; lineNumber++ {
		line := remaining
		if before, after, found := bytes.Cut(remaining, []byte("\n")); found {
			line = before
			remaining = after
		} else {
			remaining = nil
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || bytes.HasPrefix(trimmed, []byte("#")) {
			continue
		}
		if bytes.HasPrefix(trimmed, []byte("AGE-SECRET-KEY-PQ-1")) || bytes.HasPrefix(trimmed, []byte("AGE-SECRET-KEY-1")) || bytes.HasPrefix(trimmed, []byte("AGE-PLUGIN-")) {
			count++
			continue
		}
		return 0, fmt.Errorf("identity file line %d has an unsupported identity marker", lineNumber)
	}
	if count == 0 {
		return 0, errors.New("identity file has no identity markers")
	}
	return count, nil
}

func configuredCacheIdentityNames(cfg *Config) map[string]bool {
	names := map[string]bool{}
	for _, environment := range cfg.Environments {
		if environment.Cache != nil {
			names[environment.Cache.Identity] = true
		}
	}
	return names
}

func (r *doctorReport) checkTool(area, id, tool string) {
	if err := requireTool(tool); err != nil {
		r.add(doctorStatusFail, area, id, tool, err.Error())
		return
	}
	r.add(doctorStatusOK, area, id, tool, "required tool is available")
}

func (r *doctorReport) checkProviders(cfg *Config) {
	if len(cfg.Providers) == 0 {
		r.add(doctorStatusWarn, "provider", "providers.configured", cfg.Path, "no providers configured")
		return
	}
	for _, name := range sortedMapKeys(cfg.Providers) {
		provider := cfg.Providers[name]
		path := cfg.ResolveFile(provider.File)
		idPrefix := "provider." + name
		r.add(doctorStatusOK, "provider", idPrefix+".type", path, fmt.Sprintf("provider type %q is supported", provider.Type))
		if _, ok := cfg.Identities[provider.Identity]; !ok {
			r.add(doctorStatusFail, "provider", idPrefix+".identity", path, fmt.Sprintf("provider references unknown identity %q", provider.Identity))
		} else {
			r.add(doctorStatusOK, "provider", idPrefix+".identity", path, fmt.Sprintf("provider identity %q exists", provider.Identity))
		}
		if !r.checkPrivatePath("provider", idPrefix+".file", path, "provider file", false, 0o600, doctorStatusFail, "provider file does not exist") {
			continue
		}
		if err := checkAgeCiphertextFile(path); err != nil {
			r.add(doctorStatusFail, "provider", idPrefix+".ciphertext", path, err.Error())
		} else {
			r.add(doctorStatusOK, "provider", idPrefix+".ciphertext", path, "provider file looks like age ciphertext")
		}
	}
}

func checkAgeCiphertextFile(path string) error {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("read age ciphertext header: %w", err)
	}
	if len(data) == 0 {
		return errors.New("file is empty")
	}
	if !bytes.HasPrefix(data, []byte(ageCiphertextHeader)) {
		return errors.New("file does not start with an age ciphertext header")
	}
	return nil
}

func (r *doctorReport) checkEnvironments(cfg *Config) {
	if len(cfg.Environments) == 0 {
		r.add(doctorStatusWarn, "environment", "environments.configured", cfg.Path, "no environments configured")
		return
	}
	for _, name := range sortedMapKeys(cfg.Environments) {
		environment := cfg.Environments[name]
		idPrefix := "environment." + name
		r.add(doctorStatusOK, "environment", idPrefix+".type", cfg.Path, fmt.Sprintf("environment type %q is supported", environment.Type))
		if _, ok := cfg.Providers[environment.Provider]; !ok {
			r.add(doctorStatusFail, "environment", idPrefix+".provider", cfg.Path, fmt.Sprintf("environment references unknown provider %q", environment.Provider))
		} else {
			r.add(doctorStatusOK, "environment", idPrefix+".provider", cfg.Path, fmt.Sprintf("environment provider %q exists", environment.Provider))
		}
		if strings.TrimSpace(environment.UUID) == "" {
			r.add(doctorStatusFail, "environment", idPrefix+".uuid", cfg.Path, "environment UUID is empty")
		} else {
			r.add(doctorStatusOK, "environment", idPrefix+".uuid", cfg.Path, "environment UUID is set")
		}
		if environment.Cache == nil {
			r.add(doctorStatusOK, "environment", idPrefix+".cache", cfg.Path, "environment cache is not configured")
			continue
		}
		if _, err := parseCacheTTL(environment.Cache.TTL); err != nil {
			r.add(doctorStatusFail, "environment", idPrefix+".cache.ttl", cfg.Path, fmt.Sprintf("cache ttl is invalid: %s", err))
		} else {
			r.add(doctorStatusOK, "environment", idPrefix+".cache.ttl", cfg.Path, "cache ttl is valid")
		}
		if _, ok := cfg.Identities[environment.Cache.Identity]; !ok {
			r.add(doctorStatusFail, "environment", idPrefix+".cache.identity", cfg.Path, fmt.Sprintf("cache references unknown identity %q", environment.Cache.Identity))
		} else {
			r.add(doctorStatusOK, "environment", idPrefix+".cache.identity", cfg.Path, fmt.Sprintf("cache identity %q exists", environment.Cache.Identity))
		}
	}
}

func (r *doctorReport) checkProfiles(cfg *Config) {
	if len(cfg.Profiles) == 0 {
		r.add(doctorStatusOK, "profile", "profiles.configured", cfg.Path, "no profiles configured")
		return
	}
	for _, name := range sortedMapKeys(cfg.Profiles) {
		profile := cfg.Profiles[name]
		idPrefix := "profile." + name
		if len(profile.Environments) == 0 {
			r.add(doctorStatusWarn, "profile", idPrefix+".environments", cfg.Path, "profile has no environments")
			continue
		}
		for _, environmentName := range profile.Environments {
			if _, ok := cfg.Environments[environmentName]; !ok {
				r.add(doctorStatusFail, "profile", idPrefix+".environment."+environmentName, cfg.Path, fmt.Sprintf("profile references unknown environment %q", environmentName))
			} else {
				r.add(doctorStatusOK, "profile", idPrefix+".environment."+environmentName, cfg.Path, fmt.Sprintf("profile environment %q exists", environmentName))
			}
		}
	}
}

func (r *doctorReport) checkCache(cfg *Config, now time.Time) {
	cacheConfigured := hasConfiguredEnvironmentCache(cfg)
	cacheDir, cacheDirErr := DefaultCacheDir()
	environmentCacheDir, environmentCacheDirErr := DefaultEnvironmentCacheDir()
	stateDir, stateDirErr := DefaultStateDir()
	dbPath, dbPathErr := DefaultStateDBPath()
	if cacheDirErr != nil {
		r.add(doctorStatusFail, "cache", "cache.dir.path", "", fmt.Sprintf("resolve cache directory: %s", cacheDirErr))
	} else {
		r.checkOptionalCachePath("cache.dir", cacheDir, "cache directory", true, 0o700, cacheConfigured)
	}
	if environmentCacheDirErr != nil {
		r.add(doctorStatusFail, "cache", "cache.environment_dir.path", "", fmt.Sprintf("resolve environment cache directory: %s", environmentCacheDirErr))
	} else {
		r.checkOptionalCachePath("cache.environment_dir", environmentCacheDir, "environment cache directory", true, 0o700, cacheConfigured)
	}
	if stateDirErr != nil {
		r.add(doctorStatusFail, "cache", "cache.state_dir.path", "", fmt.Sprintf("resolve state directory: %s", stateDirErr))
	} else {
		r.checkOptionalCachePath("cache.state_dir", stateDir, "state directory", true, 0o700, cacheConfigured)
	}
	if dbPathErr != nil {
		r.add(doctorStatusFail, "cache", "cache.state_db.path", "", fmt.Sprintf("resolve cache state database path: %s", dbPathErr))
		return
	}

	dbExists, err := fileExists(dbPath)
	if err != nil {
		r.add(doctorStatusFail, "cache", "cache.state_db", dbPath, fmt.Sprintf("stat cache state database: %s", err))
		return
	}
	if !dbExists {
		status := doctorStatusOK
		message := "cache state database is not present"
		if cacheConfigured {
			status = doctorStatusWarn
			message = "cache state database is not initialized yet"
		}
		r.add(status, "cache", "cache.state_db", dbPath, message)
		statuses, err := environmentCacheStatuses(cfg, nil, "", now)
		if err != nil {
			r.add(doctorStatusFail, "cache", "cache.entries", dbPath, fmt.Sprintf("inspect configured cache entries: %s", err))
			return
		}
		if len(statuses) == 0 {
			r.add(doctorStatusOK, "cache", "cache.entries", dbPath, "no cache entries to inspect")
			return
		}
		for _, status := range statuses {
			r.addEnvironmentCacheStatus(status)
		}
		return
	}
	if !r.checkPrivatePath("cache", "cache.state_db", dbPath, "cache state database", false, 0o600, doctorStatusFail, "cache state database does not exist") {
		return
	}

	store, version, err := openEnvironmentCacheStoreReadOnly()
	if errors.Is(err, errEnvironmentCacheStoreMissing) {
		r.add(doctorStatusOK, "cache", "cache.state_db", dbPath, "cache state database is not present")
		return
	}
	var versionErr environmentCacheStoreVersionError
	if errors.As(err, &versionErr) {
		status := doctorStatusWarn
		if versionErr.version > environmentCacheDBVersion {
			status = doctorStatusFail
		}
		r.add(status, "cache", "cache.state_db.version", dbPath, versionErr.Error())
		return
	}
	if err != nil {
		r.add(doctorStatusFail, "cache", "cache.state_db.open", dbPath, fmt.Sprintf("open cache state database read-only: %s", err))
		return
	}
	defer func() { _ = store.close() }()
	r.add(doctorStatusOK, "cache", "cache.state_db.version", dbPath, fmt.Sprintf("cache state database version %d is supported", version))

	statuses, err := environmentCacheStatuses(cfg, store, "", now)
	if err != nil {
		r.add(doctorStatusFail, "cache", "cache.entries", dbPath, fmt.Sprintf("inspect cache entries: %s", err))
		return
	}
	if len(statuses) == 0 {
		r.add(doctorStatusOK, "cache", "cache.entries", dbPath, "no cache entries to inspect")
		return
	}
	for _, status := range statuses {
		r.addEnvironmentCacheStatus(status)
	}
}

func hasConfiguredEnvironmentCache(cfg *Config) bool {
	for _, environment := range cfg.Environments {
		if environment.Cache != nil {
			return true
		}
	}
	return false
}

func (r *doctorReport) checkOptionalCachePath(id, path, label string, wantDir bool, mode os.FileMode, cacheConfigured bool) {
	status := doctorStatusOK
	message := fmt.Sprintf("%s is not present", label)
	if cacheConfigured {
		status = doctorStatusWarn
		message = fmt.Sprintf("%s is not initialized yet", label)
	}
	r.checkPrivatePath("cache", id, path, label, wantDir, mode, status, message)
}

func (r *doctorReport) addEnvironmentCacheStatus(status environmentCacheStatus) {
	severity := doctorStatusWarn
	switch status.State {
	case environmentCacheStateActive:
		severity = doctorStatusOK
	case environmentCacheStateUnreadable:
		severity = doctorStatusFail
	}
	target := status.CacheFile
	message := fmt.Sprintf("state=%s; reason=%s", status.State, status.Reason)
	if status.FileStatus != "" {
		message += "; file-status=" + status.FileStatus
	}
	r.add(severity, "cache", "cache.environment."+status.Environment, target, message)
}

func (a *App) printDoctorReport(report doctorReport) error {
	if _, err := fmt.Fprintf(a.out, "cage doctor: %d checks, %d failures, %d warnings\n\n", len(report.Checks), report.Failures, report.Warnings); err != nil {
		return err
	}
	for _, check := range report.Checks {
		if _, err := fmt.Fprintf(a.out, "%s\t%s\t%s\t%s\n", strings.ToUpper(check.Status), check.ID, dashIfEmpty(check.Target), check.Message); err != nil {
			return err
		}
	}
	return nil
}
