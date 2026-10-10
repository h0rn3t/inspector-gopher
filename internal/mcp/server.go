// Package mcp implements the inspector-gopher MCP server: tool catalog,
// resources, transports and health tracking.
package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/h0rn3t/inspector-gopher/internal/collector"
	"github.com/h0rn3t/inspector-gopher/internal/config"
	"github.com/h0rn3t/inspector-gopher/internal/debug"
	dockerpkg "github.com/h0rn3t/inspector-gopher/internal/docker"
	"github.com/h0rn3t/inspector-gopher/internal/store"
	"github.com/h0rn3t/inspector-gopher/internal/targets"
	"github.com/h0rn3t/inspector-gopher/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool names exposed by the server.
const (
	ToolProfileCPU       = "profile_cpu"
	ToolProfileHeap      = "profile_heap"
	ToolProfileMutex     = "profile_mutex"
	ToolProfileBlock     = "profile_block"
	ToolProfileGoroutine = "profile_goroutine"
	ToolCaptureTrace     = "capture_trace"
	ToolRuntimeStats     = "runtime_stats"
	ToolDockerList       = "docker_list_targets"
	ToolDockerProfile    = "docker_profile"
	ToolAnalyze          = "analyze_profile"
	ToolDiff             = "diff_profiles"
	ToolFlamegraph       = "flamegraph_data"
	ToolHealth           = "server_health"
	ToolDebugStart       = "debug_start"
	ToolDebugStop        = "debug_stop"
	ToolDebugList        = "debug_list"
	ToolDebugBreakpoint  = "debug_breakpoint"
	ToolDebugContinue    = "debug_continue"
	ToolDebugStep        = "debug_step"
	ToolDebugStack       = "debug_stack"
	ToolDebugVars        = "debug_vars"
	ToolDebugEvaluate    = "debug_evaluate"
)

// ToolNames lists every registered tool.
var ToolNames = []string{
	ToolProfileCPU, ToolProfileHeap, ToolProfileMutex, ToolProfileBlock,
	ToolProfileGoroutine, ToolCaptureTrace, ToolRuntimeStats,
	ToolDockerList, ToolDockerProfile,
	ToolAnalyze, ToolDiff, ToolFlamegraph, ToolHealth,
	ToolDebugStart, ToolDebugStop, ToolDebugList, ToolDebugBreakpoint,
	ToolDebugContinue, ToolDebugStep, ToolDebugStack, ToolDebugVars, ToolDebugEvaluate,
}

// Deps wires the server to its collaborators.
type Deps struct {
	Config    config.Config
	Registry  *targets.Registry
	Collector *collector.Collector
	Store     *store.Store
	Docker    *dockerpkg.Lister
	DockerRaw dockerpkg.Client
	Debugger  *debug.Manager
	Logger    *slog.Logger
	StartTime time.Time
}

// Server is the MCP server with call counters.
type Server struct {
	mcpServer *mcp.Server
	deps      Deps
	calls     atomic.Int64
	errors    atomic.Int64
}

// New builds the MCP server and registers all tools and resources.
func New(d Deps) *Server {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.StartTime.IsZero() {
		d.StartTime = time.Now()
	}
	if d.Debugger == nil {
		maxSessions := d.Config.DebugCfg.MaxSessions
		timeoutS := d.Config.DebugCfg.TimeoutS
		d.Debugger = debug.NewManager(maxSessions, time.Duration(timeoutS)*time.Second)
	}
	s := &Server{deps: d}
	ms := mcp.NewServer(&mcp.Implementation{Name: "inspector-gopher", Version: version.Version}, nil)
	s.mcpServer = ms
	s.registerTools(ms)
	s.registerResources(ms)
	return s
}

func (s *Server) count(ok bool) {
	s.calls.Add(1)
	if !ok {
		s.errors.Add(1)
	}
}

// MCPServer exposes the underlying SDK server (for transports/tests).
func (s *Server) MCPServer() *mcp.Server { return s.mcpServer }

// --- tool input types (schemas inferred by the SDK from map[string]any) ---

type profileOut struct {
	ProfileID  string `json:"profile_id"`
	Target     string `json:"target"`
	Kind       string `json:"kind"`
	Format     string `json:"format"`
	Size       int64  `json:"size"`
	DurationS  int    `json:"duration_s,omitempty"`
	Collection string `json:"collection"`
	SampleType string `json:"sample_type,omitempty"`
	DataBase64 string `json:"data_base64,omitempty"`
	TextDump   string `json:"text_dump,omitempty"`
	Warning    string `json:"warning,omitempty"`
}

type healthOut struct {
	Version  string `json:"version"`
	UptimeS  int64  `json:"uptime_s"`
	Calls    int64  `json:"calls"`
	Errors   int64  `json:"errors"`
	Tools    int    `json:"tools"`
	Profiles int    `json:"profiles"`
}

