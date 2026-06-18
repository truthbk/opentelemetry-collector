// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package xexporterhelper

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/requesttest"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/sizer"
	"go.opentelemetry.io/collector/pdata/pprofile"
	"go.opentelemetry.io/collector/pdata/testdata"
)

func TestMergeProfiles(t *testing.T) {
	pr1 := newProfilesRequest(testdata.GenerateProfiles(2))
	pr2 := newProfilesRequest(testdata.GenerateProfiles(3))
	res, err := pr1.MergeSplit(context.Background(), 0, exporterhelper.RequestSizerTypeItems, pr2)
	require.NoError(t, err)
	assert.Len(t, res, 1)
	assert.Equal(t, 5, res[0].ItemsCount())
}

func TestMergeProfilesInvalidInput(t *testing.T) {
	pr2 := newProfilesRequest(testdata.GenerateProfiles(3))
	_, err := pr2.MergeSplit(context.Background(), 0, exporterhelper.RequestSizerTypeItems, &requesttest.FakeRequest{Items: 1})
	require.Error(t, err)
}

func TestMergeSplitProfiles(t *testing.T) {
	tests := []struct {
		name     string
		szt      exporterhelper.RequestSizerType
		maxSize  int
		pr1      Request
		pr2      Request
		expected []Request
	}{
		{
			name:     "both_requests_empty",
			szt:      exporterhelper.RequestSizerTypeItems,
			maxSize:  10,
			pr1:      newProfilesRequest(pprofile.NewProfiles()),
			pr2:      newProfilesRequest(pprofile.NewProfiles()),
			expected: []Request{newProfilesRequest(pprofile.NewProfiles())},
		},
		{
			name:    "first_request_empty",
			szt:     exporterhelper.RequestSizerTypeItems,
			maxSize: 10,
			pr1:     newProfilesRequest(testdata.GenerateProfiles(0)),
			pr2:     newProfilesRequest(testdata.GenerateProfiles(5)),
			expected: []Request{newProfilesRequest(func() pprofile.Profiles {
				profiles := testdata.GenerateProfiles(0)
				_ = testdata.GenerateProfiles(5).MergeTo(profiles)
				return profiles
			}())},
		},
		{
			name:     "first_empty_second_nil",
			szt:      exporterhelper.RequestSizerTypeItems,
			maxSize:  10,
			pr1:      newProfilesRequest(pprofile.NewProfiles()),
			pr2:      nil,
			expected: []Request{newProfilesRequest(pprofile.NewProfiles())},
		},
		{
			name:    "merge_only",
			szt:     exporterhelper.RequestSizerTypeItems,
			maxSize: 10,
			pr1:     newProfilesRequest(testdata.GenerateProfiles(4)),
			pr2:     newProfilesRequest(testdata.GenerateProfiles(6)),
			expected: []Request{newProfilesRequest(func() pprofile.Profiles {
				profiles := testdata.GenerateProfiles(4)
				testdata.GenerateProfiles(6).ResourceProfiles().MoveAndAppendTo(profiles.ResourceProfiles())
				return profiles
			}())},
		},
		{
			name:    "split_only",
			szt:     exporterhelper.RequestSizerTypeItems,
			maxSize: 4,
			pr1:     newProfilesRequest(testdata.GenerateProfiles(10)),
			pr2:     nil,
			expected: []Request{
				newProfilesRequest(testdata.GenerateProfiles(4)),
				newProfilesRequest(testdata.GenerateProfiles(4)),
				newProfilesRequest(testdata.GenerateProfiles(2)),
			},
		},
		{
			name:    "merge_and_split",
			szt:     exporterhelper.RequestSizerTypeItems,
			maxSize: 10,
			pr1:     newProfilesRequest(testdata.GenerateProfiles(8)),
			pr2:     newProfilesRequest(testdata.GenerateProfiles(20)),
			expected: []Request{
				newProfilesRequest(func() pprofile.Profiles {
					profiles := testdata.GenerateProfiles(8)
					testdata.GenerateProfiles(2).ResourceProfiles().MoveAndAppendTo(profiles.ResourceProfiles())
					return profiles
				}()),
				newProfilesRequest(testdata.GenerateProfiles(10)),
				newProfilesRequest(testdata.GenerateProfiles(8)),
			},
		},
		{
			name:    "scope_profiles_split",
			szt:     exporterhelper.RequestSizerTypeItems,
			maxSize: 4,
			pr1: newProfilesRequest(func() pprofile.Profiles {
				return testdata.GenerateProfiles(6)
			}()),
			pr2: nil,
			expected: []Request{
				newProfilesRequest(testdata.GenerateProfiles(4)),
				newProfilesRequest(func() pprofile.Profiles {
					return testdata.GenerateProfiles(2)
				}()),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tt.pr1.MergeSplit(context.Background(), tt.maxSize, tt.szt, tt.pr2)
			require.NoError(t, err)
			require.Len(t, res, len(tt.expected))
			for i, r := range res {
				assert.Equal(t, tt.expected[i].(*profilesRequest).pd, r.(*profilesRequest).pd)
			}
		})
	}
}

