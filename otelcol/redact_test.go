// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otelcol

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/confmap"
)

func TestMapListPairKeys(t *testing.T) {
	conf := confmap.New()
	require.NoError(t, conf.Marshal(configopaque.Pair{
		Name:  "header",
		Value: "secret",
	}))

	assert.Equal(t, map[string]any{
		nameKey:  "header",
		valueKey: redactedMask,
	}, conf.ToStringMap())
}

func TestRedactWithPreExpansion(t *testing.T) {
	tests := []struct {
		name     string
		pre      map[string]any
		redacted map[string]any
		want     map[string]any
	}{
		{
			name: "redact hardcoded opaque value",
			pre: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"token": "secret",
						"other": "hello",
					},
				},
			},
			redacted: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"token": redactedMask,
						"other": "hello",
					},
				},
			},
			want: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"token": redactedMask,
						"other": "hello",
					},
				},
			},
		},
		{
			name: "redact provider reference that embeds a literal default",
			pre: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{ // #nosec G101
						"token": "${env:NAME:-literal}",
					},
				},
			},
			redacted: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"token": redactedMask,
					},
				},
			},
			want: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"token": redactedMask,
					},
				},
			},
		},
		{
			name: "preserve provider reference with an empty default",
			pre: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{ // #nosec G101
						"token": "${env:NAME:-}",
					},
				},
			},
			redacted: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"token": redactedMask,
					},
				},
			},
			want: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{ // #nosec G101
						"token": "${env:NAME:-}",
					},
				},
			},
		},
		{
			name: "preserve non-env provider reference that contains the default marker",
			pre: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{ // #nosec G101
						"token": "${file:c:/dir/name:-backup}",
					},
				},
			},
			redacted: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"token": redactedMask,
					},
				},
			},
			want: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{ // #nosec G101
						"token": "${file:c:/dir/name:-backup}",
					},
				},
			},
		},
		{
			name: "redact and preserve values in a map-style list",
			pre: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"headers": []any{
							map[string]any{"name": "Authorization", "value": "${env:TOKEN:-literal}"},
							map[string]any{"name": "X-Other", "value": "${env:OTHER}"},
						},
					},
				},
			},
			redacted: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"headers": []any{
							map[string]any{"name": "Authorization", "value": redactedMask},
							map[string]any{"name": "X-Other", "value": redactedMask},
						},
					},
				},
			},
			want: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"headers": []any{
							map[string]any{"name": "Authorization", "value": redactedMask},
							map[string]any{"name": "X-Other", "value": "${env:OTHER}"},
						},
					},
				},
			},
		},
		{
			name: "preserve provider reference at opaque leaf",
			pre: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{ // #nosec G101
						"token": "${env:TOKEN}",
					},
				},
			},
			redacted: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"token": redactedMask,
					},
				},
			},
			want: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{ // #nosec G101
						"token": "${env:TOKEN}",
					},
				},
			},
		},
		{
			name: "redact embedded provider reference at opaque leaf",
			pre: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{ // #nosec G101
						"token": "Bearer ${env:TOKEN}",
					},
				},
			},
			redacted: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"token": redactedMask,
					},
				},
			},
			want: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"token": redactedMask,
					},
				},
			},
		},
		{
			name: "redact provider reference with hardcoded suffix at opaque leaf",
			pre: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{ // #nosec G101
						"token": "${env:TOKEN} hardcoded-secret",
					},
				},
			},
			redacted: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"token": redactedMask,
					},
				},
			},
			want: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"token": redactedMask,
					},
				},
			},
		},
		{
			name: "redact nested map and slice",
			pre: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"nested": map[string]any{
							"secret": "abc",
							"list":   []any{"a", "${env:B}", "c"},
						},
					},
				},
			},
			redacted: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"nested": map[string]any{
							"secret": redactedMask,
							"list":   []any{redactedMask, redactedMask, redactedMask},
						},
					},
				},
			},
			want: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"nested": map[string]any{
							"secret": redactedMask,
							"list":   []any{redactedMask, "${env:B}", redactedMask},
						},
					},
				},
			},
		},
		{
			name: "redact map transformed to name value list",
			pre: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"headers": map[string]any{ // #nosec G101
							"token": "abc",
							"other": "${env:HEADER}",
						},
					},
				},
			},
			redacted: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"headers": []any{
							map[string]any{nameKey: "token", valueKey: redactedMask},
							map[string]any{nameKey: "other", valueKey: redactedMask},
						},
					},
				},
			},
			want: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"headers": map[string]any{ // #nosec G101
							"token": redactedMask,
							"other": "${env:HEADER}",
						},
					},
				},
			},
		},
		{
			name: "ignore malformed transformed map list entries",
			pre: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"headers": map[string]any{ // #nosec G101
							"non-map":         "unchanged",
							"123":             "unchanged",
							"missing-value":   "unchanged",
							"extra-field":     "unchanged",
							"wrong-value-key": "unchanged",
							"valid":           "secret",
						},
					},
				},
			},
			redacted: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"headers": []any{
							"non-map",
							map[string]any{nameKey: 123, valueKey: redactedMask},
							map[string]any{nameKey: "missing-value"},
							map[string]any{nameKey: "extra-field", valueKey: redactedMask, "extra": true},
							map[string]any{nameKey: "wrong-value-key", "value-name": redactedMask},
							map[string]any{nameKey: "valid", valueKey: redactedMask},
						},
					},
				},
			},
			want: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"headers": map[string]any{ // #nosec G101
							"non-map":         "unchanged",
							"123":             "unchanged",
							"missing-value":   "unchanged",
							"extra-field":     "unchanged",
							"wrong-value-key": "unchanged",
							"valid":           redactedMask,
						},
					},
				},
			},
		},
		{
			name: "non-opaque strings untouched",
			pre: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						// Value happens to equal the masked string but is not an
						// opaque field — the redacted view confirms that by
						// carrying the same plain string back.
						"other": redactedMask,
					},
				},
			},
			redacted: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"other": redactedMask,
					},
				},
			},
			want: map[string]any{
				"exporters": map[string]any{
					"foo": map[string]any{
						"other": redactedMask,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pre := confmap.NewFromStringMap(tt.pre)
			red := confmap.NewFromStringMap(tt.redacted)
			got := redactor{}.redactByMirroring(pre, red)
			require.NotNil(t, got)
			assert.Equal(t, tt.want, got.ToStringMap())
		})
	}
}

