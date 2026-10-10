package debug

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	json "encoding/json/v2"
)

// fakeDAPServer answers a scripted DAP conversation over TCP.
type fakeDAPServer struct {
	t        *testing.T
	listener net.Listener
	mu       sync.Mutex
	handlers map[string]func(seq int) string
}

func newFakeDAPServer(t *testing.T) *fakeDAPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeDAPServer{t: t, listener: ln, handlers: map[string]func(seq int) string{}}
	f.handlers["initialize"] = func(seq int) string { return `{"capabilities":{}}` }
	f.handlers["launch"] = func(seq int) string { return `{}` }
	f.handlers["attach"] = func(seq int) string { return `{}` }
	f.handlers["configurationDone"] = func(seq int) string { return `{}` }
	f.handlers["threads"] = func(seq int) string {
		return `{"threads":[{"id":1,"name":"main"}]}`
	}
	f.handlers["setBreakpoints"] = func(seq int) string {
		return `{"breakpoints":[{"id":7,"verified":true,"line":42}]}`
	}
	f.handlers["continue"] = func(seq int) string {
		return `{"allThreadsContinued":true}`
	}
	f.handlers["next"] = func(seq int) string {
		return `{}`
	}
	f.handlers["stackTrace"] = func(seq int) string {
		return `{"stackFrames":[{"id":11,"name":"main.hotLoop","line":20,"column":1,"source":{"path":"/app/main.go"}}],"totalFrames":1}`
	}
	f.handlers["scopes"] = func(seq int) string {
		return `{"scopes":[{"name":"Locals","variablesReference":100}]}`
	}
	f.handlers["variables"] = func(seq int) string {
		return `{"variables":[{"name":"n","value":"3","type":"int"}]}`
	}
	f.handlers["evaluate"] = func(seq int) string {
		return `{"result":"3","type":"int"}`
	}
	f.handlers["disconnect"] = func(seq int) string { return `{}` }
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeDAPServer) addr() string { return f.listener.Addr().String() }

func (f *fakeDAPServer) serve() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeDAPServer) handle(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := conn.Read(tmp)
		if err != nil {
			return
		}
		buf = append(buf, tmp[:n]...)
		for {
			msg, rest, ok := splitDAP(buf)
			if !ok {
				break
			}
			buf = rest
			var req struct {
				Seq     int            `json:"seq"`
				Command string         `json:"command"`
				Args    map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}
			f.mu.Lock()
			h := f.handlers[req.Command]
			f.mu.Unlock()
			if h == nil {
				writeDAP(conn, req.Seq, req.Command, false, "unsupported", "{}")
				continue
			}
			writeDAP(conn, req.Seq, req.Command, true, "", h(req.Seq))
			// Delve answers continue/next with a response first, then a
			// stopped event once the goroutine halts.
			switch req.Command {
			case "continue":
				writeEvent(conn, "stopped", `{"reason":"breakpoint","threadId":1}`)
			case "next", "stepIn", "stepOut":
				writeEvent(conn, "stopped", `{"reason":"step","threadId":1}`)
			}
		}
	}
}

func splitDAP(buf []byte) (msg, rest []byte, ok bool) {
	s := string(buf)
	idx := strings.Index(s, "\r\n\r\n")
	if idx < 0 {
		return nil, buf, false
	}
	var length int
	for line := range strings.Lines(s[:idx]) {
		line = strings.TrimRight(line, "\r\n")
		if v, found := strings.CutPrefix(line, "Content-Length:"); found {
			_, _ = fmt.Sscanf(strings.TrimSpace(v), "%d", &length)
		}
	}
	if len(buf) < idx+4+length {
		return nil, buf, false
	}
	return buf[idx+4 : idx+4+length], buf[idx+4+length:], true
}

func writeDAP(conn net.Conn, reqSeq int, cmd string, success bool, message, body string) {
	if body == "" {
		body = "{}"
	}
	resp := fmt.Sprintf(`{"seq":%d,"type":"response","request_seq":%d,"success":%v,"command":%q,"message":%q,"body":%s}`,
		900+reqSeq, reqSeq, success, cmd, message, body)
	_, _ = fmt.Fprintf(conn, "Content-Length: %d\r\n\r\n%s", len(resp), resp)
}

func writeEvent(conn net.Conn, event, body string) {
	msg := fmt.Sprintf(`{"seq":950,"type":"event","event":%q,"body":%s}`, event, body)
	_, _ = fmt.Fprintf(conn, "Content-Length: %d\r\n\r\n%s", len(msg), msg)
}

