# RFC — Selective copy-on-write for pdata

**Status:** *Local — Alternative A prototyped and shelved; shipping
Alternative C scaffolding only. See "Engagement outcome" below.*

## Engagement outcome (2026-06-17)

The Alternative A design described in this document (Handle indirection
at top-level + detach mechanism at every AssertMutable site) was
prototyped under [`pdata/internal/cowproto/`](../../pdata/internal/cowproto/)
with index-path nested wrappers + generation-cache fast paths.
Apples-to-apples microbench (`pdata/xpdata/internal/cowprotobench/`)
vs today's pdata wrappers, wrapping the same backing tree:

| Bench | Baseline | cowproto (Alt A) | Delta |
|-------|----------|------------------|-------|
| ReadAllAttributes (deep, 25k chains) | 920µs | 1539µs | **+67%** |
| MutateAllAttributes | 1.87ms | 2.40ms | **+28%** |
| MetricsUsage (mixed read+mutate) | 565µs | 876µs | **+55%** |
| ReadResourceAttrsOnly (shallow) | 570ns | 389ns | **−32%** (faster) |
| Geomean | 153µs | 188µs | **+23%** |

Verdict against the perf gate in "Decision: Alternative A" (5% target /
10% acceptable ceiling): **fails** on the realistic deep-path workload
that processors typically exhibit (datapoint attribute access).

Two compounding factors made the work not ship:

1. Wrapper-layer overhead is real and well above the gate. The shallow
   path is actually faster, but production processors heavily touch
   datapoint attributes.
2. The deferred-clone benefit is narrower than the rig's headline
   `325k→0` number suggested. That number was a **bench artifact** —
   MarkReadOnly contamination on iter 2+ of a reused `md`. In
   production each batch is fresh, so the contamination doesn't fire.
   Post-O2 (the wrapped-batched-exporter fix), the receiver-side fanout
   is already at ~1 clone per batch in most realistic topologies.

The decision was to pivot to **Alternative C** (eager clone in
`cow.Share*`) and ship the scaffolding without the deferred-clone
mechanism:

- `pdata.cow` feature gate registered (Alpha, default off).
- `pdata/xpdata/cow/` API stable: `Share* / Release* / IsShared*`.
  Under the gate, `Share*` does an eager CopyTo (functionally identical
  to today's `cloneMetrics`); `Release*` decrements bookkeeping cowRefs.
- `pdata/internal/handle.go` Handle struct preserved as foundation for
  a future deferred-clone design (currently unused by the eager path).
- `State.cowRefs` + `State.generation` fields preserved (cowRefs is
  used by the Share/Release bookkeeping; generation is reserved for a
  future deferred design).
- Fanout (`internal/fanoutconsumer/{metrics,traces,logs,profiles}.go`)
  branches on the gate: ON → `cow.Share*`; OFF → unchanged CopyTo.
  Wiring is in place for a future engagement that designs a working
  per-subtree COW to flip the Share semantics without re-touching the
  fanout.

The prototype + microbench live in commit history (see commit
`cf63dd25e`) for the next engagement that takes another swing at the
deferred-clone problem.

The remainder of this document describes the original Alternative A
design as it stood before the prototype findings. Treat it as
**historical** — the engagement shipped C, not A. The terminology,
design alternatives, and refcount/State extensions still apply.

## Path Y resume (2026-06-18) — pcommon strategy decision

Subsequent static-analysis audit of opentelemetry-collector-contrib
processors (commit `bac37790c`) revised the production-benefit
estimate: 27 of 34 `MutatesData: true` components (~79%) are
conditional mutators (OTTL `where`, name/key lookups, data-shape
preconditions). The conditional-mutator pipeline bench (Phase 2.C in
[`hey-claude-a-while-wise-stonebraker.md`](../../../../.claude/plans/hey-claude-a-while-wise-stonebraker.md))
validated `saved ≈ (1 − hit_rate) × one_clone_cost` at pipeline level.
This was sufficient justification to resume the Alternative A codegen
migration on a fork-bound branch (`path-y/codegen-migration`), with a
six-phase plan and locked correctness rules — see the engagement
plan for the resume plan and the rules.

The resume work surfaced one design decision that wasn't visible at
the original Alternative A RFC: **how to handle pcommon shared types
(`pcommon.Resource`, `pcommon.Map`, `pcommon.Value`, etc.) under the
Handle + index-path layout.**

### The pcommon problem

The cowproto design (Alternative A) threads a typed `*Handle[T]` —
where `T` is the top-level proto type (`ExportMetricsServiceRequest`,
`ExportTraceServiceRequest`, etc.) — through every nested wrapper so
the wrapper can re-derive its `orig` after a detach rebinds the
top-level pair. This works fine for per-signal nested types
(`pmetric.ResourceMetricsSlice`, `ptrace.SpanSlice`, etc.) because
their top-level type is fixed by their signal.

`pcommon.Resource` and friends are used by metrics, traces, logs, and
profiles. There is no single `T` they can be parameterised over — Go
generics can't bridge multiple concrete types at the same call site.
Without a `*Handle[T]`, a pcommon wrapper held across a detach has no
way to re-derive its `orig` pointer.

### Three options considered

**Option A — Per-signal pcommon variants.** Triplicate (quadruplicate
including profiles) pcommon: `MetricsResource`, `TracesResource`,
`LogsResource`, `ProfilesResource`, and same for `Map`, `Value`,
`KeyValueSlice`, etc. Each variant carries the appropriate
`*Handle[T]` and re-derives correctly.

- Pros: full auto-detach coverage; correctness end-to-end.
- Cons: major API break — `pcommon.Resource` is the type widely used
  across the ecosystem. Every contrib processor's import surface
  changes. Multi-month coordination, almost certain to be rejected
  upstream.

**Option B — Hybrid Path X for pcommon.** Keep `pcommon.Resource` and
friends with today's inline `{orig, state}` layout. The auto-detach
mechanism applies to `pmetric`/`ptrace`/`plog`/`pprofile` nested
wrappers only. Callers that mutate via pcommon on cow-shared data
(`md.ResourceMetrics().At(0).Resource().Attributes().PutStr(...)`)
must call `cow.DetachX(md)` explicitly first. The `AssertMutable`
safety net (which already panics on `cowRefs > 0`) surfaces
contract violations during dev/test.

- Pros: zero API change for pcommon; minimum-risk, minimum-blast-
  radius implementation. Path Y's auto-detach still wins for filter,
  batch, transform-via-RemoveIf-style processors. The opt-in burden
  for attribute-mutating processors mirrors O2's pattern (which
  maintainers already approved for the `exporterhelper` batcher).
