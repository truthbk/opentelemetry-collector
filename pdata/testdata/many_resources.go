// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package testdata // import "go.opentelemetry.io/collector/pdata/testdata"

import (
	"fmt"
	"strconv"

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// GenerateMetricsManyResources builds a pmetric.Metrics with
// rmCount resources × smCount scopes per resource × 1 IntGauge per scope
// × dpCount datapoints per gauge, each carrying attrCount string
// attributes. The default 100×5×50×10 shape produces ~25 000 leaf
// datapoints and ~250 000 leaf attributes — a representative "wide
// fanout × dense attributes" workload used by the perf rig to stress
// the fanout / pdata clone path. See docs/performance.md.
func GenerateMetricsManyResources(rmCount, smCount, dpCount, attrCount int) pmetric.Metrics {
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

// GenerateTracesManyResources is the ptrace.Traces analog of
// GenerateMetricsManyResources: rsCount × ssCount × spanCount with
// attrCount attributes per span.
func GenerateTracesManyResources(rsCount, ssCount, spanCount, attrCount int) ptrace.Traces {
	td := ptrace.NewTraces()
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

// GenerateLogsManyResources is the plog.Logs analog of
// GenerateMetricsManyResources: rlCount × slCount × recordCount with
// attrCount attributes per record.
func GenerateLogsManyResources(rlCount, slCount, recordCount, attrCount int) plog.Logs {
	ld := plog.NewLogs()
	ld.ResourceLogs().EnsureCapacity(rlCount)
	for r := range rlCount {
		rl := ld.ResourceLogs().AppendEmpty()
		attrs := rl.Resource().Attributes()
		attrs.PutStr("host.name", "host-"+strconv.Itoa(r))
		attrs.PutStr("service.name", "bench")
		rl.ScopeLogs().EnsureCapacity(slCount)
		for s := range slCount {
			sl := rl.ScopeLogs().AppendEmpty()
			sl.Scope().SetName("scope-" + strconv.Itoa(s))
			sl.LogRecords().EnsureCapacity(recordCount)
			for rec := range recordCount {
				lr := sl.LogRecords().AppendEmpty()
				lr.Body().SetStr("benchmark log line")
				recAttrs := lr.Attributes()
				for a := range attrCount {
					recAttrs.PutStr("attr_"+strconv.Itoa(a),
						fmt.Sprintf("v-%d-%d-%d-%d", r, s, rec, a))
				}
			}
		}
	}
	return ld
}
