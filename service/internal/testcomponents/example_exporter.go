// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package testcomponents // import "go.opentelemetry.io/collector/service/internal/testcomponents"

import (
	"context"
	"strings"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/xexporter"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pprofile"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/xpdata/pref"
)

var exporterType = component.MustNewType("exampleexporter")

// ExampleExporterFactory is factory for ExampleExporter.
var ExampleExporterFactory = xexporter.NewFactory(
	exporterType,
	createExporterDefaultConfig,
	xexporter.WithTraces(createTracesExporter, component.StabilityLevelDevelopment),
	xexporter.WithMetrics(createMetricsExporter, component.StabilityLevelDevelopment),
	xexporter.WithLogs(createLogsExporter, component.StabilityLevelDevelopment),
	xexporter.WithProfiles(createProfilesExporter, component.StabilityLevelDevelopment),
)

func createExporterDefaultConfig() component.Config {
	return &struct{}{}
}

func createTracesExporter(_ context.Context, set exporter.Settings, _ component.Config) (exporter.Traces, error) {
	return &ExampleExporter{mutatesData: strings.HasPrefix(set.ID.Name(), "batched")}, nil
}

func createMetricsExporter(_ context.Context, set exporter.Settings, _ component.Config) (exporter.Metrics, error) {
	return &ExampleExporter{mutatesData: strings.HasPrefix(set.ID.Name(), "batched")}, nil
}

func createLogsExporter(_ context.Context, set exporter.Settings, _ component.Config) (exporter.Logs, error) {
	return &ExampleExporter{mutatesData: strings.HasPrefix(set.ID.Name(), "batched")}, nil
}

func createProfilesExporter(_ context.Context, set exporter.Settings, _ component.Config) (xexporter.Profiles, error) {
	return &ExampleExporter{mutatesData: strings.HasPrefix(set.ID.Name(), "batched")}, nil
}

// ExampleExporter stores consumed traces, metrics, logs and profiles for testing purposes.
// When constructed via a factory with a component ID whose name starts with "batched", it
// declares MutatesData: true to simulate the exporterhelper's queue+batch path injecting
// that flag (see audit finding #19). The prefix-based check lets a single pipeline carry
// multiple distinct batched exporters (e.g. "batched_a", "batched_b"). Otherwise it
// declares MutatesData: false like a normal exporter.
type ExampleExporter struct {
	componentState
	Traces      []ptrace.Traces
	Metrics     []pmetric.Metrics
	Logs        []plog.Logs
	Profiles    []pprofile.Profiles
	mutatesData bool
}

// ConsumeTraces receives ptrace.Traces for processing by the consumer.Traces.
func (exp *ExampleExporter) ConsumeTraces(_ context.Context, td ptrace.Traces) error {
	pref.RefTraces(td)
	exp.Traces = append(exp.Traces, td)
	return nil
}

// ConsumeMetrics receives pmetric.Metrics for processing by the Metrics.
func (exp *ExampleExporter) ConsumeMetrics(_ context.Context, md pmetric.Metrics) error {
	pref.RefMetrics(md)
	exp.Metrics = append(exp.Metrics, md)
	return nil
}

// ConsumeLogs receives plog.Logs for processing by the Logs.
func (exp *ExampleExporter) ConsumeLogs(_ context.Context, ld plog.Logs) error {
	pref.RefLogs(ld)
	exp.Logs = append(exp.Logs, ld)
	return nil
}

// ConsumeProfiles receives pprofile.Profiles for processing by the xconsumer.Profiles.
func (exp *ExampleExporter) ConsumeProfiles(_ context.Context, pd pprofile.Profiles) error {
	pref.RefProfiles(pd)
	exp.Profiles = append(exp.Profiles, pd)
	return nil
}

func (exp *ExampleExporter) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: exp.mutatesData}
}
