// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal // import "go.opentelemetry.io/collector/pdata/internal"

// Two symbols named DetachIfShared live in this package; they are NOT
// interchangeable and a Phase-5 implementor must wire the right one:
//
//   - This file's generic free function DetachIfShared[T](h, cloneFn)
//     is the TOP-LEVEL wrapper path. It takes the top-level Handle (the
//     one returned by NewXWrapper) and the per-signal clone function;
//     it rebinds h.orig + h.state atomically so subsequent accessors
//     observe the new backing. Use this from generated mutators on the
//     top-level signal types (Metrics.MoveTo, Traces.CopyTo, ...).
//
//   - state.go's DetachIfShared method on *State is the NESTED-wrapper
//     dispatch path. Nested wrappers (e.g. Resource.Attributes() on a
//     shared Metrics) don't have access to the top-level Handle but do
//     share its *State; they call st.DetachIfShared(), which in turn
//     dispatches to the closure that cow.ShareX installed via
//     SetDetacher. The closure ultimately calls THIS file's generic
//     function with the captured Handle and cloneFn.
//
// In short: generated code at the top level calls internal.DetachIfShared
// directly; generated code at nested levels calls state.DetachIfShared().
// Today neither path is invoked by any production code — both are
// scaffolding for Path Y phases 4-6 (template emission + auto-detach
// wiring). cow.ShareX will gain a SetDetacher call in Phase 5 to install
// the per-signal closure that bridges nested → generic.

// DetachIfShared is the load-bearing primitive of Path Y's auto-detach
// mechanism. Called from a mutating wrapper before any actual mutation
// fires: if the wrapper's State carries an outstanding COW share
// (cowRefs > 0) and is not marked read-only, it deep-clones the orig
// tree via the caller-provided cloneFn, allocates a fresh State with
// cowRefs == 0, and atomically rebinds h.orig + h.state to the new
// pair. After the call, the wrapper observes an independent backing
// tree it can safely mutate.
//
// cloneFn is per-signal because the internal package does not know
// about the proto-pool allocators directly — each generated mutator
// supplies the appropriate Copy*Request function.
//
// Concurrency: a single wrapper is single-writer per the existing
// pdata contract; concurrent goroutines mutating the same wrapper
// is undefined behavior today and is unchanged here. Multiple
// shares (returned by separate cow.ShareX calls) each have their
// own Handle and State, so their detach operations are independent.
//
// Fast path (cowRefs == 0): one atomic load, one compare, one
// branch, no allocation. This is the path every non-shared wrapper
// hits — i.e. every wrapper outside the fanout's broadcast.
//
// Slow path (cowRefs > 0): deep clone of orig + State allocation +
// Handle rebind. Same allocation profile as today's eager fanout
// clone (one CopyTo per dispatched consumer), just deferred to the
// first mutation and skipped entirely on the no-mutation path.
func DetachIfShared[T any](h *Handle[T], cloneFn func(dest, src *T) *T) {
	st := h.state
	if st.cowRefs.Load() == 0 {
		return
	}
	if st.state&stateReadOnlyBit != 0 {
		// Read-only wrapper that's also COW-shared shouldn't happen in
		// well-behaved code (cow.ShareX gives the share a fresh
		// non-readonly State). If it does, fall through to AssertMutable
		// which will panic with the readonly message — preserves today's
		// safety net.
		return
	}
	// Bump generation BEFORE the rebind. The bump is on the OLD State,
	// which is still pointed at by the source / sibling shares. Any
	// debug-tag-enabled nested wrapper that captured the old generation
	// will detect the mismatch on its next access and re-derive. Path Y
	// nested wrappers re-derive on every getOrig anyway, so this is
	// belt-and-suspenders for future captured-pointer designs.
	st.generation.Add(1)

	var newOrig T
	dest := cloneFn(&newOrig, h.orig)
	newState := NewState()
	h.rebind(dest, newState)
}
