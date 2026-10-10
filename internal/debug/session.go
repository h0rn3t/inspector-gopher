// Session lifecycle: handshake, breakpoints, execution control, inspection.
package debug

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"crypto/rand"
	"encoding/json/jsontext"
	json "encoding/json/v2"
)

// Session is one live DAP connection.
type Session struct {
	mu        sync.Mutex
	id        string
	target    string
	container string
	dapAddr   string
	mode      string
	created   time.Time
	client    *dapClient
	timeout   time.Duration
	closed    bool
}

func (s *Session) info() SessionInfo {
	return SessionInfo{
		ID: s.id, Target: s.target, Container: s.container,
		DAPAddr: s.dapAddr, Mode: s.mode, Created: s.created.UTC().Format(time.RFC3339),
	}
}

// ValidateAddr rejects empty hosts, missing ports and out-of-range ports.
func ValidateAddr(addr string) error {
	if addr == "" {
		return &Error{Code: "debug_bad_addr", Retryable: false, Message: "dap address is empty"}
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return &Error{Code: "debug_bad_addr", Retryable: false,
			Message: fmt.Sprintf("dap address %q: %v", addr, err),
			Hint:    "want host:port, e.g. 127.0.0.1:2345"}
	}
	if host == "" {
		return &Error{Code: "debug_bad_addr", Retryable: false, Message: "dap host is empty"}
	}
	if port == "" {
		return &Error{Code: "debug_bad_addr", Retryable: false, Message: "dap port is empty"}
	}
	var n int
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return &Error{Code: "debug_bad_addr", Retryable: false, Message: fmt.Sprintf("dap port %q is not numeric", port)}
		}
		n = n*10 + int(port[i]-'0')
	}
	if n < 1 || n > 65535 {
		return &Error{Code: "debug_bad_addr", Retryable: false, Message: fmt.Sprintf("dap port %d out of range", n)}
	}
	return nil
}

// ValidateOptions checks StartOptions before any network access.
func ValidateOptions(o StartOptions) error {
	if o.Target == "" && o.Container == "" {
		return &Error{Code: "debug_no_origin", Retryable: false,
			Message: "target or container is required",
			Hint:    "pass target for local or container for Docker"}
	}
	if o.Target != "" && o.Container != "" {
		return &Error{Code: "debug_bad_origin", Retryable: false, Message: "target and container are mutually exclusive"}
	}
	if err := ValidateAddr(o.DAPAddr); err != nil {
		return err
	}
	switch o.Mode {
	case "launch":
		if o.Program == "" {
			return &Error{Code: "debug_bad_mode", Retryable: false, Message: "launch requires program"}
		}
		if len(o.Args) > 32 {
			return &Error{Code: "debug_bad_args", Retryable: false, Message: "too many args (max 32)"}
		}
		for _, a := range o.Args {
			if len(a) > 1024 {
				return &Error{Code: "debug_bad_args", Retryable: false, Message: "arg too long (max 1024)"}
			}
		}
	case "attach":
		if o.PID <= 0 {
			return &Error{Code: "debug_bad_mode", Retryable: false, Message: "attach requires pid > 0"}
		}
	case "remote":
	default:
		return &Error{Code: "debug_bad_mode", Retryable: false,
			Message: fmt.Sprintf("mode %q unknown", o.Mode), Hint: "want launch, attach or remote"}
	}
	return nil
}

func newSessionID() string {
	var b [9]byte
	_, _ = rand.Read(b[:])
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 12)
	for i := range out {
		out[i] = alphabet[int(b[i%len(b)])%len(alphabet)]
	}
	return "dbg-" + string(out)
}