func (s *Server) registerTools(ms *mcp.Server) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	rw := &mcp.ToolAnnotations{ReadOnlyHint: false}

	add := func(name, desc string, ann *mcp.ToolAnnotations, h func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error)) {
		mcp.AddTool(ms, &mcp.Tool{Name: name, Description: desc, Annotations: ann}, h)
	}

	collect := func(kind string, needSeconds bool) func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		return func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
			target, _ := in["target"].(string)
			seconds := 0
			if f, ok := in["seconds"].(float64); ok {
				seconds = int(f)
			}
			if needSeconds && seconds == 0 {
				seconds = collector.DefaultCPUSecs
			}
			out, err := s.collectLocal(ctx, target, kind, seconds)
			s.count(err == nil)
			if err != nil {
				return nil, nil, err
			}
			return nil, out, nil
		}
	}

	add(ToolProfileCPU, "Collect a CPU profile (side-effect: blocks target handler for N seconds, 5..120).", rw, collect("cpu", true))
	add(ToolProfileHeap, "Collect a heap profile (alloc_space/inuse_space).", rw, collect("heap", false))
	add(ToolProfileMutex, "Collect a mutex contention profile.", rw, collect("mutex", false))
	add(ToolProfileBlock, "Collect a block (goroutine blocking) profile.", rw, collect("block", false))
	add(ToolProfileGoroutine, "Collect a goroutine profile plus text stack dump.", rw, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		target, _ := in["target"].(string)
		out, err := s.collectGoroutine(ctx, target)
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
	add(ToolCaptureTrace, "Capture a runtime execution trace (side-effect: 1..30s).", rw, collect("trace", true))
	add(ToolRuntimeStats, "Read lightweight runtime stats (goroutines, mem) without profiling.", ro, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		target, _ := in["target"].(string)
		ep, err := s.deps.Registry.Resolve(target)
		if err != nil {
			s.count(false)
			return nil, nil, err
		}
		stats, err := s.deps.Collector.FetchStats(ctx, ep)
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, stats, nil
	})
	add(ToolDockerList, "List running Docker containers with pprof reachability.", ro, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		if s.deps.Docker == nil {
			s.count(false)
			return nil, nil, &dockerpkg.Error{Code: "docker_unavailable", Retryable: false, Message: "docker is not configured", Hint: "set docker socket / DOCKER_HOST"}
		}
		containers, err := s.deps.Docker.Targets(ctx)
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"containers": containers}, nil
	})
	add(ToolDockerProfile, "Profile a Go process in a Docker container (allowlisted only).", rw, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		containerName, _ := in["container"].(string)
		kind, _ := in["profile"].(string)
		seconds := 0
		if f, ok := in["seconds"].(float64); ok {
			seconds = int(f)
		}
		out, err := s.dockerProfile(ctx, containerName, kind, seconds)
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
	add(ToolAnalyze, "Analyze a collected profile: top functions, GC hints.", ro, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		id, _ := in["profile_id"].(string)
		topN := 15
		if f, ok := in["top_n"].(float64); ok && f > 0 {
			topN = int(f)
		}
		rep, err := s.analyze(id, topN)
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, rep, nil
	})
	add(ToolDiff, "Diff two profiles of the same kind to find regressions.", ro, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		base, _ := in["base"].(string)
		cur, _ := in["current"].(string)
		out, err := s.diff(base, cur)
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
	add(ToolFlamegraph, "Render flamegraph data (folded stacks or JSON tree).", ro, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		id, _ := in["profile_id"].(string)
		format, _ := in["format"].(string)
		out, err := s.flamegraph(id, format)
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
	add(ToolHealth, "Server health: version, uptime, call counters.", ro, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		s.count(true)
		return nil, healthOut{
			Version: version.Version, UptimeS: int64(time.Since(s.deps.StartTime).Seconds()),
			Calls: s.calls.Load(), Errors: s.errors.Load(),
			Tools: len(ToolNames), Profiles: len(s.deps.Store.Recent(1000000)),
		}, nil
	})
	s.registerDebugTools(ms, ro, rw)
}

func (s *Server) registerResources(ms *mcp.Server) {
	ms.AddResource(&mcp.Resource{URI: "config://targets", Name: "targets", Description: "Configured profiling targets"}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		names := []string{}
		for _, t := range s.deps.Config.Local {
			names = append(names, t.Name+" -> "+t.PprofURL)
		}
		text := "local targets:\n"
		for _, n := range names {
			text += "  - " + n + "\n"
		}
		if len(s.deps.Config.Debug) > 0 {
			text += "debug targets:\n"
			for _, t := range s.deps.Config.Debug {
				text += "  - " + t.Name + " -> " + t.DAPAddr + "\n"
			}
		}
		text += fmt.Sprintf("docker allowlist: %v\n", s.deps.Config.Docker.Allowlist)
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "config://targets", MIMEType: "text/plain", Text: text}}}, nil
	})
	ms.AddResource(&mcp.Resource{URI: "profile://recent", Name: "recent", Description: "Recently collected profiles"}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		var b strings.Builder
		for _, p := range s.deps.Store.Recent(20) {
			fmt.Fprintf(&b, "%s %s %s %d bytes\n", p.ID, p.Kind, p.Target, p.Size)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "profile://recent", MIMEType: "text/plain", Text: b.String()}}}, nil
	})
	ms.AddResource(&mcp.Resource{URI: "debug://sessions", Name: "sessions", Description: "Live debug sessions"}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		var b strings.Builder
		for _, sess := range s.deps.Debugger.List() {
			fmt.Fprintf(&b, "%s %s %s %s\n", sess.ID, sess.Target, sess.Container, sess.DAPAddr)
		}
		if b.Len() == 0 {
			b.WriteString("(no live sessions)\n")
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "debug://sessions", MIMEType: "text/plain", Text: b.String()}}}, nil
	})
}
