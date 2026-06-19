# Baseline: Path Y Phase 6 validation

**Date**: 2026-06-19
**Branch tip**: `3e1529bf4` (path-y/codegen-migration, Phase 6 bench prep)
**Comparison base**: `cb645a3a4` (o2/cow-aware-batcher, pre-Path-Y)

Captured on Apple M1 Max, darwin/arm64, count=5, benchtime=2s.

## Files

- `conditional-explicit-gate-{off,on}.txt` — BenchmarkConditionalFanoutMetrics
  with explicit `cow.DetachMetrics` in the mutator (Path X-hybrid behavior).
  Gate-OFF is today's eager-clone baseline; Gate-ON exercises cow.Share at
  fanout dispatch + explicit detach in the mutator's hit path.
- `conditional-autodetach-gate-{off,on}.txt` — same bench, mutator skips the
  explicit `cow.DetachMetrics` call. Phase 5's codegen-injected
  `state.DetachIfShared()` prelude inside `RemoveIf` is responsible for the
  detach. Validates the auto-detach mechanism.
- `deep-access-{o2,pathy}.txt` — BenchmarkDeepAccessMetrics on each branch.
  Read-only traversal `RM[*].SM[*].M[*].DP[*].Attributes()` on the rich
  100×5×50×10 shape (~250k attribute reads). Measures the Phase 4
  Handle-indirection's index-walk overhead vs pre-Path-Y direct-pointer
  access. Cache-decision criterion (`perf/rfc/pdata-cow-handoff.md`).

## Headline results

### Deferred-clone benefit at pipeline level (BenchmarkConditionalFanoutMetrics)

Gate-OFF → Gate-ON, explicit Detach variant (geomean across hit rates):

| metric | delta |
|---|---|
| sec/op | **-12.39%** |
| B/op | **-18.52%** |
| allocs/op | **-17.41%** |

By hit rate (allocs/op):

| hit rate | gate-OFF | gate-ON | savings |
|---|---|---|---|
| 0% | 1.258M | 929.2k | **-26.12%** |
| 10% | 1.258M | 956.6k | -23.94% |
| 20% | 1.258M | 993.1k | -21.04% |
| 50% | 1.258M | 1.089M | -13.41% |
| 100% | 1.258M | 1.258M | ~ |

The curve is exactly what the audit predicted: linear scaling between the
no-mutation case (full deferred-clone savings) and the always-mutate case
(matches eager-clone ceiling). At hit=0% (the audit's "false-positive
mutator" pattern — ~79% of MutatesData=true components in contrib), we save
~328k allocs / batch — the cost of the eager CopyTo that the fanout
previously did unconditionally.

### Auto-detach matches explicit Detach (Phase 5 validation)

BenchmarkConditionalFanoutMetricsAutoDetach (no explicit `cow.DetachMetrics`,
relies on the codegen-injected prelude in `RemoveIf`):

| hit rate | explicit allocs/op | auto allocs/op | delta |
|---|---|---|---|
| 0% | 929.2k | 929.2k | exact match |
| 10% | 956.6k | 956.6k | exact match |
| 20% | 993.1k | 993.1k | exact match |
| 50% | 1.089M | 1.089M | exact match |
| 100% | 1.258M | 1.258M | exact match |

Phase 5's auto-detach delivers byte-identical pipeline-level performance to
explicit `cow.DetachMetrics`. Processor authors don't need to opt into the
optimisation — the codegen does it transparently.

### Deep-access overhead (cache decision)

BenchmarkDeepAccessMetrics on `100×5×50×10` (~250k attribute reads,
read-only):

| branch | sec/op |
|---|---|
| `o2/cow-aware-batcher` (pre-Path-Y direct-pointer access) | 57.47µs |
| `path-y/codegen-migration` (Handle-indirection index walk) | 57.56µs |

**Delta: ~ (statistically indistinguishable, p=1.000)**. The Phase 4
Handle-indirection has **zero measurable overhead** vs the pre-Path-Y
direct-pointer access — the Go compiler inlines `h.GetOrig()` /
`h.GetState()` accessors at cost 3 (verified with `-gcflags='-m=2'`
earlier).

**Cache decision** (per `perf/rfc/pdata-cow-handoff.md` threshold):
- <5%   slowdown → ship without cache ← **THIS**
- 5-10% → cache if <100 LOC
- >10%  → cache required

The cowproto prototype's generation-cache optimisation is **not needed**
under Path Y's index-walk layout. The combination of Go's
single-method-receiver inlining and pdata's "always pointer" slice
layout means the path walk costs the same as a direct dereference.

## What this validates

1. **Audit-#19 + Phase 4 + Phase 5 deliver the deferred-clone benefit
   the audit predicted, at pipeline level.** Conditional mutators
   (~79% of MutatesData=true components per the contrib audit) save the
   full eager-clone cost on no-match iterations.

2. **Phase 5 auto-detach is correct AND zero-overhead.** Auto-detach
   matches explicit `cow.DetachMetrics` to the byte.

3. **Path Y Handle-indirection has no observable read-side cost.** The
   shelved cowproto's index-walk-with-cache hypothesis was solving a
   problem that doesn't exist at this layout granularity.

## Open follow-ups (not done in this baseline run)

- Re-run with `BenchmarkRealHelperMultiPipeline` from
  `service/internal/graph/pipeline_real_helper_bench_test.go` to confirm
  the savings hold under a full receiver→fanout→exporter pipeline shape,
  not just the fanoutconsumer-level dispatch. Expected: same curve.
- pcommon-mutator scenarios (mutations via
  `Resource.Attributes().PutStr(...)`) are still Path-X-hybrid (explicit
  `cow.DetachX` required). The conditional bench here uses slice
  `RemoveIf` which is on the auto-detach path. Future engagement: extend
  to pcommon if desired.
- Multi-signal verification (traces / logs / profiles). Phase 5 is
  signal-symmetric; verifying via signal-specific benches would close
  any residual doubt.
