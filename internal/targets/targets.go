// Package targets resolves logical target names to pprof endpoints and
// enforces the host allowlist before any network access.
package targets

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/h0rn3t/inspector-gopher/internal/config"
)

// Registry resolves target names against configuration.
type Registry struct {
	cfg config.Config
}

// New builds a Registry from config.
func New(cfg config.Config) *Registry {
	return &Registry{cfg: cfg}
}

// Endpoint is a resolved collection endpoint.
type Endpoint struct {
	Name       string
	PprofURL   string
	Collection string // "local-http" or "http-via-published-port"
}

// Resolve maps a target name to its pprof base URL. Only configured local
// targets or hosts matching the docker allowlist patterns are permitted;
// anything else returns a forbidden error without network access.
func (r *Registry) Resolve(name string) (Endpoint, error) {
	for _, t := range r.cfg.Local {
		if t.Name == name {
			return Endpoint{Name: name, PprofURL: strings.TrimSuffix(t.PprofURL, "/"), Collection: "local-http"}, nil
		}
	}
	if ep, ok := r.matchAllowlist(name); ok {
		return ep, nil
	}
	// Unknown names are rejected as forbidden (never probed) to avoid SSRF.
	return Endpoint{}, &Error{
		Code: "forbidden_target", Retryable: false,
		Message: fmt.Sprintf("target %q is not configured and matches no allowlist pattern", name),
	}
}

// matchAllowlist treats the name as host[:port] or URL and checks it against
// docker allowlist globs. A match yields an http endpoint on the default
// pprof port when no scheme is present.
func (r *Registry) matchAllowlist(name string) (Endpoint, bool) {
	host := name
	var base string
	if strings.Contains(name, "://") {
		u, err := url.Parse(name)
		if err != nil {
			return Endpoint{}, false
		}
		host = u.Hostname()
		base = strings.TrimSuffix(name, "/")
	} else if h, _, err := splitHostPort(name); err == nil {
		host = h
		base = "http://" + name
	} else {
		base = fmt.Sprintf("http://%s:%d", name, r.cfg.Docker.DefaultPprofPort)
	}
	for _, pattern := range r.cfg.Docker.Allowlist {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if ok, _ := path.Match(pattern, host); ok {
			return Endpoint{Name: name, PprofURL: strings.TrimSuffix(base, "/"), Collection: "http-via-published-port"}, true
		}
		if ok, _ := path.Match(pattern, name); ok {
			return Endpoint{Name: name, PprofURL: strings.TrimSuffix(base, "/"), Collection: "http-via-published-port"}, true
		}
	}
	return Endpoint{}, false
}

func splitHostPort(s string) (string, string, error) {
	// Accept host:port without scheme.
	if !strings.Contains(s, ":") {
		return "", "", fmt.Errorf("no port")
	}
	host, port, _ := strings.Cut(s, ":")
	if host == "" || port == "" || strings.Contains(port, "/") {
		return "", "", fmt.Errorf("bad host:port")
	}
	return host, port, nil
}

// Error is a typed MCP error with retry hint.
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

// Forbidden builds a forbidden_target error.
func Forbidden(target string) *Error {
	return &Error{
		Code: "forbidden_target", Retryable: false,
		Message: fmt.Sprintf("target %q is not allowed", target),
		Hint:    "add it to targets.yaml local list or docker allowlist",
	}
}

// Unknown builds an unknown_target error.
func Unknown(target string) *Error {
	return &Error{
		Code: "unknown_target", Retryable: false,
		Message: fmt.Sprintf("target %q is not configured", target),
	}
}
