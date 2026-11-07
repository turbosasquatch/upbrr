// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDiscoveryPieceConstraintsBinaryBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		piece         int64
		pieces, size  int
		wrong, reject bool
	}{
		{"below-4MiB-before-count-boundary", 4*1024*1024 - 1, 4999, 100, false, false},
		{"below-4MiB-at-count-boundary", 4*1024*1024 - 1, 5000, 100, false, true},
		{"exact-4MiB", 4 * 1024 * 1024, 5000, 100, false, false},
		{"below-8MiB-before-count-boundary", 8*1024*1024 - 1, 7999, 100, false, false},
		{"below-8MiB-at-count-boundary", 8*1024*1024 - 1, 8000, 100, false, true},
		{"exact-8MiB", 8 * 1024 * 1024, 8000, 100, false, false},
		{"exact-8MiB-11222-pieces", 8 * 1024 * 1024, 11222, 100, false, false},
		{"below-piece-count-cap", 16 * 1024 * 1024, 11999, 100, false, false},
		{"at-piece-count-cap", 16 * 1024 * 1024, 12000, 100, false, true},
		{"below-piece-floor", 32*1024 - 1, 1, 100, false, true},
		{"at-piece-floor", 32 * 1024, 1, 100, false, false},
		{"at-metainfo-cap", 16 * 1024 * 1024, 1, 250 * 1024, false, false},
		{"over-metainfo-cap", 16 * 1024 * 1024, 1, 250*1024 + 1, false, true},
		{"wrong-layout", 16 * 1024 * 1024, 1, 100, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := invalidPieceConstraints(test.piece, test.pieces, int64(test.size), test.wrong); got != test.reject {
				t.Fatalf("rejected=%t want %t", got, test.reject)
			}
		})
	}
}

func TestVerifiedEightMiB11222PieceCandidateSurvivesDiscoveryAndWins(t *testing.T) {
	t.Parallel()
	const name = "Example.Release.2026.mkv"
	const total = int64(11221)*(8*1024*1024) + 1
	source := filepath.Join(t.TempDir(), name)
	subject := api.ClientSubject{SourcePath: source, FileList: []string{source}}
	data := make(map[string][]byte)
	hashes := make([]string, 0, 2)
	for _, piece := range []int64{8 * 1024 * 1024, 16 * 1024 * 1024} {
		count := (total + piece - 1) / piece
		info := metainfo.Info{
			Name:        name,
			Length:      total,
			PieceLength: piece,
			Pieces:      make([]byte, count*metainfo.HashSize),
		}
		raw, err := bencode.Marshal(info)
		if err != nil {
			t.Fatal(err)
		}
		metadata := metainfo.MetaInfo{InfoBytes: raw}
		var buffer bytes.Buffer
		if err := metadata.Write(&buffer); err != nil {
			t.Fatal(err)
		}
		hash := metadata.HashInfoBytes().String()
		data[hash] = buffer.Bytes()
		hashes = append(hashes, hash)
		result := validateTorrentData(subject, hash, buffer.Bytes())
		if !result.reusable {
			t.Fatalf("piece=%d unexpectedly rejected: %s", piece, result.reason)
		}
	}
	verified, unverified := hashes[0], hashes[1]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/export" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		raw, ok := data[r.FormValue("hash")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	service := NewServiceWithRegistry(
		config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "state.sqlite")}},
		api.NopLogger{}, trackerPatternRegistry(t),
	)
	for _, matches := range [][]api.TorrentMatch{{{Hash: unverified}, {Hash: verified}}, {{Hash: verified}, {Hash: unverified}}} {
		selected, err := service.selectValidTorrent(t.Context(), subject, matches, map[string]bool{verified: true}, pieceConstraints{preferMax16: true}, nil, server.Client(), server.URL, true)
		if err != nil {
			t.Fatal(err)
		}
		if selected.infoHash != verified || !selected.torrentDataVerified {
			t.Fatalf("verified 8MiB candidate did not win: %#v", selected)
		}
	}
}
