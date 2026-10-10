package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/h0rn3t/inspector-gopher/internal/dashboard"
)

// TestLiveDashboardFromDemo exercises the full dashboard path against the
// real demo service. Skipped when the demo is not running.
func TestLiveDashboardFromDemo(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	heapOut, err := s.collectLocal(ctx, "api-local", "heap", 0)
	if err != nil {
		t.Skipf("demo not running: %v", err)
	}
	data, err := s.reportData(heapOut.ProfileID, 0, 0)
	if err != nil {
		t.Fatalf("reportData: %v", err)
	}
	page, err := dashboard.Render(*data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{heapOut.ProfileID, data.Top[0].Function, "__INSPECTOR_FLAMEGRAPH__"} {
		if !strings.Contains(page, want) {
			t.Errorf("dashboard page missing %q (bytes=%d)", want, len(page))
		}
	}
	t.Logf("dashboard bytes=%d top=%s", len(page), data.Top[0].Function)
}
