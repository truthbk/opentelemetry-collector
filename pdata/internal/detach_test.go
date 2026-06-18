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

// NOTE: A TestDetachMetricsIfShared_PerSignalWrapper test was previously
// here to exercise the per-signal entry point that codegen-injected
// prelude calls. That helper (DetachMetricsIfShared) is emitted by the
// templates as part of Path Y Phase 4 (template update for Handle
// layout). Phase 2 reverted the hand-added helper since it has no
// corresponding template emission yet; the test will be reinstated in
// Phase 4 when the helper is generated correctly.
