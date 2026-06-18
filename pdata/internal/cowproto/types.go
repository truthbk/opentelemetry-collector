// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cowproto // import "go.opentelemetry.io/collector/pdata/internal/cowproto"

import (
	"go.opentelemetry.io/collector/pdata/internal"
)

// Handle is the single heap-allocated indirection carried by the top-level
// wrapper. A detach replaces both fields in place; all wrappers descending
// from this Handle see the rebinding on their next access via the indices
// they carry.
type Handle struct {
	orig  *internal.ExportMetricsServiceRequest
	state *internal.State
}

// Metrics is the top-level wrapper, the only one that holds the Handle
// directly.
type Metrics struct {
	h *Handle
}

// NewMetrics returns a Metrics wrapping a fresh ExportMetricsServiceRequest
// and State.
func NewMetrics() Metrics {
	return Metrics{h: &Handle{
		orig:  &internal.ExportMetricsServiceRequest{},
		state: internal.NewState(),
	}}
}

// FromInternal wraps an existing internal request + state pair, used by the
// bench to wrap the SAME backing tree as today's pmetric.Metrics so the two
// designs can be compared apples-to-apples.
func FromInternal(orig *internal.ExportMetricsServiceRequest, state *internal.State) Metrics {
	return Metrics{h: &Handle{orig: orig, state: state}}
}

// ResourceMetrics returns the top-level slice wrapper.
func (m Metrics) ResourceMetrics() ResourceMetricsSlice {
	return ResourceMetricsSlice{h: m.h}
}

// ResourceMetricsSlice is value-typed; it carries only the top-level
// Handle. Slice operations resolve against h.orig.ResourceMetrics.
type ResourceMetricsSlice struct {
	h *Handle
}

func (s ResourceMetricsSlice) Len() int {
	return len(s.h.orig.ResourceMetrics)
}

func (s ResourceMetricsSlice) At(i int) ResourceMetrics {
	return ResourceMetrics{h: s.h, rmIdx: i}
}

// ResourceMetrics is value-typed; the (h, rmIdx) tuple is its identity.
type ResourceMetrics struct {
	h     *Handle
	rmIdx int
}

func (rm ResourceMetrics) Resource() Resource {
	return Resource{h: rm.h, rmIdx: rm.rmIdx}
}

func (rm ResourceMetrics) ScopeMetrics() ScopeMetricsSlice {
	return ScopeMetricsSlice{h: rm.h, rmIdx: rm.rmIdx}
}

// Resource is value-typed; resolves its orig via h + rmIdx.
type Resource struct {
	h     *Handle
	rmIdx int
}

func (r Resource) getOrig() *internal.Resource {
	return &r.h.orig.ResourceMetrics[r.rmIdx].Resource
}

func (r Resource) Attributes() ResourceAttrMap {
	return ResourceAttrMap{h: r.h, rmIdx: r.rmIdx}
}

// ResourceAttrMap is a leaf wrapper whose orig is the Attributes slice of
// the resource at h.orig.ResourceMetrics[rmIdx].Resource.
//
// Every call to getOrig() walks h.orig → ResourceMetrics[rmIdx] → Resource
// → &Attributes. After a detach this resolves through the NEW tree.
type ResourceAttrMap struct {
	h     *Handle
	rmIdx int
}

func (m ResourceAttrMap) getOrig() *[]internal.KeyValue {
	return &m.h.orig.ResourceMetrics[m.rmIdx].Resource.Attributes
}

// Len returns the number of key-value pairs in the map.
func (m ResourceAttrMap) Len() int {
	return len(*m.getOrig())
}

