package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestExecutionRuntimeRouterKeepsLegacyAndOutlinePathsSeparate(t *testing.T) {
	legacy := &stubLegacyRuntimeRunner{}
	outline := &stubOutlineRuntimeRunner{}
	router := newExecutionRuntimeRouter(legacy, outline)

	legacyPackage := runtimeTestPackage(model.ExecutableScriptRuntimePlaywrightRestrictedSandbox)
	if _, err := router.Run(context.Background(), executionRuntimeRequest{Package: &legacyPackage}); err != nil {
		t.Fatal(err)
	}
	if legacy.calls != 1 || outline.calls != 0 {
		t.Fatalf("legacy package reached wrong runner: legacy=%d outline=%d", legacy.calls, outline.calls)
	}

	outlinePackage := readBrowserAgentOutlineFixture(t)
	if _, err := router.Run(context.Background(), executionRuntimeRequest{Package: &outlinePackage}); err != nil {
		t.Fatal(err)
	}
	if legacy.calls != 1 || outline.calls != 1 {
		t.Fatalf("outline package must bypass legacy worker: legacy=%d outline=%d", legacy.calls, outline.calls)
	}
}

func TestExecutionRuntimeRouterReturnsStableErrors(t *testing.T) {
	outlinePackage := readBrowserAgentOutlineFixture(t)
	router := newExecutionRuntimeRouter(&stubLegacyRuntimeRunner{}, unavailableBrowserAgentOutlineRunner{})
	_, err := router.Run(context.Background(), executionRuntimeRequest{Package: &outlinePackage})
	if runtimeExecutionErrorCode(err) != runtimeErrorOutlineRunnerUnavailable {
		t.Fatalf("expected stable unavailable error, got %q: %v", runtimeExecutionErrorCode(err), err)
	}

	unknownPackage := runtimeTestPackage("future-unknown-runtime")
	_, err = router.Run(context.Background(), executionRuntimeRequest{Package: &unknownPackage})
	if runtimeExecutionErrorCode(err) != runtimeErrorUnsupported {
		t.Fatalf("expected unsupported_runtime, got %q: %v", runtimeExecutionErrorCode(err), err)
	}
}

func TestUnavailableOutlineRunnerReportsNewRuntimeStageOnly(t *testing.T) {
	legacy := &stubLegacyRuntimeRunner{err: errors.New("legacy runner must not be called")}
	router := newExecutionRuntimeRouter(legacy, unavailableBrowserAgentOutlineRunner{})
	pkg := readBrowserAgentOutlineFixture(t)
	var stages []string
	_, err := router.Run(context.Background(), executionRuntimeRequest{
		Package:  &pkg,
		Progress: func(stage string, _ string, _ int) { stages = append(stages, stage) },
	})
	if runtimeExecutionErrorCode(err) != runtimeErrorOutlineRunnerUnavailable {
		t.Fatalf("unexpected error: %v", err)
	}
	if legacy.calls != 0 {
		t.Fatal("outline package entered the legacy TypeScript worker")
	}
	if len(stages) != 1 || stages[0] != "validating_pre_execution" {
		t.Fatalf("outline skeleton reported unexpected stages: %v", stages)
	}
}

func TestOutlineRuntimePublishesStageEventAuditArtifact(t *testing.T) {
	outline := &eventPublishingOutlineRunner{}
	router := newExecutionRuntimeRouter(&stubLegacyRuntimeRunner{}, outline)
	pkg := readBrowserAgentOutlineFixture(t)
	result, err := router.Run(context.Background(), executionRuntimeRequest{
		Package: &pkg, CloudJobID: "job_audit", RecordingOutputDir: filepath.Join(t.TempDir(), "recording"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutionRuntime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 || result.StageEventLogRef == nil || result.StageEventLogRef.SHA256 == "" {
		t.Fatalf("outline result did not receive runtime provenance: %+v", result)
	}
}

func TestExecutionRuntimeRouterBlocksContractViolationBeforeOutlineRunner(t *testing.T) {
	outline := &stubOutlineRuntimeRunner{}
	router := newExecutionRuntimeRouter(&stubLegacyRuntimeRunner{}, outline)
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.ExecutableScriptBundle.ScriptOutline.Stages[0].Objective = "Changed after approval"
	_, err := router.Run(context.Background(), executionRuntimeRequest{Package: &pkg})
	if runtimeExecutionErrorCode(err) != runtimeErrorBrowserAgentContractViolation {
		t.Fatalf("expected contract violation, got %q: %v", runtimeExecutionErrorCode(err), err)
	}
	if outline.calls != 0 {
		t.Fatal("outline runner was called before mandatory contract preflight")
	}
}

type stubLegacyRuntimeRunner struct {
	calls int
	err   error
}

func (r *stubLegacyRuntimeRunner) Run(context.Context, executionRuntimeRequest) (model.RecordingResultPackage, error) {
	r.calls++
	return model.RecordingResultPackage{}, r.err
}

type stubOutlineRuntimeRunner struct {
	calls int
	err   error
}

type eventPublishingOutlineRunner struct{}

func (eventPublishingOutlineRunner) Run(ctx context.Context, request BrowserAgentOutlineRunRequest) (model.RecordingResultPackage, error) {
	if len(request.RuntimePlan.Stages) == 0 {
		return model.RecordingResultPackage{}, errors.New("compiled runtime plan missing")
	}
	event := model.StageExecutionEvent{
		SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_1", RunID: "run_1",
		SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
		NodeID: "node_1", StageID: "stage_1", Attempt: 1, Sequence: 1,
		EventType: model.StageExecutionEventObservationCollected, OccurredAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
		Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, Title: "项目详情"},
		EvidenceRefs: []model.EvidenceRef{{ID: "artifact_1"}},
	}
	if err := request.EventSink.Append(ctx, event); err != nil {
		return model.RecordingResultPackage{}, err
	}
	return model.RecordingResultPackage{SourcePackageID: "pkg_1"}, nil
}

func (r *stubOutlineRuntimeRunner) Run(context.Context, BrowserAgentOutlineRunRequest) (model.RecordingResultPackage, error) {
	r.calls++
	return model.RecordingResultPackage{}, r.err
}

func runtimeTestPackage(runtimeName string) model.ClientExecutionPackage {
	return model.ClientExecutionPackage{ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{
		ScriptManifest: model.ExecutableScriptManifest{Runtime: runtimeName},
	}}
}
