package cage

import (
	"bytes"
	"strings"
	"testing"
)

func TestChildEnvironmentRemovesOnePasswordServiceAccountToken(t *testing.T) {
	tokenKey := "OP_SERVICE_ACCOUNT_" + "TOKEN"
	sessionKey := "OP_SESSION_example"
	t.Setenv("CAGE_TEST_KEEP", "parent")
	t.Setenv(tokenKey, "parent-value")
	t.Setenv(sessionKey, "session-value")

	env, err := childEnvironment(map[string]string{
		"CAGE_TEST_KEEP": "override",
		"CAGE_TEST_NEW":  "value",
		tokenKey:         "override-value",
		sessionKey:       "override-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, unexpected := range []string{"OP_SERVICE_ACCOUNT_TOKEN=", "OP_SESSION_example="} {
		if strings.Contains(joined, unexpected) {
			t.Fatalf("child env leaked %s: %s", unexpected, joined)
		}
	}
	if !strings.Contains(joined, "\nCAGE_TEST_KEEP=override\n") {
		t.Fatalf("child env did not override parent value: %s", joined)
	}
	if !strings.Contains(joined, "\nCAGE_TEST_NEW=value\n") {
		t.Fatalf("child env missing override: %s", joined)
	}
}

func TestChildEnvironmentRejectsInvalidOverrideName(t *testing.T) {
	_, err := childEnvironment(map[string]string{"BAD=NAME": "value"})
	if err == nil {
		t.Fatal("childEnvironment accepted invalid override name")
	}
	if !strings.Contains(err.Error(), "contains =") {
		t.Fatalf("error = %q, want contains =", err)
	}
}

func TestRedactSecretLookingValues(t *testing.T) {
	input := "token=ops_abc123 AGE-SECRET-KEY-PQ-1ABCDEF AGE-PLUGIN-SE-1SECRET password: hunter2"
	redacted := Redact(input)
	for _, leaked := range []string{"ops_abc123", "AGE-SECRET-KEY", "AGE-PLUGIN-SE", "hunter2"} {
		if strings.Contains(redacted, leaked) {
			t.Fatalf("redacted output leaked %q: %s", leaked, redacted)
		}
	}
}

func TestVerboseAndDebugDiagnosticsDiffer(t *testing.T) {
	var out bytes.Buffer
	app := &App{errOut: &out}
	app.verbosef("visible")
	app.debugf("hidden")
	if out.Len() != 0 {
		t.Fatalf("diagnostics without flags = %q, want none", out.String())
	}

	app.verbose = true
	app.verbosef("visible")
	app.debugf("hidden")
	if got := out.String(); got != "cage: visible\n" {
		t.Fatalf("verbose diagnostics = %q, want only high-level output", got)
	}

	out.Reset()
	app.verbose = false
	app.debug = true
	app.verbosef("visible")
	app.debugf("details")
	if got := out.String(); got != "cage: visible\ndebug: details\n" {
		t.Fatalf("debug diagnostics = %q, want high-level and debug output", got)
	}
}
