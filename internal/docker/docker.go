// Package docker discovers Go targets in containers and collects profiles
// either via a published pprof port or via docker exec with a fixed
// command allowlist (no shell injection surface).
package docker

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"time"
)

// Container is a running container with pprof reachability info.
type Container struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Image          string `json:"image"`
	Ports          []int  `json:"ports"`
	PprofReachable bool   `json:"pprof_reachable"`
	PprofURL       string `json:"pprof_url,omitempty"`
}

// Client abstracts the Docker API for testability.
type Client interface {
	ListContainers(ctx context.Context) ([]Container, error)
	Exec(ctx context.Context, containerID string, cmd []string, timeout time.Duration) ([]byte, error)
	ProbeURL(ctx context.Context, url string, timeout time.Duration) bool
	Close() error
}

// Lister collects container targets.
type Lister struct {
	client      Client
	allowlist   []string
	pprofPort   int
	probeTimout time.Duration
	audit       *slog.Logger
}

// NewLister builds a Lister.
func NewLister(c Client, allowlist []string, pprofPort int, audit *slog.Logger) *Lister {
	if pprofPort <= 0 {
		pprofPort = 6060
	}
	if audit == nil {
		audit = slog.Default()
	}
	return &Lister{client: c, allowlist: allowlist, pprofPort: pprofPort, probeTimout: 3 * time.Second, audit: audit}
}

// Allowed reports whether a container name/ID matches the allowlist.
// An empty allowlist denies everything.
func (l *Lister) Allowed(name, id string, labels map[string]string) bool {
	for _, pattern := range l.allowlist {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if ok, _ := path.Match(pattern, name); ok {
			return true
		}
		if ok, _ := path.Match(pattern, id); ok {
			return true
		}
		if strings.HasPrefix(pattern, "label:") {
			kv := strings.TrimPrefix(pattern, "label:")
			k, v, _ := strings.Cut(kv, "=")
			if lv, ok := labels[k]; ok && (v == "" || lv == v) {
				return true
			}
		}
	}
	return false
}

// Targets lists running containers, probing published ports for pprof.
func (l *Lister) Targets(ctx context.Context) ([]Container, error) {
	containers, err := l.client.ListContainers(ctx)
	if err != nil {
		return nil, &Error{Code: "docker_unavailable", Retryable: false,
			Message: fmt.Sprintf("list containers: %v", err),
			Hint:    "check DOCKER_HOST and socket permissions"}
	}
	for i, c := range containers {
		for _, port := range c.Ports {
			url := fmt.Sprintf("http://127.0.0.1:%d/debug/pprof/", port)
			if l.client.ProbeURL(ctx, url, l.probeTimout) {
				containers[i].PprofReachable = true
				containers[i].PprofURL = fmt.Sprintf("http://127.0.0.1:%d", port)
				break
			}
		}
	}
	return containers, nil
}

// AllowedProfiles are the only profile kinds fetchable via exec.
var AllowedProfiles = map[string]bool{"cpu": true, "heap": true, "goroutine": true, "trace": true}

// BuildExecCommand returns the fixed argv for fetching a profile inside a
// container. kind and seconds are validated; no user input is interpolated
// into shell — the result is an argv array, never a shell string.
func BuildExecCommand(tool, kind string, seconds int) ([]string, error) {
	if !AllowedProfiles[kind] {
		return nil, fmt.Errorf("profile kind %q not allowed via exec", kind)
	}
	if seconds < 0 {
		return nil, fmt.Errorf("seconds must be >= 0")
	}
	var pprofPath string
	switch kind {
	case "cpu":
		if seconds < 5 || seconds > 120 {
			return nil, fmt.Errorf("seconds must be 5..120 for cpu")
		}
		pprofPath = fmt.Sprintf("http://127.0.0.1:6060/debug/pprof/profile?seconds=%d", seconds)
	case "heap":
		pprofPath = "http://127.0.0.1:6060/debug/pprof/heap"
	case "goroutine":
		pprofPath = "http://127.0.0.1:6060/debug/pprof/goroutine?debug=0"
	case "trace":
		if seconds < 1 || seconds > 30 {
			return nil, fmt.Errorf("seconds must be 1..30 for trace")
		}
		pprofPath = fmt.Sprintf("http://127.0.0.1:6060/debug/pprof/trace?seconds=%d", seconds)
	}
	switch tool {
	case "curl":
		return []string{"curl", "-sS", "--max-time", strconv.Itoa(seconds + 15), pprofPath}, nil
	case "wget":
		return []string{"wget", "-qO-", pprofPath}, nil
	}
	return nil, fmt.Errorf("unknown fetch tool %q", tool)
}

// ExecCollect runs the allowlisted fetch inside the container after an
// allowlist check, with audit logging.
func (l *Lister) ExecCollect(ctx context.Context, name, id string, labels map[string]string, kind string, seconds int, fetchTool string) ([]byte, error) {
	if !l.Allowed(name, id, labels) {
		return nil, &Error{Code: "forbidden_target", Retryable: false,
			Message: fmt.Sprintf("container %q is not in the allowlist", name)}
	}
	cmd, err := BuildExecCommand(fetchTool, kind, seconds)
	if err != nil {
		return nil, err
	}
	l.audit.Info("docker exec collect", "container", name, "kind", kind, "tool", fetchTool)
	timeout := time.Duration(seconds+15) * time.Second
	data, err := l.client.Exec(ctx, id, cmd, timeout)
	if err != nil {
		if strings.Contains(err.Error(), "executable file not found") || strings.Contains(err.Error(), "not found") {
			return nil, &Error{Code: "exec_tooling_missing", Retryable: false,
				Message: fmt.Sprintf("container %q has no %s", name, fetchTool),
				Hint:    "publish the pprof port (-p 6060:6060) or add curl to the image"}
		}
		return nil, err
	}
	return data, nil
}

// Error is a typed MCP error.
type Error struct {
	Code      string
	Retryable bool
	Message   string
	Hint      string
}

func (e *Error) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Hint)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
