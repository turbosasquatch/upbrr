package metadata

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestProgrammeInspectionCollectionSufficiencyUsesMeasuredCoverage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                          string
		demand, coverage, audioAbsent bool
		want                          bool
	}{
		{"no programme demand", false, false, false, true},
		{"primary-only pack", true, false, false, false},
		{"measured full programme", true, true, false, true},
		{"verified single-file audio absence", true, true, true, true},
		{"unverified audio absence", true, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Collection sufficiency does not manufacture language/role acceptance.
			release := api.PreparedRelease{Media: api.MediaFacts{
				TrackCoverageComplete: tc.coverage,
				AudioAbsent:           tc.audioAbsent,
				Tracks:                []api.MediaTrackFacts{{Kind: api.MediaTrackAudio}},
			}}
			got := PreparedRequirementsSatisfied(api.MetadataRequirementSet{InspectProgrammeTracks: tc.demand}, release, api.ReleaseFactInstructions{})
			if got != tc.want {
				t.Fatalf("sufficient=%t want=%t", got, tc.want)
			}
		})
	}
}
