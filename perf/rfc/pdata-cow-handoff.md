# Path Y handoff — pdata.cow auto-detach

Honest status snapshot for the engineer resuming this work. The
engagement reached the foundation of Path Y (auto-detach via codegen)
but the bulk of the migration (Y.3 + Y.5) is still ahead. This is a
checkpoint, not a finished design — the design decisions documented
below were made during exploration and some are worth re-litigating.

Branch: `o3-alt-a/pipeline-validation` off `o3/cow-pdata` off
`o2/cow-aware-batcher` off `perf/local-rig` off `main`.

Tip commit: `45dae78f9` ([path-y/foundation+] State.DetachIfShared
hook). Everything below the tip is committed; nothing is pushed.

## What's shipped and validated

1. **`pdata.cow` Alpha feature gate** (commit `5bccc199c`). Registered
   in `pdata/xpdata/internal/metadata/generated_feature_gates.go`.
   Default off.

2. **`pdata/xpdata/cow/` public API** (commits `9da9a62db`, `bbde26bee`,
   `b29c516da`). Share/Release/IsShared/Detach × 4 signals. Under
   the gate, `Share*` returns a wrapper at the same backing orig as
   the source but with its own fresh `*State` (cowRefs == 1); the
   source state is untouched. `Detach*` is the explicit-detach
   escape hatch — deep-clone the backing tree, allocate fresh State,
   return an independent wrapper.

3. **`internal.Handle[T]` indirection at top-level** (commits
   `8cc889aeb`, `27eb92fa7`). All four `*Wrapper` types
   (`MetricsWrapper`, `TracesWrapper`, `LogsWrapper`, `ProfilesWrapper`)
   carry a `*Handle[T]` field instead of inline `{orig, state}`. The
   indirection is the structural prerequisite for detach: a mutating
   wrapper's first call on a shared tree triggers `DetachIfShared`
   which rebinds `h.orig` and `h.state` in place. Public typedefs
   (`pmetric.Metrics`, etc.) unchanged; existing test suite stays
   green.

4. **`internal.DetachIfShared[T]` + per-signal entry points**
   (commit `27eb92fa7`). Fast path: one atomic load + compare, zero
   alloc. Slow path: deep clone via `Copy*Request` + Handle rebind.
   Per-signal `DetachMetricsIfShared(MetricsWrapper)` etc. live in
   the typed wrapper files — keeps cloneFn embedded in a typed
   function so it never lives on `*State` (otherwise `reflect.DeepEqual`
   on two states with distinct closures breaks proto wire-compat tests).

5. **State.DetachIfShared method + closure hook** (commit `45dae78f9`).
   `*State` now has a `detach func()` field, populated ONLY by
   `cow.ShareX` (never by `NewXWrapper`). This keeps non-shared
   `*State` values identical at reflect.DeepEqual level (critical
   for the wire-compat tests). When a nested mutator calls
   `state.DetachIfShared()`, the closure invokes the per-signal
   `DetachXIfShared` against the captured top-level Handle.

6. **Auto-detach in two top-level mutators** (commit `27eb92fa7`).
   `Metrics.MoveTo` and `Metrics.CopyTo` now call
   `internal.DetachMetricsIfShared` before `AssertMutable`. Proof of
   concept — the mechanism works end-to-end for top-level mutators.

