// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package fanoutconsumer

import (
	"context"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/testdata"
	"go.opentelemetry.io/collector/pdata/xpdata/cow"
)

// conditionalMutatorMetrics simulates a processor that declares
// MutatesData=true but only mutates when a runtime predicate fires —
// e.g., filterprocessor with OTTL `where`, transformprocessor with a
// rarely-matching match expression, attributesprocessor with a skip
// expression. Production audit (see perf/rfc + the contrib processor
// scan) puts this pattern at ~79% of MutatesData=true components.
//
// hitEvery=N means "mutate every Nth ConsumeMetrics call". hitEvery=0
// means "never mutate". hitEvery=1 means "always mutate" (worst case
// for Alt A: matches today's eager-clone cost). counter is per-instance
// so each consumer in a fanout has its own hit-every counter.
type conditionalMutatorMetrics struct {
	hitEvery int
	counter  int
}

func (c *conditionalMutatorMetrics) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (c *conditionalMutatorMetrics) ConsumeMetrics(_ context.Context, md pmetric.Metrics) error {
	c.counter++
	if c.hitEvery == 0 || c.counter%c.hitEvery != 0 {
		// no-match path: do nothing. Under Alt A, no detach happens here,
		// so the share's backing tree stays pristine and the fanout
		// avoided paying any clone cost. Under today's semantics, the
		// fanout already paid the eager clone before reaching this
		// branch — wasted work.
		return nil
	}
	// hit path: a real mutator would call cow.DetachMetrics to obtain a
	// private copy before mutating. The actual mutation work below is
	// representative of "rebuild a slice header" — what a filter's
	// RemoveIf does.
	md = cow.DetachMetrics(md)
	rms := md.ResourceMetrics()
	if rms.Len() > 0 {
		rms.RemoveIf(func(_ pmetric.ResourceMetrics) bool { return false }) // touch the slice
	}
	return nil
}

// BenchmarkConditionalFanoutMetrics — pipeline-level bench: fanout to
// 4 consumers, 1 conditional mutator (varying hit rate) + 3 readonly
// sinks. Mirrors a multi-pipeline collector topology where the
// receiver feeds N pipelines, one of which carries a filter/transform
// processor with a WHERE clause.
//
// hitEvery sweep:
//   - 0   (never mutates) — Alt A drops all fanout clones to 0
//   - 10  (10% hit rate)  — typical low-match WHERE
//   - 5   (20%)
//   - 2   (50%)
//   - 1   (always)        — matches today's cost; ceiling for Alt A
//
// Compared to today's behavior (the fanout pays the clone eagerly
// regardless of hit rate), Alt A's clone cost scales with hit rate.
func BenchmarkConditionalFanoutMetrics(b *testing.B) {
	hitRates := []struct {
		name     string
		hitEvery int
	}{
		{"hit=0pct", 0},
		{"hit=10pct", 10},
		{"hit=20pct", 5},
		{"hit=50pct", 2},
		{"hit=100pct", 1},
	}
	for _, hr := range hitRates {
		b.Run(hr.name, func(b *testing.B) {
			b.Run("shape=rich_100x5x50x10", func(b *testing.B) {
				benchConditional(b, hr.hitEvery, func() pmetric.Metrics {
					return testdata.GenerateMetricsManyResources(100, 5, 50, 10)
				})
			})
		})
	}
}

func benchConditional(b *testing.B, hitEvery int, gen func() pmetric.Metrics) {
	b.Helper()
	consumers := []consumer.Metrics{
		&conditionalMutatorMetrics{hitEvery: hitEvery},
		consumertest.NewNop(),
		consumertest.NewNop(),
		consumertest.NewNop(),
	}
	fanout := NewMetrics(consumers)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		md := gen()
		if err := fanout.ConsumeMetrics(ctx, md); err != nil {
			b.Fatal(err)
		}
	}
}

