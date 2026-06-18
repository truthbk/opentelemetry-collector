// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build cowproto_prototype

// Package cowproto is a SHELVED, hand-written PROTOTYPE of the wrapper
// design originally considered for the pdata.cow feature gate (see
// perf/rfc/pdata-cow.md, "Alternative A pivot to C with prototype
// findings"). It is gated behind the `cowproto_prototype` build tag so
// it does NOT compile or run in normal builds — paired only with the
// microbench at pdata/xpdata/internal/cowprotobench/ which lives behind
// the same tag.
//
// To rerun the historical bench:
//
//	go test -tags=cowproto_prototype -bench=. -benchtime=2s -count=5 \
//	    ./pdata/xpdata/internal/cowprotobench/...
//
// The RFC at perf/rfc/pdata-cow.md documents why this design was
// rejected (deep-path workload at +23% geomean over baseline → failed
// the RFC's 5%/10% gate). Path Y supersedes it. The code is kept rather
// than deleted so a future engagement that takes another swing at
// deferred-clone can rerun the bench against the same scaffold.
//
// # Design under prototype
//
// Top-level wrappers carry a single *Handle indirection so a detach can
// rebind the {orig, state} pair in place; all wrappers descending from the
// same top-level observe the rebinding on their next access.
//
// Nested wrappers are value-typed and carry the top-level *Handle plus a
// tuple of integer indices that locate this wrapper within the proto tree.
// Their getOrig() walks `handle.orig` + slice-indices on every call —
// O(depth) cache loads per access, but ZERO heap allocation per accessor
// call (which is the load-bearing cost compared to a per-wrapper Handle).
//
// After a detach, `handle.orig` points at the cloned tree. Subsequent
// accessor calls through any descended wrapper resolve indices against
// the NEW tree automatically — no per-wrapper rebinding required.
//
// # Correctness sketch
//
//	md.ResourceMetrics().At(0).Resource().Attributes().PutStr("k","v")
//
// Today the leaf Map's orig is &OldResource.Attributes — captured at
// Resource.Attributes()'s return. Detach inside PutStr would clone the
// tree, but the Map still writes to OldResource.Attributes (the source's
// data) — silent corruption.
//
// Under cowproto the leaf Map carries (h, rmIdx) and resolves
// &h.orig.ResourceMetrics[rmIdx].Resource.Attributes on every call. After
// detach rebinds h.orig, the next *m.getOrig() = append(...) writes to the
// CLONED ResourceMetrics[rmIdx].Resource.Attributes — correct.
//
// # What the bench measures
//
// Apples-to-apples access-cost comparison:
//
//   - Baseline: a metrics tree wrapped by today's pdata wrappers (pmetric.Metrics).
//   - Prototype: the same backing tree wrapped by cowproto.Metrics.
//
// Read patterns:  iterate all resources / scopes / metrics / datapoints / attributes.
// Write patterns: PutStr/SetIntValue on every leaf via the chain.
//
// The RFC's perf gate is 5% target / 10% acceptable. The bench output is
// what decides whether O3.4's codegen migration goes forward as designed
// or pivots.
package cowproto // import "go.opentelemetry.io/collector/pdata/internal/cowproto"
