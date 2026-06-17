// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// perftestbed drives a synthetic, sustained workload through the
// fanoutconsumer for steady-state profiling. It exposes net/http/pprof
// on a configurable address so the standard go tool pprof workflow
// works without needing to wire pprofextension into a full collector.
//
// This is a developer tool — production collectors should keep using
// the pprofextension from the contrib repo.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	//nolint:gosec // G108: developer tool; the pprof endpoint is intentional.
	_ "net/http/pprof" // registers /debug/pprof/* handlers on DefaultServeMux
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/internal/fanoutconsumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/testdata"
)

type config struct {
	signal     string
	fanout     int
	mutatorMix string
	shape      string
	qps        int
	duration   time.Duration
	pprofAddr  string
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.signal, "signal", "metrics", "signal to drive: metrics, traces, logs")
	flag.IntVar(&cfg.fanout, "fanout", 4, "number of downstream consumers in the fanout")
	flag.StringVar(&cfg.mutatorMix, "mutator-mix", "one_mut_rest_ro",
		"consumer mix: all_ro, all_mut, half, one_mut_rest_ro")
	flag.StringVar(&cfg.shape, "shape", "rich_100x5x50x10",
		"batch shape: small_10, medium_1k, large_10k, rich_100x5x50x10")
	flag.IntVar(&cfg.qps, "qps", 1000, "best-effort consume rate cap per second (0 = unthrottled); overruns logged at the 5 s heartbeat")
	flag.DurationVar(&cfg.duration, "duration", 60*time.Second,
		"how long to run (0 = forever until SIGINT)")
	flag.StringVar(&cfg.pprofAddr, "pprof-addr", "localhost:6060",
		"address for net/http/pprof endpoints (set empty to disable)")
	flag.Parse()

	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func run(cfg config) error {
	if cfg.pprofAddr != "" {
		go func() {
			log.Printf("pprof listening on http://%s/debug/pprof/", cfg.pprofAddr)
			//nolint:gosec // dev tool, not a production listener
			if err := http.ListenAndServe(cfg.pprofAddr, nil); err != nil {
				log.Printf("pprof server exited: %v", err)
			}
		}()
	}

	log.Printf("perftestbed: signal=%s fanout=%d mix=%s shape=%s qps=%d duration=%s "+
		"go=%s GOMAXPROCS=%d",
		cfg.signal, cfg.fanout, cfg.mutatorMix, cfg.shape, cfg.qps, cfg.duration,
		runtime.Version(), runtime.GOMAXPROCS(0))

	ctx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if cfg.duration > 0 {
		ctx, cancel = context.WithTimeout(ctx, cfg.duration)
		defer cancel()
	}

	switch cfg.signal {
	case "metrics":
		return runMetrics(ctx, cfg)
	case "traces":
		return runTraces(ctx, cfg)
	case "logs":
		return runLogs(ctx, cfg)
	default:
		return fmt.Errorf("unknown signal %q", cfg.signal)
	}
}

func runMetrics(ctx context.Context, cfg config) error {
	// Generate one sample upfront for itemsPer reporting and to fail fast on
	// an unknown shape; the consume loop produces a fresh payload per call
	// so production-shape fanout state (MarkReadOnly etc.) is exercised on
	// every iteration the way a receiver-driven pipeline would.
	sample, err := buildMetrics(cfg.shape)
	if err != nil {
		return err
	}
	consumers, err := buildMetricsMix(cfg.mutatorMix, cfg.fanout)
	if err != nil {
		return err
	}
	fanout := fanoutconsumer.NewMetrics(consumers)
	itemsPer := int64(sample.DataPointCount())
	return loop(ctx, cfg, "datapoints", itemsPer, func(ctx context.Context) error {
		md, _ := buildMetrics(cfg.shape) // shape validated above
		return fanout.ConsumeMetrics(ctx, md)
	})
}