// Get returns the Value associated with the key. Resolves the leaf orig
// once via getOrig() and re-uses it through the loop — under the cow
// design, the inner-loop access cost dominates if the path walk is
// repeated per iteration. The baseline's Map.Get gets away with a
// per-iteration call because baseline getOrig is a trivial deref.
func (m ResourceAttrMap) Get(key string) (string, bool) {
	orig := *m.getOrig()
	for i := range orig {
		if orig[i].Key == key {
			if sv, ok := orig[i].Value.Value.(*internal.AnyValue_StringValue); ok {
				return sv.StringValue, true
			}
			return "", true
		}
	}
	return "", false
}

// PutStr inserts or updates a string-valued entry.
func (m ResourceAttrMap) PutStr(key, value string) {
	m.h.state.AssertMutable()
	origPtr := m.getOrig()
	orig := *origPtr
	for i := range orig {
		if orig[i].Key == key {
			orig[i].Value.Value = &internal.AnyValue_StringValue{StringValue: value}
			return
		}
	}
	*origPtr = append(orig, internal.KeyValue{
		Key:   key,
		Value: internal.AnyValue{Value: &internal.AnyValue_StringValue{StringValue: value}},
	})
}

// ScopeMetricsSlice — value-typed, parent (rmIdx) carried through.
type ScopeMetricsSlice struct {
	h     *Handle
	rmIdx int
}

func (s ScopeMetricsSlice) Len() int {
	return len(s.h.orig.ResourceMetrics[s.rmIdx].ScopeMetrics)
}

func (s ScopeMetricsSlice) At(i int) ScopeMetrics {
	return ScopeMetrics{h: s.h, rmIdx: s.rmIdx, smIdx: i}
}

type ScopeMetrics struct {
	h            *Handle
	rmIdx, smIdx int
}

func (sm ScopeMetrics) Metrics() MetricSlice {
	return MetricSlice{h: sm.h, rmIdx: sm.rmIdx, smIdx: sm.smIdx}
}

type MetricSlice struct {
	h            *Handle
	rmIdx, smIdx int
}

func (s MetricSlice) Len() int {
	return len(s.h.orig.ResourceMetrics[s.rmIdx].ScopeMetrics[s.smIdx].Metrics)
}

func (s MetricSlice) At(i int) Metric {
	return Metric{h: s.h, rmIdx: s.rmIdx, smIdx: s.smIdx, mIdx: i}
}

type Metric struct {
	h                  *Handle
	rmIdx, smIdx, mIdx int
}

// GaugeDataPoints resolves the Gauge pointer once at slice-wrapper
// construction and caches it on the slice wrapper alongside the State's
// generation at capture time. Subsequent At()/Len() calls compare the
// cached generation against State.Generation() and either reuse the
// cached pointer (fast path) or re-derive (slow path, after a detach).
//
// This caching scheme is what saves the deep-path access from paying the
// interface type assertion + 3 slice indexes + 4 field accesses on every
// access. In the codegen world the equivalent caching can live on the
// generated slice/datapoint structs.
func (m Metric) GaugeDataPoints() NumberDataPointSlice {
	metric := m.h.orig.ResourceMetrics[m.rmIdx].ScopeMetrics[m.smIdx].Metrics[m.mIdx]
	return NumberDataPointSlice{
		h:           m.h,
		rmIdx:       m.rmIdx,
		smIdx:       m.smIdx,
		mIdx:        m.mIdx,
		cachedGauge: metric.Data.(*internal.Metric_Gauge).Gauge,
		gen:         m.h.state.Generation(),
	}
}

type NumberDataPointSlice struct {
	h                  *Handle
	cachedGauge        *internal.Gauge
	rmIdx, smIdx, mIdx int
	gen                uint32
}

func (s NumberDataPointSlice) gaugeOrig() *internal.Gauge {
	if s.h.state.Generation() == s.gen {
		return s.cachedGauge
	}
	// Stale after detach: re-derive via the full path walk.
	metric := s.h.orig.ResourceMetrics[s.rmIdx].ScopeMetrics[s.smIdx].Metrics[s.mIdx]
	return metric.Data.(*internal.Metric_Gauge).Gauge
}

