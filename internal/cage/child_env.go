package cage

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

type childEnvironmentScope int

const (
	childEnvironmentUserExec childEnvironmentScope = iota
	childEnvironmentAgePlugin
)

var processEnvironmentMu sync.Mutex

func childEnvironment(overrides map[string]string) ([]string, error) {
	return buildChildEnvironment(childEnvironmentUserExec, os.Environ(), overrides)
}

func pluginChildEnvironment() ([]string, error) {
	return buildChildEnvironment(childEnvironmentAgePlugin, os.Environ(), nil)
}

func macOSNotificationEnvironment() []string {
	return sortedEnvironment(map[string]string{"PATH": "/usr/bin:/bin:/usr/sbin:/sbin"})
}

func buildChildEnvironment(scope childEnvironmentScope, parent []string, overrides map[string]string) ([]string, error) {
	values := map[string]string{}
	for _, entry := range parent {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if err := validateEnvironmentVariableName(key); err != nil {
			return nil, fmt.Errorf("parent environment: %w", err)
		}
		if !scope.allowsInheritedEnvironment(key) {
			continue
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("parent environment variable %q contains NUL in value", key)
		}
		values[key] = value
	}

	for key, value := range overrides {
		if err := validateEnvironmentVariableName(key); err != nil {
			return nil, fmt.Errorf("child environment override: %w", err)
		}
		if !scope.allowsOverrideEnvironment(key) {
			continue
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("child environment override %q contains NUL in value", key)
		}
		values[key] = value
	}

	return sortedEnvironment(values), nil
}

func (scope childEnvironmentScope) allowsInheritedEnvironment(key string) bool {
	if blockedChildEnvironmentKey(key) {
		return false
	}

	switch scope {
	case childEnvironmentUserExec:
		return true
	case childEnvironmentAgePlugin:
		return allowedAgePluginEnvironmentKey(key)
	default:
		return false
	}
}

func (scope childEnvironmentScope) allowsOverrideEnvironment(key string) bool {
	if blockedChildEnvironmentKey(key) {
		return false
	}
	if scope == childEnvironmentAgePlugin {
		return allowedAgePluginEnvironmentKey(key)
	}
	return scope == childEnvironmentUserExec
}

func blockedChildEnvironmentKey(key string) bool {
	upper := strings.ToUpper(key)
	return upper == "OP_SERVICE_ACCOUNT_TOKEN" || strings.HasPrefix(upper, "OP_SESSION_")
}

func allowedAgePluginEnvironmentKey(key string) bool {
	switch key {
	case "PATH", "HOME", "TMPDIR", "TEMP", "TMP", "TERM", "COLORTERM", "NO_COLOR", "LANG", "__CF_USER_TEXT_ENCODING":
		return true
	default:
		return strings.HasPrefix(key, "LC_")
	}
}

func sortedEnvironment(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

func withPluginChildEnvironment(run func() error) (err error) {
	processEnvironmentMu.Lock()
	defer processEnvironmentMu.Unlock()

	original := os.Environ()
	safeEnv, err := buildChildEnvironment(childEnvironmentAgePlugin, original, nil)
	if err != nil {
		return err
	}
	if err := replaceProcessEnvironment(safeEnv); err != nil {
		return errors.Join(err, replaceProcessEnvironment(original))
	}
	defer func() {
		err = errors.Join(err, replaceProcessEnvironment(original))
	}()
	return run()
}

func replaceProcessEnvironment(env []string) error {
	os.Clearenv()
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if err := validateEnvironmentVariableName(key); err != nil {
			return err
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set environment %q: %w", key, err)
		}
	}
	return nil
}
