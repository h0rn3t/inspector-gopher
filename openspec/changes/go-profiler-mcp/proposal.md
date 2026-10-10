# Proposal: go-profiler-mcp

## Why

Разработчики на Go тратят много времени на ручной сбор `pprof`/trace и сопоставление узких мест между локальным запуском и Docker-контейнерами. MCP-сервер `inspector-gopher` даст AI-агентам и разработчикам единый инструмент: собрать профиль локально или удалённо в Docker, получить готовый анализ топ-функций, GC/горутин и данные для flamegraph.

## What Changes

- Новый MCP-сервер на Go `inspector-gopher` с транспортами **stdio** (Copilot/Claude Code) и **HTTP/SSE**.
- Сбор CPU/heap/mutex/block/goroutine профилей через HTTP `net/http/pprof` endpoint цели.
- Сбор профилей через `docker exec` / Docker API для контейнеров без проброшенного pprof, плюс режим через проброшенный из контейнера HTTP pprof.
- Сбор `runtime/trace`, метрик GC (`runtime.MemStats`), числа горутин и `GODEBUG`/`debug` счётчиков.
- Автоанализ: топ-N функций по flat/cum, дифф двух профилей, детект утечек горутин и GC-прессинга, рекомендации.
- Выдача flamegraph-совместимых данных (folded stacks / профиль в base64 + JSON-дерево) для визуализации клиентом.
- Конфигурация целей (local PID/addr, container ID/name, pprof addr, таймауты, allowlist) и безопасные дефолты.

## Capabilities

### New Capabilities

- `mcp-server`: MCP-транспорты (stdio, HTTP/SSE), каталог tools/resources, конфигурация и безопасность сервера.
- `local-profiling`: сбор CPU/heap/mutex/block/goroutine/trace профилей локального Go-процесса через pprof HTTP и runtime.
- `docker-profiling`: сбор профилей из Docker-контейнеров через Docker API/exec и через проброшенный pprof HTTP.
- `profile-analysis`: анализ узких мест — топ функций, дифф, GC/горутины, рекомендации и flamegraph-данные.

### Modified Capabilities

- Нет (greenfield, `openspec/specs/` пуст).

## Impact

- Новый Go-модуль, зависимости: MCP SDK для Go (`modelcontextprotocol/go-sdk` или `mark3labs/mcp-go`), `docker/docker` client, `google/pprof` (парсинг), стандартные `net/http`, `runtime/pprof`, `runtime/trace`.
- Никаких изменений существующего кода (репо пустое); затронуты только новые файлы `openspec/changes/go-profiler-mcp/*` на этапе планирования.
- Эксплуатация: сервер работает локально у разработчика; доступ к Docker socket и к pprof-портам целей; профили могут содержать пути/символы — хранить локально, не отправлять наружу без согласия.
