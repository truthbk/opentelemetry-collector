# Performance baselines

This directory is the storage location for performance rig artifacts. The rig
itself lives in benchmark files across the tree (`internal/fanoutconsumer/*_bench_test.go`,
`pdata/{ptrace,pmetric,plog}/clone_bench_test.go`,
`service/internal/graph/pipeline_bench_test.go`) plus the `cmd/perftestbed`
standalone binary. See [`docs/performance.md`](../docs/performance.md) for the
end-to-end workflow.

## Layout

```
perf/
├── README.md             ← this file
├── baselines/            ← committed: text bench results + metadata
│   └── YYYY-MM-DD-<shortsha>/
│       ├── metadata.yaml
│       ├── bench.txt
│       └── README.md
└── last-run/             ← gitignored: convenience output of `make perf-bench`
```

`pprof` binaries are intentionally excluded from git (see the project's
`.gitignore`) because each is multiple megabytes. Capture them locally and
upload to the team's preferred shared storage; the baseline's `README.md`
records where they live.

## Capturing a new baseline

```sh
make perf-baseline
```

This runs the bench suite with stable settings (`-count=10 -benchtime=5s`),
optionally runs `cmd/perftestbed` for a steady-state profile capture, and
writes everything to `perf/baselines/YYYY-MM-DD-<shortsha>/`.

After running, edit the baseline's `README.md` to note:

- The intent of the baseline ("pre-O0 capture", "validation of O2", etc.).
- The host hardware (CPU model, RAM, OS).
- Where any external `pprof` artifacts were uploaded.

## Comparing a change against a baseline

```sh
make perf-compare BASELINE=perf/baselines/YYYY-MM-DD-<shortsha>/
```

Runs the same bench suite at HEAD and emits a `benchstat` comparison.

## Baseline metadata schema

Each baseline's `metadata.yaml` records the captured state:

```yaml
captured_at: 2026-06-17T14:32:08Z
git_sha: ef31443a2922d05fa26f582ff62dbcea06c246dc
git_branch: perf/local-rig
go_version: go1.26.3
goos: darwin
goarch: arm64
host: jaime-laptop
flags: "-benchmem -count=10 -benchtime=5s"
notes: |
  Free-form text describing the run context.
```

Keep `metadata.yaml` lean — anything that doesn't fit cleanly belongs in the
baseline's `README.md`.
