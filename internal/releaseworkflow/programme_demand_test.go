// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/metadata/tvmaze"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

type programmeDemandEvaluator struct {
	selectionDemandEvaluator
	registry *trackers.Registry
}

func (e programmeDemandEvaluator) Requirements(_ context.Context, selected []api.TrackerID) (api.MetadataRequirementSet, error) {
	requirements, err := trackers.CollectMetadataRequirements(e.registry, selected)
	if err != nil {
		return api.MetadataRequirementSet{}, fmt.Errorf("collect selected programme requirements: %w", err)
	}
	return requirements, nil
}

type programmeDemandTransport struct{}

func (programmeDemandTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"d":[],"results":[]}`)),
		Request:    request,
	}, nil
}

type programmeDemandAnalyzer struct{}

func (programmeDemandAnalyzer) Analyze(_ context.Context, target string) (string, []byte, error) {
	return "General\nComplete name : " + target, []byte(
		`{"media":{"track":[{"@type":"General","Format":"Matroska","UniqueID":"123"},{"@type":"Video","Format":"AVC","Width":"1920","Height":"1080","BitDepth":"8"},{"@type":"Audio","Format":"AC-3","Language":"en","Title":"Main","Default":"Yes"}]}}`,
	), nil
}

func TestSelectionRecollectsSparseProgrammeWithoutLST(t *testing.T) {
	t.Parallel()
	for _, tracker := range []api.TrackerID{"YUS", "DP"} {
		t.Run(string(tracker), func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "Example.Series.S01.1080p.WEB-DL-GRP")
			if err := os.Mkdir(source, 0o700); err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 7; i++ {
				path := filepath.Join(source, fmt.Sprintf("Example.Series.S01E%02d.1080p.WEB-DL-GRP.mkv", i))
				if err := os.WriteFile(path, []byte("synthetic programme"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			database, err := db.Open(filepath.Join(root, "prepared.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			if err = database.Migrate(); err != nil {
				t.Fatal(err)
			}
			offlineHTTP := &http.Client{Transport: programmeDemandTransport{}}
			service := metadata.NewService(
				database,
				metadata.WithIMDBClient(imdb.NewClient(offlineHTTP, nil)),
				metadata.WithTVmazeClient(tvmaze.NewClient(offlineHTTP, nil)),
				metadata.WithTMDBClient(tmdb.NewClient(offlineHTTP, nil, "synthetic-key")),
				metadata.WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "prepared.sqlite")}}),
				metadata.WithMediaInfoExporter(mediainfo.NewService(nil, programmeDemandAnalyzer{})),
			)
			collector, err := preparedrelease.NewEvidenceCollector(service)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := externalidentity.NewWithCandidateSource(database, collector)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := preparedrelease.New(database, identity, collector)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			preparer := testPreparer()
			preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				calls.Add(1)
				return prepared.Prepare(ctx, input)
			}
			preparer.DisplayFunc = prepared.ResolveDisplay
			preparer.SubjectFunc = prepared.ResolveUploadSubject
			preparer.DuplicateFunc = prepared.ResolveDuplicateSubject
			registry, err := impl.NewRegistry()
			if err != nil {
				t.Fatal(err)
			}
			module, _ := newTestModule(
				t,
				preparer,
				WithClock(&selectionEnrichmentClock{}),
				WithInputReadinessEvaluator(programmeDemandEvaluator{registry: registry}),
			)
			current := executeCommand(t, module, CreateWorkflowCommand{})
			current = executeCommand(t, module, PrepareReleaseCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: source},
				TrackerIDs:       []api.TrackerID{"IHD"},
			})
			sparse := current.Release.Release
			if sparse.Source.SourcePath != source ||
				len(slices.DeleteFunc(slices.Clone(sparse.Source.Entries), func(entry api.SourceManifestEntry) bool { return entry.Type != "file" })) != 7 ||
				sparse.Media.TrackCoverageComplete ||
				len(sparse.Media.Tracks) != 1 {
				t.Fatalf("fixture must start with genuine sparse IHD generation: %+v", sparse)
			}
			current = continueForSelectionEnrichment(t, module, current, []api.TrackerID{tracker})
			if calls.Load() != 2 || current.Release == nil {
				t.Fatalf("selected programme demand did not reprepare: calls=%d release_present=%t", calls.Load(), current.Release != nil)
			}
			full := current.Release.Release
			if full.Generation <= sparse.Generation || full.Source.SourcePath != source || !slices.Equal(full.Source.Entries, sparse.Source.Entries) ||
				!full.Media.TrackCoverageComplete ||
				len(full.Media.MediaFileFacts.Files) != 7 {
				t.Fatalf("new generation lost exact source or programme coverage: %+v", full)
			}
			resources := map[string]bool{}
			for _, file := range full.Media.MediaFileFacts.Files {
				resources[file.ResourceID] = true
			}
			audio := 0
			for _, track := range full.Media.Tracks {
				if track.Kind == api.MediaTrackAudio {
					audio++
					if !resources[track.ResourceID] || track.ManifestFingerprint == "" {
						t.Fatal("programme audio has foreign manifest resource")
					}
				}
			}
			if audio != 7 || len(resources) != 7 {
				t.Fatal("programme resources not completely measured")
			}
			subject, err := prepared.ResolveUploadSubject(
				t.Context(),
				api.UploadSubjectInput{Release: api.ReleaseRef{SourcePath: source, Generation: full.Generation}, Trackers: []string{string(tracker)}},
			)
			if err != nil {
				t.Fatal(err)
			}
			if subject.SourcePath != source || subject.Identity.Generation != full.Generation || !subject.LanguageFacts.TrackCoverageComplete ||
				subject.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusComplete {
				t.Fatal("exact-generation subject did not retain measured programme evidence")
			}
			current = continueForSelectionEnrichment(t, module, current, []api.TrackerID{tracker})
			if calls.Load() != 2 || current.InputReadiness == nil || !slices.Equal(current.InputReadiness.SelectedTrackerIDs, []api.TrackerID{tracker}) {
				t.Fatal("same programme demand repeated preparation or widened scope")
			}
		})
	}
}

func TestProgrammeDemandReusesCompleteFactsAndDoesNotLoopOnPartialInspection(t *testing.T) {
	t.Parallel()
	registry, err := impl.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		complete      bool
		expectedCalls int32
	}{
		{"existing complete measurement reused", true, 1},
		{"unavailable later resources attempted once", false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			preparer := testPreparer()
			base := preparer.PrepareFunc
			preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
				result, prepareErr := base(ctx, input)
				result.Release.Generation = api.PreparedGeneration(calls.Add(1))
				result.Release.Media.TrackCoverageComplete = tc.complete
				result.Release.Identity.TMDBID = 1234567
				result.Release.ProviderMetadata.TMDB = &api.TMDBMetadata{TMDBID: 1234567, Category: "movie"}
				return result, prepareErr
			}
			module, _ := newTestModule(
				t,
				preparer,
				WithClock(&selectionEnrichmentClock{}),
				WithInputReadinessEvaluator(programmeDemandEvaluator{registry: registry}),
			)
			current := executeCommand(t, module, CreateWorkflowCommand{})
			current = executeCommand(t, module, PrepareReleaseCommand{
				WorkflowID:       current.Workflow.ID,
				ExpectedRevision: current.Workflow.Revision,
				Input:            api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.mkv")},
				TrackerIDs:       []api.TrackerID{"IHD"},
			})
			for range 3 {
				current = continueForSelectionEnrichment(t, module, current, []api.TrackerID{"YUS"})
			}
			if calls.Load() != tc.expectedCalls {
				t.Fatalf("preparations=%d want=%d", calls.Load(), tc.expectedCalls)
			}
			if current.InputReadiness == nil || !slices.Equal(current.InputReadiness.SelectedTrackerIDs, []api.TrackerID{"YUS"}) {
				t.Fatal("programme demand widened selected scope")
			}
		})
	}
}
