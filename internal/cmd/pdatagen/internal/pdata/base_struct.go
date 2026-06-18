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
		"isTopLevel": ms.isTopLevel,
		"nestedPath": ms.nestedPath,
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
