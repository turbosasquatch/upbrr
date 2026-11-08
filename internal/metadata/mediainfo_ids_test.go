// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestApplyMediaInfoIDsFromJSON(t *testing.T) {
	base := t.TempDir()
	jsonPath := filepath.Join(base, "MediaInfo.json")
	payload := `{"media":{"track":[{"@type":"General","extra":{"IMDB":"tt1234567","TMDB":"movie/765432","TVDB2":"movies/456789"}}]}}`
	if err := os.WriteFile(jsonPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write json: %v", err)
	}

	svc := &Service{}
	meta, err := svc.collectMediaInfoIdentityEvidence(context.Background(), preparationstate.State{MediaInfoJSONPath: jsonPath})
	if err != nil {
		t.Fatalf("apply mediainfo ids: %v", err)
	}
	if meta.MediaInfoTMDBID != 765432 {
		t.Fatalf("expected tmdb 765432, got %d", meta.MediaInfoTMDBID)
	}
	if meta.MediaInfoIMDBID != 1234567 {
		t.Fatalf("expected imdb 1234567, got %d", meta.MediaInfoIMDBID)
	}
	if meta.MediaInfoTVDBID != 456789 {
		t.Fatalf("expected tvdb 456789, got %d", meta.MediaInfoTVDBID)
	}
	if meta.MediaInfoCategory != "movie" {
		t.Fatalf("expected category movie, got %q", meta.MediaInfoCategory)
	}
}

func TestApplyMediaInfoIDsTextTVDBPriority(t *testing.T) {
	base := t.TempDir()
	textPath := filepath.Join(base, "mediainfo.txt")
	payload := "Writing library                          : libebml v1.4.5 + libmatroska v1.7.1\nIMDB                                     : tt7654321\nTMDB                                     : tv/765433\nTVDB                                     : 456790\nTVDB2                                    : episodes/456791\n"
	if err := os.WriteFile(textPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write text: %v", err)
	}

	svc := &Service{}
	meta, err := svc.collectMediaInfoIdentityEvidence(context.Background(), preparationstate.State{MediaInfoTextPath: textPath})
	if err != nil {
		t.Fatalf("apply mediainfo ids: %v", err)
	}
	if meta.MediaInfoTVDBID != 456790 {
		t.Fatalf("expected tvdb 456790, got %d", meta.MediaInfoTVDBID)
	}
}

func TestApplyMediaInfoIDsMismatch(t *testing.T) {
	base := t.TempDir()
	jsonPath := filepath.Join(base, "MediaInfo.json")
	payload := `{"media":{"track":[{"@type":"General","extra":{"TMDB":"movie/123"}}]}}`
	if err := os.WriteFile(jsonPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write json: %v", err)
	}

	svc := &Service{}
	meta, err := svc.collectMediaInfoIdentityEvidence(context.Background(), preparationstate.State{
		MediaInfoJSONPath: jsonPath,
		TrackerData:       []api.TrackerMetadata{{TMDBID: 999}},
	})
	if err != nil {
		t.Fatalf("apply mediainfo ids: %v", err)
	}
	if meta.MismatchedMediaInfoTMDBID != 123 {
		t.Fatalf("expected mismatched tmdb 123, got %d", meta.MismatchedMediaInfoTMDBID)
	}
}

func TestMediaInfoTMDBRejectsIMDbTokens(t *testing.T) {
	for _, token := range []string{
		"tt1234567", "movie/tt1234567", "tv/tt1234567",
		"https://www.themoviedb.org/movie/tt1234567", "https://www.themoviedb.org/tv/tt1234567/",
		" TT1234567 ", "MOVIE/TT1234567", "TV/TT1234567",
	} {
		for _, format := range []string{"json", "text"} {
			t.Run(format+"/"+token, func(t *testing.T) {
				meta := collectTestMediaInfoIDs(t, &Service{}, format, token)
				if meta.MediaInfoTMDBID != 0 || meta.MediaInfoCategory != "" || meta.MediaInfoIMDBID != 1234567 {
					t.Fatalf("malformed TMDB token changed provider identity: TMDB=%d category=%q IMDb=%d",
						meta.MediaInfoTMDBID, meta.MediaInfoCategory, meta.MediaInfoIMDBID)
				}
			})
		}
	}
}

