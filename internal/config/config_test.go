package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsWhenNoFile(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Limits.MaxProfileMB != 64 {
		t.Errorf("MaxProfileMB = %d, want 64", cfg.Limits.MaxProfileMB)
	}
	if cfg.MaxProfileBytes() != 64<<20 {
		t.Errorf("MaxProfileBytes wrong: %d", cfg.MaxProfileBytes())
	}
	if !cfg.Server.Stdio {
		t.Error("Stdio default should be true")
	}
}

func TestLoadMissingFileIsNotError(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
}

func TestLoadYAMLAndEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.yaml")
	content := `
local:
  - name: api-local
    pprof_url: http://127.0.0.1:6060
docker:
  socket: unix:///var/run/docker.sock
  allowlist: ["api-*"]
  default_pprof_port: 6060
limits:
  max_profile_mb: 32
  default_timeout_s: 45
  max_cpu_seconds: 60
  max_trace_seconds: 20
server:
  http_addr: 127.0.0.1:8080
  stdio: true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("INSPECTOR_MAX_PROFILE_MB", "16")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Local) != 1 || cfg.Local[0].Name != "api-local" {
		t.Fatalf("local targets: %+v", cfg.Local)
	}
	if cfg.Limits.MaxProfileMB != 16 {
		t.Errorf("env override MaxProfileMB = %d, want 16", cfg.Limits.MaxProfileMB)
	}
	if cfg.Server.HTTPAddr != "127.0.0.1:8080" {
		t.Errorf("HTTPAddr = %q", cfg.Server.HTTPAddr)
	}
}

func TestLoadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.json")
	if err := os.WriteFile(path, []byte(`{"local":[{"name":"a","pprof_url":"http://x:6060"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Local) != 1 {
		t.Fatalf("local: %+v", cfg.Local)
	}
}

func TestValidateRejectsBadConfig(t *testing.T) {
	cfg := Defaults()
	cfg.Limits.MaxProfileMB = 0
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for MaxProfileMB=0")
	}
	cfg = Defaults()
	cfg.Local = []LocalTarget{{Name: "x"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for target without url")
	}
	cfg = Defaults()
	cfg.Server.HTTPAddr = "not-an-addr"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for bad http_addr")
	}
}
