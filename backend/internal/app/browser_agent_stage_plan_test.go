package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestAdaptiveHarnessReplaysJobE1DAsOneSubmitAndSkipsStageFive(t *testing.T) {
	file, err := os.Open(filepath.Join("testdata", "adaptive-harness", "job-e1d-stage-3-5-replay.jsonl"))
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
	modeStage := BrowserAgentRuntimeStage{
		ID: "stage_step_04_business_stage_select_build_mode", Order: 4, NodeID: "business_stage_select_build_mode",
		StageKind: model.BusinessStageKindModeSelection, EntryRoute: "https://product.example/workspace",
		InteractionContract: &model.InteractionContract{ReplayPolicy: model.InteractionReplayIdempotentWrite},
	}
	submitStage := BrowserAgentRuntimeStage{
		ID: "stage_step_05_business_stage_start_agent_build", Order: 5, NodeID: "business_stage_start_agent_build",
		StageKind: model.BusinessStageKindBusinessSubmit, EntryRoute: "https://product.example/workspace",
		InteractionContract: &model.InteractionContract{ReplayPolicy: model.InteractionReplayOnceEffect},
	}
	plan := BrowserAgentRuntimePlan{
		RunID: "run_replay", SourcePackageID: "pkg_replay", SourceBundleHashSHA256: "bundle-replay", PolicyHashSHA256: "policy-replay",
		HarnessProfile: model.AdaptiveBusinessHarnessProfileV1, Stages: []BrowserAgentRuntimeStage{modeStage, submitStage},
	}
	absorbed := historicalAdaptiveAbsorptions(plan, events)
	checkpoint := absorbed[submitStage.ID]
	if checkpoint == nil || checkpoint.EntityRef != "https://product.example/entity/alpha" || !checkpoint.ActionOccurred {
		t.Fatalf("stage four was not reclassified as the real submit effect: %+v", checkpoint)
	}
	sink := &memoryStageEventSink{events: events}
	executor := &resumableStageExecutor{revalidatePass: true}
	result, err := newBrowserAgentStageOrchestrator(contractBrowserAgentPolicyGuard{}).Run(context.Background(), plan, unresolvedStageObserver{}, executor, sink)
	if err != nil {
		t.Fatal(err)
	}
	if executor.executeCalls != 0 {
		t.Fatalf("replay must perform zero new clicks, got %d actions", executor.executeCalls)
	}
	foundSkip, foundAbsorption := false, false
	for _, event := range result.Events {
		foundSkip = foundSkip || event.StageID == submitStage.ID && event.EventType == model.StageExecutionEventStepSatisfied
		foundAbsorption = foundAbsorption || event.StageID == submitStage.ID && event.EventType == model.StageExecutionEventTransitionAbsorbed
	}
	if !foundSkip || !foundAbsorption {
		t.Fatalf("stage five was not auditably skipped by successor observation: %+v", result.Events)
	}
}

func TestAdaptiveHarnessPersistsOptionalModeSkipWhenControlIsAbsent(t *testing.T) {
	stage := BrowserAgentRuntimeStage{
		ID: "stage_optional_mode", Order: 1, NodeID: "node_optional_mode",
		StageKind: model.BusinessStageKindModeSelection, EntryRoute: "https://product.example/app",
		TargetContract: model.BrowserAgentTargetContract{SemanticID: "optional_mode", Destructive: false, Confidence: .76},
		Interactions:   []model.BrowserAgentInteraction{{Kind: model.GraphActionClick, NonDestructive: true}},
	}
	plan := BrowserAgentRuntimePlan{
		RunID: "run_optional_mode", SourcePackageID: "pkg_optional_mode",
		SourceBundleHashSHA256: "bundle_optional_mode", PolicyHashSHA256: "policy_optional_mode",
		HarnessProfile: model.AdaptiveBusinessHarnessProfileV1, Stages: []BrowserAgentRuntimeStage{stage},
	}
	observer := unresolvedOptionalModeObserver{}
	executor := &countingStageExecutor{}
	sink, err := newStageEventAuditLog(t.TempDir(), "job_optional_mode")
	if err != nil {
		t.Fatal(err)
	}
	result, err := newBrowserAgentStageOrchestrator(contractBrowserAgentPolicyGuard{}).Run(context.Background(), plan, observer, executor, sink)
	if err != nil {
		t.Fatal(err)
	}
	if result.AuditError != nil || executor.calls != 0 {
		t.Fatalf("optional mode must be skipped without an action or audit failure: calls=%d audit=%v", executor.calls, result.AuditError)
	}
	foundBusinessState, foundSkip, foundComplete := false, false, false
	for _, event := range result.Events {
		foundBusinessState = foundBusinessState || event.EventType == model.StageExecutionEventBusinessStateObserved
		foundSkip = foundSkip || event.EventType == model.StageExecutionEventStepSatisfied
		foundComplete = foundComplete || event.EventType == model.StageExecutionEventStageCompleted
	}
	if !foundBusinessState || !foundSkip || !foundComplete {
		t.Fatalf("optional mode skip was not fully audited: %+v", result.Events)
	}
}

func TestAdaptiveHarnessSkipsAbsentOptionalExecutionContinuation(t *testing.T) {
	stage := BrowserAgentRuntimeStage{
		ID: "stage_optional_continue", Order: 1, NodeID: "node_optional_continue",
		StageKind: model.BusinessStageKindBusinessSubmit, EntryRoute: "https://product.example/entity/1",
		TargetContract: model.BrowserAgentTargetContract{SemanticID: "continue_execution", Destructive: false, Confidence: .76},
		Interactions:   []model.BrowserAgentInteraction{{Kind: model.GraphActionClick, NonDestructive: true, Parameters: map[string]any{"optional_when_target_absent": "true"}}},
	}
	plan := BrowserAgentRuntimePlan{
		RunID: "run_optional_continue", SourcePackageID: "pkg_optional_continue",
		SourceBundleHashSHA256: "bundle_optional_continue", PolicyHashSHA256: "policy_optional_continue",
		HarnessProfile: model.AdaptiveBusinessHarnessProfileV1, Stages: []BrowserAgentRuntimeStage{stage},
	}
	observer := unresolvedOptionalModeObserver{}
	executor := &countingStageExecutor{}
	sink, err := newStageEventAuditLog(t.TempDir(), "job_optional_continue")
	if err != nil {
		t.Fatal(err)
	}
	result, err := newBrowserAgentStageOrchestrator(contractBrowserAgentPolicyGuard{}).Run(context.Background(), plan, observer, executor, sink)
	if err != nil || result.AuditError != nil || executor.calls != 0 {
		t.Fatalf("optional continuation must be skipped without action: err=%v calls=%d audit=%v", err, executor.calls, result.AuditError)
	}
	foundSkip := false
	for _, event := range result.Events {
		foundSkip = foundSkip || event.EventType == model.StageExecutionEventStepSatisfied
	}
	if !foundSkip {
		t.Fatalf("optional continuation skip was not recorded: %+v", result.Events)
	}
}

