// Package debug manages Delve DAP sessions for local and Docker targets.
//
// A session dials an already-running `dlv dap` server (local process or
// container with a published DAP port) and speaks a minimal subset of the
// Debug Adapter Protocol over TCP: initialize, launch/attach,
// setBreakpoints, continue, next/stepIn/stepOut, stackTrace, scopes,
// variables, evaluate, disconnect.
package debug

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// StartOptions selects what to debug and how the DAP server was started.
//
// Exactly one of Target (local targets.yaml entry) or Container (Docker
// container name/ID) identifies the origin; DAPAddr is the already-validated
// dial address (host:port) resolved by the caller. Mode is "launch" or
// "attach": launch starts Program with Args, attach connects to PID.
type StartOptions struct {
	Target    string
	Container string
	DAPAddr   string
	Mode      string
	Program   string
	Args      []string
	PID       int
}

// SessionInfo is the wire view of a session.
type SessionInfo struct {
	ID        string `json:"id"`
	Target    string `json:"target,omitempty"`
	Container string `json:"container,omitempty"`
	DAPAddr   string `json:"dap_addr"`
	Mode      string `json:"mode"`
	Created   string `json:"created_at"`
}

// Breakpoint is a verified DAP breakpoint.
type Breakpoint struct {
	ID       int    `json:"id"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Verified bool   `json:"verified"`
}

// Frame is one stack frame.
type Frame struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column,omitempty"`
}

// Variable is one evaluated symbol.
type Variable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type,omitempty"`
}

// StoppedInfo reports where execution halted.
type StoppedInfo struct {
	Reason   string  `json:"reason"`
	ThreadID int     `json:"thread_id,omitempty"`
	Frames   []Frame `json:"frames,omitempty"`
}

// Error is a typed debug error.
type Error struct {
	Code      string
	Retryable bool
	Message   string
	Hint      string
}

func (e *Error) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Hint)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Manager tracks live DAP sessions.
type Manager struct {
	mu          sync.Mutex
	sessions    map[string]*Session
	maxSessions int
	timeout     time.Duration
	dial        func(ctx context.Context, addr string) (net.Conn, error)
}

// NewManager builds a Manager. maxSessions <= 0 defaults to 4,
// timeout <= 0 defaults to 10s.
func NewManager(maxSessions int, timeout time.Duration) *Manager {
	if maxSessions <= 0 {
		maxSessions = 4
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Manager{sessions: make(map[string]*Session), maxSessions: maxSessions, timeout: timeout, dial: dialTCP}
}

// List returns session infos, newest last.
func (m *Manager) List() []SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SessionInfo, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.info())
	}
	return out
}

// Count returns the live session count.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}
