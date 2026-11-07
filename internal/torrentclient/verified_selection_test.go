// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestReusableSelectionPrefersCompleteDataWithinPieceClass(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                                   string
		piece, best                            int64
		verified, bestVerified, prefer16, want bool
	}{
		{"verified-larger", 8 << 20, 4 << 20, true, false, false, true},
		{"do-not-replace-verified", 4 << 20, 8 << 20, false, true, false, false},
		{"same-evidence-retains-piece-order", 4 << 20, 8 << 20, true, true, false, true},
		{"configured-class-still-wins", 32 << 20, 4 << 20, true, false, true, false},
		{"configured-class-over-unusable-large", 4 << 20, 32 << 20, false, true, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldReplaceReusable(
				test.piece,
				test.best,
				test.verified,
				test.bestVerified,
				pieceConstraints{preferMax16: test.prefer16},
			); got != test.want {
				t.Fatalf("replace=%t want%t", got, test.want)
			}
		})
	}
}

func TestSelectedReusableCandidateRetainsItsOwnCompletionEvidence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	name := "Example.Release.2026.mkv"
	ambiguous, dataA := createTestTorrent(t, dir, name, 22)
	complete, dataB := createTestTorrent(t, dir, name, 23)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/export" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		switch r.FormValue("hash") {
		case ambiguous:
			_, _ = w.Write(dataA)
		case complete:
			_, _ = w.Write(dataB)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := NewServiceWithRegistry(
		config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "state.sqlite")}},
		api.NopLogger{},
		trackerPatternRegistry(t),
	)
	for _, matches := range [][]api.TorrentMatch{{{Hash: ambiguous}, {Hash: complete}}, {{Hash: complete}, {Hash: ambiguous}}} {
		selected, err := service.selectValidTorrent(
			t.Context(),
			api.ClientSubject{SourcePath: filepath.Join(dir, name), FileList: []string{filepath.Join(dir, name)}},
			matches,
			map[string]bool{complete: true},
			pieceConstraints{},
			nil,
			server.Client(),
			server.URL,
			true,
		)
		if err != nil {
			t.Fatal(err)
		}
		if selected.infoHash != complete || !selected.torrentDataVerified {
			t.Fatalf("wrong selected completion provenance: %#v", selected)
		}
	}
}
