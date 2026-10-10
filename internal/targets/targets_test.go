package targets

import (
	"errors"
	"testing"

	"github.com/h0rn3t/inspector-gopher/internal/config"
)

func testConfig() config.Config {
	cfg := config.Defaults()
	cfg.Local = []config.LocalTarget{{Name: "api-local", PprofURL: "http://127.0.0.1:6060/"}}
	cfg.Docker.Allowlist = []string{"api-*", "127.0.0.1"}
	return cfg
}

func TestResolveLocalTarget(t *testing.T) {
	r := New(testConfig())
	ep, err := r.Resolve("api-local")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ep.PprofURL != "http://127.0.0.1:6060" {
		t.Errorf("PprofURL = %q (trailing slash must be trimmed)", ep.PprofURL)
	}
	if ep.Collection != "local-http" {
		t.Errorf("Collection = %q", ep.Collection)
	}
}

func TestResolveAllowlistedHost(t *testing.T) {
	r := New(testConfig())
	ep, err := r.Resolve("api-staging")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ep.Collection != "http-via-published-port" {
		t.Errorf("Collection = %q", ep.Collection)
	}
}

func TestResolveForbiddenWithoutNetwork(t *testing.T) {
	r := New(testConfig())
	_, err := r.Resolve("evil.example.com")
	if err == nil {
		t.Fatal("expected forbidden error")
	}
	var terr *Error
	if !errors.As(err, &terr) {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if terr.Code != "forbidden_target" {
		t.Errorf("code = %q", terr.Code)
	}
	if terr.Retryable {
		t.Error("forbidden must not be retryable")
	}
}

func TestErrorHelpers(t *testing.T) {
	if Forbidden("x").Code != "forbidden_target" {
		t.Error("Forbidden code")
	}
	if Unknown("x").Code != "unknown_target" {
		t.Error("Unknown code")
	}
}
