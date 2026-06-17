# Baseline 2026-06-17-cee113d2b

Replacement baseline for the local performance rig after the
`/code-review` feedback pass landed on `perf/local-rig`. Supersedes the
earlier `2026-06-17-ef31443a2` baseline, which was captured before the
style polish renamed pipeline-bench subtests from `N=` to `n=` (so the
old `bench.txt` is no longer benchstat-comparable against current HEAD).

## Intent

Same as the previous baseline:

- Confirm the audit's quantitative claim about pdata clone cost
  ("~250k allocations per `CopyTo` for a 100×5×50×10 metrics batch").
- Capture a snapshot of fanout dispatch, intrinsic clone, and
  end-to-end pipeline costs at HEAD so subsequent optimization PRs can
  cite a concrete delta via `make perf-compare BASELINE=...`.

## Host

See [`metadata.yaml`](metadata.yaml). Apple M1 Max, Go 1.26.3, darwin/arm64.

## Capture command

```sh
make perf-baseline \
  PERF_BASELINE_FLAGS="-benchmem -benchtime=500ms -count=2 -timeout=20m"
```

Lighter-than-default flags (`-count=2 -benchtime=500ms` instead of the
target `-count=10 -benchtime=5s`) were used because the full grid takes
hours on the rich shape × all-mutating × N=16 row. The numbers below
are stable to within a few percent; for shipping a Workstream-3 PR,
rerun with the default `make perf-baseline` flags for tighter
confidence intervals.

## Headline numbers

Lifted from [`bench.txt`](bench.txt). All measurements on a single host —
not for cross-host comparison.

### Intrinsic `CopyTo` cost (audit claim verification)

For the audit's reference shape (100 resources × 5 scopes × 50 datapoints/
spans/records × 10 attributes each):

| Signal  | allocs/op | bytes/op   | ns/op   |
| ------- | --------- | ---------- | ------- |
| Metrics | 328 503   | 17.4 MB    | ~10 ms  |
| Traces  | 301 503   | 19.9 MB    | ~10 ms  |
| Logs    | 326 503   | 18.3 MB    | ~11 ms  |

The audit estimated "~250k allocations per clone". The actual figure is
~300-330k — same order of magnitude, slightly higher than the audit's
ballpark. The motivation for the COW work stands.

### Pipeline fanout, mutator vs no-mutator (audit finding #19 pattern)

`BenchmarkPipelineFanoutMetrics/n=4/shape=rich_100x5x50x10`:

| Config         | allocs/op | bytes/op    | ns/op  |
| -------------- | --------- | ----------- | ------ |
| `mutator=false`|        ~0 |    ~350 B   | ~120 ns|
| `mutator=true` |  ~219 000 |   ~12 MB    |   ~5 ms|

A single mutating processor in the chain converts the fanout from a
nearly-free pass-through into a ~5 ms / 219 k-alloc operation. This is
the delta that the CoW-aware batcher (finding #19) and selective COW
pdata (finding #18) need to eliminate.

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
make perf-compare BASELINE=perf/baselines/2026-06-17-cee113d2b/
```
