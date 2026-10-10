# Spec Delta: mcp-server

## Purpose

MCP-сервер inspector-gopher предоставляет AI-агентам и разработчикам единый каталог инструментов профилирования Go-программ через транспорты stdio и HTTP/SSE с безопасной конфигурацией целей.

## ADDED Requirements

### Requirement: MCP-транспорты stdio и HTTP/SSE

Сервер SHALL поддерживать оба транспорта: stdio для локальных AI-клиентов (Copilot, Claude Code) и HTTP/SSE (Streamable HTTP) для сетевых клиентов, с выбором через флаги/конфиг и возможностью запускать оба одновременно на разных портах.

#### Scenario: Запуск через stdio

- **WHEN** сервер запущен в режиме stdio и клиент отправляет `initialize` + `tools/list`
- **THEN** сервер возвращает каталог инструментов профилирования и отвечает на `tools/call` по JSON-RPC через stdin/stdout

#### Scenario: Запуск через HTTP/SSE

- **WHEN** сервер запущен с `--http :8080` и клиент открывает SSE-сессию
- **THEN** клиент получает тот же каталог инструментов и может вызывать их по HTTP с тем же контрактом ответов и ошибок

### Requirement: Каталог инструментов и ресурсов

Сервер SHALL экспонировать минимальный стабильный каталог: инструменты сбора (`profile_cpu`, `profile_heap`, `profile_mutex`, `profile_block`, `profile_goroutine`, `capture_trace`, `runtime_stats`), инструменты Docker (`docker_list_targets`, `docker_profile`), инструменты анализа (`analyze_profile`, `diff_profiles`, `flamegraph_data`) и ресурсы (`config://targets`, `profile://recent`).

#### Scenario: Листинг инструментов

- **WHEN** клиент вызывает `tools/list`
- **THEN** каждый инструмент содержит имя, человекочитаемое описание, JSON Schema входных параметров и аннотации (read-only vs side-effect: сбор профиля помечен как side-effect с указанием длительности)

### Requirement: Конфигурация целей и безопасность

Сервер SHALL читать конфигурацию целей из файла YAML/JSON и переменных окружения, поддерживать allowlist хостов/контейнеров, таймауты и лимиты размера профиля; по умолчанию запрещать доступ к произвольным хостам вне allowlist и требовать явного разрешения для Docker socket.

#### Scenario: Запрет вне allowlist

- **WHEN** вызван сбор профиля для хоста, отсутствующего в allowlist
- **THEN** сервер возвращает ошибку `forbidden_target` без выполнения сетевого запроса

#### Scenario: Конфигурация из файла

- **WHEN** сервер стартует с `--config targets.yaml`, где описаны local и docker цели
- **THEN** ресурс `config://targets` показывает эти цели, а инструменты принимают `target` только из этого списка или из allowlist-паттерна

### Requirement: Контракт ошибок и наблюдаемость

Сервер SHALL возвращать типизированные ошибки (`unknown_target`, `connection_failed`, `timeout`, `pprof_unavailable`, `docker_unavailable`, `profile_too_large`, `forbidden_target`) с retryable-флагом, писать структурированные логи (slog, JSON) и экспонировать `server_health` с версией, аптаймом и счётчиками вызовов.

#### Scenario: Недоступный pprof

- **WHEN** цель не отвечает на pprof endpoint в течение таймаута
- **THEN** сервер возвращает `pprof_unavailable` с `retryable: true`, текстом причины и подсказкой (проверить импорт `net/http/pprof` / флаг агента)
