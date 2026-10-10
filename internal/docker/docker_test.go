package docker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeClient struct {
	containers []Container
	probe      map[string]bool
	execErr    error
	lastCmd    []string
}

func (f *fakeClient) ListContainers(ctx context.Context) ([]Container, error) {
	return f.containers, nil
}

func (f *fakeClient) Exec(ctx context.Context, id string, cmd []string, timeout time.Duration) ([]byte, error) {
	f.lastCmd = cmd
	if f.execErr != nil {
		return nil, f.execErr
	}
	return []byte("profile-bytes"), nil
}

func (f *fakeClient) ProbeURL(ctx context.Context, url string, timeout time.Duration) bool {
	return f.probe[url]
}

func (f *fakeClient) Close() error { return nil }

func TestTargetsProbePublishedPort(t *testing.T) {
	fc := &fakeClient{
		containers: []Container{{ID: "abc", Name: "api", Image: "api:latest", Ports: []int{6060}}},
		probe:      map[string]bool{"http://127.0.0.1:6060/debug/pprof/": true},
	}
	l := NewLister(fc, []string{"api"}, 6060, nil)
	got, err := l.Targets(context.Background())
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if !got[0].PprofReachable || got[0].PprofURL != "http://127.0.0.1:6060" {
		t.Errorf("probe result: %+v", got[0])
	}
}

func TestAllowedMatching(t *testing.T) {
	l := NewLister(nil, []string{"api-*", "label:team=backend"}, 6060, nil)
	if !l.Allowed("api-1", "zzz", nil) {
		t.Error("glob should match")
	}
	if !l.Allowed("other", "zzz", map[string]string{"team": "backend"}) {
		t.Error("label should match")
	}
	if l.Allowed("prod-db", "zzz", nil) {
		t.Error("prod-db must be denied")
	}
	empty := NewLister(nil, nil, 6060, nil)
	if empty.Allowed("api-1", "x", nil) {
		t.Error("empty allowlist denies everything")
	}
}

func TestBuildExecCommandNoInjection(t *testing.T) {
	// A malicious container name must never reach the command: only kind and
	// seconds shape it, and the result is argv, not shell.
	cmd, err := BuildExecCommand("curl", "cpu", 10)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	joined := strings.Join(cmd, " ")
	if strings.Contains(joined, ";") || strings.Contains(joined, "$(") {
		t.Errorf("command must not contain shell: %q", joined)
	}
	if cmd[0] != "curl" {
		t.Errorf("argv[0] = %q", cmd[0])
	}
	if _, err := BuildExecCommand("curl", "cpu; rm -rf /", 10); err == nil {
		t.Error("injected kind must be rejected")
	}
	if _, err := BuildExecCommand("curl", "cpu", -1); err == nil {
		t.Error("negative seconds must be rejected")
	}
	if _, err := BuildExecCommand("sh", "heap", 0); err == nil {
		t.Error("unknown tool must be rejected")
	}
}

func TestExecCollectForbiddenBeforeDocker(t *testing.T) {
	fc := &fakeClient{}
	l := NewLister(fc, []string{"api"}, 6060, nil)
	_, err := l.ExecCollect(context.Background(), "prod-db", "id", nil, "heap", 0, "curl")
	var derr *Error
	if !errors.As(err, &derr) || derr.Code != "forbidden_target" {
		t.Fatalf("got %v", err)
	}
	if fc.lastCmd != nil {
		t.Error("Docker must not be touched for forbidden containers")
	}
}

func TestExecCollectMissingTooling(t *testing.T) {
	fc := &fakeClient{execErr: errors.New("executable file not found in $PATH")}
	l := NewLister(fc, []string{"*"}, 6060, nil)
	_, err := l.ExecCollect(context.Background(), "api", "id", nil, "heap", 0, "curl")
	var derr *Error
	if !errors.As(err, &derr) || derr.Code != "exec_tooling_missing" {
		t.Fatalf("got %v", err)
	}
}
