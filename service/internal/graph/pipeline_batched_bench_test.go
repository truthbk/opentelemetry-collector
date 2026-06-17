// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package graph

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/connector"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/testdata"
	"go.opentelemetry.io/collector/pipeline"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/service/internal/builders"
	"go.opentelemetry.io/collector/service/internal/testcomponents"
	"go.opentelemetry.io/collector/service/pipelines"
)

// buildBatchedPipeline wires a receiver → fanout → list of exporters where each
// exporter's ID name controls whether it declares MutatesData: true (any name
// starting with "batched" — see testcomponents.ExampleExporter) or false. This
// is used by the BenchmarkPipelineFanoutBatched* benches to simulate the
// exporterhelper queue+batch's MutatesData: true injection (audit finding #19)
// without depending on the actual exporterhelper queue+batch wiring.
//
// `exporterNames` is the list of ID names to use; an empty string maps to a
// default-named exampleexporter (MutatesData: false), while "batched" or any
// "batched*" name maps to a MutatesData: true variant. The returned graph and
// receiver are usable for ConsumeXxxFunc-driven bench loops.
func buildBatchedPipeline(ctx context.Context, b *testing.B, signal pipeline.Signal, exporterNames ...string) *Graph {
	b.Helper()

	receiverID := component.MustNewID("examplereceiver")
	receiverConfigs := map[component.ID]component.Config{
		receiverID: testcomponents.ExampleReceiverFactory.CreateDefaultConfig(),
	}

	exporterIDs := make([]component.ID, len(exporterNames))
	exporterConfigs := map[component.ID]component.Config{}
	for i, name := range exporterNames {
		var id component.ID
		if name == "" {
			id = component.MustNewIDWithName("exampleexporter", "ro_"+strconv.Itoa(i))
		} else {
			id = component.MustNewIDWithName("exampleexporter", name)
		}
		exporterIDs[i] = id
		exporterConfigs[id] = testcomponents.ExampleExporterFactory.CreateDefaultConfig()
	}

	set := Settings{
		Telemetry: componenttest.NewNopTelemetrySettings(),
		BuildInfo: component.NewDefaultBuildInfo(),
		ReceiverBuilder: builders.NewReceiver(
			receiverConfigs,
			map[component.Type]receiver.Factory{
				testcomponents.ExampleReceiverFactory.Type(): testcomponents.ExampleReceiverFactory,
			},
		),
		ProcessorBuilder: builders.NewProcessor(
			map[component.ID]component.Config{},
			map[component.Type]processor.Factory{},
		),
		ExporterBuilder: builders.NewExporter(
			exporterConfigs,
			map[component.Type]exporter.Factory{
				testcomponents.ExampleExporterFactory.Type(): testcomponents.ExampleExporterFactory,
			},
		),
		ConnectorBuilder: builders.NewConnector(map[component.ID]component.Config{}, map[component.Type]connector.Factory{}),
		PipelineConfigs: pipelines.Config{
			pipeline.NewID(signal): {
				Receivers: []component.ID{receiverID},
				Exporters: exporterIDs,
			},
		},
	}

	g, err := Build(ctx, set)
	require.NoError(b, err)
	return g
}

// batchedMix names the exporter list shapes the bench iterates over. Each
// corresponds to one mode in the audit's finding-#19 reasoning:
//
//   - "single_batched":    one batched exporter, alone. Today: 0 clones (the
//     fanout short-circuits because len(mcs)==1 and the single mutator
//     receives md directly). After O2: same.
//   - "batched_plus_ro":   1 batched + 1 readonly sibling. Today: 1 clone
//     (the fanout clones for the mutator branch). After O2: 1 clone (moved
//     inside the batcher). The headline "DDOT default traces" topology.
//   - "batched_plus_3ro":  1 batched + 3 readonly siblings (N=4). Today:
//     1 clone. After O2: 1 clone (moved). Same shape, larger N.
//   - "two_batched":       2 batched exporters, no readonly. Today: 1 clone
//     (the fanout's last-mutator-gets-md-direct optimization). After O2: 2
//     clones (one inside each batcher) — the known corner-case regression
//     that O3 collapses back to 1.
//   - "two_batched_plus_ro": 2 batched + 1 readonly. Today: 2 clones (loop
//     over mutators clones for the first, lastConsumer takes the else
//     branch because readonly is non-empty so also clones). After O2: 2
//     clones (moved inside each batcher).
type batchedMix struct {
	name      string
	exporters []string
}

