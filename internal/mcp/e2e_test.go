package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/h0rn3t/inspector-gopher/internal/collector"
	"github.com/h0rn3t/inspector-gopher/internal/config"
	"github.com/h0rn3t/inspector-gopher/internal/store"
	"github.com/h0rn3t/inspector-gopher/internal/targets"
)

// TestE2ECollectAndAnalyze exercises collect → analyze → flamegraph against
// the local demo service (127.0.0.1:6060). Skip when the demo is not running.
func TestE2ECollectAndAnalyze(t *testing.T) {
	cfg := config.Defaults()
	cfg.Local = []config.LocalTarget{{Name: "api-local", PprofURL: "http://127.0.0.1:6060"}}
	st := store.New(20, "")
	s := New(Deps{Config: cfg, Registry: targets.New(cfg),
		Collector: collector.New(st, cfg.MaxProfileBytes(), 30*time.Second),
		Store:     st, StartTime: time.Now()})
	ctx := context.Background()

	heapOut, err := s.collectLocal(ctx, "api-local", "heap", 0)
	if err != nil {
		t.Skipf("demo not running: %v", err)
	}
	if heapOut.DataBase64 == "" {
		t.Fatal("heap profile has no data")
	}
	rep, err := s.analyze(heapOut.ProfileID, 10)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if rep.Total <= 0 || len(rep.TopFlat) == 0 {
		t.Errorf("empty report: %+v", rep)
	}
	folded, err := s.flamegraph(heapOut.ProfileID, "folded")
	if err != nil {
		t.Fatalf("flamegraph: %v", err)
	}
	if folded["total"] == int64(0) {
		t.Error("folded total is zero")
	}
	tree, err := s.flamegraph(heapOut.ProfileID, "tree")
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	if tree["root"] == nil {
		t.Error("tree root missing")
	}
	t.Logf("heap=%s top=%s", heapOut.ProfileID, rep.TopFlat[0].Function)
}