type unresolvedOptionalModeObserver struct{}

func (unresolvedOptionalModeObserver) ObserveStage(_ context.Context, _ BrowserAgentRuntimePlan, _ BrowserAgentRuntimeStage) (BrowserAgentStageObservation, error) {
	return BrowserAgentStageObservation{
		Observation: model.RuntimeObservation{
			Source: model.RuntimeObservationActualBrowser, URL: "https://product.example/app",
			Assertions: []model.RuntimeAssertion{{Kind: "target_resolved", Passed: false, Actual: "no optional configuration control"}},
		},
		EvidenceRefs:   []model.EvidenceRef{{ID: "optional_mode_page", Kind: model.EvidenceKindWebScreenshot, Confidence: 1}},
		TargetResolved: false,
	}, nil
}

func TestCompileBrowserAgentRuntimePlanKeepsApprovedStageOrderAndSemantics(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	approved := &pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[1]
	outline := &pkg.ExecutableScriptBundle.ScriptOutline.Stages[1]
	approved.TargetRouteTemplate = "/project/:id"
	approved.ExpectedRouteAfterAction = "/project/:id"
	approved.RuntimeRouteVerificationRequired = true
	outline.TargetRouteTemplate = "/project/:id"
	outline.ExpectedRouteAfterAction = "/project/:id"
	outline.RuntimeRouteVerificationRequired = true
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
	if plan.Stages[1].ExpectedRouteAfterAction != "/project/:id" || plan.Stages[1].TargetRouteTemplate != "/project/:id" || !plan.Stages[1].RuntimeRouteVerificationRequired {
		t.Fatalf("compiled stage lost App-approved post-action route verification: %+v", plan.Stages[1])
	}
}

func TestBrowserAgentPolicyAllowsApprovedKeyboardPress(t *testing.T) {
	if !browserAgentActionTypeAllowed(model.GraphActionPress) {
		t.Fatal("approved non-destructive keyboard press must be executable by the Browser Agent policy")
	}
}

func TestBrowserAgentReadinessAcceptsCompleteFixture(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	report := browserAgentReadiness(&pkg)
	if !report.CanRun || len(report.Blockers) != 0 {
		t.Fatalf("complete outline fixture should be runnable: %+v", report)
	}
}

func TestBrowserAgentReadinessRejectsWorkspaceWaitWithoutTransitionOrValidation(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	stage := &pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[0]
	stage.StageKind = model.BusinessStageKindSessionSetup
	stage.RouteState = model.BusinessRouteStateWorkspace
	stage.Interaction = model.BrowserAgentInteraction{Kind: model.GraphActionWait, NonDestructive: true}
	pkg.ExecutableScriptBundle.ScriptOutline.Stages[0].StageKind = stage.StageKind
	pkg.ExecutableScriptBundle.ScriptOutline.Stages[0].RouteState = stage.RouteState
	pkg.ExecutableScriptBundle.ScriptOutline.Stages[0].Interactions = []model.BrowserAgentInteraction{{Kind: model.GraphActionWait, NonDestructive: true}}
	pkg.ExecutableScriptBundle.PlanJSON.Steps[0].Validations = nil

	report := browserAgentReadiness(&pkg)
	if report.CanRun || !browserAgentReadinessHasBlocker(report, "workspace_login_path_missing") || !browserAgentReadinessHasBlocker(report, "stage_success_condition_missing") {
		t.Fatalf("workspace wait must be rejected before opening a browser: %+v", report)
	}
}

func TestBrowserAgentReadinessRequiresCredentialGrantForSecretReference(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[1].Interaction.SecretRef = "vault://demo/login-password"
	pkg.ExecutableScriptBundle.ScriptOutline.Stages[1].Interactions[0].SecretRef = "vault://demo/login-password"
	pkg.ExecutableScriptBundle.PlanJSON.Steps[1].Action.SecretRef = "vault://demo/login-password"
	pkg.CredentialGrants = nil

	report := browserAgentReadiness(&pkg)
	if report.CanRun || !browserAgentReadinessHasBlocker(report, "credential_grant_missing") {
		t.Fatalf("secret reference without a scoped grant must be rejected: %+v", report)
	}
}

func TestBrowserAgentReadinessRejectsLiteralDynamicRouteValidation(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	step := &pkg.ExecutableScriptBundle.PlanJSON.Steps[1]
	step.Validations = []model.ValidationSpec{{ID: "result_route", Kind: "url_matches", Target: model.ActionTarget{URL: "https://app.example.com/project/:id"}, Required: true}}
	pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[1].RuntimeRouteVerificationRequired = true
	report := browserAgentReadiness(&pkg)
	if report.CanRun || !browserAgentReadinessHasBlocker(report, "dynamic_route_binding_missing") {
		t.Fatalf("literal dynamic route must be rejected before execution: %+v", report)
	}
}

func TestBrowserAgentReadinessAcceptsAppApprovedDynamicRouteTemplate(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	stage := &pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[1]
	outline := &pkg.ExecutableScriptBundle.ScriptOutline.Stages[1]
	stage.RuntimeRouteVerificationRequired = true
	stage.TargetRouteTemplate = "/project/:id"
	outline.TargetRouteTemplate = "/project/:id"
	step := &pkg.ExecutableScriptBundle.PlanJSON.Steps[1]
	step.Validations = []model.ValidationSpec{{ID: "result_route", Kind: "url_matches", Target: model.ActionTarget{URL: "https://app.example.com/project/:id"}, Required: true}}
	report := browserAgentReadiness(&pkg)
	if !report.CanRun || browserAgentReadinessHasBlocker(report, "dynamic_route_binding_missing") {
		t.Fatalf("matching App-approved dynamic route template should be accepted: %+v", report)
	}
}

func TestBrowserAgentReadinessRejectsPostActionValidationReusingClickedControl(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	step := &pkg.ExecutableScriptBundle.PlanJSON.Steps[1]
	step.Action.Type = model.GraphActionClick
	step.Action.Target = model.ActionTarget{TestID: "button-new-project"}
	step.Validations = []model.ValidationSpec{{ID: "after_click", Kind: "element_visible", Target: model.ActionTarget{TestID: "button-new-project"}, Required: true}}
	pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[1].StageKind = model.BusinessStageKindBusinessSubmit
	report := browserAgentReadiness(&pkg)
	if report.CanRun || !browserAgentReadinessHasBlocker(report, "post_action_validation_reuses_action_target") {
		t.Fatalf("post-action validation reuse must block formal execution: %+v", report)
	}
}

