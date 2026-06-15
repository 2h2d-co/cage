//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	integrationProfile             = "integration-test"
	integrationOverrideEnvironment = "integration-test-override"
	integrationEdgeValue           = "value with spaces; equals=ok; pipe|ampersand&dollar$quote\"apostrophe'backslash\\done"
)

type cageResult struct {
	stdout string
	stderr string
}

type integrationReportEntry struct {
	name   string
	status string
	detail string
}

type integrationReport struct {
	entries []integrationReportEntry
}

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
		assertFileContains(t, configPath, `integration-local = { type = "basic", file = "integration-local.identity" }`)

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
		assertFileContains(t, configPath, `integration-provider = { type = "1password", identity = "integration-local", file = "integration-provider.1p.age" }`)
		assertFileNotContains(t, providerPath, fakeToken)

		runCage(t, bin, configEnv, "", "environment", "create", "integration-env", "--provider", "integration-provider", "--uuid", "integration-env-uuid")
		assertFileContains(t, configPath, `integration-env = { type = "1password-environment", provider = "integration-provider", uuid = "integration-env-uuid" }`)
		listEnvironments := runCage(t, bin, configEnv, "", "environment", "list")
		assertContains(t, listEnvironments.stdout, "integration-env")
		assertContains(t, listEnvironments.stdout, "provider-status=present")

		runCage(t, bin, configEnv, "", "profile", "create", "integration-profile", "--environments", "integration-env")
		assertFileContains(t, configPath, `integration-profile = ["integration-env"]`)
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

func reportedSubtest(t *testing.T, report *integrationReport, name string, detail string, run func(t *testing.T)) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Cleanup(func() {
			status := "ok"
			switch {
			case t.Failed():
				status = "fail"
			case t.Skipped():
				status = "missing"
			}
			report.Add(name, status, detail)
		})
		run(t)
	})
}

func (r *integrationReport) Add(name string, status string, detail string) {
	r.entries = append(r.entries, integrationReportEntry{name: name, status: status, detail: detail})
}

func (r *integrationReport) Print() {
	fmt.Println("cage integration identity/profile results:")
	if len(r.entries) == 0 {
		fmt.Println("  (none)")
		return
	}
	for _, entry := range r.entries {
		if entry.detail == "" {
			fmt.Printf("  %s\tstatus=%s\n", entry.name, entry.status)
			continue
		}
		fmt.Printf("  %s\tstatus=%s\t%s\n", entry.name, entry.status, entry.detail)
	}
}

func requireDarwin(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("cage is macOS-only")
	}
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func buildCage(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("find integration test source path")
	}
	repoRoot := filepath.Dir(filepath.Dir(file))
	bin := filepath.Join(t.TempDir(), "cage")
	cmd := exec.Command("go", "build", "-o", bin, repoRoot)
	cmd.Env = sanitizedEnv(nil)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build cage: %v\n%s", err, output)
	}
	return bin
}

func requireIntegrationConfig(t *testing.T) string {
	t.Helper()
	path := os.Getenv("CAGE_INTEGRATION_CONFIG")
	if path == "" {
		path = os.Getenv("CAGE_CONFIG")
	}
	if path == "" {
		configHome := os.Getenv("XDG_CONFIG_HOME")
		if configHome == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatalf("find home directory: %v", err)
			}
			configHome = filepath.Join(home, ".config")
		}
		path = filepath.Join(configHome, "cage", "integration-test", "config.toml")
	}
	path = expandHome(path)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("integration config %s does not exist; see integration/README.md for setup", path)
		}
		t.Fatalf("stat integration config %s: %v", path, err)
	}
	return path
}

func expandHome(path string) string {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func runCage(t *testing.T, bin string, extraEnv []string, stdin string, args ...string) cageResult {
	t.Helper()
	result, code := runCageRaw(t, bin, extraEnv, stdin, args...)
	if code != 0 {
		t.Fatalf("cage %s failed with exit code %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, result.stdout, result.stderr)
	}
	return result
}

func profileExists(t *testing.T, bin string, extraEnv []string, profile string) bool {
	t.Helper()
	result := runCage(t, bin, extraEnv, "", "profile", "list")
	for _, line := range strings.Split(result.stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == profile {
			return true
		}
	}
	return false
}

func runCageFailure(t *testing.T, bin string, extraEnv []string, stdin string, args ...string) (cageResult, int) {
	t.Helper()
	result, code := runCageRaw(t, bin, extraEnv, stdin, args...)
	if code == 0 {
		t.Fatalf("cage %s unexpectedly succeeded\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), result.stdout, result.stderr)
	}
	return result, code
}

func runShell(t *testing.T, extraEnv []string, script string) cageResult {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = sanitizedEnv(extraEnv)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("shell script failed: %v\nscript:\n%s\nstdout:\n%s\nstderr:\n%s", err, script, stdout.String(), stderr.String())
	}
	return cageResult{stdout: stdout.String(), stderr: stderr.String()}
}

func runCageInteractive(t *testing.T, bin string, extraEnv []string, args ...string) cageResult {
	t.Helper()
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open /dev/tty for interactive hardware prompt: %v", err)
	}
	defer func() {
		if err := tty.Close(); err != nil {
			t.Fatalf("close /dev/tty: %v", err)
		}
	}()

	cmd := exec.Command(bin, args...)
	cmd.Env = sanitizedEnv(extraEnv)
	cmd.Stdin = tty
	cmd.Stderr = tty
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("interactive cage %s failed: %v\nstdout:\n%s", strings.Join(args, " "), err, stdout.String())
	}
	return cageResult{stdout: stdout.String()}
}

func runCageRaw(t *testing.T, bin string, extraEnv []string, stdin string, args ...string) (cageResult, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = sanitizedEnv(extraEnv)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := cageResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result, 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return result, exitErr.ExitCode()
	}
	t.Fatalf("run cage %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, result.stdout, result.stderr)
	return result, -1
}

func sanitizedEnv(extra []string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if key == "OP_SERVICE_ACCOUNT_TOKEN" || key == "CAGE_CONFIG" || key == "CAGE_PROFILES" || key == "CAGE_ENVIRONMENTS" {
			continue
		}
		env = append(env, entry)
	}
	return append(env, extra...)
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %v, want %v", path, got, want)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		t.Fatalf("%s exists, want missing", path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat %s: %v", path, err)
	}
}

func assertFileContains(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	assertContains(t, string(data), want)
}

func assertFileNotContains(t *testing.T, path string, unexpected string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	assertNotContains(t, string(data), unexpected)
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output missing %q:\n%s", want, got)
	}
}

func assertNotContains(t *testing.T, got string, unexpected string) {
	t.Helper()
	if strings.Contains(got, unexpected) {
		t.Fatalf("output contains %q:\n%s", unexpected, got)
	}
}

func assertEqual[T comparable](t *testing.T, got T, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}
