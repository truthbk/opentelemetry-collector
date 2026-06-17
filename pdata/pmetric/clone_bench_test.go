// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package pmetric

import (
	"fmt"
	"strconv"
	"testing"
)

// cloneBenchShape names a canonical benchmark batch shape.
type cloneBenchShape struct {
	name string
	gen  func() Metrics
}

// genSimpleMetrics creates a single ResourceMetrics × single ScopeMetrics
// container holding count IntGauge metrics with one data point each. Used
// for the small/medium/large shape sweep — quick to construct, exercises
// scaling along a single dimension.
func genSimpleMetrics(count int) Metrics {
	md := NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("service.name", "bench")
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName("bench-scope")
	sm.Metrics().EnsureCapacity(count)
	for i := range count {
		m := sm.Metrics().AppendEmpty()
		m.SetName("metric_" + strconv.Itoa(i))
		dp := m.SetEmptyGauge().DataPoints().AppendEmpty()
		dp.SetIntValue(int64(i))
		dp.Attributes().PutStr("idx", strconv.Itoa(i))
	}
	return md
}

// genRichMetrics builds a batch that exercises every nested container
// path: rmCount × smCount × 1 IntGauge with dpCount data points each
// carrying attrCount string attributes.
//
// The default 100×5×50×10 shape mirrors the example used in the
// "cost of a fanout clone" analysis (250k+ wrapper allocations per
// CopyTo); see docs/performance.md.
func genRichMetrics(rmCount, smCount, dpCount, attrCount int) Metrics {
	md := NewMetrics()
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
			m := sm.Metrics().AppendEmpty()
			m.SetName("benchmark.metric")
			gauge := m.SetEmptyGauge()
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

func cloneBenchShapes() []cloneBenchShape {
	return []cloneBenchShape{
		{"small_10", func() Metrics { return genSimpleMetrics(10) }},
		{"medium_1k", func() Metrics { return genSimpleMetrics(1000) }},
		{"large_10k", func() Metrics { return genSimpleMetrics(10000) }},
		{"rich_100x5x50x10", func() Metrics { return genRichMetrics(100, 5, 50, 10) }},
	}
}

// BenchmarkCopyToMetrics measures the raw cost of (Metrics).CopyTo across
// the canonical batch-shape grid. This is the lower bound for any
// "remove a clone" optimization upstream of the fanout consumer.
//
// allocs/op should track the total wrapper-struct count in the batch.
// The audit's quantitative claim ("~250k allocs/op for 100×5×50×10")
// is verified by the rich_100x5x50x10 subtest.
func BenchmarkCopyToMetrics(b *testing.B) {
	for _, shape := range cloneBenchShapes() {
		b.Run("shape="+shape.name, func(b *testing.B) {
			src := shape.gen()
			b.ReportAllocs()
			b.SetBytes(int64(src.DataPointCount()))
			b.ResetTimer()
			for b.Loop() {
				dst := NewMetrics()
				src.CopyTo(dst)
			}
		})
	}
}
