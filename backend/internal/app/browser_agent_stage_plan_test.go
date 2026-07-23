package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestCompileBrowserAgentRuntimePlanKeepsApprovedStageOrderAndSemantics(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Stages) != 2 || plan.Stages[0].NodeID != "node_open_dashboard" || plan.Stages[1].NodeID != "node_invite_member" {
		t.Fatalf("unexpected compiled stage order: %+v", plan.Stages)
	}
	if plan.Stages[1].Interactions[0].Kind != model.GraphActionClick || plan.Stages[1].TargetContract.SemanticID == "" {
		t.Fatalf("compiled stage lost approved action or target contract: %+v", plan.Stages[1])
	}
	if plan.Stages[0].DurationMS != 10000 {
		t.Fatalf("explicit approved duration was not preserved: %+v", plan.Stages[0])
	}
	if len(plan.Stages[0].Validations) == 0 || !plan.Stages[0].Validations[0].Required {
		t.Fatalf("compiled stage lost required outcome validations: %+v", plan.Stages[0])
	}
}

func TestCompileBrowserAgentRuntimePlanRejectsImmutableConflicts(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.ExecutableScriptBundle.ScriptOutline.Stages[1].Objective = "Delete the workspace"
	if _, err := compileBrowserAgentRuntimePlan(&pkg); runtimeExecutionErrorCode(err) != runtimeErrorBrowserAgentContractViolation {
		t.Fatalf("objective conflict must be blocked, got %q: %v", runtimeExecutionErrorCode(err), err)
	}

	pkg = readBrowserAgentOutlineFixture(t)
	pkg.ExecutableScriptBundle.ScriptOutline.Stages[0].Order = 2
	if _, err := compileBrowserAgentRuntimePlan(&pkg); runtimeExecutionErrorCode(err) != runtimeErrorBrowserAgentContractViolation {
		t.Fatalf("stage reorder must be blocked, got %q: %v", runtimeExecutionErrorCode(err), err)
	}

	pkg = readBrowserAgentOutlineFixture(t)
	pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[1].Interaction.Value = "approved@example.com"
	pkg.ExecutableScriptBundle.ScriptOutline.Stages[1].Interactions[0].Value = "other@example.com"
	if _, err := compileBrowserAgentRuntimePlan(&pkg); runtimeExecutionErrorCode(err) != runtimeErrorBrowserAgentContractViolation {
		t.Fatalf("input semantic change must be blocked, got %q: %v", runtimeExecutionErrorCode(err), err)
	}

	pkg = readBrowserAgentOutlineFixture(t)
	pkg.ExecutableScriptBundle.ScriptOutline.Stages[1].Interactions = append(pkg.ExecutableScriptBundle.ScriptOutline.Stages[1].Interactions, model.BrowserAgentInteraction{Kind: model.GraphActionUpload, NonDestructive: true})
	if _, err := compileBrowserAgentRuntimePlan(&pkg); runtimeExecutionErrorCode(err) != runtimeErrorBrowserAgentContractViolation {
		t.Fatalf("unapproved action insertion must be blocked, got %q: %v", runtimeExecutionErrorCode(err), err)
	}
}

