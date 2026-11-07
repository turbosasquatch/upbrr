// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ant"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"
	"github.com/autobrr/upbrr/pkg/api"
)

func memoFixture(t *testing.T) (*Service, context.Context, api.TorrentSubject, ValidationScope, *atomic.Int32) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	candidate := filepath.Join(t.TempDir(), "candidate.torrent")
	createTestTorrent(t, source, candidate)
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stableFileStamp(source, info); !ok {
		t.Skip("reliable file identities unavailable on this platform")
	}
	scope := ValidationScope{
		WorkflowID:        "workflow-one",
		Release:           api.ReleaseRef{SourcePath: source, Generation: 1},
		SourceIdentity:    "checked-source",
		ConfigFingerprint: "config-one",
		Manifest: api.SourceManifest{
			SourcePath: source,
			Size:       info.Size(),
			Entries: []api.SourceManifestEntry{{
				Path:       source,
				Type:       api.SourceEntryTypeFile,
				Size:       info.Size(),
				ModifiedAt: info.ModTime(),
			}},
		},
	}
	ctx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "process-one", Fence: 1})
	ctx = WithValidationScope(ctx, scope)
	reuseOnly := true
	meta := api.TorrentSubject{
		SourcePath:        source,
		SourceSize:        info.Size(),
		FileList:          []string{source},
		ClientTorrentPath: candidate,
		TorrentOverrides:  api.TorrentOverrides{NoHash: &reuseOnly},
	}
	s := NewService(api.NopLogger{}, t.TempDir())
	calls := new(atomic.Int32)
	s.verifyData = func(path string, meta api.TorrentSubject) error {
		calls.Add(1)
		return verifyCandidateTorrentData(path, meta)
	}
	return s, ctx, meta, scope, calls
}

func memoCreate(ctx context.Context, t *testing.T, s *Service, meta api.TorrentSubject) {
	t.Helper()
	if _, err := s.Create(ctx, meta); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowByteMemoReusesOnlyStableSuccess(t *testing.T) {
	t.Parallel()
	s, ctx, meta, _, calls := memoFixture(t)
	memoCreate(ctx, t, s, meta)
	memoCreate(ctx, t, s, meta)
	if calls.Load() != 1 {
		t.Fatalf("byte passes=%d want1", calls.Load())
	}
	// Announce personalization and a different metainfo filename do not change
	// the proven payload pieces or layout. Its current layout is still checked.
	m, err := metainfo.LoadFromFile(meta.ClientTorrentPath)
	if err != nil {
		t.Fatal(err)
	}
	m.Announce = "https://example.invalid/announce"
	path := filepath.Join(t.TempDir(), "personalized.torrent")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Write(f); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	meta.ClientTorrentPath = path
	memoCreate(ctx, t, s, meta)
	if calls.Load() != 1 {
		t.Fatal("personalization caused another byte pass")
	}
}

func TestWorkflowByteMemoNamespaceMisses(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"workflow", "fence", "process", "generation", "config", "identity", "source", "unscoped", "new-service"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, ctx, meta, scope, calls := memoFixture(t)
			memoCreate(ctx, t, s, meta)
			switch name {
			case "workflow":
				scope.WorkflowID = "workflow-two"
			case "fence":
				ctx = api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: "process-one", Fence: 2})
			case "process":
				ctx = api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: "process-two", Fence: 1})
			case "generation":
				scope.Release.Generation++
			case "config":
				scope.ConfigFingerprint = "config-two"
			case "identity":
				scope.SourceIdentity = "new-source-identity"
			case "source":
				nextPath := filepath.Join(t.TempDir(), filepath.Base(meta.SourcePath))
				data, err := os.ReadFile(meta.SourcePath)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(nextPath, data, 0o600); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(nextPath)
				if err != nil {
					t.Fatal(err)
				}
				meta.SourcePath = nextPath
				meta.FileList = []string{nextPath}
				scope.Release.SourcePath = nextPath
				scope.Manifest.SourcePath = nextPath
				scope.Manifest.Entries = []api.SourceManifestEntry{{
					Path:       nextPath,
					Type:       api.SourceEntryTypeFile,
					Size:       info.Size(),
					ModifiedAt: info.ModTime(),
				}}
			case "unscoped":
				ctx = t.Context()
			case "new-service":
				next := NewService(api.NopLogger{}, t.TempDir())
				next.verifyData = s.verifyData
				s = next
			}
			if name != "unscoped" {
				ctx = WithValidationScope(ctx, scope)
			}
			memoCreate(ctx, t, s, meta)
			if calls.Load() != 2 {
				t.Fatalf("namespace %s reused old proof", name)
			}
		})
	}
}

