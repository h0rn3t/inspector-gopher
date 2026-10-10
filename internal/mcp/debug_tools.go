package mcp

import (
	"context"
	"fmt"
	"net"

	"github.com/h0rn3t/inspector-gopher/internal/debug"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func strArg(in map[string]any, key string) string {
	v, _ := in[key].(string)
	return v
}

func floatArg(in map[string]any, key string) int {
	f, _ := in[key].(float64)
	return int(f)
}

func (s *Server) registerDebugTools(
	ms *mcp.Server,
	ro, rw *mcp.ToolAnnotations,
) {
	add := func(name, desc string, ann *mcp.ToolAnnotations, h func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error)) {
		mcp.AddTool(ms, &mcp.Tool{Name: name, Description: desc, Annotations: ann}, h)
	}

	add(ToolDebugStart, "Start a Go debug session via Delve DAP (local target or allowlisted container).", rw, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		opts, err := s.resolveDebugStart(ctx, in)
		if err != nil {
			s.count(false)
			return nil, nil, err
		}
		info, err := s.deps.Debugger.Start(ctx, opts)
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, info, nil
	})
	add(ToolDebugStop, "Stop a debug session and disconnect.", rw, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		id := strArg(in, "session")
		err := s.deps.Debugger.Stop(ctx, id)
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"stopped": id}, nil
	})
	add(ToolDebugList, "List live debug sessions.", ro, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		s.count(true)
		return nil, map[string]any{"sessions": s.deps.Debugger.List()}, nil
	})
	add(ToolDebugBreakpoint, "Set a breakpoint at file:line in a debug session.", rw, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		sess, err := s.deps.Debugger.Get(strArg(in, "session"))
		if err != nil {
			s.count(false)
			return nil, nil, err
		}
		bp, err := sess.SetBreakpoint(ctx, strArg(in, "file"), floatArg(in, "line"))
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, bp, nil
	})
	add(ToolDebugContinue, "Resume execution until the next stop.", rw, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		sess, err := s.deps.Debugger.Get(strArg(in, "session"))
		if err != nil {
			s.count(false)
			return nil, nil, err
		}
		stopped, err := sess.Continue(ctx)
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, stopped, nil
	})
	add(ToolDebugStep, "Step over/into/out (step: next, in, out).", rw, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		sess, err := s.deps.Debugger.Get(strArg(in, "session"))
		if err != nil {
			s.count(false)
			return nil, nil, err
		}
		step := strArg(in, "step")
		if step == "" {
			step = "next"
		}
		stopped, err := sess.Step(ctx, step, floatArg(in, "thread"))
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, stopped, nil
	})
	add(ToolDebugStack, "Stack frames for a stopped thread.", ro, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		sess, err := s.deps.Debugger.Get(strArg(in, "session"))
		if err != nil {
			s.count(false)
			return nil, nil, err
		}
		frames, err := sess.Stack(ctx, floatArg(in, "thread"))
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"frames": frames}, nil
	})
	add(ToolDebugVars, "Local variables for a stack frame.", ro, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		sess, err := s.deps.Debugger.Get(strArg(in, "session"))
		if err != nil {
			s.count(false)
			return nil, nil, err
		}
		vars, err := sess.Vars(ctx, floatArg(in, "frame"))
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"variables": vars}, nil
	})
	add(ToolDebugEvaluate, "Evaluate a Go expression in a frame.", ro, func(ctx context.Context, req *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
		sess, err := s.deps.Debugger.Get(strArg(in, "session"))
		if err != nil {
			s.count(false)
			return nil, nil, err
		}
		val, err := sess.Evaluate(ctx, strArg(in, "expression"), floatArg(in, "frame"))
		s.count(err == nil)
		if err != nil {
			return nil, nil, err
		}
		return nil, val, nil
	})
}