func TestMergeSplitProfilesBasedOnByteSize(t *testing.T) {
	tests := []struct {
		name     string
		szt      exporterhelper.RequestSizerType
		maxSize  int
		pr1      Request
		pr2      Request
		expected []Request
	}{
		{
			name:     "both_requests_empty",
			szt:      exporterhelper.RequestSizerTypeItems,
			maxSize:  10,
			pr1:      newProfilesRequest(pprofile.NewProfiles()),
			pr2:      newProfilesRequest(pprofile.NewProfiles()),
			expected: []Request{newProfilesRequest(pprofile.NewProfiles())},
		},
		{
			name:     "first_request_empty",
			szt:      exporterhelper.RequestSizerTypeItems,
			maxSize:  10,
			pr1:      newProfilesRequest(pprofile.NewProfiles()),
			pr2:      newProfilesRequest(testdata.GenerateProfiles(5)),
			expected: []Request{newProfilesRequest(testdata.GenerateProfiles(5))},
		},
		{
			name:     "first_empty_second_nil",
			szt:      exporterhelper.RequestSizerTypeItems,
			maxSize:  10,
			pr1:      newProfilesRequest(pprofile.NewProfiles()),
			pr2:      nil,
			expected: []Request{newProfilesRequest(pprofile.NewProfiles())},
		},
		{
			name:    "merge_only",
			szt:     exporterhelper.RequestSizerTypeItems,
			maxSize: 10,
			pr1:     newProfilesRequest(testdata.GenerateProfiles(4)),
			pr2:     newProfilesRequest(testdata.GenerateProfiles(6)),
			expected: []Request{newProfilesRequest(func() pprofile.Profiles {
				profiles := testdata.GenerateProfiles(4)
				testdata.GenerateProfiles(6).ResourceProfiles().MoveAndAppendTo(profiles.ResourceProfiles())
				return profiles
			}())},
		},
		{
			name:    "split_only",
			szt:     exporterhelper.RequestSizerTypeItems,
			maxSize: 4,
			pr1:     newProfilesRequest(testdata.GenerateProfiles(10)),
			pr2:     nil,
			expected: []Request{
				newProfilesRequest(testdata.GenerateProfiles(4)),
				newProfilesRequest(testdata.GenerateProfiles(4)),
				newProfilesRequest(testdata.GenerateProfiles(2)),
			},
		},
		{
			name:    "merge_and_split",
			szt:     exporterhelper.RequestSizerTypeItems,
			maxSize: 10,
			pr1:     newProfilesRequest(testdata.GenerateProfiles(8)),
			pr2:     newProfilesRequest(testdata.GenerateProfiles(20)),
			expected: []Request{
				newProfilesRequest(func() pprofile.Profiles {
					profiles := testdata.GenerateProfiles(8)
					testdata.GenerateProfiles(2).ResourceProfiles().MoveAndAppendTo(profiles.ResourceProfiles())
					return profiles
				}()),
				newProfilesRequest(testdata.GenerateProfiles(10)),
				newProfilesRequest(testdata.GenerateProfiles(8)),
			},
		},
		{
			name:    "scope_profiles_split",
			szt:     exporterhelper.RequestSizerTypeItems,
			maxSize: 4,
			pr1: newProfilesRequest(func() pprofile.Profiles {
				return testdata.GenerateProfiles(6)
			}()),
			pr2: nil,
			expected: []Request{
				newProfilesRequest(testdata.GenerateProfiles(4)),
				newProfilesRequest(func() pprofile.Profiles {
					return testdata.GenerateProfiles(2)
				}()),
			},
		},
		{
			name:     "both_requests_empty",
			szt:      exporterhelper.RequestSizerTypeBytes,
			maxSize:  profilesMarshaler.ProfilesSize(testdata.GenerateProfiles(10)),
			pr1:      newProfilesRequest(pprofile.NewProfiles()),
			pr2:      newProfilesRequest(pprofile.NewProfiles()),
			expected: []Request{newProfilesRequest(pprofile.NewProfiles())},
		},
		{
			name:     "first_request_empty",
			szt:      exporterhelper.RequestSizerTypeBytes,
			maxSize:  profilesMarshaler.ProfilesSize(testdata.GenerateProfiles(10)),
			pr1:      newProfilesRequest(pprofile.NewProfiles()),
			pr2:      newProfilesRequest(testdata.GenerateProfiles(5)),
			expected: []Request{newProfilesRequest(testdata.GenerateProfiles(5))},
		},
		{
			name:     "first_empty_second_nil",
			szt:      exporterhelper.RequestSizerTypeBytes,
			maxSize:  profilesMarshaler.ProfilesSize(testdata.GenerateProfiles(10)),
			pr1:      newProfilesRequest(pprofile.NewProfiles()),
			pr2:      nil,
			expected: []Request{newProfilesRequest(pprofile.NewProfiles())},
		},
		{
			name:    "merge_only",
			szt:     exporterhelper.RequestSizerTypeBytes,
			maxSize: profilesMarshaler.ProfilesSize(testdata.GenerateProfiles(13)),
			pr1:     newProfilesRequest(testdata.GenerateProfiles(4)),
			pr2:     newProfilesRequest(testdata.GenerateProfiles(6)),
			expected: []Request{newProfilesRequest(func() pprofile.Profiles {
				profiles := testdata.GenerateProfiles(4)
				testdata.GenerateProfiles(6).ResourceProfiles().MoveAndAppendTo(profiles.ResourceProfiles())
				return profiles
			}())},
		},
		{
			name:    "split_only",
			szt:     exporterhelper.RequestSizerTypeBytes,
			maxSize: profilesMarshaler.ProfilesSize(testdata.GenerateProfiles(4)),
			pr1:     newProfilesRequest(testdata.GenerateProfiles(0)),
			pr2:     newProfilesRequest(testdata.GenerateProfiles(10)),
			expected: []Request{
				newProfilesRequest(testdata.GenerateProfiles(4)),
				newProfilesRequest(testdata.GenerateProfiles(5)),
				newProfilesRequest(testdata.GenerateProfiles(1)),
			},
		},
		{
			name:    "merge_and_split",
			szt:     exporterhelper.RequestSizerTypeBytes,
			maxSize: profilesMarshaler.ProfilesSize(testdata.GenerateProfiles(10)),
			pr1:     newProfilesRequest(testdata.GenerateProfiles(8)),
			pr2:     newProfilesRequest(testdata.GenerateProfiles(20)),
			expected: []Request{
				newProfilesRequest(func() pprofile.Profiles {
					profiles := testdata.GenerateProfiles(7)
					testdata.GenerateProfiles(3).ResourceProfiles().MoveAndAppendTo(profiles.ResourceProfiles())
					return profiles
				}()),
				newProfilesRequest(testdata.GenerateProfiles(11)),
				newProfilesRequest(testdata.GenerateProfiles(7)),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tt.pr1.MergeSplit(context.Background(), tt.maxSize, tt.szt, tt.pr2)
			require.NoError(t, err)
			require.Len(t, res, len(tt.expected))
			for i, r := range res {
				assert.Equal(t, tt.expected[i].(*profilesRequest).pd.SampleCount(), r.(*profilesRequest).pd.SampleCount(), i)
			}
		})
	}
}

