# perf/ — local engagement glossary

This file is the canonical glossary for the local performance-rig +
optimization engagement under `perf/`. Definitions resolved here are
binding for `perf/rfc/`, `perf/baselines/`, and the rig benchmarks
under `internal/fanoutconsumer/`, `pdata/`, `service/internal/graph/`,
and `cmd/perftestbed/`.

Implementation lives in the code, not here. This file is a glossary.

---

**Fanout (consumer).** The internal wrapper at
[`internal/fanoutconsumer/`](../internal/fanoutconsumer/) that broadcasts a single incoming
batch to multiple downstream consumers. Lives at the end of every multi-consumer pipeline,
and at the receiver level when a receiver feeds multiple pipelines via
[`service/internal/graph/receiver.go`](../service/internal/graph/receiver.go).

**Share.** A new pdata wrapper that points at the same underlying State + orig as the source.
Created by `cow.Share(md)` (function in [`pdata/xpdata/cow/`](../pdata/xpdata/cow/), mirroring
the existing [`pdata/xpdata/pref/`](../pdata/xpdata/pref/) precedent); bumps the source's
`cowRefs`. Sharing is *internal* — the fanout creates shares to dispatch one input batch to
multiple downstream consumers without an eager deep-clone.

**Detach.** Materialise an independent copy of the State + orig tree for a Share that is
about to be mutated. Deep-clones the root proto, allocates a fresh `*State` (`cowRefs: 0`,
inheriting the readonly bit only if appropriate), and rebinds the mutating wrapper's Handle
to point at the new pair. The source State's `cowRefs` is decremented as the Share leaves.

**Handle.** The heap-allocated `{orig, state}` pair that pdata wrappers carry a pointer to
under the RFC's recommended Alternative A. Today wrappers carry `(orig, state)` as inline
fields; the Handle indirection makes the pair mutable in-place so detach can rebind it
without losing the reference held by the calling wrapper.

**Release.** Decrement `cowRefs` to mark a Share as no longer active. **Explicit, paired with
`ShareTo`** — the fanout calls `defer shared.Release()` immediately after `ShareTo`. The
top-level wrapper exposes `Release` as a public method for symmetry but the only documented
caller is the fanout. No-op if the Share was already detached.

**`cowRefs`.** Atomic int32 on `*State`, distinct from the existing `refs` (which `pref` owns
for pipeline-ownership lifecycle). Incremented by `ShareTo`, decremented by detach + Release.
Detach fires when `cowRefs > 0` AND the data is not read-only.

**`refs`.** Atomic int32 on `*State`, owned by `pref.Ref/Unref` (gated on the existing
`pdata.enableRefCounting` feature gate). Counts pipeline-ownership references, not COW
shares.

**`stateReadOnlyBit`.** The existing bit in `State.state` set by `MarkReadOnly()` when the
fanout broadcasts to multiple read-only consumers. `detachIfShared` short-circuits when set
— a mutation under a readonly state is a contract violation; AssertMutable's panic stays.

**`generation`.** Atomic uint32 on `*State`, incremented by detach. Captured on nested
wrappers at creation time. Under `pdatacowdebug` build tag, nested wrapper accessors compare
captured vs current generation and panic on mismatch — surfaces the "captured nested
wrapper held across a mutation" contract violation during development. Release builds skip
the check (zero overhead).

**Rig.** The local performance-measurement infrastructure: microbenchmarks in
[`internal/fanoutconsumer/`](../internal/fanoutconsumer/) and
[`pdata/{pmetric,ptrace,plog}/`](../pdata/), pipeline benches in
[`service/internal/graph/`](../service/internal/graph/), the standalone
[`cmd/perftestbed/`](../cmd/perftestbed/) binary, and the captured baselines in
[`baselines/`](baselines/).

**Baseline.** A captured run of the rig's bench suite plus metadata, stored under
[`baselines/YYYY-MM-DD-<shortsha>/`](baselines/). Used as the reference point for
`benchstat`-driven before/after comparisons in optimization PRs.
