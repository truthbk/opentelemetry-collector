// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package pdata // import "go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/pdata"

import "strings"

// PathSegment describes one step from a top-level wrapper's Handle.orig
// down to a nested wrapper's orig. The path is what makes auto-detach
// safe under pdata.cow: when a detach rebinds the top-level Handle's
// orig, any nested wrapper can re-derive its own orig by walking this
// path from h.orig.
//
// Path Y design (see perf/rfc/pdata-cow.md "Path Y resume" section):
//
//   - SliceIndex segments correspond to slice fields whose elements are
//     wrapped by another type (e.g., ResourceMetrics is the element of
//     the slice at h.orig.ResourceMetrics — that's one SliceIndex
//     segment with FieldName="ResourceMetrics" and IndexVar="rmIdx").
//
//   - FieldAccess segments correspond to direct singular fields (e.g.,
//     ResourceMetrics.Resource — that's one FieldAccess segment with
//     FieldName="Resource"). Used to reach types like pcommon.Resource
//     when that type IS migrated to Path Y (today it's not, per the
//     Phase 1 pcommon decision — pcommon stays inline).
//
//   - Oneof segments TBD; not needed for the metrics-signal validation
//     since the metrics paths reach DataPoints via per-variant Sum,
//     Gauge, Histogram, etc. (oneof at the Metric level). Phase 4 will
//     extend this if needed.
type PathSegment struct {
	Kind      PathSegmentKind
	FieldName string // proto field name on the parent's orig (e.g. "ResourceMetrics")
	IndexVar  string // for SliceIndex: the int field name on the wrapper (e.g. "rmIdx")
	// ChildOriginName is the proto type name of the value at this path
	// segment. For SliceIndex, it is the element type (e.g.
	// "ResourceMetrics" for the ResourceMetrics slice). For FieldAccess,
	// it is the field's type name. Used by RenderSyntheticParent and by
	// the Phase 4 templates to emit `[]*internal.{ChildOriginName}{...}`
	// literals when synthesizing the standalone Handle tree.
	ChildOriginName string
}

type PathSegmentKind int

const (
	// PathSegmentSliceIndex: parent's orig has a slice field; the
	// wrapper carries an int index into that slice. Path renders as
	// `.{FieldName}[{IndexVar}]`.
	PathSegmentSliceIndex PathSegmentKind = iota

	// PathSegmentFieldAccess: parent's orig has a direct (non-slice)
	// field that holds the next wrapper's orig. Path renders as
	// `.{FieldName}`. No index variable.
	PathSegmentFieldAccess
)

// indexVarForSlice returns the canonical int field name for a slice
// segment. Convention: the first letter (lowercased) of each CamelCase
// boundary in the field name, then "Idx". e.g. ResourceMetrics →
// rmIdx, ScopeMetrics → smIdx, Metrics → mIdx, DataPoints → dpIdx.
func indexVarForSlice(fieldName string) string {
	var prefix []byte
	for _, r := range fieldName {
		if r >= 'A' && r <= 'Z' {
			prefix = append(prefix, byte(r-'A'+'a'))
		}
	}
	if len(prefix) == 0 {
		// Fallback: shouldn't happen for proto-typical CamelCase names.
		prefix = []byte{'x'}
	}
	return string(prefix) + "Idx"
}

// ComputeNestedPaths walks the type tree from each top-level
// messageStruct in pkg (those flagged isTopLevel=true) and assigns the
// nestedPath on every reachable signal-specific descendant. pcommon
// shared types (packageName == "pcommon") are skipped per the Phase 1
// decision — they stay with inline orig/state and do not carry path
// tracking under Path Y.
//
// Idempotent: a type whose nestedPath is already set is left as-is.
// Handles repeated references within a single signal (e.g. the same
// nested type reachable via two field paths) by preferring the first
// path encountered in the depth-first walk; in practice the proto
// trees are tree-shaped, so this comes up only for cycle-safety.
func ComputeNestedPaths(pkg *Package) {
	for _, s := range pkg.structs {
		ms, ok := s.(*messageStruct)
		if !ok || !ms.isTopLevel {
			continue
		}
		walkNestedFields(ms, nil, ms.getOriginName())
	}
}