func TestExtractProfiles(t *testing.T) {
	for i := range 10 {
		ld := testdata.GenerateProfiles(10)
		extractedProfiles, _ := extractProfiles(ld, i, &sizer.ProfilesCountSizer{})
		assert.Equal(t, i, extractedProfiles.SampleCount())
		assert.Equal(t, 10-i, ld.SampleCount())
	}
}

func TestMergeSplitManySmallProfiles(t *testing.T) {
	// All requests merge into a single batch.
	merged := []Request{newProfilesRequest(testdata.GenerateProfiles(1))}
	for range 1000 {
		pr2 := newProfilesRequest(testdata.GenerateProfiles(10))
		res, _ := merged[len(merged)-1].MergeSplit(context.Background(), 10000, exporterhelper.RequestSizerTypeItems, pr2)
		merged = append(merged[0:len(merged)-1], res...)
	}
	assert.Len(t, merged, 2)
}

func BenchmarkSplittingBasedOnByteSizeManySmallProfiles(b *testing.B) {
	// All requests merge into a single batch.
	b.ReportAllocs()
	for b.Loop() {
		merged := []Request{newProfilesRequest(testdata.GenerateProfiles(10))}
		for range 1000 {
			pr2 := newProfilesRequest(testdata.GenerateProfiles(10))
			res, _ := merged[len(merged)-1].MergeSplit(
				context.Background(),
				profilesMarshaler.ProfilesSize(testdata.GenerateProfiles(11000)),
				exporterhelper.RequestSizerTypeBytes,
				pr2,
			)
			merged = append(merged[0:len(merged)-1], res...)
		}
		assert.Len(b, merged, 2)
	}
}

