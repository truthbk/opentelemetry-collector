// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package fanoutconsumer

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/testdata"
)

// mutatingNopMetrics declares MutatesData: true but otherwise behaves like
// a no-op consumer. Used to assemble consumer mixes for the fanout
// benchmark without contaminating the measurement with sink-side overhead.
type mutatingNopMetrics struct{ consumer.Metrics }

func (mutatingNopMetrics) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

// metricsShape names a canonical benchmark batch shape with its generator.
// The generator must produce a fresh, mutable pmetric.Metrics each call.
type metricsShape struct {
	name string
	gen  func() pmetric.Metrics
}

// generateRichMetrics builds a batch designed to exercise every nested
// container path: rmCount × smCount × 1 IntGauge with dpCount data points
// each carrying attrCount string attributes.
//
// The default 100×5×50×10 shape mirrors the example used in the
// "cost of a fanout clone" analysis (250k+ wrapper allocations per
// CopyTo); see docs/performance.md.
func generateRichMetrics(rmCount, smCount, dpCount, attrCount int) pmetric.Metrics {
	md := pmetric.NewMetrics()
	md.ResourceMetrics().EnsureCapacity(rmCount)
	for r := range rmCount {
		rm := md.ResourceMetrics().AppendEmpty()
		attrs := rm.Resource().Attributes()
		attrs.PutStr("host.name", "host-"+strconv.Itoa(r))
		attrs.PutStr("service.name", "bench")
		rm.ScopeMetrics().EnsureCapacity(smCount)
		for s := range smCount {
			sm := rm.ScopeMetrics().AppendEmpty()
			sm.Scope().SetName("scope-" + strconv.Itoa(s))
			metric := sm.Metrics().AppendEmpty()
			metric.SetName("benchmark.metric")
			gauge := metric.SetEmptyGauge()
			gauge.DataPoints().EnsureCapacity(dpCount)
			for d := range dpCount {
				dp := gauge.DataPoints().AppendEmpty()
				dp.SetIntValue(int64(d))
				dpAttrs := dp.Attributes()
				for a := range attrCount {
					dpAttrs.PutStr("attr_"+strconv.Itoa(a),
						fmt.Sprintf("v-%d-%d-%d-%d", r, s, d, a))
				}
			}
		}
	}
	return md
}

func metricsShapes() []metricsShape {
	return []metricsShape{
		{"small_10", func() pmetric.Metrics { return testdata.GenerateMetrics(10) }},
		{"medium_1k", func() pmetric.Metrics { return testdata.GenerateMetrics(1000) }},
		{"large_10k", func() pmetric.Metrics { return testdata.GenerateMetrics(10000) }},
		{"rich_100x5x50x10", func() pmetric.Metrics { return generateRichMetrics(100, 5, 50, 10) }},
	}
}

// buildMetricsMix returns N consumer.Metrics split according to the named
// mix. The mix vocabulary is shared across all signal-specific benchmarks
// in this package.
func buildMetricsMix(name string, n int) []consumer.Metrics {
	cs := make([]consumer.Metrics, 0, n)
	for i := range n {
		var add consumer.Metrics
		switch name {
		case "all_mut":
			add = mutatingNopMetrics{Metrics: consumertest.NewNop()}
		case "all_ro":
			add = consumertest.NewNop()
		case "half":
			if i < n/2 {
				add = mutatingNopMetrics{Metrics: consumertest.NewNop()}
			} else {
				add = consumertest.NewNop()
			}
		case "one_mut_rest_ro":
			if i == 0 {
				add = mutatingNopMetrics{Metrics: consumertest.NewNop()}
			} else {
				add = consumertest.NewNop()
			}
		default:
			panic("unknown mix: " + name)
		}
		cs = append(cs, add)
	}
	return cs
}

// BenchmarkMetricsFanout measures the per-batch cost of
// fanoutconsumer.NewMetrics across a grid of (consumer count × mutator
// mix × batch shape). The dominant cost in cloning-heavy combinations
// is pmetric.Metrics.CopyTo (see BenchmarkCopyToMetrics in
// pdata/pmetric).
//
// Reads as: allocs/op scales with (N_mut − 1) × leaf_entities for the
// all-mutating cases.
func BenchmarkMetricsFanout(b *testing.B) {
	ns := []int{1, 2, 4, 8, 16}
	mixes := []string{"all_mut", "all_ro", "half", "one_mut_rest_ro"}
	shapes := metricsShapes()
	ctx := context.Background()

	for _, shape := range shapes {
		for _, mix := range mixes {
			for _, n := range ns {
				name := fmt.Sprintf("n=%d/mix=%s/shape=%s", n, mix, shape.name)
				b.Run(name, func(b *testing.B) {
					md := shape.gen()
					fanout := NewMetrics(buildMetricsMix(mix, n))
					b.ReportAllocs()
					b.SetBytes(int64(md.DataPointCount()))
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if err := fanout.ConsumeMetrics(ctx, md); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