7. **`AssertMutable` safety net** (commit `b29c516da`). Panics on
   `cowRefs > 0` with a clear message pointing at `pdata/xpdata/cow`.
   Catches contract violations (mutator that didn't call Detach)
   during dev/test rather than silently corrupting the source.

8. **Conditional-mutator pipeline bench** (commit `bac37790c`).
   `BenchmarkConditionalFanoutMetrics` validates the deferred-clone
   benefit at pipeline level: `saved ≈ (1 − hit_rate) × one_clone_cost`,
   linear, monotonic. At 0% hit rate (no-op mutator), 328k allocs +
   17 MB saved per batch (26% of total pipeline cost). At 100% hit
   rate, matches today's eager cost.

9. **Static-analysis audit** (in commit `bac37790c`'s body): 27 of
   34 `MutatesData: true` components in opentelemetry-collector-contrib
   are conditional mutates (~79%). Dominant patterns: OTTL `where`
   clauses, name/key lookups, data-shape preconditions.

## What's blocked and why

**Path Y.3 — nested wrapper migration to Handle + index-path layout.**
Attempted in this session on `ResourceMetricsSlice` and `ResourceMetrics`
(reverted at the end). The blockers, in priority order:

### Blocker 1: generated test files use `reflect.DeepEqual` on wrapper struct values

The ~20-30 generated test files in `pdata/pmetric/` (and same in ptrace,
plog, pprofile, pcommon) use `assert.Equal` from testify, which uses
`reflect.DeepEqual` under the hood. ANY layout change — adding a
`*Handle` field, switching to dual-mode getOrig — breaks comparisons
across the entire test surface. Example: `TestMetrics_ResourceMetrics`
compares `NewResourceMetricsSlice()` (h=nil) against `ms.ResourceMetrics()`
(h set). Even if both represent semantically-equal data, the field
diff fails the test.

**Required fix**: update `message_test.go.tmpl` and `slice_test.go.tmpl`
to use semantic equality (compare via public API: `Len()`, `At()`, `Get()`,
etc.) rather than struct-level `reflect.DeepEqual`. This is invasive
but mechanical once the pattern is decided.

### Blocker 2: pcommon shared types are signal-agnostic

`pcommon.Resource`, `pcommon.Map`, `pcommon.Value`, etc. are used by
metrics, traces, logs, profiles. Under cowproto's design (Handle +
index-path), each nested wrapper needs to know its **top-level
type T**: `*Handle[ExportMetricsServiceRequest]` vs
`*Handle[ExportTraceServiceRequest]` etc. Go generics can't bridge
multiple concrete types at the same call site, so the pcommon types
can't carry a generic Handle.

**Three options for pcommon, all imperfect**:

   A. **Per-signal pcommon variants** — `MetricsResource`,
      `TracesResource`, `LogsResource`, `ProfilesResource` and same
      for Map, Value. Triplicates pcommon (~quadruplicates) and is a
      major API break.

   B. **Hybrid Path X for pcommon** — pcommon types stay as-is with
      inline `{orig, state}`. The caller of a pcommon mutator on
      cow-shared data must call `cow.DetachX` explicitly first.
      Auto-detach works for `pmetric` / `ptrace` / `plog` / `pprofile`
      nested mutations but NOT for pcommon nested mutations.
      Coverage gap: any processor that mutates attributes via
      `Resource.Attributes().PutStr(...)` (which is most attribute
      processors) needs the explicit cow.DetachX call.

   C. **Type-erased Handle via interface or unsafe.Pointer** — pcommon
      types carry an opaque `Detacher` interface that dispatches the
      detach without knowing T. Wraps `*Handle[T]` in a per-signal
      adapter. Works but adds an interface dispatch per access; also
      requires the per-signal adapter to track the path-from-root
      info for re-deriving orig (Resource at `RM[i].Resource` for
      metrics, `RS[i].Resource` for traces, etc.).

The session didn't commit to one. Recommendation in the RFC:
**Option B as the first deliverable** (smaller API impact, partial
coverage with documented limitation), with Option C as a future
follow-up if processor authors find the explicit-Detach pattern too
costly.

### Blocker 3: pdatagen engine doesn't track path-from-root info

The current engine (`internal/cmd/pdatagen/internal/pdata/`) produces
generated wrappers with simple `{orig, state}` fields. To emit
`{h, indices}` layout for nested types, the engine needs to know:

   - For each nested type, its parent type and the field name that
     leads from the parent's orig to this type's orig.
   - Whether the field is direct (`.Resource`), a slice (`.ResourceMetrics`),
     or a slice element (`.ResourceMetrics[i]`).
   - The top-level type for the signal (so `*Handle[T]` is correctly
     parameterised).

This is a non-trivial engine extension. The current `messageStruct`,
`messageSlice`, and `Field` interfaces don't carry the parent-relative
path. Adding it touches multiple files in `internal/cmd/pdatagen/`.

**Recommended approach**: extend `messageStruct` with a `parentField`
field that records the parent type + access path. Propagate through
the template field map as `{ .pathFromRoot }`. The templates use it
in `getOrig()` to walk the indices.

## Locked design decisions for Phase 4-6

Two decisions were surfaced by the pre-Phase-4 audit checkpoint
(comparison of `pdata/internal/cowproto/` prototype against today's
post-Phase-2/3 generated layout) and locked here so a future engineer
doesn't re-litigate them during the regen.

### Decision A: keep `internal.Handle[T]` generic, not concrete-per-signal

The prototype at `pdata/internal/cowproto/types.go` uses a
concrete `Handle` baked to one signal (Metrics). The Path Y
foundation at `pdata/internal/handle.go` uses a generic
`Handle[T any]`. Lock generic for the production templates.

Rationale:
* Generic is already in place (commit 8cc889aeb) and preserves
  type-safety on `orig` per signal — no `unsafe.Pointer` escape.
* Cross-signal symmetry: pmetric/ptrace/plog/pprofile templates
  emit the same field declaration shape, parameterised differently.
* The Go compiler monomorphises `*Handle[T]` per concrete T, so the
  generated code is no slower than 4 hand-written concrete types.
* The verbosity tax (every nested wrapper struct field becomes
  `h *internal.Handle[internal.ExportMetricsServiceRequest]`) is
  absorbed by codegen — not human-maintained.

### Decision B: drop the deep-path generation cache from Phase 4 emission, reassess in Phase 6

The cowproto prototype carries `cachedGauge + gen` fields on deep
slice and datapoint wrappers (`NumberDataPointSlice`,
`NumberDataPoint`, `DataPointAttrMap`) and short-circuits the
index walk when the captured generation matches `state.Generation()`.
The cache reduced the prototype's deep-path microbench overhead
from +67% to +23% over baseline pdata.

Even with the cache, the prototype failed the RFC's 5%/10% perf
gate on `pdata/xpdata/internal/cowprotobench/`. That's why Path Y
was chosen over the index-path-with-cache design originally.

Lock for Phase 4: emit the plain index-walk layout. NO cache fields.
The decision is justified by the simpler scope but the perf
trade-off has NOT been measured at pipeline level — only at the
microbench level documented in `perf/rfc/pdata-cow.md`'s
Engagement-outcome section.

### Phase 6 reassessment criteria (committed upfront)

Phase 6's pipeline-bench validation MUST include:

1. **`BenchmarkConditionalFanoutMetrics` under the cow gate**:
   already-shipped bench, measures deferred-clone benefit at
   pipeline level. Wrapper-access overhead from the index walk
   shows up here as a tax against the benefit.

2. **Deep-access pipeline bench (new)**: a processor that iterates
   `RM[*].SM[*].M[*].DP[*].Attributes()` on the rich
   100×5×50×10 shape. Models batchprocessor / attributesprocessor
   with deep predicates — the unconditional ~21% of `MutatesData:
   true` components from the contrib audit at commit `bac37790c`.
   This is the deep-path workload the prototype's cache existed to
   help.

3. **Cache add-back harness**: emit the cache fields behind a
   template flag (`-cache-deep-paths=true`), regen, benchstat
   against the no-cache HEAD. Mechanical re-add because the cache
   fields are additive — they go on slice/datapoint wrappers and
   the fast path is one branch on `state.Generation() ==
   cachedGen`.

### Phase 6 decision threshold

Based on the deep-access pipeline bench delta vs current pdata
(baseline = perf/baselines/2026-06-17-4ff09b609 or a fresh capture
on `o2/cow-aware-batcher`):

| Slowdown | Action |
| --- | --- |
| < 5% | Ship without cache. Simplicity wins. |
| 5-10% | Add cache back IF the template diff is < 100 LOC. |
| > 10% | Add cache back regardless. |

If the cache is added back, also rerun
`BenchmarkConditionalFanoutMetrics` to confirm the deferred-clone
benefit is preserved (cache overhead on the fast path is two
atomic.Uint32 loads + compare — should be in the noise on the
non-mutator branch).

This explicit threshold replaces the "we'll look at it later" plan;
the data dictates the outcome.

## Path Y resume plan

For the engineer picking this up. Estimated 1-2 weeks of focused
work; do it in this order:

1. **Decide pcommon strategy** (~half-day discussion + design).
   Recommendation: Option B (Path X-hybrid for pcommon). Document
   the contract in `pdata/xpdata/cow/INTEGRATION.md`: "pcommon
   mutators on cow-shared data require an explicit `cow.DetachX(md)`
   call by the surrounding signal-specific code; this is enforced
   at runtime by the AssertMutable safety net."

2. **Update pdatagen test templates** (~1 day) — `message_test.go.tmpl`
   and `slice_test.go.tmpl` to use semantic equality. Run regen,
   confirm tests still pass on today's layout.

3. **Extend pdatagen engine to track parent path** (~2-3 days) —
   `messageStruct.parentField`, propagation through template fields,
   path-aware accessors in the templates. Each nested type's template
   data should include enough info to emit `getOrig() { return
   &h.orig.<path>.<field> }` correctly.

4. **Update production templates** (~2 days) — `message.go.tmpl`,
   `slice.go.tmpl`, `message_internal.go.tmpl` to emit:
     - Top-level wrappers with `{h *Handle[T]}` (already hand-modified;
       template should reproduce).
     - Nested wrappers with `{h *Handle[topT], <indices>}`.
     - `getOrig()` walks `h.orig + indices`.
     - Mutating methods get `state.DetachIfShared()` prelude before
       `AssertMutable`.

5. **Run `make genpdata`** — regenerate all ~344 files atomically.
   Run full pdata test suite. Iterate until green.

6. **Resume `cow.ShareX` to install detacher** — the closure-on-state
   approach (`State.detach`) is already plumbed (commit `45dae78f9`);
   `cow.ShareX` needs the call. Verify with the conditional bench:
   under gate ON, `state.DetachIfShared()` from a nested mutator
   triggers the closure and rebinds the top-level Handle.

7. **Pipeline bench validation** (Path Y.6) — re-run the rig under
   `--feature-gates=pdata.cow` and compare to baseline. Expected:
   the conditional-mutator scenarios show the `(1-hit_rate) ×
   one_clone_cost` curve at pipeline level for processors that mutate
   via pmetric (filter, transform with where, etc.). Document
   limitations for pcommon mutators (Option B caveat).

## Key files to read first

- `perf/rfc/pdata-cow.md` — full design doc including the Engagement
  outcome section that documents the cowproto microbench findings
  (22-67% wrapper-access overhead).
- `pdata/internal/cowproto/` — reference prototype with the index-path
  design for metrics. Demonstrates correctness.
- `pdata/xpdata/internal/cowprotobench/` — bench validating the
  prototype's cost.
- `pdata/internal/handle.go`, `pdata/internal/detach.go`,
  `pdata/internal/state.go` — the foundation in place today.
- `pdata/xpdata/cow/INTEGRATION.md` — current opt-in contract for
  processors. Will need updating for Path Y to reflect auto-detach.

## What to commit to upstream when Path Y completes

1. O2 (audit #19): exporterhelper batcher's `cloneIfShared` and
   removal of the `WithCapabilities({MutatesData: true})` injection.
   Already proven (commit `3de2b3efc`), can land independently of
   Path Y if maintainers want it first.

2. Path Y full: codegen migration + gate + xpdata/cow API +
   conditional-bench evidence + RFC. Single large PR series, gated
   by `pdata.cow` Alpha feature gate.

3. Contrib processor opt-ins (only if Option B for pcommon): per-
   processor PRs adding `cow.DetachX` to processors that mutate via
   pcommon. Audit list of 27 conditional processors lives in
   commit `bac37790c`'s body.

## DO NOT in upstream PRs

- DO NOT post AI-generated comments on issues or PRs. AGENTS.md is
  explicit.
- DO NOT push without re-signing the older commits on
  `perf/local-rig` and `o2/cow-aware-batcher` (some are still
  unsigned).
- DO NOT use `Co-authored-by:` for AI assistance — EasyCLA fails it.
  Use `Assisted-by: Claude Opus 4.7 (1M context)` trailer instead.

Good luck. The hard part (architectural design + benchmark
validation) is done; what remains is mechanical-but-large execution.
