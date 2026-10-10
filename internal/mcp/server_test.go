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
	if len(res.Tools) < 13 {
		t.Errorf("expected >= 13 tools, got %d", len(res.Tools))
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
	if !uris["config://targets"] || !uris["profile://recent"] {
		t.Errorf("resources missing: %v", uris)
	}
}