func TestBrowserAgentReadinessDetectsApprovedActionIdentityReuse(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	stage := &pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[1]
	stage.StageKind = model.BusinessStageKindBusinessAction
	outline := &pkg.ExecutableScriptBundle.ScriptOutline.Stages[1]
	outline.Components = []model.BrowserAgentComponentTarget{{
		ComponentRef: stage.TargetContract.ComponentRef,
		Selector:     `[data-testid="button-new-project"]`,
		EvidenceRefs: stage.TargetContract.EvidenceRefs,
	}}
	step := &pkg.ExecutableScriptBundle.PlanJSON.Steps[1]
	step.Action.Type = model.GraphActionClick
	step.Action.Target = model.ActionTarget{
		Selector:     `[data-testid="project-list"]`,
		Label:        "新建项目 button-new-project",
		EvidenceRefs: stage.TargetContract.EvidenceRefs,
	}
	step.Validations = []model.ValidationSpec{{
		ID:       "after_click",
		Kind:     "element_visible",
		Target:   model.ActionTarget{Selector: `[data-testid="button-new-project"]`},
		Required: true,
	}}
	report := browserAgentReadiness(&pkg)
	if report.CanRun || (!browserAgentReadinessHasBlocker(report, "post_action_validation_reuses_action_identity") && !browserAgentReadinessHasBlocker(report, "post_action_validation_reuses_approved_action_evidence")) {
		t.Fatalf("expected action identity reuse blocker, got: %+v", report)
	}
}

func TestBrowserAgentReadinessAcceptsIndependentResultComponentWithSharedStageEvidence(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	stage := &pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[1]
	stage.StageKind = model.BusinessStageKindBusinessAction
	stage.TargetContract.ComponentRef = "new-project-action"
	step := &pkg.ExecutableScriptBundle.PlanJSON.Steps[1]
	step.Action.Type = model.GraphActionClick
	step.Action.Target = model.ActionTarget{
		Selector: `[data-testid="button-new-project"]`, TestID: "button-new-project", EvidenceRefs: stage.TargetContract.EvidenceRefs,
	}
	step.Validations = []model.ValidationSpec{{
		ID: "after_click", Kind: "element_visible", Target: model.ActionTarget{Selector: `[data-testid="dialog-new-project"]`, TestID: "dialog-new-project"}, Required: true,
	}}
	pkg.ExecutableScriptBundle.ScriptOutline.Stages[1].Components = []model.BrowserAgentComponentTarget{
		{ComponentRef: "new-project-action", Selector: `[data-testid="button-new-project"]`, TestID: "button-new-project", EvidenceRefs: stage.TargetContract.EvidenceRefs},
		{Selector: `[data-testid="dialog-new-project"]`, TestID: "dialog-new-project", EvidenceRefs: stage.TargetContract.EvidenceRefs},
	}

	report := browserAgentReadiness(&pkg)
	if browserAgentReadinessHasBlocker(report, "post_action_validation_reuses_approved_action_evidence") {
		t.Fatalf("independent result-state component was misclassified as the approved action: %+v", report)
	}
}

func TestBrowserAgentReadinessRejectsDeclaredInputWithoutFillAction(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	pkg.ProjectContextSummary.Goals = []model.DemoGoal{{
		ID: "goal_input", ValueProposition: "在输入栏中输入一个业务需求并提交。",
	}}
	for index := range pkg.ExecutableScriptBundle.PlanJSON.Steps {
		pkg.ExecutableScriptBundle.PlanJSON.Steps[index].Action.Type = model.GraphActionClick
	}
	for index := range pkg.ExecutableScriptBundle.StageApprovalPlan.Stages {
		pkg.ExecutableScriptBundle.StageApprovalPlan.Stages[index].Interaction.Kind = model.GraphActionClick
	}
	report := browserAgentReadiness(&pkg)
	if !browserAgentReadinessHasBlocker(report, "declared_business_input_action_missing") {
		t.Fatalf("declared input without a fill action must be blocked: %+v", report)
	}
}

