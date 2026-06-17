package cage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"
)

const (
	environmentCacheStateActive     = "active"
	environmentCacheStateExpired    = "expired"
	environmentCacheStateInactive   = "inactive"
	environmentCacheStateMissing    = "missing"
	environmentCacheStateUnreadable = "unreadable"
)

type environmentCacheStatus struct {
	Environment        string     `json:"environment"`
	Configured         bool       `json:"configured"`
	CacheConfigured    bool       `json:"cache_configured"`
	State              string     `json:"state"`
	Reason             string     `json:"reason"`
	Provider           string     `json:"provider,omitempty"`
	EnvironmentUUID    string     `json:"environment_uuid,omitempty"`
	CacheTTL           string     `json:"cache_ttl,omitempty"`
	CacheIdentity      string     `json:"cache_identity,omitempty"`
	DBEntry            bool       `json:"db_entry"`
	CacheFile          string     `json:"cache_file,omitempty"`
	CacheFileExists    bool       `json:"cache_file_exists"`
	FileStatus         string     `json:"file_status"`
	FetchedAt          *time.Time `json:"fetched_at,omitempty"`
	StoredExpiresAt    *time.Time `json:"stored_expires_at,omitempty"`
	EffectiveExpiresAt *time.Time `json:"effective_expires_at,omitempty"`
}

func (a *App) newCacheCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cache",
		Aliases: []string{"caches"},
		Short:   "Inspect and manage encrypted environment caches",
		Long:    "Inspect and manage age-encrypted 1Password Environment cache metadata and files.",
	}
	cmd.AddCommand(a.newCacheListCommand())
	cmd.AddCommand(a.newCacheStatusCommand())
	cmd.AddCommand(a.newCachePruneCommand())
	cmd.AddCommand(a.newCacheClearCommand())
	return cmd
}

func (a *App) newCacheListCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List encrypted environment caches",
		Long:  "List configured encrypted Environment caches and cache entries for the current config without printing secret values.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			store, storePresent, err := openEnvironmentCacheStoreIfPresent()
			if err != nil {
				return err
			}
			if storePresent {
				defer func() { _ = store.close() }()
			}
			statuses, err := environmentCacheStatuses(cfg, store, "", time.Now())
			if err != nil {
				return err
			}
			if jsonOutput {
				return writeCacheJSON(a.out, statuses)
			}
			return a.printEnvironmentCacheStatuses("Environment caches:", statuses)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON output")
	return cmd
}

func (a *App) newCacheStatusCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status [ENVIRONMENT]",
		Short: "Show encrypted environment cache status",
		Long:  "Show encrypted Environment cache metadata and usability status for one environment or all configured cache entries.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			store, storePresent, err := openEnvironmentCacheStoreIfPresent()
			if err != nil {
				return err
			}
			if storePresent {
				defer func() { _ = store.close() }()
			}
			environmentName := ""
			if len(args) == 1 {
				environmentName = args[0]
			}
			statuses, err := environmentCacheStatuses(cfg, store, environmentName, time.Now())
			if err != nil {
				return err
			}
			if jsonOutput {
				if environmentName != "" && len(statuses) == 1 {
					return writeCacheJSON(a.out, statuses[0])
				}
				return writeCacheJSON(a.out, statuses)
			}
			return a.printEnvironmentCacheStatuses("Environment cache status:", statuses)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON output")
	return cmd
}

func (a *App) newCachePruneCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Remove unusable encrypted environment cache entries",
		Long:  "Remove expired, inactive, missing, and unreadable encrypted Environment cache entries for the current config.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			store, storePresent, err := openEnvironmentCacheStoreIfPresent()
			if err != nil {
				return err
			}
			if !storePresent {
				_, err := fmt.Fprintln(a.out, "pruned 0 environment caches")
				return err
			}
			defer func() { _ = store.close() }()

			statuses, err := environmentCacheStatuses(cfg, store, "", time.Now())
			if err != nil {
				return err
			}
			pruned := 0
			var errs []error
			for _, status := range statuses {
				if !status.DBEntry || status.State == environmentCacheStateActive {
					continue
				}
				entry, found, err := store.entryForConfigEnvironment(cfg, status.Environment)
				if err != nil {
					errs = append(errs, fmt.Errorf("lookup cache entry for environment %q: %w", status.Environment, err))
					continue
				}
				if !found {
					continue
				}
				if err := store.deleteEntry(entry); err != nil {
					errs = append(errs, fmt.Errorf("delete cache entry for environment %q: %w", status.Environment, err))
					continue
				}
				pruned++
			}
			if len(errs) > 0 {
				return errors.Join(errs...)
			}
			_, err = fmt.Fprintf(a.out, "pruned %d environment caches\n", pruned)
			return err
		},
	}
}

