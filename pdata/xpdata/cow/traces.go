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
	w := internal.TracesWrapper(td)
	state := internal.GetTracesState(w)
	orig := internal.GetTracesOrig(w)
	state.IncCowRefs()
	return ptrace.Traces(internal.NewTracesWrapper(orig, state))
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
