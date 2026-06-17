// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package cow exposes the public API for the pdata copy-on-write feature
// gate (pdata.cow). The intended caller is the fanout consumer in
// internal/fanoutconsumer/ — see perf/rfc/pdata-cow.md for the design.
//
// API shape mirrors xpdata/pref (the pipeline-ownership refcount): one
// Share / Release / IsShared trio per signal (metrics / traces / logs /
// profiles), each operating on the existing typed wrapper.
//
// All functions in this package are no-ops when pdata.cow is disabled.
// Callers should NOT condition their calls on IsEnabled themselves; the
// package handles the gate internally so the call sites stay readable.
package cow // import "go.opentelemetry.io/collector/pdata/xpdata/cow"

import (
	"go.opentelemetry.io/collector/pdata/xpdata/internal/metadata"
)

// FeatureGate is re-exported so external tooling (tests, debug commands)
// can branch on the gate's state without re-importing the internal
// metadata package. Mirrors xpdata/pref.UseProtoPooling.
var FeatureGate = metadata.PdataCowFeatureGate
