// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cow // import "go.opentelemetry.io/collector/pdata/xpdata/cow"

import (
	"go.opentelemetry.io/collector/pdata/internal"
	"go.opentelemetry.io/collector/pdata/pprofile"
)

// ShareProfiles — see ShareMetrics.
func ShareProfiles(pd pprofile.Profiles) pprofile.Profiles {
	if !FeatureGate.IsEnabled() {
		return pd
	}
	w := internal.ProfilesWrapper(pd)
	state := internal.GetProfilesState(w)
	orig := internal.GetProfilesOrig(w)
	state.IncCowRefs()
	return pprofile.Profiles(internal.NewProfilesWrapper(orig, state))
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
