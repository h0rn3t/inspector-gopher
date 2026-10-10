package mcp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/h0rn3t/inspector-gopher/internal/collector"
	"github.com/h0rn3t/inspector-gopher/internal/config"
	"github.com/h0rn3t/inspector-gopher/internal/dashboard"
	"github.com/h0rn3t/inspector-gopher/internal/store"
	"github.com/h0rn3t/inspector-gopher/internal/targets"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func dashboardTestServer(t *testing.T, cfg config.Config) *Server {
	t.Helper()
	st := store.New(20, "")
	reg := targets.New(cfg)
	coll := collector.New(st, cfg.MaxProfileBytes(), 5*time.Second)
	return New(Deps{Config: cfg, Registry: reg, Collector: coll, Store: st, StartTime: time.Now()})
}

func seedHeapProfile(t *testing.T, s *Server) string {
	t.Helper()
	fnA := &profile.Function{ID: 1, Name: "main.hotLoop", Filename: "main.go"}
	fnB := &profile.Function{ID: 2, Name: "main.helper", Filename: "main.go"}
	locA := &profile.Location{ID: 1, Line: []profile.Line{{Function: fnA}}}
	locB := &profile.Location{ID: 2, Line: []profile.Line{{Function: fnB}}}
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "alloc_space", Unit: "bytes"}},
		Sample: []*profile.Sample{
			{Location: []*profile.Location{locA, locB}, Value: []int64{80}},
			{Location: []*profile.Location{locB}, Value: []int64{20}},
		},
		Function: []*profile.Function{fnA, fnB},
		Location: []*profile.Location{locA, locB},
	}
	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		t.Fatalf("Write(profile) error = %v", err)
	}
	stored := s.deps.Store.Put("api-local", "heap", "pprof-protobuf", buf.Bytes(), map[string]any{
		"collection": "test", "sample_type": "alloc_space", "units": "bytes",
	})
	return stored.ID
}

func dialTestServer(t *testing.T, s *Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := s.MCPServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestFlamegraphReportTool(t *testing.T) {
	cfg := config.Defaults()
	cfg.Local = []config.LocalTarget{{Name: "api-local", PprofURL: "http://127.0.0.1:6060"}}
	cfg.Dashboard.Dir = t.TempDir()
	cfg.Server.HTTPAddr = "127.0.0.1:8080"
	s := dashboardTestServer(t, cfg)
	id := seedHeapProfile(t, s)
	cs := dialTestServer(t, s)
	ctx := context.Background()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: ToolFlamegraphReport, Arguments: map[string]any{"profile_id": id},
	})
	if err != nil {
		t.Fatalf("CallTool(%s) error = %v", ToolFlamegraphReport, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s) IsError with %+v", ToolFlamegraphReport, res.Content)
	}
	if len(res.Content) != 2 {
		t.Fatalf("CallTool(%s) content blocks = %d, want 2 (text + embedded HTML)", ToolFlamegraphReport, len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("CallTool(%s) content[0] = %T, want *TextContent", ToolFlamegraphReport, res.Content[0])
	}
	for _, want := range []string{id, "http://127.0.0.1:8080/dashboard/flamegraph/" + id, dashboard.TemplatePrefix + id} {
		if !strings.Contains(text.Text, want) {
			t.Errorf("CallTool(%s) text missing %q", ToolFlamegraphReport, want)
		}
	}
	embedded, ok := res.Content[1].(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("CallTool(%s) content[1] = %T, want *EmbeddedResource", ToolFlamegraphReport, res.Content[1])
	}
	if !strings.Contains(embedded.Resource.Text, id) || !strings.Contains(embedded.Resource.Text, "main.hotLoop") {
		t.Errorf("CallTool(%s) embedded HTML missing profile content", ToolFlamegraphReport)
	}
	structured, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("CallTool(%s) structured = %T, want map", ToolFlamegraphReport, res.StructuredContent)
	}
	if structured["file_path"] == "" || structured["dashboard_url"] == "" || structured["ui_resource_uri"] == "" {
		t.Errorf("CallTool(%s) structured links incomplete: %v", ToolFlamegraphReport, structured)
	}
}

func TestFlamegraphReportErrors(t *testing.T) {
	cfg := config.Defaults()
	s := dashboardTestServer(t, cfg)
	cs := dialTestServer(t, s)
	ctx := context.Background()
	for name, args := range map[string]map[string]any{
		"missing id": {},
		"unknown id": {"profile_id": "heap-deadbeefcafe"},
		"evil id":    {"profile_id": "../evil"},
	} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: ToolFlamegraphReport, Arguments: args})
		if err == nil && !res.IsError {
			t.Errorf("CallTool(%s, %s) succeeded, want error", ToolFlamegraphReport, name)
		}
	}
}

