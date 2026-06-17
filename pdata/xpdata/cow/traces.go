// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cow // import "go.opentelemetry.io/collector/pdata/xpdata/cow"

import (
	"go.opentelemetry.io/collector/pdata/internal"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// ShareTraces — see ShareMetrics.
func ShareTraces(td ptrace.Traces) ptrace.Traces {
	if !FeatureGate.IsEnabled() {
		return td
	}
	cloned := ptrace.NewTraces()
	td.CopyTo(cloned)
	internal.GetTracesState(internal.TracesWrapper(cloned)).IncCowRefs()
	return cloned
}

// ReleaseTraces — see ReleaseMetrics.
func ReleaseTraces(td ptrace.Traces) {
	if !FeatureGate.IsEnabled() {
		return
	}
	internal.GetTracesState(internal.TracesWrapper(td)).DecCowRefs()
}

// IsSharedTraces — see IsSharedMetrics.
func IsSharedTraces(td ptrace.Traces) bool {
	if !FeatureGate.IsEnabled() {
		return false
	}
	return internal.GetTracesState(internal.TracesWrapper(td)).CowRefs() > 0
}
