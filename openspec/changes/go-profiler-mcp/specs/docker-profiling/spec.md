# Spec Delta: docker-profiling

## Purpose

Возможность Docker-профилирования находит Go-процессы в контейнерах и собирает их профили либо через проброшенный pprof HTTP, либо через docker exec / Docker API без остановки контейнера.

## ADDED Requirements

### Requirement: Обнаружение целей в Docker

Система SHALL предоставлять `docker_list_targets` — список running-контейнеров с пометкой, есть ли у них доступный pprof endpoint (проба `GET /debug/pprof/` через проброшенный порт или через `docker exec wget/curl`), именем, образом, портами и PID namespace.

#### Scenario: Листинг контейнеров

- **WHEN** вызвано `docker_list_targets()` при запущенном Docker
- **THEN** ответ содержит массив контейнеров `{id, name, image, ports, pprof_reachable: bool, pprof_url?: string}`

#### Scenario: Docker недоступен

- **WHEN** Docker socket недоступен
- **THEN** сервер возвращает ошибку `docker_unavailable` с `retryable: false` и подсказкой про `DOCKER_HOST` и права на socket

### Requirement: Профилирование через проброшенный pprof

Если у контейнера проброшен pprof порт, система SHALL собирать профили тем же кодом, что и локальные цели (по `pprof_url` из `docker_list_targets`), без использования exec.

#### Scenario: Сбор через проброшенный порт

- **WHEN** вызвано `docker_profile(container="api", profile="cpu", seconds=10)` и pprof доступен по проброшенному порту
- **THEN** сервер собирает профиль по HTTP без exec и помечает `collection: "http-via-published-port"`

### Requirement: Профилирование через docker exec

Если pprof слушает только на localhost внутри контейнера, система SHALL выполнять сбор через `docker exec` (curl/wget внутри контейнера → stdout → сервер) для cpu/heap/goroutine/trace; при отсутствии curl/wget SHALL возвращать понятную ошибку с инструкцией.

#### Scenario: Сбор через exec

- **WHEN** вызвано `docker_profile(container="api", profile="heap")` без проброшенного порта, но с pprof на 127.0.0.1:6060 внутри контейнера
- **THEN** сервер выполняет exec-запрос внутри контейнера, возвращает профиль и помечает `collection: "docker-exec"`

#### Scenario: Нет инструментов в контейнере

- **WHEN** в контейнере (distroless) нет ни curl, ни wget, ни sh
- **THEN** сервер возвращает ошибку `exec_tooling_missing` с рекомендацией пробросить pprof порт или добавить debug-слой

### Requirement: Безопасность Docker-доступа

Система SHALL ограничивать exec только allowlist-контейнерами (имя/ID/лейбл), запрещать произвольные команды (только фиксированный набор pprof-запросов), применять таймауты exec и не монтировать ничего в контейнер.

#### Scenario: Контейнер вне allowlist

- **WHEN** вызвано `docker_profile(container="prod-db")` вне allowlist
- **THEN** сервер возвращает `forbidden_target` без обращения к Docker API
