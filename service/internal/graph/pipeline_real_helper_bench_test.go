// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package graph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentstatus"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/connector"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/testdata"
	"go.opentelemetry.io/collector/pipeline"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/service/internal/builders"
	"go.opentelemetry.io/collector/service/internal/status"
	"go.opentelemetry.io/collector/service/internal/testcomponents"
	"go.opentelemetry.io/collector/service/pipelines"
)

// startRealHelperGraph starts the graph's component lifecycle (so the
// exporterhelper queue workers actually spawn and drain the queue
// during the bench) and registers a Cleanup that calls ShutdownAll on
// teardown. Without this the bench's per-iter ConsumeXxx call enqueues
// indefinitely until the bounded queue rejects with ErrQueueIsFull and
// b.Fatal fires — silently NOT exercising the post-O2 batcher path the
// bench is meant to validate.
func startRealHelperGraph(b *testing.B, g *Graph) {
	b.Helper()
	host := &Host{
		Reporter: status.NewReporter(
			func(*componentstatus.InstanceID, *componentstatus.Event) {},
			func(error) {},
		),
	}
	require.NoError(b, g.StartAll(context.Background(), host))
	b.Cleanup(func() {
		require.NoError(b, g.ShutdownAll(context.Background(), status.NewNopStatusReporter()))
	})
}

// realHelperExporterType is the component.Type for the bench-only factory
// below. The factory builds an exporter via the *real* exporter/exporterhelper
// path with sending_queue.batch enabled, so the bench exercises the actual
// O2 code path (queuebatch/MergeSplit's cloneIfShared and the wrapped
// exporter's Capabilities() reporting) end-to-end — distinct from the
// pipeline_batched_bench_test.go fixtures, which only simulate the audit's
// MutatesData declaration via the testcomponents.ExampleExporter "batched_*"
// naming convention.
var realHelperExporterType = component.MustNewType("realhelperexporter")

// realHelperExporterFactory builds traces/metrics/logs exporters via
// exporter/exporterhelper.NewX with sending_queue.batch enabled. The
// downstream pusher is a no-op so the bench measures the upstream path
// (receiver-side fanout + queue enqueue) without picking up any
// downstream-of-the-batcher cost.
func newRealHelperExporterFactory() exporter.Factory {
	return exporter.NewFactory(
		realHelperExporterType,
		func() component.Config { return &struct{}{} },
		exporter.WithMetrics(realHelperMetricsExporter, component.StabilityLevelDevelopment),
		exporter.WithTraces(realHelperTracesExporter, component.StabilityLevelDevelopment),
		exporter.WithLogs(realHelperLogsExporter, component.StabilityLevelDevelopment),
	)
}

// queueConfigWithBatch returns a queue config with sending_queue.batch
// enabled (default-zero BatchConfig) and a generous queue capacity so
// per-iteration enqueues from the bench loop never block.
func queueConfigWithBatch() exporterhelper.QueueBatchConfig {
	qCfg := exporterhelper.NewDefaultQueueConfig()
	qCfg.Batch.GetOrInsertDefault()
	qCfg.QueueSize = 100_000
	return qCfg
}

func realHelperMetricsExporter(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Metrics, error) {
	return exporterhelper.NewMetrics(ctx, set, cfg,
		func(context.Context, pmetric.Metrics) error { return nil },
		exporterhelper.WithQueue(configoptional.Some(queueConfigWithBatch())),
	)
}

func realHelperTracesExporter(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Traces, error) {
	return exporterhelper.NewTraces(ctx, set, cfg,
		func(context.Context, ptrace.Traces) error { return nil },
		exporterhelper.WithQueue(configoptional.Some(queueConfigWithBatch())),
	)
}

func realHelperLogsExporter(ctx context.Context, set exporter.Settings, cfg component.Config) (exporter.Logs, error) {
	return exporterhelper.NewLogs(ctx, set, cfg,
		func(context.Context, plog.Logs) error { return nil },
		exporterhelper.WithQueue(configoptional.Some(queueConfigWithBatch())),
	)
}

