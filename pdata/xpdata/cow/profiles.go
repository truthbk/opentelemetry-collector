// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package cow // import "go.opentelemetry.io/collector/pdata/xpdata/cow"

import (
	"go.opentelemetry.io/collector/pdata/internal"
	"go.opentelemetry.io/collector/pdata/pprofile"
)

// ShareProfiles — see ShareMetrics for the full doc + safety model.
func ShareProfiles(pd pprofile.Profiles) pprofile.Profiles {
	if !FeatureGate.IsEnabled() {
		return pd
	}
	sourceOrig := internal.GetProfilesOrig(internal.ProfilesWrapper(pd))
	sharedState := internal.NewState()
	sharedState.IncCowRefs()
	wrapper := internal.NewProfilesWrapper(sourceOrig, sharedState)
	// Path Y Phase 5 — see ShareMetrics for the full doc.
	sharedHandle := internal.GetProfilesHandle(wrapper)
	sharedState.SetDetacher(func() {
		internal.DetachIfShared(sharedHandle, internal.CopyExportProfilesServiceRequest)
	})
	return pprofile.Profiles(wrapper)
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

// DetachProfiles — see DetachMetrics for the full doc + safety model.
func DetachProfiles(pd pprofile.Profiles) pprofile.Profiles {
	if !FeatureGate.IsEnabled() {
		return pd
	}
	w := internal.ProfilesWrapper(pd)
	if internal.GetProfilesState(w).CowRefs() == 0 {
		return pd
	}
	cloned := pprofile.NewProfiles()
	pd.CopyTo(cloned)
	internal.GetProfilesState(w).DecCowRefs()
	return cloned
}
