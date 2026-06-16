# Performance rig — running and reading the benchmarks

The collector ships a small local rig for measuring performance-sensitive
paths and capturing extractable profiles. It lives in three places:

| Location | What it measures |
| --- | --- |
| `internal/fanoutconsumer/*_bench_test.go` | Per-signal fanout dispatch (`NewMetrics`/`NewTraces`/`NewLogs`) across a grid of consumer counts × mutator mixes × batch shapes. |
| `pdata/{pmetric,ptrace,plog}/clone_bench_test.go` | The raw cost of `CopyTo` for the same shape grid. The "intrinsic clone cost" reference. |
| `service/internal/graph/pipeline_bench_test.go` | End-to-end pipeline: `nopreceiver` → optional `batchprocessor` → fanout → N × `nopexporter`. Validates that microbench wins land at the pipeline level too. |
| `cmd/perftestbed/` | Standalone binary for steady-state load + live `pprof` attach. |

Use this rig to:

- **Discover** allocation/CPU hotspots in fanout, pdata, and the
  exporterhelper queue+batch path.
- **Validate** that a change actually moves the right metric (a regression
  guard for the COW pdata, CoW-aware batcher, and capability-hygiene work).

This page covers the day-to-day workflows. For background on which
optimizations the rig is meant to validate, see the audit document
referenced from `perf/baselines/*/README.md`.

## Quick start

Tight feedback loop while iterating on a change:

```sh
make perf-bench-quick   # ~10 s; one count, short benchtime
```

Stable measurement for a commit-quality result:

```sh
make perf-bench         # multiple counts, longer benchtime; tees to perf/last-run/
```

Capture a baseline alongside a steady-state profile:

```sh
make perf-baseline      # populates perf/baselines/YYYY-MM-DD-<shortsha>/
```

Compare current state against a stored baseline:

```sh
make perf-compare BASELINE=perf/baselines/2026-06-17-2e5c71d/
```

## Running a specific benchmark by hand

The `go test -bench` invocation under the hood is straightforward — useful
when you want to vary `-benchtime`, `-count`, or include a specific subtest:

```sh
go test -bench='BenchmarkMetricsFanout/n=4/mix=one_mut_rest_ro' \
        -benchmem -benchtime=2s -count=5 \
        ./internal/fanoutconsumer/...
```

```sh
go test -bench='BenchmarkCopyToMetrics/shape=rich_100x5x50x10' \
        -benchmem -benchtime=2s -count=5 \
        ./pdata/pmetric/...
```

Each subtest reports `ns/op`, `B/op`, `allocs/op`, and a `MB/s` throughput
(driven by `b.SetBytes(int64(<datapoints|spans|records>))`). The
`allocs/op` column is usually the most actionable for fanout/clone work.

## Reading the numbers

Three columns are worth keeping straight:

- **`ns/op`** — wall-clock per iteration. CPU-sensitive optimizations
  (loop tightening, branch elimination) move this.
- **`B/op`** — bytes allocated per iteration. Heap pressure tracks this.
  Compression or layout changes (struct packing, slice presizing) move
  it independently of allocation count.
- **`allocs/op`** — number of distinct allocations per iteration. **This
  is the headline number for fanout / clone work**: every clone of a
  `pmetric.Metrics` allocates one wrapper struct per nested entity, and
  `(N − 1) × clones` allocations scale linearly with N for an
  all-mutating fanout.

Comparison rule of thumb: a clone-eliminating optimization should drop
`allocs/op` by an integer multiple equal to the number of clones the
fanout previously made. If `allocs/op` doesn't move, the clone is
elsewhere.

## Extracting profiles

Add `-cpuprofile` / `-memprofile` to any `go test -bench` invocation:

```sh
go test -bench='BenchmarkPipelineFanout/N=4/batch=true' \
        -benchmem -benchtime=10s -count=1 \
        -cpuprofile=/tmp/cpu.pprof \
        -memprofile=/tmp/mem.pprof \
        ./service/internal/graph/...
```

Then explore in the browser:

```sh
go tool pprof -http=:8080 /tmp/cpu.pprof
go tool pprof -alloc_objects -http=:8081 /tmp/mem.pprof
```

Focus on the fanout clone path specifically:

```sh
go tool pprof -focus=cloneMetrics -web /tmp/cpu.pprof
```

`-alloc_objects` is the right view for fanout work — it counts wrapper
allocations directly, whereas the default `-inuse_space` view reports
only what survived to a GC cycle.

## Steady-state profiling via `cmd/perftestbed`

`go test -bench` restarts the timer between iterations, which distorts
sustained GC and queue dynamics. For long-running steady-state shapes
(e.g. profiling a pipeline that has run for 60+ seconds with a constant
QPS), use the standalone binary:

```sh
go build -o /tmp/perftestbed ./cmd/perftestbed/
/tmp/perftestbed --signal=metrics --fanout=4 --batch=true --duration=120s &
# Live attach from a second shell:
go tool pprof -seconds=60 http://localhost:6060/debug/pprof/profile
go tool pprof -alloc_objects http://localhost:6060/debug/pprof/allocs
```

The binary registers `net/http/pprof` handlers on `localhost:6060` and
exits cleanly after `--duration`. See `cmd/perftestbed/README.md` for
the full flag set.

This is intentionally only a developer tool — production deployments
should keep using the dedicated `pprofextension` from the contrib repo.

## Comparing two runs with `benchstat`

`benchstat` reports geometric-mean deltas with significance tests:

```sh
go install golang.org/x/perf/cmd/benchstat@latest

# Capture a "before" reading on the baseline commit:
git checkout <base-sha>
make perf-bench  # writes perf/last-run/bench.txt
cp perf/last-run/bench.txt /tmp/before.txt

# Switch to the optimization branch and re-run:
git checkout <my-branch>
make perf-bench
cp perf/last-run/bench.txt /tmp/after.txt

benchstat /tmp/before.txt /tmp/after.txt
```

For sensitive work prefer `-count=10 -benchtime=5s` (which `make
perf-baseline` does by default). Below that, run-to-run noise can mask
single-digit-percent wins. If `benchstat` reports `~` (no significant
change), trust it — don't ship "wins" inside the noise floor.

## Baseline storage

Captured baselines live under `perf/baselines/YYYY-MM-DD-<shortsha>/`:

```
perf/baselines/2026-06-17-2e5c71d/
├── README.md      ← notes: hardware, intent, where pprofs were uploaded
├── metadata.yaml  ← git sha, Go version, host, flags
└── bench.txt      ← raw benchstat-readable output
```

`*.pprof` artifacts are excluded from git (they're large). Capture them
locally and upload to your team's preferred shared storage; record the
URL in the baseline's `README.md`.

See `perf/README.md` for the full schema.

## Honest caveats

- **Synthetic benchmarks have weaker statistical power than production
  load.** They validate that an optimization works in the model — they
  don't prove it works in production with real cardinality and real
  fanout topologies.
- **Microbenchmarks are sensitive to laptop thermals.** A baseline
  captured on a hot laptop can shift 10 %+ vs the same hardware cold.
  Prefer `-count=10` and a consistent host.
- **`b.Loop()` and `b.ReportAllocs()` are not free.** They add a few
  nanoseconds per iteration of overhead. For ns-scale work the
  measurement noise can dominate — interpret single-digit-ns differences
  with care.
- **The "rich" shape is a stress test, not a workload.** Real-world
  fanout cost depends on your pdata shape. The shape grid spans the
  realistic range; read the row that matches your topology, not the
  worst-case row.
