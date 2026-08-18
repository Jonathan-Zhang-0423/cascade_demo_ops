package app

import (
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestBuildValidationRunReportMarksServerFixtureNonFormalAndRoutesFeedback(t *testing.T) {
	result := model.RecordingResultPackage{
		ResultID: "result-1", SourcePackageID: "pkg-1", CloudJobID: "job-1",
		SchemaVersion: model.RecordingResultPackageSchemaVersion,
		Status:        model.RecordingResultStatusFailed,
		ValidationReports: []model.ValidationReport{{
			SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "vr-1", RunID: "run-1",
			SourcePackageID: "pkg-1", SourceBundleHashSHA256: "bundle-1", PolicyHashSHA256: "policy-1",
			Phase: model.ValidationPhaseRuntimeStage, NodeID: "node-1", StageID: "stage-1",
			Decision: model.ValidationDecisionStopAndReport, PassRate: 0, OverallConfidence: 1,
			EvidenceQuality: model.RuntimeObservationActualBrowser, CreatedAt: time.Now().UTC(),
			Checks: []model.ValidationCheck{{
				ID: "check-1", Kind: "stage", Code: "business_input_missing", Passed: false,
				Required: true, Severity: model.FindingSeverityBlocking,
				ResponsibilityDomain: model.ValidationCheckDomainApp,
				Summary:              "package is missing required input", EvidenceRefs: []model.EvidenceRef{{ID: "e-1"}},
			}},
		}},
		StepResults: []model.StepResult{{NodeID: "node-1", Status: "failed"}},
	}
	pkg := model.ClientExecutionPackage{
		PackageID: "pkg-1", Metadata: map[string]any{"producer": "server_controlled_business_acceptance"},
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{Reproducibility: model.ExecutableScriptReproducibility{BundleHashSHA256: "bundle-1", BrowserAgentContractHashSHA256: "policy-1"}},
	}
	manifest := model.ReplayManifest{ManifestID: "manifest-run-1", RunID: "run-1", PackageID: "pkg-1", BundleHashSHA256: "bundle-1", PolicyHashSHA256: "policy-1", Status: "failed", FinalDecision: model.ValidationDecisionStopAndReport, CreatedAt: time.Now().UTC()}

	report, err := BuildValidationRunReport(result, pkg, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	if report.FormalAppServerSuccess || report.OriginalPackageUnchanged != true || report.ReplayManifestID != "manifest-run-1" {
		t.Fatalf("fixture provenance was not preserved: %+v", report)
	}
	if len(report.AppFeedback) != 1 || len(report.ServerFeedback) != 0 {
		t.Fatalf("feedback was not routed by responsibility domain: app=%+v server=%+v", report.AppFeedback, report.ServerFeedback)
	}
	if len(report.Findings) != 1 || report.Findings[0].Category != model.ValidationFailureCategoryAppPackageContract {
		t.Fatalf("finding category was not normalized: %+v", report.Findings)
	}
}

func TestBuildValidationRunReportRejectsHashMismatch(t *testing.T) {
	result := model.RecordingResultPackage{SourcePackageID: "pkg-1", ValidationReports: []model.ValidationReport{{
		ReportID: "vr-1", RunID: "run-1", SourcePackageID: "pkg-1", SourceBundleHashSHA256: "bundle-other", PolicyHashSHA256: "policy-1",
		SchemaVersion: model.ValidationReportSchemaVersion, Phase: model.ValidationPhasePreExecution,
		Decision: model.ValidationDecisionStopAndReport, EvidenceQuality: model.RuntimeObservationDerivedPlan,
		CreatedAt: time.Now().UTC(),
	}}}
	pkg := model.ClientExecutionPackage{PackageID: "pkg-1", ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{Reproducibility: model.ExecutableScriptReproducibility{BundleHashSHA256: "bundle-1", BrowserAgentContractHashSHA256: "policy-1"}}}
	manifest := model.ReplayManifest{ManifestID: "manifest-run-1", RunID: "run-1", PackageID: "pkg-1", BundleHashSHA256: "bundle-1", PolicyHashSHA256: "policy-1", Status: "failed", FinalDecision: model.ValidationDecisionStopAndReport, CreatedAt: time.Now().UTC()}
	if _, err := BuildValidationRunReport(result, pkg, manifest); err == nil {
		t.Fatal("hash mismatch must be rejected")
	}
}

func TestBuildValidationRunReportClassifiesRuntimeProviderAndEnvironmentFindings(t *testing.T) {
	now := time.Now().UTC()
	checks := []model.ValidationCheck{
		{ID: "runtime", Kind: "runtime", Code: "session_expired", Passed: false, Severity: model.FindingSeverityBlocking, ResponsibilityDomain: model.ValidationCheckDomainBrowserRuntime, EvidenceRefs: []model.EvidenceRef{{ID: "session-shot", ArtifactID: "shot-session"}}},
		{ID: "provider", Kind: "candidate", Code: "candidate_model_timeout", Passed: false, Severity: model.FindingSeverityWarning, ResponsibilityDomain: model.ValidationCheckDomainProviderCandidate, EvidenceRefs: []model.EvidenceRef{{ID: "provider-log"}}},
		{ID: "environment", Kind: "environment", Code: "chromium_unavailable", Passed: false, Severity: model.FindingSeverityBlocking, ResponsibilityDomain: model.ValidationCheckDomainEnvironment, EvidenceRefs: []model.EvidenceRef{{ID: "environment-log"}}},
	}
	result := model.RecordingResultPackage{SourcePackageID: "pkg-1", Status: model.RecordingResultStatusFailed, ValidationReports: []model.ValidationReport{{
		SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "vr-1", RunID: "run-1", SourcePackageID: "pkg-1", SourceBundleHashSHA256: "bundle-1", PolicyHashSHA256: "policy-1",
		Phase: model.ValidationPhaseRuntimeStage, NodeID: "node-1", StageID: "stage-1", Decision: model.ValidationDecisionStopAndReport,
		EvidenceQuality: model.RuntimeObservationActualBrowser, CreatedAt: now, Checks: checks,
	}}, StepResults: []model.StepResult{{NodeID: "node-1", Status: "failed"}}}
	pkg := model.ClientExecutionPackage{PackageID: "pkg-1", ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{Reproducibility: model.ExecutableScriptReproducibility{BundleHashSHA256: "bundle-1", BrowserAgentContractHashSHA256: "policy-1"}}}
	manifest := model.ReplayManifest{ManifestID: "manifest-run-1", RunID: "run-1", PackageID: "pkg-1", BundleHashSHA256: "bundle-1", PolicyHashSHA256: "policy-1", Status: "failed", FinalDecision: model.ValidationDecisionStopAndReport, CreatedAt: now}
	report, err := BuildValidationRunReport(result, pkg, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 3 || report.Findings[0].Category != model.ValidationFailureCategoryBrowserRuntime || report.Findings[1].Category != model.ValidationFailureCategoryProviderCandidate || report.Findings[2].Category != model.ValidationFailureCategoryEnvironment {
		t.Fatalf("failure categories were not preserved: %+v", report.Findings)
	}
	if report.Findings[0].RecommendedRepair == "" || len(report.EvidenceRefs) != 3 {
		t.Fatalf("findings must receive a repair fallback and report-level evidence index: %+v", report)
	}
}

func TestBuildValidationRunReportCopiesStructuredStageEvidenceFromReplayManifest(t *testing.T) {
	now := time.Now().UTC()
	result := model.RecordingResultPackage{SourcePackageID: "pkg-1", Status: model.RecordingResultStatusGenerated,
		StepResults:       []model.StepResult{{NodeID: "node-1", Status: "passed"}},
		ValidationReports: []model.ValidationReport{{SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "vr-1", RunID: "run-1", SourcePackageID: "pkg-1", SourceBundleHashSHA256: "bundle-1", PolicyHashSHA256: "policy-1", Phase: model.ValidationPhaseRuntimeStage, NodeID: "node-1", StageID: "stage-1", Decision: model.ValidationDecisionContinue, EvidenceQuality: model.RuntimeObservationActualBrowser, CreatedAt: now}}}
	pkg := model.ClientExecutionPackage{PackageID: "pkg-1", ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{Reproducibility: model.ExecutableScriptReproducibility{BundleHashSHA256: "bundle-1", BrowserAgentContractHashSHA256: "policy-1"}}}
	manifest := model.ReplayManifest{ManifestID: "manifest-run-1", RunID: "run-1", PackageID: "pkg-1", BundleHashSHA256: "bundle-1", PolicyHashSHA256: "policy-1", Status: "success", FinalDecision: model.ValidationDecisionContinue, CreatedAt: now,
		Stages: []model.ReplayManifestStage{{NodeID: "node-1", StageID: "stage-1", Order: 1, Status: "passed", EvidenceArtifactIDs: []string{"outcome-shot"}, ActionDefinitionEvidenceIDs: []string{"approved-action"}, BeforeScreenshotArtifactIDs: []string{"shot-before"}, AfterScreenshotArtifactIDs: []string{"shot-after"}, StageEventIDs: []string{"event-1"}, TraceArtifactIDs: []string{"trace-1"}}}}
	report, err := BuildValidationRunReport(result, pkg, manifest)
	if err != nil {
		t.Fatal(err)
	}
	stage := report.Stages[0]
	if len(stage.EvidenceArtifactIDs) != 1 || stage.EvidenceArtifactIDs[0] != "outcome-shot" || len(stage.ActionDefinitionEvidenceIDs) != 1 || len(stage.BeforeScreenshotArtifactIDs) != 1 || len(stage.AfterScreenshotArtifactIDs) != 1 || len(stage.StageEventIDs) != 1 || len(stage.TraceArtifactIDs) != 1 {
		t.Fatalf("structured replay evidence was not copied into the validation report: %+v", stage)
	}
}