func batchedMixes() []batchedMix {
	return []batchedMix{
		{"single_batched", []string{"batched"}},
		{"batched_plus_ro", []string{"batched", ""}},
		{"batched_plus_3ro", []string{"batched", "", "", ""}},
		{"two_batched", []string{"batched_a", "batched_b"}},
		{"two_batched_plus_ro", []string{"batched_a", "batched_b", ""}},
	}
}

// BenchmarkPipelineFanoutBatchedMetrics measures fanout cloning behavior when
// one or more "batched" exporters (which declare MutatesData: true to
// simulate the exporterhelper's queue+batch injection — audit finding #19)
// sit alongside zero or more plain readonly exporters in the same pipeline.
//
// Compare against BenchmarkPipelineFanoutMetrics — that bench exercises the
// receiver-side fanout via a mutating processor; this one exercises the
// pipeline-internal fanout via mutator-declaring exporters. The two stress
// different code paths.
func BenchmarkPipelineFanoutBatchedMetrics(b *testing.B) {
	shapes := []struct {
		name string
		gen  func() pmetric.Metrics
	}{
		{"small_10", func() pmetric.Metrics { return testdata.GenerateMetrics(10) }},
		{"medium_1k", func() pmetric.Metrics { return testdata.GenerateMetrics(1000) }},
		{"rich_100x5x50x10", func() pmetric.Metrics { return testdata.GenerateMetricsManyResources(100, 5, 50, 10) }},
	}
	ctx := context.Background()

	for _, shape := range shapes {
		for _, mix := range batchedMixes() {
			name := fmt.Sprintf("mix=%s/shape=%s", mix.name, shape.name)
			b.Run(name, func(b *testing.B) {
				g := buildBatchedPipeline(ctx, b, pipeline.SignalMetrics, mix.exporters...)
				rcv := receiverFor(b, g, pipeline.SignalMetrics)
				md := shape.gen()
				b.ReportAllocs()
				b.SetBytes(int64(md.DataPointCount())) // items/op, see BenchmarkMetricsFanout
				for b.Loop() {
					if err := rcv.ConsumeMetricsFunc(ctx, md); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkPipelineFanoutBatchedTraces — see BenchmarkPipelineFanoutBatchedMetrics.
func BenchmarkPipelineFanoutBatchedTraces(b *testing.B) {
	shapes := []struct {
		name string
		gen  func() ptrace.Traces
	}{
		{"small_10", func() ptrace.Traces { return testdata.GenerateTraces(10) }},
		{"medium_1k", func() ptrace.Traces { return testdata.GenerateTraces(1000) }},
		{"rich_100x5x50x10", func() ptrace.Traces { return testdata.GenerateTracesManyResources(100, 5, 50, 10) }},
	}
	ctx := context.Background()

	for _, shape := range shapes {
		for _, mix := range batchedMixes() {
			name := fmt.Sprintf("mix=%s/shape=%s", mix.name, shape.name)
			b.Run(name, func(b *testing.B) {
				g := buildBatchedPipeline(ctx, b, pipeline.SignalTraces, mix.exporters...)
				rcv := receiverFor(b, g, pipeline.SignalTraces)
				td := shape.gen()
				b.ReportAllocs()
				b.SetBytes(int64(td.SpanCount())) // items/op, see BenchmarkMetricsFanout
				for b.Loop() {
					if err := rcv.ConsumeTracesFunc(ctx, td); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// BenchmarkPipelineFanoutBatchedLogs — see BenchmarkPipelineFanoutBatchedMetrics.
func BenchmarkPipelineFanoutBatchedLogs(b *testing.B) {
	shapes := []struct {
		name string
		gen  func() plog.Logs
	}{
		{"small_10", func() plog.Logs { return testdata.GenerateLogs(10) }},
		{"medium_1k", func() plog.Logs { return testdata.GenerateLogs(1000) }},
		{"rich_100x5x50x10", func() plog.Logs { return testdata.GenerateLogsManyResources(100, 5, 50, 10) }},
	}
	ctx := context.Background()

	for _, shape := range shapes {
		for _, mix := range batchedMixes() {
			name := fmt.Sprintf("mix=%s/shape=%s", mix.name, shape.name)
			b.Run(name, func(b *testing.B) {
				g := buildBatchedPipeline(ctx, b, pipeline.SignalLogs, mix.exporters...)
				rcv := receiverFor(b, g, pipeline.SignalLogs)
				ld := shape.gen()
				b.ReportAllocs()
				b.SetBytes(int64(ld.LogRecordCount())) // items/op, see BenchmarkMetricsFanout
				for b.Loop() {
					if err := rcv.ConsumeLogsFunc(ctx, ld); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
