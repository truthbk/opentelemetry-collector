// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDetachIfShared_NotShared_IsNoop — cowRefs == 0 path. The Handle's
// orig + state stay byte-identical; no allocation.
func TestDetachIfShared_NotShared_IsNoop(t *testing.T) {
	st := NewState()
	orig := &ExportMetricsServiceRequest{}
	h := &Handle[ExportMetricsServiceRequest]{orig: orig, state: st}
	DetachIfShared(h, CopyExportMetricsServiceRequest)
	assert.Same(t, orig, h.orig, "non-shared: orig unchanged")
	assert.Same(t, st, h.state, "non-shared: state unchanged")
}

// TestDetachIfShared_Shared_RebindsHandle — the load-bearing detach
// path. The Handle's orig and state are rebound to a new, independent
// pair. Mutating the new tree doesn't affect any wrapper still
// holding the old orig.
func TestDetachIfShared_Shared_RebindsHandle(t *testing.T) {
	st := NewState()
	st.IncCowRefs() // simulate cow.Share having bumped this state
	origReq := &ExportMetricsServiceRequest{}
	h := &Handle[ExportMetricsServiceRequest]{orig: origReq, state: st}

	// Snapshot orig + state before detach.
	preOrig := h.orig
	preState := h.state

	DetachIfShared(h, CopyExportMetricsServiceRequest)

	// Handle's orig and state must have been rebound to new instances.
	assert.NotSame(t, preOrig, h.orig, "shared: orig rebound to clone")
	assert.NotSame(t, preState, h.state, "shared: state rebound to fresh")
	// New state has cowRefs == 0 (independent of share lifecycle).
	assert.Equal(t, int32(0), h.state.CowRefs(), "new state starts non-shared")
}

// TestDetachIfShared_Readonly_StaysShortCircuit — read-only state must
// fall through detach so AssertMutable can still panic with the
// readonly message. (A correctly-implemented cow.Share never produces
// a readonly share, so this combination is a contract violation; the
// existing safety net handles it.)
func TestDetachIfShared_Readonly_StaysShortCircuit(t *testing.T) {
	st := NewState()
	st.IncCowRefs()
	st.MarkReadOnly()
	origReq := &ExportMetricsServiceRequest{}
	h := &Handle[ExportMetricsServiceRequest]{orig: origReq, state: st}
	preOrig := h.orig
	preState := h.state
	DetachIfShared(h, CopyExportMetricsServiceRequest)
	assert.Same(t, preOrig, h.orig, "readonly+shared: no rebind")
	assert.Same(t, preState, h.state, "readonly+shared: no rebind")
}

// TestDetachIfShared_Shared_BumpsGeneration — the detach path bumps
// the OLD state's generation before rebinding the Handle. The bump is
// the signal a debug-tag nested wrapper would observe to know its
// captured pointer is stale. Today no production code reads generation
// (the debug build tag is unimplemented; see state.go's BumpGeneration
// doc), but the detach contract still includes the bump so the new
// state's reader sees a monotonic counter. This test fails if a future
// refactor accidentally drops the bump call.
func TestDetachIfShared_Shared_BumpsGeneration(t *testing.T) {
	st := NewState()
	st.IncCowRefs()
	preGen := st.Generation()
	origReq := &ExportMetricsServiceRequest{}
	h := &Handle[ExportMetricsServiceRequest]{orig: origReq, state: st}

	DetachIfShared(h, CopyExportMetricsServiceRequest)

	// The OLD state's generation must have been bumped. The post-bump
	// value lives on `st` (the original state we kept a reference to),
	// not on h.state (which now points at a fresh state with
	// generation == 0). Verifies the bump fires on the source of the
	// detach, not on the rebound state.
	assert.Equal(t, preGen+1, st.Generation(),
		"detach must bump the source state's generation")
	assert.Equal(t, uint32(0), h.state.Generation(),
		"rebound state starts at generation 0")
}
