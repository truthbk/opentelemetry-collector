// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package cowproto is a hand-written PROTOTYPE of the wrapper design under
// consideration for the pdata.cow feature gate (see perf/rfc/pdata-cow.md).
// It is not used by any production code — its only purpose is to host the
// microbenchmark that compares the prototype's access patterns against
// today's pdata wrappers.
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
