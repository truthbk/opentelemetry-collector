// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package fanoutconsumer // import "go.opentelemetry.io/collector/internal/fanoutconsumer"

import (
	"context"

	"go.uber.org/multierr"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/xpdata/cow"
)

// NewMetrics wraps multiple metrics consumers in a single one.
// It fans out the incoming data to all the consumers, and does smart routing:
//   - Clones only to the consumer that needs to mutate the data.
//   - If all consumers needs to mutate the data one will get the original mutable data.
func NewMetrics(mcs []consumer.Metrics) consumer.Metrics {
	// Don't wrap if there is only one non-mutating consumer.
	if len(mcs) == 1 && !mcs[0].Capabilities().MutatesData {
		return mcs[0]
	}

	mc := &metricsConsumer{}
	for i := range mcs {
		if mcs[i].Capabilities().MutatesData {
			mc.mutable = append(mc.mutable, mcs[i])
		} else {
			mc.readonly = append(mc.readonly, mcs[i])
		}
	}
	return mc
}

type metricsConsumer struct {
	mutable  []consumer.Metrics
	readonly []consumer.Metrics
}

func (msc *metricsConsumer) Capabilities() consumer.Capabilities {
	// If all consumers are mutating, then the original data will be passed to one of them.
	return consumer.Capabilities{MutatesData: len(msc.mutable) > 0 && len(msc.readonly) == 0}
}

// ConsumeMetrics exports the pmetric.Metrics to all consumers wrapped by the current one.
func (msc *metricsConsumer) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	var errs error

	if len(msc.mutable) > 0 {
		// Clone the data before sending to all mutating consumers except the last one.
		for i := 0; i < len(msc.mutable)-1; i++ {
			errs = multierr.Append(errs, msc.mutable[i].ConsumeMetrics(ctx, cloneMetrics(md)))
		}
		// Send data as is to the last mutating consumer only if there are no other non-mutating consumers and the
		// data is mutable. Never share the same data between a mutating and a non-mutating consumer since the
		// non-mutating consumer may process data async and the mutating consumer may change the data before that.
		lastConsumer := msc.mutable[len(msc.mutable)-1]
		if len(msc.readonly) == 0 && !md.IsReadOnly() {
			errs = multierr.Append(errs, lastConsumer.ConsumeMetrics(ctx, md))
		} else {
			errs = multierr.Append(errs, lastConsumer.ConsumeMetrics(ctx, cloneMetrics(md)))
		}
	}

	// Mark the data as read-only if it will be sent to more than one read-only consumer.
	if len(msc.readonly) > 1 && !md.IsReadOnly() {
		md.MarkReadOnly()
	}
	for _, mc := range msc.readonly {
		errs = multierr.Append(errs, mc.ConsumeMetrics(ctx, md))
	}

	return errs
}

// cloneMetrics returns a wrapper independent of md. Under the pdata.cow
// feature gate, the call goes through cow.ShareMetrics which produces a
// share wrapper at the same backing orig tree but with its own State —
// no proto allocation happens here, the deferred clone is up to the
// downstream consumer's first mutation (Phase 1: consumers that declare
// MutatesData but don't actually mutate skip the clone entirely;
// consumers that DO mutate without Phase 2's codegen-injected detach
// will write to the source — see ShareMetrics's doc for the contract).
// Gate-off path stays the unconditional CopyTo.
func cloneMetrics(md pmetric.Metrics) pmetric.Metrics {
	if cow.FeatureGate.IsEnabled() {
		return cow.ShareMetrics(md)
	}
	clonedMetrics := pmetric.NewMetrics()
	md.CopyTo(clonedMetrics)
	return clonedMetrics
}
