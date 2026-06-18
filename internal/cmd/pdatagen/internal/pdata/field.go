// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package pdata // import "go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/pdata"
import (
	"go.opentelemetry.io/collector/internal/cmd/pdatagen/internal/proto"
)

type Field interface {
	GenerateAccessors(ms *messageStruct) string

	GenerateAccessorsTest(ms *messageStruct) string

	GenerateTestValue(ms *messageStruct) string

	toProtoField(ms *messageStruct) proto.FieldInterface
}

// origAccessor returns "getOrig()" unconditionally. This used to branch
// on hasWrapper and return the raw "orig" field name for non-wrapper
// types, but Path Y requires every wrapper (top-level + nested) to
// route field access through a method that can re-derive after a
// detach rebinds the top-level Handle. The method body still branches
// on hasWrapper internally — see message.go.tmpl / slice.go.tmpl.
//
// One-line accessors are aggressively inlined by the Go compiler, so
// the generated code's runtime cost is identical to the previous direct
// field access on the hot path (non-shared wrappers).
func origAccessor(_ bool) string {
	return "getOrig()"
}

func stateAccessor(_ bool) string {
	return "getState()"
}
