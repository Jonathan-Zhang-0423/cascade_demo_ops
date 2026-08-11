package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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
	var replayRef *model.ArtifactRef
	for index := range result.GeneratedAssets {
		if result.GeneratedAssets[index].Kind == "replay_manifest" {
			replayRef = &result.GeneratedAssets[index]
			break
		}
	}
	if replayRef == nil || replayRef.SHA256 == "" || replayRef.SizeBytes <= 0 {
		t.Fatalf("outline result did not publish a checksummed replay manifest: %+v", result.GeneratedAssets)
	}
	manifestPath, err := directWorkerLocalPath(replayRef.URI)
	if err != nil {
		t.Fatal(err)
	}
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest model.ReplayManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ManifestURI == "" || manifest.StageEventLogURI == "" || manifest.RunID != pkg.RecordingRunSpec.RunID {
		t.Fatalf("replay manifest is not bound to the same job and stage log: %+v", manifest)
	}
	foundDelivery := false
	for _, descriptor := range result.Delivery.AssetRefs {
		if descriptor.ID == replayRef.ID && descriptor.SHA256 == replayRef.SHA256 && descriptor.SizeBytes == replayRef.SizeBytes {
			foundDelivery = true
		}
	}
	if !foundDelivery {
		t.Fatalf("replay manifest is absent from delivery descriptors: %+v", result.Delivery.AssetRefs)
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
	stage := request.RuntimePlan.Stages[0]
	bundleHash := request.Package.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
	policyHash := request.Package.ExecutableScriptBundle.Reproducibility.PlanHashSHA256
	event := model.StageExecutionEvent{
		SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_1", RunID: request.RuntimePlan.RunID,
		SourcePackageID: request.Package.PackageID, SourceBundleHashSHA256: bundleHash, PolicyHashSHA256: policyHash,
		NodeID: stage.NodeID, StageID: stage.ID, Attempt: 1, Sequence: 1,
		EventType: model.StageExecutionEventObservationCollected, OccurredAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
		Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, Title: "项目详情"},
		EvidenceRefs: []model.EvidenceRef{{ID: "artifact_1"}},
	}
	if err := request.EventSink.Append(ctx, event); err != nil {
		return model.RecordingResultPackage{}, err
	}
	return model.RecordingResultPackage{SourcePackageID: request.Package.PackageID}, nil
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
