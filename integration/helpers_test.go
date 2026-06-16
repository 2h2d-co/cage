//go:build integration

package integration_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
