// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

type yusProgrammeAnalyzer struct {
	reports map[string]string
	targets []string
}

func (a *yusProgrammeAnalyzer) Analyze(_ context.Context, target string) (string, []byte, error) {
	a.targets = append(a.targets, target)
	return "General\nComplete name : " + target, []byte(a.reports[target]), nil
}

func TestSelectedYUSCollectsItsOwnProgrammeEvidence(t *testing.T) {
	t.Parallel()
	registry, err := impl.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	mainReport := strings.ReplaceAll(packMediaReport, `"Language":"ja"`, `"Language":"en","Title":"Main"`)
	alternateReport := strings.ReplaceAll(mainReport, `"Title":"Main"`, `"Title":"Alternate Mix"`)
	for _, tc := range []struct {
		name          string
		selected      []api.TrackerID
		count         int
		secondReport  string
		complete      bool
		namingBlocked bool
	}{
		{"YUS-only seven-member season", []api.TrackerID{"YUS"}, 7, alternateReport, true, false},
		{"IHD-only retains primary inspection", []api.TrackerID{"IHD"}, 7, alternateReport, false, true},
		{"IHD and YUS without LST", []api.TrackerID{"IHD", "YUS"}, 7, alternateReport, true, false},
		{"LST still inspects whole programme", []api.TrackerID{"LST"}, 7, alternateReport, true, false},
		{"single-file movie", []api.TrackerID{"YUS"}, 1, mainReport, true, false},
		{"unknown later-member language", []api.TrackerID{"YUS"}, 7, strings.ReplaceAll(alternateReport, `,"Language":"en"`, ""), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			analyzer := &yusProgrammeAnalyzer{reports: map[string]string{}}
			files := make([]string, tc.count)
			for i := range files {
				files[i] = filepath.Join(root, fmt.Sprintf("Example.Series.S01E%02d.1080p.WEB-DL-GRP.mkv", i+1))
				if tc.count == 1 {
					files[i] = filepath.Join(root, "Example.Movie.2026.1080p.WEB-DL-GRP.mkv")
				}
				analyzer.reports[files[i]] = mainReport
				if i > 0 {
					analyzer.reports[files[i]] = tc.secondReport
				}
			}
			primaryReport := filepath.Join(root, "primary.json")
			if writeErr := os.WriteFile(primaryReport, []byte(mainReport), 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			requirements, collectErr := trackers.CollectMetadataRequirements(registry, tc.selected)
			if collectErr != nil {
				t.Fatal(collectErr)
			}
			service := NewService(
				&stubRepo{},
				WithMediaInfoExporter(mediainfo.NewService(nil, analyzer)),
				WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "db.sqlite")}}),
			)
			original := preparationstate.State{
				SourcePath:           root,
				VideoPath:            files[0],
				FileList:             files,
				SourceFingerprint:    "synthetic-manifest-v1",
				MediaInfoJSONPath:    primaryReport,
				MetadataRequirements: requirements,
				Type:                 "WEBDL",
			}
			if tc.count == 1 {
				original.SourcePath = files[0]
				original.Release.Category = "MOVIE"
			}
			got, deriveErr := service.deriveMediaFacts(t.Context(), original)
			if deriveErr != nil {
				t.Fatal(deriveErr)
			}
			if got.SourcePath != original.SourcePath || got.SourceFingerprint != original.SourceFingerprint || got.VideoPath != files[0] ||
				!slices.Equal(got.FileList, files) ||
				got.TrackCoverageComplete != tc.complete {
				t.Fatalf("source/coverage changed: %+v", got)
			}
			if requirements.InspectProgrammeTracks {
				if len(got.MediaFileFacts.Files) != tc.count || len(got.MediaTracks) != tc.count || len(analyzer.targets) != tc.count {
					t.Fatalf(
						"full selected programme not measured: files=%d tracks=%d targets=%v",
						len(got.MediaFileFacts.Files),
						len(got.MediaTracks),
						analyzer.targets,
					)
				}
				seen := map[string]bool{}
				for i, file := range files {
					resource := original
					resource.VideoPath = file
					want := mediaTrackResourceID(resource)
					if got.MediaFileFacts.Files[i].ResourceID != want || got.MediaTracks[i].ResourceID != want ||
						got.MediaTracks[i].ManifestFingerprint == "" ||
						seen[want] {
						t.Fatal("measured file/track manifest resources are not one-to-one")
					}
					seen[want] = true
					role := api.AudioRoleProgramme
					if i > 0 && strings.Contains(tc.secondReport, `"Title":"Alternate Mix"`) {
						role = api.AudioRoleAlternateMix
					}
					if got.MediaTracks[i].Role != role {
						t.Fatalf("measured role changed: %+v", got.MediaTracks[i])
					}
				}
			} else if len(analyzer.targets) != 0 || len(got.MediaFileFacts.Files) != 0 || len(got.MediaTracks) != 1 {
				t.Fatal("IHD-only acquired full-programme demand")
			}
			facts := mediafacts.ResolveLanguages(api.MediaFacts{
				Tracks:                got.MediaTracks,
				PrimaryAudioTrackID:   got.PrimaryAudioTrackID,
				TrackCoverageComplete: got.TrackCoverageComplete,
				AudioAbsent:           got.AudioAbsent,
			})
			subject := api.NewTrackerValidationSubject(api.UploadSubject{LanguageFacts: facts}, "YUS")
			failures, validationErr := trackers.EvaluateTrackerValidationWithRegistry(t.Context(), registry, "YUS", subject, api.NopLogger{})
			if validationErr != nil {
				t.Fatal(validationErr)
			}
			blocked := slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
				return f.Rule == "language_naming_evidence" && f.Disposition == api.RuleDispositionStrict
			})
			if blocked != tc.namingBlocked {
				t.Fatalf("YUS language gate changed: facts=%+v failures=%+v", facts, failures)
			}
		})
	}
}
