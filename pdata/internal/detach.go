// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal // import "go.opentelemetry.io/collector/pdata/internal"

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
// is undefined behaviour today and is unchanged here. Multiple
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
