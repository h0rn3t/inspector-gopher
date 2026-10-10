package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "net/http/pprof"

	"github.com/h0rn3t/inspector-gopher/internal/store"
	"github.com/h0rn3t/inspector-gopher/internal/targets"
)

func testServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.Handle("/debug/pprof/", http.DefaultServeMux)
	return httptest.NewServer(mux)
}

func TestCollectHeapAgainstRealPprof(t *testing.T) {
	srv := testServer()
	defer srv.Close()
	st := store.New(10, "")
	c := New(st, 64<<20, 30*time.Second)
	ep := targets.Endpoint{Name: "test", PprofURL: srv.URL, Collection: "local-http"}
	p, err := c.Collect(context.Background(), ep, "heap", 0)
	if err != nil {
		t.Fatalf("Collect heap: %v", err)
	}
	if p.Format != "pprof-protobuf" || len(p.Data) == 0 {
		t.Errorf("bad profile: %+v", p)
	}
	if p.SampleType == "" {
		t.Error("sample type must be set")
	}
}

func TestCollectCPUValidation(t *testing.T) {
	st := store.New(10, "")
	c := New(st, 64<<20, 30*time.Second)
	ep := targets.Endpoint{Name: "test", PprofURL: "http://127.0.0.1:1", Collection: "local-http"}
	if _, err := c.Collect(context.Background(), ep, "cpu", 600); err == nil {
		t.Fatal("expected validation error for seconds=600")
	} else if !strings.Contains(err.Error(), "5..120") {
		t.Errorf("error should mention range: %v", err)
	}
}

func TestCollectUnavailableTarget(t *testing.T) {
	st := store.New(10, "")
	c := New(st, 64<<20, 2*time.Second)
	ep := targets.Endpoint{Name: "dead", PprofURL: "http://127.0.0.1:1", Collection: "local-http"}
	_, err := c.Collect(context.Background(), ep, "heap", 0)
	if err == nil {
		t.Fatal("expected pprof_unavailable")
	}
	terr, ok := err.(*targets.Error)
	if !ok || terr.Code != "pprof_unavailable" || !terr.Retryable {
		t.Errorf("typed retryable error expected, got %v", err)
	}
}

func TestCollectSizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 1024))
	}))
	defer srv.Close()
	st := store.New(10, "")
	c := New(st, 10, 30*time.Second) // 10-byte cap
	ep := targets.Endpoint{Name: "big", PprofURL: srv.URL, Collection: "local-http"}
	_, err := c.Collect(context.Background(), ep, "heap", 0)
	if err == nil {
		t.Fatal("expected profile_too_large")
	}
	if terr, ok := err.(*targets.Error); !ok || terr.Code != "profile_too_large" {
		t.Errorf("got %v", err)
	}
}

func TestFetchStatsCountsGoroutines(t *testing.T) {
	srv := testServer()
	defer srv.Close()
	st := store.New(10, "")
	c := New(st, 64<<20, 30*time.Second)
	ep := targets.Endpoint{Name: "test", PprofURL: srv.URL, Collection: "local-http"}
	stats, err := c.FetchStats(context.Background(), ep)
	if err != nil {
		t.Fatalf("FetchStats: %v", err)
	}
	if stats.NumGoroutine < 1 {
		t.Errorf("NumGoroutine = %d", stats.NumGoroutine)
	}
}
