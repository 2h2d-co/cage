package cage

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPluginEnvironmentStripsSensitiveKeys(t *testing.T) {
	env := pluginEnvironment([]string{
		"PATH=/usr/bin:/bin",
		"KEEP=value",
		"TOKENIZERS_PARALLELISM=true",
		"OP_SERVICE_ACCOUNT_TOKEN=secret",
		"OP_SESSION_example=secret",
		"GITHUB_TOKEN=secret",
		"AWS_SECRET_ACCESS_KEY=secret",
		"AWS_ACCESS_KEY_ID=secret",
		"PASSWORD_STORE_DIR=secret",
		"DYLD_INSERT_LIBRARIES=secret",
		"LD_PRELOAD=secret",
		"SSH_AUTH_SOCK=secret",
		"SSLKEYLOGFILE=secret",
		"AGEDEBUG=plugin",
	})
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, unexpected := range []string{
		"OP_SERVICE_ACCOUNT_TOKEN=",
		"OP_SESSION_example=",
		"GITHUB_TOKEN=",
		"AWS_SECRET_ACCESS_KEY=",
		"AWS_ACCESS_KEY_ID=",
		"PASSWORD_STORE_DIR=",
		"DYLD_INSERT_LIBRARIES=",
		"LD_PRELOAD=",
		"SSH_AUTH_SOCK=",
		"SSLKEYLOGFILE=",
		"AGEDEBUG=",
	} {
		if strings.Contains(joined, "\n"+unexpected) {
			t.Fatalf("plugin environment leaked %s in %s", unexpected, joined)
		}
	}
	for _, expected := range []string{"PATH=/usr/bin:/bin", "KEEP=value", "TOKENIZERS_PARALLELISM=true"} {
		if !strings.Contains(joined, "\n"+expected+"\n") {
			t.Fatalf("plugin environment removed %s from %s", expected, joined)
		}
	}
}

func TestWithSanitizedPluginEnvironmentRestoresVariables(t *testing.T) {
	tokenKey := "OP_SERVICE_ACCOUNT_" + "TOKEN"
	t.Setenv(tokenKey, "secret")
	t.Setenv("CAGE_PLUGIN_TEST_KEEP", "value")
	sentinelErr := errors.New("sentinel")

	err := withSanitizedPluginEnvironment(func() error {
		if _, ok := os.LookupEnv(tokenKey); ok {
			t.Fatalf("%s was present inside sanitized environment", tokenKey)
		}
		if got := os.Getenv("CAGE_PLUGIN_TEST_KEEP"); got != "value" {
			t.Fatalf("CAGE_PLUGIN_TEST_KEEP = %q, want value", got)
		}
		return sentinelErr
	})
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("withSanitizedPluginEnvironment error = %v, want sentinel", err)
	}
	if got := os.Getenv(tokenKey); got != "secret" {
		t.Fatalf("%s after restore = %q, want secret", tokenKey, got)
	}
}

func TestRunPluginProcessUsesSanitizedEnvironment(t *testing.T) {
	envPath, err := exec.LookPath("env")
	if err != nil {
		t.Skip("env command not available")
	}
	tokenKey := "OP_SERVICE_ACCOUNT_" + "TOKEN"
	t.Setenv(tokenKey, "secret")
	t.Setenv("CAGE_PLUGIN_TEST_KEEP", "value")

	var stdout bytes.Buffer
	if err := runPluginProcess(envPath, nil, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	joined := "\n" + stdout.String()
	if strings.Contains(joined, "\n"+tokenKey+"=") {
		t.Fatalf("plugin process environment leaked %s: %s", tokenKey, joined)
	}
	if !strings.Contains(joined, "\nCAGE_PLUGIN_TEST_KEEP=value\n") {
		t.Fatalf("plugin process environment missing kept variable: %s", joined)
	}
}
