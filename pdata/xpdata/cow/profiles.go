// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cow // import "go.opentelemetry.io/collector/pdata/xpdata/cow"

import (
	"go.opentelemetry.io/collector/pdata/internal"
	"go.opentelemetry.io/collector/pdata/pprofile"
)

// ShareProfiles — see ShareMetrics for the full doc + Phase 1 limitation.
func ShareProfiles(pd pprofile.Profiles) pprofile.Profiles {
	if !FeatureGate.IsEnabled() {
		return pd
	}
	sourceOrig := internal.GetProfilesOrig(internal.ProfilesWrapper(pd))
	sharedState := internal.NewState()
	sharedState.IncCowRefs()
	return pprofile.Profiles(internal.NewProfilesWrapper(sourceOrig, sharedState))
}

// ReleaseProfiles — see ReleaseMetrics.
func ReleaseProfiles(pd pprofile.Profiles) {
	if !FeatureGate.IsEnabled() {
		return
	}
	internal.GetProfilesState(internal.ProfilesWrapper(pd)).DecCowRefs()
}

// IsSharedProfiles — see IsSharedMetrics.
func IsSharedProfiles(pd pprofile.Profiles) bool {
	if !FeatureGate.IsEnabled() {
		return false
	}
	return internal.GetProfilesState(internal.ProfilesWrapper(pd)).CowRefs() > 0
}
