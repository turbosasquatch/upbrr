// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mkbrr "github.com/autobrr/mkbrr/torrent"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/pkg/api"
)

type coreByteMemoLogger struct {
	api.NopLogger
	retained atomic.Int32
	hits     atomic.Int32
}

func (l *coreByteMemoLogger) Tracef(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if strings.Contains(message, "decision=retain_workflow_proof") {
		l.retained.Add(1)
	}
	if strings.Contains(message, "decision=reuse_workflow_proof") {
		l.hits.Add(1)
	}
}

func TestWorkflowUploadPlanBindsByteMemoAndForceRecheckDisablesIt(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("platform uses full verification fallback")
	}
	source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	if err := os.WriteFile(source, []byte("synthetic verified payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(t.TempDir(), "candidate.torrent")
	if _, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:       source,
		OutputPath: candidate,
		IsPrivate:  true,
	}); err != nil {
		t.Fatal(err)
	}
	inspected, err := preparedrelease.VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	logger := new(coreByteMemoLogger)
	builder := workflowUploadPlanBuilder{
		resolver: workflowUploadResolverFixed{subject: api.UploadSubject{
			SourcePath:        source,
			SourceSize:        inspected.Manifest.Size,
			SourceManifest:    inspected.Manifest,
			SourceIdentity:    inspected.Identity,
			FileList:          []string{source},
			ClientTorrentPath: candidate,
		}},
		trackers: &workflowRetainedUploadServiceFake{},
		torrents: torrent.NewService(logger, t.TempDir()),
	}
	ctx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "synthetic-process", Fence: 1})
	projections := api.TrackerReleaseProjectionSet{
		ID:         "projection-before-refresh",
		WorkflowID: "synthetic-workflow",
		ReleaseRef: api.ReleaseRef{SourcePath: source, Generation: 1},
		Projections: []api.TrackerReleaseProjection{{
			TrackerID:   "ALPHA",
			Readiness:   api.ReadinessStatusReady,
			UploadReady: true,
		}},
	}
	build := func(ctx context.Context, instructions api.DescriptionInstructions) {
		t.Helper()
		_, execution, err := builder.Build(ctx, projections, api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
			TrackerID: "ALPHA",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		}}}, workflowDupePrivateEvidence{}, api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, api.DescriptionSet{}, instructions, releaseworkflow.UploadPlanBuildOptions{NoSeed: true}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err = execution.Release(); err != nil {
			t.Fatal(err)
		}
	}
	build(ctx, api.DescriptionInstructions{})
	projections.ID = "projection-after-refresh"
	projections.Revision++
	build(ctx, api.DescriptionInstructions{})
	if logger.retained.Load() != 1 || logger.hits.Load() != 1 {
		t.Fatalf("core did not preserve exact local proof across fresh projection: retained=%d hits=%d", logger.retained.Load(), logger.hits.Load())
	}
	force := true
	build(ctx, api.DescriptionInstructions{Client: api.ClientOverrides{ForceRecheck: &force}})
	if logger.retained.Load() != 1 || logger.hits.Load() != 1 {
		t.Fatal("force-recheck reused or retained a byte proof")
	}
	build(ctx, api.DescriptionInstructions{})
	if logger.retained.Load() != 2 || logger.hits.Load() != 1 {
		t.Fatal("force-recheck failed to invalidate previous local proof")
	}
}
