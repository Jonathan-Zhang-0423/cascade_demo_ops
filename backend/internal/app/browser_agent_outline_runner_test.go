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
	"cascade-demoops/backend/internal/model"
)

func TestLocalBrowserAgentOutlineRunnerBuildsAuditableResultPackage(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	// Rendering is covered by the end-to-end protocol acceptance test. This
	// focused runner test uses a stub browser session and verifies orchestration.
	pkg.RecordingRunSpec.Outputs.FinalVideo = false
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	session := &stubBrowserAgentWorkerSession{}
	var openRequest driver.BrowserAgentWorkerOpenRequest
	runner := localBrowserAgentOutlineRunner{
		service: &Service{},
		sessionFactory: func(_ context.Context, request driver.BrowserAgentWorkerOpenRequest) (browserAgentWorkerSession, driver.BrowserAgentWorkerOpenResult, error) {
			openRequest = request
			return session, driver.BrowserAgentWorkerOpenResult{SessionID: request.SessionID, RuntimeVersions: map[string]string{"runner": "stub-browser-agent"}}, nil
		},
	}
	var progressStages []string
	result, err := runner.Run(context.Background(), BrowserAgentOutlineRunRequest{
		Package: &pkg, RuntimePlan: plan, CloudJobID: "job_outline_result",
		RecordingOutputDir: t.TempDir(), ResultCreatedAt: time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC),
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
	for _, required := range []string{"validating_pre_execution", "running_browser_agent", "validating_runtime_stage", "validating_post_execution", "packaging_recording"} {
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

type postStoppingBrowserAgentVerifier struct{ deterministicBrowserAgentStageVerifier }

func (v postStoppingBrowserAgentVerifier) ValidateBeforeExecution(ctx context.Context, validationContext BrowserAgentValidationContext) (model.ValidationReport, error) {
	return v.deterministicBrowserAgentStageVerifier.ValidateBeforeExecution(ctx, validationContext)
}

func (v postStoppingBrowserAgentVerifier) ValidatePostExecution(_ context.Context, validationContext BrowserAgentValidationContext, _ model.RecordingResultPackage, _ []model.StageExecutionEvent) (model.ValidationReport, error) {
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
	path := strings.TrimPrefix(uri, "file://")
	path = filepath.FromSlash(strings.ReplaceAll(path, "%5C", "\\"))
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
	observeCalls int
	executeCalls int
	closeCalls   int
	failNodeID   string
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

func (s *stubBrowserAgentWorkerSession) Close(context.Context) (driver.BrowserAgentWorkerCloseResult, error) {
	s.closeCalls++
	return driver.BrowserAgentWorkerCloseResult{
		Artifacts:       []model.ArtifactRef{{ID: "artifact_trace", Kind: "browser_trace", URI: "file:///trace.zip", SHA256: "trace_hash", SizeBytes: 10, Sensitive: true}},
		RuntimeVersions: map[string]string{"browser": "chromium"},
	}, nil
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
