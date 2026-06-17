// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cow // import "go.opentelemetry.io/collector/pdata/xpdata/cow"

import (
	"go.opentelemetry.io/collector/pdata/internal"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// ShareMetrics returns a pmetric.Metrics wrapper that points at the same
// backing orig tree as md, but with its OWN freshly-allocated *State.
// This is the "deferred clone" semantics of Alternative A (see
// perf/rfc/pdata-cow.md):
//
//   - No allocation of the orig tree (the expensive part). The returned
//     wrapper shares all of md's resource/scope/metric/datapoint
//     structures by pointer.
//   - The new State has cowRefs set to 1, identifying this wrapper as a
//     share. Detach (at the wrapper's first mutating call, once the
//     codegen migration lands) examines this counter to decide whether
//     to clone the orig.
//   - The source's State is untouched. A MarkReadOnly broadcast on the
//     source does not affect this share's mutability.
//
// CURRENT LIMITATION (Phase 1 validation, before codegen migration):
// Auto-detach-on-mutation is NOT wired into the leaf wrapper accessors.
// A consumer that DECLARES MutatesData=true but does not actually mutate
// (the audit's "false-positive mutator" — e.g. a transform/filter
// processor on a no-match batch) works correctly: no mutation, no
// corruption, the clone today's fanout would have done is skipped. A
// consumer that ACTUALLY mutates writes to the source's backing tree
// (corruption). Phase 2 adds the codegen migration that makes auto-
// detach safe for real mutators; Phase 1 is for measuring whether the
// deferred-clone benefit is real at the pipeline level before
// committing to that work.
//
// When the pdata.cow feature gate is disabled, ShareMetrics returns md
// unchanged.
func ShareMetrics(md pmetric.Metrics) pmetric.Metrics {
	if !FeatureGate.IsEnabled() {
		return md
	}
	sourceOrig := internal.GetMetricsOrig(internal.MetricsWrapper(md))
	sharedState := internal.NewState()
	sharedState.IncCowRefs()
	return pmetric.Metrics(internal.NewMetricsWrapper(sourceOrig, sharedState))
}

// ReleaseMetrics decrements the cowRefs counter on the share's State.
// Under Phase 1 share-semantics, this is bookkeeping for observability
// (IsSharedMetrics, future pdata_cow_detach_total counter). The
// orig-tree ownership is GC-driven — when no wrapper references the
// source's orig, it gets collected naturally.
//
// ReleaseMetrics is a no-op when the pdata.cow feature gate is disabled.
func ReleaseMetrics(md pmetric.Metrics) {
	if !FeatureGate.IsEnabled() {
		return
	}
	internal.GetMetricsState(internal.MetricsWrapper(md)).DecCowRefs()
}

// IsSharedMetrics reports whether the wrapper's State carries an active
// COW share (cowRefs > 0).
//
// Returns false when the pdata.cow feature gate is disabled.
func IsSharedMetrics(md pmetric.Metrics) bool {
	if !FeatureGate.IsEnabled() {
		return false
	}
	return internal.GetMetricsState(internal.MetricsWrapper(md)).CowRefs() > 0
}
