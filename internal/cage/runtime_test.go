package cage

import (
	"strings"
	"testing"
)

func TestChildEnvironmentRemovesOnePasswordServiceAccountToken(t *testing.T) {
	tokenKey := "OP_SERVICE_ACCOUNT_" + "TOKEN"
	t.Setenv("CAGE_TEST_KEEP", "parent")
	t.Setenv(tokenKey, "parent-value")

	env := childEnvironment(map[string]string{
		"CAGE_TEST_KEEP": "override",
		"CAGE_TEST_NEW":  "value",
		tokenKey:         "override-value",
	})
	joined := "\n" + strings.Join(env, "\n") + "\n"
	if strings.Contains(joined, "OP_SERVICE_ACCOUNT_TOKEN=") {
		t.Fatalf("child env leaked OP_SERVICE_ACCOUNT_TOKEN: %s", joined)
	}
	if !strings.Contains(joined, "\nCAGE_TEST_KEEP=override\n") {
		t.Fatalf("child env did not override parent value: %s", joined)
	}
	if !strings.Contains(joined, "\nCAGE_TEST_NEW=value\n") {
		t.Fatalf("child env missing override: %s", joined)
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
