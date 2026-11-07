// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	mkbrr "github.com/autobrr/mkbrr/torrent"
	"github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/pkg/api"
)

type forcedByteMemoLogger struct {
	api.NopLogger
	retained atomic.Int32
	hits     atomic.Int32
}

func (l *forcedByteMemoLogger) Tracef(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if strings.Contains(message, "decision=retain_workflow_proof") {
		l.retained.Add(1)
	}
	if strings.Contains(message, "decision=reuse_workflow_proof") {
		l.hits.Add(1)
	}
}

func TestPreparationOnlyForceRecheckInvalidatesWorkflowByteProof(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("platform uses full verification fallback")
	}
	source := writePreparedTestFile(t, "Example.Release.2026.mkv", "synthetic unchanged payload")
	candidate := filepath.Join(t.TempDir(), "candidate.torrent")
	if _, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:       source,
		OutputPath: candidate,
		IsPrivate:  true,
	}); err != nil {
		t.Fatal(err)
	}
	inspected, err := VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	collector := newClientEvidenceTestCollector(clientEvidenceTestSnapshot("synthetic-client-hash"))
	module := newTestModule(t, newMemoryStore(), collector)
	first, err := module.Prepare(t.Context(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	logger := new(forcedByteMemoLogger)
	service := torrent.NewService(logger, t.TempDir())
	// Keep workflow, coordinator, fence, config and bytes deliberately identical:
	// API preparation-only force must invalidate through its genuine generation.
	ctx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "synthetic-process", Fence: 1})
	warm := func(release api.PreparedRelease) {
		t.Helper()
		local := torrent.WithValidationScope(ctx, torrent.ValidationScope{
			WorkflowID:        "synthetic-workflow",
			Release:           api.ReleaseRef{SourcePath: source, Generation: release.Generation},
			SourceIdentity:    inspected.Identity.Digest,
			Manifest:          release.Source,
			ConfigFingerprint: "synthetic-config",
		})
		if _, err := service.Create(local, api.TorrentSubject{
			SourcePath:        source,
			SourceSize:        release.Source.Size,
			FileList:          []string{source},
			ClientTorrentPath: candidate,
		}); err != nil {
			t.Fatal(err)
		}
	}
	warm(first.Release)
	warm(first.Release)
	if logger.retained.Load() != 1 || logger.hits.Load() != 1 {
		t.Fatal("initial local proof missing")
	}
	force := true
	second, err := module.Prepare(t.Context(), api.PrepareInput{SourcePath: source, Controls: api.PreparationControls{ForceRecheck: &force}})
	if err != nil {
		t.Fatal(err)
	}
	if second.Release.Generation != first.Release.Generation+1 || collector.collectCount() != 2 {
		t.Fatal("preparation-only force failed to collect a new generation")
	}
	warm(second.Release)
	if logger.retained.Load() != 2 || logger.hits.Load() != 1 {
		t.Fatal("preparation-only force inherited prior byte proof")
	}
}
