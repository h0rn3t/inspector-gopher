package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/h0rn3t/inspector-gopher/internal/analyzer"
)

func synthTree() *analyzer.TreeNode {
	return &analyzer.TreeNode{Name: "root", Value: 100, Children: []*analyzer.TreeNode{
		{Name: "main.hotLoop", Value: 80},
		{Name: "bytes.Buffer.Write", Value: 20},
	}}
}

func TestRenderContract(t *testing.T) {
	hostile := `<script>alert(1)</script>`
	tests := []struct {
		name      string
		data      Data
		want      []string
		wantstein []string
	}{
		{
			name: "full dashboard",
			data: Data{
				ProfileID: "heap-abc123", Title: "Heap api-local", Target: "api-local",
				Kind: "heap", SampleType: "alloc_space", Units: "bytes",
				Total: 100, Top: Rows([]analyzer.FuncStat{{Function: "main.hotLoop", Flat: 80, FlatPct: 80}}),
				Tree: synthTree(), Folded: "a;b 80\n",
			},
			want: []string{
				"heap-abc123", "main.hotLoop", "window.__INSPECTOR_FLAMEGRAPH__",
				"a;b 80", "<svg", "Top functions",
			},
		},
		{
			name: "empty profile",
			data: Data{ProfileID: "cpu-empty", Tree: &analyzer.TreeNode{Name: "root"}},
			want: []string{"cpu-empty", "No samples in this profile."},
		},
		{
			name: "hostile names are escaped",
			data: Data{
				ProfileID: "heap-x", Total: 10,
				Top:  []FuncRow{{Function: hostile, Flat: 10, FlatPct: 100}},
				Tree: &analyzer.TreeNode{Name: "root", Value: 10, Children: []*analyzer.TreeNode{{Name: hostile, Value: 10}}},
			},
			want:      []string{"&lt;script&gt;"},
			wantstein: []string{hostile},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Render(tt.data)
			if err != nil {
				t.Fatalf("Render(%q) error = %v", tt.data.ProfileID, err)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("Render(%q) missing %q", tt.data.ProfileID, w)
				}
			}
			for _, bad := range tt.wantstein {
				if strings.Contains(got, bad) {
					t.Errorf("Render(%q) contains unescaped %q", tt.data.ProfileID, bad)
				}
			}
		})
	}
}

func TestRenderHasNoExternalReferences(t *testing.T) {
	t.Parallel()
	got, err := Render(Data{ProfileID: "heap-x", Total: 100, Tree: synthTree()})
	if err != nil {
		t.Fatalf("Render error = %v", err)
	}
	for _, bad := range []string{"src=\"http", "href=\"http", "cdn.", "@import http", "fetch(", "XMLHttpRequest"} {
		if strings.Contains(got, bad) {
			t.Errorf("Render must be self-contained, found %q", bad)
		}
	}
}

func TestRenderIndexContract(t *testing.T) {
	t.Parallel()
	got := RenderIndex([]IndexEntry{{ID: "heap-abc", Target: "api-local", Kind: "heap", SampleType: "alloc_space"}})
	for _, w := range []string{"heap-abc", "api-local", "flamegraph_report", TemplatePrefix + "heap-abc"} {
		if !strings.Contains(got, w) {
			t.Errorf("RenderIndex() missing %q", w)
		}
	}
	if empty := RenderIndex(nil); !strings.Contains(empty, "No profiles collected yet.") {
		t.Errorf("RenderIndex(nil) = %q, want empty notice", empty)
	}
}

func TestSaveContract(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path, err := Save(dir, "heap-abc123", "<html></html>")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if want := filepath.Join(dir, "flamegraph-heap-abc123.html"); path != want {
		t.Errorf("Save() = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("Save() file missing: %v", err)
	}
	for _, id := range []string{"", "../evil", "a/b", "a\\b", "x\x00y", "has space"} {
		if _, err := Save(dir, id, "x"); err == nil {
			t.Errorf("Save(%q) = nil, want error", id)
		}
	}
}

func TestValidID(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"heap-abc123", "cpu-0123456789ab", "trace-a.b_c-d"} {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "../x", "a/b", "a;b", "ünicode"} {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

func TestDir(t *testing.T) {
	t.Parallel()
	if got := Dir("/custom/dir"); got != "/custom/dir" {
		t.Errorf("Dir(/custom/dir) = %q", got)
	}
	if got := Dir(""); got == "" || !strings.HasSuffix(got, "dashboards") {
		t.Errorf("Dir() = %q, want default dashboards dir", got)
	}
}
