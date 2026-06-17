// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal // import "go.opentelemetry.io/collector/pdata/internal"

// Handle is the heap-allocated indirection that top-level pdata wrappers
// (MetricsWrapper, TracesWrapper, LogsWrapper, ProfilesWrapper) carry under
// the pdata.cow feature gate. The {orig, state} pair lives behind a single
// pointer so a successful detach (copy-on-write at the first mutating call
// on a shared wrapper) can rebind both fields in place and have every
// derived wrapper observe the new backing tree.
//
// Today's wrapper layout (pre-gate) is `struct { orig *T; state *State }`
// inlined directly into each generated wrapper struct. Under pdata.cow the
// codegen template emits `struct { h *Handle[T] }` instead. The Handle is
// shared by all wrappers that descend from the same top-level — when one
// of them mutates and detach fires, the Handle's fields are atomically
// rebound to point at the freshly-cloned backing tree.
//
// Handle is generic over the proto root type T so callers retain the
// existing type safety on `orig` (no unsafe.Pointer escape hatch). The
// type parameter is satisfied per signal: Handle[ExportMetricsServiceRequest]
// for Metrics, Handle[ExportTraceServiceRequest] for Traces, etc.
//
// See perf/rfc/pdata-cow.md for the design rationale and the alternatives
// considered.
type Handle[T any] struct {
	orig  *T
	state *State
}

// NewHandle allocates a Handle backing the given orig + state. Returned by
// value-typed wrapper constructors so each top-level wrapper carries a
// single *Handle field.
func NewHandle[T any](orig *T, state *State) *Handle[T] {
	return &Handle[T]{orig: orig, state: state}
}

// GetOrig returns the current backing orig pointer. After a successful
// detach this returns the cloned tree, not the original.
func (h *Handle[T]) GetOrig() *T {
	return h.orig
}

// GetState returns the current backing State pointer. After a successful
// detach this returns the new State allocated for the cloned tree, not
// the original.
func (h *Handle[T]) GetState() *State {
	return h.state
}

// rebind atomically updates the Handle's fields to point at a freshly-
// cloned backing tree. Called from the detach path; callers must hold
// the detach's CAS-claimed slot before invoking.
//
// rebind is internal to pdata/internal — exported as a method so codegen
// in pdata/{pmetric,ptrace,plog,pprofile} can call it from per-signal
// detach helpers, but it is not part of the public xpdata/cow API.
func (h *Handle[T]) rebind(orig *T, state *State) {
	h.orig = orig
	h.state = state
}
