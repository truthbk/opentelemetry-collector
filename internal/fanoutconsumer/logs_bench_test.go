// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package fanoutconsumer

import (
	"context"
	"fmt"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/testdata"
)

// mutatingNopLogs — see mutatingNopMetrics for the design notes
// (declares MutatesData: true, does not actually mutate the input).
type mutatingNopLogs struct{ consumer.Logs }

func (mutatingNopLogs) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

type logsShape struct {
	name string
	gen  func() plog.Logs
}

func logsShapes() []logsShape {
	return []logsShape{
		{"small_10", func() plog.Logs { return testdata.GenerateLogs(10) }},
		{"medium_1k", func() plog.Logs { return testdata.GenerateLogs(1000) }},
		{"large_10k", func() plog.Logs { return testdata.GenerateLogs(10000) }},
		{"rich_100x5x50x10", func() plog.Logs { return testdata.GenerateLogsManyResources(100, 5, 50, 10) }},
	}
}

func buildLogsMix(name string, n int) []consumer.Logs {
	cs := make([]consumer.Logs, 0, n)
	for i := range n {
		var add consumer.Logs
		switch name {
		case "all_mut":
			add = mutatingNopLogs{Logs: consumertest.NewNop()}
		case "all_ro":
			add = consumertest.NewNop()
		case "half":
			if i < n/2 {
				add = mutatingNopLogs{Logs: consumertest.NewNop()}
			} else {
				add = consumertest.NewNop()
			}
		case "one_mut_rest_ro":
			if i == 0 {
				add = mutatingNopLogs{Logs: consumertest.NewNop()}
			} else {
				add = consumertest.NewNop()
			}
		default:
			panic("unknown mix: " + name)
		}
		cs = append(cs, add)
	}
	return cs
}

// BenchmarkLogsFanout — see BenchmarkMetricsFanout for the grid and
// reading guide; the log and metric benchmarks are symmetric.
func BenchmarkLogsFanout(b *testing.B) {
	ns := []int{1, 2, 4, 8, 16}
	mixes := []string{"all_mut", "all_ro", "half", "one_mut_rest_ro"}
	shapes := logsShapes()
	ctx := context.Background()

	for _, shape := range shapes {
		for _, mix := range mixes {
			for _, n := range ns {
				name := fmt.Sprintf("n=%d/mix=%s/shape=%s", n, mix, shape.name)
				b.Run(name, func(b *testing.B) {
					ld := shape.gen()
					fanout := NewLogs(buildLogsMix(mix, n))
					b.ReportAllocs()
					b.SetBytes(int64(ld.LogRecordCount())) // items/op, see BenchmarkMetricsFanout
					for b.Loop() {
						if err := fanout.ConsumeLogs(ctx, ld); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
