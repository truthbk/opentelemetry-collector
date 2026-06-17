# Integrating a processor with `pdata.cow`

This is the canonical guide for processor authors who want their
processor to benefit from the `pdata.cow` deferred-clone optimization
(see [`perf/rfc/pdata-cow.md`](../../../perf/rfc/pdata-cow.md) for the
design + the benchmark evidence in commit `bac37790c`).

## When to integrate

Your processor is a candidate if **both** are true:

1. It declares `consumer.Capabilities{MutatesData: true}`.
2. Its `ConsumeXxx` mutation is **conditional** — i.e. there is at least
   one runtime branch where the data is NOT mutated. Examples:
   - OTTL `where` clause that doesn't match → no mutation.
   - `include`/`exclude` filter that doesn't match → no mutation.
   - Metric-name lookup miss → no mutation.
   - Data-shape predicate (e.g. "only act on cumulative metrics") that
     fails → no mutation.

Static analysis of in-tree + contrib processors classifies **~79% of
`MutatesData: true` components as conditional** (audit on 2026-06-18).
If you're not sure your processor qualifies, the question to ask is:
"For some plausible input, does my `ConsumeXxx` return without calling
any mutating pdata method?"

Processors that **always** mutate (batchprocessor, groupbyattrs,
groupbytrace, logdedup, logstransform) should NOT opt in — they pay
the clone cost on every batch anyway, and the gate-on path would just
add wrapper-construction overhead with no win.

## The pattern

Single rule: **before any mutation, call `cow.DetachX`. Skip it on the
no-mutation branch.**

```go
import "go.opentelemetry.io/collector/pdata/xpdata/cow"

func (p *myProcessor) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
    // 1. Evaluate the predicate(s) that decide whether to mutate.
    //    DO NOT call cow.DetachMetrics yet — that would pay the clone
    //    cost on every batch, which is exactly what we're trying to
    //    avoid.
    rms := md.ResourceMetrics()
    matched := false
    for i := 0; i < rms.Len(); i++ {
        if p.predicate.Matches(rms.At(i)) {
            matched = true
            break
        }
    }

    // 2. If nothing matched, pass through unmodified. This is the
    //    fast path — under pdata.cow, the fanout shared the data
    //    with this consumer instead of cloning, and no detach happens
    //    now, so the share's allocation cost was zero.
    if !matched {
        return p.next.ConsumeMetrics(ctx, md)
    }

    // 3. Something matched — we're going to mutate. Detach first to
    //    obtain an independent wrapper backed by a private clone.
    //    Under gate-off this is a no-op (returns md unchanged); under
    //    gate-on with a shared wrapper this deep-clones and resets
    //    cowRefs.
    md = cow.DetachMetrics(md)

    // 4. Mutate freely on the (possibly cloned) wrapper.
    rms = md.ResourceMetrics()
    rms.RemoveIf(func(rm pmetric.ResourceMetrics) bool {
        return p.predicate.Matches(rm)
    })

    return p.next.ConsumeMetrics(ctx, md)
}
```

Same shape for traces (`cow.DetachTraces`), logs (`cow.DetachLogs`),
profiles (`cow.DetachProfiles`).

## Important contract: predicate evaluation must not mutate

If your predicate evaluation itself calls any mutating pdata method
(e.g. `Map.PutStr` to cache a derived attribute, `RemoveIf` on a
sub-slice to peek at structure), the safety net in
`internal.State.AssertMutable` will panic under the gate. The fix is
either:

- Make the predicate purely read-only — use `Get` / `Range` /
  iteration without mutation.
- Detach earlier, before predicate evaluation. (Loses the deferred-
  clone benefit for this processor, but stays safe.)

If you can't make the predicate read-only and you can't afford to
detach early, don't opt in — your processor isn't a good fit for the
cow optimization.

## Testing

Add a test that exercises both branches under the gate:

```go
func TestMyProcessor_NoMatchBranchDoesNotMutate(t *testing.T) {
    require.NoError(t,
        featuregate.GlobalRegistry().Set("pdata.cow", true))
    defer featuregate.GlobalRegistry().Set("pdata.cow", false)

    // Construct an input that DOES NOT match your predicate.
    md := pmetric.NewMetrics()
    /* ... populate ... */
    shared := cow.ShareMetrics(md)
    require.True(t, cow.IsSharedMetrics(shared))

    err := p.ConsumeMetrics(ctx, shared)
    require.NoError(t, err)

    // Share should still be shared — no detach happened.
    assert.True(t, cow.IsSharedMetrics(shared),
        "no-match branch must not call DetachMetrics")
}

func TestMyProcessor_MatchBranchDetaches(t *testing.T) {
    require.NoError(t,
        featuregate.GlobalRegistry().Set("pdata.cow", true))
    defer featuregate.GlobalRegistry().Set("pdata.cow", false)

    md := pmetric.NewMetrics()
    /* ... populate so predicate matches ... */
    shared := cow.ShareMetrics(md)

    // The processor must NOT panic — meaning it called Detach before
    // any mutating method.
    require.NotPanics(t, func() {
        p.ConsumeMetrics(ctx, shared)
    })
}
```

The second test catches the contract violation pattern most often
introduced in maintenance: a code change that adds a mutating call to
a previously-conditional path without adding the corresponding
`cow.DetachX`. Under the gate, that call would `panic` at runtime; the
test surfaces it in CI.

## The safety net

If you forget the `cow.DetachX` call, `internal.State.AssertMutable`
panics with:

```
invalid access to cow-shared data: caller must call cow.Detach* before mutating (see pdata/xpdata/cow)
```

This is intentional — silent corruption of the source's backing tree
would be a much worse failure mode. The panic is gated on
`cowRefs > 0`, so the gate-off path (which has cowRefs always 0)
behaves identically to today.

## Performance: when this actually wins

Pipeline-level measurement on the rich shape
(100 resources × 5 scopes × 50 datapoints × 10 attributes) in a 4-
consumer fanout (1 conditional + 3 readonly):

| Predicate hit rate | Allocs saved per batch | % of total pipeline |
|---|---|---|
| 0% (never matches) | 328,504 | 26% |
| 10% | 308,278 | 24% |
| 20% | 280,371 | 22% |
| 50% | 168,464 | 13% |
| 100% (always matches) | 0 | 0% |

Linear scaling: `saved ≈ (1 − hit_rate) × one_clone_cost`. Workloads
with low-match WHERE clauses (the typical filter / transform pattern)
get the bulk of the benefit. Workloads with always-matching
predicates get no benefit but no regression either — the explicit
Detach in the mutation branch matches today's eager clone cost.