func TestRedactWithPreExpansion_NilPreExpansion(t *testing.T) {
	got := redactor{}.redactByMirroring(nil, confmap.New())
	assert.Nil(t, got)
}

func TestRedactWithPreExpansion_NilRedacted(t *testing.T) {
	pre := confmap.NewFromStringMap(map[string]any{"a": "b"})
	got := redactor{}.redactByMirroring(pre, nil)
	require.NotNil(t, got)
	assert.Equal(t, map[string]any{"a": "b"}, got.ToStringMap())
}

func TestRedactWithExtraReferencePatterns(t *testing.T) {
	// A distribution whose secret backend resolves ENC[...] and SEC[...] handles.
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`^ENC\[[^\]]*\]$`),
		regexp.MustCompile(`^SEC\[[^\]]*\]$`),
	}

	tests := []struct {
		name     string
		patterns []*regexp.Regexp
		value    string
		want     string
	}{
		{
			name:  "handle is masked without patterns",
			value: "ENC[api_key]",
			want:  redactedMask,
		},
		{
			name:     "whole handle is preserved",
			patterns: patterns,
			value:    "ENC[api_key]",
			want:     "ENC[api_key]",
		},
		{
			name:     "second syntax is preserved",
			patterns: patterns,
			value:    "SEC[api_key]",
			want:     "SEC[api_key]",
		},
		{
			name:     "interpolated handle is masked",
			patterns: patterns,
			value:    "Bearer SEC[token]",
			want:     redactedMask,
		},
		{
			name:     "two handles in one leaf are masked",
			patterns: patterns,
			value:    "ENC[a]ENC[b]",
			want:     redactedMask,
		},
		{
			name:     "literal is masked",
			patterns: patterns,
			value:    "hunter2",
			want:     redactedMask,
		},
		{
			// An unanchored pattern must not expose an interpolated value: the
			// match is required to span the whole leaf whatever the pattern says.
			name:     "unanchored pattern cannot expose interpolation",
			patterns: []*regexp.Regexp{regexp.MustCompile(`ENC\[[^\]]*\]`)},
			value:    "Bearer ENC[token]",
			want:     redactedMask,
		},
		{
			name:     "unanchored pattern still preserves a whole handle",
			patterns: []*regexp.Regexp{regexp.MustCompile(`ENC\[[^\]]*\]`)},
			value:    "ENC[token]",
			want:     "ENC[token]",
		},
		{
			name:     "provider references are preserved alongside extra patterns",
			patterns: patterns,
			value:    "${env:API_KEY}",
			want:     "${env:API_KEY}",
		},
		{
			name:     "env default literal stays masked alongside extra patterns",
			patterns: patterns,
			value:    "${env:API_KEY:-hunter2}",
			want:     redactedMask,
		},
		{
			name:     "nil pattern is skipped",
			patterns: []*regexp.Regexp{nil},
			value:    "ENC[api_key]",
			want:     redactedMask,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pre := confmap.NewFromStringMap(map[string]any{
				"exporters": map[string]any{"foo": map[string]any{"token": tt.value}},
			})
			red := confmap.NewFromStringMap(map[string]any{
				"exporters": map[string]any{"foo": map[string]any{"token": redactedMask}},
			})

			got := redactor{extraReferencePatterns: tt.patterns}.redactByMirroring(pre, red)
			require.NotNil(t, got)
			assert.Equal(t, map[string]any{
				"exporters": map[string]any{"foo": map[string]any{"token": tt.want}},
			}, got.ToStringMap())
		})
	}
}

// A non-opaque field is never masked, so an extra pattern changes nothing for it.
func TestExtraReferencePatternsDoNotAffectNonOpaqueFields(t *testing.T) {
	pre := confmap.NewFromStringMap(map[string]any{"endpoint": "Bearer ENC[token]"})
	red := confmap.NewFromStringMap(map[string]any{"endpoint": "Bearer ENC[token]"})

	got := redactor{extraReferencePatterns: []*regexp.Regexp{
		regexp.MustCompile(`^ENC\[[^\]]*\]$`),
	}}.redactByMirroring(pre, red)

	require.NotNil(t, got)
	assert.Equal(t, map[string]any{"endpoint": "Bearer ENC[token]"}, got.ToStringMap())
}
