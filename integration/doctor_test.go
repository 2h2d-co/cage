//go:build integration

package integration_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

type doctorIntegrationReport struct {
	Status   string                   `json:"status"`
	Checks   []doctorIntegrationCheck `json:"checks"`
	Failures int                      `json:"failures"`
	Warnings int                      `json:"warnings"`
}

type doctorIntegrationCheck struct {
	Status  string `json:"status"`
	Area    string `json:"area"`
	ID      string `json:"id"`
	Target  string `json:"target"`
	Message string `json:"message"`
}

func runDoctorIntegration(t *testing.T, bin string) {
	t.Helper()
	configPath := filepath.Join(privateTempDir(t), "config.toml")
	configDir := filepath.Dir(configPath)
	cacheHome := filepath.Join(privateTempDir(t), "cache")
	stateHome := filepath.Join(privateTempDir(t), "state")
	configEnv := []string{
		"CAGE_CONFIG=" + configPath,
		"XDG_CACHE_HOME=" + cacheHome,
		"XDG_STATE_HOME=" + stateHome,
	}

	const fakeToken = "fake-service-account-token-for-doctor-integration-test"
	runCage(t, bin, configEnv, "", "identity", "basic", "create", "doctor-local")
	runCage(t, bin, configEnv, fakeToken, "provider", "1p", "create", "doctor-provider", "--identity", "doctor-local", "--stdin")
	runCage(t, bin, configEnv, "", "environment", "create", "doctor-env", "--provider", "doctor-provider", "--uuid", "doctor-env-uuid")
	runCage(t, bin, configEnv, "", "profile", "create", "doctor-profile", "--environments", "doctor-env")

	result := runCage(t, bin, configEnv, "", "doctor", "--json")
	assertNotContains(t, result.stdout, fakeToken)
	assertNotContains(t, result.stderr, fakeToken)
	report := decodeDoctorReport(t, result.stdout)
	assertEqual(t, report.Status, "ok")
	assertEqual(t, report.Failures, 0)
	assertEqual(t, report.Warnings, 0)
	assertDoctorIntegrationCheck(t, report, "ok", "config.references")
	assertDoctorIntegrationCheck(t, report, "ok", "identity.doctor-local.markers")
	assertDoctorIntegrationCheck(t, report, "ok", "identity.doctor-local.recipient")
	assertDoctorIntegrationCheck(t, report, "ok", "provider.doctor-provider.ciphertext")

	providerPath := filepath.Join(configDir, "doctor-provider.1p.age")
	if err := writePrivateFile(providerPath, []byte("not an age ciphertext")); err != nil {
		t.Fatal(err)
	}
	failure, code := runCageFailure(t, bin, configEnv, "", "doctor", "--json")
	assertEqual(t, code, 1)
	assertNotContains(t, failure.stdout, fakeToken)
	assertNotContains(t, failure.stderr, fakeToken)
	report = decodeDoctorReport(t, failure.stdout)
	assertEqual(t, report.Status, "fail")
	assertDoctorIntegrationCheck(t, report, "fail", "provider.doctor-provider.ciphertext")

	emptyConfigPath := filepath.Join(privateTempDir(t), "config.toml")
	if err := writePrivateFile(emptyConfigPath, []byte("# empty cage config\n")); err != nil {
		t.Fatal(err)
	}
	emptyEnv := []string{
		"CAGE_CONFIG=" + emptyConfigPath,
		"XDG_CACHE_HOME=" + filepath.Join(privateTempDir(t), "cache"),
		"XDG_STATE_HOME=" + filepath.Join(privateTempDir(t), "state"),
	}
	result = runCage(t, bin, emptyEnv, "", "doctor", "--json")
	report = decodeDoctorReport(t, result.stdout)
	assertEqual(t, report.Status, "warn")
	if report.Warnings == 0 {
		t.Fatalf("doctor warnings = 0, want at least one: %#v", report)
	}
	failure, code = runCageFailure(t, bin, emptyEnv, "", "doctor", "--json", "--strict")
	assertEqual(t, code, 1)
	report = decodeDoctorReport(t, failure.stdout)
	assertEqual(t, report.Status, "warn")
	if !strings.Contains(failure.stderr, "doctor found") {
		t.Fatalf("strict doctor stderr missing summary:\n%s", failure.stderr)
	}
}

func decodeDoctorReport(t *testing.T, data string) doctorIntegrationReport {
	t.Helper()
	var report doctorIntegrationReport
	if err := json.Unmarshal([]byte(data), &report); err != nil {
		t.Fatalf("parse doctor JSON: %v\n%s", err, data)
	}
	return report
}

func assertDoctorIntegrationCheck(t *testing.T, report doctorIntegrationReport, status string, id string) doctorIntegrationCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			if check.Status != status {
				t.Fatalf("doctor check %s status = %s, want %s", id, check.Status, status)
			}
			return check
		}
	}
	t.Fatalf("doctor check %s not found in %#v", id, report.Checks)
	return doctorIntegrationCheck{}
}
