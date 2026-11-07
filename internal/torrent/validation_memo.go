// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/autobrr/go-torrent/metainfo"

	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/pkg/api"
)

// ValidationScope is server-owned local byte-verification context, never upload
// authority. Policy, authentication and workflow freshness are checked separately.
// A runtime's Service holds at most one scope; no proof is persisted or shared
// with another process/configuration/workflow/coordinator fence.
type ValidationScope struct {
	WorkflowID        api.WorkflowID
	Release           api.ReleaseRef
	SourceIdentity    string
	Manifest          api.SourceManifest
	ConfigFingerprint api.WorkflowFingerprint
}

type validationScopeKey struct{}

// WithValidationScope attaches an exact checked source to one server operation.
// The caller must already hold the workflow's current active-input authority.
func WithValidationScope(ctx context.Context, scope ValidationScope) context.Context {
	scope.Manifest.Entries = slices.Clone(scope.Manifest.Entries)
	return context.WithValue(ctx, validationScopeKey{}, scope)
}

type byteValidationMemo struct {
	mu        sync.Mutex
	scope     string
	successes map[string]struct{}
}

type memoFileStamp struct {
	Path       string
	Device     uint64
	Inode      uint64
	Size       int64
	Mode       uint32
	ModifiedNS int64
	ChangedNS  int64
}

func memoDigest(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func validationScope(ctx context.Context, meta api.TorrentSubject) (ValidationScope, string) {
	scope, ok := ctx.Value(validationScopeKey{}).(ValidationScope)
	authority, admitted := api.ActiveInputAuthorityFromContext(ctx)
	if !ok || !admitted || authority.CoordinatorID == "" || authority.Fence == 0 || scope.WorkflowID == "" ||
		scope.Release.Generation == 0 || scope.SourceIdentity == "" || scope.ConfigFingerprint == "" ||
		!pathutil.SamePath(scope.Release.SourcePath, meta.SourcePath) || !pathutil.SamePath(scope.Manifest.SourcePath, meta.SourcePath) ||
		len(scope.Manifest.Entries) == 0 {
		return ValidationScope{}, ""
	}
	return scope, memoDigest(struct {
		Workflow   api.WorkflowID
		Release    api.ReleaseRef
		Identity   string
		Config     api.WorkflowFingerprint
		Authority  api.ActiveInputAuthority
		Manifest   api.SourceManifest
		Wanted     []string
		DiscType   string
		SourceSize int64
	}{scope.WorkflowID, scope.Release, scope.SourceIdentity, scope.ConfigFingerprint, authority, scope.Manifest,
		meta.FileList, meta.DiscType, meta.SourceSize})
}

// memoKey re-stats the entire checked manifest (including directories), and
// hashes only the torrent's byte-layout/pieces, independent of tracker source or
// announce personalization. Unsupported identities disable memoization.
func memoKey(path string, scope ValidationScope) string {
	root, err := filepath.Abs(scope.Manifest.SourcePath)
	if err != nil {
		return ""
	}
	stamps := make([]memoFileStamp, 0, len(scope.Manifest.Entries))
	seen := make(map[string]struct{}, len(scope.Manifest.Entries))
	rootSeen := false
	for _, entry := range scope.Manifest.Entries {
		if !filepath.IsAbs(entry.Path) || !pathutil.IsWithinRoot(root, entry.Path) {
			return ""
		}
		if _, duplicate := seen[entry.Path]; duplicate {
			return ""
		}
		seen[entry.Path] = struct{}{}
		if pathutil.SamePath(root, entry.Path) {
			rootSeen = true
		}
		info, statErr := os.Lstat(entry.Path)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || info.Size() != entry.Size || !info.ModTime().Equal(entry.ModifiedAt) {
			return ""
		}
		switch entry.Type {
		case api.SourceEntryTypeDirectory:
			if !info.IsDir() {
				return ""
			}
		case api.SourceEntryTypeFile, api.SourceEntryTypePlaylist:
			if !info.Mode().IsRegular() {
				return ""
			}
		case api.SourceEntryTypeUnknown:
			return ""
		default:
			return ""
		}
		stamp, reliable := stableFileStamp(entry.Path, info)
		if !reliable {
			return ""
		}
		stamps = append(stamps, stamp)
	}
	if !rootSeen {
		return ""
	}
	slices.SortFunc(stamps, func(a, b memoFileStamp) int {
		if a.Path < b.Path {
			return -1
		}
		if a.Path > b.Path {
			return 1
		}
		return 0
	})
	metadata, err := metainfo.LoadFromFile(path)
	if err != nil {
		return ""
	}
	info, err := metadata.UnmarshalInfo()
	if err != nil || !info.HasV1() || len(info.Pieces) == 0 {
		return ""
	}
	type member struct {
		Path string
		Size int64
	}
	layout := make([]member, 0)
	for _, file := range torrentContentPaths(info) {
		layout = append(layout, member{Path: file.path, Size: file.length})
	}
	return memoDigest(struct {
		Files       []memoFileStamp
		Name        string
		Layout      []member
		PieceLength int64
		Pieces      []byte
	}{
		Files:       stamps,
		Name:        info.BestName(),
		Layout:      layout,
		PieceLength: info.PieceLength,
		Pieces:      info.Pieces,
	})
}

func (m *byteValidationMemo) activate(scope string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if scope == "" || m.scope != scope {
		m.scope = scope
		m.successes = make(map[string]struct{})
	}
}

func (s *Service) verifyReusableData(ctx context.Context, path string, meta api.TorrentSubject) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("torrent: verification canceled: %w", err)
	}
	scope, scopeKey := validationScope(ctx, meta)
	key := ""
	if scopeKey != "" {
		key = memoKey(path, scope)
	}
	s.memo.mu.Lock()
	_, hit := s.memo.successes[key]
	hit = hit && key != "" && s.memo.scope == scopeKey
	s.memo.mu.Unlock()
	if hit && memoKey(path, scope) == key && ctx.Err() == nil {
		s.logger.Tracef("torrent: byte validation state=completed decision=reuse_workflow_proof")
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("torrent: verification canceled: %w", err)
	}
	verify := s.verifyData
	if verify == nil {
		verify = verifyCandidateTorrentData
	}
	if err := verify(path, meta); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("torrent: verification canceled: %w", err)
	}
	stable := key != "" && memoKey(path, scope) == key
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("torrent: verification canceled: %w", err)
	}
	if stable {
		s.memo.mu.Lock()
		retained := s.memo.scope == scopeKey && ctx.Err() == nil
		if retained {
			if len(s.memo.successes) >= 16 {
				clear(s.memo.successes)
			}
			s.memo.successes[key] = struct{}{}
		}
		s.memo.mu.Unlock()
		if retained {
			s.logger.Tracef("torrent: byte validation state=completed decision=retain_workflow_proof")
		}
	}
	return nil
}