- Cons: coverage gap. Processors that mutate attributes (the
  attributes/k8sattributes/resourcedetection/transform-with-OTTL-set
  family) still need the explicit Detach call. The "transparent
  auto-detach" promise of Path Y only applies to pmetric-level
  mutations.

**Option C — Type-erased Handle via interface or unsafe.Pointer.**
pcommon types carry an opaque `Detacher` interface that dispatches
the detach without knowing `T`. Wraps the per-signal `*Handle[T]` in
an adapter. Each pcommon wrapper additionally tracks its path-from-
root info (which varies per signal — `RM[i].Resource` for metrics vs
`RS[i].Resource` for traces, etc.) so it can re-derive `orig` after
detach.

- Pros: full auto-detach coverage without API change.
- Cons: adds an interface dispatch (~1-2 ns) per accessor on the hot
  path; the per-signal adapter has to carry the same `parentField`
  information the engine extension (Phase 3) tracks; complexity is
  high.

### Decision: Option B

The engagement ships Option B as the first deliverable. Reasoning:

- **Minimum risk to existing APIs.** pcommon is the most widely-
  imported pdata package; an API change there is the highest-friction
  upstream move possible. Maintainers will weigh ergonomics heavily.
- **Mirrors O2's already-approved pattern.** The
  `cloneIfShared(...)` move in the `exporterhelper` batcher (commit
  `3de2b3efc`) is the same shape: an explicit "I'm about to mutate
  shared data, give me my own copy" call inside the mutator. If
  maintainers approved that for the batcher, the same pattern applied
  to in-tree + contrib processors is a natural extension.
- **The safety net makes the contract enforceable.** `AssertMutable`
  on `cowRefs > 0` panics with a clear pointer to `pdata/xpdata/cow`.
  A processor that violates the contract gets a loud, debuggable
  failure during dev/test, not silent corruption.
- **Option C remains available as a future iteration.** If processor
  authors find Option B's ergonomics costly, a Path Y v2 can
  introduce the type-erased Handle without re-litigating the broader
  design.

The coverage gap is documented in `pdata/xpdata/cow/INTEGRATION.md`
(to be updated as part of Phase 5): "pcommon mutators on cow-shared
data require an explicit `cow.DetachX(md)` call by the surrounding
signal-specific code." The audit's classification of conditional
mutators flags which contrib processors are affected; the resume
plan's Phase 5 includes an audit pass to list them.

## Motivation

In multi-consumer pipelines, the fanout consumer
([`internal/fanoutconsumer/{metrics,traces,logs,profiles}.go`](../../internal/fanoutconsumer/))
issues an eager deep-clone of `pmetric.Metrics` / `ptrace.Traces` / `plog.Logs` for every mutating
downstream branch except the last, and marks the data read-only when broadcasting to multiple
read-only consumers. For a representative metrics batch (100 resources × 5 scopes × 50 datapoints
× 10 attributes per datapoint), one such clone is **~328 k wrapper allocations and ~17 MB of
heap traffic per batch** (measured by `BenchmarkCopyToMetrics/shape=rich_100x5x50x10` at branch
`perf/local-rig` tip on Apple M1 Max, Go 1.26.3).

In topologies where most downstream consumers are read-only (typical for OTLP fanouts: one
mutating exporter alongside one or more passive sinks, connectors, or sampling branches), the
clone is mostly waste — the read-only consumers never mutate the data and the eager clone
allocates memory that the GC has to reclaim every batch.

This RFC proposes a **selective copy-on-write (COW) layer for pdata**: shared backing storage
across the fanout's downstream consumers, with deep-clone deferred to the first mutating call
on any one consumer. Behind a `pdata.cow` feature gate, default off until validated against the
rig + production-shape SMP cases.

