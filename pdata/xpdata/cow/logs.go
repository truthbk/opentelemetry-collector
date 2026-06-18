// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cow // import "go.opentelemetry.io/collector/pdata/xpdata/cow"

import (
	"go.opentelemetry.io/collector/pdata/internal"
	"go.opentelemetry.io/collector/pdata/plog"
)

// ShareLogs — see ShareMetrics for the full doc + safety model.
func ShareLogs(ld plog.Logs) plog.Logs {
	if !FeatureGate.IsEnabled() {
		return ld
	}
	sourceOrig := internal.GetLogsOrig(internal.LogsWrapper(ld))
	sharedState := internal.NewState()
	sharedState.IncCowRefs()
	// TODO Path Y Phase 5: install per-signal detacher closure — see ShareMetrics.
	return plog.Logs(internal.NewLogsWrapper(sourceOrig, sharedState))
}

// ReleaseLogs — see ReleaseMetrics.
func ReleaseLogs(ld plog.Logs) {
	if !FeatureGate.IsEnabled() {
		return
	}
	internal.GetLogsState(internal.LogsWrapper(ld)).DecCowRefs()
}

// IsSharedLogs — see IsSharedMetrics.
func IsSharedLogs(ld plog.Logs) bool {
	if !FeatureGate.IsEnabled() {
		return false
	}
	return internal.GetLogsState(internal.LogsWrapper(ld)).CowRefs() > 0
}

// DetachLogs — see DetachMetrics for the full doc + safety model.
func DetachLogs(ld plog.Logs) plog.Logs {
	if !FeatureGate.IsEnabled() {
		return ld
	}
	w := internal.LogsWrapper(ld)
	if internal.GetLogsState(w).CowRefs() == 0 {
		return ld
	}
	cloned := plog.NewLogs()
	ld.CopyTo(cloned)
	internal.GetLogsState(w).DecCowRefs()
	return cloned
}
