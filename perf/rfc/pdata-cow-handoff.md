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