func TestWorkflowByteMemoRestatsReplacementAndRestoredMtime(t *testing.T) {
	t.Parallel()
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "restored-mtime", true: "replaced-inode"}[replace], func(t *testing.T) {
			t.Parallel()
			s, ctx, meta, scope, calls := memoFixture(t)
			memoCreate(ctx, t, s, meta)
			data, err := os.ReadFile(meta.SourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if replace {
				if err = os.Remove(meta.SourcePath); err != nil {
					t.Fatal(err)
				}
			}
			if err = os.WriteFile(meta.SourcePath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			modified := scope.Manifest.Entries[0].ModifiedAt
			if err = os.Chtimes(meta.SourcePath, modified, modified); err != nil {
				t.Fatal(err)
			}
			memoCreate(ctx, t, s, meta)
			if calls.Load() != 2 {
				t.Fatal("changed ctime/inode reused old proof")
			}
		})
	}
}

func TestWorkflowByteMemoDoesNotCacheFailureOrCancellation(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"failure", "partial", "cancel", "mutation-during-verify"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			s, ctx, meta, scope, calls := memoFixture(t)
			verify := s.verifyData
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			s.verifyData = func(_ string, _ api.TorrentSubject) error {
				calls.Add(1)
				switch kind {
				case "failure":
					return errors.New("synthetic verifier failure")
				case "partial":
					return errors.New("synthetic missing piece")
				case "cancel":
					cancel()
					return nil
				default:
					data, err := os.ReadFile(meta.SourcePath)
					if err != nil {
						return fmt.Errorf("read synthetic source: %w", err)
					}
					if err := os.WriteFile(meta.SourcePath, data, 0o600); err != nil {
						return fmt.Errorf("rewrite synthetic source: %w", err)
					}
					return nil
				}
			}
			_, _ = s.Create(ctx, meta)
			s.memo.mu.Lock()
			retained := len(s.memo.successes)
			s.memo.mu.Unlock()
			if retained != 0 {
				t.Fatalf("%s retained a positive proof", kind)
			}
			s.verifyData = verify
			// An uncancelled retry is a unit call only, not native upload retry behavior.
			if kind != "mutation-during-verify" {
				retryCtx := WithValidationScope(api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "process-one", Fence: 1}), scope)
				memoCreate(retryCtx, t, s, meta)
			}
			if calls.Load() < 2 && kind != "mutation-during-verify" {
				t.Fatal("unsuccessful attempt bypassed byte verification")
			}
		})
	}
}

func TestWorkflowByteMemoCorruptedSourceCannotReuseProof(t *testing.T) {
	t.Parallel()
	s, ctx, meta, _, calls := memoFixture(t)
	memoCreate(ctx, t, s, meta)
	if err := os.WriteFile(meta.SourcePath, bytes.Repeat([]byte("x"), 17), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, meta); err == nil {
		t.Fatal("corrupted source accepted cached proof")
	}
	if calls.Load() > 2 {
		t.Fatal("unexpected extra candidate verification")
	}
}

