# Design: go-profiler-mcp

## Context

Репозиторий пустой (greenfield), OpenSpec-корень — `/Users/eugeneshershen/Projects/inspector-gopher`. См. `proposal.md` (Why) и `specs/*` (контракты поведения). Ограничения: сервер работает на машине разработчика, имеет доступ к Docker socket и к pprof-портам целей; профили могут содержать пути и символы — хранить только локально. Go toolchain ≥ 1.24.

## Goals / Non-Goals

**Goals:**
- Единый MCP-сервер на Go с транспортами stdio + HTTP/SSE и стабильным каталогом из ~12 инструментов.
- Сбор профилей тремя путями: pprof HTTP локально, pprof HTTP через проброшенный порт контейнера, `docker exec` внутри контейнера.
- Анализ на базе парсинга pprof (топ, дифф, эвристики GC/горутин) + выдача flamegraph-данных; без внешнего UI в v1.
- Безопасные дефолты: allowlist, таймауты, лимиты размера, только фиксированный набор exec-команд.

**Non-Goals:**
- Не пишем свой flamegraph-рендер/веб-UI (только данные для клиента).
- Не делаем continuous profiling / агент внутри целевого процесса в v1.
- Не поддерживаем не-Go процессы и удалённые кластеры (k8s) в v1 — только локальный Docker.
- Не храним историю профилей в БД — только in-memory ring + файловый кэш.

## Decisions

### 1. MCP SDK: `modelcontextprotocol/go-sdk` (официальный) с фолбэком `mark3labs/mcp-go`

**Почему:** официальный SDK — актуальный стандарт (Streamable HTTP + stdio из коробки), `mark3labs` — зрелая альтернатива с большим числом примеров. Решение: стартовать на официальном SDK; если на этапе tasks выяснится несовместимость с Copilot/Claude (проверить handshake), перейти на `mark3labs/mcp-go` — каталог инструментов изолирован в пакете `internal/mcp`, замена не затронет сбор/анализ.
**Альтернативы:** писать JSON-RPC вручную (отклонено — дорого поддерживать SSE/валидацию схем).

### 2. Структура модулей

```
cmd/inspector-gopher/      main, флаги (--config, --http, --stdio)
internal/mcp/              сервер, регистрация tools/resources, транспорт
internal/targets/          конфиг целей, allowlist, резолв target → endpoint
internal/collector/        pprof HTTP клиент (cpu/heap/mutex/block/goroutine/trace/stats)
internal/docker/           Docker client: list, probe pprof, exec-сбор
internal/analyzer/         парсинг pprof (google/pprof), топ/дифф/эвристики/flamegraph
internal/store/            in-memory ring профилей + файловый кэш, profile://recent
internal/config/           YAML/JSON конфиг + env-оверрайды
```

**Почему:** сбор, Docker и анализ независимо тестируемы; `collector` переиспользуется и для локальных целей, и для контейнеров с проброшенным портом.

### 3. Сбор через pprof HTTP — единый код для local и docker-published-port

`collector.Collect(kind, url, seconds)` делает GET на `/debug/pprof/...`, стримит тело в `store` с лимитом (дефолт 64 МБ, настраивается), считает sha/id, кладёт метаданные. Docker-путь с проброшенным портом просто резолвит `pprof_url` через `docker_list_targets` и вызывает тот же `Collect` с `collection: "http-via-published-port"`.
**Альтернатива:** отдельный код для Docker — отклонено (дублирование).

### 4. Docker exec-путь: фиксированные команды, без shell-инъекций

Exec выполняет только белый список: `wget -qO- http://127.0.0.1:6060/debug/pprof/{profile}...` или `curl -sS ...`, выбор рантайма — проба `which curl wget`. Команда собирается без подстановки пользовательского ввода (только `profile ∈ {cpu,heap,goroutine,trace}` и `seconds` как int в диапазоне). Таймаут exec = seconds + 15с. Distroless без curl/wget/sh → ошибка `exec_tooling_missing`.
**Почему exec, а не attach:** exec не требует модификации образа и работает с запущенным контейнером.

### 5. Анализ на `github.com/google/pprof` (парсинг), эвристики — отдельно

`analyzer` парсит protobuf через `google/pprof/profile`, считает flat/cum по функциям, дифф — вычитанием flat/cum с нормализацией на total. Эвристики GC/горутин — простые пороги (GCCPUFraction>0.1, рост горутин + доминантный стек ожидания), каждая помечена `heuristic: true` с объяснением. Flamegraph: folded — обходом `Sample.Location`, tree — агрегацией в префиксное дерево с обрезкой (дефолт глубина 200, ширина 5000 узлов, `truncated: true`).
**Альтернатива:** вызывать `go tool pprof` как subprocess — отклонено (зависимость от toolchain, медленнее, сложнее парсить вывод).

### 6. Конфиг и безопасность

`targets.yaml`: `local[] {name, pprof_url}`, `docker {socket, allowlist[] (имя/ID/лейбл-глоб), default_pprof_port}`, `limits {max_profile_mb, default_timeout_s, max_cpu_seconds}`, `server {http_addr, stdio}`. Env-оверрайды `INSPECTOR_*`. Allowlist проверяется до любого сетевого/Docker вызова. Docker socket по дефолту `unix:///var/run/docker.sock`, переопределяется `DOCKER_HOST`.

## Risks / Trade-offs

- [Risk] Официальный MCP Go SDK может не покрыть какой-то клиент → Mitigation: изоляция в `internal/mcp`, фолбэк на `mark3labs/mcp-go`, проверка handshake с Copilot/Claude на раннем таске.
- [Risk] CPU-профиль блокирует HTTP-хендлер цели на N секунд → Mitigation: дефолт 30с, максимум 120с, в описании инструмента явно указана длительность; конкурентные сборы на одну цель сериализуются мьютексом.
- [Risk] Trace/heap могут превышать лимиты памяти сервера → Mitigation: стриминг в файл с лимитом 64 МБ, ошибка `profile_too_large` с подсказкой уменьшить seconds.
- [Risk] Exec в чужой контейнер — поверхность для злоупотреблений → Mitigation: только allowlist, только фиксированные команды, без произвольного shell, таймауты, audit-лог каждого exec.
- [Risk] Эвристики дают ложные срабатывания → Mitigation: все рекомендации помечены `heuristic: true`, пороги настраиваются, сырые данные всегда прилагаются.
- [Trade-off] In-memory store теряет профили при рестарте → принято для v1; файловый кэш (`~/.cache/inspector-gopher/`) сохраняет сырые blobs, метаданные восстанавливаются сканированием.

## Migration Plan

Greenfield — миграции нет. Роллаут: `go build ./...` → ручной smoke (`--help`, `tools/list` через stdio) → e2e на демо Go-сервисе локально и в Docker → тег v0.1.0. Откат неприменим (новый бинарь).

## Open Questions

- Точный SDK (официальный vs mark3labs) — решится spike-таском при проверке handshake с Copilot; каталог инструментов от этого не меняется.
- Формат folded vs speedscope в v1.1 — specs фиксируют folded+tree, speedscope возможен расширением `flamegraph_data.format`.
