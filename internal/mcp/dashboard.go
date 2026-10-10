package mcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/h0rn3t/inspector-gopher/internal/analyzer"
	"github.com/h0rn3t/inspector-gopher/internal/dashboard"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// uiMeta links a tool to the generic flamegraph viewer (SEP-1865 MCP Apps).
// Hosts without UI support ignore it and fall back to the text content.
func uiMeta() mcp.Meta {
	return mcp.Meta{"ui": map[string]any{"resourceUri": dashboard.ResourceURI}}
}

// dashboardBaseURL prefers an explicit dashboard.base_url, then the
// Streamable HTTP address for localhost preview links.
func (s *Server) dashboardBaseURL() string {
	if b := strings.TrimSuffix(strings.TrimSpace(s.deps.Config.Dashboard.BaseURL), "/"); b != "" {
		return b
	}
	if a := strings.TrimSpace(s.deps.Config.Server.HTTPAddr); a != "" {
		return "http://" + a
	}
	return ""
}

// dashboardPreviewURL is the browser link for a profile, or "" when no HTTP
// base is configured.
func (s *Server) dashboardPreviewURL(profileID string) string {
	if base := s.dashboardBaseURL(); base != "" {
		return base + "/dashboard/flamegraph/" + profileID
	}
	return ""
}

// reportData loads a profile and builds everything the dashboard needs.
func (s *Server) reportData(profileID string, maxDepth, maxNodes int) (*dashboard.Data, error) {
	p, ok := s.deps.Store.Get(profileID)
	if !ok {
		return nil, fmt.Errorf("profile_not_found: %q (see profile://recent for known IDs: %v)", profileID, s.deps.Store.IDs())
	}
	prof, err := analyzer.Parse(p.Data)
	if err != nil {
		return nil, err
	}
	rep, err := analyzer.Analyze(profileID, prof, 10)
	if err != nil {
		return nil, err
	}
	tree, truncated := analyzer.Tree(prof, maxDepth, maxNodes)
	folded, _ := analyzer.Folded(prof)
	collected := ""
	if !p.Collected.IsZero() {
		collected = p.Collected.UTC().Format(time.RFC3339)
	}
	return &dashboard.Data{
		ProfileID: profileID, Target: p.Target, Kind: p.Kind,
		SampleType: rep.SampleType, Units: rep.Units, CollectedAt: collected,
		Total: rep.Total, Truncated: truncated,
		Top: dashboard.Rows(rep.TopFlat), Tree: tree, Folded: folded,
	}, nil
}

// flamegraphReport renders the dashboard, saves the artifact, and answers
// with inline HTML (MCP Apps / browser-preview hosts) plus a text fallback.
func (s *Server) flamegraphReport(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
	profileID, _ := in["profile_id"].(string)
	if !dashboard.ValidID(profileID) {
		s.count(false)
		return nil, nil, fmt.Errorf("profile_id is required (see profile://recent for known IDs: %v)", s.deps.Store.IDs())
	}
	title, _ := in["title"].(string)
	maxDepth, maxNodes := 0, 0
	if f, ok := in["max_depth"].(float64); ok && f > 0 {
		maxDepth = int(f)
	}
	if f, ok := in["max_nodes"].(float64); ok && f > 0 {
		maxNodes = int(f)
	}
	saveFile := true
	if b, ok := in["save_file"].(bool); ok {
		saveFile = b
	}

	data, err := s.reportData(profileID, maxDepth, maxNodes)
	if err != nil {
		s.count(false)
		return nil, nil, err
	}
	data.Title = strings.TrimSpace(title)
	page, err := dashboard.Render(*data)
	if err != nil {
		s.count(false)
		return nil, nil, err
	}

	filePath := ""
	if saveFile {
		filePath, err = dashboard.Save(dashboard.Dir(s.deps.Config.Dashboard.Dir), profileID, page)
		if err != nil {
			s.count(false)
			return nil, nil, err
		}
	}
	s.count(true)

	previewURL := s.dashboardPreviewURL(profileID)
	uiURI := dashboard.TemplatePrefix + profileID
	top := summarizeTop(data.Top, 5)
	structured := map[string]any{
		"profile_id": profileID, "target": data.Target, "kind": data.Kind,
		"sample_type": data.SampleType, "units": data.Units, "total": data.Total,
		"truncated": data.Truncated, "top_flat": data.Top,
		"file_path": filePath, "dashboard_url": previewURL, "ui_resource_uri": uiURI,
	}

	var text strings.Builder
	fmt.Fprintf(&text, "# %s\n\n", firstNonEmpty(data.Title, "Flamegraph "+profileID))
	fmt.Fprintf(&text, "profile `%s` (%s, %s) — total %d samples", profileID, data.Kind, data.Target, data.Total)
	if data.SampleType != "" {
		fmt.Fprintf(&text, " (%s", data.SampleType)
		if data.Units != "" {
			fmt.Fprintf(&text, ", %s", data.Units)
		}
		text.WriteString(")")
	}
	text.WriteString(".\n")
	if top != "" {
		fmt.Fprintf(&text, "Top: %s.\n", top)
	}
	if data.Truncated {
		text.WriteString("Note: the tree hit the depth/nodes cap — zoom into the hot branch in the dashboard.\n")
	}
	text.WriteString("\nDashboard:\n")
	if filePath != "" {
		fmt.Fprintf(&text, "- file artifact: file://%s\n", filePath)
	}
	if previewURL != "" {
		fmt.Fprintf(&text, "- browser preview (Claude Code): %s\n", previewURL)
	} else {
		text.WriteString("- browser preview: enable with --http 127.0.0.1:8080 (or server.http_addr)\n")
	}
	fmt.Fprintf(&text, "- MCP Apps resource: %s\n", uiURI)
	text.WriteString("\nOpen the preview link in a browser, or view the dashboard inline where MCP Apps is supported.")

	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: text.String()},
		&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
			URI: uiURI, MIMEType: "text/html", Text: page,
		}},
	}}, structured, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func summarizeTop(rows []dashboard.FuncRow, n int) string {
	if len(rows) > n {
		rows = rows[:n]
	}
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		parts = append(parts, fmt.Sprintf("`%s` %.1f%%", r.Function, r.FlatPct))
	}
	return strings.Join(parts, ", ")
}

