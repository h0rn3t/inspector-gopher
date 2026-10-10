package analyzer

import (
	"strings"
	"testing"

	"github.com/google/pprof/profile"
)

func synthProfile() *profile.Profile {
	fnA := &profile.Function{ID: 1, Name: "main.hotLoop", Filename: "main.go"}
	fnB := &profile.Function{ID: 2, Name: "main.helper", Filename: "main.go"}
	fnC := &profile.Function{ID: 3, Name: "bytes.Buffer.Write", Filename: "bytes/buffer.go"}
	locA := &profile.Location{ID: 1, Line: []profile.Line{{Function: fnA}}}
	locB := &profile.Location{ID: 2, Line: []profile.Line{{Function: fnB}}}
	locC := &profile.Location{ID: 3, Line: []profile.Line{{Function: fnC}}}
	return &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "alloc_space", Unit: "bytes"}},
		Sample: []*profile.Sample{
			{Location: []*profile.Location{locA, locB}, Value: []int64{80}},
			{Location: []*profile.Location{locC, locB}, Value: []int64{20}},
		},
		Function: []*profile.Function{fnA, fnB, fnC},
		Location: []*profile.Location{locA, locB, locC},
	}
}

func TestAnalyzeTopFunctions(t *testing.T) {
	p := synthProfile()
	rep, err := Analyze("heap-x", p, 10)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.Total != 100 {
		t.Errorf("Total = %d, want 100", rep.Total)
	}
	if len(rep.TopFlat) == 0 || rep.TopFlat[0].Function != "main.hotLoop" {
		t.Errorf("TopFlat[0] = %+v", rep.TopFlat)
	}
	if rep.TopFlat[0].FlatPct != 80 {
		t.Errorf("FlatPct = %v, want 80", rep.TopFlat[0].FlatPct)
	}
	if !rep.Heuristic {
		t.Error("report must be marked heuristic")
	}
	// bytes.Buffer allocator should trigger a gc_pressure finding.
	found := false
	for _, f := range rep.Findings {
		if f.Kind == "gc_pressure" && f.Heuristic {
			found = true
		}
	}
	if !found {
		t.Errorf("expected gc_pressure finding, got %+v", rep.Findings)
	}
}

func TestDiffFindsRegression(t *testing.T) {
	base := synthProfile()
	cur := synthProfile()
	cur.Sample[0].Value = []int64{200} // hotLoop regresses
	reg, imp, bt, ct := Diff(base, cur)
	if bt != 100 || ct != 220 {
		t.Fatalf("totals %d/%d", bt, ct)
	}
	if len(reg) == 0 || reg[0].Function != "main.hotLoop" {
		t.Fatalf("regressions: %+v", reg)
	}
	if reg[0].DeltaFlat != 120 {
		t.Errorf("delta = %d", reg[0].DeltaFlat)
	}
	_ = imp
}

func TestFoldedAndTree(t *testing.T) {
	p := synthProfile()
	folded, total := Folded(p)
	if total != 100 {
		t.Errorf("total = %d", total)
	}
	lines := strings.Split(strings.TrimSpace(folded), "\n")
	if len(lines) != 2 {
		t.Errorf("folded lines: %q", folded)
	}
	for _, l := range lines {
		if !strings.Contains(l, ";") && !strings.Contains(l, " ") {
			t.Errorf("bad folded line %q", l)
		}
	}
	tree, truncated := Tree(p, 200, 5000)
	if truncated {
		t.Error("small profile must not truncate")
	}
	var sum int64
	for _, ch := range tree.Children {
		sum += ch.Value
	}
	if sum != tree.Value || tree.Value != 100 {
		t.Errorf("tree sums: root=%d children=%d", tree.Value, sum)
	}
	_, trunc := Tree(p, 200, 1)
	if !trunc {
		t.Error("tiny width must truncate")
	}
}

func TestGoroutineLeakCheck(t *testing.T) {
	if GoroutineLeakCheck(10, 12, "x") != nil {
		t.Error("small growth must not fire")
	}
	f := GoroutineLeakCheck(10, 50, "main.waiter chan receive")
	if f == nil || f.Kind != "goroutine_leak_suspected" || !f.Heuristic {
		t.Errorf("leak finding: %+v", f)
	}
}

func TestParseInvalid(t *testing.T) {
	if _, err := Parse([]byte("not-a-profile")); err == nil {
		t.Error("expected parse error")
	}
}
