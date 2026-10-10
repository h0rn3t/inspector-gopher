package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPutGetRecent(t *testing.T) {
	s := New(3, "")
	a := s.Put("t", "cpu", "pprof-protobuf", []byte("data-a"), map[string]any{"duration_s": 5, "collection": "local-http"})
	b := s.Put("t", "heap", "pprof-protobuf", []byte("data-b"), nil)
	if a.ID == b.ID {
		t.Fatal("distinct blobs must have distinct IDs")
	}
	got, ok := s.Get(a.ID)
	if !ok || string(got.Data) != "data-a" {
		t.Fatal("Get failed")
	}
	if got.DurationS != 5 || got.Collection != "local-http" {
		t.Errorf("meta lost: %+v", got)
	}
	recent := s.Recent(10)
	if len(recent) != 2 || recent[0].ID != b.ID {
		t.Errorf("Recent order wrong: %v", recent)
	}
}

func TestEviction(t *testing.T) {
	s := New(2, "")
	p1 := s.Put("t", "cpu", "f", []byte("1"), nil)
	s.Put("t", "cpu", "f", []byte("2"), nil)
	s.Put("t", "cpu", "f", []byte("3"), nil)
	if _, ok := s.Get(p1.ID); ok {
		t.Error("oldest profile should be evicted")
	}
	if len(s.Recent(10)) != 2 {
		t.Error("capacity must hold")
	}
}

func TestRestoreFromCache(t *testing.T) {
	dir := t.TempDir()
	s := New(20, dir)
	s.Put("t", "cpu", "pprof-protobuf", []byte("blob"), nil)
	s2 := New(20, dir)
	n := s2.Restore()
	if n != 1 {
		t.Fatalf("restored = %d, want 1", n)
	}
	if len(s2.IDs()) != 1 {
		t.Fatal("IDs should list restored profile")
	}
	// Persisted file exists on disk.
	matches, _ := filepath.Glob(filepath.Join(dir, "*.bin"))
	if len(matches) != 1 {
		t.Errorf("cache files: %v", matches)
	}
	_ = os.Remove(matches[0])
}
