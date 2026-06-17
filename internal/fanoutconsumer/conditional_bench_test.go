// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package fanoutconsumer

import (
	"context"
	"fmt"
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
// Compared to today's behaviour (the fanout pays the clone eagerly
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

// guard against unused-import linting when the file is built outside
// a bench run.
var _ = fmt.Sprintf
