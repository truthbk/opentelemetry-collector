// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package pdata // import "go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/pdata"

import (
	"go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/proto"
	"go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/tmplutil"
)

const sliceAccessorTemplate = `// {{ .fieldName }} returns the {{ .fieldName }} associated with this {{ .structName }}.
func (ms {{ .structName }}) {{ .fieldName }}() {{ .packageName }}{{ .returnType }} {
	{{- if .elementHasWrapper }}
	return {{ .packageName }}{{ .returnType }}(internal.New{{ .returnType }}Wrapper(&ms.{{ .origAccessor }}.{{ .originFieldName }}, ms.{{ .stateAccessor }}))
	{{- else if and .elementUseHandleLayout .msIsTopLevel }}
	// Tree-connected (top-level parent): ms is a typedef of
	// internal.{{ .msOriginName }}Wrapper; cross the package boundary
	// via the generated Get{{ .msOriginName }}Handle accessor since
	// the unexported h field isn't visible from this package.
	return {{ .returnType }}{
		h: internal.Get{{ .msOriginName }}Handle(internal.{{ .msOriginName }}Wrapper(ms)),
	}
	{{- else if and .elementUseHandleLayout (gt (len .msNestedPath) 0) }}
	// Tree-connected (nested parent): slice shares ms's Handle + indices
	// so cow.Share detach (Phase 5) rebinds the shared tree in place
	// across parent and all derived slice/element wrappers.
	return {{ .returnType }}{
		h: ms.h,
		{{- range .msNestedPath }}
		{{ .IndexVar }}: ms.{{ .IndexVar }},
		{{- end }}
	}
	{{- else }}
	// Fall-back: ms is either hasWrapper-but-not-top-level (e.g.
	// ProfilesData) or has no nestedPath; either way ms has no Handle
	// to propagate. Use the standalone new<X>Slice constructor — under
	// useHandleLayout it synthesizes a disconnected Handle wrapping
	// orig by pointer, so reads/writes still flow through; cow.Share
	// detach won't propagate into the slice from here (these parents
	// aren't part of the live signal tree).
	return new{{ .returnType }}(&ms.{{ .origAccessor }}.{{ .originFieldName }}, ms.{{ .stateAccessor }})
	{{- end }}
}`

const sliceAccessorsTestTemplate = `func Test{{ .structName }}_{{ .fieldName }}(t *testing.T) {
	ms := New{{ .structName }}()
	// Semantic equality (Path Y Phase 4 step 2a) — see message_test.go.tmpl.
	// Cross-package wrappers (elementHasWrapper=pcommon) use internal.Get<X>Orig
	// since getOrig() is package-private.
	{{- if .elementHasWrapper }}
	assert.Equal(t, *internal.Get{{ .returnType }}Orig(internal.{{ .returnType }}Wrapper({{ .packageName }}New{{ .returnType }}())), *internal.Get{{ .returnType }}Orig(internal.{{ .returnType }}Wrapper(ms.{{ .fieldName }}())))
	{{- else }}
	assert.Equal(t, *{{ .packageName }}New{{ .returnType }}().getOrig(), *ms.{{ .fieldName }}().getOrig())
	{{- end }}
	ms.{{ .origAccessor }}.{{ .originFieldName }} = internal.GenTest{{ .elementOriginName }}{{ if .elementNullable }}Ptr{{ end }}Slice()
	{{- if .elementHasWrapper }}
	assert.Equal(t, *internal.Get{{ .returnType }}Orig(internal.GenTest{{ .returnType }}Wrapper()), *internal.Get{{ .returnType }}Orig(internal.{{ .returnType }}Wrapper(ms.{{ .fieldName }}())))
	{{- else }}
	assert.Equal(t, *generateTest{{ .returnType }}().getOrig(), *ms.{{ .fieldName }}().getOrig())
	{{- end }}
}`

const sliceSetTestTemplate = `orig.{{ .originFieldName }} = internal.GenTest{{ .elementOriginName }}{{ if .elementNullable }}Ptr{{ end }}Slice()`

type SliceField struct {
	fieldName     string
	protoType     proto.Type
	protoID       uint32
	returnSlice   baseSlice
	hideAccessors bool
}

func (sf *SliceField) GenerateAccessors(ms *messageStruct) string {
	if sf.hideAccessors {
		return ""
	}
	t := tmplutil.Parse("sliceAccessorTemplate", []byte(sliceAccessorTemplate))
	return tmplutil.Execute(t, sf.templateFields(ms))
}

func (sf *SliceField) GenerateAccessorsTest(ms *messageStruct) string {
	if sf.hideAccessors {
		return ""
	}
	t := tmplutil.Parse("sliceAccessorsTestTemplate", []byte(sliceAccessorsTestTemplate))
	return tmplutil.Execute(t, sf.templateFields(ms))
}

func (sf *SliceField) GenerateTestValue(ms *messageStruct) string {
	t := tmplutil.Parse("sliceSetTestTemplate", []byte(sliceSetTestTemplate))
	return tmplutil.Execute(t, sf.templateFields(ms))
}

func (sf *SliceField) toProtoField(ms *messageStruct) proto.FieldInterface {
	return &proto.Field{
		Type:              sf.protoType,
		ID:                sf.protoID,
		Name:              sf.fieldName,
		MessageName:       sf.returnSlice.getElementOriginName(),
		ParentMessageName: ms.protoName,
		Repeated:          sf.protoType != proto.TypeBytes,
		Nullable:          sf.returnSlice.getElementNullable(),
	}
}

func (sf *SliceField) templateFields(ms *messageStruct) map[string]any {
	return map[string]any{
		"structName":        ms.getName(),
		"fieldName":         sf.fieldName,
		"originFieldName":   sf.fieldName,
		"elementOriginName": sf.returnSlice.getElementOriginName(),
		"packageName": func() string {
			if sf.returnSlice.getPackageName() != ms.packageName {
				return sf.returnSlice.getPackageName() + "."
			}
			return ""
		}(),
		"returnType":        sf.returnSlice.getName(),
		"origAccessor":      origAccessor(ms.getHasWrapper()),
		"stateAccessor":     stateAccessor(ms.getHasWrapper()),
		"elementHasWrapper": sf.returnSlice.getHasWrapper(),
		"elementNullable":   sf.returnSlice.getElementNullable(),

		// Path Y Phase 4 step 2b.iii — slice field accessor needs the
		// parent's nestedPath and element's useHandleLayout flag to
		// decide whether to emit a tree-connected slice (sharing ms's
		// Handle + indices) or fall back to the standalone new<X>Slice
		// constructor. elementUseHandleLayout is read off the slice's
		// element struct directly; FieldAccess-pathed elements stay false.
		"msNestedPath": ms.nestedPath,
		"msIsTopLevel": ms.isTopLevel,
		"msOriginName": ms.getName(),
		"elementUseHandleLayout": func() bool {
			if ess, ok := sf.returnSlice.(*messageSlice); ok && ess.element != nil {
				return ess.element.useHandleLayout
			}
			return false
		}(),
	}
}

var _ Field = (*SliceField)(nil)