The rig that motivates this work is in
[`perf/baselines/2026-06-17-4ff09b609/`](../baselines/2026-06-17-4ff09b609/). See
[`docs/performance.md`](../../docs/performance.md) for how to run it.

## Goals

1. Replace the fanout's per-consumer `cloneMetrics(md)` calls with a `cow.Share(md)` API (in
   the new `pdata/xpdata/cow/` package) that increments a refcount and hands out wrappers
   pointing at the same underlying proto tree. Behind a feature gate.
2. On first mutation by any consumer, transparently detach (deep-copy) that consumer's view so
   the other consumers keep their shared, mutation-safe handle on the original.
3. Preserve the existing pdata public API surface — callers writing `md.PutStr(...)` or
   `md.ResourceMetrics().AppendEmpty()` must not see any new error path or panic that they did
   not see before the gate.
4. Stay compatible with the existing refcount machinery in `pdata/internal/state.go` +
   `pdata/xpdata/pref/` so the existing `pdata.enableRefCounting` feature gate's pipeline-
   ownership semantics aren't broken.

## Non-goals

- **Persistent or structurally-shared data structures** (Clojure-style vectors, Lean-style
  arrays). A complete pdata rewrite for theoretical sharing-optimal behavior is out of scope.
- **Concurrent mutation of the same wrapper.** The existing pdata contract says a wrapper is
  single-writer; that contract stays.
- **Cross-batch sharing.** Each fanout dispatch is independent; COW lives within one batch's
  fan-out, not across batches.
- **OTLP-arrow / columnar storage interop.** The columnar path has its own sharing model.
  Treated as orthogonal here.

## Background

### Existing state machinery

[`pdata/internal/state.go`](../../pdata/internal/state.go) already exposes most of what we need:

```go
type State struct {
    refs  atomic.Int32
    state uint32
}

const (
    stateReadOnlyBit      = uint32(1 << 0)
    statePipelineOwnedBit = uint32(1 << 1)
)

func (st *State) MarkReadOnly()
func (st *State) IsReadOnly() bool
func (st *State) AssertMutable()              // panic if read-only
func (st *State) MarkPipelineOwned() bool     // CAS-style; true if newly owned
func (st *State) Ref()                        // refs++
func (st *State) Unref() bool                 // refs--; true if reached 0
```

`refs` is `atomic.Int32`; `state` is a plain `uint32` of bit flags. The atomic increment exists,
but `MarkReadOnly` itself is **not atomic** (`st.state |= bit`) — fine today because read-only
marking is single-threaded at the fanout boundary, but worth keeping in mind.

The refcount is gated by [`PdataEnableRefCountingFeatureGate`
(`pdata.enableRefCounting`, Beta)](../../pdata/xpdata/internal/metadata/generated_feature_gates.go),
invoked from `service/internal/refconsumer/` to bookkeep pipeline ownership:

```go
// pref/metrics.go
func RefMetrics(md pmetric.Metrics) {
    if metadata.PdataEnableRefCountingFeatureGate.IsEnabled() {
        internal.GetMetricsState(internal.MetricsWrapper(md)).Ref()
    }
}
```

The refcount semantics today: **one increment per pipeline that owns the data**, decremented at
the pipeline's exit. This is distinct from COW sharing, which is **one increment per fanout
consumer**.

### Wrapper layout — the value-typed problem

Every pdata wrapper carries two pointers — `orig` (into the proto tree) and `state` (to the
shared `*State`):

```go
// pdata/internal/generated_wrapper_exportmetricsservicerequest.go
type MetricsWrapper struct {
    orig  *ExportMetricsServiceRequest
    state *State
}

// pdata/pmetric/generated_resourcemetrics.go
type ResourceMetrics struct {
    orig  *internal.ResourceMetrics
    state *internal.State
}

// pdata/internal/wrapper_map.go
type MapWrapper struct {
    orig  *[]KeyValue
    state *State
}
```

The wrappers are **value types**, passed by value through the API:

```go
metrics.ResourceMetrics().At(0).Resource().Attributes().PutStr("k", "v")
//                                                       ^^^^^^
//        Map (a value type) calls m.getState().AssertMutable() internally.
```

Every leaf wrapper created mid-chain — `ResourceMetricsSlice`, `ResourceMetrics`, `Resource`,
`Map` — holds the same root `*State` (verified by tracing the generated accessors). The
back-reference invariant required for "every mutator funnels through one root state" **holds**.

But each leaf wrapper also holds an `orig` pointing **into** the original backing tree. A
detach that swaps the root's backing must update those pointers; it cannot, because the leaf
wrapper's value-typed copy lives on the caller's stack and the detach code has no reference to
it. This is the load-bearing design problem.

## Design alternatives

Three plausible designs for the detach mechanism, each with different invasiveness:

### Alternative A — Handle indirection

Replace each wrapper's `(orig, state)` pair with a single `*Handle` pointer:

```go
type Handle[T any] struct {
    orig *T
    state *State
}

type MetricsWrapper struct {
    h *Handle[ExportMetricsServiceRequest]
}

type ResourceMetrics struct {
    h *Handle[internal.ResourceMetrics]
}
```

