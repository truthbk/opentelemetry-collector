// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build cowproto_prototype

// Package cowprotobench hosts the apples-to-apples microbench that compares
// today's pdata wrappers against the cowproto Handle+index-path prototype.
// It lives in xpdata (which can import pmetric/testdata) rather than pdata
// itself (which cannot — pdata is the lowest layer).
//
// Build-tag gated: `go test -tags=cowproto_prototype ./pdata/xpdata/internal/cowprotobench/...`
//
// See pdata/internal/cowproto/doc.go for the prototype design.
package cowprotobench

import (
	"strconv"
	"testing"

	"go.opentelemetry.io/collector/pdata/internal"
	"go.opentelemetry.io/collector/pdata/internal/cowproto"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/testdata"
)

func pmetricInternals(md pmetric.Metrics) (*internal.ExportMetricsServiceRequest, *internal.State) {
	w := internal.MetricsWrapper(md)
	return internal.GetMetricsOrig(w), internal.GetMetricsState(w)
}

const (
	richRM   = 100
	richSM   = 5
	richDP   = 50
	richAttr = 10
)

func buildRichBacking(b *testing.B) (*internal.ExportMetricsServiceRequest, *internal.State, pmetric.Metrics) {
	b.Helper()
	md := testdata.GenerateMetricsManyResources(richRM, richSM, richDP, richAttr)
	orig, state := pmetricInternals(md)
	return orig, state, md
}

// BenchmarkBaselineReadAllAttributes — traverse the rich-shape tree via
// today's pmetric.Metrics wrappers; read every resource + datapoint
// attribute via Get. Read-only; no AssertMutable, no mutation.
func BenchmarkBaselineReadAllAttributes(b *testing.B) {
	_, _, md := buildRichBacking(b)

	b.ReportAllocs()
	for b.Loop() {
		rms := md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			rm := rms.At(i)
			rm.Resource().Attributes().Get("resource-attr-0")
			sms := rm.ScopeMetrics()
			for j := 0; j < sms.Len(); j++ {
				sm := sms.At(j)
				ms := sm.Metrics()
				for k := 0; k < ms.Len(); k++ {
					dps := ms.At(k).Gauge().DataPoints()
					for l := 0; l < dps.Len(); l++ {
						dps.At(l).Attributes().Get("label-0")
					}
				}
			}
		}
	}
}

// BenchmarkCowprotoReadAllAttributes — same traversal via the cowproto
// prototype wrappers wrapping the SAME backing tree.
func BenchmarkCowprotoReadAllAttributes(b *testing.B) {
	orig, state, _ := buildRichBacking(b)
	m := cowproto.FromInternal(orig, state)

	b.ReportAllocs()
	for b.Loop() {
		rms := m.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			rm := rms.At(i)
			rm.Resource().Attributes().Get("resource-attr-0")
			sms := rm.ScopeMetrics()
			for j := 0; j < sms.Len(); j++ {
				sm := sms.At(j)
				ms := sm.Metrics()
				for k := 0; k < ms.Len(); k++ {
					dps := ms.At(k).GaugeDataPoints()
					for l := 0; l < dps.Len(); l++ {
						dps.At(l).Attributes().Get("label-0")
					}
				}
			}
		}
	}
}

// BenchmarkBaselineMutateAllAttributes — PutStr on every resource +
// datapoint attribute via today's wrappers. Data is mutable; no detach.
func BenchmarkBaselineMutateAllAttributes(b *testing.B) {
	_, _, md := buildRichBacking(b)

	b.ReportAllocs()
	for b.Loop() {
		rms := md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			rm := rms.At(i)
			rm.Resource().Attributes().PutStr("bench-key", "bench-value-"+strconv.Itoa(i))
			sms := rm.ScopeMetrics()
			for j := 0; j < sms.Len(); j++ {
				sm := sms.At(j)
				ms := sm.Metrics()
				for k := 0; k < ms.Len(); k++ {
					dps := ms.At(k).Gauge().DataPoints()
					for l := 0; l < dps.Len(); l++ {
						dps.At(l).Attributes().PutStr("bench-key", "v")
					}
				}
			}
		}
	}
}

