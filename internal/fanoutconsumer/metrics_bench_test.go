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
)

// mutatingNopMetrics declares MutatesData: true but otherwise behaves
// like a no-op consumer — it does NOT actually mutate the input. The
// benchmarks use it to assemble consumer mixes and exercise the fanout
// consumer's capability-flag routing (mutating vs read-only branches)
// without contaminating ns/op with sink-side overhead. A future
// "mutator must touch the input" assertion in fanoutconsumer would not
// fire here; that is by design — the benchmarks measure orchestration,
// not contract compliance.
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

func metricsShapes() []metricsShape {
	return []metricsShape{
		{"small_10", func() pmetric.Metrics { return testdata.GenerateMetrics(10) }},
		{"medium_1k", func() pmetric.Metrics { return testdata.GenerateMetrics(1000) }},
		{"large_10k", func() pmetric.Metrics { return testdata.GenerateMetrics(10000) }},
		{"rich_100x5x50x10", func() pmetric.Metrics { return testdata.GenerateMetricsManyResources(100, 5, 50, 10) }},
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
					// SetBytes is reported by `go test -bench` as MB/s.
					// We pass datapoint count, so the column reads as
					// datapoints/sec * 1e-6 — items/op, not bytes/op.
					b.SetBytes(int64(md.DataPointCount()))
					for b.Loop() {
						if err := fanout.ConsumeMetrics(ctx, md); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
