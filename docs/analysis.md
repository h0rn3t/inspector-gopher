# Analysis

`analyze_profile` parses a stored pprof blob and returns:

- `top_flat` / `top_cum` — top-N functions with `flat`, `flat_pct`, `cum`, `cum_pct`.
- `total_samples`, `sample_type`, `units`.
- `findings[]` — heuristic observations, each with `heuristic: true`.

## Heuristics

| Finding | Trigger | Recommendation |
|---|---|---|
| `gc_pressure` | heap top allocator is `bytes.Buffer`/`append`/`makeslice` | `sync.Pool`, pre-sized buffers |
| `goroutine_leak_suspected` | goroutine count grows ≥10 between dumps with one dominant `chan receive`/`mutex` waiter | inspect blocked holder |

Thresholds are conservative; raw data always ships alongside so you can disagree.

## Diff

`diff_profiles(base, current)` subtracts flat counts normalized to each total:
`regressions[]` sorted by growth first — the top row is your prime suspect.

## Flamegraph

- `flamegraph_data(format=folded)` — `stack;frames count` lines for CPU.
- `flamegraph_data(format=tree)` — `{name, value, children}` JSON tree;
  `truncated: true` when depth >200 or nodes >5000.
- `flamegraph_report(profile_id=…)` — interactive dashboard: MCP Apps UI
  (`ui://inspector-gopher/flamegraph/…`), browser preview
  (`GET /dashboard/flamegraph/…`, needs `--http`), saved HTML artifact.
  Details: `docs/dashboards.md`.

## Verify

```bash
go test ./internal/analyzer/ -run 'TestAnalyze|TestDiff|TestFolded' -v
```
