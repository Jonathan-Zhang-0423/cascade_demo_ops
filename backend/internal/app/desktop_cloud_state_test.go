package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/store"
)

func TestDesktopCloudRunPersistsAcrossServiceRestartWithoutLocalPaths(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	stateStore := store.NewFileStateStore(filepath.Join(root, "desktop_state"))
	state := &orchestrator.CascadeState{ProjectID: "project_restart", Status: orchestrator.FlowStatusAwaitingHuman}
	if err := stateStore.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(config.AppRuntimeConfig{DataRoot: root, ArtifactRoot: filepath.Join(root, "artifacts")}, stateStore)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.persistCloudInit(ctx, state.ProjectID, "org_test", model.ExecutionPackageInitResponse{UploadID: "upload_restart"}); err != nil {
		t.Fatal(err)
	}
	if err := service.persistCloudUpload(ctx, state.ProjectID, "org_test", "upload_restart", model.ExecutionPackageUploadResponse{
		ExchangePackageID: "xpkg_restart", CloudJobID: "job_restart", Status: model.ExchangePackageStatusRunning,
	}); err != nil {
		t.Fatal(err)
	}
	status := model.ExecutionPackageStatusResponse{
		ExchangePackageID: "xpkg_restart", CloudJobID: "job_restart", Status: model.ExchangePackageStatusCompleted,
		Stage: "completed", Message: "render complete", ProgressPercent: 100, ResultPackageID: "result_restart",
	}
	if err := service.persistCloudStatus(ctx, state.ProjectID, "org_test", status); err != nil {
		t.Fatal(err)
	}
	if err := service.persistCloudEventCursor(ctx, state.ProjectID, "xpkg_restart", "xpkg_restart:17"); err != nil {
		t.Fatal(err)
	}
	downloadRoot := filepath.Join(root, "artifacts", "desktop", "downloads", state.ProjectID, "result_restart")
	if err := service.persistCloudDownload(ctx, state.ProjectID, "result_restart", CloudDeliverableDownloadResult{
		ArtifactID: "video_restart", Kind: "demo_video", MimeType: "video/mp4", LocalPath: filepath.Join(downloadRoot, "final.mp4"),
		SHA256: strings.Repeat("a", 64), SizeBytes: 42, ChecksumVerified: true,
	}); err != nil {
		t.Fatal(err)
	}
	ackedAt := time.Now().UTC()
	if err := service.persistCloudAck(ctx, state.ProjectID, model.ResultPackageAckResponse{
		ResultPackageID: "result_restart", VerifiedChecksums: true, AckedAt: ackedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.persistCloudReview(ctx, state.ProjectID, model.ResultReviewRecord{
		ReviewID: "review_restart", ResultPackageID: "result_restart", Decision: model.ResultReviewApproved, ReviewedAt: ackedAt,
	}); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewService(config.AppRuntimeConfig{DataRoot: root, ArtifactRoot: filepath.Join(root, "artifacts")}, store.NewFileStateStore(filepath.Join(root, "desktop_state")))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := restarted.LoadProject(ctx, state.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	run := loaded.DesktopCloudRun
	if run == nil || run.ExchangePackageID != "xpkg_restart" || run.ResultPackageID != "result_restart" || !run.ResultDownloaded {
		t.Fatalf("cloud run was not restored: %#v", run)
	}
	if run.LastEventID != "xpkg_restart:17" || run.ResultReview == nil || run.ResultReview.Decision != string(model.ResultReviewApproved) {
		t.Fatalf("cursor or review was not restored: %#v", run)
	}
	if len(run.DownloadedAssets) != 1 || run.DownloadedAssets[0].FileName != "final.mp4" {
		t.Fatalf("safe downloaded asset was not restored: %#v", run.DownloadedAssets)
	}
	payload := mustJSONMarshal(t, loaded)
	if strings.Contains(string(payload), downloadRoot) || strings.Contains(string(payload), `\\`) {
		t.Fatalf("persisted cloud state exposed an absolute local path: %s", payload)
	}
}

func mustJSONMarshal(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := model.CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