func TestValidateOptionsContract(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		opts    StartOptions
		wantErr string
	}{
		{name: "no origin", opts: StartOptions{DAPAddr: "127.0.0.1:2345", Mode: "remote"}, wantErr: "debug_no_origin"},
		{name: "both origins", opts: StartOptions{Target: "a", Container: "b", DAPAddr: "127.0.0.1:2345", Mode: "remote"}, wantErr: "debug_bad_origin"},
		{name: "empty addr", opts: StartOptions{Target: "a", Mode: "remote"}, wantErr: "debug_bad_addr"},
		{name: "bad port", opts: StartOptions{Target: "a", DAPAddr: "127.0.0.1:notaport", Mode: "remote"}, wantErr: "debug_bad_addr"},
		{name: "port out of range", opts: StartOptions{Target: "a", DAPAddr: "127.0.0.1:99999", Mode: "remote"}, wantErr: "debug_bad_addr"},
		{name: "unknown mode", opts: StartOptions{Target: "a", DAPAddr: "127.0.0.1:2345", Mode: "fly"}, wantErr: "debug_bad_mode"},
		{name: "launch needs program", opts: StartOptions{Target: "a", DAPAddr: "127.0.0.1:2345", Mode: "launch"}, wantErr: "debug_bad_mode"},
		{name: "attach needs pid", opts: StartOptions{Target: "a", DAPAddr: "127.0.0.1:2345", Mode: "attach"}, wantErr: "debug_bad_mode"},
		{name: "remote ok", opts: StartOptions{Target: "a", DAPAddr: "127.0.0.1:2345", Mode: "remote"}},
		{name: "launch ok", opts: StartOptions{Target: "a", DAPAddr: "127.0.0.1:2345", Mode: "launch", Program: "./app"}},
		{name: "attach ok", opts: StartOptions{Container: "c", DAPAddr: "127.0.0.1:2345", Mode: "attach", PID: 42}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateOptions(tt.opts)
			if tt.wantErr == "" && err != nil {
				t.Errorf("ValidateOptions(%+v) = %v, want nil", tt.opts, err)
			}
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ValidateOptions(%+v) = nil, want %q", tt.opts, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ValidateOptions(%+v) = %v, want %q", tt.opts, err, tt.wantErr)
				}
			}
		})
	}
}

func TestSessionLifecycleContract(t *testing.T) {
	f := newFakeDAPServer(t)
	m := NewManager(4, 5*time.Second)
	ctx := t.Context()

	info, err := m.Start(ctx, StartOptions{Target: "api-local", DAPAddr: f.addr(), Mode: "remote"})
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	if info.ID == "" || info.Target != "api-local" {
		t.Errorf("Start() = %+v, want id and target", info)
	}
	if m.Count() != 1 {
		t.Errorf("Count() = %d, want 1", m.Count())
	}

	s, err := m.Get(info.ID)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	bp, err := s.SetBreakpoint(ctx, "/app/main.go", 42)
	if err != nil {
		t.Fatalf("SetBreakpoint() error = %v, want nil", err)
	}
	if !bp.Verified || bp.Line != 42 {
		t.Errorf("SetBreakpoint() = %+v, want verified line 42", bp)
	}
	if _, err := s.SetBreakpoint(ctx, "", 0); err == nil {
		t.Error("SetBreakpoint(empty) = nil, want error")
	}

	stopped, err := s.Continue(ctx)
	if err != nil {
		t.Fatalf("Continue() error = %v, want nil", err)
	}
	if stopped.Reason != "breakpoint" {
		t.Errorf("Continue() reason = %q, want breakpoint", stopped.Reason)
	}

	stepped, err := s.Step(ctx, "next", 0)
	if err != nil {
		t.Fatalf("Step() error = %v, want nil", err)
	}
	if stepped.Reason != "step" {
		t.Errorf("Step() reason = %q, want step", stepped.Reason)
	}
	if _, err := s.Step(ctx, "teleport", 0); err == nil {
		t.Error("Step(teleport) = nil, want error")
	}

	frames, err := s.Stack(ctx, 1)
	if err != nil {
		t.Fatalf("Stack() error = %v, want nil", err)
	}
	if len(frames) != 1 || frames[0].Name != "main.hotLoop" || frames[0].File != "/app/main.go" {
		t.Errorf("Stack() = %+v, want main.hotLoop at /app/main.go", frames)
	}

	vars, err := s.Vars(ctx, 11)
	if err != nil {
		t.Fatalf("Vars() error = %v, want nil", err)
	}
	if len(vars) != 1 || vars[0].Name != "n" || vars[0].Value != "3" {
		t.Errorf("Vars() = %+v, want n=3", vars)
	}

	val, err := s.Evaluate(ctx, "n+1", 11)
	if err != nil {
		t.Fatalf("Evaluate() error = %v, want nil", err)
	}
	if val.Value != "3" {
		t.Errorf("Evaluate() = %+v, want 3", val)
	}
	if _, err := s.Evaluate(ctx, "  ", 0); err == nil {
		t.Error("Evaluate(empty) = nil, want error")
	}

	if err := m.Stop(ctx, info.ID); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	if m.Count() != 0 {
		t.Errorf("Count() = %d, want 0", m.Count())
	}
	if _, err := m.Get(info.ID); err == nil {
		t.Error("Get(stopped) = nil, want error")
	}
	if err := m.Stop(ctx, "dbg-missing"); err == nil {
		t.Error("Stop(missing) = nil, want error")
	}
}

func TestStartRefusesWhenFull(t *testing.T) {
	f := newFakeDAPServer(t)
	m := NewManager(1, 5*time.Second)
	ctx := t.Context()
	first, err := m.Start(ctx, StartOptions{Target: "a", DAPAddr: f.addr(), Mode: "remote"})
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	if _, err := m.Start(ctx, StartOptions{Target: "b", DAPAddr: f.addr(), Mode: "remote"}); err == nil {
		t.Error("second Start() = nil, want debug_busy")
	} else if !strings.Contains(err.Error(), "debug_busy") {
		t.Errorf("second Start() = %v, want debug_busy", err)
	}
	_ = m.Stop(ctx, first.ID)
}

func TestStartFailsWithoutDAP(t *testing.T) {
	m := NewManager(4, time.Second)
	ctx := t.Context()
	_, err := m.Start(ctx, StartOptions{Target: "a", DAPAddr: "127.0.0.1:1", Mode: "remote"})
	if err == nil {
		t.Fatal("Start() = nil, want debug_unavailable")
	}
	if !strings.Contains(err.Error(), "debug_unavailable") {
		t.Errorf("Start() = %v, want debug_unavailable", err)
	}
}