func browserAgentReadinessHasBlocker(report BrowserAgentReadinessReport, code string) bool {
	for _, blocker := range report.Blockers {
		if blocker.Code == code {
			return true
		}
	}
	return false
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
	outsideOrigin := base
	outsideOrigin.URL = "http://app.example.com/dashboard"
	if decision := guard.Authorize(plan, outsideOrigin); decision.Allowed || decision.Code != "origin_not_allowed" {
		t.Fatalf("origin scheme downgrade was not blocked: %+v", decision)
	}
	outsideRoute := base
	outsideRoute.URL = "https://app.example.com/settings"
	if decision := guard.Authorize(plan, outsideRoute); decision.Allowed || decision.Code != "route_not_allowed" {
		t.Fatalf("unapproved same-origin route was not blocked: %+v", decision)
	}
	childRoute := base
	childRoute.URL = "https://app.example.com/dashboard/projects/42"
	if decision := guard.Authorize(plan, childRoute); !decision.Allowed {
		t.Fatalf("approved route must cover its child paths: %+v", decision)
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

func TestBrowserAgentPolicyGuardBindsApprovedDynamicRouteTemplate(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	plan.ExplorationScope.AllowedRoutes = []string{"/project/:id"}
	guard := contractBrowserAgentPolicyGuard{}
	stage := plan.Stages[0]
	intent := BrowserAgentActionIntent{
		NodeID: stage.NodeID, StageID: stage.ID, ActionType: model.GraphActionNavigate,
		URL: "https://app.example.com/project/proj_42", Route: "/project/proj_42",
		NonDestructive: true, TargetContract: stage.TargetContract,
	}
	if decision := guard.Authorize(plan, intent); !decision.Allowed {
		t.Fatalf("runtime-created resource route should match approved template: %+v", decision)
	}
	intent.URL = "https://app.example.com/settings"
	intent.Route = "/settings"
	if decision := guard.Authorize(plan, intent); decision.Allowed || decision.Code != "route_not_allowed" {
		t.Fatalf("unapproved route must remain blocked: %+v", decision)
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

func TestBrowserAgentStageOrchestratorReexecutesOnlyApprovedRepairAndRecordsLedger(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &repairThenContinueVerifier{}
	executor := &countingStageExecutor{}
	orchestrator := newBrowserAgentStageOrchestratorWithVerifier(contractBrowserAgentPolicyGuard{}, verifier)
	result, err := orchestrator.Run(context.Background(), plan, stubStageObserver{}, executor, &memoryStageEventSink{})
	if err != nil {
		t.Fatalf("approved repair must re-execute its stage: %v", err)
	}
	if verifier.calls != len(plan.Stages)+1 || executor.calls != len(plan.Stages)+1 {
		t.Fatalf("expected exactly one re-execution after repair: verifier=%d executor=%d", verifier.calls, executor.calls)
	}
	if len(result.PatchLedger) != 1 || !result.PatchLedger[0].Applied {
		t.Fatalf("approved repair was not written to the patch ledger: %+v", result.PatchLedger)
	}
	if len(result.ValidationReports) < 2 || len(result.ValidationReports[0].RepairProposalRefs) != 1 {
		t.Fatalf("repair proposal must remain linked to the verifier report: %+v", result.ValidationReports)
	}
	seenProposed, seenApplied, seenRetry := false, false, false
	for _, event := range result.Events {
		seenProposed = seenProposed || event.EventType == model.StageExecutionEventRepairProposed
		seenApplied = seenApplied || event.EventType == model.StageExecutionEventRepairApplied
		seenRetry = seenRetry || (event.EventType == model.StageExecutionEventActionStarted && event.Attempt == 2)
	}
	if !seenProposed || !seenApplied {
		t.Fatalf("repair audit events are missing: %+v", result.Events)
	}
	if !seenRetry {
		t.Fatalf("repaired action must be marked as the second attempt: %+v", result.Events)
	}
}

func TestBrowserAgentStageOrchestratorCaptureTimingRepairDoesNotReplayAction(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &captureTimingThenContinueVerifier{}
	executor := &captureTimingStageExecutor{}
	result, err := newBrowserAgentStageOrchestratorWithVerifier(contractBrowserAgentPolicyGuard{}, verifier).Run(context.Background(), plan, stubStageObserver{}, executor, &memoryStageEventSink{})
	if err != nil {
		t.Fatalf("approved capture timing repair must revalidate without replaying the action: %v", err)
	}
	if executor.executeCalls != len(plan.Stages) || executor.revalidateCalls != 1 {
		t.Fatalf("capture timing repair replayed an action or skipped revalidation: execute=%d revalidate=%d", executor.executeCalls, executor.revalidateCalls)
	}
	if len(result.PatchLedger) != 1 || !result.PatchLedger[0].Applied || result.PatchLedger[0].Field != "script_outline.stages[].capture_plan.pre_capture_wait_ms" {
		t.Fatalf("capture timing patch ledger is incomplete: %+v", result.PatchLedger)
	}
	seenSecondAttemptAction, seenSecondAttemptObservation := false, false
	for _, event := range result.Events {
		seenSecondAttemptAction = seenSecondAttemptAction || (event.Attempt == 2 && (event.EventType == model.StageExecutionEventActionStarted || event.EventType == model.StageExecutionEventActionCompleted))
		seenSecondAttemptObservation = seenSecondAttemptObservation || (event.Attempt == 2 && event.EventType == model.StageExecutionEventObservationCollected)
	}
	if seenSecondAttemptAction || !seenSecondAttemptObservation {
		t.Fatalf("capture timing audit must show recapture, not action replay: %+v", result.Events)
	}
}

func TestBrowserAgentStageOrchestratorOnceEffectRepairNeverReplaysAction(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	plan.Stages[0].InteractionContract = &model.InteractionContract{ReplayPolicy: model.InteractionReplayOnceEffect}
	executor := &countingRevalidatingStageExecutor{}
	result, err := newBrowserAgentStageOrchestratorWithVerifier(contractBrowserAgentPolicyGuard{}, &repairThenContinueVerifier{}).Run(context.Background(), plan, stubStageObserver{}, executor, &memoryStageEventSink{})
	if err != nil {
		t.Fatalf("once-effect outcome repair should revalidate: %v", err)
	}
	if executor.executeCalls != len(plan.Stages) || executor.revalidateCalls != 1 {
		t.Fatalf("once-effect repair replayed the action: execute=%d revalidate=%d", executor.executeCalls, executor.revalidateCalls)
	}
	for _, event := range result.Events {
		if event.StageID == plan.Stages[0].ID && event.Attempt == 2 && (event.EventType == model.StageExecutionEventActionStarted || event.EventType == model.StageExecutionEventActionCompleted) {
			t.Fatalf("second once-effect action attempt was audited: %+v", event)
		}
	}
}

func TestBrowserAgentStageOrchestratorCaptureTimingRepairFailsClosedWithoutRevalidator(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	result, err := newBrowserAgentStageOrchestratorWithVerifier(contractBrowserAgentPolicyGuard{}, &captureTimingThenContinueVerifier{}).Run(context.Background(), plan, stubStageObserver{}, &countingStageExecutor{}, &memoryStageEventSink{})
	if runtimeExecutionErrorCode(err) != "browser_agent_revalidation_unavailable" || len(result.PatchLedger) != 1 || !result.PatchLedger[0].Applied {
		t.Fatalf("missing non-action revalidator must stop after recording the approved patch: code=%q ledger=%+v err=%v", runtimeExecutionErrorCode(err), result.PatchLedger, err)
	}
}

func TestBrowserAgentStageOrchestratorAppliesOnlyWorkerVerifiedSelectorAlternative(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	stage := plan.Stages[1]
	if len(stage.Components) == 0 || len(stage.Components[0].SelectorAlternatives) == 0 {
		t.Fatal("fixture must declare an App-approved selector alternative")
	}
	observer := &selectorAlternativeObserver{candidate: stage.Components[0].SelectorAlternatives[0]}
	orchestrator := newBrowserAgentStageOrchestratorWithVerifier(contractBrowserAgentPolicyGuard{}, &stubStageVerifier{decision: model.ValidationDecisionContinue})
	result, err := orchestrator.Run(context.Background(), plan, observer, &stubStageExecutor{}, &memoryStageEventSink{})
	if err != nil {
		t.Fatalf("worker-verified approved alternative should be applied: %v", err)
	}
	if observer.calls != len(plan.Stages)+1 || len(result.PatchLedger) != 1 || !result.PatchLedger[0].Applied {
		t.Fatalf("expected exactly one selector patch and re-observation: calls=%d ledger=%+v", observer.calls, result.PatchLedger)
	}
	entry := result.PatchLedger[0]
	if entry.After != selectorCandidateEncoding(stage.Components[0].SelectorAlternatives[0]) || entry.Field != "script_outline.stages[].components[].selector" {
		t.Fatalf("selector patch must retain the approved candidate identity: %+v", entry)
	}
}

func TestCurrentStageSelectorFallsBackToApprovedSemanticLocator(t *testing.T) {
	stage := BrowserAgentRuntimeStage{
		TargetContract: model.BrowserAgentTargetContract{SemanticID: "invite-member", ComponentRef: "component:invite-button"},
		Components: []model.BrowserAgentComponentTarget{{
			ComponentRef: "component:invite-button",
			Role:         "button",
			Name:         "Invite teammate",
			TestID:       "invite-member",
		}},
	}
	if got := currentStageSelector(stage); got != "testid:invite-member" {
		t.Fatalf("approved test id should identify the pre-repair locator, got %q", got)
	}

	stage.Components[0].TestID = ""
	if got := currentStageSelector(stage); got != "role:button:name:Invite teammate" {
		t.Fatalf("approved role and name should identify the pre-repair locator, got %q", got)
	}
}

func TestBrowserAgentStageOrchestratorStopsWithoutWorkerVerifiedAlternative(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	observer := unresolvedStageObserver{}
	result, err := newBrowserAgentStageOrchestrator(contractBrowserAgentPolicyGuard{}).Run(context.Background(), plan, observer, &stubStageExecutor{}, &memoryStageEventSink{})
	if runtimeExecutionErrorCode(err) != "browser_agent_target_not_resolved" || len(result.PatchLedger) != 0 {
		t.Fatalf("unverified alternatives must never be guessed or patched: code=%q ledger=%+v err=%v", runtimeExecutionErrorCode(err), result.PatchLedger, err)
	}
}

func TestBrowserAgentStageOrchestratorAppliesBusyPageWaitRepairOnce(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	plan, err := compileBrowserAgentRuntimePlan(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	observer := &busyPageWaitObserver{}
	result, err := newBrowserAgentStageOrchestratorWithVerifier(contractBrowserAgentPolicyGuard{}, &stubStageVerifier{decision: model.ValidationDecisionContinue}).Run(context.Background(), plan, observer, &stubStageExecutor{}, &memoryStageEventSink{})
	if err != nil {
		t.Fatalf("busy-page wait repair should remain in the approved stage: %v", err)
	}
	if observer.calls != len(plan.Stages)+1 || len(result.PatchLedger) != 1 || !result.PatchLedger[0].Applied {
		t.Fatalf("expected one wait repair and one re-observation: calls=%d ledger=%+v", observer.calls, result.PatchLedger)
	}
	entry := result.PatchLedger[0]
	if entry.Reason != "approved bounded runtime repair" || entry.Before != "wait_after_entry_at_least_1000ms" || entry.After != "wait_after_entry_at_least_2000ms" {
		t.Fatalf("wait repair must be a bounded increase of the declared entry wait: %+v", entry)
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

type selectorAlternativeObserver struct {
	calls     int
	candidate model.SelectorCandidate
}

func (o *selectorAlternativeObserver) ObserveStage(_ context.Context, _ BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage) (BrowserAgentStageObservation, error) {
	o.calls++
	resolved := stage.PreferredSelectorAlternative != nil || stage.NodeID != "node_invite_member"
	observation := model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, Title: "Dashboard"}
	if !resolved {
		observation.Assertions = []model.RuntimeAssertion{{Kind: "target_resolved", Passed: false, Actual: "browser_agent_target_not_resolved: node_invite_member; strategies=primary"}}
		return BrowserAgentStageObservation{Observation: observation, EvidenceRefs: []model.EvidenceRef{{ID: "selector_before", Kind: model.EvidenceKindBrowserTrace}}, TargetResolved: false, PreferredSelectorAlternative: &o.candidate}, nil
	}
	observation.Assertions = []model.RuntimeAssertion{{Kind: "target_resolved", Passed: true, Actual: "approved_testid"}}
	return BrowserAgentStageObservation{Observation: observation, EvidenceRefs: []model.EvidenceRef{{ID: "selector_after", Kind: model.EvidenceKindBrowserTrace}}, TargetResolved: true}, nil
}

type unresolvedStageObserver struct{}

func (unresolvedStageObserver) ObserveStage(_ context.Context, _ BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage) (BrowserAgentStageObservation, error) {
	return BrowserAgentStageObservation{Observation: model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, Assertions: []model.RuntimeAssertion{{Kind: "target_resolved", Passed: false, Actual: "browser_agent_target_not_resolved"}}}, EvidenceRefs: []model.EvidenceRef{{ID: "unresolved", Kind: model.EvidenceKindBrowserTrace}}, TargetResolved: stage.NodeID != "node_invite_member"}, nil
}

type busyPageWaitObserver struct{ calls int }

func (o *busyPageWaitObserver) ObserveStage(_ context.Context, _ BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage) (BrowserAgentStageObservation, error) {
	o.calls++
	resolved := stage.NodeID != "node_invite_member" || containsExact(stage.WaitConditions, "wait_after_entry_at_least_2000ms")
	observation := model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, Assertions: []model.RuntimeAssertion{{Kind: "target_resolved", Passed: resolved, Actual: "live_page"}}}
	result := BrowserAgentStageObservation{Observation: observation, EvidenceRefs: []model.EvidenceRef{{ID: fmt.Sprintf("busy_%d", o.calls), Kind: model.EvidenceKindBrowserTrace}}, TargetResolved: resolved}
	if !resolved {
		result.SuggestedWaitCondition = "wait_after_entry_at_least_2000ms"
	}
	return result, nil
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

func (s *memoryStageEventSink) Events() []model.StageExecutionEvent {
	return append([]model.StageExecutionEvent{}, s.events...)
}

type resumableStageExecutor struct {
	executeCalls    int
	revalidateCalls int
	revalidatePass  bool
}

type consumedCheckpointStageExecutor struct {
	executeCalls    int
	revalidateCalls int
	checkpointURL   string
}

func (e *consumedCheckpointStageExecutor) ExecuteStage(_ context.Context, _ BrowserAgentRuntimePlan, _ BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error) {
	e.executeCalls++
	return BrowserAgentStageActionResult{
		Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, URL: e.checkpointURL, Assertions: []model.RuntimeAssertion{{Kind: "current_stage", Passed: true}}},
		EvidenceRefs: []model.EvidenceRef{{ID: fmt.Sprintf("execute_after_resume_%d", e.executeCalls), Kind: model.EvidenceKindBrowserTrace}},
	}, nil
}

func (e *consumedCheckpointStageExecutor) RevalidateStage(_ context.Context, _ BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error) {
	e.revalidateCalls++
	if !stage.CheckpointRestore || stage.URL != e.checkpointURL {
		return BrowserAgentStageActionResult{}, errors.New("resume did not bind the exact audited checkpoint route")
	}
	return BrowserAgentStageActionResult{
		Observation: &model.RuntimeObservation{
			Source: model.RuntimeObservationAssertion, URL: e.checkpointURL,
			// An async page may legitimately leave the prior phase after the
			// following stage has already begun. This stale assertion must not
			// cause the completed observe-only stage to be replayed.
			Assertions: []model.RuntimeAssertion{{Kind: "prior_phase_visible", Passed: false}},
		},
		EvidenceRefs: []model.EvidenceRef{{ID: "checkpoint_route_recaptured", Kind: model.EvidenceKindWebScreenshot}},
	}, nil
}

func (e *resumableStageExecutor) ExecuteStage(_ context.Context, _ BrowserAgentRuntimePlan, _ BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error) {
	e.executeCalls++
	return BrowserAgentStageActionResult{Observation: &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "result", Passed: true}}}, EvidenceRefs: []model.EvidenceRef{{ID: fmt.Sprintf("execute_%d", e.executeCalls), Kind: model.EvidenceKindBrowserTrace}}}, nil
}

func (e *resumableStageExecutor) RevalidateStage(_ context.Context, _ BrowserAgentRuntimePlan, _ BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error) {
	e.revalidateCalls++
	return BrowserAgentStageActionResult{Observation: &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "checkpoint", Passed: e.revalidatePass}}}, EvidenceRefs: []model.EvidenceRef{{ID: fmt.Sprintf("revalidate_%d", e.revalidateCalls), Kind: model.EvidenceKindBrowserTrace}}}, nil
}

