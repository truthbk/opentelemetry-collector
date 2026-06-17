// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cow // import "go.opentelemetry.io/collector/pdata/xpdata/cow"

import (
	"go.opentelemetry.io/collector/pdata/internal"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// ShareMetrics returns an independent pmetric.Metrics that the caller
// can mutate without affecting md.
//
// # Current behaviour: eager clone
//
// Under the pdata.cow feature gate as currently shipped, ShareMetrics
// does an eager CopyTo — the returned wrapper has its own State and
// orig tree, and `cowRefs` is bumped only for observability bookkeeping.
// This matches today's fanout `cloneMetrics(md)` semantics; ShareMetrics
// is a renamed, gated entry point that future deferred-clone work can
// repurpose without API changes.
//
// The original deferred-clone design (Alternative A in perf/rfc/pdata-
// cow.md) was prototyped under pdata/internal/cowproto and benched in
// pdata/xpdata/internal/cowprotobench. The wrapper-layer overhead
// measured 22-67% on deep-path access while the deferred-clone benefit
// fires only in narrow post-O2 scenarios — net production impact was
// flat-to-negative. The pivot to eager-clone preserves the gate, the
// xpdata/cow API surface, the State.cowRefs/generation fields, and the
// observability hooks as scaffolding for a future engagement that
// designs a working deferred mechanism (e.g. per-subtree COW).
//
// Pairs with ReleaseMetrics: every successful ShareMetrics must be
// balanced by exactly one ReleaseMetrics (typically `defer
// cow.ReleaseMetrics(shared)` immediately after the call).
//
// When the pdata.cow feature gate is disabled, ShareMetrics returns md
// unchanged — callers are expected to fall back to their pre-gate
// cloning strategy (cloneMetrics in fanout, etc.) on the gate-off path.
func ShareMetrics(md pmetric.Metrics) pmetric.Metrics {
	if !FeatureGate.IsEnabled() {
		return md
	}
	cloned := pmetric.NewMetrics()
	md.CopyTo(cloned)
	internal.GetMetricsState(internal.MetricsWrapper(cloned)).IncCowRefs()
	return cloned
}

// ReleaseMetrics decrements the cowRefs counter on the cloned wrapper's
// State. Under the eager-clone semantics this is purely bookkeeping —
// the cloned State has its own refcount and isn't observed by the
// source — but the explicit Release is preserved so call sites match
// the eventual deferred-clone API shape.
//
// ReleaseMetrics is a no-op when the pdata.cow feature gate is disabled.
func ReleaseMetrics(md pmetric.Metrics) {
	if !FeatureGate.IsEnabled() {
		return
	}
	internal.GetMetricsState(internal.MetricsWrapper(md)).DecCowRefs()
}

// IsSharedMetrics reports whether the backing State has at least one
// outstanding COW share. Under the eager-clone semantics this is only
// true between a successful ShareMetrics and its paired ReleaseMetrics
// — useful for observability or contract assertions in test code.
//
// Returns false when the pdata.cow feature gate is disabled.
func IsSharedMetrics(md pmetric.Metrics) bool {
	if !FeatureGate.IsEnabled() {
		return false
	}
	return internal.GetMetricsState(internal.MetricsWrapper(md)).CowRefs() > 0
}
