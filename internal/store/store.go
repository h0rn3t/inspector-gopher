// Package store keeps collected profiles in an in-memory ring with a
// file-backed cache so raw blobs survive restarts.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Profile is one collected artifact.
type Profile struct {
	ID         string    `json:"id"`
	Target     string    `json:"target"`
	Kind       string    `json:"kind"`
	Format     string    `json:"format"`
	Size       int64     `json:"size"`
	DurationS  int       `json:"duration_s,omitempty"`
	Collection string    `json:"collection"`
	SampleType string    `json:"sample_type,omitempty"`
	Units      string    `json:"units,omitempty"`
	Collected  time.Time `json:"collected_at"`
	Data       []byte    `json:"-"`
	TextDump   string    `json:"text_dump,omitempty"`
	Warning    string    `json:"warning,omitempty"`
}

// Store is a bounded ring of profiles with file cache.
type Store struct {
	mu       sync.Mutex
	capacity int
	order    []string
	byID     map[string]*Profile
	cacheDir string
}

// New creates a Store. cacheDir may be empty (memory only).
func New(capacity int, cacheDir string) *Store {
	if capacity <= 0 {
		capacity = 20
	}
	return &Store{capacity: capacity, byID: make(map[string]*Profile), cacheDir: cacheDir}
}

// Put stores data and returns the profile with a content-derived ID.
func (s *Store) Put(target, kind, format string, data []byte, meta map[string]any) *Profile {
	sum := sha256.Sum256(data)
	id := kind + "-" + hex.EncodeToString(sum[:])[:12]
	p := &Profile{
		ID: id, Target: target, Kind: kind, Format: format,
		Size: int64(len(data)), Collected: time.Now().UTC(), Data: data,
	}
	if v, _ := meta["duration_s"].(int); v != 0 {
		p.DurationS = v
	}
	if v, _ := meta["collection"].(string); v != "" {
		p.Collection = v
	}
	if v, _ := meta["sample_type"].(string); v != "" {
		p.SampleType = v
	}
	if v, _ := meta["units"].(string); v != "" {
		p.Units = v
	}
	if v, _ := meta["text_dump"].(string); v != "" {
		p.TextDump = v
	}
	if v, _ := meta["warning"].(string); v != "" {
		p.Warning = v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byID[id]; !exists {
		s.order = append(s.order, id)
		for len(s.order) > s.capacity {
			oldest := s.order[0]
			s.order = s.order[1:]
			delete(s.byID, oldest)
		}
	}
	s.byID[id] = p
	s.persistLocked(p)
	return p
}

// Get returns a profile by ID.
func (s *Store) Get(id string) (*Profile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[id]
	return p, ok
}

// Recent returns up to n newest profiles, newest first.
func (s *Store) Recent(n int) []*Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Profile, 0, len(s.order))
	for i := len(s.order) - 1; i >= 0; i-- {
		out = append(out, s.byID[s.order[i]])
		if n > 0 && len(out) >= n {
			break
		}
	}
	return out
}

// IDs returns known profile IDs, newest first.
func (s *Store) IDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.order))
	for i := len(s.order) - 1; i >= 0; i-- {
		ids = append(ids, s.order[i])
	}
	return ids
}

func (s *Store) persistLocked(p *Profile) {
	if s.cacheDir == "" {
		return
	}
	_ = os.MkdirAll(s.cacheDir, 0o755)
	_ = os.WriteFile(filepath.Join(s.cacheDir, p.ID+".bin"), p.Data, 0o644)
}

// Restore scans the cache dir and re-registers blobs found there.
func (s *Store) Restore() int {
	if s.cacheDir == "" {
		return 0
	}
	entries, err := os.ReadDir(s.cacheDir)
	if err != nil {
		return 0
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	restored := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".bin" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.cacheDir, e.Name()))
		if err != nil {
			continue
		}
		id := e.Name()[:len(e.Name())-len(".bin")]
		kind := "restored"
		if i := indexDash(id); i > 0 {
			kind = id[:i]
		}
		s.mu.Lock()
		if _, ok := s.byID[id]; !ok {
			s.byID[id] = &Profile{ID: id, Kind: kind, Format: "pprof-protobuf",
				Size: int64(len(data)), Data: data, Collection: "file-cache"}
			s.order = append(s.order, id)
			restored++
		}
		s.mu.Unlock()
	}
	return restored
}

func indexDash(s string) int {
	for i, c := range s {
		if c == '-' {
			return i
		}
	}
	return -1
}

// DefaultCacheDir returns ~/.cache/inspector-gopher.
func DefaultCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "inspector-gopher")
}

// ErrNotFound formats a profile_not_found error payload.
func ErrNotFound(id string, known []string) map[string]any {
	return map[string]any{
		"code": "profile_not_found", "retryable": false,
		"message":   fmt.Sprintf("profile %q not found", id),
		"known_ids": known,
	}
}