// Start dials addr, runs the DAP handshake and registers the session.
func (m *Manager) Start(ctx context.Context, o StartOptions) (*SessionInfo, error) {
	if err := ValidateOptions(o); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if len(m.sessions) >= m.maxSessions {
		m.mu.Unlock()
		return nil, &Error{Code: "debug_busy", Retryable: true,
			Message: fmt.Sprintf("too many debug sessions (max %d)", m.maxSessions),
			Hint:    "stop an idle session with debug_stop"}
	}
	m.mu.Unlock()

	dialCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	raw, err := m.dial(dialCtx, o.DAPAddr)
	if err != nil {
		return nil, &Error{Code: "debug_unavailable", Retryable: true,
			Message: fmt.Sprintf("dial %s: %v", o.DAPAddr, err),
			Hint:    "start dlv dap --listen " + o.DAPAddr + " with -N -l build"}
	}
	client := newDAPClient(raw)
	s := &Session{
		id: newSessionID(), target: o.Target, container: o.Container,
		dapAddr: o.DAPAddr, mode: o.Mode, created: time.Now().UTC(),
		client: client, timeout: m.timeout,
	}
	if err := s.handshake(ctx, o); err != nil {
		client.close()
		return nil, err
	}
	m.mu.Lock()
	m.sessions[s.id] = s
	m.mu.Unlock()
	info := s.info()
	return &info, nil
}

func (s *Session) handshake(ctx context.Context, o StartOptions) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout*3)
	defer cancel()
	if _, err := s.client.send(ctx, "initialize", map[string]any{"adapterID": "inspector-gopher"}); err != nil {
		return &Error{Code: "debug_handshake", Retryable: true, Message: fmt.Sprintf("initialize: %v", err)}
	}
	switch o.Mode {
	case "launch":
		if _, err := s.client.send(ctx, "launch", map[string]any{
			"mode": "exec", "program": o.Program, "args": o.Args,
		}); err != nil {
			return &Error{Code: "debug_launch", Retryable: false, Message: fmt.Sprintf("launch: %v", err)}
		}
	case "attach":
		if _, err := s.client.send(ctx, "attach", map[string]any{
			"mode": "local", "processId": o.PID,
		}); err != nil {
			return &Error{Code: "debug_attach", Retryable: false, Message: fmt.Sprintf("attach: %v", err)}
		}
	case "remote":
		if _, err := s.client.send(ctx, "attach", map[string]any{"mode": "remote"}); err != nil {
			return &Error{Code: "debug_attach", Retryable: false, Message: fmt.Sprintf("remote attach: %v", err)}
		}
	}
	// configurationDone is best-effort: some servers proceed without it.
	_ = func() error {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, err := s.client.send(cctx, "configurationDone", nil)
		return err
	}()
	return nil
}

// Get returns a session by ID.
func (m *Manager) Get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, &Error{Code: "debug_not_found", Retryable: false,
			Message: fmt.Sprintf("session %q not found", id), Hint: "list live sessions with debug_list"}
	}
	return s, nil
}

// Stop disconnects and removes a session.
func (m *Manager) Stop(ctx context.Context, id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	closed := s.closed
	s.closed = true
	s.mu.Unlock()
	if !closed {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, _ = s.client.send(cctx, "disconnect", map[string]any{"terminateDebuggee": false})
		cancel()
		s.client.close()
	}
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	return nil
}