func TestBrowserAgentPolicyGuardBlocksDomainControlPlaneAndDestructiveActions(t *testing.T) {
	plan, err := compileBrowserAgentRuntimePlan(ptrBrowserAgentPackage(readBrowserAgentOutlineFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	guard := contractBrowserAgentPolicyGuard{}
	base := BrowserAgentActionIntent{
		NodeID: plan.Stages[0].NodeID, StageID: plan.Stages[0].ID, ActionType: model.GraphActionNavigate,
		URL: "https://app.example.com/dashboard", NonDestructive: true, TargetContract: plan.Stages[0].TargetContract,
	}
	if decision := guard.Authorize(plan, base); !decision.Allowed {
		t.Fatalf("approved same-domain action was denied: %+v", decision)
	}
	outside := base
	outside.URL = "https://evil.example.net/dashboard"
	if decision := guard.Authorize(plan, outside); decision.Allowed || decision.Code != "domain_not_allowed" {
		t.Fatalf("outside domain was not blocked: %+v", decision)
	}
	controlPlane := base
	controlPlane.URL = "https://app.example.com/v1/execution-packages"
	if decision := guard.Authorize(plan, controlPlane); decision.Allowed || decision.Code != "forbidden_page" {
		t.Fatalf("control-plane route was not blocked: %+v", decision)
	}
	destructive := base
	destructive.TargetContract.Destructive = true
	if decision := guard.Authorize(plan, destructive); decision.Allowed || decision.Code != "destructive_action_denied" {
		t.Fatalf("destructive action was not blocked: %+v", decision)
	}
}

func TestBrowserAgentStageOrchestratorPreflightsEveryInteraction(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	orchestrator := newBrowserAgentStageOrchestrator(contractBrowserAgentPolicyGuard{})
	plan, err := orchestrator.Prepare(&pkg)
	if err != nil || len(plan.Stages) != 2 {
		t.Fatalf("approved outline did not pass preflight: plan=%+v err=%v", plan, err)
	}

	pkg = readBrowserAgentOutlineFixture(t)
	pkg.ExecutableScriptBundle.ScriptOutline.Stages[0].URL = "https://app.example.com/aigc/v1/execution-packages"
	if _, err := orchestrator.Prepare(&pkg); runtimeExecutionErrorCode(err) != runtimeErrorBrowserAgentPolicyDenied {
		t.Fatalf("forbidden runtime route must fail policy preflight, got %q: %v", runtimeExecutionErrorCode(err), err)
	}
}

func TestBrowserAgentStageOrchestratorEmitsOrderedEventsAndStopsOnFailure(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	orchestrator := newBrowserAgentStageOrchestrator(contractBrowserAgentPolicyGuard{})
	plan, err := orchestrator.Prepare(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	sink := &memoryStageEventSink{}
	executor := &stubStageExecutor{failNodeID: plan.Stages[1].NodeID}
	result, err := orchestrator.Run(context.Background(), plan, stubStageObserver{}, executor, sink)
	if runtimeExecutionErrorCode(err) != "browser_agent_action_failed" {
		t.Fatalf("expected stage action failure, got %q: %v", runtimeExecutionErrorCode(err), err)
	}
	if len(result.Events) != 12 || len(sink.events) != 12 {
		t.Fatalf("expected first stage and failed second-stage events, got %+v", result.Events)
	}
	for index, event := range result.Events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("event sequence is not monotonic: %+v", result.Events)
		}
	}
	if result.Events[len(result.Events)-1].EventType != model.StageExecutionEventStageFailed {
		t.Fatalf("last event must record stage failure: %+v", result.Events[len(result.Events)-1])
	}
}

func TestBrowserAgentStageOrchestratorStopsWhenOutcomeVerifierBlocks(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	verifier := &stubStageVerifier{decision: model.ValidationDecisionStopAndReport}
	orchestrator := newBrowserAgentStageOrchestratorWithVerifier(contractBrowserAgentPolicyGuard{}, verifier)
	plan, err := orchestrator.Prepare(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	result, err := orchestrator.Run(context.Background(), plan, stubStageObserver{}, &stubStageExecutor{}, &memoryStageEventSink{})
	if runtimeExecutionErrorCode(err) != "outcome_verification_failed" || verifier.calls != 1 {
		t.Fatalf("verifier stop decision was not enforced: events=%d calls=%d code=%q err=%v", len(result.Events), verifier.calls, runtimeExecutionErrorCode(err), err)
	}
	if len(result.Events) != 7 || result.Events[len(result.Events)-1].EventType != model.StageExecutionEventStageFailed {
		t.Fatalf("second stage must not start after verifier stop, got %+v", result.Events)
	}
}

type stubStageObserver struct{}

func (stubStageObserver) ObserveStage(context.Context, BrowserAgentRuntimePlan, BrowserAgentRuntimeStage) (BrowserAgentStageObservation, error) {
	return BrowserAgentStageObservation{
		Observation:    model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, Title: "Dashboard"},
		EvidenceRefs:   []model.EvidenceRef{{ID: "observation_1", Kind: model.EvidenceKindBrowserTrace}},
		TargetResolved: true,
	}, nil
}

type stubStageExecutor struct{ failNodeID string }

func (e *stubStageExecutor) ExecuteStage(_ context.Context, _ BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error) {
	if stage.NodeID == e.failNodeID {
		return BrowserAgentStageActionResult{}, errors.New("synthetic action failure")
	}
	return BrowserAgentStageActionResult{
		Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "element_visible", Passed: true}}},
		EvidenceRefs: []model.EvidenceRef{{ID: "action_1", Kind: model.EvidenceKindBrowserTrace}},
	}, nil
}

type memoryStageEventSink struct{ events []model.StageExecutionEvent }

func (s *memoryStageEventSink) Append(_ context.Context, event model.StageExecutionEvent) error {
	s.events = append(s.events, event)
	return nil
}

type stubStageVerifier struct {
	decision model.ValidationDecision
	calls    int
}

func (v *stubStageVerifier) ValidateStageEvents(_ context.Context, context BrowserAgentValidationContext, events []model.StageExecutionEvent) (model.ValidationReport, error) {
	v.calls++
	last := events[len(events)-1]
	return model.ValidationReport{
		SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "report_1", RunID: last.RunID,
		SourcePackageID: context.SourcePackageID, SourceBundleHashSHA256: context.SourceBundleHashSHA256,
		PolicyHashSHA256: context.EffectivePolicyHashSHA256, Phase: model.ValidationPhaseRuntimeStage,
		NodeID: last.NodeID, StageID: last.StageID, Decision: v.decision, PassRate: 1, OverallConfidence: .9,
		EvidenceQuality: model.RuntimeObservationActualBrowser, EvidenceRefs: []model.EvidenceRef{{ID: "verification_1"}},
		CreatedAt: timeNowUTC(),
	}, nil
}

func ptrBrowserAgentPackage(pkg model.ClientExecutionPackage) *model.ClientExecutionPackage {
	return &pkg
}

func readBrowserAgentOutlineFixture(t *testing.T) model.ClientExecutionPackage {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve fixture path")
	}
	path := filepath.Join(filepath.Dir(current), "..", "..", "..", "contracts", "exchange", "v1", "client_execution_package.browser_agent_outline.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pkg model.ClientExecutionPackage
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatal(err)
	}
	return pkg
}
