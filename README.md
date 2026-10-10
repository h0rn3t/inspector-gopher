# inspector-gopher

MCP-сервер на Go для профілювання Go-програм локально та в Docker: збір
`pprof`/trace, аналіз вузьких місць, flamegraph-дані для AI-агентів.

## Встановлення

```bash
# Останній реліз (рекомендовано):
go install github.com/h0rn3t/inspector-gopher/cmd/inspector-gopher@latest

# Або конкретна версія:
go install github.com/h0rn3t/inspector-gopher/cmd/inspector-gopher@v0.1.0

# Перевірка:
inspector-gopher --version
```

Вимоги: Go ≥ 1.24, доступ до Docker socket (для Docker-режиму).

## Швидкий старт (≤10 хвилин)

```bash
# 1. Зібрати
go build -o inspector-gopher ./cmd/inspector-gopher

# 2. Підняти демо-ціль з pprof
go run ./demo/pprof-demo &

# 3. Запустити сервер (stdio для Copilot/Claude Code)
./inspector-gopher --config targets.yaml.example --stdio

# або HTTP:
./inspector-gopher --config targets.yaml.example --http 127.0.0.1:8080 < /dev/null
```

Приклад `targets.yaml` — див. `targets.yaml.example`. Документація: `docs/config.md`,
`docs/docker.md`, `docs/analysis.md`, `docs/debug.md`.

## MCP-клієнти

stdio:

```json
{ "mcpServers": { "inspector-gopher": {
  "command": "/path/to/inspector-gopher",
  "args": ["--config", "/path/to/targets.yaml"]
}}}
```

## Інструменти (22)

Збір: `profile_cpu`, `profile_heap`, `profile_mutex`, `profile_block`,
`profile_goroutine`, `capture_trace`, `runtime_stats`.
Docker: `docker_list_targets`, `docker_profile`.
Аналіз: `analyze_profile`, `diff_profiles`, `flamegraph_data`.
Службовий: `server_health`.
Дебаг (Delve DAP, local + Docker): `debug_start`, `debug_stop`, `debug_list`,
`debug_breakpoint`, `debug_continue`, `debug_step`, `debug_stack`,
`debug_vars`, `debug_evaluate` — див. `docs/debug.md`.
Ресурси: `config://targets`, `profile://recent`, `debug://sessions`.

## Приклад роботи

Живий сценарій через stdio проти демо-сервісу (`demo/pprof-demo`,
гаряча функція `main.hotLoop` компілює regexp у циклі).

**1. Здоров'я сервера** — `server_health`:

```json
{"version": "0.1.0", "uptime_s": 0, "calls": 1, "errors": 0, "tools": 13, "profiles": 0}
```

**2. Статистика рантайму** — `runtime_stats(target="api-local")`:

```json
{"num_goroutine": 1, "gomaxprocs": 0,
 "mem_stats": {"note": "detailed MemStats require target cooperation"},
 "collected_at": "2026-10-10T13:58:09Z"}
```

**3. Збір heap-профілю** — `profile_heap(target="api-local")`:

```json
{"profile_id": "heap-168cbc56b568", "target": "api-local", "kind": "heap",
 "format": "pprof-protobuf", "size": 2739, "collection": "local-http",
 "sample_type": "alloc_space/inuse_space", "data_base64": "H4sIAAAAAAAE/6RWXXDc1nU..."}
```

**4. Аналіз вузьких місць** — `analyze_profile(profile_id="heap-168cbc56b568", top_n=5)`:

| функція | flat | % |
|---|---|---|
| `net.IP.String` | 32768 | 64.7% |
| `regexp/syntax.(*parser).newRegexp` | 14044 | 27.7% |
| `regexp/syntax.(*parser).parseClass` | 9363 | 18.5% |
| `regexp/syntax.(*parser).repeat` | 4681 | 9.2% |

Парсер regexp у топі — саме він гальмує `hotLoop`.

**5. Flamegraph-дані** — `flamegraph_data(profile_id="heap-168cbc56b568", format="folded")`:

```
net/http.(*conn).serve;...;main.main.func1;main.hotLoop;regexp.Compile;... 3277
net/http.(*conn).serve;...;main.main.func1;main.hotLoop;regexp.Compile;regexp/syntax.Parse;... 14044
net/http.(*conn).serve;net.ipEmptyString;net.IP.String 32768
```

Кожен рядок — повний стек через `;` + лічильник, готовий до рендеру.

**6. Docker-цілі** — `docker_list_targets`:

```json
{"containers": [
  {"name": "wt-tests-run-10fc53c955d6", "image": "mcr.microsoft.com/playwright:v1.61.1-noble", "pprof_reachable": false},
  {"name": "ms-dispute-approve-dko", "image": "postgres:17", "pprof_reachable": false}]}
```

Не-Go контейнери коректно позначені `pprof_reachable: false`. Для Go
з `-p 6060:6060` буде `true` + `pprof_url`.

## Перевірка

```bash
go vet ./... && go test ./... -race -count=1
```