// BenchmarkCowprotoMutateAllAttributes — same mutation pattern via the
// cowproto prototype. AssertMutable passes; no detach fired (state was
// never marked readonly).
func BenchmarkCowprotoMutateAllAttributes(b *testing.B) {
	orig, state, _ := buildRichBacking(b)
	m := cowproto.FromInternal(orig, state)

	b.ReportAllocs()
	for b.Loop() {
		rms := m.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			rm := rms.At(i)
			rm.Resource().Attributes().PutStr("bench-key", "bench-value-"+strconv.Itoa(i))
			sms := rm.ScopeMetrics()
			for j := 0; j < sms.Len(); j++ {
				sm := sms.At(j)
				ms := sm.Metrics()
				for k := 0; k < ms.Len(); k++ {
					dps := ms.At(k).GaugeDataPoints()
					for l := 0; l < dps.Len(); l++ {
						dps.At(l).Attributes().PutStr("bench-key", "v")
					}
				}
			}
		}
	}
}

// BenchmarkBaselineMetricsUsage mirrors pdata/pmetric.BenchmarkMetricsUsage
// on the rich shape: read + mutate + read on attributes, SetIntValue on
// every datapoint. Typical "processor touches a few attributes" pattern.
func BenchmarkBaselineMetricsUsage(b *testing.B) {
	_, _, md := buildRichBacking(b)

	b.ReportAllocs()
	for b.Loop() {
		rms := md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			rm := rms.At(i)
			res := rm.Resource()
			res.Attributes().PutStr("foo", "bar")
			v, _ := res.Attributes().Get("foo")
			_ = v.Str()
			v.SetStr("new-bar")
			res.Attributes().Remove("foo")
			sms := rm.ScopeMetrics()
			for j := 0; j < sms.Len(); j++ {
				sm := sms.At(j)
				ms := sm.Metrics()
				for k := 0; k < ms.Len(); k++ {
					dps := ms.At(k).Gauge().DataPoints()
					for l := 0; l < dps.Len(); l++ {
						dps.At(l).SetIntValue(int64(l))
					}
				}
			}
		}
	}
}

// BenchmarkBaselineReadResourceAttrsOnly — exercises only the
// shallow-path leaf (Resource.Attributes), avoiding the 4-level
// datapoint chain that includes an interface type assertion.
// Isolates the per-level path-walk cost from the type-assertion cost.
func BenchmarkBaselineReadResourceAttrsOnly(b *testing.B) {
	_, _, md := buildRichBacking(b)

	b.ReportAllocs()
	for b.Loop() {
		rms := md.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			rms.At(i).Resource().Attributes().Get("resource-attr-0")
		}
	}
}

func BenchmarkCowprotoReadResourceAttrsOnly(b *testing.B) {
	orig, state, _ := buildRichBacking(b)
	m := cowproto.FromInternal(orig, state)

	b.ReportAllocs()
	for b.Loop() {
		rms := m.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			rms.At(i).Resource().Attributes().Get("resource-attr-0")
		}
	}
}

// BenchmarkCowprotoMetricsUsage — same usage pattern via cowproto. The
// "Remove" step is dropped (the prototype's leaf Map exposes Put/Get only;
// the access counts dominate timing).
func BenchmarkCowprotoMetricsUsage(b *testing.B) {
	orig, state, _ := buildRichBacking(b)
	m := cowproto.FromInternal(orig, state)

	b.ReportAllocs()
	for b.Loop() {
		rms := m.ResourceMetrics()
		for i := 0; i < rms.Len(); i++ {
			rm := rms.At(i)
			res := rm.Resource()
			res.Attributes().PutStr("foo", "bar")
			res.Attributes().Get("foo")
			res.Attributes().PutStr("foo", "new-bar")
			sms := rm.ScopeMetrics()
			for j := 0; j < sms.Len(); j++ {
				sm := sms.At(j)
				ms := sm.Metrics()
				for k := 0; k < ms.Len(); k++ {
					dps := ms.At(k).GaugeDataPoints()
					for l := 0; l < dps.Len(); l++ {
						dps.At(l).SetIntValue(int64(l))
					}
				}
			}
		}
	}
}
