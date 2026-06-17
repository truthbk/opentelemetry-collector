// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cow // import "go.opentelemetry.io/collector/pdata/xpdata/cow"

import (
	"go.opentelemetry.io/collector/pdata/internal"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// ShareMetrics returns a wrapper that points at the same backing State +
// orig as md, and increments the State's cowRefs counter to record an
// additional copy-on-write reference. The returned wrapper carries its
// own per-wrapper indirection (the Handle, once the codegen migration
// in O3.4 lands) so a mutation on the returned wrapper detaches without
// disturbing md.
//
// Pairs with ReleaseMetrics: every successful ShareMetrics must be
// balanced by exactly one ReleaseMetrics (typically `defer
// cow.ReleaseMetrics(shared)` immediately after the call).
//
// When the pdata.cow feature gate is disabled, ShareMetrics is a no-op
// and returns md unchanged — no refcount manipulation, no allocation.
func ShareMetrics(md pmetric.Metrics) pmetric.Metrics {
	if !FeatureGate.IsEnabled() {
		return md
	}
	w := internal.MetricsWrapper(md)
	state := internal.GetMetricsState(w)
	orig := internal.GetMetricsOrig(w)
	state.IncCowRefs()
	return pmetric.Metrics(internal.NewMetricsWrapper(orig, state))
}

// ReleaseMetrics decrements the cowRefs counter on the backing State.
// The caller is responsible for calling ReleaseMetrics exactly once per
// successful ShareMetrics — extra calls will panic on the State's
// negative-cowRefs guard.
//
// ReleaseMetrics is a no-op when the pdata.cow feature gate is disabled.
func ReleaseMetrics(md pmetric.Metrics) {
	if !FeatureGate.IsEnabled() {
		return
	}
	internal.GetMetricsState(internal.MetricsWrapper(md)).DecCowRefs()
}

// IsSharedMetrics reports whether the backing State has at least one
// outstanding COW share. Used by the detach precondition: a mutation
// path materialises a clone when IsShared is true AND the data is not
// marked readonly.
//
// Returns false when the pdata.cow feature gate is disabled.
func IsSharedMetrics(md pmetric.Metrics) bool {
	if !FeatureGate.IsEnabled() {
		return false
	}
	return internal.GetMetricsState(internal.MetricsWrapper(md)).CowRefs() > 0
}