func (a *App) newCacheClearCommand() *cobra.Command {
	var all bool
	var yes bool
	cmd := &cobra.Command{
		Use:   "clear [ENVIRONMENT]",
		Short: "Clear encrypted environment cache entries",
		Long:  "Clear encrypted Environment cache entries for one environment or all environments in the current config.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			if all && len(args) == 1 {
				return errors.New("--all cannot be used with ENVIRONMENT")
			}
			if !all && len(args) == 0 {
				return errors.New("specify an environment or --all")
			}

			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			store, storePresent, err := openEnvironmentCacheStoreIfPresent()
			if err != nil {
				return err
			}
			if storePresent {
				defer func() { _ = store.close() }()
			}

			if all {
				ok, err := confirm("Clear all environment caches for this config?", yes)
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("clear cancelled")
				}
				cleared, err := clearAllEnvironmentCaches(cfg, store)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(a.out, "cleared %d environment caches\n", cleared)
				return err
			}

			environmentName := args[0]
			if _, configured := cfg.Environments[environmentName]; !configured && !storePresent {
				return fmt.Errorf("unknown environment %q", environmentName)
			}
			if storePresent {
				if _, configured := cfg.Environments[environmentName]; !configured {
					if _, found, err := store.entryForConfigEnvironment(cfg, environmentName); err != nil {
						return err
					} else if !found {
						return fmt.Errorf("unknown environment %q", environmentName)
					}
				}
			}

			ok, err := confirm(fmt.Sprintf("Clear environment cache for %q?", environmentName), yes)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("clear cancelled")
			}
			cleared, err := clearEnvironmentCache(cfg, store, environmentName)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.out, "cleared %d environment caches\n", cleared)
			return err
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "clear all environment caches for the current config")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "answer yes to clear confirmations")
	return cmd
}

func openEnvironmentCacheStoreIfPresent() (*environmentCacheStore, bool, error) {
	store, err := openEnvironmentCacheStore(false)
	if errors.Is(err, errEnvironmentCacheStoreMissing) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open environment cache: %w", err)
	}
	return store, true, nil
}

func environmentCacheStatuses(cfg *Config, store *environmentCacheStore, requested string, now time.Time) ([]environmentCacheStatus, error) {
	entriesByEnvironment := map[string]environmentCacheEntry{}
	if store != nil {
		entries, err := store.entriesForConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("list environment cache entries: %w", err)
		}
		for _, entry := range entries {
			entriesByEnvironment[entry.environmentName] = entry
		}
	}

	names := map[string]struct{}{}
	if requested != "" {
		if _, configured := cfg.Environments[requested]; configured {
			names[requested] = struct{}{}
		} else if _, found := entriesByEnvironment[requested]; found {
			names[requested] = struct{}{}
		} else {
			return nil, fmt.Errorf("unknown environment %q", requested)
		}
	} else {
		for name, environment := range cfg.Environments {
			if environment.Cache != nil {
				names[name] = struct{}{}
			}
		}
		for name := range entriesByEnvironment {
			names[name] = struct{}{}
		}
	}

	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	statuses := make([]environmentCacheStatus, 0, len(ordered))
	for _, name := range ordered {
		entry, found := entriesByEnvironment[name]
		statuses = append(statuses, assessEnvironmentCacheStatus(cfg, store, name, entry, found, now))
	}
	return statuses, nil
}

