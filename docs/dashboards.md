# Dashboards

`flamegraph_report` renders an interactive flamegraph dashboard for a
collected profile on three surfaces, so every MCP client gets a visual:

| Surface | Where | Needs |
|---|---|---|
| MCP Apps inline UI (`ui://…`, `text/html;profile=mcp-app`, SEP-1865) | Claude Desktop / Web and other MCP Apps hosts | nothing extra |
| Browser preview (`GET /dashboard/flamegraph/{id}`) | Claude Code CLI and any chat client | `--http 127.0.0.1:8080` (or `server.http_addr`) |
| Saved HTML artifact (`file://…/flamegraph-<id>.html`) | offline viewing, sharing, archiving | default on (`save_file: false` skips it) |

Pages are self-contained (no CDN, no fetch): SVG flamegraph with click to
zoom, breadcrumbs, frame search, hover values, top-functions table, and the
folded stacks for speedscope/inferno. Machine-readable hooks:
`window.__INSPECTOR_FLAMEGRAPH__` holds `{profile_id, total, tree}`.

## Usage

```text
1. profile_cpu(target="api-local", seconds=15)   → profile_id
2. flamegraph_report(profile_id="cpu-…")          → dashboard links
```

`flamegraph_report` arguments: `profile_id` (required),
`title`, `max_depth`, `max_nodes`, `save_file=true`.

The tool answers with text (markdown links for the agent to quote),
an embedded `text/html` dashboard, and structured data
(`file_path`, `dashboard_url`, `ui_resource_uri`, `top_flat`, …).
It also carries `_meta.ui.resourceUri = ui://inspector-gopher/flamegraph`,
as does `flamegraph_data`, so UI-capable hosts offer the dashboard view.

UI resources:

- `ui://inspector-gopher/flamegraph` — landing viewer with recent profiles.
- `ui://inspector-gopher/flamegraph/{profile_id}` — per-profile dashboard,
  rendered on demand (no file write).

HTTP (Streamable MCP stays on `/` unchanged):

- `GET /dashboard/` — index of recent profiles.
- `GET /dashboard/flamegraph/{profile_id}` — profile dashboard (404 for
  unknown IDs, 405 + `Allow: GET` for other methods).

## Claude Code setup

```json
{ "mcpServers": { "inspector-gopher": {
  "command": "/path/to/inspector-gopher",
  "args": ["--config", "/path/to/targets.yaml",
           "--http", "127.0.0.1:8080"]
}}}
```

Ask for a flamegraph; open the `http://127.0.0.1:8080/dashboard/…` link the
tool returns in a browser tab.

## Configuration

```yaml
dashboard:
  dir: ""       # default ~/.cache/inspector-gopher/dashboards
  base_url: ""  # default http://<server.http_addr>; sets preview links
```

Env: `INSPECTOR_DASHBOARD_DIR`, `INSPECTOR_DASHBOARD_BASE_URL`.

## Verify

```bash
go test ./internal/dashboard/ ./internal/mcp/ -run 'Dashboard|Flamegraph|Render|Save|ValidID' -v
```