func TestBrowserAgentStageOrchestratorResumesCompletedStagesWithoutReplayingActions(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	orchestrator := newBrowserAgentStageOrchestrator(contractBrowserAgentPolicyGuard{})
	plan, err := orchestrator.Prepare(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	sink := &memoryStageEventSink{}
	executor := &resumableStageExecutor{revalidatePass: true}
	if _, err := orchestrator.Run(context.Background(), plan, stubStageObserver{}, executor, sink); err != nil {
		t.Fatal(err)
	}
	firstExecuteCalls := executor.executeCalls
	resumed, err := orchestrator.Run(context.Background(), plan, stubStageObserver{}, executor, sink)
	if err != nil {
		t.Fatal(err)
	}
	if executor.executeCalls != firstExecuteCalls || executor.revalidateCalls != 1 || len(resumed.Events) != len(plan.Stages) {
		t.Fatalf("checkpoint resume replayed actions or skipped revalidation: execute=%d revalidate=%d events=%+v", executor.executeCalls, executor.revalidateCalls, resumed.Events)
	}
	for _, event := range resumed.Events {
		if event.EventType != model.StageExecutionEventStageResumed {
			t.Fatalf("resume audit event is missing: %+v", resumed.Events)
		}
	}
}

func TestBrowserAgentStageOrchestratorDoesNotReplayConsumedObserveCheckpoint(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	orchestrator := newBrowserAgentStageOrchestrator(contractBrowserAgentPolicyGuard{})
	plan, err := orchestrator.Prepare(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Stages) < 2 {
		t.Fatal("resume fixture requires two stages")
	}
	plan.Stages = plan.Stages[:2]
	plan.Stages[0].InteractionContract = &model.InteractionContract{ReplayPolicy: model.InteractionReplayObserveOnly}
	checkpointURL := "https://app.example.com/results/run-42"
	completed := model.StageExecutionEvent{
		RunID: plan.RunID, SourcePackageID: plan.SourcePackageID, SourceBundleHashSHA256: plan.SourceBundleHashSHA256,
		PolicyHashSHA256: plan.PolicyHashSHA256, StageID: plan.Stages[0].ID, Sequence: 1,
		EventType:    model.StageExecutionEventStageCompleted,
		Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, URL: checkpointURL, Assertions: []model.RuntimeAssertion{{Kind: "prior_phase_visible", Passed: true}}},
		EvidenceRefs: []model.EvidenceRef{{ID: "completed_checkpoint", Kind: model.EvidenceKindWebScreenshot}},
	}
	laterStarted := model.StageExecutionEvent{
		RunID: plan.RunID, SourcePackageID: plan.SourcePackageID, SourceBundleHashSHA256: plan.SourceBundleHashSHA256,
		PolicyHashSHA256: plan.PolicyHashSHA256, StageID: plan.Stages[1].ID, Sequence: 2,
		EventType: model.StageExecutionEventStageStarted,
	}
	sink := &memoryStageEventSink{events: []model.StageExecutionEvent{completed, laterStarted}}
	executor := &consumedCheckpointStageExecutor{checkpointURL: checkpointURL}

	resumed, err := orchestrator.Run(context.Background(), plan, stubStageObserver{}, executor, sink)
	if err != nil {
		t.Fatal(err)
	}
	if executor.revalidateCalls != 1 || executor.executeCalls != 1 {
		t.Fatalf("completed observe checkpoint was replayed: execute=%d revalidate=%d", executor.executeCalls, executor.revalidateCalls)
	}
	if len(resumed.Events) == 0 || resumed.Events[0].StageID != plan.Stages[0].ID || resumed.Events[0].EventType != model.StageExecutionEventStageResumed {
		t.Fatalf("completed checkpoint was not resumed before the first incomplete stage: %+v", resumed.Events)
	}
}

