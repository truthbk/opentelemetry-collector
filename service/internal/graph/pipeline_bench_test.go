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

// buildBenchPipeline wires a receiver → optional mutating processor →
// N exporters pipeline and returns the built graph. The receiver and
// exporters are testcomponents.ExampleReceiver/Exporter (both declare
// MutatesData: false). When `mutator` is true a testcomponents.
// ExampleProcessor with name "mutate" is inserted; that variant declares
// MutatesData: true and forces the fanout consumer to clone for every
// downstream exporter.
//
// The signal-specific helpers below extract the receiver back out of
// the graph so the bench loop can drive load directly.
func buildBenchPipeline(ctx context.Context, b *testing.B, signal pipeline.Signal, n int, mutator bool) *Graph {
	b.Helper()

	receiverID := component.MustNewID("examplereceiver")
	receiverConfigs := map[component.ID]component.Config{
		receiverID: testcomponents.ExampleReceiverFactory.CreateDefaultConfig(),
	}

	procIDs := []component.ID{}
	procConfigs := map[component.ID]component.Config{}
	if mutator {
		mutateID := component.MustNewIDWithName("exampleprocessor", "mutate")
		procIDs = []component.ID{mutateID}
		procConfigs[mutateID] = testcomponents.ExampleProcessorFactory.CreateDefaultConfig()
	}

	exporterIDs := make([]component.ID, n)
	exporterConfigs := map[component.ID]component.Config{}
	for i := range n {
		id := component.MustNewIDWithName("exampleexporter", strconv.Itoa(i))
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
			procConfigs,
			map[component.Type]processor.Factory{
				testcomponents.ExampleProcessorFactory.Type(): testcomponents.ExampleProcessorFactory,
			},
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
				Receivers:  []component.ID{receiverID},
				Processors: procIDs,
				Exporters:  exporterIDs,
			},
		},
	}

	g, err := Build(ctx, set)
	require.NoError(b, err)
	return g
}

// receiverFor returns the single testcomponents.ExampleReceiver instance
// wired into the built graph for the given signal.
func receiverFor(b *testing.B, g *Graph, signal pipeline.Signal) *testcomponents.ExampleReceiver {
	b.Helper()
	receivers := g.getReceivers()[signal]
	for _, c := range receivers {
		r, ok := c.(*testcomponents.ExampleReceiver)
		require.True(b, ok, "expected *testcomponents.ExampleReceiver")
		return r
	}
	b.Fatalf("no receiver found for signal %s", signal)
	return nil
}

func genRichMetricsBench(rmCount, smCount, dpCount, attrCount int) pmetric.Metrics {
	md := pmetric.NewMetrics()
	md.ResourceMetrics().EnsureCapacity(rmCount)
	for r := range rmCount {
		rm := md.ResourceMetrics().AppendEmpty()
		rm.Resource().Attributes().PutStr("host.name", "host-"+strconv.Itoa(r))
		rm.Resource().Attributes().PutStr("service.name", "bench")
		rm.ScopeMetrics().EnsureCapacity(smCount)
		for s := range smCount {
			sm := rm.ScopeMetrics().AppendEmpty()
			sm.Scope().SetName("scope-" + strconv.Itoa(s))
			m := sm.Metrics().AppendEmpty()
			m.SetName("benchmark.metric")
			gauge := m.SetEmptyGauge()
			gauge.DataPoints().EnsureCapacity(dpCount)
			for d := range dpCount {
				dp := gauge.DataPoints().AppendEmpty()
				dp.SetIntValue(int64(d))
				for a := range attrCount {
					dp.Attributes().PutStr("attr_"+strconv.Itoa(a),
						fmt.Sprintf("v-%d-%d-%d-%d", r, s, d, a))
				}
			}
		}
	}
	return md
}

func genRichTracesBench(rsCount, ssCount, spanCount, attrCount int) ptrace.Traces {
	td := ptrace.NewTraces()
	td.ResourceSpans().EnsureCapacity(rsCount)
	for r := range rsCount {
		rs := td.ResourceSpans().AppendEmpty()
		rs.Resource().Attributes().PutStr("host.name", "host-"+strconv.Itoa(r))
		rs.Resource().Attributes().PutStr("service.name", "bench")
		rs.ScopeSpans().EnsureCapacity(ssCount)
		for s := range ssCount {
			ss := rs.ScopeSpans().AppendEmpty()
			ss.Scope().SetName("scope-" + strconv.Itoa(s))
			ss.Spans().EnsureCapacity(spanCount)
			for sp := range spanCount {
				span := ss.Spans().AppendEmpty()
				span.SetName("benchmark.span")
				for a := range attrCount {
					span.Attributes().PutStr("attr_"+strconv.Itoa(a),
						fmt.Sprintf("v-%d-%d-%d-%d", r, s, sp, a))
				}
			}
		}
	}
	return td
}

