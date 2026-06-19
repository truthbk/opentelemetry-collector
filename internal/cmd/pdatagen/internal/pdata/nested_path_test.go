// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package pdata

import (
	"strings"
	"testing"
)

// TestRenderSyntheticParent_Depth1 exercises the simplest meaningful path:
// one SliceIndex segment, producing the synthetic single-element parent
// tree that a depth-1 nested wrapper's standalone constructor needs.
func TestRenderSyntheticParent_Depth1(t *testing.T) {
	segs := []PathSegment{
		{
			Kind:            PathSegmentSliceIndex,
			FieldName:       "ResourceMetrics",
			IndexVar:        "rmIdx",
			ChildOriginName: "ResourceMetrics",
		},
	}
	got := renderSyntheticParent("ExportMetricsServiceRequest", segs, "orig")
	want := "&internal.ExportMetricsServiceRequest{ResourceMetrics: []*internal.ResourceMetrics{orig}}"
	if got != want {
		t.Errorf("depth-1 mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestRenderSyntheticParent_Depth2 exercises a nested-slice-of-slice path,
// the shape ScopeMetrics has: ResourceMetrics[rmIdx].ScopeMetrics[smIdx].
// Catches brace-counting bugs in the loop: two segments need 2 outer-slice
// braces + 1 inner-struct brace + 1 outer-struct brace = 4 closes.
func TestRenderSyntheticParent_Depth2(t *testing.T) {
	segs := []PathSegment{
		{
			Kind:            PathSegmentSliceIndex,
			FieldName:       "ResourceMetrics",
			IndexVar:        "rmIdx",
			ChildOriginName: "ResourceMetrics",
		},
		{
			Kind:            PathSegmentSliceIndex,
			FieldName:       "ScopeMetrics",
			IndexVar:        "smIdx",
			ChildOriginName: "ScopeMetrics",
		},
	}
	got := renderSyntheticParent("ExportMetricsServiceRequest", segs, "orig")
	want := "&internal.ExportMetricsServiceRequest{ResourceMetrics: []*internal.ResourceMetrics{{ScopeMetrics: []*internal.ScopeMetrics{orig}}}}"
	if got != want {
		t.Errorf("depth-2 mismatch\n got: %s\nwant: %s", got, want)
	}
	// Brace balance is the load-bearing invariant — count both sides.
	if opens, closes := strings.Count(got, "{"), strings.Count(got, "}"); opens != closes {
		t.Errorf("unbalanced braces: %d opens vs %d closes in %s", opens, closes, got)
	}
}

// TestRenderSyntheticParent_FieldAccessRejected ensures we don't silently
// emit malformed Go when a FieldAccess segment slips through (today's
// walker skips pcommon-bound paths, but a future signal could regress).
// The sentinel comment is non-compiling Go which fails at the next
// `make genpdata` rather than silently producing wrong output.
func TestRenderSyntheticParent_FieldAccessRejected(t *testing.T) {
	segs := []PathSegment{
		{Kind: PathSegmentFieldAccess, FieldName: "Resource", ChildOriginName: "Resource"},
	}
	got := renderSyntheticParent("ExportMetricsServiceRequest", segs, "orig")
	if !strings.Contains(got, "unsupported") {
		t.Errorf("expected 'unsupported' sentinel for FieldAccess segment, got: %s", got)
	}
}

// TestRenderSliceSyntheticParent_Depth1 — top-level slice, no parent
// indices. The slice value IS the field on the top-level struct.
func TestRenderSliceSyntheticParent_Depth1(t *testing.T) {
	got := renderSliceSyntheticParent("ExportMetricsServiceRequest", nil, "ResourceMetrics", "*orig")
	want := "&internal.ExportMetricsServiceRequest{ResourceMetrics: *orig}"
	if got != want {
		t.Errorf("top-level slice mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestRenderSliceSyntheticParent_Depth2 — nested slice (e.g.
// ScopeMetricsSlice on RM): one parent SliceIndex segment + the slice
// at the leaf. Catches brace-counting in the parent walk + leaf insertion.
func TestRenderSliceSyntheticParent_Depth2(t *testing.T) {
	parentSegs := []PathSegment{
		{
			Kind:            PathSegmentSliceIndex,
			FieldName:       "ResourceMetrics",
			IndexVar:        "rmIdx",
			ChildOriginName: "ResourceMetrics",
		},
	}
	got := renderSliceSyntheticParent("ExportMetricsServiceRequest", parentSegs, "ScopeMetrics", "*orig")
	want := "&internal.ExportMetricsServiceRequest{ResourceMetrics: []*internal.ResourceMetrics{{ScopeMetrics: *orig}}}"
	if got != want {
		t.Errorf("nested slice mismatch\n got: %s\nwant: %s", got, want)
	}
	if opens, closes := strings.Count(got, "{"), strings.Count(got, "}"); opens != closes {
		t.Errorf("unbalanced braces: %d opens vs %d closes in %s", opens, closes, got)
	}
}

// TestIndexVarForSlice — convention check: first letter of each CamelCase
// boundary, lowercased, plus "Idx".
func TestIndexVarForSlice(t *testing.T) {
	cases := map[string]string{
		"ResourceMetrics":  "rmIdx",
		"ScopeMetrics":     "smIdx",
		"Metrics":          "mIdx",
		"DataPoints":       "dpIdx",
		"ResourceProfiles": "rpIdx",
	}
	for input, want := range cases {
		if got := indexVarForSlice(input); got != want {
			t.Errorf("indexVarForSlice(%q) = %q, want %q", input, got, want)
		}
	}
}