// resolveDebugStart maps tool args to validated StartOptions without network
// access: local names resolve against config, containers against the Docker
// allowlist plus probed DAP reachability. An explicit dap_addr is allowed
// only for loopback hosts (local dev / published container ports) to keep
// the no-SSRF guarantee: a configured target name cannot be used to dial
// arbitrary internal hosts.
func (s *Server) resolveDebugStart(ctx context.Context, in map[string]any) (debug.StartOptions, error) {
	target := strArg(in, "target")
	container := strArg(in, "container")
	mode := strArg(in, "mode")
	if mode == "" {
		mode = "remote"
	}
	var args []string
	if raw, ok := in["args"].([]any); ok {
		for _, a := range raw {
			s, _ := a.(string)
			args = append(args, s)
		}
	}
	opts := debug.StartOptions{
		Target: target, Container: container, Mode: mode,
		Program: strArg(in, "program"), Args: args, PID: floatArg(in, "pid"),
		DAPAddr: strArg(in, "dap_addr"),
	}
	if opts.DAPAddr != "" {
		if target == "" && container == "" {
			return debug.StartOptions{}, &debug.Error{Code: "debug_no_origin", Retryable: false,
				Message: "target or container is required with dap_addr"}
		}
		if err := debug.ValidateAddr(opts.DAPAddr); err != nil {
			return debug.StartOptions{}, err
		}
		if err := checkLoopbackDAP(opts.DAPAddr); err != nil {
			return debug.StartOptions{}, err
		}
		if container != "" {
			if err := s.checkContainerAllowed(ctx, container); err != nil {
				return debug.StartOptions{}, err
			}
		} else if err := s.checkTargetAllowed(target); err != nil {
			return debug.StartOptions{}, err
		}
		return opts, nil
	}
	if target != "" {
		addr, err := s.dapAddrForTarget(target)
		if err != nil {
			return debug.StartOptions{}, err
		}
		opts.DAPAddr = addr
		return opts, nil
	}
	if container != "" {
		addr, err := s.dapAddrForContainer(ctx, container)
		if err != nil {
			return debug.StartOptions{}, err
		}
		opts.DAPAddr = addr
		return opts, nil
	}
	return debug.StartOptions{}, &debug.Error{Code: "debug_no_origin", Retryable: false,
		Message: "target, container or dap_addr is required"}
}

func (s *Server) checkTargetAllowed(target string) error {
	for _, t := range s.deps.Config.Local {
		if t.Name == target {
			return nil
		}
	}
	for _, t := range s.deps.Config.Debug {
		if t.Name == target {
			return nil
		}
	}
	if _, err := s.deps.Registry.Resolve(target); err != nil {
		return &debug.Error{Code: "forbidden_target", Retryable: false,
			Message: fmt.Sprintf("target %q is not configured", target),
			Hint:    "add it to targets.yaml local/debug list or docker allowlist"}
	}
	return nil
}

func (s *Server) dapAddrForTarget(target string) (string, error) {
	for _, t := range s.deps.Config.Debug {
		if t.Name == target {
			return t.DAPAddr, nil
		}
	}
	for _, t := range s.deps.Config.Local {
		if t.Name == target && t.DAPAddr != "" {
			return t.DAPAddr, nil
		}
	}
	return "", &debug.Error{Code: "debug_no_dap", Retryable: false,
		Message: fmt.Sprintf("target %q has no dap_addr", target),
		Hint:    "set local[].dap_addr or debug[] entry, or pass dap_addr explicitly"}
}

func (s *Server) checkContainerAllowed(ctx context.Context, container string) error {
	if s.deps.Docker == nil {
		return &debug.Error{Code: "docker_unavailable", Retryable: false, Message: "docker is not configured"}
	}
	containers, err := s.deps.Docker.Targets(ctx)
	if err != nil {
		return err
	}
	for _, c := range containers {
		if c.Name == container || c.ID == container {
			if !s.deps.Docker.Allowed(c.Name, c.ID, nil) {
				return &debug.Error{Code: "forbidden_target", Retryable: false,
					Message: fmt.Sprintf("container %q is not in the allowlist", container)}
			}
			return nil
		}
	}
	return &debug.Error{Code: "unknown_target", Retryable: false,
		Message: fmt.Sprintf("container %q not found", container)}
}

func (s *Server) dapAddrForContainer(ctx context.Context, container string) (string, error) {
	if s.deps.Docker == nil {
		return "", &debug.Error{Code: "docker_unavailable", Retryable: false, Message: "docker is not configured"}
	}
	containers, err := s.deps.Docker.Targets(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range containers {
		if c.Name != container && c.ID != container {
			continue
		}
		if !s.deps.Docker.Allowed(c.Name, c.ID, nil) {
			return "", &debug.Error{Code: "forbidden_target", Retryable: false,
				Message: fmt.Sprintf("container %q is not in the allowlist", container)}
		}
		if c.DAPAddr != "" {
			return c.DAPAddr, nil
		}
		if c.DAPReachable {
			return c.DAPAddr, nil
		}
		return "", &debug.Error{Code: "debug_no_dap", Retryable: false,
			Message: fmt.Sprintf("container %q exposes no DAP port", container),
			Hint:    "run dlv dap --listen :2345 and publish it (-p 2345:2345)"}
	}
	return "", &debug.Error{Code: "unknown_target", Retryable: false,
		Message: fmt.Sprintf("container %q not found", container)}
}

// checkLoopbackDAP keeps the no-SSRF guarantee for explicit dap_addr: only
// loopback hosts are dialable (local dlv or published container ports on
// 127.0.0.1). Config-resolved addresses are trusted as operator input.
func checkLoopbackDAP(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return &debug.Error{Code: "debug_bad_addr", Retryable: false, Message: err.Error()}
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return &debug.Error{Code: "forbidden_target", Retryable: false,
		Message: fmt.Sprintf("dap host %q is not loopback", host),
		Hint:    "use a configured target/container, or a 127.0.0.1 published port"}
}
