# Tasks: go-profiler-mcp

## 1. Scaffolding и SDK-spike

- [x] 1.1 Инициализировать Go-модуль `inspector-gopher` (go ≥1.24), структуру `cmd/inspector-gopher`, `internal/{mcp,targets,collector,docker,analyzer,store,config}`, `go.mod`/`Makefile`/`.golangci.yml`; верификация: `go build ./...` и `go vet ./...` проходят.
- [x] 1.2 Spike MCP-транспорта: минимальный сервер с одним tool `server_health` на официальном `modelcontextprotocol/go-sdk` через stdio, handshake проверен pipe-тестом `echo '{"jsonrpc":"2.0",...}' | go run ./cmd/inspector-gopher --stdio`; при несовместимости зафиксировать переход на `mark3labs/mcp-go` (см. design Open Questions); верификация: `tools/list` возвращает `server_health`.
- [x] 1.3 Добавить зависимости `docker/docker`, `google/pprof`, `gopkg.in/yaml.v3`; верификация: `go mod tidy && go build ./...` проходят, версии зафиксированы в `go.mod`.

## 2. Конфиг, цели и MCP-каркас

- [x] 2.1 Реализовать `internal/config` (YAML/JSON `targets.yaml` + env `INSPECTOR_*`: local цели, docker socket/allowlist, лимиты, server http/stdio) и `internal/targets` (резолв имени → endpoint, проверка allowlist до любых вызовов); верификация: unit-тесты резолва и запрета вне allowlist (`forbidden_target` без сетевого вызова, проверено mock-транспортом).
- [x] 2.2 Реализовать `internal/mcp` каркас: регистрация всех 12 инструментов с JSON Schema и аннотациями side-effect, ресурсы `config://targets` и `profile://recent`, типизированные ошибки (`unknown_target`, `connection_failed`, `timeout`, `pprof_unavailable`, `docker_unavailable`, `profile_too_large`, `forbidden_target`, `profile_not_found`, `exec_tooling_missing`) с `retryable`; верификация: `tools/list` показывает 12 tools, вызов с неизвестной целью возвращает `unknown_target`.
- [x] 2.3 Добавить stdio + HTTP/SSE (Streamable HTTP) транспорты с флагами `--stdio/--http :8080`, структурированные slog-JSON логи и `server_health` (версия, аптайм, счётчики); верификация: e2e-тест поднимает оба транспорта и `tools/list` совпадает на обоих.
- [x] 2.4 Задокументировать конфигурацию в `docs/config.md` (пример `targets.yaml`, allowlist, лимиты) и верифицировать: пример из доки парсится конфигом без ошибок.

## 3. Локальный сбор профилей

- [x] 3.1 Реализовать `internal/collector` pprof HTTP клиент: `profile_cpu` (`/debug/pprof/profile?seconds=N`, 5–120с, дефолт 30с), `profile_heap/mutex/block/goroutine` (включая текстовый дамп стеков для goroutine), стриминг в store с лимитом 64 МБ; верификация: интеграционный тест против `net/http/pprof` тестового сервера возвращает `format: pprof-protobuf` и корректный `duration_s`.
- [x] 3.2 Реализовать `capture_trace` (`/debug/pprof/trace?seconds≤30`) и `runtime_stats` (goroutine count, MemStats, GOMAXPROCS); верификация: тест против pprof-сервера проверяет `format: go-trace` и наличие `HeapAlloc/NumGC/GCCPUFraction` в stats.
- [x] 3.3 Реализовать `internal/store`: in-memory ring (дефолт 20 профилей) + файловый кэш `~/.cache/inspector-gopher/`, ID по sha, ресурс `profile://recent`; верификация: unit-тест eviction и восстановления метаданных сканированием кэша после рестарта.
- [x] 3.4 Добавить валидацию входов (диапазоны seconds, лимит размера → `profile_too_large`, таймауты → `timeout`/`pprof_unavailable` с `retryable: true` и подсказкой про `net/http/pprof`); верификация: тесты граничных значений и недоступной цели.

## 4. Docker-профилирование

- [x] 4.1 Реализовать `docker_list_targets` (list running-контейнеров, проба pprof через published port, поля `id/name/image/ports/pprof_reachable/pprof_url`); верификация: тест с fake Docker client + httptest pprof-сервером; недоступный socket → `docker_unavailable` с подсказкой про `DOCKER_HOST`.
- [x] 4.2 Реализовать `docker_profile` HTTP-путь (переиспользование `collector` по `pprof_url`, пометка `collection: http-via-published-port`); верификация: e2e против тестового контейнера/стаба с проброшенным портом.
- [x] 4.3 Реализовать `docker_profile` exec-путь (белый список команд curl/wget на `127.0.0.1:6060`, проба `which`, таймаут seconds+15с, пометка `collection: docker-exec`, distroless → `exec_tooling_missing`); верификация: unit-тесты сборки команд (нет shell-инъекций при вредоносном имени контейнера) + интеграционный тест exec против реального контейнера с curl.
- [x] 4.4 Добавить allowlist-проверку контейнеров (имя/ID/лейбл-глоб) до Docker API вызовов и audit-лог каждого exec; верификация: тест `forbidden_target` для контейнера вне allowlist без обращения к Docker API.
- [x] 4.5 Задокументировать Docker-режим в `docs/docker.md` (published-port vs exec, требования к образу, distroless-рекомендации) и верифицировать команды из доки на демо-контейнере.

## 5. Анализ и flamegraph

- [x] 5.1 Реализовать `analyze_profile` (парсинг через `google/pprof`, топ-N flat/cum с %, `total_samples`, `sample_type`); верификация: golden-тест на fixture CPU-профиля сверяет топ-10 с `go tool pprof -top`.
- [x] 5.2 Реализовать `diff_profiles` (delta flat/cum абс.+%, `regressions[]`/`improvements[]`/`summary`); верификация: golden-тест на паре fixture before/after находит инjected-регрессию первой строкой.
- [x] 5.3 Реализовать эвристики `gc_pressure` (GCCPUFraction>0.1, churn HeapAlloc vs HeapInuse) и `goroutine_leak_suspected` (рост count + доминантный стек ожидания), все findings с `heuristic: true`; верификация: unit-тесты на синтетических stats/дампах (позитив и негатив).
- [x] 5.4 Реализовать `flamegraph_data` (folded stacks для CPU, JSON-tree для heap/CPU с обрезкой и `truncated`); верификация: snapshot-тест folded-вывода и валидация tree (сумма children == root value).
- [x] 5.5 Задокументировать интерпретацию отчётов в `docs/analysis.md` (примеры выводов, пороги эвристик, как читать flamegraph-данные); верификация: примеры из доки воспроизводятся на fixture-профилях.

## 6. Сквозная проверка и релиз v0.1.0

- [x] 6.1 Поднять демо Go-сервис с `net/http/pprof` (известная горячая функция) локально и в Docker (published-port и exec-only варианты); верификация: все 12 tools отрабатывают на демо-целях, `analyze_profile` находит инjected-горячую функцию в топ-3.
- [x] 6.2 Прогнать `openspec validate --change go-profiler-mcp --strict`, `golangci-lint run`, `go test ./... -race -count=1`; верификация: все три команды зелёные, покрытие `collector/analyzer/docker` ≥70%.
- [x] 6.3 Собрать релизные артефакты (`make build`, README quickstart, пример `targets.yaml`, тег v0.1.0); верификация: fresh-checkout по README от нуля до первого `analyze_profile` проходит за ≤10 минут.
