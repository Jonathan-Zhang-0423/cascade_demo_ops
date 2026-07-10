package executor

import (
	"context"
	"errors"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestRunClientExecutionRecordingAndRenderCallsExecutorInProtocolOrder(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	now := time.Date(2026, 7, 9, 20, 0, 0, 0, time.UTC)
	service := &fakeRecordingRenderService{
		recordResult: RecordResult{
			RecordingPath:   "artifacts/recording/job_1/recording.webm",
			ScreenshotPaths: []string{"artifacts/recording/job_1/step-001.png"},
			TracePath:       "artifacts/recording/job_1/trace.zip",
			WorkerID:        "worker_1",
			RuntimeVersions: map[string]string{"runner": "test"},
			StartedAt:       now.Add(-time.Second),
			CompletedAt:     now,
		},
		renderResult: RenderResult{
			VideoPath:                "artifacts/render/job_1/final.mp4",
			StepByStepDocsPath:       "artifacts/render/job_1/steps.md",
			AssetTimelineCatalogPath: "artifacts/render/job_1/asset_timeline_catalog.json",
		},
	}

	result, err := RunClientExecutionRecordingAndRender(t.Context(), service, RecordingRenderPipelineRequest{
		SourcePackage:      &pkg,
		CloudJobID:         "job_1",
		RecordingOutputDir: "artifacts/recording/job_1",
		RenderOutputDir:    "artifacts/render/job_1",
		ResultCreatedAt:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(service.calls) != 2 || service.calls[0] != "record" || service.calls[1] != "render" {
		t.Fatalf("expected record then render calls, got %+v", service.calls)
	}
	if service.recordRequest.SourcePackageID != pkg.PackageID || service.recordRequest.ExecutableScriptBundle == nil {
		t.Fatalf("record request did not use protocol package: %+v", service.recordRequest)
	}
	if service.recordRequest.RecordingMode != RecordingModePlaywright {
		t.Fatalf("pipeline should default to real Playwright recording, got %q", service.recordRequest.RecordingMode)
	}
	if result.RecordingResultPackage.SourcePackageID != pkg.PackageID || result.RecordingResultPackage.CloudJobID != "job_1" {
		t.Fatalf("recording result package identity mismatch: %+v", result.RecordingResultPackage)
	}
	if got := countArtifactsByKind(result.RecordingResultPackage.GeneratedAssets, "demo_video"); got != 1 {
		t.Fatalf("expected final demo video artifact in result package, got %d: %+v", got, result.RecordingResultPackage.GeneratedAssets)
	}
	demoVideo := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "demo_video")
	if demoVideo == nil || demoVideo.URI != "artifacts/render/job_1/final.mp4" || demoVideo.Metadata["asset_role"] != "final_demo" || demoVideo.Metadata["include_in_demo"] != true {
		t.Fatalf("unexpected demo video artifact metadata: %+v", demoVideo)
	}
	if service.renderRequest.RecordingResultPackage == nil || service.renderRequest.RecordingResultPackage.SourcePackageID != pkg.PackageID {
		t.Fatalf("render request must consume recording result package: %+v", service.renderRequest)
	}
	if service.renderRequest.OutputDir != "artifacts/render/job_1" || result.RenderResult.VideoPath == "" {
		t.Fatalf("unexpected render output: request=%+v result=%+v", service.renderRequest, result.RenderResult)
	}
}

func TestRunClientExecutionRecordingAndRenderAllowsRecordingModeOverride(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	now := time.Date(2026, 7, 9, 20, 15, 0, 0, time.UTC)
	service := &fakeRecordingRenderService{
		recordResult: RecordResult{
			TracePath:       "artifacts/recording/job_1/script_execution_trace.json",
			GeneratedAssets: []model.ArtifactRef{{ID: "artifact_trace", Kind: "execution_trace", URI: "artifacts/recording/job_1/script_execution_trace.json", MimeType: "application/json", CreatedAt: now}},
			StepResults:     []model.StepResult{{NodeID: "node_start", Status: "passed"}},
			StartedAt:       now.Add(-time.Second),
			CompletedAt:     now,
		},
		renderResult: RenderResult{
			VideoPath:          "artifacts/render/job_1/final.mp4",
			StepByStepDocsPath: "artifacts/render/job_1/steps.md",
		},
	}

	_, err := RunClientExecutionRecordingAndRender(t.Context(), service, RecordingRenderPipelineRequest{
		SourcePackage:      &pkg,
		CloudJobID:         "job_1",
		RecordingOutputDir: "artifacts/recording/job_1",
		RenderOutputDir:    "artifacts/render/job_1",
		RecordingMode:      RecordingModeDryRun,
		ResultCreatedAt:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.recordRequest.RecordingMode != RecordingModeDryRun {
		t.Fatalf("expected dry-run override to reach worker request, got %q", service.recordRequest.RecordingMode)
	}
}

func TestRunClientExecutionRecordingAndRenderStopsBeforeRenderOnRecordFailure(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	service := &fakeRecordingRenderService{recordErr: errors.New("record failed")}

	_, err := RunClientExecutionRecordingAndRender(t.Context(), service, RecordingRenderPipelineRequest{
		SourcePackage:      &pkg,
		CloudJobID:         "job_1",
		RecordingOutputDir: "artifacts/recording/job_1",
		RenderOutputDir:    "artifacts/render/job_1",
	})
	if err == nil {
		t.Fatal("expected record failure")
	}
	if len(service.calls) != 1 || service.calls[0] != "record" {
		t.Fatalf("expected only record call, got %+v", service.calls)
	}
}

func TestRunClientExecutionRecordingAndRenderRejectsMissingRenderOutput(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	service := &fakeRecordingRenderService{}

	if _, err := RunClientExecutionRecordingAndRender(t.Context(), service, RecordingRenderPipelineRequest{
		SourcePackage:      &pkg,
		CloudJobID:         "job_1",
		RecordingOutputDir: "artifacts/recording/job_1",
	}); err == nil {
		t.Fatal("expected render output dir validation error")
	}
}

type fakeRecordingRenderService struct {
	calls         []string
	recordRequest RecordRequest
	renderRequest RenderRequest
	recordResult  RecordResult
	renderResult  RenderResult
	recordErr     error
	renderErr     error
}

func (s *fakeRecordingRenderService) Record(ctx context.Context, request RecordRequest) (RecordResult, error) {
	s.calls = append(s.calls, "record")
	s.recordRequest = request
	if s.recordErr != nil {
		return RecordResult{}, s.recordErr
	}
	return s.recordResult, nil
}

func (s *fakeRecordingRenderService) Render(ctx context.Context, request RenderRequest) (RenderResult, error) {
	s.calls = append(s.calls, "render")
	s.renderRequest = request
	if s.renderErr != nil {
		return RenderResult{}, s.renderErr
	}
	return s.renderResult, nil
}

func findPipelineArtifact(artifacts []model.ArtifactRef, kind string) *model.ArtifactRef {
	for index := range artifacts {
		if artifacts[index].Kind == kind {
			return &artifacts[index]
		}
	}
	return nil
}
