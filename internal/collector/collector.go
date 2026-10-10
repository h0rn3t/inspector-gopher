// Package collector fetches pprof artifacts from a target's HTTP endpoint
// and stores them with size limits and validation.
package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/h0rn3t/inspector-gopher/internal/store"
	"github.com/h0rn3t/inspector-gopher/internal/targets"
)

// Bounds for collection durations.
const (
	MinCPUSconds   = 5
	MaxCPUSconds   = 120
	DefaultCPUSecs = 30
	MaxTraceSecs   = 30
)

// Collector fetches profiles over HTTP.
type Collector struct {
	client *http.Client
	store  *store.Store
	maxB   int64
	mu     sync.Mutex
	active map[string]bool // serialized per target+kind
}

// New builds a Collector with per-request timeouts and a blob size cap.
func New(st *store.Store, maxBytes int64, timeout time.Duration) *Collector {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Collector{
		client: &http.Client{Timeout: timeout},
		store:  st, maxB: maxBytes,
		active: make(map[string]bool),
	}
}

// ValidateSeconds checks cpu/trace durations.
func ValidateSeconds(kind string, seconds int) error {
	switch kind {
	case "cpu":
		if seconds < MinCPUSconds || seconds > MaxCPUSconds {
			return fmt.Errorf("seconds must be %d..%d for cpu profiles, got %d", MinCPUSconds, MaxCPUSconds, seconds)
		}
	case "trace":
		if seconds < 1 || seconds > MaxTraceSecs {
			return fmt.Errorf("seconds must be 1..%d for traces, got %d", MaxTraceSecs, seconds)
		}
	}
	return nil
}

// Collect fetches one profile kind from a resolved endpoint.
func (c *Collector) Collect(ctx context.Context, ep targets.Endpoint, kind string, seconds int) (*store.Profile, error) {
	path, format, sampleType, units := endpointFor(kind, seconds)
	if path == "" {
		return nil, fmt.Errorf("unknown profile kind %q", kind)
	}
	if err := ValidateSeconds(kind, seconds); err != nil {
		return nil, err
	}
	key := ep.Name + "|" + kind
	c.mu.Lock()
	if c.active[key] {
		c.mu.Unlock()
		return nil, fmt.Errorf("collection of %s already in progress for target %q", kind, ep.Name)
	}
	c.active[key] = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.active, key)
		c.mu.Unlock()
	}()

	url := ep.PprofURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, &targets.Error{Code: "pprof_unavailable", Retryable: true,
			Message: fmt.Sprintf("GET %s failed: %v", url, err),
			Hint:    "check that the target imports net/http/pprof and the port is reachable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &targets.Error{Code: "pprof_unavailable", Retryable: true,
			Message: fmt.Sprintf("GET %s: status %s", url, resp.Status),
			Hint:    "check that the target imports net/http/pprof"}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxB+1))
	if err != nil {
		return nil, fmt.Errorf("read profile body: %w", err)
	}
	if int64(len(data)) > c.maxB {
		return nil, &targets.Error{Code: "profile_too_large", Retryable: false,
			Message: fmt.Sprintf("profile exceeds %d bytes; reduce seconds", c.maxB)}
	}
	meta := map[string]any{"collection": ep.Collection, "sample_type": sampleType, "units": units}
	if kind == "cpu" || kind == "trace" {
		meta["duration_s"] = seconds
	}
	if kind == "trace" && int64(len(data)) > c.maxB/2 {
		meta["warning"] = "trace is large; consider fewer seconds"
	}
	return c.store.Put(ep.Name, kind, format, data, meta), nil
}

// CollectText fetches a debug=1 text dump (goroutine stacks).
func (c *Collector) CollectText(ctx context.Context, ep targets.Endpoint, kind string) (string, error) {
	var path string
	switch kind {
	case "goroutine":
		path = "/debug/pprof/goroutine?debug=1"
	default:
		return "", fmt.Errorf("no text dump for kind %q", kind)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.PprofURL+path, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", &targets.Error{Code: "pprof_unavailable", Retryable: true, Message: err.Error()}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxB+1))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// RuntimeStats holds lightweight process stats scraped without profiling.
type RuntimeStats struct {
	NumGoroutine int            `json:"num_goroutine"`
	GoMaxProcs   int            `json:"gomaxprocs"`
	Mem          map[string]any `json:"mem_stats"`
	CollectedAt  string         `json:"collected_at"`
}

// FetchStats scrapes goroutine count (via debug=1 header) and expvar-style
// debug vars when available. It never stops the target.
func (c *Collector) FetchStats(ctx context.Context, ep targets.Endpoint) (*RuntimeStats, error) {
	dump, err := c.CollectText(ctx, ep, "goroutine")
	if err != nil {
		return nil, err
	}
	count := 0
	for _, line := range strings.Split(dump, "\n") {
		if strings.HasPrefix(line, "goroutine ") {
			count++
		}
	}
	return &RuntimeStats{
		NumGoroutine: count,
		Mem:          map[string]any{"note": "detailed MemStats require target cooperation; see runtime_stats doc"},
		CollectedAt:  time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func endpointFor(kind string, seconds int) (path, format, sampleType, units string) {
	switch kind {
	case "cpu":
		return fmt.Sprintf("/debug/pprof/profile?seconds=%d", seconds), "pprof-protobuf", "samples/cpu", "nanoseconds"
	case "heap":
		return "/debug/pprof/heap", "pprof-protobuf", "alloc_space/inuse_space", "bytes"
	case "mutex":
		return "/debug/pprof/mutex", "pprof-protobuf", "contentions/delay", "nanoseconds"
	case "block":
		return "/debug/pprof/block", "pprof-protobuf", "contentions/delay", "nanoseconds"
	case "goroutine":
		return "/debug/pprof/goroutine?debug=0", "pprof-protobuf", "goroutines", "count"
	case "trace":
		return fmt.Sprintf("/debug/pprof/trace?seconds=%d", seconds), "go-trace", "trace", "bytes"
	}
	return "", "", "", ""
}
