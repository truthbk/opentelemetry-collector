// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package fanoutconsumer

import (
	"context"
	"fmt"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/testdata"
)

// mutatingNopTraces — see mutatingNopMetrics for the design notes
// (declares MutatesData: true, does not actually mutate the input).
type mutatingNopTraces struct{ consumer.Traces }

func (mutatingNopTraces) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

type tracesShape struct {
	name string
	gen  func() ptrace.Traces
}

func tracesShapes() []tracesShape {
	return []tracesShape{
		{"small_10", func() ptrace.Traces { return testdata.GenerateTraces(10) }},
		{"medium_1k", func() ptrace.Traces { return testdata.GenerateTraces(1000) }},
		{"large_10k", func() ptrace.Traces { return testdata.GenerateTraces(10000) }},
		{"rich_100x5x50x10", func() ptrace.Traces { return testdata.GenerateTracesManyResources(100, 5, 50, 10) }},
	}
}

func buildTracesMix(name string, n int) []consumer.Traces {
	cs := make([]consumer.Traces, 0, n)
	for i := range n {
		var add consumer.Traces
		switch name {
		case "all_mut":
			add = mutatingNopTraces{Traces: consumertest.NewNop()}
		case "all_ro":
			add = consumertest.NewNop()
		case "half":
			if i < n/2 {
				add = mutatingNopTraces{Traces: consumertest.NewNop()}
			} else {
				add = consumertest.NewNop()
			}
		case "one_mut_rest_ro":
			if i == 0 {
				add = mutatingNopTraces{Traces: consumertest.NewNop()}
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

// BenchmarkTracesFanout — see BenchmarkMetricsFanout for the grid and
// reading guide; the trace and metric benchmarks are symmetric.
func BenchmarkTracesFanout(b *testing.B) {
	ns := []int{1, 2, 4, 8, 16}
	mixes := []string{"all_mut", "all_ro", "half", "one_mut_rest_ro"}
	shapes := tracesShapes()
	ctx := context.Background()

	for _, shape := range shapes {
		for _, mix := range mixes {
			for _, n := range ns {
				name := fmt.Sprintf("n=%d/mix=%s/shape=%s", n, mix, shape.name)
				b.Run(name, func(b *testing.B) {
					td := shape.gen()
					fanout := NewTraces(buildTracesMix(mix, n))
					b.ReportAllocs()
					b.SetBytes(int64(td.SpanCount())) // items/op, see BenchmarkMetricsFanout
					for b.Loop() {
						if err := fanout.ConsumeTraces(ctx, td); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
