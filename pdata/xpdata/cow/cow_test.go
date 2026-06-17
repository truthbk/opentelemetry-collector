// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/featuregate"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pprofile"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// withGate flips the pdata.cow feature gate to enabled for the duration of
// the test, and restores its prior value on cleanup. The gate is Alpha and
// off by default, so most test files need this helper to exercise the
// non-no-op paths.
func withGate(t *testing.T) {
	t.Helper()
	prev := FeatureGate.IsEnabled()
	require.NoError(t, featuregate.GlobalRegistry().Set(FeatureGate.ID(), true))
	t.Cleanup(func() {
		require.NoError(t, featuregate.GlobalRegistry().Set(FeatureGate.ID(), prev))
	})
}

func TestShareMetrics_GateDisabled_IsNoOp(t *testing.T) {
	md := pmetric.NewMetrics()
	shared := ShareMetrics(md)
	assert.False(t, IsSharedMetrics(md))
	assert.False(t, IsSharedMetrics(shared))
	// Release under disabled gate must not panic even without a paired Share.
	assert.NotPanics(t, func() { ReleaseMetrics(md) })
}

// TestShareMetrics_GateEnabled_BumpsCowRefs — under the eager-clone
// semantics the SOURCE is never marked shared; only the cloned wrapper
// carries cowRefs. The source's lifecycle is independent.
func TestShareMetrics_GateEnabled_BumpsCowRefs(t *testing.T) {
	withGate(t)
	md := pmetric.NewMetrics()
	require.False(t, IsSharedMetrics(md))

	shared := ShareMetrics(md)
	assert.False(t, IsSharedMetrics(md), "source is independent under eager-clone")
	assert.True(t, IsSharedMetrics(shared), "clone has cowRefs bumped")

	ReleaseMetrics(shared)
	assert.False(t, IsSharedMetrics(shared), "clone no longer marked shared after release")
	assert.False(t, IsSharedMetrics(md))
}

// TestShareMetrics_GateEnabled_MultipleShares — each call produces an
// independent clone with its own cowRefs counter. Releases on one clone
// don't affect the others.
func TestShareMetrics_GateEnabled_MultipleShares(t *testing.T) {
	withGate(t)
	md := pmetric.NewMetrics()

	s1 := ShareMetrics(md)
	s2 := ShareMetrics(md)
	s3 := ShareMetrics(md)
	assert.False(t, IsSharedMetrics(md), "source remains independent")
	assert.True(t, IsSharedMetrics(s1))
	assert.True(t, IsSharedMetrics(s2))
	assert.True(t, IsSharedMetrics(s3))

	ReleaseMetrics(s1)
	assert.False(t, IsSharedMetrics(s1))
	assert.True(t, IsSharedMetrics(s2), "independent clone unaffected")
	assert.True(t, IsSharedMetrics(s3))
	ReleaseMetrics(s2)
	ReleaseMetrics(s3)
}

func TestReleaseMetrics_UnpairedRelease_Panics(t *testing.T) {
	withGate(t)
	md := pmetric.NewMetrics()
	// No matching Share — cowRefs would go negative; the State guard panics.
	assert.Panics(t, func() { ReleaseMetrics(md) })
}

// TestDetachMetrics_GateDisabled_IsPassthrough — DetachMetrics is a no-op
// under gate-off; the returned wrapper is exactly md (same backing).
func TestDetachMetrics_GateDisabled_IsPassthrough(t *testing.T) {
	md := pmetric.NewMetrics()
	got := DetachMetrics(md)
	// Same backing State + orig → equivalent wrapper values.
	assert.Equal(t, md, got)
}

// TestDetachMetrics_NotShared_IsPassthrough — under gate ON, calling
// Detach on a non-share returns the wrapper unchanged. This is the fast
// path for a conditional mutator that DOES enter its mutation branch
// but happens to receive a non-shared wrapper (e.g. fanout broadcast
// to a single mutator with no other consumers).
func TestDetachMetrics_NotShared_IsPassthrough(t *testing.T) {
	withGate(t)
	md := pmetric.NewMetrics()
	got := DetachMetrics(md)
	assert.Equal(t, md, got, "non-shared wrapper returned unchanged")
}