func BenchmarkSplittingBasedOnByteSizeManyProfilesSlightlyAboveLimit(b *testing.B) {
	// Every incoming request results in a split.
	b.ReportAllocs()
	for b.Loop() {
		merged := []Request{newProfilesRequest(testdata.GenerateProfiles(0))}
		for range 10 {
			pr2 := newProfilesRequest(testdata.GenerateProfiles(10001))
			res, _ := merged[len(merged)-1].MergeSplit(
				context.Background(),
				profilesMarshaler.ProfilesSize(testdata.GenerateProfiles(10000)),
				exporterhelper.RequestSizerTypeBytes,
				pr2,
			)
			assert.Len(b, res, 2)
			merged = append(merged[0:len(merged)-1], res...)
		}
		assert.Len(b, merged, 11)
	}
}

func BenchmarkSplittingBasedOnByteSizeHugeProfiles(b *testing.B) {
	// One request splits into many batches.
	b.ReportAllocs()
	for b.Loop() {
		merged := []Request{newProfilesRequest(testdata.GenerateProfiles(0))}
		pr2 := newProfilesRequest(testdata.GenerateProfiles(100000))
		res, _ := merged[len(merged)-1].MergeSplit(
			context.Background(),
			profilesMarshaler.ProfilesSize(testdata.GenerateProfiles(10010)),
			exporterhelper.RequestSizerTypeBytes,
			pr2,
		)
		merged = append(merged[0:len(merged)-1], res...)
		assert.Len(b, merged, 10)
	}
}

// TestProfilesRequest_CloneIfShared mirrors the contract that the
// metrics / traces / logs symmetric tests verify (see audit-#19 commit
// 3de2b3efc and queuebatch/metrics_batch_test.go's
// TestMetricsRequest_CloneIfShared for the canonical comment).
func TestProfilesRequest_CloneIfShared(t *testing.T) {
	t.Run("mutable input returns same request", func(t *testing.T) {
		pd := testdata.GenerateProfiles(10)
		require.False(t, pd.IsReadOnly())
		req := &profilesRequest{pd: pd, cachedSize: -1}

		got := req.cloneIfShared()

		// Same pointer — no clone happened.
		assert.Same(t, req, got)
	})

	t.Run("read-only input is deep-cloned", func(t *testing.T) {
		original := testdata.GenerateProfiles(10)
		original.MarkReadOnly()
		require.True(t, original.IsReadOnly())
		preLen := original.ResourceProfiles().Len()
		req := &profilesRequest{pd: original, cachedSize: -1}

		got := req.cloneIfShared()

		// Different request, mutable pd, no shared backing.
		assert.NotSame(t, req, got)
		assert.False(t, got.pd.IsReadOnly())
		assert.Equal(t, req.pd.SampleCount(), got.pd.SampleCount())

		// Mutating the clone does not touch the original — verifies the
		// deep-clone happened, not just a shallow handle swap.
		got.pd.ResourceProfiles().AppendEmpty()
		assert.Equal(t, preLen+1, got.pd.ResourceProfiles().Len())
		assert.Equal(t, preLen, original.ResourceProfiles().Len(),
			"original ResourceProfiles slice must not grow")
	})
}

// TestMergeSplitProfiles_ReadOnlyR2NotMutated exercises the second-arg
// cloneIfShared branch in MergeSplit: when r2 (the additional request
// being merged into req) is read-only, the source data backing r2 must
// remain untouched after the merge. Profiles is the fourth signal
// mirror of the metrics/traces/logs ReadOnlyR2NotMutated coverage from
// commit 21c725e23, with one extra twist: pprofile.Profiles.MergeTo
// itself calls AssertMutable on the source, so without cloneIfShared
// the merge would panic on a read-only r2 — this test also doubles as
// a regression check for that panic.
func TestMergeSplitProfiles_ReadOnlyR2NotMutated(t *testing.T) {
	req := &profilesRequest{pd: testdata.GenerateProfiles(2), cachedSize: -1}

	r2Source := testdata.GenerateProfiles(3)
	r2Source.MarkReadOnly()
	preLen := r2Source.ResourceProfiles().Len()
	r2 := &profilesRequest{pd: r2Source, cachedSize: -1}

	_, err := req.MergeSplit(context.Background(), 0, exporterhelper.RequestSizerTypeItems, r2)
	require.NoError(t, err)

	// r2's backing tree must not have been touched by MergeTo's
	// MoveAndAppendTo / MarkReadOnly — the in-batcher cloneIfShared on
	// r2 should have produced a clone that took the mutation instead.
	assert.Equal(t, preLen, r2Source.ResourceProfiles().Len(),
		"r2's source ResourceProfiles slice must not be cleared by mergeTo")
	assert.True(t, r2Source.IsReadOnly(), "r2's source remains read-only")
}
