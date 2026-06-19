// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package pdata // import "go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/pdata"

import (
	"go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/proto"
	"go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/tmplutil"
)

type baseStruct interface {
	getName() string
	getOriginName() string
	getOriginFullName() string
	getHasWrapper() bool
	getHasOnlyInternal() bool
	generate(packageInfo *PackageInfo) []byte
	generateTests(packageInfo *PackageInfo) []byte
	generateInternal(packageInfo *PackageInfo) []byte

	getProtoMessage() *proto.Message
}

// messageStruct generates a struct for a proto message. The struct can be generated both as a common struct
// that can be used as a field in struct from other packages and as an isolated struct with depending on a package name.
type messageStruct struct {
	structName      string
	packageName     string
	description     string
	protoName       string
	upstreamProto   string
	fields          []Field
	hasWrapper      bool
	hasOnlyInternal bool

	// isTopLevel marks the per-signal root types (Metrics, Traces, Logs,
	// Profiles) under Path Y. ComputeNestedPaths uses this as the entry
	// point for the tree walk that fills in nestedPath on reachable
	// non-pcommon descendants. Default false.
	isTopLevel bool

	// nestedPath records the path from a top-level wrapper's Handle.orig
	// down to this type's orig, expressed as a chain of slice indices
	// and direct field accesses. Empty for top-level types and for
	// types in pcommon (which stay inline per the Phase 1 decision).
	// Computed by ComputeNestedPaths; consumed by Phase 4 template
	// emission. See nested_path.go.
	nestedPath []PathSegment

	// topLevelOriginName is the proto type name of the top-level wrapper
	// at the root of this type's signal tree (e.g.
	// "ExportMetricsServiceRequest" for every non-pcommon descendant of
	// the metrics signal). Empty for top-level types themselves and for
	// pcommon types. Used by Phase 4 templates to parameterise
	// `internal.Handle[<topLevelOriginName>]` and to render the
	// synthetic parent tree in nested-wrapper standalone constructors.
	topLevelOriginName string
}

func (ms *messageStruct) getName() string {
	return ms.structName
}

func (ms *messageStruct) generate(packageInfo *PackageInfo) []byte {
	return []byte(tmplutil.Execute(messageTemplate, ms.templateFields(packageInfo)))
}

func (ms *messageStruct) generateTests(packageInfo *PackageInfo) []byte {
	return []byte(tmplutil.Execute(messageTestTemplate, ms.templateFields(packageInfo)))
}

func (ms *messageStruct) generateInternal(packageInfo *PackageInfo) []byte {
	return []byte(tmplutil.Execute(messageInternalTemplate, ms.templateFields(packageInfo)))
}

func (ms *messageStruct) getProtoMessage() *proto.Message {
	fields := make([]proto.FieldInterface, len(ms.fields))
	for i := range ms.fields {
		fields[i] = ms.fields[i].toProtoField(ms)
	}
	return &proto.Message{
		Name:            ms.protoName,
		Description:     ms.description,
		UpstreamMessage: ms.upstreamProto,
		Fields:          fields,
	}
}

func (ms *messageStruct) templateFields(packageInfo *PackageInfo) map[string]any {
	hasWrapper := ms.hasWrapper
	if !hasWrapper {
		hasWrapper = usedByOtherDataTypes(ms.packageName)
	}

	// Path Y Phase 4 — synthetic parent is only meaningful for
	// nested-non-pcommon types (where nestedPath is non-empty). For
	// top-level types and pcommon types, the syntheticParent string
	// is left empty; the upcoming step 2b.ii templates branch on
	// `len .nestedPath > 0` before emitting it. Mirrors the guard in
	// messageSlice.templateFields().
	var syntheticParent string
	if len(ms.nestedPath) > 0 {
		syntheticParent = renderSyntheticParent(ms.topLevelOriginName, ms.nestedPath, "orig")
	}

	return map[string]any{
		"messageStruct": ms,
		"fields":        ms.fields,
		"structName":    ms.getName(),
		"protoName":     ms.getOriginFullName(),
		"originName":    ms.getOriginName(),
		"description":   ms.description,
		"hasWrapper":    hasWrapper,
		"origAccessor":  origAccessor(hasWrapper),
		"stateAccessor": stateAccessor(hasWrapper),
		"packageName":   packageInfo.name,
		"imports":       packageInfo.imports,
		"testImports":   packageInfo.testImports,

		// Path Y Phase 4: isTopLevel switches the wrapper template
		// between Handle[T]-backed (the 4 signal roots) and inline
		// {orig, state} (pcommon shared types — kept inline per the
		// Phase 1 pcommon-stays-inline decision). nestedPath carries
		// the path from the top-level Handle.orig down to this type's
		// orig — empty for top-level and pcommon types.
		// topLevelOriginName is the proto type name at the root of
		// this type's signal tree, used to parameterise
		// internal.Handle[T] in the nested-wrapper struct fields.
		// syntheticParent is the pre-rendered "&internal.{T}{...}"
		// expression that nested-wrapper standalone constructors emit
		// to synthesize a single-element parent tree wrapping the
		// caller's orig. Empty when nestedPath is empty.
		"isTopLevel":         ms.isTopLevel,
		"nestedPath":         ms.nestedPath,
		"topLevelOriginName": ms.topLevelOriginName,
		"syntheticParent":    syntheticParent,
	}
}

func (ms *messageStruct) getHasWrapper() bool {
	if ms.hasWrapper {
		return true
	}
	if ms.hasOnlyInternal {
		return false
	}
	return usedByOtherDataTypes(ms.packageName)
}

func (ms *messageStruct) getHasOnlyInternal() bool {
	return ms.hasOnlyInternal
}

func (ms *messageStruct) getOriginName() string {
	return ms.protoName
}

func (ms *messageStruct) getOriginFullName() string {
	return ms.protoName
}

var _ baseStruct = (*messageStruct)(nil)
