//go:build integration

package integration_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const (
	integrationProfile             = "integration-test"
	integrationEnvironment         = "integration-test"
	integrationOverrideEnvironment = "integration-test-override"
	integrationCacheIdentity       = "integration-cache"
	integrationEdgeValue           = "value with spaces; equals=ok; pipe|ampersand&dollar$quote\"apostrophe'backslash\\done"
)

func TestEndToEnd(t *testing.T) {
	requireDarwin(t)
	report := &integrationReport{}
	defer report.Print()
	bin := buildCage(t)

	reportedSubtest(t, report, "identity basic temporary lifecycle", "create/list/delete/provider/environment/profile", func(t *testing.T) {
		configPath := filepath.Join(privateTempDir(t), "config.toml")
		configEnv := []string{"CAGE_CONFIG=" + configPath}

		runCage(t, bin, configEnv, "", "identity", "basic", "create", "integration-local")
		identityPath := filepath.Join(filepath.Dir(configPath), "integration-local.identity")
		assertFileMode(t, identityPath, 0o600)
		assertFileContains(t, configPath, `integration-local = {type = 'basic', file = 'integration-local.identity'}`)

		listResult := runCage(t, bin, configEnv, "", "identity", "basic", "list")
		assertContains(t, listResult.stdout, "integration-local")
		assertContains(t, listResult.stdout, "status=present")
		assertContains(t, listResult.stdout, "recipient=age1")

		runCage(t, bin, configEnv, "", "identity", "basic", "create", "integration-pq", "--pq")
		assertFileContains(t, filepath.Join(filepath.Dir(configPath), "integration-pq.identity"), "AGE-SECRET-KEY-PQ-")

		runCage(t, bin, configEnv, "", "identity", "basic", "create", "integration-delete")
		runCage(t, bin, configEnv, "", "identity", "basic", "delete", "--yes", "integration-delete")
		assertFileMissing(t, filepath.Join(filepath.Dir(configPath), "integration-delete.identity"))
		assertFileNotContains(t, configPath, "integration-delete")

		const fakeToken = "fake-service-account-token-for-provider-encryption-test"
		runCage(t, bin, configEnv, fakeToken, "provider", "1p", "create", "integration-provider", "--identity", "integration-local", "--stdin")
		providerPath := filepath.Join(filepath.Dir(configPath), "integration-provider.1p.age")
		assertFileMode(t, providerPath, 0o600)
		assertFileContains(t, configPath, `integration-provider = {type = '1password', identity = 'integration-local', file = 'integration-provider.1p.age'}`)
		assertFileNotContains(t, providerPath, fakeToken)

		runCage(t, bin, configEnv, "", "environment", "create", "integration-env", "--provider", "integration-provider", "--uuid", "integration-env-uuid")
		assertFileContains(t, configPath, `integration-env = {type = '1password-environment', provider = 'integration-provider', uuid = 'integration-env-uuid'}`)
		listEnvironments := runCage(t, bin, configEnv, "", "environment", "list")
		assertContains(t, listEnvironments.stdout, "integration-env")
		assertContains(t, listEnvironments.stdout, "provider-status=present")

		runCage(t, bin, configEnv, "", "profile", "create", "integration-profile", "--environments", "integration-env")
		assertFileContains(t, configPath, `integration-profile = ['integration-env']`)
		listProfiles := runCage(t, bin, configEnv, "", "profile", "list")
		assertContains(t, listProfiles.stdout, "integration-profile")
		assertContains(t, listProfiles.stdout, "status=ok")

		deleteResult, code := runCageFailure(t, bin, configEnv, "", "environment", "delete", "integration-env", "--yes")
		if code != 1 {
			t.Fatalf("referenced environment delete exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, deleteResult.stdout, deleteResult.stderr)
		}
		assertContains(t, deleteResult.stderr, `environment "integration-env" is referenced by profiles: integration-profile`)

		runCage(t, bin, configEnv, "", "profile", "delete", "integration-profile", "--yes")
		runCage(t, bin, configEnv, "", "environment", "delete", "integration-env", "--yes")
		assertFileNotContains(t, configPath, "integration-profile")
		assertFileNotContains(t, configPath, "integration-env")
	})

	t.Run("configured 1Password profiles", func(t *testing.T) {
		configPath := requireIntegrationConfig(t)
		configEnv := []string{"CAGE_CONFIG=" + configPath}

		reportedSubtest(t, report, "profile "+integrationProfile, "basic identity", func(t *testing.T) {
			result := runCage(t, bin, configEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
			assertEqual(t, strings.TrimSpace(result.stdout), "ok")

			result = runCage(t, bin, configEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_EDGE")
			assertEqual(t, result.stdout, integrationEdgeValue+"\n")

			result = runCage(t, bin, []string{"CAGE_CONFIG=" + configPath, "CAGE_PROFILES=" + integrationProfile}, "", "get", "CAGE_INTEGRATION_HEALTH")
			assertEqual(t, strings.TrimSpace(result.stdout), "ok")

			result = runCage(t, bin, []string{"CAGE_CONFIG=" + configPath, "CAGE_PROFILES=does-not-exist"}, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_HEALTH")
			assertEqual(t, strings.TrimSpace(result.stdout), "ok")

			result = runCage(t, bin, configEnv, "", "get", "--profiles", integrationProfile, "--environments", integrationOverrideEnvironment, "CAGE_INTEGRATION_ORDER")
			assertEqual(t, strings.TrimSpace(result.stdout), "explicit")

			result = runCage(t, bin, configEnv, "", "get", "--profiles", integrationProfile, "*")
			assertContains(t, result.stdout, "CAGE_INTEGRATION_EDGE="+integrationEdgeValue+"\n")
			assertContains(t, result.stdout, "CAGE_INTEGRATION_HEALTH=ok\n")
			assertContains(t, result.stdout, "CAGE_INTEGRATION_ORDER=profile\n")

			result = runCage(t, bin, configEnv, "", "get", "--profiles", integrationProfile, "--json", "*")
			var values map[string]string
			if err := json.Unmarshal([]byte(result.stdout), &values); err != nil {
				t.Fatalf("parse JSON get output: %v\nstdout:\n%s", err, result.stdout)
			}
			assertEqual(t, values["CAGE_INTEGRATION_EDGE"], integrationEdgeValue)
			assertEqual(t, values["CAGE_INTEGRATION_HEALTH"], "ok")
			assertEqual(t, values["CAGE_INTEGRATION_ORDER"], "profile")

			runShell(t, []string{
				"CAGE_BIN=" + bin,
				"CAGE_CONFIG=" + configPath,
				"CAGE_PROFILE=" + integrationProfile,
				"EXPECTED_EDGE=" + integrationEdgeValue,
			}, `
set -eu
value="$("$CAGE_BIN" get --profiles "$CAGE_PROFILE" CAGE_INTEGRATION_EDGE)"
[ "$value" = "$EXPECTED_EDGE" ]
"$CAGE_BIN" get --profiles "$CAGE_PROFILE" CAGE_INTEGRATION_EDGE | /usr/bin/grep -F "$EXPECTED_EDGE" >/dev/null
"$CAGE_BIN" get --profiles "$CAGE_PROFILE" "*" | /usr/bin/grep -F "CAGE_INTEGRATION_EDGE=$EXPECTED_EDGE" >/dev/null
[ "$("$CAGE_BIN" get --profiles "$CAGE_PROFILE" CAGE_INTEGRATION_HEALTH)" = "ok" ] && [ "$("$CAGE_BIN" get --profiles "$CAGE_PROFILE" CAGE_INTEGRATION_ORDER)" = "profile" ]
`)

			failure, code := runCageFailure(t, bin, configEnv, "", "get", "--profiles", integrationProfile, "CAGE_INTEGRATION_MISSING")
			if code != 1 {
				t.Fatalf("missing variable exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, failure.stdout, failure.stderr)
			}
			assertContains(t, failure.stderr, `environment variable "CAGE_INTEGRATION_MISSING" is not set`)

			result = runCage(t, bin, []string{"CAGE_CONFIG=" + configPath, "OP_SERVICE_ACCOUNT_TOKEN=parent-value", "CAGE_INTEGRATION_PARENT=parent"}, "", "exec", "--profiles", integrationProfile, "--", "/usr/bin/env")
			execEnv := "\n" + result.stdout
			assertContains(t, execEnv, "\nCAGE_INTEGRATION_EXEC=exec-ok\n")
			assertContains(t, execEnv, "\nCAGE_INTEGRATION_PARENT=parent\n")
			assertNotContains(t, execEnv, "\nOP_SERVICE_ACCOUNT_TOKEN=")
		})

		reportedSubtest(t, report, "encrypted cache ephemeral copy", "cache hit/skip/refresh/cleanup", func(t *testing.T) {
			runEncryptedCacheIntegration(t, bin, configPath)
		})

		for _, profile := range []struct {
			name   string
			detail string
		}{
			{name: "integration-test-secure-enclave", detail: "secure-enclave identity"},
			{name: "integration-test-yubikey-touch", detail: "yubikey touch-only identity"},
			{name: "integration-test-yubikey-touch-pin", detail: "yubikey touch-and-pin identity"},
		} {
			reportedSubtest(t, report, "profile "+profile.name, profile.detail, func(t *testing.T) {
				if !profileExists(t, bin, configEnv, profile.name) {
					t.Skipf("optional hardware profile %q is not configured", profile.name)
				}
				result := runCageInteractive(t, bin, configEnv, "get", "--profiles", profile.name, "--json", "*")
				var values map[string]string
				if err := json.Unmarshal([]byte(result.stdout), &values); err != nil {
					t.Fatalf("parse JSON get output for %s: %v\nstdout:\n%s", profile.name, err, result.stdout)
				}
				assertEqual(t, values["CAGE_INTEGRATION_EDGE"], integrationEdgeValue)
				assertEqual(t, values["CAGE_INTEGRATION_HEALTH"], "ok")
				assertEqual(t, values["CAGE_INTEGRATION_ORDER"], "profile")
			})
		}

	})

	t.Run("completion and manpage generation", func(t *testing.T) {
		for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
			result := runCage(t, bin, nil, "", "completion", shell)
			assertContains(t, result.stdout, "cage")
		}

		manDir := filepath.Join(t.TempDir(), "man")
		runCage(t, bin, nil, "", "man", manDir)
		assertFileContains(t, filepath.Join(manDir, "cage.1"), "CAGE")
	})
}
