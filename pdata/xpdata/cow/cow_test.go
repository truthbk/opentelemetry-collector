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

func TestShareMetrics_GateEnabled_BumpsCowRefs(t *testing.T) {
	withGate(t)
	md := pmetric.NewMetrics()
	require.False(t, IsSharedMetrics(md))

	shared := ShareMetrics(md)
	assert.True(t, IsSharedMetrics(md))
	assert.True(t, IsSharedMetrics(shared))

	ReleaseMetrics(shared)
	assert.False(t, IsSharedMetrics(md))
}

func TestShareMetrics_GateEnabled_MultipleShares(t *testing.T) {
	withGate(t)
	md := pmetric.NewMetrics()

	s1 := ShareMetrics(md)
	s2 := ShareMetrics(md)
	s3 := ShareMetrics(md)
	assert.True(t, IsSharedMetrics(md))

	ReleaseMetrics(s1)
	assert.True(t, IsSharedMetrics(md), "still shared after one release of three")
	ReleaseMetrics(s2)
	assert.True(t, IsSharedMetrics(md), "still shared after two releases of three")
	ReleaseMetrics(s3)
	assert.False(t, IsSharedMetrics(md), "no longer shared after final release")
}

func TestReleaseMetrics_UnpairedRelease_Panics(t *testing.T) {
	withGate(t)
	md := pmetric.NewMetrics()
	// No matching Share — cowRefs would go negative; the State guard panics.
	assert.Panics(t, func() { ReleaseMetrics(md) })
}

func TestShareTraces_GateEnabled_BumpsCowRefs(t *testing.T) {
	withGate(t)
	td := ptrace.NewTraces()
	require.False(t, IsSharedTraces(td))

	shared := ShareTraces(td)
	assert.True(t, IsSharedTraces(td))
	assert.True(t, IsSharedTraces(shared))

	ReleaseTraces(shared)
	assert.False(t, IsSharedTraces(td))
}

func TestShareLogs_GateEnabled_BumpsCowRefs(t *testing.T) {
	withGate(t)
	ld := plog.NewLogs()
	require.False(t, IsSharedLogs(ld))

	shared := ShareLogs(ld)
	assert.True(t, IsSharedLogs(ld))
	assert.True(t, IsSharedLogs(shared))

	ReleaseLogs(shared)
	assert.False(t, IsSharedLogs(ld))
}

func TestShareProfiles_GateEnabled_BumpsCowRefs(t *testing.T) {
	withGate(t)
	pd := pprofile.NewProfiles()
	require.False(t, IsSharedProfiles(pd))

	shared := ShareProfiles(pd)
	assert.True(t, IsSharedProfiles(pd))
	assert.True(t, IsSharedProfiles(shared))

	ReleaseProfiles(shared)
	assert.False(t, IsSharedProfiles(pd))
}