func TestWorkflowByteMemoDoesNotIncludeAccessTime(t *testing.T) {
	t.Parallel()
	s, ctx, meta, scope, calls := memoFixture(t)
	memoCreate(ctx, t, s, meta)
	// Read-induced atime changes cannot invalidate a successful proof. Explicit
	// Chtimes would also change ctime, so use ordinary reads rather than that API.
	f, err := os.Open(meta.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	_, _ = f.Read(one[:])
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	memoCreate(WithValidationScope(ctx, scope), t, s, meta)
	if calls.Load() != 1 {
		t.Fatal("ordinary source read invalidated byte proof")
	}
}

func TestWorkflowByteMemoElapsedTimeDoesNotAuthorizeStalePolicy(t *testing.T) {
	t.Parallel()
	s, ctx, meta, _, calls := memoFixture(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	preflightExpiry := now.Add(15 * time.Minute)
	verify := s.verifyData
	s.verifyData = func(path string, m api.TorrentSubject) error {
		err := verify(path, m)
		now = now.Add(18 * time.Minute)
		return err
	}
	memoCreate(ctx, t, s, meta)
	if !now.After(preflightExpiry) {
		t.Fatal("slow verification did not expire simulated remote authority")
	}
	// The caller must replace remote policy/approval separately. Time passage
	// does not remove exact local byte identity, so that second review is cheap.
	memoCreate(ctx, t, s, meta)
	if calls.Load() != 1 {
		t.Fatal("fresh review reread unchanged bytes")
	}
	// Existing tracker policy remains checked before the byte memo. Oversized
	// ANT metainfo must not become accepted by a previous unrelated success.
	s.registry = trackers.NewRegistry()
	if err := s.registry.Register(ant.New()); err != nil {
		t.Fatal(err)
	}
	metadata, err := metainfo.LoadFromFile(meta.ClientTorrentPath)
	if err != nil {
		t.Fatal(err)
	}
	metadata.Comment = strings.Repeat("x", int(antMaxTorrentBytesForTest)+1)
	f, err := os.OpenFile(meta.ClientTorrentPath, os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = metadata.Write(f); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	meta.Trackers = []string{"ANT"}
	if _, err := s.Create(ctx, meta); err == nil {
		t.Fatal("memo waived refreshed tracker policy")
	}
}

func TestWorkflowByteMemoExplicitRehashClearsOldSuccess(t *testing.T) {
	t.Parallel()
	s, ctx, meta, _, calls := memoFixture(t)
	memoCreate(ctx, t, s, meta)
	force := true
	meta.TorrentOverrides.Rehash = &force
	memoCreate(ctx, t, s, meta)
	meta.TorrentOverrides.Rehash = nil
	memoCreate(ctx, t, s, meta)
	if calls.Load() != 2 {
		t.Fatal("explicit rehash left an old verification memo active")
	}
}

func TestWorkflowByteMemoPieceReplacementDoesNotInheritSuccess(t *testing.T) {
	t.Parallel()
	s, ctx, meta, _, calls := memoFixture(t)
	memoCreate(ctx, t, s, meta)
	metadata, err := metainfo.LoadFromFile(meta.ClientTorrentPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := metadata.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	info.Pieces[0] ^= 1
	encoded, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	metadata.InfoBytes = encoded
	f, err := os.OpenFile(meta.ClientTorrentPath, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = metadata.Write(f); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, meta); err == nil {
		t.Fatal("replaced metainfo inherited prior piece proof")
	}
	if calls.Load() != 2 {
		t.Fatalf("changed pieces did not require fresh validation: %d", calls.Load())
	}
}

func TestWorkflowByteMemoBindsEntireMultiFileManifest(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "Example.Season.2026")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	files := []string{filepath.Join(source, "Example.S01E01.mkv"), filepath.Join(source, "Example.S01E02.mkv")}
	for _, path := range files {
		if err := os.WriteFile(path, []byte("synthetic episode"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	candidate := filepath.Join(t.TempDir(), "candidate.torrent")
	if _, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:       source,
		OutputPath: candidate,
		IsPrivate:  true,
	}); err != nil {
		t.Fatal(err)
	}
	manifest := api.SourceManifest{SourcePath: source}
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect synthetic manifest member: %w", err)
		}
		if _, ok := stableFileStamp(path, info); !ok {
			t.Skip("reliable stat identity unavailable")
		}
		kind := api.SourceEntryTypeFile
		if entry.IsDir() {
			kind = api.SourceEntryTypeDirectory
		} else {
			manifest.Size += info.Size()
		}
		manifest.Entries = append(manifest.Entries, api.SourceManifestEntry{
			Path:       path,
			Type:       kind,
			Size:       info.Size(),
			ModifiedAt: info.ModTime(),
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	scope := ValidationScope{
		WorkflowID:        "workflow-pack",
		Release:           api.ReleaseRef{SourcePath: source, Generation: 1},
		SourceIdentity:    "pack-source",
		Manifest:          manifest,
		ConfigFingerprint: "config",
	}
	ctx := WithValidationScope(api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "process", Fence: 1}), scope)
	only := true
	meta := api.TorrentSubject{
		SourcePath:        source,
		SourceSize:        manifest.Size,
		FileList:          files,
		ClientTorrentPath: candidate,
		TorrentOverrides:  api.TorrentOverrides{NoHash: &only},
	}
	s := NewService(api.NopLogger{}, t.TempDir())
	calls := 0
	s.verifyData = func(path string, m api.TorrentSubject) error { calls++; return verifyCandidateTorrentData(path, m) }
	memoCreate(ctx, t, s, meta)
	memoCreate(ctx, t, s, meta)
	if calls != 1 {
		t.Fatal("stable pack not reused")
	}
	// An unrecorded member changes the directory stamp, even when the selected
	// torrent's two original members are still unchanged and locally valid.
	if err := os.WriteFile(filepath.Join(source, "new-member.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	memoCreate(ctx, t, s, meta)
	if calls != 2 {
		t.Fatal("changed full inventory reused a byte proof")
	}
}
