# Spec Delta: profile-analysis

## Purpose

Возможность анализа профилей превращает сырые pprof/trace-данные в готовый отчёт об узких местах: топ функций, дифф профилей, детект GC-прессинга и утечек горутин, а также flamegraph-совместимые данные для визуализации.

## ADDED Requirements

### Requirement: Анализ одного профиля (топ узких мест)

Система SHALL принимать собранный профиль (по ID из `profile://recent` или inline base64) и возвращать топ-N функций по flat и cumulative (дефолт N=15), проценты от общего, sample-тип и единицы, плюс краткие рекомендации (например, «80% CPU в regexp — рассмотреть предкомпиляцию» как эвристика, помеченная `heuristic: true`).

#### Scenario: Топ CPU-функций

- **WHEN** вызвано `analyze_profile(profile_id="cpu-abc", top_n=10)`
- **THEN** ответ содержит `top_flat[]` и `top_cum[]` с `{function, file:line, flat, flat_pct, cum, cum_pct}`, `total_samples` и `recommendations[]`

#### Scenario: Неизвестный профиль

- **WHEN** вызвано `analyze_profile(profile_id="nope")`
- **THEN** сервер возвращает ошибку `profile_not_found` со списком доступных ID из `profile://recent`

### Requirement: Дифф двух профилей (регрессии)

Система SHALL сравнивать базовый и текущий профили одного типа и возвращать функции с наибольшим ростом/падением (delta flat/cum, абсолют и %), чтобы находить регрессии между релизами или до/после фикса.

#### Scenario: Поиск регрессии

- **WHEN** вызвано `diff_profiles(base="cpu-before", current="cpu-after")`
- **THEN** ответ содержит `regressions[]` (рост flat_pct, отсортировано по delta), `improvements[]` и `summary` (изменение total)

### Requirement: Детект GC-прессинга и утечек горутин

Система SHALL по heap-профилю + `runtime_stats` детектировать GC-прессинг (высокий `GCCPUFraction`, рост `NumGC`, большой `HeapAlloc` при малом `HeapInuse` — churn) и по двум дампам горутин детектировать утечки (рост числа горутин + одинаковые стеки ожидания на mutex/channel).

#### Scenario: GC-прессинг

- **WHEN** `runtime_stats` показывает `GCCPUFraction > 0.1` и heap-топ занят `bytes.Buffer`/`append`
- **THEN** `analyze_profile` включает finding `gc_pressure: high` с рекомендацией (пулы буферов, `sync.Pool`, уменьшение аллокаций в горячем пути)

#### Scenario: Утечка горутин

- **WHEN** вызвано сравнение двух дампов горутин с ростом count и доминирующим стеком `chan receive` в одном месте кода
- **THEN** ответ содержит finding `goroutine_leak_suspected` с местом ожидания и ростом count между дампами

### Requirement: Flamegraph-данные

Система SHALL отдавать `flamegraph_data` в двух формах: folded stacks (строки `stack;frames count`) для CPU-профилей и JSON-дерево `{name, value, children}` для heap/CPU, пригодные для рендера клиентом без дополнительной обработки.

#### Scenario: Folded stacks для CPU

- **WHEN** вызвано `flamegraph_data(profile_id="cpu-abc", format="folded")`
- **THEN** ответ содержит `folded` текст, где каждая строка — стек через `;` и счётчик, плюс `total` и `sample_type`

#### Scenario: JSON-дерево для heap

- **WHEN** вызвано `flamegraph_data(profile_id="heap-xyz", format="tree")`
- **THEN** ответ содержит дерево `root{name,value,children}` с агрегацией по стеку вызовов и `truncated: bool` при обрезке глубоких стеков