// SetBreakpoint sets one breakpoint via setBreakpoints.
func (s *Session) SetBreakpoint(ctx context.Context, file string, line int) (*Breakpoint, error) {
	if strings.TrimSpace(file) == "" {
		return nil, &Error{Code: "debug_bad_breakpoint", Retryable: false, Message: "file is required"}
	}
	if line <= 0 {
		return nil, &Error{Code: "debug_bad_breakpoint", Retryable: false, Message: "line must be > 0"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, &Error{Code: "debug_closed", Retryable: false, Message: "session is closed"}
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	body, err := s.client.send(ctx, "setBreakpoints", map[string]any{
		"source": map[string]any{"path": file}, "breakpoints": []any{map[string]any{"line": line}},
	})
	if err != nil {
		return nil, &Error{Code: "debug_breakpoint", Retryable: false, Message: err.Error()}
	}
	var parsed struct {
		Breakpoints []struct {
			ID       int  `json:"id"`
			Verified bool `json:"verified"`
			Line     int  `json:"line"`
		} `json:"breakpoints"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return nil, &Error{Code: "debug_breakpoint", Retryable: false, Message: err.Error()}
	}
	if len(parsed.Breakpoints) == 0 {
		return nil, &Error{Code: "debug_breakpoint", Retryable: false, Message: "delve returned no breakpoints"}
	}
	b := parsed.Breakpoints[0]
	return &Breakpoint{ID: b.ID, File: file, Line: b.Line, Verified: b.Verified}, nil
}

// Continue resumes execution and waits for the next stop.
func (s *Session) Continue(ctx context.Context) (*StoppedInfo, error) {
	thread, err := s.firstThread(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, &Error{Code: "debug_closed", Retryable: false, Message: "session is closed"}
	}
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	s.client.drainEvents()
	if _, err := s.client.send(cctx, "continue", map[string]any{"threadId": thread}); err != nil {
		return nil, &Error{Code: "debug_continue", Retryable: true, Message: err.Error()}
	}
	return s.waitStopped(ctx)
}

// Step performs next, stepIn or stepOut and waits for the stop.
func (s *Session) Step(ctx context.Context, kind string, threadID int) (*StoppedInfo, error) {
	var cmd string
	switch kind {
	case "", "next":
		cmd = "next"
	case "in", "stepIn":
		cmd = "stepIn"
	case "out", "stepOut":
		cmd = "stepOut"
	default:
		return nil, &Error{Code: "debug_bad_step", Retryable: false,
			Message: fmt.Sprintf("step %q unknown", kind), Hint: "want next, in or out"}
	}
	if threadID <= 0 {
		thread, err := s.firstThread(ctx)
		if err != nil {
			return nil, err
		}
		threadID = thread
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, &Error{Code: "debug_closed", Retryable: false, Message: "session is closed"}
	}
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	s.client.drainEvents()
	if _, err := s.client.send(cctx, cmd, map[string]any{"threadId": threadID}); err != nil {
		return nil, &Error{Code: "debug_step", Retryable: true, Message: err.Error()}
	}
	return s.waitStopped(ctx)
}

func (s *Session) firstThread(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, &Error{Code: "debug_closed", Retryable: false, Message: "session is closed"}
	}
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	body, err := s.client.send(cctx, "threads", nil)
	if err != nil {
		return 0, &Error{Code: "debug_threads", Retryable: true, Message: err.Error()}
	}
	var parsed struct {
		Threads []struct {
			ID int `json:"id"`
		} `json:"threads"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return 0, &Error{Code: "debug_threads", Retryable: true, Message: err.Error()}
	}
	if len(parsed.Threads) == 0 {
		return 0, &Error{Code: "debug_threads", Retryable: true, Message: "no threads reported"}
	}
	return parsed.Threads[0].ID, nil
}

func (s *Session) waitStopped(ctx context.Context) (*StoppedInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout*6)
	defer cancel()
	ev, err := s.client.waitEvent(ctx, "stopped")
	if err != nil {
		// A terminated/exited event while waiting means the program ended.
		select {
		case term := <-s.client.events:
			if term.Event == "terminated" || term.Event == "exited" {
				return &StoppedInfo{Reason: term.Event}, nil
			}
			if term.Event == "stopped" {
				return stoppedFromEvent(term.Body)
			}
		default:
		}
		return nil, &Error{Code: "debug_wait", Retryable: true, Message: err.Error()}
	}
	return stoppedFromEvent(ev.Body)
}

func stoppedFromEvent(body jsontext.Value) (*StoppedInfo, error) {
	var parsed struct {
		Reason   string `json:"reason"`
		ThreadID int    `json:"threadId"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal([]byte(body), &parsed); err != nil {
			return nil, &Error{Code: "debug_wait", Retryable: true, Message: err.Error()}
		}
	}
	if parsed.Reason == "" {
		parsed.Reason = "stopped"
	}
	return &StoppedInfo{Reason: parsed.Reason, ThreadID: parsed.ThreadID}, nil
}

// Stack returns stack frames for a thread.
func (s *Session) Stack(ctx context.Context, threadID int) ([]Frame, error) {
	if threadID <= 0 {
		thread, err := s.firstThread(ctx)
		if err != nil {
			return nil, err
		}
		threadID = thread
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, &Error{Code: "debug_closed", Retryable: false, Message: "session is closed"}
	}
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	body, err := s.client.send(cctx, "stackTrace", map[string]any{"threadId": threadID})
	if err != nil {
		return nil, &Error{Code: "debug_stack", Retryable: true, Message: err.Error()}
	}
	var parsed struct {
		StackFrames []struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
			Source *struct {
				Path string `json:"path"`
			} `json:"source"`
		} `json:"stackFrames"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return nil, &Error{Code: "debug_stack", Retryable: true, Message: err.Error()}
	}
	out := make([]Frame, 0, len(parsed.StackFrames))
	for _, f := range parsed.StackFrames {
		file := ""
		if f.Source != nil {
			file = f.Source.Path
		}
		out = append(out, Frame{ID: f.ID, Name: f.Name, File: file, Line: f.Line, Column: f.Column})
	}
	return out, nil
}

// Vars returns locals for a frame via scopes+variables.
func (s *Session) Vars(ctx context.Context, frameID int) ([]Variable, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, &Error{Code: "debug_closed", Retryable: false, Message: "session is closed"}
	}
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	scopesBody, err := s.client.send(cctx, "scopes", map[string]any{"frameId": frameID})
	if err != nil {
		return nil, &Error{Code: "debug_vars", Retryable: true, Message: err.Error()}
	}
	var scopes struct {
		Scopes []struct {
			VariablesReference int `json:"variablesReference"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal([]byte(scopesBody), &scopes); err != nil {
		return nil, &Error{Code: "debug_vars", Retryable: true, Message: err.Error()}
	}
	var out []Variable
	for _, sc := range scopes.Scopes {
		if sc.VariablesReference == 0 {
			continue
		}
		varsBody, err := s.client.send(cctx, "variables", map[string]any{"variablesReference": sc.VariablesReference})
		if err != nil {
			return nil, &Error{Code: "debug_vars", Retryable: true, Message: err.Error()}
		}
		var vars struct {
			Variables []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
				Type  string `json:"type"`
			} `json:"variables"`
		}
		if err := json.Unmarshal([]byte(varsBody), &vars); err != nil {
			return nil, &Error{Code: "debug_vars", Retryable: true, Message: err.Error()}
		}
		for _, v := range vars.Variables {
			out = append(out, Variable{Name: v.Name, Value: v.Value, Type: v.Type})
		}
	}
	return out, nil
}

// Evaluate runs a Go expression in the context of a frame.
func (s *Session) Evaluate(ctx context.Context, expr string, frameID int) (*Variable, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, &Error{Code: "debug_bad_expr", Retryable: false, Message: "expression is required"}
	}
	if len(expr) > 4096 {
		return nil, &Error{Code: "debug_bad_expr", Retryable: false, Message: "expression too long (max 4096)"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, &Error{Code: "debug_closed", Retryable: false, Message: "session is closed"}
	}
	cctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	args := map[string]any{"expression": expr, "context": "repl"}
	if frameID > 0 {
		args["frameId"] = frameID
	}
	body, err := s.client.send(cctx, "evaluate", args)
	if err != nil {
		return nil, &Error{Code: "debug_evaluate", Retryable: false, Message: err.Error()}
	}
	var parsed struct {
		Result string `json:"result"`
		Type   string `json:"type"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return nil, &Error{Code: "debug_evaluate", Retryable: false, Message: err.Error()}
	}
	return &Variable{Name: expr, Value: parsed.Result, Type: parsed.Type}, nil
}