// buildRealHelperMultiPipelineGraph wires one receiver into two pipelines: a
// "batched" pipeline whose exporter is built via the real exporterhelper
// queue+batch path (audit finding #19's target), and a "readonly" pipeline
// with a plain testcomponents.ExampleExporter (declares MutatesData: false).
//
// Pre-O2 the wrapped batched exporter declared MutatesData: true (the
// auto-injected WithCapabilities), so the receiver-side fanout's per-
// pipeline aggregate flipped to true for the batched pipeline and the fanout
// deep-cloned the pdata for that branch on every call.
//
// Post-O2 the wrapped batched exporter declares MutatesData: false; the
// aggregate stays false for both pipelines; the fanout's MarkReadOnly
// broadcast replaces the per-call clone with a single atomic state-bit flip.
// The clone moves into the batcher's MergeSplit cloneIfShared on the queue
// worker goroutine, so it falls outside the bench's per-call measurement.
func buildRealHelperMultiPipelineGraph(ctx context.Context, b *testing.B, signal pipeline.Signal) *Graph {
	b.Helper()

	receiverID := component.MustNewID("examplereceiver")
	receiverConfigs := map[component.ID]component.Config{
		receiverID: testcomponents.ExampleReceiverFactory.CreateDefaultConfig(),
	}

	realHelperFactory := newRealHelperExporterFactory()
	batchedExpID := component.NewIDWithName(realHelperExporterType, "batched")
	readonlyExpID := component.MustNewIDWithName("exampleexporter", "ro")
	exporterConfigs := map[component.ID]component.Config{
		batchedExpID:  realHelperFactory.CreateDefaultConfig(),
		readonlyExpID: testcomponents.ExampleExporterFactory.CreateDefaultConfig(),
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
				realHelperExporterType:                       realHelperFactory,
				testcomponents.ExampleExporterFactory.Type(): testcomponents.ExampleExporterFactory,
			},
		),
		ConnectorBuilder: builders.NewConnector(map[component.ID]component.Config{}, map[component.Type]connector.Factory{}),
		PipelineConfigs: pipelines.Config{
			pipeline.NewIDWithName(signal, "batched"): {
				Receivers: []component.ID{receiverID},
				Exporters: []component.ID{batchedExpID},
			},
			pipeline.NewIDWithName(signal, "readonly"): {
				Receivers: []component.ID{receiverID},
				Exporters: []component.ID{readonlyExpID},
			},
		},
	}

	g, err := Build(ctx, set)
	require.NoError(b, err)
	return g
}

// BenchmarkRealHelperMultiPipelineMetrics drives metrics through a graph
// built with the *real* exporterhelper queue+batch path (audit #19's
// scenario). See buildRealHelperMultiPipelineGraph for the topology and
// pre/post-O2 framing.
//
// Per-iteration the bench measures: receiver-side fanout dispatch +
// per-pipeline capability check + enqueue into the batched exporter's
// queue + the readonly exporter's append. The queue worker's batch +
// flush work happens off the bench goroutine and is NOT counted.
func BenchmarkRealHelperMultiPipelineMetrics(b *testing.B) {
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
		b.Run("shape="+shape.name, func(b *testing.B) {
			g := buildRealHelperMultiPipelineGraph(ctx, b, pipeline.SignalMetrics)
			startRealHelperGraph(b, g)
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

// BenchmarkRealHelperMultiPipelineTraces — see BenchmarkRealHelperMultiPipelineMetrics.
func BenchmarkRealHelperMultiPipelineTraces(b *testing.B) {
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
		b.Run("shape="+shape.name, func(b *testing.B) {
			g := buildRealHelperMultiPipelineGraph(ctx, b, pipeline.SignalTraces)
			startRealHelperGraph(b, g)
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

// BenchmarkRealHelperMultiPipelineLogs — see BenchmarkRealHelperMultiPipelineMetrics.
func BenchmarkRealHelperMultiPipelineLogs(b *testing.B) {
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
		b.Run("shape="+shape.name, func(b *testing.B) {
			g := buildRealHelperMultiPipelineGraph(ctx, b, pipeline.SignalLogs)
			startRealHelperGraph(b, g)
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
