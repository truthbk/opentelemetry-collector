// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package pdata // import "go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/pdata"

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
		walkNestedFields(ms, nil)
	}
}

func walkNestedFields(parent *messageStruct, parentPath []PathSegment) {
	for _, f := range parent.fields {
		switch fld := f.(type) {
		case *MessageField:
			child := fld.returnMessage
			if child == nil || child.packageName == "pcommon" || len(child.nestedPath) > 0 {
				continue
			}
			childPath := append(clonePath(parentPath), PathSegment{
				Kind:      PathSegmentFieldAccess,
				FieldName: fld.fieldName,
			})
			child.nestedPath = childPath
			walkNestedFields(child, childPath)
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
				Kind:      PathSegmentSliceIndex,
				FieldName: fld.fieldName,
				IndexVar:  indexVarForSlice(fld.fieldName),
			})
			elem.nestedPath = elemPath
			walkNestedFields(elem, elemPath)
		}
	}
}

func clonePath(p []PathSegment) []PathSegment {
	out := make([]PathSegment, len(p))
	copy(out, p)
	return out
}
