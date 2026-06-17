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
type State struct {
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

// AssertMutable panics if the state is not StateMutable.
func (st *State) AssertMutable() {
	if st.state&stateReadOnlyBit != 0 {
		panic("invalid access to shared data")
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
