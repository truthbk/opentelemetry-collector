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
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/testdata"
)

// mutatingNopTraces declares MutatesData: true but otherwise behaves like
// a no-op consumer. Used to assemble consumer mixes for the fanout
// benchmark without contaminating the measurement with sink-side overhead.
type mutatingNopTraces struct{ consumer.Traces }

func (mutatingNopTraces) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

type tracesShape struct {
	name string
	gen  func() ptrace.Traces
}

// generateRichTraces builds a batch designed to exercise every nested
// container path: rsCount × ssCount × spanCount with attrCount string
// attributes per span. Mirrors the "rich" metrics shape so the two
// signal benchmarks are comparable.
func generateRichTraces(rsCount, ssCount, spanCount, attrCount int) ptrace.Traces {
	td := ptrace.NewTraces()
	td.ResourceSpans().EnsureCapacity(rsCount)
	for r := 0; r < rsCount; r++ {
		rs := td.ResourceSpans().AppendEmpty()
		attrs := rs.Resource().Attributes()
		attrs.PutStr("host.name", "host-"+strconv.Itoa(r))
		attrs.PutStr("service.name", "bench")
		rs.ScopeSpans().EnsureCapacity(ssCount)
		for s := 0; s < ssCount; s++ {
			ss := rs.ScopeSpans().AppendEmpty()
			ss.Scope().SetName("scope-" + strconv.Itoa(s))
			ss.Spans().EnsureCapacity(spanCount)
			for sp := 0; sp < spanCount; sp++ {
				span := ss.Spans().AppendEmpty()
				span.SetName("benchmark.span")
				spanAttrs := span.Attributes()
				for a := 0; a < attrCount; a++ {
					spanAttrs.PutStr("attr_"+strconv.Itoa(a),
						fmt.Sprintf("v-%d-%d-%d-%d", r, s, sp, a))
				}
			}
		}
	}
	return td
}

func tracesShapes() []tracesShape {
	return []tracesShape{
		{"small_10", func() ptrace.Traces { return testdata.GenerateTraces(10) }},
		{"medium_1k", func() ptrace.Traces { return testdata.GenerateTraces(1000) }},
		{"large_10k", func() ptrace.Traces { return testdata.GenerateTraces(10000) }},
		{"rich_100x5x50x10", func() ptrace.Traces { return generateRichTraces(100, 5, 50, 10) }},
	}
}

func buildTracesMix(name string, n int) []consumer.Traces {
	cs := make([]consumer.Traces, 0, n)
	for i := 0; i < n; i++ {
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
					b.SetBytes(int64(td.SpanCount()))
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if err := fanout.ConsumeTraces(ctx, td); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
