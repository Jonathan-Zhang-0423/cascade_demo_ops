package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestBuildAndWriteReplayManifestBasicSuccess(t *testing.T) {
	dir := t.TempDir()
	result := model.RecordingResultPackage{
		ResultID:        "result_001",
		SourcePackageID: "pkg_001",
		Status:          model.RecordingResultStatusGenerated,
		ValidationReports: []model.ValidationReport{
			{ReportID: "rpt_pre", Phase: model.ValidationPhasePreExecution, Decision: model.ValidationDecisionContinue, Checks: nil},
			{ReportID: "rpt_post", Phase: model.ValidationPhasePostExecution, Decision: model.ValidationDecisionContinue, Checks: nil},
		},
		GeneratedAssets: []model.ArtifactRef{
			{ID: "asset_mp4", Kind: "demo_video", URI: "file:///tmp/demo.mp4"},
			{ID: "asset_rec", Kind: "raw_recording", URI: "file:///tmp/rec.webm"},
		},
		AuditTrail: model.CloudExecutionAuditTrail{
			RuntimeVersions: map[string]string{"node": "v24", "chromium": "1228"},
		},
	}
	pkg := model.ClientExecutionPackage{
		PackageID: "pkg_001",
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{
			Reproducibility: model.ExecutableScriptReproducibility{
				BundleHashSHA256:               "bundle_hash",
				PlanHashSHA256:                 "plan_hash",
				BrowserAgentContractHashSHA256: "policy_hash",
			},
		},
	}

	manifest, err := BuildReplayManifest(BuildReplayManifestInput{
		Result:    result,
		Events:    nil,
		Package:   pkg,
		RunID:     "run_001",
		EventDir:  dir,
		CreatedAt: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("BuildReplayManifest: %v", err)
	}
	if manifest.ManifestID == "" || manifest.RunID != "run_001" || manifest.PackageID != "pkg_001" {
		t.Fatalf("manifest identity fields wrong: %+v", manifest)
	}
	if manifest.BundleHashSHA256 != "bundle_hash" {
		t.Fatalf("bundle hash not carried: %s", manifest.BundleHashSHA256)
	}
	if manifest.PolicyHashSHA256 != "policy_hash" {
		t.Fatalf("policy hash must bind the browser agent contract: %s", manifest.PolicyHashSHA256)
	}
	if manifest.Status != "success" {
		t.Fatalf("expected success, got %s", manifest.Status)
	}
	if manifest.MP4URI == "" {
		t.Error("MP4URI must be set from demo_video asset")
	}
	if manifest.RawRecordingURI == "" {
		t.Error("RawRecordingURI must be set from raw_recording asset")
	}
	if len(manifest.ValidationReports) != 2 {
		t.Errorf("expected 2 validation report refs, got %d", len(manifest.ValidationReports))
	}
	// File must have been written to the event directory
	manifestPath := filepath.Join(dir, "replay-manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("replay-manifest.json not written to event dir: %v", err)
	}
	if manifest.ManifestURI == "" {
		t.Error("ManifestURI must be set after write")
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("written manifest failed Validate: %v", err)
	}
}

func TestBuildReplayManifestFailedStatusFromResult(t *testing.T) {
	dir := t.TempDir()
	result := model.RecordingResultPackage{
		ResultID:        "result_002",
		SourcePackageID: "pkg_002",
		Status:          model.RecordingResultStatusFailed,
		FailureDiagnostic: &model.ScriptFailureDiagnostic{
			FailedNodeID: "node_stage1",
			Error:        model.AgentError{Code: "browser_agent_target_not_resolved"},
		},
		ValidationReports: []model.ValidationReport{
			{ReportID: "rpt_rt", Phase: model.ValidationPhaseRuntimeStage, Decision: model.ValidationDecisionStopAndReport,
				Checks: []model.ValidationCheck{{Code: "STAGE_FAILED", Passed: false, Required: true}}},
		},
	}
	pkg := model.ClientExecutionPackage{
		PackageID: "pkg_002",
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{
			Reproducibility: model.ExecutableScriptReproducibility{
				BundleHashSHA256:               "bundle_hash_2",
				PlanHashSHA256:                 "plan_hash_2",
				BrowserAgentContractHashSHA256: "policy_hash_2",
			},
		},
	}
	manifest, err := BuildReplayManifest(BuildReplayManifestInput{
		Result: result, Events: nil, Package: pkg,
		RunID: "run_002", EventDir: dir,
		CreatedAt: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("BuildReplayManifest: %v", err)
	}
	if manifest.Status != "failed" {
		t.Errorf("expected failed, got %s", manifest.Status)
	}
	if manifest.FailedNodeID != "node_stage1" {
		t.Errorf("expected FailedNodeID=node_stage1, got %s", manifest.FailedNodeID)
	}
	if manifest.FinalDecision != model.ValidationDecisionStopAndReport {
		t.Errorf("expected stop_and_report final decision")
	}
}

func TestBuildReplayManifestWaiverContext(t *testing.T) {
	dir := t.TempDir()
	result := model.RecordingResultPackage{
		ResultID: "result_003", SourcePackageID: "pkg_003",
		Status: model.RecordingResultStatusFailed,
	}
	pkg := model.ClientExecutionPackage{
		PackageID: "pkg_003",
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{
			Reproducibility: model.ExecutableScriptReproducibility{BundleHashSHA256: "bh", PlanHashSHA256: "ph", BrowserAgentContractHashSHA256: "policy"},
		},
	}
	waiver := BrowserAgentTestWaiver{
		WaiverID:       "wv_001",
		DevTestOnly:    true,
		AllowedNodes:   []BrowserAgentTestWaiverNode{{NodeID: "node_1"}, {NodeID: "node_2"}},
		BlockedReasons: []string{"reason_a"},
	}
	manifest, err := BuildReplayManifest(BuildReplayManifestInput{
		Result: result, Events: nil, Package: pkg,
		RunID: "run_003", EventDir: dir,
		Waiver:    &waiver,
		CreatedAt: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("BuildReplayManifest: %v", err)
	}
	if !manifest.DevTestOnly || manifest.WaiverID != "wv_001" {
		t.Error("waiver context not propagated")
	}
	if len(manifest.WaiverAllowedNodeIDs) != 2 {
		t.Errorf("expected 2 allowed node IDs, got %d", len(manifest.WaiverAllowedNodeIDs))
	}
	if len(manifest.WaiverBlockedReasons) != 1 {
		t.Errorf("expected 1 blocked reason, got %d", len(manifest.WaiverBlockedReasons))
	}
}

func TestBuildReplayManifestBindsStageEventsAndTraceArtifacts(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	result := model.RecordingResultPackage{
		Status:            model.RecordingResultStatusGenerated,
		StepResults:       []model.StepResult{{NodeID: "node_build", Status: "passed", Artifacts: []model.ArtifactRef{{ID: "shot_build", Kind: "screenshot", URI: "file:///shot.png"}}}},
		ExecutionTrace:    &model.ExecutionTrace{Artifacts: []model.ArtifactRef{{ID: "trace_build", Kind: "browser_trace", URI: "file:///trace.zip"}}},
		ValidationReports: []model.ValidationReport{{ReportID: "runtime_build", NodeID: "node_build", StageID: "stage_build", Decision: model.ValidationDecisionContinue}},
	}
	pkg := model.ClientExecutionPackage{PackageID: "pkg_build", ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{
		ScriptManifest:  model.ExecutableScriptManifest{Runtime: model.ExecutableScriptRuntimeBrowserAgentOutlineV1},
		Reproducibility: model.ExecutableScriptReproducibility{BundleHashSHA256: "bundle_build", PlanHashSHA256: "policy_build"},
	}}
	manifest, err := BuildReplayManifest(BuildReplayManifestInput{
		Result: result, Package: pkg, RunID: "run_build", EventDir: t.TempDir(), CreatedAt: now,
		Events: []model.StageExecutionEvent{{
			NodeID: "node_build", StageID: "stage_build",
			Observation:  &model.RuntimeObservation{URL: "https://example.com/project/1", Title: "Build complete"},
			EvidenceRefs: []model.EvidenceRef{{ID: "evidence_build", ArtifactID: "shot_build"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Stages) != 1 || manifest.Stages[0].StageID != "stage_build" || manifest.Stages[0].ObservedTitle != "Build complete" || manifest.Stages[0].ValidationDecision != model.ValidationDecisionContinue {
		t.Fatalf("manifest stage binding is incomplete: %+v", manifest.Stages)
	}
	if len(manifest.Stages[0].EvidenceArtifactIDs) != 1 || manifest.Stages[0].EvidenceArtifactIDs[0] != "shot_build" {
		t.Fatalf("manifest evidence IDs are not deduplicated and bound: %+v", manifest.Stages[0].EvidenceArtifactIDs)
	}
	if manifest.BrowserTraceURI != "file:///trace.zip" || manifest.ExecutionBundleRuntime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		t.Fatalf("manifest runtime or trace provenance is missing: %+v", manifest)
	}
}