func (s NumberDataPointSlice) Len() int {
	return len(s.gaugeOrig().DataPoints)
}

func (s NumberDataPointSlice) At(i int) NumberDataPoint {
	return NumberDataPoint{
		h:           s.h,
		rmIdx:       s.rmIdx,
		smIdx:       s.smIdx,
		mIdx:        s.mIdx,
		dpIdx:       i,
		cachedGauge: s.gaugeOrig(),
		gen:         s.gen,
	}
}

type NumberDataPoint struct {
	h                         *Handle
	cachedGauge               *internal.Gauge
	rmIdx, smIdx, mIdx, dpIdx int
	gen                       uint32
}

func (dp NumberDataPoint) getOrig() *internal.NumberDataPoint {
	if dp.h.state.Generation() == dp.gen {
		return dp.cachedGauge.DataPoints[dp.dpIdx]
	}
	// Stale: re-derive.
	gauge := dp.h.orig.ResourceMetrics[dp.rmIdx].ScopeMetrics[dp.smIdx].Metrics[dp.mIdx].Data.(*internal.Metric_Gauge).Gauge
	return gauge.DataPoints[dp.dpIdx]
}

func (dp NumberDataPoint) IntValue() int64 {
	if v, ok := dp.getOrig().Value.(*internal.NumberDataPoint_AsInt); ok {
		return v.AsInt
	}
	return 0
}

func (dp NumberDataPoint) SetIntValue(v int64) {
	dp.h.state.AssertMutable()
	dp.getOrig().Value = &internal.NumberDataPoint_AsInt{AsInt: v}
}

func (dp NumberDataPoint) Attributes() DataPointAttrMap {
	return DataPointAttrMap{
		h:           dp.h,
		rmIdx:       dp.rmIdx,
		smIdx:       dp.smIdx,
		mIdx:        dp.mIdx,
		dpIdx:       dp.dpIdx,
		cachedGauge: dp.cachedGauge,
		gen:         dp.gen,
	}
}

// DataPointAttrMap is the leaf Map for a NumberDataPoint's Attributes.
// Inherits the cachedGauge + generation from the parent NumberDataPoint,
// so getOrig() avoids the 3 slice indexes + interface assertion on the
// fast path. Stale generation falls back to the full re-derive.
type DataPointAttrMap struct {
	h                         *Handle
	cachedGauge               *internal.Gauge
	rmIdx, smIdx, mIdx, dpIdx int
	gen                       uint32
}

func (m DataPointAttrMap) getOrig() *[]internal.KeyValue {
	if m.h.state.Generation() == m.gen {
		return &m.cachedGauge.DataPoints[m.dpIdx].Attributes
	}
	gauge := m.h.orig.ResourceMetrics[m.rmIdx].ScopeMetrics[m.smIdx].Metrics[m.mIdx].Data.(*internal.Metric_Gauge).Gauge
	return &gauge.DataPoints[m.dpIdx].Attributes
}

func (m DataPointAttrMap) Len() int {
	return len(*m.getOrig())
}

func (m DataPointAttrMap) Get(key string) (string, bool) {
	orig := *m.getOrig()
	for i := range orig {
		if orig[i].Key == key {
			if sv, ok := orig[i].Value.Value.(*internal.AnyValue_StringValue); ok {
				return sv.StringValue, true
			}
			return "", true
		}
	}
	return "", false
}

func (m DataPointAttrMap) PutStr(key, value string) {
	m.h.state.AssertMutable()
	origPtr := m.getOrig()
	orig := *origPtr
	for i := range orig {
		if orig[i].Key == key {
			orig[i].Value.Value = &internal.AnyValue_StringValue{StringValue: value}
			return
		}
	}
	*origPtr = append(orig, internal.KeyValue{
		Key:   key,
		Value: internal.AnyValue{Value: &internal.AnyValue_StringValue{StringValue: value}},
	})
}
