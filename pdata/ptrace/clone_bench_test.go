// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ptrace

import (
	"fmt"
	"strconv"
	"testing"
)

type cloneBenchShape struct {
	name string
	gen  func() Traces
}

func genSimpleTraces(count int) Traces {
	td := NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "bench")
	ss := rs.ScopeSpans().AppendEmpty()
	ss.Scope().SetName("bench-scope")
	ss.Spans().EnsureCapacity(count)
	for i := range count {
		span := ss.Spans().AppendEmpty()
		span.SetName("span_" + strconv.Itoa(i))
		span.Attributes().PutStr("idx", strconv.Itoa(i))
	}
	return td
}

// genRichTraces builds a batch that exercises every nested container
// path: rsCount × ssCount × spanCount with attrCount string attributes
// per span. Mirrors the "rich" pmetric shape so the two signal benchmarks
// are comparable.
func genRichTraces(rsCount, ssCount, spanCount, attrCount int) Traces {
	td := NewTraces()
	td.ResourceSpans().EnsureCapacity(rsCount)
	for r := range rsCount {
		rs := td.ResourceSpans().AppendEmpty()
		attrs := rs.Resource().Attributes()
		attrs.PutStr("host.name", "host-"+strconv.Itoa(r))
		attrs.PutStr("service.name", "bench")
		rs.ScopeSpans().EnsureCapacity(ssCount)
		for s := range ssCount {
			ss := rs.ScopeSpans().AppendEmpty()
			ss.Scope().SetName("scope-" + strconv.Itoa(s))
			ss.Spans().EnsureCapacity(spanCount)
			for sp := range spanCount {
				span := ss.Spans().AppendEmpty()
				span.SetName("benchmark.span")
				spanAttrs := span.Attributes()
				for a := range attrCount {
					spanAttrs.PutStr("attr_"+strconv.Itoa(a),
						fmt.Sprintf("v-%d-%d-%d-%d", r, s, sp, a))
				}
			}
		}
	}
	return td
}

func cloneBenchShapes() []cloneBenchShape {
	return []cloneBenchShape{
		{"small_10", func() Traces { return genSimpleTraces(10) }},
		{"medium_1k", func() Traces { return genSimpleTraces(1000) }},
		{"large_10k", func() Traces { return genSimpleTraces(10000) }},
		{"rich_100x5x50x10", func() Traces { return genRichTraces(100, 5, 50, 10) }},
	}
}

// BenchmarkCopyToTraces — see pdata/pmetric/clone_bench_test.go for the
// grid and reading guide; the trace and metric clone benchmarks are
// symmetric.
func BenchmarkCopyToTraces(b *testing.B) {
	for _, shape := range cloneBenchShapes() {
		b.Run("shape="+shape.name, func(b *testing.B) {
			src := shape.gen()
			b.ReportAllocs()
			b.SetBytes(int64(src.SpanCount()))
			b.ResetTimer()
			for b.Loop() {
				dst := NewTraces()
				src.CopyTo(dst)
			}
		})
	}
}