func TestFlamegraphToolsHaveUIMeta(t *testing.T) {
	s := dashboardTestServer(t, config.Defaults())
	cs := dialTestServer(t, s)
	res, err := cs.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}
	for _, name := range []string{ToolFlamegraph, ToolFlamegraphReport} {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("tool %q missing", name)
		}
		ui, ok := tool.Meta["ui"].(map[string]any)
		if !ok || ui["resourceUri"] != dashboard.ResourceURI {
			t.Errorf("tool %q _meta.ui.resourceUri = %v, want %q", name, tool.Meta["ui"], dashboard.ResourceURI)
		}
	}
}

func TestDashboardUIResources(t *testing.T) {
	cfg := config.Defaults()
	s := dashboardTestServer(t, cfg)
	id := seedHeapProfile(t, s)
	cs := dialTestServer(t, s)
	ctx := context.Background()

	list, err := cs.ListResources(ctx, &mcp.ListResourcesParams{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	found := false
	for _, r := range list.Resources {
		if r.URI == dashboard.ResourceURI {
			found = true
			if r.MIMEType != dashboard.MIMEApp {
				t.Errorf("ui resource MIME = %q, want %q", r.MIMEType, dashboard.MIMEApp)
			}
		}
	}
	if !found {
		t.Fatalf("ui resource %q not listed", dashboard.ResourceURI)
	}

	landing, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: dashboard.ResourceURI})
	if err != nil {
		t.Fatalf("ReadResource(landing): %v", err)
	}
	if len(landing.Contents) != 1 || landing.Contents[0].MIMEType != dashboard.MIMEApp {
		t.Fatalf("ReadResource(landing) contents = %+v", landing.Contents)
	}

	perProfile, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: dashboard.TemplatePrefix + id})
	if err != nil {
		t.Fatalf("ReadResource(profile): %v", err)
	}
	if !strings.Contains(perProfile.Contents[0].Text, id) {
		t.Errorf("ReadResource(profile) missing %q", id)
	}

	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: dashboard.TemplatePrefix + "heap-deadbeefcafe"}); err == nil {
		t.Error("ReadResource(unknown profile) = nil, want error")
	}
}

func TestDashboardHTTP(t *testing.T) {
	cfg := config.Defaults()
	s := dashboardTestServer(t, cfg)
	id := seedHeapProfile(t, s)
	srv := httptest.NewServer(s.httpHandler())
	defer srv.Close()

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   []string
		wantCT     string
	}{
		{"profile page", "GET", "/dashboard/flamegraph/" + id, 200, []string{id, "main.hotLoop", "__INSPECTOR_FLAMEGRAPH__"}, "text/html"},
		{"unknown profile", "GET", "/dashboard/flamegraph/heap-deadbeefcafe", 404, nil, ""},
		{"evil id", "GET", "/dashboard/flamegraph/..%2Fevil", 400, nil, ""},
		{"index", "GET", "/dashboard/", 200, []string{id, "flamegraph_report"}, "text/html"},
		{"method not allowed", "POST", "/dashboard/flamegraph/" + id, 405, nil, ""},
		{"head serves index", "HEAD", "/dashboard/", 200, nil, "text/html"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), tt.method, srv.URL+tt.path, nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s error = %v", tt.method, tt.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("%s %s status = %d, want %d", tt.method, tt.path, resp.StatusCode, tt.wantStatus)
			}
			if tt.wantCT != "" && !strings.Contains(resp.Header.Get("Content-Type"), tt.wantCT) {
				t.Errorf("%s %s Content-Type = %q", tt.method, tt.path, resp.Header.Get("Content-Type"))
			}
			if len(tt.wantBody) > 0 {
				body, _ := io.ReadAll(resp.Body)
				for _, w := range tt.wantBody {
					if !strings.Contains(string(body), w) {
						t.Errorf("%s %s body missing %q", tt.method, tt.path, w)
					}
				}
			}
		})
	}
}

func TestDashboardPreviewURL(t *testing.T) {
	cfg := config.Defaults()
	s := dashboardTestServer(t, cfg)
	if got := s.dashboardPreviewURL("heap-x"); got != "" {
		t.Errorf("dashboardPreviewURL without http = %q, want empty", got)
	}
	cfg.Server.HTTPAddr = "127.0.0.1:8080"
	s = dashboardTestServer(t, cfg)
	if got, want := s.dashboardPreviewURL("heap-x"), "http://127.0.0.1:8080/dashboard/flamegraph/heap-x"; got != want {
		t.Errorf("dashboardPreviewURL = %q, want %q", got, want)
	}
	cfg.Dashboard.BaseURL = "https://example.internal:8443/pprof"
	s = dashboardTestServer(t, cfg)
	if got, want := s.dashboardPreviewURL("heap-x"), "https://example.internal:8443/pprof/dashboard/flamegraph/heap-x"; got != want {
		t.Errorf("dashboardPreviewURL = %q, want %q", got, want)
	}
}
