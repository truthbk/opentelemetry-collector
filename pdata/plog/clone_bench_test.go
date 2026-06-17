// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package plog

import (
	"fmt"
	"strconv"
	"testing"
)

type cloneBenchShape struct {
	name string
	gen  func() Logs
}

func genSimpleLogs(count int) Logs {
	ld := NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "bench")
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName("bench-scope")
	sl.LogRecords().EnsureCapacity(count)
	for i := range count {
		lr := sl.LogRecords().AppendEmpty()
		lr.Body().SetStr("log_" + strconv.Itoa(i))
		lr.Attributes().PutStr("idx", strconv.Itoa(i))
	}
	return ld
}

// genRichLogs builds a batch that exercises every nested container path:
// rlCount × slCount × recordCount with attrCount string attributes per
// record. Mirrors the "rich" pmetric/ptrace shape.
func genRichLogs(rlCount, slCount, recordCount, attrCount int) Logs {
	ld := NewLogs()
	ld.ResourceLogs().EnsureCapacity(rlCount)
	for r := range rlCount {
		rl := ld.ResourceLogs().AppendEmpty()
		attrs := rl.Resource().Attributes()
		attrs.PutStr("host.name", "host-"+strconv.Itoa(r))
		attrs.PutStr("service.name", "bench")
		rl.ScopeLogs().EnsureCapacity(slCount)
		for s := range slCount {
			sl := rl.ScopeLogs().AppendEmpty()
			sl.Scope().SetName("scope-" + strconv.Itoa(s))
			sl.LogRecords().EnsureCapacity(recordCount)
			for rec := range recordCount {
				lr := sl.LogRecords().AppendEmpty()
				lr.Body().SetStr("benchmark log line")
				recAttrs := lr.Attributes()
				for a := range attrCount {
					recAttrs.PutStr("attr_"+strconv.Itoa(a),
						fmt.Sprintf("v-%d-%d-%d-%d", r, s, rec, a))
				}
			}
		}
	}
	return ld
}

func cloneBenchShapes() []cloneBenchShape {
	return []cloneBenchShape{
		{"small_10", func() Logs { return genSimpleLogs(10) }},
		{"medium_1k", func() Logs { return genSimpleLogs(1000) }},
		{"large_10k", func() Logs { return genSimpleLogs(10000) }},
		{"rich_100x5x50x10", func() Logs { return genRichLogs(100, 5, 50, 10) }},
	}
}

// BenchmarkCopyToLogs — see pdata/pmetric/clone_bench_test.go for the
// grid and reading guide; the log and metric clone benchmarks are
// symmetric.
func BenchmarkCopyToLogs(b *testing.B) {
	for _, shape := range cloneBenchShapes() {
		b.Run("shape="+shape.name, func(b *testing.B) {
			src := shape.gen()
			b.ReportAllocs()
			b.SetBytes(int64(src.LogRecordCount()))
			b.ResetTimer()
			for b.Loop() {
				dst := NewLogs()
				src.CopyTo(dst)
			}
		})
	}
}
