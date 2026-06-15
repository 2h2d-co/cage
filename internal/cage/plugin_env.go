package cage

import (
	"os"
	"strings"
	"sync"
)

var pluginEnvironmentMu sync.Mutex

func pluginEnvironment(parent []string) []string {
	env := make([]string, 0, len(parent))
	for _, entry := range parent {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || isSensitivePluginEnvKey(key) {
			continue
		}
		env = append(env, entry)
	}
	return env
}

func withSanitizedPluginEnvironment(run func() error) error {
	pluginEnvironmentMu.Lock()
	defer pluginEnvironmentMu.Unlock()

	restore := []environmentValue{}
	seen := map[string]bool{}
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || seen[key] || !isSensitivePluginEnvKey(key) {
			continue
		}
		seen[key] = true
		value, present := os.LookupEnv(key)
		restore = append(restore, environmentValue{key: key, value: value, present: present})
		_ = os.Unsetenv(key)
	}

	defer func() {
		for i := len(restore) - 1; i >= 0; i-- {
			entry := restore[i]
			if entry.present {
				_ = os.Setenv(entry.key, entry.value)
				continue
			}
			_ = os.Unsetenv(entry.key)
		}
	}()
	return run()
}

type environmentValue struct {
	key     string
	value   string
	present bool
}

func isSensitivePluginEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	if upper == "OP_SERVICE_ACCOUNT_TOKEN" || strings.HasPrefix(upper, "OP_SESSION_") {
		return true
	}
	if upper == "AGEDEBUG" || upper == "SSLKEYLOGFILE" {
		return true
	}
	if upper == "SSH_AUTH_SOCK" || upper == "GPG_AGENT_INFO" {
		return true
	}
	if strings.HasPrefix(upper, "DYLD_") {
		return true
	}
	switch upper {
	case "LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT":
		return true
	}
	if strings.Contains(upper, "API_KEY") || strings.Contains(upper, "PRIVATE_KEY") || strings.Contains(upper, "ACCESS_KEY") {
		return true
	}

	for _, segment := range strings.FieldsFunc(upper, func(r rune) bool { return r < 'A' || r > 'Z' }) {
		switch segment {
		case "TOKEN", "SECRET", "PASSWORD", "PASSWD", "APIKEY", "AUTHORIZATION", "CREDENTIAL", "CREDENTIALS":
			return true
		}
	}
	return false
}