// walkNestedFields walks the type tree rooted at parent, filling nestedPath
// and topLevelOriginName on each non-pcommon descendant. topLevelOriginName
// is propagated unchanged through the recursion — every reachable type
// inherits the same top-level orig name as the root that started the walk.
func walkNestedFields(parent *messageStruct, parentPath []PathSegment, topLevelOriginName string) {
	for _, f := range parent.fields {
		switch fld := f.(type) {
		case *MessageField:
			child := fld.returnMessage
			if child == nil || child.packageName == "pcommon" || len(child.nestedPath) > 0 {
				continue
			}
			childPath := append(clonePath(parentPath), PathSegment{
				Kind:            PathSegmentFieldAccess,
				FieldName:       fld.fieldName,
				ChildOriginName: child.getOriginName(),
			})
			child.nestedPath = childPath
			child.topLevelOriginName = topLevelOriginName
			child.useHandleLayout = allSliceIndex(childPath)
			walkNestedFields(child, childPath, topLevelOriginName)
		case *SliceField:
			ms, ok := fld.returnSlice.(*messageSlice)
			if !ok {
				continue
			}
			elem := ms.element
			if elem == nil || elem.packageName == "pcommon" || len(elem.nestedPath) > 0 {
				continue
			}
			elemPath := append(clonePath(parentPath), PathSegment{
				Kind:            PathSegmentSliceIndex,
				FieldName:       fld.fieldName,
				IndexVar:        indexVarForSlice(fld.fieldName),
				ChildOriginName: elem.getOriginName(),
			})
			elem.nestedPath = elemPath
			elem.topLevelOriginName = topLevelOriginName
			elem.useHandleLayout = allSliceIndex(elemPath)
			walkNestedFields(elem, elemPath, topLevelOriginName)
		}
	}
}

// allSliceIndex reports whether every segment in path is a SliceIndex.
// Used to gate the Phase 4 step 2b.ii {h, indices} layout to types whose
// walk path doesn't include FieldAccess segments — FieldAccess support
// is deferred (Span.Status etc. stay on the inline {orig, state} layout).
func allSliceIndex(path []PathSegment) bool {
	for _, seg := range path {
		if seg.Kind != PathSegmentSliceIndex {
			return false
		}
	}
	return true
}

// renderSliceSyntheticParent emits a Go expression that constructs a top-level
// orig tree whose leaf is the slice the caller is wrapping. `parentSegments`
// is the path from the top-level down to (but not including) the slice
// itself — i.e. element.nestedPath[:-1]. `sliceFieldName` is the field name
// of the slice on its parent struct (element.nestedPath[-1].FieldName).
// `sliceVar` is the Go variable name carrying the *[]*Element value
// (typically "*orig" — dereferenced because the standalone constructor
// receives `orig *[]*Element`).
//
// Examples:
//
//	Top-level slice (ResourceMetricsSlice, parentSegments=[], sliceField="ResourceMetrics", sliceVar="*orig"):
//	    &internal.ExportMetricsServiceRequest{ResourceMetrics: *orig}
//
//	Nested slice (ScopeMetricsSlice, parentSegments=[SI(ResourceMetrics, ResourceMetrics)],
//	              sliceField="ScopeMetrics", sliceVar="*orig"):
//	    &internal.ExportMetricsServiceRequest{
//	        ResourceMetrics: []*internal.ResourceMetrics{{
//	            ScopeMetrics: *orig,
//	        }},
//	    }
//
// This mirrors renderSyntheticParent but plants the entire slice value at
// the leaf instead of a single-element-wrapped scalar.
//
// Consumed by base_slices.go's templateFields() and surfaced to slice.go.tmpl
// as `{{ .sliceSyntheticParent }}` in Phase 4 step 2b.ii.
func renderSliceSyntheticParent(topLevelOriginName string, parentSegments []PathSegment, sliceFieldName, sliceVar string) string {
	var sb strings.Builder
	sb.WriteString("&internal.")
	sb.WriteString(topLevelOriginName)
	sb.WriteString("{")
	for _, seg := range parentSegments {
		if seg.Kind != PathSegmentSliceIndex {
			return "/* unsupported: FieldAccess segment in slice-synthetic-parent render */"
		}
		sb.WriteString(seg.FieldName)
		sb.WriteString(": []*internal.")
		sb.WriteString(seg.ChildOriginName)
		sb.WriteString("{{")
	}
	sb.WriteString(sliceFieldName)
	sb.WriteString(": ")
	sb.WriteString(sliceVar)
	// Close: 1 for the outer top-level struct + 2 per parent segment
	// (slice literal + inner struct literal).
	sb.WriteString("}")
	for i := 0; i < 2*len(parentSegments); i++ {
		sb.WriteString("}")
	}
	return sb.String()
}

