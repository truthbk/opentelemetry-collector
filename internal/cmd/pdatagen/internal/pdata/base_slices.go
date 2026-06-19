// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package pdata // import "go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/pdata"

import (
	"go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/proto"
	"go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/tmplutil"
)

type baseSlice interface {
	getName() string
	getHasWrapper() bool
	getOriginFullName() string
	getElementOriginName() string
	getElementNullable() bool
	getPackageName() string
}

// messageSlice generates code for a slice of pointer fields. The generated structs cannot be used from other packages.
type messageSlice struct {
	structName      string
	packageName     string
	elementNullable bool
	element         *messageStruct
}

func (ss *messageSlice) getProtoMessage() *proto.Message {
	return nil
}

func (ss *messageSlice) getName() string {
	return ss.structName
}

func (ss *messageSlice) getPackageName() string {
	return ss.packageName
}

func (ss *messageSlice) generate(packageInfo *PackageInfo) []byte {
	return []byte(tmplutil.Execute(sliceTemplate, ss.templateFields(packageInfo)))
}

func (ss *messageSlice) generateTests(packageInfo *PackageInfo) []byte {
	return []byte(tmplutil.Execute(sliceTestTemplate, ss.templateFields(packageInfo)))
}

func (ss *messageSlice) generateInternal(packageInfo *PackageInfo) []byte {
	return []byte(tmplutil.Execute(sliceInternalTemplate, ss.templateFields(packageInfo)))
}

func (ss *messageSlice) templateFields(packageInfo *PackageInfo) map[string]any {
	hasWrapper := usedByOtherDataTypes(ss.packageName)

	// Path Y Phase 4 — slice-side path info derived from the element's
	// nestedPath. The element's path encodes the FULL chain from the
	// top-level Handle.orig down to the element struct (including the
	// SliceIndex segment that addresses individual elements). The slice
	// itself sits ONE LEVEL ABOVE the element: at the parent struct +
	// the slice field name. So:
	//
	//   - parentNestedPath = element.nestedPath[:-1]
	//       (indices the slice wrapper needs to carry; empty for top-level slices)
	//   - sliceFieldName   = element.nestedPath[-1].FieldName
	//       (the slice's field name on its parent's struct)
	//   - elementIndexVar  = element.nestedPath[-1].IndexVar
	//       (the int field name a slice element wrapper uses)
	//   - topLevelOriginName = element.topLevelOriginName
	//
	// All four are empty for pcommon slices (walker skips pcommon, so
	// element.nestedPath stays nil). Phase 4 templates check len > 0 /
	// non-empty before branching to the new layout.
	var parentNestedPath []PathSegment
	var sliceFieldName, elementIndexVar, topLevelOriginName string
	var sliceSyntheticParent string
	if elem := ss.element; elem != nil && len(elem.nestedPath) > 0 {
		topLevelOriginName = elem.topLevelOriginName
		parentNestedPath = elem.nestedPath[:len(elem.nestedPath)-1]
		last := elem.nestedPath[len(elem.nestedPath)-1]
		sliceFieldName = last.FieldName
		elementIndexVar = last.IndexVar
		sliceSyntheticParent = renderSliceSyntheticParent(
			topLevelOriginName, parentNestedPath, sliceFieldName, "*orig",
		)
	}

	return map[string]any{
		"hasWrapper":        usedByOtherDataTypes(ss.packageName),
		"structName":        ss.structName,
		"elementName":       ss.element.getName(),
		"elementOriginName": ss.getElementOriginName(),
		"elementNullable":   ss.elementNullable,
		"origAccessor":      origAccessor(hasWrapper),
		"stateAccessor":     stateAccessor(hasWrapper),
		"packageName":       packageInfo.name,
		"imports":           packageInfo.imports,
		"testImports":       packageInfo.testImports,

		// Path Y Phase 4 slice-side path info (see comment above).
		"parentNestedPath":     parentNestedPath,
		"sliceFieldName":       sliceFieldName,
		"elementIndexVar":      elementIndexVar,
		"topLevelOriginName":   topLevelOriginName,
		"sliceSyntheticParent": sliceSyntheticParent,
	}
}

func (ss *messageSlice) getOriginName() string {
	return ss.element.getOriginName() + "Slice"
}

func (ss *messageSlice) getOriginFullName() string {
	return ss.element.getOriginFullName()
}

func (ss *messageSlice) getHasWrapper() bool {
	return usedByOtherDataTypes(ss.packageName)
}

func (ss *messageSlice) getHasOnlyInternal() bool {
	return false
}

func (ss *messageSlice) getElementOriginName() string {
	return ss.element.getOriginName()
}

func (ss *messageSlice) getElementNullable() bool {
	return ss.elementNullable
}

var _ baseStruct = (*messageSlice)(nil)
