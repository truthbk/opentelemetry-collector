// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal // import "go.opentelemetry.io/collector/pdata/internal"
import (
	"sync/atomic"
)

// State defines an ownership state of pmetric.Metrics, plog.Logs, ptrace.Traces or pprofile.Profiles.
//
// Two refcount fields:
//
//   - refs counts pipeline-ownership references, manipulated by pref.Ref/Unref
//     and gated on pdata.enableRefCounting (today). One increment per pipeline
//     that holds the data; decremented at pipeline exit.
//   - cowRefs counts copy-on-write shares, manipulated by xpdata/cow.Share /
//     cow.Release / detachIfShared, and gated on pdata.cow. One increment per
//     fanout-shared consumer; decremented when the consumer's processing
//     finishes (via Release) or when a mutation triggers detach.
//
// The two counters are independent on purpose: see perf/rfc/pdata-cow.md
// "Refcount: separate cowRefs field" for the reasoning. In short, sharing the
// counter would let detach silently absorb readonly contract violations.
//
// generation is bumped by detach and read by nested-wrapper accessors in
// debug builds (pdatacowdebug build tag). It surfaces the "captured nested
// wrapper held across a mutation" contract violation as a development-time
// panic; release builds skip the check entirely.
//
// detach is the per-signal detach closure. Set ONLY by cow.ShareX (which
// produces a shared State); never set by NewXWrapper. This keeps freshly-
// constructed non-shared States identical at reflect.DeepEqual level —
// critical for the existing wire-compatibility tests that compare a
// round-tripped wrapper against its source via assert.Equal.
type State struct {
	detach     func()
	refs       atomic.Int32
	cowRefs    atomic.Int32
	state      uint32
	generation atomic.Uint32
}

const (
	defaultState          uint32 = 0
	stateReadOnlyBit             = uint32(1 << 0)
	statePipelineOwnedBit        = uint32(1 << 1)
)

func NewState() *State {
	st := &State{
		state: defaultState,
	}
	st.refs.Store(1)
	return st
}

func (st *State) MarkReadOnly() {
	st.state |= stateReadOnlyBit
}

func (st *State) IsReadOnly() bool {
	return st.state&stateReadOnlyBit != 0
}

// SetDetacher installs the per-signal detach closure on this State.
// Called by cow.ShareX after constructing the shared State; never
// called by NewXWrapper. The closure captures the per-signal Handle +
// cloneFn pair so any wrapper that derived its State pointer from
// this one can trigger detach without knowing the proto type T.
func (st *State) SetDetacher(detach func()) {
	st.detach = detach
}

// DetachIfShared is the codegen-injected prelude for nested mutators.
// If cowRefs > 0 AND a per-signal detacher is installed, invoke it to
// rebind the top-level Handle's orig + state to a freshly-cloned pair.
// On the gate-off / unshared path, the detacher is nil and this is
// effectively cowRefs.Load() + branch — zero allocation.
func (st *State) DetachIfShared() {
	if st.cowRefs.Load() == 0 {
		return
	}
	if st.detach != nil {
		st.detach()
	}
}

// AssertMutable panics if the state is not StateMutable.
//
// Under the pdata.cow feature gate, a State that carries an outstanding
// COW share (cowRefs > 0) is also non-mutable from the share's
// perspective — the share's backing tree is pointed at by other
// wrappers (the source + sibling shares), and a direct mutation would
// corrupt their view. Consumers that need to mutate a share MUST
// detach first (cow.DetachX in xpdata/cow), which produces an
// independent wrapper whose State has cowRefs == 0.
//
// AssertMutable panics on cowRefs > 0 to surface contract violations
// loudly during development/testing rather than silently corrupting
// the source's data. The cowRefs check is gated so it has zero impact
// when the cow feature is off (cowRefs stays 0 in that path).
func (st *State) AssertMutable() {
	if st.state&stateReadOnlyBit != 0 {
		panic("invalid access to shared data")
	}
	if st.cowRefs.Load() > 0 {
		panic("invalid access to cow-shared data: caller must call cow.Detach* before mutating (see pdata/xpdata/cow)")
	}
}

// MarkPipelineOwned marks the data as owned by the pipeline, returns true if the data were
// previously not owned by the pipeline, otherwise false.
func (st *State) MarkPipelineOwned() bool {
	if st.state&statePipelineOwnedBit != 0 {
		return false
	}
	st.state |= statePipelineOwnedBit
	return true
}

// Ref add one to the count of active references.
func (st *State) Ref() {
	st.refs.Add(1)
}

// Unref returns true if reference count got to 0 which means no more active references,
// otherwise it returns false.
func (st *State) Unref() bool {
	refs := st.refs.Add(-1)
	switch {
	case refs > 0:
		return false
	case refs == 0:
		return true
	default:
		panic("Cannot unref freed data")
	}
}

// IncCowRefs increments the copy-on-write share counter. Called by
// xpdata/cow.Share when the fanout consumer creates a shared wrapper for a
// downstream consumer. See perf/rfc/pdata-cow.md.
func (st *State) IncCowRefs() {
	st.cowRefs.Add(1)
}

// DecCowRefs decrements the COW share counter and returns the post-decrement
// value. Called by xpdata/cow.Release when a Share's lifecycle ends, and by
// the detach path when a mutation materialises a private clone (and releases
// the current Share from the original State).
func (st *State) DecCowRefs() int32 {
	v := st.cowRefs.Add(-1)
	if v < 0 {
		panic("pdata: cowRefs decremented below zero — unbalanced Share/Release")
	}
	return v
}

// CowRefs returns the current number of active COW shares. The detach
// precondition is cowRefs > 0 AND state has no readonly bit set.
func (st *State) CowRefs() int32 {
	return st.cowRefs.Load()
}

// BumpGeneration is called by a successful detach. The post-bump value is
// stored on every nested wrapper at its creation time; under the
// pdatacowdebug build tag, accessors compare captured generation against
// current and panic on mismatch — surfacing the contract that captured
// nested wrappers are invalid across a mutation that triggered detach.
func (st *State) BumpGeneration() {
	st.generation.Add(1)
}

// Generation returns the current detach-generation counter on this State.
// Used by nested-wrapper accessors under pdatacowdebug to enforce the
// "no use after detach" contract.
func (st *State) Generation() uint32 {
	return st.generation.Load()
}
