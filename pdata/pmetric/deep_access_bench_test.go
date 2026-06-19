// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package pmetric

import (
	"testing"
)

// BenchmarkDeepAccessMetrics measures the cost of read-only deep
// traversal: RM[*].SM[*].M[*].DP[*].Attributes() on the rich shape
// (~25 000 leaf datapoints, ~250 000 leaf attribute reads). Models
// batchprocessor / attributesprocessor / metricstransformprocessor
// patterns that walk every datapoint's attributes for matching,
// counting, or serialization — the "unconditional ~21% of
// MutatesData: true components" from the contrib processor audit.
//
// Phase 6 cache-decision criterion (committed in
// perf/rfc/pdata-cow-handoff.md): under the new Handle-based layout
// (Phase 4), every getOrig() call walks `h.GetOrig().ResourceMetrics[
// rmIdx].ScopeMetrics[smIdx].Metrics[mIdx]` etc. instead of dereferencing
// the previous direct `ms.orig` pointer. This bench measures that
// index-walk tax on the worst-case (4-level deep) access pattern.
//
// Threshold for adding the deferred generation-cache optimization
// (shelved during Phase 4 step 2b.i):
//   - <5%   slowdown vs pre-Path-Y baseline → ship without cache
//   - 5-10% → add cache back IF template diff <100 LOC
//   - >10%  → must add cache back regardless
//
// Comparison against the pre-Path-Y baseline requires running the same
// bench on `o2/cow-aware-batcher` and benchstat'ing the two outputs.
// This bench file is added on path-y; a sibling cherry-pick to o2 (or
// a worktree) is what closes the % comparison.
func BenchmarkDeepAccessMetrics(b *testing.B) {
	md := genRichMetrics(100, 5, 50, 10)
	b.ReportAllocs()
	b.SetBytes(int64(md.DataPointCount()))
	for b.Loop() {
		var total int
		rms := md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			sms := rms.At(i).ScopeMetrics()
			for j := 0; j < sms.Len(); j++ {
				ms := sms.At(j).Metrics()
				for k := 0; k < ms.Len(); k++ {
					m := ms.At(k)
					var dps NumberDataPointSlice
					switch m.Type() {
					case MetricTypeGauge:
						dps = m.Gauge().DataPoints()
					case MetricTypeSum:
						dps = m.Sum().DataPoints()
					default:
						continue
					}
					for l := 0; l < dps.Len(); l++ {
						total += dps.At(l).Attributes().Len()
					}
				}
			}
		}
		_ = total
	}
}