func TestBindRuntimeStageToCheckpointUsesExactAuditedRouteWithoutChangingAction(t *testing.T) {
	stage := BrowserAgentRuntimeStage{
		ID: "stage-observe", Order: 2, NodeID: "observe", URL: "https://app.example/app", Route: "/app", EntryRoute: "/app",
		Interactions: []model.BrowserAgentInteraction{{Kind: model.GraphActionInspect, NonDestructive: true}},
		Validations:  []model.ValidationSpec{{ID: "entry", Kind: "url_matches", Target: model.ActionTarget{URL: "/app"}, Expected: "/app", Required: true}},
	}
	bound := bindRuntimeStageToCheckpoint(stage, &model.RuntimeObservation{URL: "https://app.example/results/runtime-42"})
	if !bound.CheckpointRestore || bound.URL != "https://app.example/results/runtime-42" || bound.Validations[0].Target.URL != bound.URL || bound.Interactions[0].Kind != model.GraphActionInspect {
		t.Fatalf("checkpoint route binding changed action semantics or missed the exact audited route: %+v", bound)
	}
}

func TestBrowserAgentStageOrchestratorInjectedRestartResumesCommittedOnceEffectWithoutReplay(t *testing.T) {
	pkg := readBrowserAgentOutlineFixture(t)
	orchestrator := newBrowserAgentStageOrchestrator(contractBrowserAgentPolicyGuard{})
	plan, err := orchestrator.Prepare(&pkg)
	if err != nil {
		t.Fatal(err)
	}
	plan.Stages = plan.Stages[:1]
	plan.Stages[0].InteractionContract = &model.InteractionContract{ReplayPolicy: model.InteractionReplayOnceEffect}
	plan.InterruptAfterFirstOnceEffect = true
	sink := &memoryStageEventSink{}
	executor := &resumableStageExecutor{revalidatePass: true}

	if _, err := orchestrator.Run(context.Background(), plan, stubStageObserver{}, executor, sink); runtimeExecutionErrorCode(err) != runtimeErrorWorkerRestartInjected {
		t.Fatalf("first run must stop only after the once-effect checkpoint is committed: %v", err)
	}
	if executor.executeCalls != 1 {
		t.Fatalf("first run must execute the once-effect exactly once, got %d", executor.executeCalls)
	}
	completed := 0
	for _, event := range sink.events {
		if event.EventType == model.StageExecutionEventStageCompleted {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("restart injection must happen after one durable stage_completed event: %+v", sink.events)
	}

	resumed, err := orchestrator.Run(context.Background(), plan, stubStageObserver{}, executor, sink)
	if err != nil {
		t.Fatal(err)
	}
	if executor.executeCalls != 1 || executor.revalidateCalls != 1 {
		t.Fatalf("resumed run must revalidate without replay: execute=%d revalidate=%d", executor.executeCalls, executor.revalidateCalls)
	}
	if len(resumed.Events) != 1 || resumed.Events[0].EventType != model.StageExecutionEventStageResumed {
		t.Fatalf("resumed run must emit one auditable stage_resumed event: %+v", resumed.Events)
	}
}

type stubStageVerifier struct {
	decision model.ValidationDecision
	calls    int
}

func TestWarningOnlyRepairDecisionContinuesAfterPassedBrowserAssertions(t *testing.T) {
	report := model.ValidationReport{
		SchemaVersion: model.ValidationReportSchemaVersion, ReportID: "warning_report", RunID: "run_warning",
		SourcePackageID: "pkg_warning", SourceBundleHashSHA256: "bundle_warning", PolicyHashSHA256: "policy_warning",
		Phase: model.ValidationPhaseRuntimeStage, NodeID: "node_warning", StageID: "stage_warning",
		Decision: model.ValidationDecisionRepairAllowed, PassRate: .5, OverallConfidence: .8,
		EvidenceQuality: model.RuntimeObservationAssertion, CreatedAt: timeNowUTC(),
		Checks: []model.ValidationCheck{{ID: "warning_check", Kind: "warning", Code: string(model.ValidationResultTypeWarning), Severity: model.FindingSeverityWarning, Passed: false, Summary: "warning-only compatibility check"}},
	}
	observation := &model.RuntimeObservation{Assertions: []model.RuntimeAssertion{{Kind: "element_visible", Passed: true}}}
	if !warningOnlyRepairDecisionCanContinue(report, observation) {
		t.Fatal("warning-only compatibility feedback should not require an invented repair proposal")
	}
	normalized, ok := normalizeWarningOnlyRepairDecision(report, observation, []model.EvidenceRef{{ID: "runtime_screenshot", Kind: model.EvidenceKindWebScreenshot}})
	if !ok || normalized.Decision != model.ValidationDecisionContinue || len(normalized.EvidenceRefs) != 1 {
		t.Fatalf("normalized continue report must retain real runtime evidence: %+v", normalized)
	}
	if err := normalized.Validate(); err != nil {
		t.Fatalf("normalized continue report must satisfy the formal report contract: %v", err)
	}
	report.Checks = append(report.Checks, model.ValidationCheck{Severity: model.FindingSeverityBlocking, Passed: false})
	if warningOnlyRepairDecisionCanContinue(report, observation) {
		t.Fatal("blocking findings must not bypass runtime repair or stop policy")
	}
	report.Checks = report.Checks[:1]
	observation.Assertions[0].Passed = false
	if warningOnlyRepairDecisionCanContinue(report, observation) {
		t.Fatal("failed browser assertions must not be normalized to continue")
	}
}

type countingStageExecutor struct{ calls int }

func (e *countingStageExecutor) ExecuteStage(_ context.Context, _ BrowserAgentRuntimePlan, _ BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error) {
	e.calls++
	return BrowserAgentStageActionResult{Observation: &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "element_visible", Passed: true}}}, EvidenceRefs: []model.EvidenceRef{{ID: fmt.Sprintf("action_%d", e.calls), Kind: model.EvidenceKindBrowserTrace}}}, nil
}

