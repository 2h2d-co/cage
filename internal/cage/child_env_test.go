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

func TestAgePluginEnvironmentAllowsOnlyOperationalKeys(t *testing.T) {
	env, err := buildChildEnvironment(childEnvironmentAgePlugin, []string{
		"PATH=/usr/bin:/bin",
		"HOME=/Users/test",
		"TMPDIR=/tmp/cage",
		"TERM=xterm-256color",
		"LANG=en_US.UTF-8",
		"LC_CTYPE=en_US.UTF-8",
		"__CF_USER_TEXT_ENCODING=0x1F5:0x0:0x0",
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
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, unexpected := range []string{
		"KEEP=",
		"TOKENIZERS_PARALLELISM=",
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
	for _, expected := range []string{
		"PATH=/usr/bin:/bin",
		"HOME=/Users/test",
		"TMPDIR=/tmp/cage",
		"TERM=xterm-256color",
		"LANG=en_US.UTF-8",
		"LC_CTYPE=en_US.UTF-8",
		"__CF_USER_TEXT_ENCODING=0x1F5:0x0:0x0",
	} {
		if !strings.Contains(joined, "\n"+expected+"\n") {
			t.Fatalf("plugin environment removed %s from %s", expected, joined)
		}
	}
}

func TestWithPluginChildEnvironmentRestoresVariables(t *testing.T) {
	tokenKey := "OP_SERVICE_ACCOUNT_" + "TOKEN"
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv(tokenKey, "secret")
	t.Setenv("CAGE_PLUGIN_TEST_KEEP", "value")
	sentinelErr := errors.New("sentinel")

	err := withPluginChildEnvironment(func() error {
		if _, ok := os.LookupEnv(tokenKey); ok {
			t.Fatalf("%s was present inside plugin environment", tokenKey)
		}
		if _, ok := os.LookupEnv("CAGE_PLUGIN_TEST_KEEP"); ok {
			t.Fatal("CAGE_PLUGIN_TEST_KEEP was present inside plugin environment")
		}
		if got := os.Getenv("PATH"); got != "/usr/bin:/bin" {
			t.Fatalf("PATH = %q, want /usr/bin:/bin", got)
		}
		return sentinelErr
	})
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("withPluginChildEnvironment error = %v, want sentinel", err)
	}
	if got := os.Getenv(tokenKey); got != "secret" {
		t.Fatalf("%s after restore = %q, want secret", tokenKey, got)
	}
	if got := os.Getenv("CAGE_PLUGIN_TEST_KEEP"); got != "value" {
		t.Fatalf("CAGE_PLUGIN_TEST_KEEP after restore = %q, want value", got)
	}
}

func TestRunPluginProcessUsesSanitizedEnvironment(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
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
	for _, unexpected := range []string{tokenKey + "=", "CAGE_PLUGIN_TEST_KEEP="} {
		if strings.Contains(joined, "\n"+unexpected) {
			t.Fatalf("plugin process environment leaked %s: %s", unexpected, joined)
		}
	}
	if !strings.Contains(joined, "\nPATH=/usr/bin:/bin\n") {
		t.Fatalf("plugin process environment missing PATH: %s", joined)
	}
}

func TestMacOSNotificationEnvironmentIsFixed(t *testing.T) {
	got := strings.Join(macOSNotificationEnvironment(), "\n")
	want := "PATH=/usr/bin:/bin:/usr/sbin:/sbin"
	if got != want {
		t.Fatalf("macOSNotificationEnvironment() = %q, want %q", got, want)
	}
}