func TestMediaInfoTMDBNumericTokensRemainValid(t *testing.T) {
	for _, tc := range []struct {
		value    string
		category string
	}{
		{value: "765432"},
		{value: "movie/765432", category: "movie"},
		{value: "tv/765432", category: "tv"},
		{value: "https://www.themoviedb.org/movie/765432", category: "movie"},
		{value: "HTTPS://WWW.THEMOVIEDB.ORG/TV/765432/", category: "tv"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			category, id := parseTMDBValue(tc.value)
			if id != 765432 || category != tc.category {
				t.Fatalf("TMDB token = (%q, %d), want (%q, 765432)", category, id, tc.category)
			}
		})
	}
}

func collectTestMediaInfoIDs(t *testing.T, svc *Service, format, tmdbToken string) preparationstate.State {
	t.Helper()
	base := t.TempDir()
	meta := preparationstate.State{SourcePath: filepath.Join(base, "Example.2026.mkv")}
	payload := fmt.Sprintf("TMDB : %s\nIMDB : tt1234567\n", tmdbToken)
	mediaInfoPath := filepath.Join(base, "mediainfo.txt")
	meta.MediaInfoTextPath = mediaInfoPath
	if format == "json" {
		payload = fmt.Sprintf(`{"media":{"track":[{"@type":"General","extra":{"TMDB":%q,"IMDB":"tt1234567"}}]}}`, tmdbToken)
		mediaInfoPath = filepath.Join(base, "mediainfo.json")
		meta.MediaInfoTextPath = ""
		meta.MediaInfoJSONPath = mediaInfoPath
	}
	if err := os.WriteFile(mediaInfoPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := svc.collectMediaInfoIdentityEvidence(t.Context(), meta)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMalformedMediaInfoTMDBFallsBackToArr(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		for _, mode := range []string{"fresh", "refresh retained mediainfo", "explicit pin"} {
			t.Run(format+"/"+mode, func(t *testing.T) {
				repo := &fakeRepo{}
				tmdbClient := &stubTMDB{metadata: tmdb.MetadataResult{Title: "Example", Year: 2026}}
				imdbClient := &stubIMDB{info: imdb.Info{
					IMDbID: "tt1234567",
					Title:  "Example",
					Type:   "movie",
					Year:   2026,
				}}
				svc := NewService(repo, WithTMDBClient(tmdbClient), WithIMDBClient(imdbClient),
					WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{}))
				meta := collectTestMediaInfoIDs(t, svc, format, "movie/tt1234567")
				meta.ArrSource, meta.ArrTMDBID, meta.ArrIMDBID = "radarr", 765432, 1234567
				wantTMDB, wantProvenance := 765432, api.IdentityProvenanceArr
				wantIMDB, wantIMDBProvenance := 1234567, api.IdentityProvenanceMediaInfo
				if mode == "refresh retained mediainfo" {
					meta.StoredDataFresh = true
					meta.ExternalFreshness = api.ExternalFreshnessRefresh
					meta.Identity = api.ExternalIdentity{
						SourcePath: meta.SourcePath,
						TMDBID:     1234567,
						Provenance: api.IdentityProvenanceSet{TMDB: api.IdentityProvenanceMediaInfo},
					}
					repo.ids = meta.Identity
				}
				if mode == "explicit pin" {
					pin := 246810
					imdbPin := 246811
					meta.ExternalIDOverrides.TMDBID = &pin
					meta.ExternalIDOverrides.IMDBID = &imdbPin
					meta.ExternalFreshness = api.ExternalFreshnessRefresh
					wantTMDB, wantProvenance = pin, api.IdentityProvenanceExplicit
					wantIMDB, wantIMDBProvenance = imdbPin, api.IdentityProvenanceExplicit
				}
				result, err := svc.resolveExternalIdentity(t.Context(), meta)
				if err != nil {
					t.Fatal(err)
				}
				if result.Identity.TMDBID != wantTMDB || result.Identity.Provenance.TMDB != wantProvenance {
					t.Fatalf("TMDB identity=%#v, want %d from %s", result.Identity, wantTMDB, wantProvenance)
				}
				if result.Identity.IMDBID != wantIMDB || result.Identity.Provenance.IMDB != wantIMDBProvenance {
					t.Fatalf("valid MediaInfo IMDb identity changed: %#v", result.Identity)
				}
				if tmdbClient.searchCalls != 0 || imdbClient.searchCalls != 0 {
					t.Fatalf("independent identities unexpectedly triggered title search")
				}
			})
		}
	}
}
