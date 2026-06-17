# Baseline 2026-06-17-4ff09b609

Post-W3-prerequisites baseline. Supersedes `2026-06-17-cee113d2b` as the
reference point for the Workstream-3 optimization phases (O0 capability
hygiene → O1 fanout fast-paths → O2 CoW-aware exporterhelper batcher →
O3 selective COW pdata).

## What changed from the previous baseline

Three new bench surfaces (W3 prerequisites P1+P2+P3):

- **`internal/fanoutconsumer/*_bench_test.go`**: new mix variant
  `one_real_mut_rest_ro` that uses a real mutator (`mutatingReal*` —
  appends one Resource per `ConsumeXxx` call) instead of the existing
  `mutatingNop*` no-op shim. The COW detach path (audit finding #18)
  only fires when something actually mutates; this variant is what
  the O3 validation will compare against.
- **`service/internal/graph/pipeline_batched_bench_test.go`** (new):
  `BenchmarkPipelineFanoutBatched{Metrics,Traces,Logs}` exercising a
  single-pipeline fanout with one-or-more `MutatesData: true`-declaring
  exporters (simulating the exporterhelper queue+batch's `MutatesData`
  injection, audit finding #19). Iterates over five mixes:
  `single_batched`, `batched_plus_ro`, `batched_plus_3ro`, `two_batched`,
  `two_batched_plus_ro`. The `two_batched` mix is where the audit's
  known 1→N corner-case regression for O2 surfaces.
- **`service/internal/graph/pipeline_bench_test.go`** (extended):
  `BenchmarkReceiverFanoutMultiPipeline{Metrics,Traces,Logs}` exercising
  the topology where one receiver feeds two pipelines (one batched,
  one readonly). The receiver-side fanout's clone for the mutator
  pipeline is the cost O2 directly targets.

The existing bench surfaces (`BenchmarkMetricsFanout`,
`BenchmarkCopyToMetrics`, `BenchmarkPipelineFanoutMetrics`, …) are
unchanged; their numbers carry across baselines (modulo a few percent
run-to-run noise).

## Host

See [`metadata.yaml`](metadata.yaml). Apple M1 Max, Go 1.26.3, darwin/arm64.

## Capture command

```sh
make perf-baseline \
  PERF_BASELINE_FLAGS="-benchmem -benchtime=500ms -count=2 -timeout=30m"
```

Lighter-than-default flags (count=2, benchtime=500ms) to keep the run
under ~6 min on M1 Max. For shipping a Workstream-3 PR rerun with the
default flags for tighter confidence intervals.

## Headline numbers

Lifted from [`bench.txt`](bench.txt). All measurements on a single host —
not for cross-host comparison.

### Pipeline fanout, batched-exporter topologies (single-pipeline)

`BenchmarkPipelineFanoutBatchedMetrics/shape=rich_100x5x50x10`:

| Mix                    | allocs/op  | Notes                                              |
| ---------------------- | ---------- | -------------------------------------------------- |
| `single_batched`       |        ~1  | No fanout wrap; single mutator gets md directly.   |
| `batched_plus_ro`      |  328 503   | DDOT default traces topology; 1 deep clone.        |
| `batched_plus_3ro`     |  328 503   | Same shape, larger N; still 1 deep clone.          |
| `two_batched`          |  328 503   | Last-mutator-passthrough optimization; 1 clone.    |
| `two_batched_plus_ro`  |  657 009   | 2 mutators + readonly forces 2 clones.             |

### Receiver-side fanout, multi-pipeline topology

`BenchmarkReceiverFanoutMultiPipelineMetrics`:

| Shape               | allocs/op  | Notes                                          |
| ------------------- | ---------- | ---------------------------------------------- |
| `small_10`          |     147    | 1 clone of small shape.                        |
| `medium_1k`         |  14 285    | 1 clone of medium.                             |
| `rich_100x5x50x10`  | 328 502    | 1 clone of rich shape — the audit's #19 cost.  |

After O2 lands, the `rich_100x5x50x10` row should drop to single-digit
allocs/op (the receiver-side fanout's clone disappears; the batcher's
internal clone moves out of the bench's measurement window because
`exampleexporter` doesn't actually batch — only its capability
declaration is simulated).

### Fanout under real-mutation conditions

`BenchmarkMetricsFanout/n=4/mix=one_real_mut_rest_ro/shape=rich_100x5x50x10`:

|                                       | allocs/op  | Notes                                  |
| ------------------------------------- | ---------- | -------------------------------------- |
| `mutatingNop*` (existing)             |   328 503  | No real mutation; capability-flag only |
| `mutatingReal*` (new, P1)             |   328 505  | +2 allocs for the AppendEmpty payload  |

After O3, the `one_real_mut_rest_ro` row should drop because the COW
detach only fires once (for the real mutator) rather than for every
clone the fanout makes today.

## Files

- [`metadata.yaml`](metadata.yaml) — host, Go version, capture flags
- [`bench.txt`](bench.txt) — full `go test -bench` output, benchstat-readable

`cpu.pprof` / `mem.pprof` not captured for this baseline — the
benchmark-driven workflow (`go test -cpuprofile=...`) is sufficient to
extract focused profiles per-bench when needed. For a steady-state
profile, use `cmd/perftestbed` with the same parameters and attach
`go tool pprof http://localhost:6060/debug/pprof/...`.

## Comparing against this baseline

```sh
make perf-compare BASELINE=perf/baselines/2026-06-17-4ff09b609/
```