func runTraces(ctx context.Context, cfg config) error {
	sample, err := buildTraces(cfg.shape)
	if err != nil {
		return err
	}
	consumers, err := buildTracesMix(cfg.mutatorMix, cfg.fanout)
	if err != nil {
		return err
	}
	fanout := fanoutconsumer.NewTraces(consumers)
	itemsPer := int64(sample.SpanCount())
	return loop(ctx, cfg, "spans", itemsPer, func(ctx context.Context) error {
		td, _ := buildTraces(cfg.shape) // shape validated above
		return fanout.ConsumeTraces(ctx, td)
	})
}

func runLogs(ctx context.Context, cfg config) error {
	sample, err := buildLogs(cfg.shape)
	if err != nil {
		return err
	}
	consumers, err := buildLogsMix(cfg.mutatorMix, cfg.fanout)
	if err != nil {
		return err
	}
	fanout := fanoutconsumer.NewLogs(consumers)
	itemsPer := int64(sample.LogRecordCount())
	return loop(ctx, cfg, "records", itemsPer, func(ctx context.Context) error {
		ld, _ := buildLogs(cfg.shape) // shape validated above
		return fanout.ConsumeLogs(ctx, ld)
	})
}

// loop runs `consume` at the target QPS until the context is done. Prints
// a summary line every 5 seconds and a final tally on exit.
func loop(ctx context.Context, cfg config, itemUnit string, itemsPer int64,
	consume func(context.Context) error,
) error {
	var batches atomic.Int64
	start := time.Now()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var pacer <-chan time.Time
	if cfg.qps > 0 {
		t := time.NewTicker(time.Second / time.Duration(cfg.qps))
		defer t.Stop()
		pacer = t.C
	}

	defer func() {
		elapsed := time.Since(start)
		b := batches.Load()
		items := b * itemsPer
		rate := float64(items) / elapsed.Seconds()
		log.Printf("done: %d batches, %d %s, %.0f %s/s over %s × %d consumers",
			b, items, itemUnit, rate, itemUnit, elapsed.Round(time.Millisecond), cfg.fanout)
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			b := batches.Load()
			elapsed := time.Since(start)
			log.Printf("%s elapsed: %d batches, %.0f %s/s",
				elapsed.Round(time.Second), b,
				float64(b*itemsPer)/elapsed.Seconds(), itemUnit)
			// Pacer overrun: if QPS-throttled, compare actual batches to the
			// number a perfect pacer would have produced. Tolerate ~10 %
			// jitter; anything beyond that means consume is the bottleneck.
			if cfg.qps > 0 {
				expected := int64(elapsed.Seconds() * float64(cfg.qps))
				lag := expected - b
				if expected > 0 && lag > expected/10 {
					log.Printf("  pacer overrun: %d batches behind target (%.0f%% of target reached)",
						lag, 100.0*float64(b)/float64(expected))
				}
			}
		default:
		}
		if pacer != nil {
			select {
			case <-ctx.Done():
				return nil
			case <-pacer:
			}
		}
		if err := consume(ctx); err != nil {
			return fmt.Errorf("consume failed: %w", err)
		}
		batches.Add(1)
	}
}

// ---- payload builders ------------------------------------------------------

func buildMetrics(shape string) (pmetric.Metrics, error) {
	switch shape {
	case "small_10":
		return testdata.GenerateMetrics(10), nil
	case "medium_1k":
		return testdata.GenerateMetrics(1000), nil
	case "large_10k":
		return testdata.GenerateMetrics(10000), nil
	case "rich_100x5x50x10":
		return genRichMetrics(100, 5, 50, 10), nil
	default:
		return pmetric.Metrics{}, fmt.Errorf("unknown shape %q", shape)
	}
}

func buildTraces(shape string) (ptrace.Traces, error) {
	switch shape {
	case "small_10":
		return testdata.GenerateTraces(10), nil
	case "medium_1k":
		return testdata.GenerateTraces(1000), nil
	case "large_10k":
		return testdata.GenerateTraces(10000), nil
	case "rich_100x5x50x10":
		return genRichTraces(100, 5, 50, 10), nil
	default:
		return ptrace.Traces{}, fmt.Errorf("unknown shape %q", shape)
	}
}

