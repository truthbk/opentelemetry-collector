// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cow // import "go.opentelemetry.io/collector/pdata/xpdata/cow"

import (
	"go.opentelemetry.io/collector/pdata/internal"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// ShareTraces — see ShareMetrics for the full doc + Phase 1 limitation.
func ShareTraces(td ptrace.Traces) ptrace.Traces {
	if !FeatureGate.IsEnabled() {
		return td
	}
	sourceOrig := internal.GetTracesOrig(internal.TracesWrapper(td))
	sharedState := internal.NewState()
	sharedState.IncCowRefs()
	return ptrace.Traces(internal.NewTracesWrapper(sourceOrig, sharedState))
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

// DetachTraces — see DetachMetrics for the full doc + Phase 1 contract.
func DetachTraces(td ptrace.Traces) ptrace.Traces {
	if !FeatureGate.IsEnabled() {
		return td
	}
	w := internal.TracesWrapper(td)
	if internal.GetTracesState(w).CowRefs() == 0 {
		return td
	}
	cloned := ptrace.NewTraces()
	td.CopyTo(cloned)
	internal.GetTracesState(w).DecCowRefs()
	return cloned
}