func genRichLogsBench(rlCount, slCount, recordCount, attrCount int) plog.Logs {
	ld := plog.NewLogs()
	ld.ResourceLogs().EnsureCapacity(rlCount)
	for r := range rlCount {
		rl := ld.ResourceLogs().AppendEmpty()
		rl.Resource().Attributes().PutStr("host.name", "host-"+strconv.Itoa(r))
		rl.Resource().Attributes().PutStr("service.name", "bench")
		rl.ScopeLogs().EnsureCapacity(slCount)
		for s := range slCount {
			sl := rl.ScopeLogs().AppendEmpty()
			sl.Scope().SetName("scope-" + strconv.Itoa(s))
			sl.LogRecords().EnsureCapacity(recordCount)
			for rec := range recordCount {
				lr := sl.LogRecords().AppendEmpty()
				lr.Body().SetStr("benchmark log line")
				for a := range attrCount {
					lr.Attributes().PutStr("attr_"+strconv.Itoa(a),
						fmt.Sprintf("v-%d-%d-%d-%d", r, s, rec, a))
				}
			}
		}
	}
	return ld
}

// BenchmarkPipelineFanoutMetrics measures the end-to-end cost of pushing
// one pmetric.Metrics batch through a fully-built graph
// (examplereceiver → [optional mutating processor] → N exampleexporter).
//
// The fanout consumer between the processor and exporters is inserted
// automatically by Build. When `mutator=true` the chain declares
// MutatesData: true, forcing the fanout to deep-clone for every
// downstream consumer — this is the pattern targeted by the audit's
// finding #19 (CoW-aware exporterhelper batcher) and finding #18
// (selective COW pdata).
//
// Compare against BenchmarkMetricsFanout in
// internal/fanoutconsumer/metrics_bench_test.go to attribute overhead
// between the bare fanout and the rest of the graph (capabilities node,
// obs/refconsumer wrappers, etc.).
func BenchmarkPipelineFanoutMetrics(b *testing.B) {
	shapes := []struct {
		name string
		gen  func() pmetric.Metrics
	}{
		{"small_10", func() pmetric.Metrics { return testdata.GenerateMetrics(10) }},
		{"medium_1k", func() pmetric.Metrics { return testdata.GenerateMetrics(1000) }},
		{"rich_100x5x50x10", func() pmetric.Metrics { return genRichMetricsBench(100, 5, 50, 10) }},
	}
	ns := []int{1, 2, 4, 8}
	mutators := []bool{false, true}
	ctx := context.Background()

	for _, shape := range shapes {
		for _, mutator := range mutators {
			for _, n := range ns {
				name := fmt.Sprintf("N=%d/mutator=%v/shape=%s", n, mutator, shape.name)
				b.Run(name, func(b *testing.B) {
					g := buildBenchPipeline(ctx, b, pipeline.SignalMetrics, n, mutator)
					rcv := receiverFor(b, g, pipeline.SignalMetrics)
					md := shape.gen()
					b.ReportAllocs()
					b.SetBytes(int64(md.DataPointCount()))
					b.ResetTimer()
					for b.Loop() {
						if err := rcv.ConsumeMetricsFunc(ctx, md); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

// BenchmarkPipelineFanoutTraces — see BenchmarkPipelineFanoutMetrics.
func BenchmarkPipelineFanoutTraces(b *testing.B) {
	shapes := []struct {
		name string
		gen  func() ptrace.Traces
	}{
		{"small_10", func() ptrace.Traces { return testdata.GenerateTraces(10) }},
		{"medium_1k", func() ptrace.Traces { return testdata.GenerateTraces(1000) }},
		{"rich_100x5x50x10", func() ptrace.Traces { return genRichTracesBench(100, 5, 50, 10) }},
	}
	ns := []int{1, 2, 4, 8}
	mutators := []bool{false, true}
	ctx := context.Background()

	for _, shape := range shapes {
		for _, mutator := range mutators {
			for _, n := range ns {
				name := fmt.Sprintf("N=%d/mutator=%v/shape=%s", n, mutator, shape.name)
				b.Run(name, func(b *testing.B) {
					g := buildBenchPipeline(ctx, b, pipeline.SignalTraces, n, mutator)
					rcv := receiverFor(b, g, pipeline.SignalTraces)
					td := shape.gen()
					b.ReportAllocs()
					b.SetBytes(int64(td.SpanCount()))
					b.ResetTimer()
					for b.Loop() {
						if err := rcv.ConsumeTracesFunc(ctx, td); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

// BenchmarkPipelineFanoutLogs — see BenchmarkPipelineFanoutMetrics.
func BenchmarkPipelineFanoutLogs(b *testing.B) {
	shapes := []struct {
		name string
		gen  func() plog.Logs
	}{
		{"small_10", func() plog.Logs { return testdata.GenerateLogs(10) }},
		{"medium_1k", func() plog.Logs { return testdata.GenerateLogs(1000) }},
		{"rich_100x5x50x10", func() plog.Logs { return genRichLogsBench(100, 5, 50, 10) }},
	}
	ns := []int{1, 2, 4, 8}
	mutators := []bool{false, true}
	ctx := context.Background()

	for _, shape := range shapes {
		for _, mutator := range mutators {
			for _, n := range ns {
				name := fmt.Sprintf("N=%d/mutator=%v/shape=%s", n, mutator, shape.name)
				b.Run(name, func(b *testing.B) {
					g := buildBenchPipeline(ctx, b, pipeline.SignalLogs, n, mutator)
					rcv := receiverFor(b, g, pipeline.SignalLogs)
					ld := shape.gen()
					b.ReportAllocs()
					b.SetBytes(int64(ld.LogRecordCount()))
					b.ResetTimer()
					for b.Loop() {
						if err := rcv.ConsumeLogsFunc(ctx, ld); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
