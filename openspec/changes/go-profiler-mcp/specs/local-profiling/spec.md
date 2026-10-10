# Spec Delta: local-profiling

## Purpose

Возможность локального профилирования собирает CPU/heap/mutex/block/goroutine профили и runtime-трейсы локальных Go-процессов через их pprof HTTP endpoint и runtime-метрики для последующего анализа узких мест.

## ADDED Requirements

### Requirement: Сбор CPU-профиля локальной цели

Система SHALL собирать CPU-профиль локальной цели через `GET {base}/debug/pprof/profile?seconds=N` (дефолт 30с, диапазон 5–120с) и возвращать сырой профиль (base64 protobuf) плюс метаданные (цель, длительность, размер, timestamp).

#### Scenario: Успешный сбор CPU

- **WHEN** вызвано `profile_cpu(target="api-local", seconds=15)`
- **THEN** сервер возвращает профиль длительностью ~15с, `format: "pprof-protobuf"`, base64-поле и `duration_s: 15`

#### Scenario: Недопустимая длительность

- **WHEN** вызвано `profile_cpu` с `seconds=600`
- **THEN** сервер возвращает ошибку валидации с допустимым диапазоном и не выполняет запрос

### Requirement: Сбор heap/mutex/block/goroutine профилей

Система SHALL собирать heap (`/debug/pprof/heap`), mutex (`/debug/pprof/mutex`), block (`/debug/pprof/block`) и goroutine (`/debug/pprof/goroutine?debug=1`) профили одним запросом каждый, с опцией `gc_before_heap` (вызов `debug.FreeOSMemory` невозможен удалённо — для heap фиксировать, был ли вызван GC целью, через параметр `?gc=` если поддерживается).

#### Scenario: Сбор heap-профиля

- **WHEN** вызвано `profile_heap(target="api-local")`
- **THEN** сервер возвращает heap-профиль в формате pprof-protobuf с метаданными `sample_type: "alloc_space/inuse_space"`

#### Scenario: Дамп горутин

- **WHEN** вызвано `profile_goroutine(target="api-local")`
- **THEN** сервер возвращает и бинарный профиль, и текстовый дамп стеков (первые N стеков + общее число горутин)

### Requirement: Runtime-трейс и статистика

Система SHALL собирать `runtime/trace` (`/debug/pprof/trace?seconds=N`, ≤30с) и `runtime_stats` (число горутин, `runtime.MemStats`: HeapAlloc/HeapSys/HeapIdle/HeapInuse/GCCPUFraction/NumGC, `GOMAXPROCS`, uptime цели) одним вызовом без остановки процесса.

#### Scenario: Сбор трейса

- **WHEN** вызвано `capture_trace(target="api-local", seconds=5)`
- **THEN** сервер возвращает trace-файл base64 с `format: "go-trace"` и предупреждением о размере при превышении лимита

#### Scenario: Статистика runtime

- **WHEN** вызвано `runtime_stats(target="api-local")`
- **THEN** ответ содержит `num_goroutine`, `mem_stats` (HeapAlloc, NumGC, GCCPUFraction, PauseNs), `gomaxprocs` и `collected_at`
