# Debugging Go via Delve DAP

Nine MCP tools drive Delve's Debug Adapter Protocol over TCP. The server
never spawns `dlv` itself: point it at an already-running
`dlv dap --listen` endpoint (local process or container with a published
port).

## Start a DAP server

Local (build without optimizations first):

```bash
go build -gcflags="all=-N -l" -o /tmp/app ./...
dlv --listen 127.0.0.1:2345 --headless --accept-multiclient --api-version=2 exec /tmp/app
```

Docker (needs `SYS_PTRACE`; publish the DAP port):

```bash
docker run -d --name api-dbg --cap-add=SYS_PTRACE \
  --security-opt seccomp=unconfined -p 2345:2345 my-go-app \
  dlv --listen :2345 --headless --accept-multiclient --api-version=2 exec /app/server
```

## Configure

```yaml
local:
  - name: api-local
    pprof_url: http://127.0.0.1:6060
    dap_addr: 127.0.0.1:2345
docker:
  allowlist: ["api-*"]
  default_dap_port: 2345
debug_cfg:
  max_sessions: 4
  timeout_s: 10
```

Env overrides: `INSPECTOR_DEBUG_MAX_SESSIONS`, `INSPECTOR_DEBUG_TIMEOUT_S`.

## Tools (9)

| Tool | Args | Notes |
|---|---|---|
| `debug_start` | `target` or `container`, `mode` (remote/launch/attach), `program`, `args`, `pid`, `dap_addr` | resolves addr from config; containers must be allowlisted |
| `debug_list` | — | live sessions |
| `debug_breakpoint` | `session`, `file`, `line` | |
| `debug_continue` | `session` | waits for next stop |
| `debug_step` | `session`, `step` (next/in/out), `thread` | |
| `debug_stack` | `session`, `thread` | frames with file:line |
| `debug_vars` | `session`, `frame` | locals via scopes+variables |
| `debug_evaluate` | `session`, `expression`, `frame` | Go expression in frame |
| `debug_stop` | `session` | disconnect, keep debuggee alive |

Resource `debug://sessions` lists live sessions.

## Safety

- `continue`/`step` halt the debuggee: local/dev only, never prod.
- `target`/`container` resolve against config/allowlist **before any dial**
  (same SSRF rule as profiling); unknown names return
  `forbidden_target`/`unknown_target` without network access.
- An explicit `dap_addr` dials loopback only (`127.0.0.1`, `::1`,
  `localhost`); anything else is `forbidden_target` without a dial.
- Sessions cap at `debug_cfg.max_sessions` (`debug_busy` when full).

## Verify

```bash
go test ./internal/debug/ ./internal/mcp/ -race -count=1
```
