// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package graph

import (
	"os"
	"testing"

	"go.uber.org/goleak"

	"go.opentelemetry.io/collector/featuregate"
)

// TestMain wires the standard goleak verifier and, if PDATA_COW_GATE_ON=1
// is set in the env, flips the pdata.cow feature gate ON for the
// duration of this package's tests + benchmarks. Used to A/B the
// fanout's gate-OFF vs gate-ON paths from the same test binary.
func TestMain(m *testing.M) {
	if os.Getenv("PDATA_COW_GATE_ON") == "1" {
		if err := featuregate.GlobalRegistry().Set("pdata.cow", true); err != nil {
			panic(err)
		}
	}
	goleak.VerifyTestMain(m)
}
