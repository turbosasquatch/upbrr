// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

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
	"github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/pkg/api"
)

type byteMemoClock struct{ nanos atomic.Int64 }

func (c *byteMemoClock) Now() time.Time { return time.Unix(0, c.nanos.Load()).UTC() }

type byteMemoLogger struct {
	api.NopLogger
	retained atomic.Int32
	hits     atomic.Int32
}

func (l *byteMemoLogger) Tracef(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if strings.Contains(message, "decision=retain_workflow_proof") {
		l.retained.Add(1)
	}
	if strings.Contains(message, "decision=reuse_workflow_proof") {
		l.hits.Add(1)
	}
}

type slowBytePlanBuilder struct {
	*uploadPlanBuilderFake
	clock    *byteMemoClock
	torrents *torrent.Service
	scope    torrent.ValidationScope
	subject  api.TorrentSubject
}

func (b *slowBytePlanBuilder) Build(
	ctx context.Context,
	projections api.TrackerReleaseProjectionSet,
	dupes api.DupeAssessment,
	privateDupes any,
	media api.MediaArtifactSet,
	privateMedia any,
	descriptions api.DescriptionSet,
	privateDescriptions any,
	options UploadPlanBuildOptions,
	now time.Time,
) (api.UploadPlan, RetainedUploadExecution, error) {
	scope := b.scope
	scope.WorkflowID = projections.WorkflowID
	scope.Release = projections.ReleaseRef
	local := api.WithActiveInputAuthority(ctx, api.ActiveInputAuthority{CoordinatorID: "synthetic-process", Fence: 1})
	local = torrent.WithValidationScope(local, scope)
	if _, err := b.torrents.Create(local, b.subject); err != nil {
		return api.UploadPlan{}, nil, fmt.Errorf("validate synthetic source bytes: %w", err)
	}
	if b.builds == 0 {
		b.clock.nanos.Add(int64(18 * time.Minute))
	}
	return b.uploadPlanBuilderFake.Build(ctx, projections, dupes, privateDupes, media, privateMedia, descriptions, privateDescriptions, options, now)
}

func TestSlowByteVerificationRefreshesRemoteAuthorityWithoutRehashing(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("platform uses full verification fallback")
	}
	source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	if err := os.WriteFile(source, []byte("synthetic payload for local validation"), 0o600); err != nil {
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
	clock := new(byteMemoClock)
	clock.nanos.Store(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	logger := new(byteMemoLogger)
	module, _, plans := newCompositeUploadTestModule(t)
	module.clock = clock
	base := module.trackerPreflight
	var preflights atomic.Int32
	module.trackerPreflight = trackerPreflightBuilderFunc(
		func(ctx context.Context, subject api.UploadSubject, catalog api.TrackerCatalogSnapshot, runtime api.TrackerRuntimeSnapshot, initial api.TrackerReleaseProjectionSet, now time.Time) (api.TrackerPreflightAssessment, []api.TrackerReleaseProjection, error) {
			preflights.Add(1)
			assessment, finalized, err := base.Build(ctx, subject, catalog, runtime, initial, now)
			if err != nil {
				return api.TrackerPreflightAssessment{}, nil, fmt.Errorf("refresh synthetic preflight: %w", err)
			}
			assessment.ExpiresAt = now.Add(15 * time.Minute)
			return assessment, finalized, nil
		},
	)
	reuseOnly := true
	module.uploadPlanBuilder = &slowBytePlanBuilder{
		uploadPlanBuilderFake: plans,
		clock:                 clock,
		torrents:              torrent.NewService(logger, t.TempDir()),
		scope: torrent.ValidationScope{
			SourceIdentity:    inspected.Identity.Digest,
			Manifest:          inspected.Manifest,
			ConfigFingerprint: "synthetic-config",
		},
		subject: api.TorrentSubject{
			SourcePath:        source,
			SourceSize:        inspected.Manifest.Size,
			FileList:          []string{source},
			ClientTorrentPath: candidate,
			TorrentOverrides:  api.TorrentOverrides{NoHash: &reuseOnly},
		},
	}
	request := compositeUploadTestRequest(false, api.ReleaseWorkflowUploadModeUpload, "slow-local-validation")
	request.Source.Path = source
	noSeed := true
	request.Client.NoSeed = &noSeed
	started, err := module.StartUpload(t.Context(), testOwnerID, request)
	if err != nil {
		t.Fatal(err)
	}
	first := waitCompositeUploadTestOperation(t, module, started)
	refreshed := approveCompositeUploadTrackers(t, module, first, []api.TrackerID{"ALPHA", "BETA"}, "approve-before-slow-validation")
	if preflights.Load() < 2 || refreshed.Preflight == nil || refreshed.Preflight.ID == first.Preflight.ID || refreshed.Dupes.ID == first.Dupes.ID {
		t.Fatalf("slow work did not refresh remote authority: checks=%d first=%#v refreshed=%#v", preflights.Load(), first.Preflight, refreshed.Preflight)
	}
	completed := approveCompositeUploadTrackers(t, module, refreshed, []api.TrackerID{"ALPHA", "BETA"}, "approve-fresh-authority")
	if completed.DryRun == nil || completed.DryRun.Status != api.StageStatusCompleted || completed.UploadResult == nil || plans.builds != 2 {
		t.Fatalf("refreshed synthetic workflow did not complete: builds=%d result=%#v", plans.builds, completed)
	}
	if logger.retained.Load() != 1 || logger.hits.Load() != 1 {
		t.Fatalf("local proof retain=%d hit=%d", logger.retained.Load(), logger.hits.Load())
	}
}