func assessEnvironmentCacheStatus(cfg *Config, store *environmentCacheStore, environmentName string, entry environmentCacheEntry, found bool, now time.Time) environmentCacheStatus {
	status := environmentCacheStatus{
		Environment: environmentName,
		State:       environmentCacheStateInactive,
		FileStatus:  "none",
	}
	environment, configured := cfg.Environments[environmentName]
	status.Configured = configured
	if configured {
		status.Provider = environment.Provider
		status.EnvironmentUUID = environment.UUID
		if environment.Cache != nil {
			status.CacheConfigured = true
			status.CacheTTL = environment.Cache.TTL
			status.CacheIdentity = environment.Cache.Identity
		}
	}
	if found {
		status.DBEntry = true
		status.CacheFile = entry.cacheFile
		status.Provider = firstNonEmpty(status.Provider, entry.providerName)
		status.EnvironmentUUID = firstNonEmpty(status.EnvironmentUUID, entry.environmentUUID)
		status.CacheIdentity = firstNonEmpty(status.CacheIdentity, entry.identityName)
		fetchedAt := time.Unix(entry.fetchedAtUnix, 0)
		storedExpiresAt := time.Unix(entry.expiresAtUnix, 0)
		status.FetchedAt = &fetchedAt
		status.StoredExpiresAt = &storedExpiresAt
	}
	if !found {
		if configured && environment.Cache != nil {
			status.State = environmentCacheStateMissing
			status.Reason = "no cache entry"
			status.FileStatus = "missing"
			return status
		}
		status.Reason = "environment cache is not configured"
		return status
	}
	if !configured {
		status.Reason = "environment is not configured"
		return status
	}
	if environment.Cache == nil {
		status.Reason = "environment cache is not configured"
		return status
	}
	if entry.providerName != environment.Provider {
		status.Reason = fmt.Sprintf("provider changed from %q to %q", entry.providerName, environment.Provider)
		return status
	}
	if entry.environmentUUID != environment.UUID {
		status.Reason = "environment UUID changed"
		return status
	}
	if entry.identityName != environment.Cache.Identity {
		status.Reason = fmt.Sprintf("cache identity changed from %q to %q", entry.identityName, environment.Cache.Identity)
		return status
	}

	ttl, err := parseCacheTTL(environment.Cache.TTL)
	if err != nil {
		status.State = environmentCacheStateUnreadable
		status.Reason = fmt.Sprintf("cache ttl is invalid: %s", err)
		return status
	}
	fetchedAt := time.Unix(entry.fetchedAtUnix, 0)
	storedExpiresAt := time.Unix(entry.expiresAtUnix, 0)
	effectiveExpiresAt := storedExpiresAt
	currentConfigExpiresAt := fetchedAt.Add(ttl)
	if currentConfigExpiresAt.Before(effectiveExpiresAt) {
		effectiveExpiresAt = currentConfigExpiresAt
	}
	status.EffectiveExpiresAt = &effectiveExpiresAt
	if !now.Before(effectiveExpiresAt) {
		status.State = environmentCacheStateExpired
		status.Reason = "cache entry expired"
		return status
	}

	recipient, err := cacheIdentityRecipient(cfg, environment.Cache.Identity)
	if err != nil {
		status.State = environmentCacheStateUnreadable
		status.Reason = fmt.Sprintf("cache identity is unreadable: %s", err)
		return status
	}
	if entry.identityRecipient != recipient {
		status.Reason = "cache identity recipient changed"
		return status
	}
	if err := validateCacheFileName(entry.cacheFile); err != nil {
		status.Reason = err.Error()
		return status
	}
	if store == nil {
		status.State = environmentCacheStateUnreadable
		status.Reason = "cache store is not open"
		return status
	}
	cachePath, err := store.cacheFilePath(entry.cacheFile)
	if err != nil {
		status.Reason = err.Error()
		return status
	}
	info, err := os.Lstat(filepath.Clean(cachePath))
	if errors.Is(err, os.ErrNotExist) {
		status.Reason = "cache file is missing"
		status.FileStatus = "missing"
		return status
	}
	if err != nil {
		status.State = environmentCacheStateUnreadable
		status.Reason = fmt.Sprintf("stat cached environment file: %s", err)
		status.FileStatus = "unreadable"
		return status
	}
	status.CacheFileExists = true
	if err := ensurePrivateInfo(cachePath, "cached environment file", info, false, 0o600); err != nil {
		status.State = environmentCacheStateUnreadable
		status.Reason = err.Error()
		status.FileStatus = "unreadable"
		return status
	}
	status.State = environmentCacheStateActive
	status.Reason = "cache entry is active"
	status.CacheFileExists = true
	status.FileStatus = "present"
	return status
}

func clearAllEnvironmentCaches(cfg *Config, store *environmentCacheStore) (int, error) {
	if store == nil {
		return 0, nil
	}
	entries, err := store.entriesForConfig(cfg)
	if err != nil {
		return 0, err
	}
	cleared := 0
	var errs []error
	for _, entry := range entries {
		if err := store.deleteEntry(entry); err != nil {
			errs = append(errs, fmt.Errorf("delete cache entry for environment %q: %w", entry.environmentName, err))
			continue
		}
		cleared++
	}
	return cleared, errors.Join(errs...)
}

func clearEnvironmentCache(cfg *Config, store *environmentCacheStore, environmentName string) (int, error) {
	if store == nil {
		return 0, nil
	}
	entry, found, err := store.entryForConfigEnvironment(cfg, environmentName)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	if err := store.deleteEntry(entry); err != nil {
		return 0, err
	}
	return 1, nil
}

func (a *App) printEnvironmentCacheStatuses(title string, statuses []environmentCacheStatus) error {
	if _, err := fmt.Fprintln(a.out, title); err != nil {
		return err
	}
	if len(statuses) == 0 {
		_, err := fmt.Fprintln(a.out, "  (none)")
		return err
	}
	for _, status := range statuses {
		if _, err := fmt.Fprintf(a.out,
			"  %s\tstate=%s\tprovider=%s\tcache-ttl=%s\tcache-identity=%s\tfetched=%s\texpires=%s\tcache-file=%s\tfile-status=%s\treason=%s\n",
			status.Environment,
			status.State,
			dashIfEmpty(status.Provider),
			dashIfEmpty(status.CacheTTL),
			dashIfEmpty(status.CacheIdentity),
			formatOptionalTime(status.FetchedAt),
			formatOptionalTime(displayExpiresAt(status)),
			dashIfEmpty(status.CacheFile),
			status.FileStatus,
			status.Reason,
		); err != nil {
			return err
		}
	}
	return nil
}

func writeCacheJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func displayExpiresAt(status environmentCacheStatus) *time.Time {
	if status.EffectiveExpiresAt != nil {
		return status.EffectiveExpiresAt
	}
	return status.StoredExpiresAt
}

func formatOptionalTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "-"
	}
	return value.Format(time.RFC3339)
}

func dashIfEmpty(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func firstNonEmpty(first string, second string) string {
	if first != "" {
		return first
	}
	return second
}