// registerDashboardResources exposes the MCP Apps UI resources: a landing
// viewer plus per-profile dashboards rendered on demand.
func (s *Server) registerDashboardResources(ms *mcp.Server) {
	ms.AddResource(&mcp.Resource{
		URI: dashboard.ResourceURI, Name: "flamegraph-dashboard",
		Title:       "Flamegraph dashboard",
		Description: "Interactive flamegraph viewer (MCP Apps). Call flamegraph_report with a profile_id for a per-profile dashboard.",
		MIMEType:    dashboard.MIMEApp,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: dashboard.ResourceURI, MIMEType: dashboard.MIMEApp, Text: s.renderIndex(),
		}}}, nil
	})
	ms.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: dashboard.URITemplate, Name: "flamegraph-profile",
		Title:       "Profile flamegraph",
		Description: "Interactive flamegraph for one collected profile.",
		MIMEType:    dashboard.MIMEApp,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		id := strings.TrimPrefix(req.Params.URI, dashboard.TemplatePrefix)
		if !dashboard.ValidID(id) {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		data, err := s.reportData(id, 0, 0)
		if err != nil {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		page, err := dashboard.Render(*data)
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: req.Params.URI, MIMEType: dashboard.MIMEApp, Text: page,
		}}}, nil
	})
}

func (s *Server) renderIndex() string {
	entries := make([]dashboard.IndexEntry, 0, 20)
	for _, p := range s.deps.Store.Recent(20) {
		entries = append(entries, dashboard.IndexEntry{
			ID: p.ID, Target: p.Target, Kind: p.Kind, SampleType: p.SampleType,
		})
	}
	return dashboard.RenderIndex(entries)
}

// handleDashboardIndex lists recent profiles with dashboard links.
func (s *Server) handleDashboardIndex(w http.ResponseWriter, r *http.Request) {
	if !requireGetRead(w, r) {
		return
	}
	page := s.renderIndex()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(page)) // headers are sent; a failed write is the client's disconnect
}

// handleDashboardFlamegraph renders one profile dashboard on demand.
func (s *Server) handleDashboardFlamegraph(w http.ResponseWriter, r *http.Request) {
	if !requireGetRead(w, r) {
		return
	}
	id := r.PathValue("id")
	if !dashboard.ValidID(id) {
		http.Error(w, "invalid profile id", http.StatusBadRequest)
		return
	}
	if _, ok := s.deps.Store.Get(id); !ok {
		http.Error(w, "profile not found", http.StatusNotFound)
		return
	}
	data, err := s.reportData(id, 0, 0)
	if err != nil {
		s.deps.Logger.ErrorContext(r.Context(), "render dashboard", "profile", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	page, err := dashboard.Render(*data)
	if err != nil {
		s.deps.Logger.ErrorContext(r.Context(), "render dashboard", "profile", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(page)) // headers are sent; a failed write is the client's disconnect
}