On detach, `h.orig` is reassigned to point at the freshly-deep-cloned tree. Every wrapper that
descended from the same `*Handle` automatically sees the new orig because they all dereference
the same heap pointer.

**Pros.**

- Preserves call-site ergonomics — no API change at the pdata public surface.
- Detach is local: one assignment to `h.orig`, one new `*State`, refcount fixup.
- No reflection / no type-erasure tricks; codegen produces typed handles via a `Handle[T]`
  generic.

**Cons.**

- **Every wrapper access becomes one extra pointer chase.** For a hot inner loop iterating a
  slice of 10 k spans, that's 10 k extra cache loads — measurable on tight benchmarks.
- All ~344 generated files under `pdata/{pmetric,ptrace,plog,pcommon,pprofile}/` are touched.
- `MetricsWrapper.h == nil` becomes a new zero-value error mode that the existing
  `getOrig()/getState()` helpers would need to handle (or panic on).
- Existing helpers like `internal.GetMetricsOrig` and `internal.GetMetricsState` change return
  semantics: instead of returning the wrapper's direct field, they dereference one more
  pointer.

**Touch.** ~1 LOC change in every generated wrapper file's `orig`/`state` accessor + a
template change to the pdatagen `message.go.tmpl` and `slice.go.tmpl`. Auto-regenerated diff is
large but mechanical.

### Alternative B — State owns orig (centralized indirection)

Move `orig` into the `*State`:

```go
type State struct {
    refs  atomic.Int32
    state uint32
    orig  unsafe.Pointer  // *X for the root pdata type
}

type MetricsWrapper struct {
    state *State  // orig is state.orig
}
```

Nested wrappers (e.g. `ResourceMetrics`) still need to navigate to their subtree. They could
either (a) recompute their orig from state.orig + a path on every access, or (b) cache a `local
orig` plus a generation counter against state, and rebuild on mismatch.

**Pros.**

- One source of truth for orig.
- Detach is a single State mutation.

**Cons.**

- Either (a) every nested access pays a navigation cost (more expensive than Handle's single
  pointer chase) or (b) every nested wrapper needs a generation field — extra struct field,
  extra check on every access, complex invalidation logic.
- `unsafe.Pointer` reintroduces an `interface{}`-like cost (escape analysis loses precision).
- Doesn't fit Go's value-typed wrapper model as cleanly as Alternative A.

**Recommendation:** *do not pursue.* All the costs of Alternative A, plus more.

### Alternative C — Detach-on-handout (narrow scope)

`cow.Share` does NOT defer the clone. Instead it:

1. Increments the existing-State's refcount.
2. Allocates a NEW orig (deep clone) and NEW state for the returned wrapper.
3. The original's refcount represents "I might still be detached from later"; the new
   wrapper's refcount starts at 1.

This is just **eager clone with a refcount counter**. The "sharing" only buys us:

- Marking the original read-only safely (still useful for protecting against accidental
  upstream mutation).
- A hook for future "all-read-only fast path" optimization (don't bother detaching if no
  consumer mutates).

**Pros.**

- Zero changes to mutator codegen — the existing `AssertMutable` panic remains.
- Strictly additive API.
- Worst-case cost is the same as today.

**Cons.**

- **No performance win** in the typical "1 mutator + N read-only" topology. The audit's
  motivating clone still happens.
- Only useful as scaffolding for a later subtree-COW change.

**Recommendation:** *do not pursue alone.* Acceptable as a stepping stone if maintainers
balk at Alternative A's blast radius.

## Decision: Alternative A

We commit to **Handle indirection (Alternative A)** as the design. It is the only one of the
three that delivers the audit's promised win (deferred clone on first mutation) without
giving up call-site ergonomics or introducing reflection/unsafe escape hatches. The pointer-
chase cost is one additional cache-line load per wrapper access — non-zero but bounded — and
the rig microbench gate at O3.7 (below) ensures we don't ship a regression.

Alternative C (detach-on-handout / eager clone with refcount) is retained as a **documented
fallback** in the Risks section: if Alternative A's hot-loop overhead exceeds the gate
threshold, the work pivots to C without re-litigating the entire design. Alternative B
(State owns orig) is rejected outright — all of A's costs, none of its ergonomic wins.

**Benchmark gate (mandatory before `pdata.cow` flips on by default).** The cost of Handle
indirection on the existing `BenchmarkMetricsUsage`, `BenchmarkTracesUsage`,
`BenchmarkLogsUsage` hot loops must be under **5 %** (target) / **10 %** (acceptable
ceiling). Exceeding 10 % triggers the C-fallback path.

## Contract change: captured nested wrappers and detach

Strategy A's Handle indirection rebinds the **top-level** wrapper's `(orig, state)` pair when a
detach fires. It does **not** rebind every nested wrapper value the caller may have previously
captured. Specifically, value-typed wrappers obtained from a derived chain hold direct
pointers into the (now-stale) old tree:

```go
res := md.ResourceMetrics().At(0).Resource()   // captured: Resource{orig: *old, state: ...}
res.Attributes().PutStr("k", "v")              // triggers detach; md.h now points at new tree
v, ok := res.Attributes().Get("k")             // reads from `res.orig` — the OLD tree
// Bug: Get returns "k" not found, because PutStr wrote to the CLONE.
```

