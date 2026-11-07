// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dp

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns DP's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:              "DP",
		BaseURL:           "https://darkpeers.org",
		Rules:             Rules(),
		ValidationPolicy:  validationPolicy(),
		BannedGroups:      BannedGroups(),
		ReleaseNamePolicy: namePolicy(),
		// Strict language naming needs measured evidence from every programme file.
		MetadataPolicy: &trackers.TrackerMetadataPolicy{
			InspectProgrammeTracks: true,
			Requirements: []trackers.MetadataRequirement{{
				Scope:       trackers.MetadataScopeAny,
				AnyOf:       []trackers.MetadataField{trackers.MetadataFieldTMDB},
				Disposition: api.RuleDispositionStrict,
			}},
		},
		Site: unit3d.SiteProfile{AllowSeasonZeroEpisodes: true, ProjectionQuestionnaire: manualLanguageQuestionnaire},
	}
}
