package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/h0rn3t/inspector-gopher/internal/collector"
	"github.com/h0rn3t/inspector-gopher/internal/config"
	"github.com/h0rn3t/inspector-gopher/internal/store"
	"github.com/h0rn3t/inspector-gopher/internal/targets"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.Defaults()
	cfg.Local = []config.LocalTarget{{Name: "api-local", PprofURL: "http://127.0.0.1:6060"}}
	cfg.Docker.Allowlist = []string{"api-*"}
	st := store.New(20, "")
	reg := targets.New(cfg)
	coll := collector.New(st, cfg.MaxProfileBytes(), 5*time.Second)
	return New(Deps{Config: cfg, Registry: reg, Collector: coll, Store: st, StartTime: time.Now()})
}

func TestToolCatalogHasTwelvePlusHealth(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := s.MCPServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	for _, want := range ToolNames {
		if !names[want] {
			t.Errorf("tool %q missing from catalog (got %v)", want, names)
		}
	}
	if len(res.Tools) < 22 {
		t.Errorf("expected >= 22 tools, got %d", len(res.Tools))
	}
}

func TestDebugToolsResolveWithoutNetwork(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	// Unknown target: forbidden before any dial.
	if _, err := s.resolveDebugStart(ctx, map[string]any{"target": "nope-missing", "mode": "remote"}); err == nil {
		t.Error("resolveDebugStart(unknown) = nil, want error")
	}
	// Known target without dap_addr: actionable hint, no dial.
	if _, err := s.resolveDebugStart(ctx, map[string]any{"target": "api-local", "mode": "remote"}); err == nil {
		t.Error("resolveDebugStart(no dap) = nil, want debug_no_dap")
	}
	// Explicit dap_addr with unknown container and no docker: unavailable.
	if _, err := s.resolveDebugStart(ctx, map[string]any{"container": "api-1", "mode": "remote", "dap_addr": "127.0.0.1:2345"}); err == nil {
		t.Error("resolveDebugStart(container, no docker) = nil, want error")
	}
	// Explicit non-loopback dap_addr: forbidden without dial.
	if _, err := s.resolveDebugStart(ctx, map[string]any{"target": "api-local", "mode": "remote", "dap_addr": "10.0.0.9:2345"}); err == nil {
		t.Error("resolveDebugStart(non-loopback) = nil, want forbidden")
	}
	// debug_list on a fresh server is empty.
	if got := s.deps.Debugger.List(); len(got) != 0 {
		t.Errorf("fresh debug list = %v, want empty", got)
	}
}

func TestUnknownTargetReturnsError(t *testing.T) {
	s := testServer(t)
	_, err := s.collectLocal(context.Background(), "nope-missing", "heap", 0)
	if err == nil {
		t.Fatal("expected error for unknown target")
	}
}

func TestHealthAndResources(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := s.MCPServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	callRes, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: ToolHealth})
	if err != nil {
		t.Fatalf("CallTool server_health: %v", err)
	}
	if callRes.IsError {
		t.Errorf("server_health returned error: %+v", callRes)
	}

	res, err := cs.ListResources(ctx, &mcp.ListResourcesParams{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	uris := map[string]bool{}
	for _, r := range res.Resources {
		uris[r.URI] = true
	}
	if !uris["config://targets"] || !uris["profile://recent"] || !uris["debug://sessions"] {
		t.Errorf("resources missing: %v", uris)
	}
}
