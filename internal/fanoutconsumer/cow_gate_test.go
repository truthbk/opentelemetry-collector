// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package fanoutconsumer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/featuregate"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pprofile"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/xpdata/cow"
)

// withGate flips the pdata.cow gate ON for the duration of the test and
// restores its prior value on cleanup. Used to assert the gated branch
// of cloneX without relying on the process-wide PDATA_COW_GATE_ON env
// hook (which is only checked at TestMain).
func withGate(t *testing.T) {
	t.Helper()
	prev := cow.FeatureGate.IsEnabled()
	require.NoError(t, featuregate.GlobalRegistry().Set(cow.FeatureGate.ID(), true))
	t.Cleanup(func() {
		require.NoError(t, featuregate.GlobalRegistry().Set(cow.FeatureGate.ID(), prev))
	})
}

// TestClone_GateOFF_DeepClones — the default gate-OFF path goes through
// pmetric.NewMetrics + CopyTo. The result is byte-equal to the source
// data-wise but the wrapper is NOT a cow share (IsShared returns false).
// This is the today's-fanout behavior; the gate flip must not break it.
func TestClone_GateOFF_DeepClones(t *testing.T) {
	// Explicitly leave the gate alone (process-default OFF unless
	// PDATA_COW_GATE_ON=1 is set; this test would skip cleanly under
	// the env override). The asserted contract is "if gate is OFF,
	// cloneMetrics deep-clones".
	if cow.FeatureGate.IsEnabled() {
		t.Skip("gate is enabled at process level; this test exercises gate-OFF only")
	}

	t.Run("metrics", func(t *testing.T) {
		md := pmetric.NewMetrics()
		md.ResourceMetrics().AppendEmpty().Resource().Attributes().PutStr("k", "v")
		got := cloneMetrics(md)
		assert.False(t, cow.IsSharedMetrics(got), "gate-OFF clone must NOT be a cow share")
	})
	t.Run("traces", func(t *testing.T) {
		td := ptrace.NewTraces()
		td.ResourceSpans().AppendEmpty().Resource().Attributes().PutStr("k", "v")
		got := cloneTraces(td)
		assert.False(t, cow.IsSharedTraces(got))
	})
	t.Run("logs", func(t *testing.T) {
		ld := plog.NewLogs()
		ld.ResourceLogs().AppendEmpty().Resource().Attributes().PutStr("k", "v")
		got := cloneLogs(ld)
		assert.False(t, cow.IsSharedLogs(got))
	})
	t.Run("profiles", func(t *testing.T) {
		pd := pprofile.NewProfiles()
		pd.ResourceProfiles().AppendEmpty().Resource().Attributes().PutStr("k", "v")
		got := cloneProfiles(pd)
		assert.False(t, cow.IsSharedProfiles(got))
	})
}

// TestClone_GateON_Shares — under the pdata.cow gate, cloneX routes
// through cow.ShareX which produces a share wrapper (cowRefs > 0) at
// the SAME backing tree as the source — no proto allocation, no
// CopyTo. This is the load-bearing assertion that the gate-ON path
// actually exercises share semantics rather than silently falling back
// to the deep-clone path.
//
// Round 2 review flagged that the env hook (PDATA_COW_GATE_ON) flips
// behavior but no test asserts cloneX's branch on it. This test closes
// that gap for all 4 signals.
func TestClone_GateON_Shares(t *testing.T) {
	withGate(t)

	t.Run("metrics", func(t *testing.T) {
		md := pmetric.NewMetrics()
		md.ResourceMetrics().AppendEmpty().Resource().Attributes().PutStr("k", "v")
		got := cloneMetrics(md)
		assert.True(t, cow.IsSharedMetrics(got),
			"gate-ON cloneMetrics must produce a share, not a deep clone")
		// Source is independent — Share doesn't bump cowRefs on it.
		assert.False(t, cow.IsSharedMetrics(md), "source remains non-shared")
	})
	t.Run("traces", func(t *testing.T) {
		td := ptrace.NewTraces()
		td.ResourceSpans().AppendEmpty().Resource().Attributes().PutStr("k", "v")
		got := cloneTraces(td)
		assert.True(t, cow.IsSharedTraces(got))
		assert.False(t, cow.IsSharedTraces(td))
	})
	t.Run("logs", func(t *testing.T) {
		ld := plog.NewLogs()
		ld.ResourceLogs().AppendEmpty().Resource().Attributes().PutStr("k", "v")
		got := cloneLogs(ld)
		assert.True(t, cow.IsSharedLogs(got))
		assert.False(t, cow.IsSharedLogs(ld))
	})
	t.Run("profiles", func(t *testing.T) {
		pd := pprofile.NewProfiles()
		pd.ResourceProfiles().AppendEmpty().Resource().Attributes().PutStr("k", "v")
		got := cloneProfiles(pd)
		assert.True(t, cow.IsSharedProfiles(got))
		assert.False(t, cow.IsSharedProfiles(pd))
	})
}
