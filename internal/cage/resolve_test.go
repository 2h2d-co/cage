package cage

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	onepassword "github.com/1password/onepassword-sdk-go"
)

func TestResolveVariablesReusesProviderClient(t *testing.T) {
	cfg := resolveTestConfig()
	cfg.Environments["dev"] = EnvironmentConfig{Type: EnvironmentType1Password, Provider: "project", UUID: "dev-uuid"}
	cfg.Environments["stage"] = EnvironmentConfig{Type: EnvironmentType1Password, Provider: "project", UUID: "stage-uuid"}

	api := &fakeEnvironmentsAPI{responses: map[string]onepassword.GetVariablesResponse{
		"dev-uuid": {
			Variables: []onepassword.EnvironmentVariable{{Name: "DEV_ONLY", Value: "dev"}},
		},
		"stage-uuid": {
			Variables: []onepassword.EnvironmentVariable{{Name: "STAGE_ONLY", Value: "stage"}},
		},
	}}

	decryptCalls := 0
	factoryCalls := 0
	app := &App{
		decryptProviderToken: func(_ *Config, providerName string) (string, error) {
			decryptCalls++
			if providerName != "project" {
				return "", fmt.Errorf("unexpected provider %q", providerName)
			}
			return "token-project", nil
		},
		newOnePasswordEnvironments: func(_ context.Context, token, version string) (onepassword.EnvironmentsAPI, error) {
			factoryCalls++
			if token != "token-project" {
				return nil, fmt.Errorf("unexpected token %q", token)
			}
			return api, nil
		},
	}

	variables, err := app.resolveVariables(context.Background(), cfg, Selection{Environments: []string{"dev", "stage"}})
	if err != nil {
		t.Fatal(err)
	}
	if decryptCalls != 1 {
		t.Fatalf("decrypt calls = %d, want 1", decryptCalls)
	}
	if factoryCalls != 1 {
		t.Fatalf("client factory calls = %d, want 1", factoryCalls)
	}
	if variables["DEV_ONLY"] != "dev" || variables["STAGE_ONLY"] != "stage" {
		t.Fatalf("variables = %#v", variables)
	}

	calls := api.callsSnapshot()
	sort.Strings(calls)
	wantCalls := []string{"dev-uuid", "stage-uuid"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("GetVariables calls = %#v, want %#v", calls, wantCalls)
	}
}

func TestResolveVariablesFetchesConcurrentlyAndMergesInSelectionOrder(t *testing.T) {
	cfg := resolveTestConfig()
	cfg.Environments["first"] = EnvironmentConfig{Type: EnvironmentType1Password, Provider: "project", UUID: "first-uuid"}
	cfg.Environments["second"] = EnvironmentConfig{Type: EnvironmentType1Password, Provider: "project", UUID: "second-uuid"}

	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	var firstOnce sync.Once
	var secondOnce sync.Once

	api := &fakeEnvironmentsAPI{
		onGet: func(ctx context.Context, environmentID string) (onepassword.GetVariablesResponse, error) {
			switch environmentID {
			case "first-uuid":
				firstOnce.Do(func() { close(firstStarted) })
				select {
				case <-secondStarted:
				case <-ctx.Done():
					return onepassword.GetVariablesResponse{}, ctx.Err()
				}
				return onepassword.GetVariablesResponse{Variables: []onepassword.EnvironmentVariable{
					{Name: "SHARED", Value: "first"},
				}}, nil
			case "second-uuid":
				secondOnce.Do(func() { close(secondStarted) })
				return onepassword.GetVariablesResponse{Variables: []onepassword.EnvironmentVariable{
					{Name: "SHARED", Value: "second"},
				}}, nil
			default:
				return onepassword.GetVariablesResponse{}, fmt.Errorf("unexpected environment %q", environmentID)
			}
		},
	}
	app := &App{
		decryptProviderToken: func(_ *Config, _ string) (string, error) { return "token", nil },
		newOnePasswordEnvironments: func(context.Context, string, string) (onepassword.EnvironmentsAPI, error) {
			return api, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	variables, err := app.resolveVariables(ctx, cfg, Selection{Environments: []string{"first", "second"}})
	if err != nil {
		t.Fatal(err)
	}
	if variables["SHARED"] != "second" {
		t.Fatalf("SHARED = %q, want second", variables["SHARED"])
	}
}

func TestResolveVariablesRejectsInvalidVariableName(t *testing.T) {
	cfg := resolveTestConfig()
	cfg.Environments["dev"] = EnvironmentConfig{Type: EnvironmentType1Password, Provider: "project", UUID: "dev-uuid"}

	api := &fakeEnvironmentsAPI{responses: map[string]onepassword.GetVariablesResponse{
		"dev-uuid": {
			Variables: []onepassword.EnvironmentVariable{{Name: "BAD=NAME", Value: "value"}},
		},
	}}
	app := &App{
		decryptProviderToken: func(_ *Config, _ string) (string, error) { return "token", nil },
		newOnePasswordEnvironments: func(context.Context, string, string) (onepassword.EnvironmentsAPI, error) {
			return api, nil
		},
	}

	_, err := app.resolveVariables(context.Background(), cfg, Selection{Environments: []string{"dev"}})
	if err == nil {
		t.Fatal("resolveVariables accepted an invalid variable name")
	}
	if !strings.Contains(err.Error(), "invalid variable name") || !strings.Contains(err.Error(), "contains =") {
		t.Fatalf("error = %q, want invalid variable name containing =", err)
	}
}

func TestValidateEnvironmentVariableName(t *testing.T) {
	for _, name := range []string{"FOO", "1", "with-dash", "with.dot"} {
		if err := validateEnvironmentVariableName(name); err != nil {
			t.Fatalf("validateEnvironmentVariableName(%q) returned %v", name, err)
		}
	}

	cases := map[string]string{
		"":            "empty",
		"BAD=NAME":    "contains =",
		"BAD\x00NAME": "contains NUL",
	}
	for name, want := range cases {
		err := validateEnvironmentVariableName(name)
		if err == nil {
			t.Fatalf("validateEnvironmentVariableName(%q) succeeded", name)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validateEnvironmentVariableName(%q) = %q, want %q", name, err, want)
		}
	}
}

func resolveTestConfig() *Config {
	cfg := emptyConfig("/tmp/cage-test/config.toml")
	cfg.Identities["identity"] = IdentityConfig{Type: IdentityTypeBasic, File: "identity.age"}
	cfg.Providers["project"] = ProviderConfig{Type: ProviderType1Password, Identity: "identity", File: "project.1p.age"}
	return cfg
}

type fakeEnvironmentsAPI struct {
	mu        sync.Mutex
	responses map[string]onepassword.GetVariablesResponse
	calls     []string
	onGet     func(context.Context, string) (onepassword.GetVariablesResponse, error)
}

func (f *fakeEnvironmentsAPI) GetVariables(ctx context.Context, environmentID string) (onepassword.GetVariablesResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, environmentID)
	f.mu.Unlock()

	if f.onGet != nil {
		return f.onGet(ctx, environmentID)
	}
	response, ok := f.responses[environmentID]
	if !ok {
		return onepassword.GetVariablesResponse{}, fmt.Errorf("unexpected environment %q", environmentID)
	}
	return response, nil
}

func (f *fakeEnvironmentsAPI) callsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}