func buildLogs(shape string) (plog.Logs, error) {
	switch shape {
	case "small_10":
		return testdata.GenerateLogs(10), nil
	case "medium_1k":
		return testdata.GenerateLogs(1000), nil
	case "large_10k":
		return testdata.GenerateLogs(10000), nil
	case "rich_100x5x50x10":
		return genRichLogs(100, 5, 50, 10), nil
	default:
		return plog.Logs{}, fmt.Errorf("unknown shape %q", shape)
	}
}

func genRichMetrics(rmCount, smCount, dpCount, attrCount int) pmetric.Metrics {
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

func genRichTraces(rsCount, ssCount, spanCount, attrCount int) ptrace.Traces {
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

func genRichLogs(rlCount, slCount, recordCount, attrCount int) plog.Logs {
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

// ---- consumer mixes --------------------------------------------------------

type mutatingNopMetrics struct{ consumer.Metrics }

func (mutatingNopMetrics) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

type mutatingNopTraces struct{ consumer.Traces }

func (mutatingNopTraces) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

type mutatingNopLogs struct{ consumer.Logs }

func (mutatingNopLogs) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func buildMetricsMix(mix string, n int) ([]consumer.Metrics, error) {
	out := make([]consumer.Metrics, 0, n)
	for i := range n {
		switch mix {
		case "all_mut":
			out = append(out, mutatingNopMetrics{Metrics: consumertest.NewNop()})
		case "all_ro":
			out = append(out, consumertest.NewNop())
		case "half":
			if i < n/2 {
				out = append(out, mutatingNopMetrics{Metrics: consumertest.NewNop()})
			} else {
				out = append(out, consumertest.NewNop())
			}
		case "one_mut_rest_ro":
			if i == 0 {
				out = append(out, mutatingNopMetrics{Metrics: consumertest.NewNop()})
			} else {
				out = append(out, consumertest.NewNop())
			}
		default:
			return nil, fmt.Errorf("unknown mix %q", mix)
		}
	}
	return out, nil
}

func buildTracesMix(mix string, n int) ([]consumer.Traces, error) {
	out := make([]consumer.Traces, 0, n)
	for i := range n {
		switch mix {
		case "all_mut":
			out = append(out, mutatingNopTraces{Traces: consumertest.NewNop()})
		case "all_ro":
			out = append(out, consumertest.NewNop())
		case "half":
			if i < n/2 {
				out = append(out, mutatingNopTraces{Traces: consumertest.NewNop()})
			} else {
				out = append(out, consumertest.NewNop())
			}
		case "one_mut_rest_ro":
			if i == 0 {
				out = append(out, mutatingNopTraces{Traces: consumertest.NewNop()})
			} else {
				out = append(out, consumertest.NewNop())
			}
		default:
			return nil, fmt.Errorf("unknown mix %q", mix)
		}
	}
	return out, nil
}

func buildLogsMix(mix string, n int) ([]consumer.Logs, error) {
	out := make([]consumer.Logs, 0, n)
	for i := range n {
		switch mix {
		case "all_mut":
			out = append(out, mutatingNopLogs{Logs: consumertest.NewNop()})
		case "all_ro":
			out = append(out, consumertest.NewNop())
		case "half":
			if i < n/2 {
				out = append(out, mutatingNopLogs{Logs: consumertest.NewNop()})
			} else {
				out = append(out, consumertest.NewNop())
			}
		case "one_mut_rest_ro":
			if i == 0 {
				out = append(out, mutatingNopLogs{Logs: consumertest.NewNop()})
			} else {
				out = append(out, consumertest.NewNop())
			}
		default:
			return nil, fmt.Errorf("unknown mix %q", mix)
		}
	}
	return out, nil
}
