package dp_test

import (
	"reflect"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/dp"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDPProgrammeInspectionBelongsToSelectedTracker(t *testing.T) {
	t.Parallel()
	registry, err := impl.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		selected []api.TrackerID
		inspect  bool
	}{
		{"DP alone", []api.TrackerID{"DP"}, true},
		{"IHD alone", []api.TrackerID{"IHD"}, false},
		{"LST unchanged", []api.TrackerID{"LST"}, true},
		{"approved IHD and DP", []api.TrackerID{"IHD", "DP"}, true},
		{"no tracker", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requirements, collectErr := trackers.CollectMetadataRequirements(registry, tc.selected)
			if collectErr != nil {
				t.Fatal(collectErr)
			}
			if requirements.InspectProgrammeTracks != tc.inspect {
				t.Fatalf("requirements=%+v", requirements)
			}
		})
	}
	previous := unit3d.NewWithProfile(unit3d.Profile{Name: "EXAMPLE"}).MetadataPolicy()
	current := unit3d.NewWithProfile(dp.Profile()).MetadataPolicy()
	if !reflect.DeepEqual(previous.Requirements, current.Requirements) {
		t.Fatalf("strict metadata requirements changed: before=%+v after=%+v", previous, current)
	}
}
