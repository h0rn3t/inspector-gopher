package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/h0rn3t/inspector-gopher/internal/analyzer"
	"github.com/h0rn3t/inspector-gopher/internal/collector"
	"github.com/h0rn3t/inspector-gopher/internal/store"
	"github.com/h0rn3t/inspector-gopher/internal/targets"
)

func (s *Server) collectLocal(ctx context.Context, target, kind string, seconds int) (*profileOut, error) {
	if target == "" {
		return nil, targets.Unknown(target)
	}
	ep, err := s.deps.Registry.Resolve(target)
	if err != nil {
		return nil, err
	}
	p, err := s.deps.Collector.Collect(ctx, ep, kind, seconds)
	if err != nil {
		return nil, err
	}
	return profileToOut(p, true), nil
}

func (s *Server) collectGoroutine(ctx context.Context, target string) (*profileOut, error) {
	if target == "" {
		return nil, targets.Unknown(target)
	}
	ep, err := s.deps.Registry.Resolve(target)
	if err != nil {
		return nil, err
	}
	p, err := s.deps.Collector.Collect(ctx, ep, "goroutine", 0)
	if err != nil {
		return nil, err
	}
	dump, derr := s.deps.Collector.CollectText(ctx, ep, "goroutine")
	if derr == nil {
		// Keep the dump bounded for the wire.
		if len(dump) > 20000 {
			dump = dump[:20000] + "\n...[truncated]..."
		}
		p.TextDump = dump
		p.Size += int64(len(dump))
	}
	_ = collector.MinCPUSconds
	return profileToOut(p, true), nil
}

func profileToOut(p *store.Profile, inline bool) *profileOut {
	out := &profileOut{
		ProfileID: p.ID, Target: p.Target, Kind: p.Kind, Format: p.Format,
		Size: p.Size, DurationS: p.DurationS, Collection: p.Collection,
		SampleType: p.SampleType, TextDump: p.TextDump, Warning: p.Warning,
	}
	if inline && len(p.Data) > 0 {
		out.DataBase64 = base64.StdEncoding.EncodeToString(p.Data)
	}
	return out
}

func (s *Server) dockerProfile(ctx context.Context, containerName, kind string, seconds int) (*profileOut, error) {
	if s.deps.Docker == nil {
		return nil, &dockerError{Code: "docker_unavailable", Retryable: false, Message: "docker is not configured"}
	}
	if containerName == "" || kind == "" {
		return nil, fmt.Errorf("container and profile are required")
	}
	if kind == "cpu" && seconds == 0 {
		seconds = collector.DefaultCPUSecs
	}
	if kind == "trace" && seconds == 0 {
		seconds = 5
	}
	// 1. Try published-port HTTP via Targets probe.
	containers, err := s.deps.Docker.Targets(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range containers {
		if c.Name != containerName && c.ID != containerName {
			continue
		}
		if c.PprofReachable && c.PprofURL != "" {
			ep := targets.Endpoint{Name: containerName, PprofURL: c.PprofURL, Collection: "http-via-published-port"}
			p, err := s.deps.Collector.Collect(ctx, ep, kind, seconds)
			if err != nil {
				return nil, err
			}
			return profileToOut(p, true), nil
		}
		// 2. Fall back to exec inside the container.
		tool := "curl"
		data, err := s.deps.Docker.ExecCollect(ctx, c.Name, c.ID, nil, kind, seconds, tool)
		if err != nil {
			if isMissingTooling(err) {
				tool = "wget"
				data, err = s.deps.Docker.ExecCollect(ctx, c.Name, c.ID, nil, kind, seconds, tool)
			}
			if err != nil {
				return nil, err
			}
		}
		format := "pprof-protobuf"
		if kind == "trace" {
			format = "go-trace"
		}
		p := s.deps.Store.Put(containerName, kind, format, data, map[string]any{
			"collection": "docker-exec", "duration_s": seconds,
		})
		return profileToOut(p, true), nil
	}
	return nil, targets.Unknown(containerName)
}

func isMissingTooling(err error) bool {
	return strings.Contains(err.Error(), "exec_tooling_missing")
}

type dockerError struct {
	Code      string
	Retryable bool
	Message   string
}

func (e *dockerError) Error() string { return e.Code + ": " + e.Message }

func (s *Server) analyze(id string, topN int) (*analyzer.Report, error) {
	p, ok := s.deps.Store.Get(id)
	if !ok {
		return nil, fmt.Errorf("profile_not_found: %q (see profile://recent for known IDs: %v)", id, s.deps.Store.IDs())
	}
	prof, err := analyzer.Parse(p.Data)
	if err != nil {
		return nil, err
	}
	return analyzer.Analyze(id, prof, topN)
}

func (s *Server) diff(baseID, curID string) (map[string]any, error) {
	bp, ok := s.deps.Store.Get(baseID)
	if !ok {
		return nil, fmt.Errorf("profile_not_found: %q", baseID)
	}
	cp, ok := s.deps.Store.Get(curID)
	if !ok {
		return nil, fmt.Errorf("profile_not_found: %q", curID)
	}
	base, err := analyzer.Parse(bp.Data)
	if err != nil {
		return nil, err
	}
	cur, err := analyzer.Parse(cp.Data)
	if err != nil {
		return nil, err
	}
	reg, imp, bt, ct := analyzer.Diff(base, cur)
	summary := fmt.Sprintf("total %d → %d (delta %+d)", bt, ct, ct-bt)
	return map[string]any{
		"regressions": reg, "improvements": imp,
		"base_total": bt, "current_total": ct, "summary": summary,
	}, nil
}

func (s *Server) flamegraph(id, format string) (map[string]any, error) {
	p, ok := s.deps.Store.Get(id)
	if !ok {
		return nil, fmt.Errorf("profile_not_found: %q", id)
	}
	prof, err := analyzer.Parse(p.Data)
	if err != nil {
		return nil, err
	}
	switch format {
	case "", "folded":
		folded, total := analyzer.Folded(prof)
		return map[string]any{"format": "folded", "folded": folded, "total": total, "sample_type": p.SampleType}, nil
	case "tree":
		tree, truncated := analyzer.Tree(prof, 200, 5000)
		return map[string]any{"format": "tree", "root": tree, "truncated": truncated}, nil
	default:
		return nil, fmt.Errorf("unknown flamegraph format %q (want folded or tree)", format)
	}
}