type captureTimingStageExecutor struct {
	executeCalls    int
	revalidateCalls int
}

type countingRevalidatingStageExecutor struct {
	executeCalls    int
	revalidateCalls int
}

func (e *countingRevalidatingStageExecutor) ExecuteStage(_ context.Context, _ BrowserAgentRuntimePlan, _ BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error) {
	e.executeCalls++
	return BrowserAgentStageActionResult{Observation: &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "outcome", Passed: true}}}, EvidenceRefs: []model.EvidenceRef{{ID: fmt.Sprintf("once_action_%d", e.executeCalls), Kind: model.EvidenceKindBrowserTrace}}}, nil
}

func (e *countingRevalidatingStageExecutor) RevalidateStage(_ context.Context, _ BrowserAgentRuntimePlan, _ BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error) {
	e.revalidateCalls++
	return BrowserAgentStageActionResult{Observation: &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "outcome", Passed: true}}}, EvidenceRefs: []model.EvidenceRef{{ID: "once_effect_revalidated", Kind: model.EvidenceKindBrowserTrace}}}, nil
}

func (e *captureTimingStageExecutor) ExecuteStage(_ context.Context, _ BrowserAgentRuntimePlan, _ BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error) {
	e.executeCalls++
	return BrowserAgentStageActionResult{Observation: &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "capture_ready", Passed: e.executeCalls > 1}}}, EvidenceRefs: []model.EvidenceRef{{ID: fmt.Sprintf("capture_action_%d", e.executeCalls), Kind: model.EvidenceKindWebScreenshot}}}, nil
}

