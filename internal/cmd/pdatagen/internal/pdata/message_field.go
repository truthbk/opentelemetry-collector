// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package pdata // import "go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/pdata"

import (
	"strings"

	"go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/proto"
	"go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/tmplutil"
)

const messageAccessorsTemplate = `// {{ .fieldName }} returns the {{ .lowerFieldName }} associated with this {{ .structName }}.
func (ms {{ .structName }}) {{ .fieldName }}() {{ .packageName }}{{ .returnType }} {
	{{- if .messageHasWrapper }}
	return {{ .packageName }}{{ .returnType }}(internal.New{{ .returnType }}Wrapper(&ms.{{ .origAccessor }}.{{ .fieldOriginFullName }}, ms.{{ .stateAccessor }}))
	{{- else }}
	return new{{ .returnType }}(&ms.{{ .origAccessor }}.{{ .fieldOriginFullName }}, ms.{{ .stateAccessor }})
	{{- end }}
}`

const messageAccessorsTestTemplate = `func Test{{ .structName }}_{{ .fieldName }}(t *testing.T) {
	ms := New{{ .structName }}()
	// Semantic equality (Path Y Phase 4 step 2a): compare *getOrig() rather
	// than the wrapper struct itself. See message_test.go.tmpl for rationale.
	// When the field type lives in a different package (.messageHasWrapper),
	// use the exported internal.Get<X>Orig accessor since getOrig() is
	// package-private. When it lives in the same package, use getOrig()
	// directly.
	{{- if .messageHasWrapper }}
	assert.Equal(t, *internal.Get{{ .returnType }}Orig(internal.{{ .returnType }}Wrapper({{ .packageName }}New{{ .returnType }}{{- if eq .returnType "Value" }}Empty{{- end }}())), *internal.Get{{ .returnType }}Orig(internal.{{ .returnType }}Wrapper(ms.{{ .fieldName }}())))
	{{- else }}
	assert.Equal(t, *{{ .packageName }}New{{ .returnType }}().getOrig(), *ms.{{ .fieldName }}().getOrig())
	{{- end }}
	ms.{{ .origAccessor }}.{{ .fieldOriginFullName }} = *internal.GenTest{{ .fieldOriginName }}()
	{{- if .messageHasWrapper }}
	assert.Equal(t, *internal.Get{{ .returnType }}Orig(internal.GenTest{{ .returnType }}Wrapper()), *internal.Get{{ .returnType }}Orig(internal.{{ .returnType }}Wrapper(ms.{{ .fieldName }}())))
	{{- else }}
	assert.Equal(t, *generateTest{{ .returnType }}().getOrig(), *ms.{{ .fieldName }}().getOrig())
	{{- end }}
}`

const messageSetTestTemplate = `orig.{{ .fieldOriginFullName }} = *GenTest{{ .fieldOriginName }}()`

type MessageField struct {
	fieldName     string
	protoID       uint32
	nullable      bool
	returnMessage *messageStruct
}

func (mf *MessageField) GenerateAccessors(ms *messageStruct) string {
	t := tmplutil.Parse("messageAccessorsTemplate", []byte(messageAccessorsTemplate))
	return tmplutil.Execute(t, mf.templateFields(ms))
}

func (mf *MessageField) GenerateAccessorsTest(ms *messageStruct) string {
	t := tmplutil.Parse("messageAccessorsTestTemplate", []byte(messageAccessorsTestTemplate))
	return tmplutil.Execute(t, mf.templateFields(ms))
}

func (mf *MessageField) GenerateTestValue(ms *messageStruct) string {
	t := tmplutil.Parse("messageSetTestTemplate", []byte(messageSetTestTemplate))
	return tmplutil.Execute(t, mf.templateFields(ms))
}

func (mf *MessageField) toProtoField(ms *messageStruct) proto.FieldInterface {
	pt := proto.TypeMessage
	if mf.returnMessage.getName() == "TraceState" {
		pt = proto.TypeString
	}
	return &proto.Field{
		Type:              pt,
		ID:                mf.protoID,
		Name:              mf.fieldName,
		MessageName:       mf.returnMessage.getOriginName(),
		ParentMessageName: ms.protoName,
		Nullable:          mf.nullable,
	}
}

func (mf *MessageField) templateFields(ms *messageStruct) map[string]any {
	return map[string]any{
		"messageHasWrapper":   usedByOtherDataTypes(mf.returnMessage.packageName),
		"structName":          ms.getName(),
		"fieldName":           mf.fieldName,
		"fieldOriginFullName": mf.fieldName,
		"fieldOriginName":     mf.returnMessage.getOriginName(),
		"lowerFieldName":      strings.ToLower(mf.fieldName),
		"returnType":          mf.returnMessage.getName(),
		"packageName": func() string {
			if mf.returnMessage.packageName != ms.packageName {
				return mf.returnMessage.packageName + "."
			}
			return ""
		}(),
		"origAccessor":  origAccessor(ms.getHasWrapper()),
		"stateAccessor": stateAccessor(ms.getHasWrapper()),
	}
}

var _ Field = (*MessageField)(nil)
