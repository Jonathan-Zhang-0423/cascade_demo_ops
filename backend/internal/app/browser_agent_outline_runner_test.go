package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/driver"
	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/model"
)

func TestLocalBrowserAgentOutlineRunnerBuildsAuditableResultPackage(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	recordingPath := filepath.Join(t.TempDir(), "recording.webm")
	if err := os.WriteFile(recordingPath, []byte("recording"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := &stubBrowserAgentWorkerSession{recordingPath: recordingPath}
	var openRequest driver.BrowserAgentWorkerOpenRequest
	runner := localBrowserAgentOutlineRunner{
		service:       &Service{},
		renderService: stubBrowserAgentRenderService{},
		sessionFactory: func(_ context.Context, request driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error) {
			openRequest = request
			return session, driver.BrowserAgentWorkerOpenResult{SessionID: request.SessionID, RuntimeVersions: map[string]string{"runner": "stub-browser-agent"}}, nil
		},
	}
	var progressStages []string
	result, err := runner.Run(context.Background(), BrowserAgentOutlineRunRequest{
		Package: &pkg, RuntimePlan: plan, CloudJobID: "job_outline_result",
		RecordingOutputDir: t.TempDir(), RenderOutputDir: t.TempDir(), ResultCreatedAt: time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC),
		Progress:  func(stage, _ string, _ int) { progressStages = append(progressStages, stage) },
		EventSink: &memoryStageEventSink{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(openRequest.AllowedDomains) == 0 || len(openRequest.ForbiddenPathPrefixes) == 0 {
		t.Fatalf("worker session did not receive runtime network policy: %+v", openRequest)
	}
	if session.observeCalls != len(plan.Stages) || session.executeCalls != len(plan.Stages) || session.closeCalls != 1 {
		t.Fatalf("runner did not preserve one browser session across stages: %+v", session)
	}
	if result.Status != model.RecordingResultStatusGenerated || result.ExecutionRuntime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		t.Fatalf("unexpected browser-agent result package: %+v", result)
	}
	if len(result.StepResults) != len(plan.Stages) || len(result.ValidationReports) != len(plan.Stages) {
		t.Fatalf("result package lost stage results or validations: steps=%d reports=%d", len(result.StepResults), len(result.ValidationReports))
	}
	if result.ExecutionTrace == nil || result.ExecutionTrace.PassRate != 1 || len(result.GeneratedAssets) < len(plan.Stages)*2 {
		t.Fatalf("result package lost browser evidence: %+v", result.ExecutionTrace)
	}
	for _, required := range []string{"validating_pre_execution", "running_browser_agent", "validating_runtime_stage", "validating_post_execution", "packaging_recording", "directing", "rendering", "quality_validation"} {
		if !containsProgressStage(progressStages, required) {
			t.Fatalf("missing progress stage %q in %v", required, progressStages)
		}
	}
}

func TestDeterministicBrowserAgentStageVerifierStopsOnFailedAssertion(t *testing.T) {
	verifier := deterministicBrowserAgentStageVerifier{}
	report, err := verifier.ValidateStageEvents(context.Background(), BrowserAgentValidationContext{
		SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", EffectivePolicyHashSHA256: "policy_hash",
	}, []model.StageExecutionEvent{{
		SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_1", RunID: "run_1",
		SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
		NodeID: "node_1", StageID: "stage_1", Attempt: 1, Sequence: 1,
		EventType: model.StageExecutionEventOutcomeObserved, OccurredAt: timeNowUTC(),
		Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "element_visible", Passed: false}}},
		EvidenceRefs: []model.EvidenceRef{{ID: "evidence_1", Kind: model.EvidenceKindWebScreenshot}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != model.ValidationDecisionStopAndReport || report.PassRate != 0 {
		t.Fatalf("failed assertion must stop the next stage: %+v", report)
	}
}

func TestDeterministicBrowserAgentStageVerifierStopsWhenRequiredValidationIsMissing(t *testing.T) {
	verifier := deterministicBrowserAgentStageVerifier{requiredValidations: map[string][]model.ValidationSpec{
		"node_1": {{ID: "validation_1", Kind: "element_visible", Required: true}},
	}}
	report, err := verifier.ValidateStageEvents(context.Background(), BrowserAgentValidationContext{
		SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", EffectivePolicyHashSHA256: "policy_hash",
	}, []model.StageExecutionEvent{{
		SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_1", RunID: "run_1",
		SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
		NodeID: "node_1", StageID: "stage_1", Attempt: 1, Sequence: 1,
		EventType: model.StageExecutionEventOutcomeObserved, OccurredAt: timeNowUTC(),
		Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "action_click_completed", Passed: true}}},
		EvidenceRefs: []model.EvidenceRef{{ID: "evidence_1", Kind: model.EvidenceKindWebScreenshot}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Fatalf("missing required validation must stop the next stage: %+v", report)
	}
}

type stubBrowserAgentWorkerSession struct {
	observeCalls  int
	executeCalls  int
	closeCalls    int
	recordingPath string
}

func (s *stubBrowserAgentWorkerSession) Observe(_ context.Context, stage driver.BrowserAgentWorkerStage) (driver.BrowserAgentWorkerStageResult, error) {
	s.observeCalls++
	artifact := stubBrowserAgentArtifact(stage.NodeID, "before")
	return driver.BrowserAgentWorkerStageResult{
		Observation:  model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, URL: "https://app.example.com/dashboard", Title: "Dashboard", Assertions: []model.RuntimeAssertion{{Kind: "target_resolved", Passed: true, Actual: "approved_semantic_target"}}},
		EvidenceRefs: []model.EvidenceRef{{ID: "evidence_" + artifact.ID, Kind: model.EvidenceKindWebScreenshot, ArtifactID: artifact.ID, Confidence: 1}},
		Artifacts:    []model.ArtifactRef{artifact}, TargetResolved: true,
	}, nil
}

func (s *stubBrowserAgentWorkerSession) Execute(_ context.Context, stage driver.BrowserAgentWorkerStage) (driver.BrowserAgentWorkerStageResult, error) {
	s.executeCalls++
	artifact := stubBrowserAgentArtifact(stage.NodeID, "after")
	assertions := []model.RuntimeAssertion{{Kind: "action_completed", Passed: true, Actual: stage.TargetContract.SemanticID}}
	for _, validation := range stage.Validations {
		if validation.Required {
			assertions = append(assertions, model.RuntimeAssertion{Kind: "required_" + validation.Kind + ":" + validation.ID, Passed: true, Actual: "matched"})
		}
	}
	return driver.BrowserAgentWorkerStageResult{
		Observation:  model.RuntimeObservation{Source: model.RuntimeObservationAssertion, URL: "https://app.example.com/dashboard", Title: "Dashboard", Assertions: assertions},
		EvidenceRefs: []model.EvidenceRef{{ID: "evidence_" + artifact.ID, Kind: model.EvidenceKindWebScreenshot, ArtifactID: artifact.ID, Confidence: 1}},
		Artifacts:    []model.ArtifactRef{artifact}, TargetResolved: true,
	}, nil
}

func (s *stubBrowserAgentWorkerSession) Close(context.Context) (driver.BrowserAgentWorkerCloseResult, error) {
	s.closeCalls++
	return driver.BrowserAgentWorkerCloseResult{
		RecordingPath:   s.recordingPath,
		Artifacts:       []model.ArtifactRef{{ID: "artifact_trace", Kind: "browser_trace", URI: "file:///trace.zip", SHA256: "trace_hash", SizeBytes: 10, Sensitive: true}},
		RuntimeVersions: map[string]string{"browser": "chromium"},
	}, nil
}

type stubBrowserAgentRenderService struct{}

func (stubBrowserAgentRenderService) Render(_ context.Context, request executor.RenderRequest) (executor.RenderResult, error) {
	if err := os.MkdirAll(request.OutputDir, 0o700); err != nil {
		return executor.RenderResult{}, err
	}
	videoPath := filepath.Join(request.OutputDir, "demo.mp4")
	manifestPath := filepath.Join(request.OutputDir, "render_manifest.json")
	planPath := filepath.Join(request.OutputDir, "demo_edit_plan.json")
	for path, contents := range map[string]string{videoPath: "demo-video", manifestPath: "{}", planPath: "{}"} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			return executor.RenderResult{}, err
		}
	}
	return executor.RenderResult{VideoPath: videoPath, RenderManifestPath: manifestPath, DemoEditPlanPath: planPath}, nil
}

func (s *stubBrowserAgentWorkerSession) Abort() error { return nil }

func stubBrowserAgentArtifact(nodeID, phase string) model.ArtifactRef {
	return model.ArtifactRef{
		ID: "artifact_" + nodeID + "_" + phase, Kind: "screenshot", URI: "file:///" + nodeID + "-" + phase + ".png",
		MimeType: "image/png", SHA256: "sha256_" + nodeID + "_" + phase, SizeBytes: 10, Sensitive: true,
		SourceNodeID: nodeID, Metadata: map[string]any{"include_in_demo": phase == "after"},
	}
}

func containsProgressStage(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