// conditionalMutatorMetricsAutoDetach is the Phase-5 sibling of
// conditionalMutatorMetrics: same shape, same hit pattern, but it does
// NOT call cow.DetachMetrics explicitly. Under Phase 5's codegen-injected
// auto-detach prelude, mutating a cow.Share'd wrapper triggers detach
// transparently — the RemoveIf slice mutator's prelude (
// `state.DetachIfShared()` before `state.AssertMutable()`) fires inside
// when cowRefs > 0 and the per-signal detacher closure is installed.
// This is the bench that validates Phase 5 actually delivers the
// deferred-clone benefit without requiring processor authors to opt
// into explicit detach.
type conditionalMutatorMetricsAutoDetach struct {
	hitEvery int
	counter  int
}

func (c *conditionalMutatorMetricsAutoDetach) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (c *conditionalMutatorMetricsAutoDetach) ConsumeMetrics(_ context.Context, md pmetric.Metrics) error {
	c.counter++
	if c.hitEvery == 0 || c.counter%c.hitEvery != 0 {
		// no-match path: do nothing. Under Phase 5 gate ON, the share
		// stays intact; the fanout's eager-clone never fired (cow.Share
		// took its place). Zero clone cost paid on this iteration.
		return nil
	}
	// hit path: no explicit cow.DetachMetrics. The RemoveIf below
	// triggers state.DetachIfShared() inside its prelude, which
	// invokes the per-signal detacher closure installed by
	// cow.ShareMetrics; that deep-clones the source tree and rebinds
	// md's Handle to the new tree. Subsequent mutations write to the
	// private clone; source is untouched.
	rms := md.ResourceMetrics()
	if rms.Len() > 0 {
		rms.RemoveIf(func(_ pmetric.ResourceMetrics) bool { return false })
	}
	return nil
}

// BenchmarkConditionalFanoutMetricsAutoDetach — Phase-5 auto-detach
// variant of BenchmarkConditionalFanoutMetrics. Same 4-consumer fanout
// (1 conditional mutator + 3 readonly), same hit-rate sweep, same rich
// shape. The mutator here does NOT call cow.DetachMetrics explicitly;
// Phase 5's prelude inside RemoveIf is responsible for the rebind.
//
// Expected (gate ON):
//   - hit=0pct  — auto-detach never fires; ~zero clone cost (the
//     fanout's eager-clone is replaced by cheap cow.Share at dispatch)
//   - hit=100pct — auto-detach fires every call; cost equals one
//     full clone per call (matches the explicit-Detach variant's ceiling)
//
// Compared against BenchmarkConditionalFanoutMetrics (explicit Detach),
// this should produce essentially identical numbers — Phase 5 makes the
// explicit call redundant for slice-mutator paths.
func BenchmarkConditionalFanoutMetricsAutoDetach(b *testing.B) {
	hitRates := []struct {
		name     string
		hitEvery int
	}{
		{"hit=0pct", 0},
		{"hit=10pct", 10},
		{"hit=20pct", 5},
		{"hit=50pct", 2},
		{"hit=100pct", 1},
	}
	for _, hr := range hitRates {
		b.Run(hr.name, func(b *testing.B) {
			b.Run("shape=rich_100x5x50x10", func(b *testing.B) {
				benchConditionalAutoDetach(b, hr.hitEvery, func() pmetric.Metrics {
					return testdata.GenerateMetricsManyResources(100, 5, 50, 10)
				})
			})
		})
	}
}

func benchConditionalAutoDetach(b *testing.B, hitEvery int, gen func() pmetric.Metrics) {
	b.Helper()
	consumers := []consumer.Metrics{
		&conditionalMutatorMetricsAutoDetach{hitEvery: hitEvery},
		consumertest.NewNop(),
		consumertest.NewNop(),
		consumertest.NewNop(),
	}
	fanout := NewMetrics(consumers)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		md := gen()
		if err := fanout.ConsumeMetrics(ctx, md); err != nil {
			b.Fatal(err)
		}
	}
}
