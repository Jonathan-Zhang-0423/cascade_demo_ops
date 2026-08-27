package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	if openRequest.InitialURL == "" || len(openRequest.AllowedDomains) == 0 || len(openRequest.ForbiddenPathPrefixes) == 0 {
		t.Fatalf("worker session did not receive runtime network policy: %+v", openRequest)
	}
	if openRequest.InitialURL != pkg.ProjectContextSummary.ProductURL {
		t.Fatalf("worker must start from the approved product URL, not a broader base origin: %q", openRequest.InitialURL)
	}
	if openRequest.Browser.Viewport.Width != 2560 || openRequest.Browser.Viewport.Height != 1440 {
		t.Fatalf("Browser Agent evidence master must use the canonical 2K 16:9 web viewport: %+v", openRequest.Browser.Viewport)
	}
	if session.observeCalls != len(plan.Stages) || session.executeCalls != len(plan.Stages) || session.closeCalls != 1 {
		t.Fatalf("runner did not preserve one browser session across stages: %+v", session)
	}
	if result.Status != model.RecordingResultStatusGenerated || result.ExecutionRuntime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		t.Fatalf("unexpected browser-agent result package: %+v", result)
	}
	if len(result.StepResults) != len(plan.Stages) || len(result.ValidationReports) != len(plan.Stages)+2 {
		t.Fatalf("result package lost stage results or validations: steps=%d reports=%d", len(result.StepResults), len(result.ValidationReports))
	}
	if result.ValidationReports[0].Phase != model.ValidationPhasePreExecution || result.ValidationReports[len(result.ValidationReports)-1].Phase != model.ValidationPhasePostExecution {
		t.Fatalf("result package must include strict pre/post validation reports: %+v", result.ValidationReports)
	}
	for _, step := range result.StepResults {
		if step.ObservedState == "" || step.ObservedState == plan.Stages[0].SuccessState {
			t.Fatalf("step result must contain real observation summary, not planned success text: %+v", step)
		}
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

func TestWorkerStageFromRuntimePreservesStageKind(t *testing.T) {
	stage := workerStageFromRuntime(BrowserAgentRuntimeStage{
		ID:        "stage_final",
		Order:     6,
		NodeID:    "final_observe",
		StageKind: model.BusinessStageKindFinalObserve,
	})
	if stage.StageKind != model.BusinessStageKindFinalObserve {
		t.Fatalf("worker stage kind = %q, want final_observe", stage.StageKind)
	}
	encoded, err := json.Marshal(stage)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"stage_kind":"final_observe"`) {
		t.Fatalf("worker RPC lost stage_kind: %s", encoded)
	}
}

func TestDeterministicBrowserAgentStageVerifierStopsOnFailedAssertion(t *testing.T) {
	verifier := deterministicBrowserAgentStageVerifier{}
	report, err := verifier.ValidateStageEvents(context.Background(), model.BrowserAgentValidationContext{
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
	report, err := verifier.ValidateStageEvents(context.Background(), model.BrowserAgentValidationContext{
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

func TestDeterministicBrowserAgentVerifierPreExecutionReportsMissingHashes(t *testing.T) {
	report, err := newDeterministicBrowserAgentStageVerifier(nil).ValidateBeforeExecution(context.Background(), model.BrowserAgentValidationContext{
		RunID: "run_1", SourcePackageID: "pkg_1",
		StageApprovalPlan:    &model.StageApprovalPlan{Stages: []model.StageApprovalStage{{NodeID: "node_1"}}},
		ScriptOutline:        &model.BrowserAgentScriptOutline{Stages: []model.BrowserAgentOutlineStage{{NodeID: "node_1"}}},
		BrowserAgentContract: &model.BrowserAgentContract{ID: "contract_1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	if report.Decision != model.ValidationDecisionStopAndReport || !validationReportHasCheckCode(report, "approved_hash_missing", false) {
		t.Fatalf("pre-execution verifier must stop when approved hashes are missing: %+v", report)
	}
}

func TestDeterministicBrowserAgentStageVerifierStopsOnRuntimeIdentityMismatch(t *testing.T) {
	verifier := deterministicBrowserAgentStageVerifier{}
	report, err := verifier.ValidateStageEvents(context.Background(), model.BrowserAgentValidationContext{
		RunID: "run_1", SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", EffectivePolicyHashSHA256: "policy_hash",
	}, []model.StageExecutionEvent{{
		SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_1", RunID: "run_1",
		SourcePackageID: "pkg_1", SourceBundleHashSHA256: "tampered_bundle_hash", PolicyHashSHA256: "policy_hash",
		NodeID: "node_1", StageID: "stage_1", Attempt: 1, Sequence: 1,
		EventType: model.StageExecutionEventOutcomeObserved, OccurredAt: timeNowUTC(),
		Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "action_completed", Passed: true}}},
		EvidenceRefs: []model.EvidenceRef{{ID: "evidence_1", Kind: model.EvidenceKindWebScreenshot}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != model.ValidationDecisionStopAndReport || !validationReportHasCheckCode(report, "runtime_event_identity_mismatch", false) {
		t.Fatalf("runtime verifier must stop on event/package identity mismatch: %+v", report)
	}
}

func TestDeterministicBrowserAgentPostVerifierRequiresRuntimeReportsAndObservedState(t *testing.T) {
	verifier := deterministicBrowserAgentStageVerifier{}
	now := timeNowUTC()
	vctx := model.BrowserAgentValidationContext{RunID: "run_1", SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", EffectivePolicyHashSHA256: "policy_hash"}
	events := []model.StageExecutionEvent{
		{SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_1", RunID: "run_1", SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash", NodeID: "node_1", StageID: "stage_1", Attempt: 1, Sequence: 1, EventType: model.StageExecutionEventOutcomeObserved, OccurredAt: now, Observation: &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "action_completed", Passed: true}}}, EvidenceRefs: []model.EvidenceRef{{ID: "evidence_1", Kind: model.EvidenceKindWebScreenshot}}},
		{SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_2", RunID: "run_1", SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash", NodeID: "node_1", StageID: "stage_1", Attempt: 1, Sequence: 2, EventType: model.StageExecutionEventStageCompleted, OccurredAt: now.Add(time.Second), Observation: &model.RuntimeObservation{Source: model.RuntimeObservationAssertion}, EvidenceRefs: []model.EvidenceRef{{ID: "evidence_1", Kind: model.EvidenceKindWebScreenshot}}},
	}
	result := model.RecordingResultPackage{
		SourcePackageID:   "pkg_1",
		StepResults:       []model.StepResult{{NodeID: "node_1", Status: "passed", ObservedState: "planned success state", Artifacts: []model.ArtifactRef{{ID: "artifact_1", SourceNodeID: "node_1"}}}},
		ValidationReports: []model.ValidationReport{{SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "validation_pre", RunID: "run_1", SourcePackageID: "pkg_1", SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash", Phase: model.ValidationPhasePreExecution, Decision: model.ValidationDecisionContinue, PassRate: 1, OverallConfidence: 1, EvidenceQuality: model.RuntimeObservationDerivedPlan, EvidenceRefs: []model.EvidenceRef{{ID: "approved_package_contract", Kind: model.EvidenceKindDocs}}, CreatedAt: now}},
	}
	report, err := verifier.ValidatePostExecution(context.Background(), vctx, result, events)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != model.ValidationDecisionStopAndReport ||
		!validationReportHasCheckCode(report, "runtime_stage_report_missing", false) ||
		!validationReportHasCheckCode(report, "observed_state_not_runtime_derived", false) {
		t.Fatalf("post verifier must reject missing runtime reports and plan-derived observed state: %+v", report)
	}
}

func TestBrowserAgentStepResultsNeverUsePlannedSuccessStateAsEvidence(t *testing.T) {
	plan := BrowserAgentRuntimePlan{Stages: []BrowserAgentRuntimeStage{{NodeID: "node_1", SuccessState: "planned business success"}}}
	now := timeNowUTC()
	results := browserAgentStepResults(plan, []model.StageExecutionEvent{
		{NodeID: "node_1", EventType: model.StageExecutionEventStageStarted, OccurredAt: now},
		{NodeID: "node_1", EventType: model.StageExecutionEventOutcomeObserved, OccurredAt: now, Observation: &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "business_saved", Passed: true}}}},
		{NodeID: "node_1", EventType: model.StageExecutionEventStageCompleted, OccurredAt: now.Add(time.Second)},
	}, nil)
	if len(results) != 1 || results[0].Status != "passed" || results[0].ObservedState == "planned business success" || !strings.Contains(results[0].ObservedState, "assertion:business_saved=passed") {
		t.Fatalf("step result must derive its state from runtime observations: %+v", results)
	}
}

func TestBrowserAgentStepResultsAcceptOptionalCapabilityCompletionEvidence(t *testing.T) {
	plan := BrowserAgentRuntimePlan{Stages: []BrowserAgentRuntimeStage{{NodeID: "node_optional"}}}
	now := timeNowUTC()
	results := browserAgentStepResults(plan, []model.StageExecutionEvent{
		{NodeID: "node_optional", EventType: model.StageExecutionEventStageStarted, OccurredAt: now},
		{
			NodeID: "node_optional", EventType: model.StageExecutionEventStageCompleted, OccurredAt: now.Add(time.Second),
			Observation: &model.RuntimeObservation{
				Source:     model.RuntimeObservationActualBrowser,
				Assertions: []model.RuntimeAssertion{{Kind: "optional_capability_recorded", Passed: true, Actual: "optional control was not observed"}},
			},
		},
	}, nil)
	if len(results) != 1 || results[0].Status != "passed" || !strings.Contains(results[0].ObservedState, "assertion:optional_capability_recorded=passed") {
		t.Fatalf("optional capability completion with real browser evidence must remain a passed step result: %+v", results)
	}
}

func validationReportHasCheckCode(report model.ValidationReport, code string, passed bool) bool {
	for _, check := range report.Checks {
		if check.Code == code && check.Passed == passed {
			return true
		}
	}
	return false
}

func TestPreExecutionRepairAllowedRunsOnlyWithoutBlockingChecks(t *testing.T) {
	warning := model.ValidationReport{
		Decision: model.ValidationDecisionRepairAllowed,
		Checks:   []model.ValidationCheck{{Severity: model.FindingSeverityWarning, Passed: false}},
	}
	if !preExecutionValidationAllowsRun(warning) {
		t.Fatal("repair_allowed with warning-only findings should enter bounded runtime repair")
	}
	warning.Checks = append(warning.Checks, model.ValidationCheck{Severity: model.FindingSeverityBlocking, Passed: false})
	if preExecutionValidationAllowsRun(warning) {
		t.Fatal("repair_allowed must not bypass a blocking pre-execution finding")
	}
	if preExecutionValidationAllowsRun(model.ValidationReport{Decision: model.ValidationDecisionStopAndReport}) ||
		preExecutionValidationAllowsRun(model.ValidationReport{Decision: model.ValidationDecisionReunderstandingRequired}) {
		t.Fatal("hard-stop pre-execution decisions must remain blocking")
	}
}

func TestLocalBrowserAgentOutlineRunnerPostValidationStopsDelivery(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.RecordingRunSpec.Outputs.FinalVideo = false
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	runner := localBrowserAgentOutlineRunner{
		service: &Service{}, outcomeVerifier: postStoppingBrowserAgentVerifier{deterministicBrowserAgentStageVerifier: newDeterministicBrowserAgentStageVerifier(&pkg)},
		sessionFactory: func(_ context.Context, _ driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error) {
			return &stubBrowserAgentWorkerSession{}, driver.BrowserAgentWorkerOpenResult{SessionID: "post-stop", RuntimeVersions: map[string]string{"runner": "stub-browser-agent"}}, nil
		},
	}
	result, err := runner.Run(context.Background(), BrowserAgentOutlineRunRequest{Package: &pkg, RuntimePlan: plan, CloudJobID: "post_stop", RecordingOutputDir: t.TempDir(), ResultCreatedAt: timeNowUTC(), EventSink: &memoryStageEventSink{}})
	if err != nil {
		t.Fatalf("a post-validation business failure must become a failed result package: %v", err)
	}
	if result.Status != model.RecordingResultStatusFailed || result.FailureDiagnostic == nil || result.StepResults[0].Status != "failed" {
		t.Fatalf("post-validation stop must prevent a successful delivery: %+v", result)
	}
	if result.ValidationReports[len(result.ValidationReports)-1].Phase != model.ValidationPhasePostExecution || result.ValidationReports[len(result.ValidationReports)-1].Decision != model.ValidationDecisionStopAndReport {
		t.Fatalf("post-validation decision was not retained in the result: %+v", result.ValidationReports)
	}
}

func TestLocalBrowserAgentOutlineRunnerUsesServerOutcomeVerifierSnapshot(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.RecordingRunSpec.Outputs.FinalVideo = false
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{}
	service.SetBrowserAgentOutcomeVerifier(postStoppingBrowserAgentVerifier{deterministicBrowserAgentStageVerifier: newDeterministicBrowserAgentStageVerifier(&pkg)})
	runner := localBrowserAgentOutlineRunner{
		service: service,
		sessionFactory: func(_ context.Context, _ driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error) {
			return &stubBrowserAgentWorkerSession{}, driver.BrowserAgentWorkerOpenResult{SessionID: "service-verifier", RuntimeVersions: map[string]string{"runner": "stub-browser-agent"}}, nil
		},
	}
	result, err := runner.Run(context.Background(), BrowserAgentOutlineRunRequest{Package: &pkg, RuntimePlan: plan, CloudJobID: "service_verifier", RecordingOutputDir: t.TempDir(), ResultCreatedAt: timeNowUTC(), EventSink: &memoryStageEventSink{}})
	if err != nil {
		t.Fatalf("Server-injected verifier should package a controlled failed result: %v", err)
	}
	if result.Status != model.RecordingResultStatusFailed || len(result.ValidationReports) != len(plan.Stages)+2 || result.ValidationReports[len(result.ValidationReports)-1].Decision != model.ValidationDecisionStopAndReport {
		t.Fatalf("runner did not use the Server verifier snapshot: %+v", result)
	}
}

type postStoppingBrowserAgentVerifier struct {
	deterministicBrowserAgentStageVerifier
}

func (v postStoppingBrowserAgentVerifier) ValidateBeforeExecution(ctx context.Context, validationContext model.BrowserAgentValidationContext) (model.ValidationReport, error) {
	return v.deterministicBrowserAgentStageVerifier.ValidateBeforeExecution(ctx, validationContext)
}

func (v postStoppingBrowserAgentVerifier) ValidatePostExecution(_ context.Context, validationContext model.BrowserAgentValidationContext, _ model.RecordingResultPackage, _ []model.StageExecutionEvent) (model.ValidationReport, error) {
	return model.ValidationReport{
		SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "post_stop", RunID: validationContext.SourcePackageID,
		SourcePackageID: validationContext.SourcePackageID, SourceBundleHashSHA256: validationContext.SourceBundleHashSHA256,
		PolicyHashSHA256: validationContext.EffectivePolicyHashSHA256, Phase: model.ValidationPhasePostExecution,
		NodeID: "node_open_dashboard", Decision: model.ValidationDecisionStopAndReport, PassRate: 0, OverallConfidence: 0,
		EvidenceQuality: model.RuntimeObservationAssertion, EvidenceRefs: []model.EvidenceRef{{ID: "post_stop_evidence", Kind: model.EvidenceKindWebScreenshot}}, CreatedAt: timeNowUTC(),
	}, nil
}

func TestLocalBrowserAgentOutlineRunnerPackagesRedactedFailureResult(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	runner := localBrowserAgentOutlineRunner{
		service: &Service{},
		sessionFactory: func(_ context.Context, _ driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error) {
			return &stubBrowserAgentWorkerSession{failNodeID: plan.Stages[1].NodeID}, driver.BrowserAgentWorkerOpenResult{SessionID: "session_failure", RuntimeVersions: map[string]string{"runner": "stub-browser-agent"}}, nil
		},
	}
	result, err := runner.Run(context.Background(), BrowserAgentOutlineRunRequest{
		Package: &pkg, RuntimePlan: plan, CloudJobID: "job_outline_failure", RecordingOutputDir: t.TempDir(),
		ResultCreatedAt: time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC), EventSink: &memoryStageEventSink{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != model.RecordingResultStatusFailed || result.FailureDiagnostic == nil || result.RepairRequest == nil {
		t.Fatalf("outline failure must be returned as a failed result package: %+v", result)
	}
	if result.FailureDiagnostic.FailedNodeID != plan.Stages[1].NodeID || !result.FailureDiagnostic.RedactionReport.Applied || result.FailureDiagnostic.RedactionReport.FullHTMLIncluded {
		t.Fatalf("failure diagnostic did not preserve protocol safety requirements: %+v", result.FailureDiagnostic)
	}
	if result.RepairRequest.ApprovalRequired != true || len(result.ValidationReports) != 3 {
		t.Fatalf("failure result lost repair approval or validation evidence: %+v", result)
	}
	if result.StepResults[0].Status != "passed" || result.StepResults[1].Status != "failed" {
		t.Fatalf("failure result must retain only observed success and the stopped stage: %+v", result.StepResults)
	}
}

func TestLocalBrowserAgentOutlineRunnerPreservesStageFailureWhenRecordingCloseFails(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.RecordingRunSpec.Outputs.FinalVideo = false
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	session := &stubBrowserAgentWorkerSession{failNodeID: plan.Stages[1].NodeID, closeErr: errors.New("synthetic recording finalize failure")}
	runner := localBrowserAgentOutlineRunner{
		service: &Service{},
		sessionFactory: func(_ context.Context, _ driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error) {
			return session, driver.BrowserAgentWorkerOpenResult{SessionID: "session_failure_close", RuntimeVersions: map[string]string{"runner": "stub-browser-agent"}}, nil
		},
	}
	result, err := runner.Run(context.Background(), BrowserAgentOutlineRunRequest{
		Package: &pkg, RuntimePlan: plan, CloudJobID: "job_outline_failure_close", RecordingOutputDir: t.TempDir(),
		ResultCreatedAt: time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC), EventSink: &memoryStageEventSink{},
	})
	if err != nil {
		t.Fatalf("recording cleanup must not mask a persisted stage failure: %v", err)
	}
	if result.Status != model.RecordingResultStatusFailed || result.FailureDiagnostic == nil {
		t.Fatalf("stage failure package was not preserved: %+v", result)
	}
	if result.FailureDiagnostic.FailedNodeID != plan.Stages[1].NodeID || result.FailureDiagnostic.Error.Code == "browser_agent_session_close_failed" {
		t.Fatalf("cleanup failure replaced the business diagnosis: %+v", result.FailureDiagnostic)
	}
}

func TestLocalBrowserAgentOutlineRunnerUsesInjectedVerifierForApprovedRepair(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.RecordingRunSpec.Outputs.FinalVideo = false
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	session := &stubBrowserAgentWorkerSession{}
	runner := localBrowserAgentOutlineRunner{
		service: &Service{}, outcomeVerifier: &repairThenContinueVerifier{},
		sessionFactory: func(_ context.Context, _ driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error) {
			return session, driver.BrowserAgentWorkerOpenResult{SessionID: "repair_session", RuntimeVersions: map[string]string{"runner": "stub-browser-agent"}}, nil
		},
	}
	result, err := runner.Run(context.Background(), BrowserAgentOutlineRunRequest{Package: &pkg, RuntimePlan: plan, CloudJobID: "repair_job", RecordingOutputDir: t.TempDir(), ResultCreatedAt: timeNowUTC(), EventSink: &memoryStageEventSink{}})
	if err != nil {
		t.Fatalf("injected verifier repair should complete: %v", err)
	}
	if len(result.PatchLedger) != 1 || !result.PatchLedger[0].Applied || session.executeCalls != len(plan.Stages)+1 {
		t.Fatalf("repair was not run through the normal outline pipeline: ledger=%+v executes=%d", result.PatchLedger, session.executeCalls)
	}
}

func TestOutlineRuntimeFullPathRecordsSelectorRepairInResultAndAuditLog(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.RecordingRunSpec.Outputs.FinalVideo = false
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	originalBundleHash := pkg.ExecutableScriptBundle.Reproducibility.BundleHashSHA256
	stage := plan.Stages[1]
	if len(stage.Components) == 0 || len(stage.Components[0].SelectorAlternatives) == 0 {
		t.Fatal("fixture must provide one App-approved selector alternative")
	}
	session := &repairAuditBrowserAgentWorkerSession{candidate: stage.Components[0].SelectorAlternatives[0]}
	runner := localBrowserAgentOutlineRunner{
		service: &Service{},
		sessionFactory: func(_ context.Context, _ driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error) {
			return session, driver.BrowserAgentWorkerOpenResult{SessionID: "repair-audit", RuntimeVersions: map[string]string{"runner": "repair-audit"}}, nil
		},
	}
	recordingDir := t.TempDir()
	result, err := newExecutionRuntimeRouter(&stubLegacyRuntimeRunner{}, runner).Run(context.Background(), executionRuntimeRequest{
		Package: &pkg, CloudJobID: "job_repair_audit", RecordingOutputDir: recordingDir, ResultCreatedAt: timeNowUTC(),
	})
	if err != nil {
		t.Fatalf("full outline runtime should package an approved selector repair: %v", err)
	}
	if result.Status != model.RecordingResultStatusGenerated || len(result.PatchLedger) != 1 || !result.PatchLedger[0].Applied {
		t.Fatalf("result package lost approved repair ledger: %+v", result)
	}
	entry := result.PatchLedger[0]
	if entry.After != selectorCandidateEncoding(session.candidate) || entry.SourceBundleHashSHA256 != originalBundleHash || pkg.ExecutableScriptBundle.Reproducibility.BundleHashSHA256 != originalBundleHash {
		t.Fatalf("runtime repair must preserve the approved source bundle and record its candidate: %+v", entry)
	}
	if session.executeCalls != len(plan.Stages) || session.observeCalls != len(plan.Stages)+1 || result.StageEventLogRef == nil {
		t.Fatalf("expected repair re-observation and event audit: session=%+v log=%+v", session, result.StageEventLogRef)
	}
	events := readStageEventAudit(t, result.StageEventLogRef.URI)
	if !hasStageEvent(events, model.StageExecutionEventRepairProposed, 1) || !hasStageEvent(events, model.StageExecutionEventRepairApplied, 2) || !hasStageEvent(events, model.StageExecutionEventTargetResolved, 2) {
		t.Fatalf("repair events or second-attempt target resolution are missing: %+v", events)
	}
	if len(result.ValidationReports) != len(plan.Stages)+2 || result.ValidationReports[2].Decision != model.ValidationDecisionContinue {
		t.Fatalf("outcome verification did not pass after the selector repair: %+v", result.ValidationReports)
	}
}

func TestOutlineRuntimeFullPathRejectsUnapprovedSelectorRepair(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.RecordingRunSpec.Outputs.FinalVideo = false
	session := &repairAuditBrowserAgentWorkerSession{candidate: model.SelectorCandidate{Kind: "testid", Value: "server-invented-target"}}
	runner := localBrowserAgentOutlineRunner{
		service: &Service{},
		sessionFactory: func(_ context.Context, _ driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error) {
			return session, driver.BrowserAgentWorkerOpenResult{SessionID: "repair-denied", RuntimeVersions: map[string]string{"runner": "repair-denied"}}, nil
		},
	}
	result, err := newExecutionRuntimeRouter(&stubLegacyRuntimeRunner{}, runner).Run(context.Background(), executionRuntimeRequest{
		Package: &pkg, CloudJobID: "job_repair_denied", RecordingOutputDir: t.TempDir(), ResultCreatedAt: timeNowUTC(),
	})
	if err != nil {
		t.Fatalf("denied repair must be packaged as a failed result, not escape the runtime: %v", err)
	}
	if result.Status != model.RecordingResultStatusFailed || len(result.PatchLedger) != 1 || result.PatchLedger[0].Applied || result.PatchLedger[0].Reason == "" {
		t.Fatalf("unapproved selector must stop with a denied ledger entry: %+v", result)
	}
	if session.executeCalls != 1 || result.FailureDiagnostic == nil || result.FailureDiagnostic.Error.Code != "browser_agent_repair_policy_denied" {
		t.Fatalf("unapproved repair must not retry the action: session=%+v diagnostic=%+v", session, result.FailureDiagnostic)
	}
}

func TestOutlineRuntimeFullPathRecordsBusyPageWaitRepair(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.RecordingRunSpec.Outputs.FinalVideo = false
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	session := &busyWaitAuditBrowserAgentWorkerSession{}
	runner := localBrowserAgentOutlineRunner{
		service: &Service{},
		sessionFactory: func(_ context.Context, _ driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error) {
			return session, driver.BrowserAgentWorkerOpenResult{SessionID: "wait-audit", RuntimeVersions: map[string]string{"runner": "wait-audit"}}, nil
		},
	}
	result, err := newExecutionRuntimeRouter(&stubLegacyRuntimeRunner{}, runner).Run(context.Background(), executionRuntimeRequest{
		Package: &pkg, CloudJobID: "job_wait_audit", RecordingOutputDir: t.TempDir(), ResultCreatedAt: timeNowUTC(),
	})
	if err != nil {
		t.Fatalf("full outline runtime should package a bounded wait repair: %v", err)
	}
	if result.Status != model.RecordingResultStatusGenerated || len(result.PatchLedger) != 1 || !result.PatchLedger[0].Applied {
		t.Fatalf("wait repair ledger missing from result package: %+v", result)
	}
	entry := result.PatchLedger[0]
	if entry.Before != "wait_after_entry_at_least_1000ms" || entry.After != "wait_after_entry_at_least_2000ms" || entry.Attempt != 1 {
		t.Fatalf("wait repair must be bounded and auditable: %+v", entry)
	}
	if session.observeCalls != len(plan.Stages)+1 || session.executeCalls != len(plan.Stages) || result.StageEventLogRef == nil {
		t.Fatalf("wait repair did not re-observe before execution: session=%+v log=%+v", session, result.StageEventLogRef)
	}
	events := readStageEventAudit(t, result.StageEventLogRef.URI)
	if !hasStageEvent(events, model.StageExecutionEventRepairProposed, 1) || !hasStageEvent(events, model.StageExecutionEventRepairApplied, 2) || !hasStageEvent(events, model.StageExecutionEventTargetResolved, 2) {
		t.Fatalf("wait repair audit events are incomplete: %+v", events)
	}
}

func TestOutlineRuntimeCaptureTimingRepairRevalidatesWithoutReplayingAction(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.RecordingRunSpec.Outputs.FinalVideo = false
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	session := &stubBrowserAgentWorkerSession{}
	runner := localBrowserAgentOutlineRunner{
		service: &Service{}, outcomeVerifier: &captureTimingThenContinueVerifier{},
		sessionFactory: func(_ context.Context, _ driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error) {
			return session, driver.BrowserAgentWorkerOpenResult{SessionID: "capture-revalidate", RuntimeVersions: map[string]string{"runner": "capture-revalidate"}}, nil
		},
	}
	result, err := newExecutionRuntimeRouter(&stubLegacyRuntimeRunner{}, runner).Run(context.Background(), executionRuntimeRequest{
		Package: &pkg, CloudJobID: "job_capture_revalidate", RecordingOutputDir: t.TempDir(), ResultCreatedAt: timeNowUTC(),
	})
	if err != nil {
		t.Fatalf("capture timing repair should complete through non-action revalidation: %v", err)
	}
	if result.Status != model.RecordingResultStatusGenerated || len(result.PatchLedger) != 1 || !result.PatchLedger[0].Applied {
		t.Fatalf("capture timing repair was not packaged: %+v", result)
	}
	if session.executeCalls != len(plan.Stages) || session.revalidateCalls != 1 {
		t.Fatalf("capture timing repair replayed an action: execute=%d revalidate=%d", session.executeCalls, session.revalidateCalls)
	}
	events := readStageEventAudit(t, result.StageEventLogRef.URI)
	for _, event := range events {
		if event.Attempt == 2 && (event.EventType == model.StageExecutionEventActionStarted || event.EventType == model.StageExecutionEventActionCompleted) {
			t.Fatalf("capture timing repair must not emit a replayed action event: %+v", event)
		}
	}
	if !hasStageEvent(events, model.StageExecutionEventObservationCollected, 2) || !hasStageEvent(events, model.StageExecutionEventOutcomeObserved, 2) {
		t.Fatalf("capture timing revalidation evidence is missing: %+v", events)
	}
}

type repairAuditBrowserAgentWorkerSession struct {
	candidate    model.SelectorCandidate
	observeCalls int
	executeCalls int
}

func (s *repairAuditBrowserAgentWorkerSession) Observe(_ context.Context, stage driver.BrowserAgentWorkerStage) (driver.BrowserAgentWorkerStageResult, error) {
	s.observeCalls++
	artifact := stubBrowserAgentArtifact(stage.NodeID, "repair-before")
	if stage.NodeID == "node_invite_member" && stage.PreferredSelectorAlternative == nil {
		return driver.BrowserAgentWorkerStageResult{Observation: model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, Assertions: []model.RuntimeAssertion{{Kind: "target_resolved", Passed: false, Actual: "approved primary locator missing"}}}, EvidenceRefs: []model.EvidenceRef{{ID: "evidence_" + artifact.ID, Kind: model.EvidenceKindWebScreenshot, ArtifactID: artifact.ID, Confidence: 1}}, Artifacts: []model.ArtifactRef{artifact}, PreferredSelectorAlternative: &s.candidate}, nil
	}
	return driver.BrowserAgentWorkerStageResult{Observation: model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, Assertions: []model.RuntimeAssertion{{Kind: "target_resolved", Passed: true, Actual: "approved live target"}}}, EvidenceRefs: []model.EvidenceRef{{ID: "evidence_" + artifact.ID, Kind: model.EvidenceKindWebScreenshot, ArtifactID: artifact.ID, Confidence: 1}}, Artifacts: []model.ArtifactRef{artifact}, TargetResolved: true}, nil
}

func (s *repairAuditBrowserAgentWorkerSession) Execute(_ context.Context, stage driver.BrowserAgentWorkerStage) (driver.BrowserAgentWorkerStageResult, error) {
	s.executeCalls++
	artifact := stubBrowserAgentArtifact(stage.NodeID, "repair-after")
	assertions := []model.RuntimeAssertion{{Kind: "action_completed", Passed: true, Actual: stage.TargetContract.SemanticID}}
	for _, validation := range stage.Validations {
		if validation.Required {
			assertions = append(assertions, model.RuntimeAssertion{Kind: "required_" + validation.Kind + ":" + validation.ID, Passed: true, Actual: "matched"})
		}
	}
	return driver.BrowserAgentWorkerStageResult{Observation: model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: assertions}, EvidenceRefs: []model.EvidenceRef{{ID: "evidence_" + artifact.ID, Kind: model.EvidenceKindWebScreenshot, ArtifactID: artifact.ID, Confidence: 1}}, Artifacts: []model.ArtifactRef{artifact}, TargetResolved: true}, nil
}

func (s *repairAuditBrowserAgentWorkerSession) Close(context.Context) (driver.BrowserAgentWorkerCloseResult, error) {
	return driver.BrowserAgentWorkerCloseResult{Artifacts: []model.ArtifactRef{{ID: "repair_trace", Kind: "browser_trace", URI: "file:///repair-trace.zip", SHA256: "repair_trace_hash", SizeBytes: 10, Sensitive: true}}}, nil
}
func (s *repairAuditBrowserAgentWorkerSession) Abort() error { return nil }

type busyWaitAuditBrowserAgentWorkerSession struct {
	observeCalls int
	executeCalls int
}

func (s *busyWaitAuditBrowserAgentWorkerSession) Observe(_ context.Context, stage driver.BrowserAgentWorkerStage) (driver.BrowserAgentWorkerStageResult, error) {
	s.observeCalls++
	artifact := stubBrowserAgentArtifact(stage.NodeID, "wait-before")
	if stage.NodeID == "node_invite_member" && !containsExact(stage.WaitConditions, "wait_after_entry_at_least_2000ms") {
		return driver.BrowserAgentWorkerStageResult{Observation: model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, Assertions: []model.RuntimeAssertion{{Kind: "target_resolved", Passed: false, Actual: "live page busy"}}}, EvidenceRefs: []model.EvidenceRef{{ID: "evidence_" + artifact.ID, Kind: model.EvidenceKindWebScreenshot, ArtifactID: artifact.ID, Confidence: 1}}, Artifacts: []model.ArtifactRef{artifact}, SuggestedWaitCondition: "wait_after_entry_at_least_2000ms"}, nil
	}
	return driver.BrowserAgentWorkerStageResult{Observation: model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, Assertions: []model.RuntimeAssertion{{Kind: "target_resolved", Passed: true, Actual: "live target after bounded wait"}}}, EvidenceRefs: []model.EvidenceRef{{ID: "evidence_" + artifact.ID, Kind: model.EvidenceKindWebScreenshot, ArtifactID: artifact.ID, Confidence: 1}}, Artifacts: []model.ArtifactRef{artifact}, TargetResolved: true}, nil
}

func (s *busyWaitAuditBrowserAgentWorkerSession) Execute(_ context.Context, stage driver.BrowserAgentWorkerStage) (driver.BrowserAgentWorkerStageResult, error) {
	s.executeCalls++
	artifact := stubBrowserAgentArtifact(stage.NodeID, "wait-after")
	assertions := []model.RuntimeAssertion{{Kind: "action_completed", Passed: true, Actual: stage.TargetContract.SemanticID}}
	for _, validation := range stage.Validations {
		if validation.Required {
			assertions = append(assertions, model.RuntimeAssertion{Kind: "required_" + validation.Kind + ":" + validation.ID, Passed: true, Actual: "matched"})
		}
	}
	return driver.BrowserAgentWorkerStageResult{Observation: model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: assertions}, EvidenceRefs: []model.EvidenceRef{{ID: "evidence_" + artifact.ID, Kind: model.EvidenceKindWebScreenshot, ArtifactID: artifact.ID, Confidence: 1}}, Artifacts: []model.ArtifactRef{artifact}, TargetResolved: true}, nil
}

func (s *busyWaitAuditBrowserAgentWorkerSession) Close(context.Context) (driver.BrowserAgentWorkerCloseResult, error) {
	return driver.BrowserAgentWorkerCloseResult{Artifacts: []model.ArtifactRef{{ID: "wait_trace", Kind: "browser_trace", URI: "file:///wait-trace.zip", SHA256: "wait_trace_hash", SizeBytes: 10, Sensitive: true}}}, nil
}
func (s *busyWaitAuditBrowserAgentWorkerSession) Abort() error { return nil }

func readStageEventAudit(t *testing.T, uri string) []model.StageExecutionEvent {
	t.Helper()
	path, err := directWorkerLocalPath(uri)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	events := []model.StageExecutionEvent{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event model.StageExecutionEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func hasStageEvent(events []model.StageExecutionEvent, eventType model.StageExecutionEventType, attempt int) bool {
	for _, event := range events {
		if event.EventType == eventType && event.Attempt == attempt {
			return true
		}
	}
	return false
}

type stubBrowserAgentWorkerSession struct {
	observeCalls            int
	executeCalls            int
	credentialExecuteCalls  int
	credentialSecretMatched bool
	revalidateCalls         int
	closeCalls              int
	recordingPath           string
	failNodeID              string
	closeErr                error
}

func TestBrowserAgentStageUsesCredentialBrokerWithoutEmbeddingSecretInStage(t *testing.T) {
	const (
		secretRef = "vault://approved/login"
		secret    = "credential-value-for-test"
	)
	broker, err := newOneTimeBrowserAgentCredentialBroker(secretRef, secret)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Destroy()
	session := &stubBrowserAgentWorkerSession{}
	runtime := &localBrowserAgentStageRuntime{session: session, credentialResolver: broker, stageCount: 1, artifacts: map[string]model.ArtifactRef{}}
	stage := BrowserAgentRuntimeStage{
		ID: "stage_login", Order: 1, NodeID: "node_login",
		TargetContract: model.BrowserAgentTargetContract{SemanticID: "login_password", Destructive: false},
		Interactions:   []model.BrowserAgentInteraction{{Kind: model.GraphActionFill, SecretRef: secretRef, NonDestructive: true}},
	}
	result, err := runtime.ExecuteStage(context.Background(), BrowserAgentRuntimePlan{}, stage)
	if err != nil || result.Observation == nil {
		t.Fatalf("credential broker execution failed: result=%+v err=%v", result, err)
	}
	if session.credentialExecuteCalls != 1 || session.executeCalls != 1 || !session.credentialSecretMatched {
		t.Fatalf("credential was not delivered through the narrow worker method: %+v", session)
	}
	if stage.Interactions[0].Value != "" {
		t.Fatal("resolved credential must not be copied into the approved runtime stage")
	}
}

func TestManualSessionCheckpointRevalidatesWithoutExecutingCredentialAction(t *testing.T) {
	session := &stubBrowserAgentWorkerSession{}
	runtime := &localBrowserAgentStageRuntime{session: session, stageCount: 1, artifacts: map[string]model.ArtifactRef{}}
	stage := BrowserAgentRuntimeStage{
		ID: "stage_session", Order: 1, NodeID: "node_session", StageKind: model.BusinessStageKindSessionSetup,
		ManualSessionCheckpoint: true, TargetContract: model.BrowserAgentTargetContract{SemanticID: "session_target"},
		Interactions: []model.BrowserAgentInteraction{{Kind: model.GraphActionFill, SecretRef: "secret://password"}},
		Validations:  []model.ValidationSpec{{ID: "validate_app", Kind: "url_matches", Required: true}},
	}
	observed, err := runtime.ObserveStage(context.Background(), BrowserAgentRuntimePlan{}, stage)
	if err != nil || !observed.TargetResolved {
		t.Fatalf("manual checkpoint observation failed: %+v, %v", observed, err)
	}
	result, err := runtime.ExecuteStage(context.Background(), BrowserAgentRuntimePlan{}, stage)
	if err != nil || result.Observation == nil {
		t.Fatalf("manual checkpoint execution failed: %+v, %v", result, err)
	}
	if session.executeCalls != 0 || session.observeCalls != 0 || session.revalidateCalls != 2 {
		t.Fatalf("manual checkpoint must only perform non-action revalidation: %+v", session)
	}
	found := false
	for _, assertion := range result.Observation.Assertions {
		if assertion.Kind == "manual_session_checkpoint_verified" && assertion.Passed {
			found = true
		}
	}
	if !found {
		t.Fatalf("manual checkpoint evidence marker is missing: %+v", result.Observation.Assertions)
	}
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
	if stage.NodeID == s.failNodeID {
		return driver.BrowserAgentWorkerStageResult{}, errors.New("synthetic target resolution failure")
	}
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

func (s *stubBrowserAgentWorkerSession) ExecuteWithSecrets(ctx context.Context, stage driver.BrowserAgentWorkerStage, secrets map[string]string) (driver.BrowserAgentWorkerStageResult, error) {
	s.credentialExecuteCalls++
	for _, interaction := range stage.Interactions {
		if interaction.SecretRef != "" && secrets[interaction.SecretRef] == "credential-value-for-test" {
			s.credentialSecretMatched = true
		}
	}
	return s.Execute(ctx, stage)
}

func (s *stubBrowserAgentWorkerSession) Revalidate(_ context.Context, stage driver.BrowserAgentWorkerStage) (driver.BrowserAgentWorkerStageResult, error) {
	s.revalidateCalls++
	artifact := stubBrowserAgentArtifact(stage.NodeID, "revalidate")
	assertions := []model.RuntimeAssertion{{Kind: "capture_ready", Passed: true, Actual: stage.TargetContract.SemanticID}}
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
	if s.closeErr != nil {
		return driver.BrowserAgentWorkerCloseResult{}, s.closeErr
	}
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

func (stubBrowserAgentRenderService) ProbeMedia(_ context.Context, request executor.MediaProbeRequest) (executor.MediaProbeResult, error) {
	// The browser evidence master is captured with the canonical 2K viewport,
	// while its encoded recording evidence is normalized to the 1080p profile.
	width, height := 2560, 1440
	if filepath.Base(request.Path) == "recording.webm" {
		width, height = 1920, 1080
	}
	return executor.MediaProbeResult{Path: request.Path, Format: "mov,mp4", DurationMS: 1000, VideoCodec: "h264", Width: width, Height: height, FPS: 30, PixelFormat: "yuv420p", FFProbeAvailable: true}, nil
}

func (s *stubBrowserAgentWorkerSession) Abort() error { return nil }

func stubBrowserAgentArtifact(nodeID, phase string) model.ArtifactRef {
	return model.ArtifactRef{
		ID: "artifact_" + nodeID + "_" + phase, Kind: "screenshot", URI: "file:///" + nodeID + "-" + phase + ".png",
		MimeType: "image/png", SHA256: "sha256_" + nodeID + "_" + phase, SizeBytes: 10, Sensitive: true,
		SourceNodeID: nodeID, Metadata: map[string]any{"include_in_demo": phase == "after" || phase == "revalidate"},
	}
}

func TestDeterministicBrowserAgentVerifierPreExecutionReportsContractMissing(t *testing.T) {
	report, err := newDeterministicBrowserAgentStageVerifier(nil).ValidateBeforeExecution(
		context.Background(),
		model.BrowserAgentValidationContext{
			RunID: "run_contract_missing", SourcePackageID: "pkg_1",
			SourceBundleHashSHA256: "bundle_hash", EffectivePolicyHashSHA256: "policy_hash",
			StageApprovalPlan: &model.StageApprovalPlan{Stages: []model.StageApprovalStage{{NodeID: "node_1"}}},
			ScriptOutline:     &model.BrowserAgentScriptOutline{Stages: []model.BrowserAgentOutlineStage{{NodeID: "node_1"}}},
			// BrowserAgentContract deliberately nil → structurePassed=false → approved_contract_missing
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Fatalf("expected stop_and_report when BrowserAgentContract is nil, got %q", report.Decision)
	}
	if !validationReportHasCheckCode(report, "approved_contract_missing", false) {
		t.Fatalf("expected approved_contract_missing check with Passed=false; report: %+v", report)
	}
	for _, check := range report.Checks {
		if check.Code == "approved_contract_missing" {
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("approved_contract_missing severity = %v, want Blocking", check.Severity)
			}
			if !check.Required {
				t.Errorf("approved_contract_missing Required = false, want true")
			}
		}
	}
}

func TestDeterministicBrowserAgentVerifierPreExecutionReportsStagePlanEmpty(t *testing.T) {
	report, err := newDeterministicBrowserAgentStageVerifier(nil).ValidateBeforeExecution(
		context.Background(),
		model.BrowserAgentValidationContext{
			RunID: "run_stage_plan_empty", SourcePackageID: "pkg_1",
			SourceBundleHashSHA256: "bundle_hash", EffectivePolicyHashSHA256: "policy_hash",
			// All three non-nil so structurePassed=true, but Stages empty → stagePlanPassed=false
			StageApprovalPlan:    &model.StageApprovalPlan{Stages: []model.StageApprovalStage{}},
			ScriptOutline:        &model.BrowserAgentScriptOutline{Stages: []model.BrowserAgentOutlineStage{}},
			BrowserAgentContract: &model.BrowserAgentContract{ID: "contract_1"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Fatalf("expected stop_and_report when stage plan has no stages, got %q", report.Decision)
	}
	if !validationReportHasCheckCode(report, "approved_stage_plan_empty", false) {
		t.Fatalf("expected approved_stage_plan_empty check with Passed=false; report: %+v", report)
	}
	for _, check := range report.Checks {
		if check.Code == "approved_stage_plan_empty" {
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("approved_stage_plan_empty severity = %v, want Blocking", check.Severity)
			}
			if !check.Required {
				t.Errorf("approved_stage_plan_empty Required = false, want true")
			}
		}
	}
}

func TestDeterministicBrowserAgentPostVerifierReportsObservedStageCompletionMissing(t *testing.T) {
	verifier := deterministicBrowserAgentStageVerifier{}
	now := timeNowUTC()
	vctx := model.BrowserAgentValidationContext{
		RunID: "run_completion_missing", SourcePackageID: "pkg_1",
		SourceBundleHashSHA256: "bundle_hash", EffectivePolicyHashSHA256: "policy_hash",
	}
	// OutcomeObserved with ActualBrowser (hasObservation=true) but NO StageCompleted event
	// → completed["node_1"]=false → completionPassed=false
	events := []model.StageExecutionEvent{
		{
			SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_1",
			RunID: "run_completion_missing", SourcePackageID: "pkg_1",
			SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
			NodeID: "node_1", StageID: "stage_1", Attempt: 1, Sequence: 1,
			EventType:  model.StageExecutionEventOutcomeObserved,
			OccurredAt: now,
			Observation: &model.RuntimeObservation{
				Source:     model.RuntimeObservationActualBrowser,
				URL:        "https://app.example.com/dashboard",
				Assertions: []model.RuntimeAssertion{{Kind: "action_completed", Passed: true}},
			},
			EvidenceRefs: []model.EvidenceRef{{ID: "evidence_1", Kind: model.EvidenceKindWebScreenshot}},
		},
		// StageCompleted intentionally omitted
	}
	result := model.RecordingResultPackage{
		SourcePackageID: "pkg_1",
		StepResults: []model.StepResult{{
			NodeID:        "node_1",
			Status:        "passed",
			ObservedState: "source=browser url=https://app.example.com/dashboard",
			Artifacts:     []model.ArtifactRef{{ID: "artifact_1", SourceNodeID: "node_1"}},
		}},
		ValidationReports: []model.ValidationReport{{
			SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "report_pre",
			RunID: "run_completion_missing", SourcePackageID: "pkg_1",
			SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
			Phase: model.ValidationPhaseRuntimeStage, NodeID: "node_1",
			Decision: model.ValidationDecisionContinue, PassRate: 1, OverallConfidence: 1,
			EvidenceQuality: model.RuntimeObservationActualBrowser,
			EvidenceRefs:    []model.EvidenceRef{{ID: "evidence_1", Kind: model.EvidenceKindWebScreenshot}},
			CreatedAt:       now,
		}},
	}
	report, err := verifier.ValidatePostExecution(context.Background(), vctx, result, events)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Fatalf("expected stop_and_report when stage completion event missing, got %q", report.Decision)
	}
	if !validationReportHasCheckCode(report, "observed_stage_completion_missing", false) {
		t.Fatalf("expected observed_stage_completion_missing check with Passed=false; report: %+v", report)
	}
	for _, check := range report.Checks {
		if check.Code == "observed_stage_completion_missing" {
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("observed_stage_completion_missing severity = %v, want Blocking", check.Severity)
			}
			if !check.Required {
				t.Errorf("observed_stage_completion_missing Required = false, want true")
			}
		}
	}
}

func TestDeterministicBrowserAgentPostVerifierReportsEvidenceRefsMissing(t *testing.T) {
	verifier := deterministicBrowserAgentStageVerifier{}
	now := timeNowUTC()
	vctx := model.BrowserAgentValidationContext{
		RunID: "run_evidence_missing", SourcePackageID: "pkg_1",
		SourceBundleHashSHA256: "bundle_hash", EffectivePolicyHashSHA256: "policy_hash",
	}
	// StageCompleted + OutcomeObserved with ActualBrowser (hasObservation=true, completionPassed=true)
	// but OutcomeObserved has NO EvidenceRefs and StepResult has no Artifacts
	// → nodeEvidence=[] and Artifacts=[] → evidencePassed=false
	events := []model.StageExecutionEvent{
		{
			SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_1",
			RunID: "run_evidence_missing", SourcePackageID: "pkg_1",
			SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
			NodeID: "node_1", StageID: "stage_1", Attempt: 1, Sequence: 1,
			EventType:  model.StageExecutionEventOutcomeObserved,
			OccurredAt: now,
			Observation: &model.RuntimeObservation{
				Source:     model.RuntimeObservationActualBrowser,
				URL:        "https://app.example.com/dashboard",
				Assertions: []model.RuntimeAssertion{{Kind: "action_completed", Passed: true}},
			},
			EvidenceRefs: []model.EvidenceRef{}, // no evidence refs
		},
		{
			SchemaVersion: model.StageExecutionEventSchemaVersion, EventID: "event_2",
			RunID: "run_evidence_missing", SourcePackageID: "pkg_1",
			SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
			NodeID: "node_1", StageID: "stage_1", Attempt: 1, Sequence: 2,
			EventType:    model.StageExecutionEventStageCompleted,
			OccurredAt:   now.Add(time.Second),
			EvidenceRefs: []model.EvidenceRef{},
		},
	}
	result := model.RecordingResultPackage{
		SourcePackageID: "pkg_1",
		StepResults: []model.StepResult{{
			NodeID:        "node_1",
			Status:        "passed",
			ObservedState: "source=browser url=https://app.example.com/dashboard",
			Artifacts:     []model.ArtifactRef{}, // no artifacts
		}},
		ValidationReports: []model.ValidationReport{{
			SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "report_runtime",
			RunID: "run_evidence_missing", SourcePackageID: "pkg_1",
			SourceBundleHashSHA256: "bundle_hash", PolicyHashSHA256: "policy_hash",
			Phase: model.ValidationPhaseRuntimeStage, NodeID: "node_1",
			Decision: model.ValidationDecisionContinue, PassRate: 1, OverallConfidence: 1,
			EvidenceQuality: model.RuntimeObservationActualBrowser,
			EvidenceRefs:    []model.EvidenceRef{{ID: "runtime_report_ev", Kind: model.EvidenceKindWebScreenshot}},
			CreatedAt:       now,
		}},
	}
	report, err := verifier.ValidatePostExecution(context.Background(), vctx, result, events)
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != model.ValidationDecisionStopAndReport {
		t.Fatalf("expected stop_and_report when evidence refs are missing, got %q", report.Decision)
	}
	if !validationReportHasCheckCode(report, "evidence_refs_missing", false) {
		t.Fatalf("expected evidence_refs_missing check with Passed=false; report: %+v", report)
	}
	for _, check := range report.Checks {
		if check.Code == "evidence_refs_missing" {
			if check.Severity != model.FindingSeverityBlocking {
				t.Errorf("evidence_refs_missing severity = %v, want Blocking", check.Severity)
			}
			if !check.Required {
				t.Errorf("evidence_refs_missing Required = false, want true")
			}
		}
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
