// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cow // import "go.opentelemetry.io/collector/pdata/xpdata/cow"

import (
	"go.opentelemetry.io/collector/pdata/internal"
	"go.opentelemetry.io/collector/pdata/plog"
)

// ShareLogs — see ShareMetrics.
func ShareLogs(ld plog.Logs) plog.Logs {
	if !FeatureGate.IsEnabled() {
		return ld
	}
	cloned := plog.NewLogs()
	ld.CopyTo(cloned)
	internal.GetLogsState(internal.LogsWrapper(cloned)).IncCowRefs()
	return cloned
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
