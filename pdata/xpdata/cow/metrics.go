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
// SAFETY MODEL: a consumer that DECLARES MutatesData=true but does not
// actually mutate (the audit's "false-positive mutator" — e.g. a
// transform/filter processor on a no-match batch) works correctly: no
// mutation, no clone, the eager-clone today's fanout would have done is
// skipped. A consumer that ACTUALLY mutates a share without first
// calling cow.DetachMetrics panics at the first mutating accessor via
// state.AssertMutable's cowRefs>0 check (see pdata/internal/state.go).
// Silent corruption is impossible under the gate — the safety net
// (commit 5f2370aac) replaces the deferred-clone correctness gap from
// the original Phase 1 design.
//
// Phases 4-6 of Path Y will replace the explicit DetachX call sites
// with auto-detach codegen so mutators get the deferred-clone benefit
// without explicit opt-in. This commit lands the Path X-hybrid (opt-in
// explicit DetachX with AssertMutable backstop).
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
	wrapper := internal.NewMetricsWrapper(sourceOrig, sharedState)
	// Path Y Phase 5: install the per-signal detacher closure on the
	// shared state. When a nested mutator's prelude calls
	// state.DetachIfShared() and observes cowRefs > 0, it invokes this
	// closure, which deep-clones the orig tree via
	// CopyExportMetricsServiceRequest and rebinds the wrapper's Handle
	// to the new {orig, state} pair. The closure captures the wrapper's
	// Handle by reference (via GetMetricsHandle) so the rebind affects
	// every derived nested/slice wrapper through Handle indirection.
	sharedHandle := internal.GetMetricsHandle(wrapper)
	sharedState.SetDetacher(func() {
		internal.DetachIfShared(sharedHandle, internal.CopyExportMetricsServiceRequest)
	})
	return pmetric.Metrics(wrapper)
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

// DetachMetrics is the explicit copy-on-write detach point for a shared
// wrapper. A consumer that received md via ShareMetrics and is about to
// mutate must call DetachMetrics to obtain an independent wrapper whose
// backing tree it can safely write to.
//
// If md is shared (cowRefs > 0): deep-clones md.orig into a new tree,
// resets cowRefs on the wrapper's State, and returns a new wrapper at
// the cloned tree. The caller's md becomes effectively dead — callers
// should always use the returned value.
//
// If md is not shared: returns md unchanged. This is the fast path on
// the no-mutation branch of a conditional mutator — the caller never
// calls DetachMetrics, no clone happens.
//
// DetachMetrics returns md unchanged when the pdata.cow feature gate is
// disabled (no sharing semantics in effect).
//
// This explicit-detach API is what makes Alt A safe without the full
// auto-detach codegen migration: each conditional mutator (filter,
// transform, attributes with skip-expr, etc.) calls DetachMetrics from
// its mutation branch, never from its no-match branch. The fanout's
// eager clone is fully deferred to first-actual-mutation by any one
// downstream consumer.
func DetachMetrics(md pmetric.Metrics) pmetric.Metrics {
	if !FeatureGate.IsEnabled() {
		return md
	}
	w := internal.MetricsWrapper(md)
	if internal.GetMetricsState(w).CowRefs() == 0 {
		return md
	}
	cloned := pmetric.NewMetrics()
	md.CopyTo(cloned)
	internal.GetMetricsState(w).DecCowRefs()
	return cloned
}