// renderSyntheticParent emits a Go expression that constructs a single-element
// top-level orig tree wrapping `leafOrigVar` at the slice-index path
// described by segments. The result is a struct literal of the form:
//
//	&internal.{topLevelOriginName}{
//	    {FieldName1}: []*internal.{ChildOriginName1}{
//	        {  // (only for intermediate segments)
//	            {FieldName2}: []*internal.{ChildOriginName2}{
//	                {leafOrigVar},
//	            },
//	        },
//	    },
//	}
//
// Used by Phase 4 nested-wrapper constructors (new<X>(orig, state)) to
// synthesize a standalone Handle whose getOrig walks back to the passed-in
// orig. This is the "always-h" pattern from the engagement plan: every
// wrapper carries a Handle regardless of how it was constructed, so the
// struct shape is uniform across standalone and tree-embedded cases.
//
// Only SliceIndex segments are supported. FieldAccess segments are reserved
// for pcommon-targeted paths, but pcommon types stay inline {orig, state}
// per the Phase 1 decision and never need synthetic-parent rendering.
//
// Consumed by base_struct.go's templateFields() and surfaced to message.go.tmpl
// as `{{ .syntheticParent }}` in Phase 4 step 2b.ii.
func renderSyntheticParent(topLevelOriginName string, segments []PathSegment, leafOrigVar string) string {
	var sb strings.Builder
	sb.WriteString("&internal.")
	sb.WriteString(topLevelOriginName)
	sb.WriteString("{")
	for i, seg := range segments {
		if seg.Kind != PathSegmentSliceIndex {
			// FieldAccess synthesis is not implemented; the walker should
			// have skipped pcommon-bound paths before reaching this helper.
			return "/* unsupported: FieldAccess segment in synthetic-parent render */"
		}
		sb.WriteString(seg.FieldName)
		sb.WriteString(": []*internal.")
		sb.WriteString(seg.ChildOriginName)
		sb.WriteString("{")
		if i == len(segments)-1 {
			sb.WriteString(leafOrigVar)
		} else {
			// Intermediate segment: open the next struct literal.
			sb.WriteString("{")
		}
	}
	// Close all opened braces. Per-segment opens: each segment opens 1
	// slice literal; intermediate segments additionally open 1 struct
	// literal. Plus the outer top-level struct.
	// Total closes = 1 (outer) + len(segments) (slice) + (len(segments)-1)
	//              = 2 * len(segments).
	for i := 0; i < 2*len(segments); i++ {
		sb.WriteString("}")
	}
	return sb.String()
}

func clonePath(p []PathSegment) []PathSegment {
	out := make([]PathSegment, len(p))
	copy(out, p)
	return out
}
