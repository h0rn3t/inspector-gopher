# Configuration

`inspector-gopher` reads `targets.yaml` (YAML or JSON) plus `INSPECTOR_*` env overrides.

```yaml
local:
  - name: api-local
    pprof_url: http://127.0.0.1:6060
    dap_addr: 127.0.0.1:2345   # optional Delve DAP endpoint
debug:
  - name: api-dbg
    dap_addr: 127.0.0.1:2345
docker:
  socket: unix:///var/run/docker.sock   # or $DOCKER_HOST / $INSPECTOR_DOCKER_SOCKET
  allowlist: ["api-*", "label:team=backend"]
  default_pprof_port: 6060
  default_dap_port: 2345
limits:
  max_profile_mb: 64
  default_timeout_s: 60
  max_cpu_seconds: 120
  max_trace_seconds: 30
debug_cfg:
  max_sessions: 4
  timeout_s: 10
server:
  http_addr: ""        # e.g. 127.0.0.1:8080 to enable Streamable HTTP + dashboard previews
  stdio: true
dashboard:
  dir: ""              # default ~/.cache/inspector-gopher/dashboards
  base_url: ""          # default http://<http_addr> for preview links
```

## Rules

- Tools accept `target` only from `local[]` or from hosts matching `docker.allowlist` globs.
- Anything else returns `forbidden_target` **without any network access** (no SSRF probe).
- `debug_start` with an explicit `dap_addr` dials loopback only; anything else
  is `forbidden_target` without a dial. Config-resolved addresses are operator input.
- Docker exec runs only for allowlisted containers and only fixed `curl`/`wget` argv — never shell.
- Env overrides: `INSPECTOR_HTTP_ADDR`, `INSPECTOR_STDIO`, `INSPECTOR_DOCKER_SOCKET`,
  `INSPECTOR_DOCKER_ALLOWLIST` (comma-separated), `INSPECTOR_MAX_PROFILE_MB`,
  `INSPECTOR_TIMEOUT_S`, `INSPECTOR_MAX_CPU_SECONDS`,
  `INSPECTOR_DEBUG_MAX_SESSIONS`, `INSPECTOR_DEBUG_TIMEOUT_S`,
  `INSPECTOR_DASHBOARD_DIR`, `INSPECTOR_DASHBOARD_BASE_URL`.
  `DOCKER_HOST` sets the socket too.

## Verify

```bash
go test ./internal/config/ ./internal/targets/
```
