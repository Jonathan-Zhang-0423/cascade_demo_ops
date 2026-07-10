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
	finalVideoDescriptor := findPackageDescriptor(result.RecordingResultPackage.Delivery.AssetRefs, "artifact_pkg_1_demo_video_001")
	if finalVideoDescriptor == nil || finalVideoDescriptor.Role != model.ArtifactRoleFinalDemoVideo || finalVideoDescriptor.Kind != model.ArtifactKindVideo || !finalVideoDescriptor.Encrypted || !finalVideoDescriptor.Sensitive {
		t.Fatalf("expected encrypted final demo video delivery descriptor, got %+v", finalVideoDescriptor)
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

func TestRunClientExecutionRecordingAndRenderReturnsFailedResultPackageWithoutRender(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	now := time.Date(2026, 7, 9, 20, 45, 0, 0, time.UTC)
	service := &fakeRecordingRenderService{
		recordResult: RecordResult{
			GeneratedAssets: []model.ArtifactRef{{
				ID:           "artifact_failure_screenshot_001",
				Kind:         "failure_screenshot",
				URI:          "file:///tmp/failure-step-001.png",
				MimeType:     "image/png",
				SHA256:       "failure_hash",
				Sensitive:    true,
				SourceNodeID: "node_start",
				CreatedAt:    now,
				Metadata:     map[string]any{"asset_role": "failure_screenshot", "include_in_demo": false},
			}, {
				ID:        "artifact_browser_trace",
				Kind:      "browser_trace",
				URI:       "file:///tmp/trace.zip",
				MimeType:  "application/zip",
				SHA256:    "trace_hash",
				CreatedAt: now,
			}},
			StepResults: []model.StepResult{{NodeID: "node_start", Status: "failed", ObservedState: "Timeout waiting for selector"}},
			FailureDiagnostic: &model.ScriptFailureDiagnostic{
				ID:              "diag_node_start",
				SchemaVersion:   model.ScriptFailureDiagnosticSchemaVersion,
				FailedNodeID:    "node_start",
				Error:           model.AgentError{Code: "selector_timeout", Message: "Timeout waiting for selector", Retryable: true},
				CurrentURL:      "https://app.example.com/dashboard",
				PageTitle:       "Dashboard",
				RedactionReport: model.DiagnosticRedactionReport{Applied: true, FullHTMLIncluded: false},
				CapturedAt:      now,
			},
			StartedAt:   now.Add(-time.Second),
			CompletedAt: now,
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
	if len(service.calls) != 1 || service.calls[0] != "record" {
		t.Fatalf("failed recording should stop before render, got calls %+v", service.calls)
	}
	if result.RecordingResultPackage.Status != model.RecordingResultStatusFailed || result.RecordingResultPackage.FailureDiagnostic == nil {
		t.Fatalf("expected failed recording result package, got %+v", result.RecordingResultPackage)
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

func findPackageDescriptor(descriptors []model.PackageArtifactDescriptor, id string) *model.PackageArtifactDescriptor {
	for index := range descriptors {
		if descriptors[index].ID == id {
			return &descriptors[index]
		}
	}
	return nil
}
