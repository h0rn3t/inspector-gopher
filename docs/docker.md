# Docker profiling

Two collection paths, fastest first:

1. **Published port** — if the container publishes the pprof port
   (`-p 6060:6060`), `docker_list_targets` reports `pprof_reachable: true`
   with `pprof_url`, and `docker_profile` reuses the normal HTTP collector
   (`collection: http-via-published-port`).
2. **docker exec** — if pprof listens only on container localhost,
   the server runs a fixed `curl`/`wget` argv inside the container
   (`collection: docker-exec`). Timeout is `seconds + 15s`.

## Requirements

- Container must be in `docker.allowlist` (name glob, ID glob, or `label:key=value`).
- Container needs `curl` or `wget`; distroless images without them return
  `exec_tooling_missing` — publish the pprof port instead:
  `docker run -p 6060:6060 my-go-app`.
- Target app must import `net/http/pprof` (or expose the same endpoints).

## Verify

```bash
docker run -d --name pprof-demo -p 6060:6060 pprof-demo:local
go run ./cmd/inspector-gopher --config targets.yaml --http 127.0.0.1:8080
# docker_list_targets → pprof-demo reachable; docker_profile(container=pprof-demo, profile=cpu, seconds=5)
```