// TestDetachMetrics_Shared_DeepClonesAndDecrements — the load-bearing
// detach behaviour: a share's mutation branch calls Detach, receives an
// independent wrapper backed by a fresh proto tree, and the share's
// cowRefs is decremented (so a paired Release would be a no-op or
// caller can skip the Release on the detached value).
//
// Verification approach: mutate the detached wrapper, observe that the
// shared wrapper's backing tree is unchanged. That's the real invariant
// — wrapper-value equality / pointer comparison is fragile under proto
// pooling (pmetric.NewMetrics() reuses a pooled *Request, which can
// coincidentally yield the same pointer the share already held).
func TestDetachMetrics_Shared_DeepClonesAndDecrements(t *testing.T) {
	withGate(t)
	md := pmetric.NewMetrics()
	md.ResourceMetrics().AppendEmpty().Resource().Attributes().PutStr("k", "v")

	shared := ShareMetrics(md)
	require.True(t, IsSharedMetrics(shared))

	detached := DetachMetrics(shared)
	assert.False(t, IsSharedMetrics(detached), "detached wrapper is independent")

	// Backing data is preserved through detach.
	v, ok := detached.ResourceMetrics().At(0).Resource().Attributes().Get("k")
	require.True(t, ok)
	assert.Equal(t, "v", v.Str())

	// Mutating the detached wrapper does not leak into the share's view
	// (proves the deep-clone actually happened — they have independent
	// backing trees).
	detached.ResourceMetrics().At(0).Resource().Attributes().PutStr("k", "mutated")

	v, ok = detached.ResourceMetrics().At(0).Resource().Attributes().Get("k")
	require.True(t, ok)
	assert.Equal(t, "mutated", v.Str())

	v, ok = shared.ResourceMetrics().At(0).Resource().Attributes().Get("k")
	require.True(t, ok)
	assert.Equal(t, "v", v.Str(), "shared wrapper's backing tree is independent of detached's mutation")
}

func TestDetachTraces(t *testing.T) {
	withGate(t)
	td := ptrace.NewTraces()
	shared := ShareTraces(td)
	require.True(t, IsSharedTraces(shared))
	detached := DetachTraces(shared)
	assert.False(t, IsSharedTraces(detached))
}

func TestDetachLogs(t *testing.T) {
	withGate(t)
	ld := plog.NewLogs()
	shared := ShareLogs(ld)
	require.True(t, IsSharedLogs(shared))
	detached := DetachLogs(shared)
	assert.False(t, IsSharedLogs(detached))
}

func TestDetachProfiles(t *testing.T) {
	withGate(t)
	pd := pprofile.NewProfiles()
	shared := ShareProfiles(pd)
	require.True(t, IsSharedProfiles(shared))
	detached := DetachProfiles(shared)
	assert.False(t, IsSharedProfiles(detached))
}

// TestSafetyNet_MutateSharedWithoutDetach_Panics — the contract-violation
// safety net: a consumer that declares MutatesData=true and mutates a
// share without first calling cow.Detach* gets a clear panic at the
// AssertMutable callsite, rather than silently corrupting the source's
// backing tree. This is what makes Path X (opt-in explicit detach)
// safe to ship — operators integrating a new processor will see the
// panic during testing.
func TestSafetyNet_MutateSharedWithoutDetach_Panics(t *testing.T) {
	withGate(t)
	md := pmetric.NewMetrics()
	shared := ShareMetrics(md)
	require.True(t, IsSharedMetrics(shared))
	// Skip the Detach call. Any mutation should panic at AssertMutable.
	assert.PanicsWithValue(t,
		"invalid access to cow-shared data: caller must call cow.Detach* before mutating (see pdata/xpdata/cow)",
		func() {
			shared.ResourceMetrics().AppendEmpty()
		},
	)
}

func TestShareTraces_GateEnabled_BumpsCowRefs(t *testing.T) {
	withGate(t)
	td := ptrace.NewTraces()
	require.False(t, IsSharedTraces(td))

	shared := ShareTraces(td)
	assert.False(t, IsSharedTraces(td))
	assert.True(t, IsSharedTraces(shared))

	ReleaseTraces(shared)
	assert.False(t, IsSharedTraces(shared))
}

func TestShareLogs_GateEnabled_BumpsCowRefs(t *testing.T) {
	withGate(t)
	ld := plog.NewLogs()
	require.False(t, IsSharedLogs(ld))

	shared := ShareLogs(ld)
	assert.False(t, IsSharedLogs(ld))
	assert.True(t, IsSharedLogs(shared))

	ReleaseLogs(shared)
	assert.False(t, IsSharedLogs(shared))
}

func TestShareProfiles_GateEnabled_BumpsCowRefs(t *testing.T) {
	withGate(t)
	pd := pprofile.NewProfiles()
	require.False(t, IsSharedProfiles(pd))

	shared := ShareProfiles(pd)
	assert.False(t, IsSharedProfiles(pd))
	assert.True(t, IsSharedProfiles(shared))

	ReleaseProfiles(shared)
	assert.False(t, IsSharedProfiles(shared))
}