func (e *captureTimingStageExecutor) RevalidateStage(_ context.Context, _ BrowserAgentRuntimePlan, stage BrowserAgentRuntimeStage) (BrowserAgentStageActionResult, error) {
	e.revalidateCalls++
	if stage.CapturePlan == nil || stage.CapturePlan.PreCaptureWaitMS != 1200 {
		return BrowserAgentStageActionResult{}, errors.New("patched capture timing was not passed to the revalidator")
	}
	return BrowserAgentStageActionResult{Observation: &model.RuntimeObservation{Source: model.RuntimeObservationAssertion, Assertions: []model.RuntimeAssertion{{Kind: "capture_ready", Passed: true}}}, EvidenceRefs: []model.EvidenceRef{{ID: "capture_revalidated", Kind: model.EvidenceKindWebScreenshot}}}, nil
}

type repairThenContinueVerifier struct{ calls int }

type captureTimingThenContinueVerifier struct{ calls int }

func (v *captureTimingThenContinueVerifier) ValidateStageEvents(_ context.Context, validationContext model.BrowserAgentValidationContext, events []model.StageExecutionEvent) (model.ValidationReport, error) {
	v.calls++
	last := events[len(events)-1]
	decision := model.ValidationDecisionContinue
	if v.calls == 1 {
		decision = model.ValidationDecisionRepairAllowed
	}
	return model.ValidationReport{SchemaVersion: model.ValidationReportSchemaVersion, ReportID: fmt.Sprintf("capture_report_%d", v.calls), RunID: last.RunID, SourcePackageID: validationContext.SourcePackageID, SourceBundleHashSHA256: validationContext.SourceBundleHashSHA256, PolicyHashSHA256: validationContext.EffectivePolicyHashSHA256, Phase: model.ValidationPhaseRuntimeStage, NodeID: last.NodeID, StageID: last.StageID, Decision: decision, PassRate: 1, OverallConfidence: .95, EvidenceQuality: model.RuntimeObservationAssertion, EvidenceRefs: []model.EvidenceRef{{ID: "capture_verification", Kind: model.EvidenceKindWebScreenshot}}, CreatedAt: timeNowUTC()}, nil
}

func (v *captureTimingThenContinueVerifier) ProposeRuntimeRepair(_ context.Context, validationContext model.BrowserAgentValidationContext, stage BrowserAgentRuntimeStage, events []model.StageExecutionEvent, _ model.ValidationReport) (model.RuntimeRepairProposal, error) {
	return model.RuntimeRepairProposal{SchemaVersion: model.RuntimeRepairProposalSchemaVersion, ProposalID: "repair_capture_1", RunID: events[len(events)-1].RunID, NodeID: stage.NodeID, StageID: stage.ID, BaseBundleHashSHA256: validationContext.SourceBundleHashSHA256, PolicyHashSHA256: validationContext.EffectivePolicyHashSHA256, RepairKind: "capture_timing", Field: "script_outline.stages[].capture_plan.pre_capture_wait_ms", Before: "", After: "1200", Confidence: .95, EvidenceRefs: []model.EvidenceRef{{ID: "capture_proposal", Kind: model.EvidenceKindWebScreenshot}}, CreatedAt: timeNowUTC()}, nil
}

func (v *repairThenContinueVerifier) ValidateStageEvents(_ context.Context, context model.BrowserAgentValidationContext, events []model.StageExecutionEvent) (model.ValidationReport, error) {
	v.calls++
	last := events[len(events)-1]
	decision := model.ValidationDecisionContinue
	if v.calls == 1 {
		decision = model.ValidationDecisionRepairAllowed
	}
	return model.ValidationReport{SchemaVersion: model.ValidationReportSchemaVersion, ReportID: fmt.Sprintf("repair_report_%d", v.calls), RunID: last.RunID, SourcePackageID: context.SourcePackageID, SourceBundleHashSHA256: context.SourceBundleHashSHA256, PolicyHashSHA256: context.EffectivePolicyHashSHA256, Phase: model.ValidationPhaseRuntimeStage, NodeID: last.NodeID, StageID: last.StageID, Decision: decision, PassRate: 1, OverallConfidence: .95, EvidenceQuality: model.RuntimeObservationActualBrowser, EvidenceRefs: []model.EvidenceRef{{ID: "verification_repair", Kind: model.EvidenceKindBrowserTrace}}, CreatedAt: timeNowUTC()}, nil
}

func (v *repairThenContinueVerifier) ProposeRuntimeRepair(_ context.Context, context model.BrowserAgentValidationContext, stage BrowserAgentRuntimeStage, events []model.StageExecutionEvent, _ model.ValidationReport) (model.RuntimeRepairProposal, error) {
	return model.RuntimeRepairProposal{SchemaVersion: model.RuntimeRepairProposalSchemaVersion, ProposalID: "repair_wait_1", RunID: events[len(events)-1].RunID, NodeID: stage.NodeID, StageID: stage.ID, BaseBundleHashSHA256: context.SourceBundleHashSHA256, PolicyHashSHA256: context.EffectivePolicyHashSHA256, RepairKind: "wait_strategy", Field: "script_outline.stages[].wait_conditions", Before: stage.WaitConditions[0], After: "wait_after_entry_at_least_1500ms", Confidence: .95, EvidenceRefs: []model.EvidenceRef{{ID: "proposal_evidence", Kind: model.EvidenceKindBrowserTrace}}, CreatedAt: timeNowUTC()}, nil
}

func (v *stubStageVerifier) ValidateStageEvents(_ context.Context, context model.BrowserAgentValidationContext, events []model.StageExecutionEvent) (model.ValidationReport, error) {
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

func TestCompactGraphRequirementsForUploadDropsBlankNodeRefs(t *testing.T) {
	got := compactGraphRequirementsForUpload([]model.GraphRequirement{{
		ID: "requirement", Kind: "must_show", Required: true,
		NodeRefs: []string{"", "  ", "node_a", "node_a", "node_b"},
	}})
	if len(got) != 1 || len(got[0].NodeRefs) != 2 || got[0].NodeRefs[0] != "node_a" || got[0].NodeRefs[1] != "node_b" {
		t.Fatalf("blank or duplicate node refs survived upload compaction: %+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || string(encoded) == "null" {
		t.Fatalf("requirements were not serialized: %s", encoded)
	}
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

func refreshTestPackageApprovalDigests(t *testing.T, pkg *model.ClientExecutionPackage) {
	t.Helper()
	if pkg == nil || pkg.SafetyReport.HumanApproval.ApprovalID == "" {
		return
	}
	var err error
	pkg.SafetyReport.HumanApproval.SubjectDigestsSHA256, err = model.ComputePackageApprovalComponentDigests(*pkg)
	if err != nil {
		t.Fatal(err)
	}
	pkg.SafetyReport.HumanApproval.ApprovalSubjectDigestSHA256, err = model.ComputePackageApprovalSubjectDigest(*pkg)
	if err != nil {
		t.Fatal(err)
	}
}