Under `pdata.cow`, **previously-captured nested wrappers held across a mutating call on the
chain are unsafe.** This is a new contract on top of the existing
[pdata package doc](../../pdata/README.md). In-tree exporters and processors don't violate
it (their mutations are inline or top-level; e.g.,
[`batchprocessor`'s MoveAndAppendTo](../../processor/batchprocessor/) operates on
`req.md.ResourceMetrics()` directly). The
[`BenchmarkMetricsUsage`](../../pdata/pmetric/metrics_test.go) pattern that captures a `res`
across mutations is a real pattern but only triggers the contract when `cowRefs > 0` — and
that bench runs standalone, never inside a sharing fanout, so it's unaffected.

The contract is enforced at **debug** time via a generation counter:

```go
type State struct {
    refs       atomic.Int32
    cowRefs    atomic.Int32
    state      uint32
    generation uint32   // NEW: incremented by detach
}

// In every nested wrapper (codegen):
type Resource struct {
    orig       *otlpcommon.Resource
    state      *internal.State
    generation uint32   // NEW: captured at wrapper creation time (debug builds only)
}

// In every nested wrapper's getOrig() accessor (codegen, debug builds only):
func (ms Resource) getOrig() *otlpcommon.Resource {
    if ms.state.generation != ms.generation {
        panic("pdata: stale nested wrapper used after detach")
    }
    return ms.orig
}
```

The generation check is **debug-build-only** (`//go:build pdatacowdebug` or equivalent). The
release-build accessor is unchanged — zero overhead. CI runs the pdata test suite under both
tags. Production binaries pay nothing.

**Migration documentation.** Three docs land alongside the gate:

- The pdata package doc (`pdata/README.md`) gets a "Behavior under `pdata.cow`" section.
- The `pdata.cow` feature gate description points at the migration guide.
- A new `docs/pdata-cow-migration.md` walks users (especially contrib processor authors)
  through the contract change with examples of breaking patterns and refactors.

## Detach mechanism (Alternative A)

The detach lives at every site that currently calls `AssertMutable()`. The codegen template
gains a new prelude under the feature gate:

```go
// Before (today):
func (ms NumberDataPoint) SetIntValue(v int64) {
    ms.h.state.AssertMutable()                          // panic if read-only
    ms.h.orig.Value = &otlpmetrics.NumberDataPoint_AsInt{AsInt: v}
}

// After (under pdata.cow):
func (ms NumberDataPoint) SetIntValue(v int64) {
    ms.h.state.detachIfShared(ms.h)                     // cowRefs > 0 → clone the root tree
    ms.h.state.AssertMutable()                          // still panics on a marked-RO root
    ms.h.orig.Value = &otlpmetrics.NumberDataPoint_AsInt{AsInt: v}
}
```

`detachIfShared` is the load-bearing piece. Pseudocode:

```go
func (st *State) detachIfShared(h *Handle) {
    if st.state&stateReadOnlyBit != 0 {
        return // readonly: leave AssertMutable to panic
    }
    if st.cowRefs.Load() == 0 {
        return // fast path: not shared, nothing to do
    }
    // CAS-decrement cowRefs from N to N-1. On success, we claim a detach slot:
    // deep-clone the root tree, allocate a fresh *State (cowRefs: 0, refs: 1),
    // and rebind h.orig/h.state to the new tree atomically.
    // ...
}
```

Concurrency: a CAS loop is required. Two goroutines mutating shared wrappers concurrently could
both observe `cowRefs > 0`; without CAS they'd both decrement, neither would clone, and the
data would be silently shared with mutations applied to both. The CAS-loop semantics: one
detaches and clones; the other observes `cowRefs == 0` post-decrement and proceeds without
cloning. We must document this race and add a `-race` test that exercises it.

## Refcount: separate `cowRefs` field

`State.refs` exists today and is owned by `pref.Ref/Unref` (one increment per pipeline-owning
consumer). The COW design adds a **separate** `cowRefs atomic.Int32` field on `State`,
incremented by `cow.Share` and decremented by `cow.Release` / by `detachIfShared` when it
materialises a clone:

```go
type State struct {
    refs       atomic.Int32  // pref's pipeline-ownership counter (unchanged)
    cowRefs    atomic.Int32  // NEW: number of active COW shares pointing at this State
    state      uint32
    generation uint32        // NEW: bumped by detach; debug-build staleness checks
}
```

**Why separate, not shared.**

- **Preserves the `AssertMutable` safety net.** If a consumer that received the data via the
  fanout's MarkReadOnly broadcast accidentally calls a mutating leaf, a shared-`refs` design
  would observe `refs > 1` (because of `pref`'s pipeline ownership) and silently detach. A
  separate `cowRefs` short-circuits when `cowRefs == 0`, falls through to `AssertMutable`,
  and panics as intended.
- **Decouples from `pdata.enableRefCounting`'s lifecycle.** That gate is Beta. If maintainers
  iterate on its semantics (e.g., add a `MarkPipelineUnowned` hook, or change the
  Ref/Unref pairing), COW is unaffected.
- **Clean observability.** `cowRefs` maps 1:1 to "active shares of this State"; the
  `otelcol_pdata_cow_detach_total` counter and any future `otelcol_pdata_cow_shares_inflight`
  gauge come straight off it.

Memory cost is one extra `int32` per top-level `*State`. `*State` is allocated per top-level
`Metrics`/`Traces`/`Logs`/`Profiles` wrapper, so 4 bytes per in-flight batch — negligible
against the proto trees themselves (KB to MB per batch).

**Detach precondition.**

```go
func (st *State) detachIfShared(h *Handle) {
    if st.state&stateReadOnlyBit != 0 {
        return // never silently absorb a readonly contract violation
    }
    if st.cowRefs.Load() == 0 {
        return // no active shares — exclusive ownership, fast path
    }
    // CAS-loop detach; see "Detach mechanism" below.
}
```

The readonly short-circuit is intentional: if the upstream fanout broadcast read-only-marked
data to a consumer that then tries to mutate, that's a contract violation. We preserve
today's panic.

**`pdata.cow` gate dependency on `pdata.enableRefCounting`.** `pref.Ref/Unref` doesn't need
to be on for COW to work (the two counters are independent), but the existing pipeline-
ownership behavior is what informs operator mental models. We register `pdata.cow` with a
description that notes both gates are independent but typically enabled together for
production rollouts.

## API placement: `pdata/xpdata/cow/`

**Lifecycle is explicit:** `Share` bumps `cowRefs`; a paired `Release` decrements it. Pdata's
GC cannot drive the decrement (the Handle is still reachable from the source wrapper), so
explicit Release is mandatory rather than optional.

The new API ships as **functions in a new `pdata/xpdata/cow/` package**, mirroring the
existing `pdata/xpdata/pref/` precedent for pdata-adjacent functionality. This keeps the
**stable** `pmetric.Metrics` / `ptrace.Traces` / `plog.Logs` / `pprofile.Profiles` API
surface untouched. The behavior-changing piece (codegen-injected `detachIfShared` preludes,
Handle indirection, the new `State` fields) lives in `pdata/internal` where the existing
`pref` package already reaches:

```go
// pdata/xpdata/cow/metrics.go (new — symmetric files for traces, logs, profiles)
package cow

import (
    "go.opentelemetry.io/collector/pdata/internal"
    pmetadata "go.opentelemetry.io/collector/pdata/internal/metadata"  // hosts the gate
    "go.opentelemetry.io/collector/pdata/pmetric"
)

// Share returns a COW share of md — a new pmetric.Metrics wrapper value that points at the
// same backing State and orig as md, with cowRefs incremented. The first mutation on the
// returned Share triggers a deep detach; both the source and the Share are then
// independent. The caller MUST pair Share with Release, typically via defer.
//
// When the pdata.cow feature gate is off, Share returns a deep-cloned, independent
// pmetric.Metrics, preserving today's eager-clone semantics for backward compatibility.
func Share(md pmetric.Metrics) pmetric.Metrics {
    if !pmetadata.PdataCOWFeatureGate.IsEnabled() {
        // Fallback: equivalent to today's cloneMetrics.
        clone := pmetric.NewMetrics()
        md.CopyTo(clone)
        return clone
    }
    internal.GetMetricsState(internal.MetricsWrapper(md)).IncCowRefs()
    return md // value-copied wrapper; same *Handle pointer
}

// Release decrements cowRefs on md's backing State. No-op if md was already detached
// (cowRefs == 0 on a detached copy). Safe to call multiple times. Always pair with Share.
func Release(md pmetric.Metrics) {
    if !pmetadata.PdataCOWFeatureGate.IsEnabled() {
        return
    }
    internal.GetMetricsState(internal.MetricsWrapper(md)).DecCowRefs()
}

// IsShared reports whether md's underlying State has any active COW shares. Diagnostic.
func IsShared(md pmetric.Metrics) bool {
    return internal.GetMetricsState(internal.MetricsWrapper(md)).CowRefs() > 0
}
```

The pattern matches [`pdata/xpdata/pref/metrics.go`](../../pdata/xpdata/pref/metrics.go) line-
for-line. `IncCowRefs` / `DecCowRefs` / `CowRefs` are new methods on `internal.State`,
identical in shape to the existing `Ref` / `Unref`.

**Why function-form, not methods on the stable wrapper.** `pmetric.Metrics` ships under
`Stability: stable` ([pdata/README.md](../../pdata/README.md)). Adding gate-controlled
methods to a stable type is a non-trivial governance ask. Functions in `xpdata` follow the
established precedent (the `x` prefix is the project's experimental marker; see also
`xconsumer`, `xreceiver`, `xexporter`, `xprocessor`, `xpipeline`). Things land in `xpdata`
first; once validated, they can graduate into stable pdata as methods in a follow-up.

**Fanout integration** (in
[`internal/fanoutconsumer/metrics.go`](../../internal/fanoutconsumer/metrics.go) and the three
sibling files):

```go
// Before:
if len(msc.mutable) > 0 {
    for i := 0; i < len(msc.mutable)-1; i++ {
        errs = multierr.Append(errs, msc.mutable[i].ConsumeMetrics(ctx, cloneMetrics(md)))
    }
    // ...
}

// After (the gate is checked inside cow.Share — fanout just calls it unconditionally):
if len(msc.mutable) > 0 {
    for i := 0; i < len(msc.mutable)-1; i++ {
        errs = multierr.Append(errs, msc.shareAndConsume(ctx, md, msc.mutable[i]))
    }
    // ...
}

// shareAndConsume pairs Share with a deferred Release. The Share's lifecycle is bounded
// by the consumer's ConsumeMetrics call (which by the consumer contract must not retain
// the data after return).
func (msc *metricsConsumer) shareAndConsume(ctx context.Context, md pmetric.Metrics, c consumer.Metrics) error {
    shared := cow.Share(md)
    defer cow.Release(shared)
    return c.ConsumeMetrics(ctx, shared)
}
```

The read-only path stays the same — `MarkReadOnly` + broadcast. Read-only consumers do
**not** receive `Share`'d wrappers (the existing `MarkReadOnly` already prevents accidental
mutation via `AssertMutable`, and `cowRefs` stays at zero for that branch so the readonly
short-circuit in `detachIfShared` doesn't fire on accidental mutations).

## Observability

A new metric `otelcol_pdata_cow_detach_total{signal=metrics|traces|logs|profiles}` increments
on every successful `detachIfShared` deep-copy. This lets operators verify the COW path is
effective in their topology — high detach rate means most downstreams are mutating, low rate
means COW is paying off.

The counter lives in the `service/telemetry` instrumentation surface, surfaced as part of the
existing collector self-telemetry.

## Phasing

- **O3.1 — Feature gate scaffolding.** Register `pdata.cow` (Alpha, default off) in
  [`pdata/xpdata/internal/metadata/generated_feature_gates.go`](../../pdata/xpdata/internal/metadata/generated_feature_gates.go).
  Mark `pdata.enableRefCounting` as a prerequisite in the description. ~30 LOC.
- **O3.2 — Handle struct + State extensions.** Introduce `internal.Handle[T]` (or four typed
  Handles, since Go generics interact awkwardly with the wrapper accessors). Add `cowRefs`
  and `generation` fields to `internal.State`; add `IncCowRefs` / `DecCowRefs` / `CowRefs`
  methods alongside the existing `Ref` / `Unref`.
- **O3.3 — `pdata/xpdata/cow/` package.** Add `cow.Share` / `cow.Release` / `cow.IsShared`
  for each of the four signals, mirroring the existing `pdata/xpdata/pref/` shape. Each
  function is gate-checked at the top; gate-off falls back to `CopyTo`.
- **O3.4 — Codegen migration.** Modify the pdatagen templates at
  [`internal/cmd/pdatagen/internal/pdata/templates/{message,slice}.go.tmpl`](../../internal/cmd/pdatagen/internal/pdata/templates/)
  to emit Handle-bearing wrappers when the gate is on. The
  `internal.GetMetricsOrig` / `GetMetricsState` helpers gain an extra deref level. Add the
  `detachIfShared` prelude at every existing `AssertMutable` callsite. Add the
  `generation`-based staleness check in nested-wrapper `getOrig`/`getState` accessors
  under the `pdatacowdebug` build tag. Regenerate all ~344 generated files via
  `make genpdata`.
- **O3.5 — Fanout integration.** Switch the four fanout files to `cow.Share` + deferred
  `cow.Release`.
- **O3.6 — Observability.** `otelcol_pdata_cow_detach_total` counter.
- **O3.7 — Validation pass.** Run rig with `--feature-gates=pdata.cow`; capture a new baseline
  under `perf/baselines/`. Sign off on the delta vs `2026-06-17-4ff09b609`.
- **O3.8 — Stabilization.** Two release cycles at Alpha; promote to Beta only after SMP
  validation confirms no production regression. Beta → Stable per the standard featuregate
  lifecycle.

Each sub-phase is its own upstream PR, in order, with the tracking issue (Open Question 1
below) referenced.

## Migration / compatibility

- **Existing pdata callers:** unchanged. All public method signatures, all panic paths, all
  read-only contracts remain.
- **Contrib repository:** zero direct impact. The codegen change is transparent to consumers
  of the public pdata API. The Handle indirection's perf cost will surface in contrib's own
  bench numbers — we'll need to pre-announce.
- **`pdata.enableRefCounting`:** required-on for the COW gate to be meaningful. Document the
  dependency in the gate registration.
- **OpenTelemetry SDKs / OTLP wire format:** no impact. COW is internal to the in-memory
  representation.

## Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Handle pointer-chase cost on hot loops exceeds 5 % (target) | Medium | Pre-O3.7 microbench gate; if cost is > 10 % the work pivots to **Alternative C (detach-on-handout / eager clone with refcount)** — see the "Design alternatives" section. C ships the `pdata.cow` gate + `cow.Share`/`cow.Release` API but keeps today's eager-clone semantics. No deferred-clone perf win, but no regression either; the rig hooks land regardless, so a future per-subtree COW can build on the same scaffolding. |
| Captured nested wrappers held across a mutation on the chain return stale reads | Medium | New contract documented in `pdata/README.md` + `docs/pdata-cow-migration.md` + the `pdata.cow` gate description. Debug-build generation counter on nested wrappers panics on stale access during development and CI; release builds pay zero overhead. In-tree code is audited at gate-flip time; contrib has its own audit announcement on the SIG channel. |
| Concurrent-detach race produces stale references | Medium | CAS loop, `-race` test, document the worst-case "extra clone" outcome |
| Refcount semantics drift between `pref` and COW | Medium | Both feed `refs`; detach treats them uniformly; document |
| AssertMutable semantics change (panic → silent detach) breaks tests | Low | Feature-gated; existing panic path preserved with gate off |
| Wrapper indirection breaks pref / xpdata / refconsumer | Medium | Adapt `internal.GetMetricsState` etc. to new Handle layout; tested by existing pdata test suite |
| Cross-module ABI churn between `pdata` and per-signal modules | Low | The repo already coordinates this via `make genpdata`; no new pattern |

## Open questions

1. **Upstream RFC venue.** Does this live as a docs/rfcs/ doc in the upstream repo, or as a
   GitHub issue + discussion thread? The repo's
   [`docs/rfcs/README.md`](../../docs/rfcs/README.md) suggests "RFCs should be announced in a
   Collector SIG meeting and on the #otel-collector-dev Slack channel" — confirm process with
   maintainers before opening anything.
2. **Code owners.** [`pdata/README.md`](../../pdata/README.md) lists @bogdandrutu and @dmitryax
   as code owners. Engage early to align before any code lands.
3. **Stability promise.** Settled: the API lands in
   [`pdata/xpdata/cow/`](../../pdata/xpdata/) following the existing
   [`pdata/xpdata/pref/`](../../pdata/xpdata/pref/) precedent. The stable
   `pmetric.Metrics` / `ptrace.Traces` / `plog.Logs` / `pprofile.Profiles` API surface is
   untouched. The `xpdata` location signals that the API is experimental until proven; if
   maintainers later want it promoted to a method on the stable wrapper, that's a follow-up
   PR after the gate goes Stable.
4. **Default-on timeline.** Beta-default-off → Beta-default-on requires production-shape
   validation. SMP integration (Workstream Follow-up in
   [`hey-claude-a-while-wise-stonebraker.md`](../../../../.claude/plans/hey-claude-a-while-wise-stonebraker.md))
   is the natural place to capture that signal.
5. **Detach metric cardinality.** A single counter per signal is small. Should we also tag by
   the consumer's component ID? That gets expensive in high-cardinality fan-outs; default to
   no tag, add behind a sub-flag if operators ask.
6. **Profiles.** Settled: included in Phase 1 as a symmetric peer of metrics/traces/logs.
   The codegen template change is signal-agnostic; `pdata/xpdata/cow/profiles.go` lands
   alongside the other three files. **Caveat:** `pdata/pprofile` is marked Development.
   If pprofile undergoes incompatible churn during the `pdata.cow` gate's stabilization,
   profiles can lag the metrics/traces/logs gate-default-on timeline without forcing the
   whole work to wait.

## What's not in this RFC

- **Per-subtree COW.** Touching one attribute on a 10 k-span batch should ideally clone one
  span, not the whole tree. Phase-2 work; out of scope here.
- **OTLP-arrow / columnar.** Different sharing model; orthogonal.
- **Persistent / immutable pdata.** Out of scope (see Non-goals).

## Validation plan

The local rig at
[`perf/baselines/2026-06-17-4ff09b609/`](../baselines/2026-06-17-4ff09b609/) is the source of
truth. Key bench rows that should move once `--feature-gates=pdata.cow` is set:

```
BenchmarkPipelineFanoutBatchedMetrics/mix=batched_plus_ro/shape=rich   328 503 → ~0 allocs/op
BenchmarkPipelineFanoutBatchedMetrics/mix=two_batched/shape=rich       328 503 → ~328 503 (1 detach)
BenchmarkReceiverFanoutMultiPipelineMetrics/shape=rich                  328 502 → ~0 allocs/op
BenchmarkMetricsFanout/n=4/mix=one_real_mut_rest_ro/shape=rich          ~5×CopyTo → 1×CopyTo
```

Symmetric `Traces` and `Logs` bench rows should move proportionally. A new
`BenchmarkProfilesFanout/n=4/mix=one_real_mut_rest_ro/shape=rich` row lands as part of O3.7
(the rig does not currently have profiles benches; the addition mirrors the existing three
signals).

`BenchmarkRealHelperMultiPipelineMetrics` already reads 11 allocs/op (post-O2) for the rich
shape; that floor is independent of COW.

## Appendix: terminology

- **Fanout (consumer).** The internal wrapper at `internal/fanoutconsumer/` that broadcasts a
  single incoming batch to multiple downstream consumers. Lives at the end of every multi-
  consumer pipeline, and at the receiver level when a receiver feeds multiple pipelines.
- **Detach.** The act of cloning a shared State + orig tree into an independent State + orig
  tree, owned by the mutating wrapper. After detach the mutating wrapper has refcount 1.
- **Handle.** The proposed heap-allocated `{orig, state}` pair that wrappers carry a pointer
  to (Alternative A).
- **Share.** A new wrapper that points at the same Handle (refcount-bumped) as the source —
  what `cow.Share(md)` returns.
