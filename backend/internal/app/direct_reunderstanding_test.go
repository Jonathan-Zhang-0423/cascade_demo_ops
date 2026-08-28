package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/experiment"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/store"
)

func TestPersistCloudResultDoesNotUpgradeOrdinaryBrowserActionFailure(t *testing.T) {
	service, states, state, build := newDirectReunderstandingTestState(t)
	result := directReunderstandingFailedResult(build, false)
	if err := service.persistCloudResult(t.Context(), state.ProjectID, defaultDesktopOrgID, result); err != nil {
		t.Fatal(err)
	}
	persisted, err := states.Load(t.Context(), state.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ExecutionPackageGeneration != 0 || persisted.DesktopCloudRun == nil || persisted.DesktopCloudRun.Stage != "failed" || persisted.DesktopCloudRun.BlockingErrorCode != "" || persisted.DesktopCloudRun.RequiresReapproval {
		t.Fatalf("ordinary Browser Agent action failure was incorrectly upgraded: %+v", persisted.DesktopCloudRun)
	}
}

func TestPersistCloudResultUpgradesStoppedRequiredAppAssertion(t *testing.T) {
	service, states, state, build := newDirectReunderstandingTestState(t)
	result := directReunderstandingFailedResult(build, false)
	result.ValidationReports = []model.ValidationReport{{
		Decision: model.ValidationDecisionStopAndReport,
		Checks: []model.ValidationCheck{{
			ID: "check_app_assertion", Code: "REQUIRED_ASSERTION_FAILED", NodeID: build.Package.ExecutableScriptBundle.PlanJSON.Steps[0].NodeID,
			Severity: model.FindingSeverityBlocking, Required: true, Summary: "approved App assertion did not match the real page", ResponsibilityDomain: model.ValidationCheckDomainApp,
		}},
	}}
	if err := service.persistCloudResult(t.Context(), state.ProjectID, defaultDesktopOrgID, result); err != nil {
		t.Fatal(err)
	}
	persisted, err := states.Load(t.Context(), state.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.DesktopCloudRun == nil || persisted.DesktopCloudRun.Stage != "reunderstanding_required" || persisted.DesktopCloudRun.BlockingErrorCode != "reunderstanding_required" || len(persisted.DesktopCloudRun.ReunderstandingIssues) != 1 {
		t.Fatalf("required App-owned stopped assertion was not promoted to formal re-understanding: %+v", persisted.DesktopCloudRun)
	}
}

func TestDirectReunderstandingIssuesDeduplicateByStableIdentity(t *testing.T) {
	issue := model.DirectReunderstandingIssue{Code: "REQUIRED_ASSERTION_FAILED", NodeID: "mode", Required: true, ResponsibilityDomain: model.ValidationCheckDomainApp}
	issues := withDirectIssueIDs([]model.DirectReunderstandingIssue{issue, issue})
	if len(issues) != 1 || issues[0].IssueID == "" {
		t.Fatalf("duplicate validation summaries must collapse to one stable issue: %+v", issues)
	}
}

func TestTerminalInteractionRepairGraphResumesObservedRouteWithoutReplayingWrites(t *testing.T) {
	const (
		projectID  = "project_terminal_repair"
		projectURL = "https://app.example.com/project/already-built"
	)
	node := func(id string, kind model.BusinessStageKind, action model.GraphActionType) *model.GraphNode {
		return &model.GraphNode{
			ID: id, Type: model.GraphNodeTypeAction, Title: id, Action: string(action), PageRef: "/project/:id",
			ActionSpec: &model.GraphAction{Type: action, Target: model.ActionTarget{URL: "https://app.example.com/project/:id"}},
			Metadata:   map[string]any{"business_stage_id": id, "business_stage_kind": string(kind), "non_destructive": true},
		}
	}
	graph := model.NewDemoWorkflowGraph("graph_original", projectID, "https://app.example.com/login")
	graph.Nodes = []*model.GraphNode{
		node("business_stage_session_setup", model.BusinessStageKindSessionSetup, model.GraphActionNavigate),
		node("business_stage_new_project_entry", model.BusinessStageKindBusinessAction, model.GraphActionClick),
		node("business_stage_final_observe", model.BusinessStageKindFinalObserve, model.GraphActionInspect),
		node("business_stage_interactive_surface_observe", model.BusinessStageKindFinalObserve, model.GraphActionInspect),
		node("business_stage_interactive_surface_change", model.BusinessStageKindFinalObserve, model.GraphActionPress),
	}
	state := &orchestrator.CascadeState{ProjectContext: &model.ProjectContext{ID: projectID, ProductURL: "https://app.example.com", ForbiddenPages: []string{"/admin"}}, WorkflowGraph: graph}
	result := model.RecordingResultPackage{FailureDiagnostic: &model.ScriptFailureDiagnostic{FailedNodeID: "business_stage_interactive_surface_change", CurrentURL: projectURL}}
	repaired, eligible, err := terminalInteractionVerificationRepairGraph(state, result, time.Date(2026, 8, 19, 17, 0, 0, 0, time.UTC))
	if err != nil || !eligible {
		t.Fatalf("terminal repair was not created: eligible=%v err=%v", eligible, err)
	}
	if repaired.ID == graph.ID || len(repaired.Nodes) != 4 || len(repaired.Edges) != 3 {
		t.Fatalf("terminal repair retained the wrong workflow shape: id=%q nodes=%d edges=%d", repaired.ID, len(repaired.Nodes), len(repaired.Edges))
	}
	resume := repaired.Nodes[1]
	if resume.ID != "business_stage_final_observe" || resume.ActionSpec.Type != model.GraphActionNavigate || resume.ActionSpec.Target.URL != projectURL {
		t.Fatalf("terminal repair does not navigate to the exact completed project: %+v", resume)
	}
	if repaired.Nodes[3].Type != model.GraphNodeTypeEnd {
		t.Fatalf("keyboard verification is not the terminal node: %+v", repaired.Nodes[3])
	}
	foreign := result
	foreign.FailureDiagnostic = &model.ScriptFailureDiagnostic{FailedNodeID: "business_stage_interactive_surface_change", CurrentURL: "https://evil.example/project/already-built"}
	if _, eligible, err := terminalInteractionVerificationRepairGraph(state, foreign, time.Now()); !eligible || err == nil {
		t.Fatalf("foreign failure URL was accepted: eligible=%v err=%v", eligible, err)
	}
	navigationFailure := model.RecordingResultPackage{FailureDiagnostic: &model.ScriptFailureDiagnostic{
		FailedNodeID: "business_stage_final_observe", FailedStepOrder: 2, CurrentURL: "https://app.example.com/app",
		Error: model.AgentError{Code: "browser_agent_observation_failed"},
	}}
	if _, eligible, err := terminalInteractionVerificationRepairGraph(&orchestrator.CascadeState{ProjectContext: state.ProjectContext, WorkflowGraph: repaired}, navigationFailure, time.Now()); !eligible || err == nil {
		t.Fatalf("a failed generic route restore must stop for re-understanding instead of synthesizing a site selector: eligible=%v err=%v", eligible, err)
	}
}

func TestAdaptiveSuccessorRepairSkipsMissingSubmitAndKeepsVerificationSuffix(t *testing.T) {
	const observedURL = "https://app.example.com/entity/already-created"
	node := func(id string, action model.GraphActionType, replay model.InteractionReplayPolicy) *model.GraphNode {
		value := &model.GraphNode{ID: id, Type: model.GraphNodeTypeAction, Action: string(action), ActionSpec: &model.GraphAction{Type: action}, PageRef: "/workspace"}
		if replay != "" {
			value.InteractionContract = &model.InteractionContract{ReplayPolicy: replay}
		}
		return value
	}
	graph := model.NewDemoWorkflowGraph("graph_before_successor", "project", "https://app.example.com/workspace")
	graph.Nodes = []*model.GraphNode{
		node("open_creation", model.GraphActionClick, model.InteractionReplayOnceEffect),
		node("fill_request", model.GraphActionFill, model.InteractionReplayIdempotentWrite),
		node("missing_submit", model.GraphActionClick, model.InteractionReplayOnceEffect),
		node("observe_progress", model.GraphActionWait, model.InteractionReplayObserveOnly),
		node("verify_surface", model.GraphActionInspect, model.InteractionReplayObserveOnly),
		node("prove_interaction", model.GraphActionPress, model.InteractionReplayIdempotentWrite),
	}
	bundle := &model.ExecutableRecordingScriptBundle{ScriptOutline: &model.BrowserAgentScriptOutline{Stages: []model.BrowserAgentOutlineStage{{NodeID: "missing_submit", StageKind: model.BusinessStageKindBusinessSubmit}}}}
	state := &orchestrator.CascadeState{
		ProjectContext:         &model.ProjectContext{ProductURL: "https://app.example.com"},
		WorkflowGraph:          graph,
		ExecutableScriptBundle: bundle,
	}
	result := model.RecordingResultPackage{FailureDiagnostic: &model.ScriptFailureDiagnostic{FailedNodeID: "missing_submit", CurrentURL: observedURL}}
	repaired, eligible, err := terminalInteractionVerificationRepairGraph(state, result, time.Now())
	if err != nil || !eligible {
		t.Fatalf("adaptive successor repair was not created: eligible=%t err=%v", eligible, err)
	}
	if len(repaired.Nodes) != 4 || repaired.Nodes[0].ActionSpec.Type != model.GraphActionNavigate || repaired.Nodes[0].ActionSpec.Target.URL != observedURL {
		t.Fatalf("repair did not begin at the observed entity: %+v", repaired.Nodes)
	}
	if repaired.Nodes[0].InteractionContract == nil || repaired.Nodes[0].InteractionContract.ReplayPolicy != model.InteractionReplayObserveOnly {
		t.Fatalf("successor navigation must remain observe-only: %+v", repaired.Nodes[0].InteractionContract)
	}
	for _, repairedNode := range repaired.Nodes {
		if repairedNode.ID == "open_creation" || repairedNode.ID == "fill_request" || (repairedNode.ActionSpec != nil && repairedNode.ActionSpec.Type == model.GraphActionClick) {
			t.Fatalf("repair retained a creation or submit action: %+v", repairedNode)
		}
	}
}

func TestTerminalInteractionRepairRestoresSingleNodeVerificationSuffix(t *testing.T) {
	node := &model.GraphNode{
		ID: "touch_verification", Type: model.GraphNodeTypeEnd, Action: string(model.GraphActionGesture),
		ActionSpec: &model.GraphAction{Type: model.GraphActionGesture},
		InteractionContract: &model.InteractionContract{
			ContractID: "touch_contract", ReplayPolicy: model.InteractionReplayIdempotentWrite,
		},
	}
	state := &orchestrator.CascadeState{
		ProjectContext: &model.ProjectContext{ProductURL: "https://app.example.com"},
		WorkflowGraph:  &model.DemoWorkflowGraph{ID: "graph_terminal_interaction_repair_trimmed", Nodes: []*model.GraphNode{node}},
	}
	result := model.RecordingResultPackage{FailureDiagnostic: &model.ScriptFailureDiagnostic{
		FailedNodeID: node.ID, CurrentURL: "https://app.example.com/entity/one",
	}}

	repaired, eligible, err := terminalInteractionVerificationRepairGraph(state, result, time.Now())
	if err != nil || !eligible || repaired == nil {
		t.Fatalf("single-node terminal proof was not recoverable: eligible=%v err=%v", eligible, err)
	}
	if len(repaired.Nodes) != 2 || repaired.Nodes[0].ActionSpec.Type != model.GraphActionNavigate || repaired.Nodes[1].ActionSpec.Type != model.GraphActionGesture {
		t.Fatalf("repair must retain navigation plus the interrupted verification: %+v", repaired.Nodes)
	}
	if repaired.Nodes[0].ID == repaired.Nodes[1].ID {
		t.Fatalf("repair synthesized duplicate node ids: %+v", repaired.Nodes)
	}
}

func TestSelectObservedSuccessorURLPrefersConcreteEntityBeforeWorkspaceDrift(t *testing.T) {
	project := &model.ProjectContext{ProductURL: "https://app.example.com/workspace"}
	events := []model.StageExecutionEvent{
		{Sequence: 10, Observation: &model.RuntimeObservation{URL: "https://app.example.com/workspace"}},
		{Sequence: 20, Observation: &model.RuntimeObservation{URL: "https://app.example.com/entity/runtime-42"}},
		{Sequence: 30, Observation: &model.RuntimeObservation{URL: "https://app.example.com/workspace"}},
	}
	if got := selectObservedSuccessorURL(project, "https://app.example.com/workspace", events); got != "https://app.example.com/entity/runtime-42" {
		t.Fatalf("workspace drift replaced the concrete observed entity: %q", got)
	}
}

func TestSelectObservedSuccessorURLDoesNotRegressWhenProjectContextAlreadyNamesEntity(t *testing.T) {
	entityURL := "https://app.example.com/entity/runtime-42"
	project := &model.ProjectContext{ProductURL: entityURL}
	events := []model.StageExecutionEvent{
		{Sequence: 10, Observation: &model.RuntimeObservation{URL: entityURL}},
		{Sequence: 20, Observation: &model.RuntimeObservation{URL: "https://app.example.com/workspace"}},
	}
	if got := selectObservedSuccessorURL(project, entityURL, events); got != entityURL {
		t.Fatalf("an authenticated workspace redirect replaced the bound entity: %q", got)
	}
}

func TestSelectAdaptiveBoundEntityURLRecoversConcreteHistoryAfterWorkspaceDrift(t *testing.T) {
	project := &model.ProjectContext{ProductURL: "https://app.example.com/workspace"}
	got := selectAdaptiveBoundEntityURL(project.ProductURL, project, []string{
		"https://app.example.com/workspace",
		"https://app.example.com/entity/runtime-42",
	})
	if got != "https://app.example.com/entity/runtime-42" {
		t.Fatalf("historical bound entity was not recovered: %q", got)
	}
}

func TestInsertPendingAdaptiveContinuationRestoresOnlySkippedUnstartedEffect(t *testing.T) {
	started := &model.GraphNode{ID: "continue_one", Type: model.GraphNodeTypeAction, ActionSpec: &model.GraphAction{Type: model.GraphActionClick, Parameters: map[string]any{"action_recipe": "continue_execution"}}}
	pending := &model.GraphNode{ID: "continue_two", Type: model.GraphNodeTypeAction, PageRef: "https://app.example.com/workspace", ActionSpec: &model.GraphAction{Type: model.GraphActionClick, Parameters: map[string]any{"action_recipe": "continue_execution"}}, InteractionContract: &model.InteractionContract{SchemaVersion: model.InteractionContractSchemaVersion, ContractID: "pending", ReplayPolicy: model.InteractionReplayOnceEffect}}
	source := model.NewDemoWorkflowGraph("source", "project", "https://app.example.com/workspace")
	source.Nodes = []*model.GraphNode{started, pending}
	resume := &model.GraphNode{ID: "resume", Type: model.GraphNodeTypeStart, ActionSpec: &model.GraphAction{Type: model.GraphActionNavigate}}
	verify := &model.GraphNode{ID: "verify", Type: model.GraphNodeTypeEnd, ActionSpec: &model.GraphAction{Type: model.GraphActionInspect}}
	repair := model.NewDemoWorkflowGraph("repair", "project", "https://app.example.com/entity/runtime-42")
	repair.Nodes = []*model.GraphNode{resume, verify}
	events := []model.StageExecutionEvent{
		{NodeID: "continue_one", EventType: model.StageExecutionEventActionStarted},
		{NodeID: "continue_two", EventType: model.StageExecutionEventStepSatisfied, HarnessDecision: &model.HarnessDecision{Kind: model.HarnessDecisionSkip}},
	}
	inserted, err := insertPendingAdaptiveContinuation(repair, source, events, "https://app.example.com/entity/runtime-42", "verify")
	if err != nil || !inserted {
		t.Fatalf("pending continuation was not restored: inserted=%t err=%v", inserted, err)
	}
	if len(repair.Nodes) != 3 || repair.Nodes[1].ID != "continue_two" || repair.Nodes[1].PageRef != "https://app.example.com/entity/runtime-42" {
		t.Fatalf("unexpected reconciliation graph: %+v", repair.Nodes)
	}
	for _, node := range repair.Nodes {
		if node.ID == "continue_one" {
			t.Fatal("an already-started continuation effect was restored")
		}
	}
}

func TestInsertPendingAdaptiveContinuationPrefersFirstSkippedEffect(t *testing.T) {
	primary := &model.GraphNode{ID: "primary_confirmation", Type: model.GraphNodeTypeAction, ActionSpec: &model.GraphAction{Type: model.GraphActionClick, Parameters: map[string]any{"action_recipe": "continue_execution"}}}
	followup := &model.GraphNode{ID: "followup_confirmation", Type: model.GraphNodeTypeAction, ActionSpec: &model.GraphAction{Type: model.GraphActionClick, Parameters: map[string]any{"action_recipe": "continue_execution"}}}
	source := model.NewDemoWorkflowGraph("source", "project", "https://app.example.com/workspace")
	source.Nodes = []*model.GraphNode{primary, followup}
	repair := model.NewDemoWorkflowGraph("repair", "project", "https://app.example.com/entity/runtime-42")
	repair.Nodes = []*model.GraphNode{{ID: "resume", Type: model.GraphNodeTypeStart, ActionSpec: &model.GraphAction{Type: model.GraphActionNavigate}}, {ID: "verify", Type: model.GraphNodeTypeEnd, ActionSpec: &model.GraphAction{Type: model.GraphActionInspect}}}
	events := []model.StageExecutionEvent{
		{NodeID: primary.ID, EventType: model.StageExecutionEventStepSatisfied, HarnessDecision: &model.HarnessDecision{Kind: model.HarnessDecisionSkip}},
		{NodeID: followup.ID, EventType: model.StageExecutionEventStepSatisfied, HarnessDecision: &model.HarnessDecision{Kind: model.HarnessDecisionSkip}},
	}
	inserted, err := insertPendingAdaptiveContinuation(repair, source, events, "https://app.example.com/entity/runtime-42", "verify")
	if err != nil || !inserted || len(repair.Nodes) != 3 || repair.Nodes[1].ID != primary.ID {
		t.Fatalf("earliest skipped continuation was not restored: inserted=%t nodes=%+v err=%v", inserted, repair.Nodes, err)
	}
}

func TestSynthesizeMissingAdaptiveContinuationForSubmittedPassiveStall(t *testing.T) {
	observedURL := "https://app.example.com/entity/runtime-42"
	evidence := model.EvidenceRef{ID: "evidence-runtime", Kind: "webpage_screenshot", Confidence: 1}
	submit := &model.GraphNode{ID: "submit", Type: model.GraphNodeTypeAction, ActionSpec: &model.GraphAction{Type: model.GraphActionClick}}
	failed := &model.GraphNode{ID: "surface", Type: model.GraphNodeTypeEnd, ActionSpec: &model.GraphAction{Type: model.GraphActionInspect}, InteractionContract: &model.InteractionContract{ReplayPolicy: model.InteractionReplayObserveOnly}}
	source := model.NewDemoWorkflowGraph("source", "project", "https://app.example.com/workspace")
	source.Nodes = []*model.GraphNode{submit, failed}
	repair := model.NewDemoWorkflowGraph("repair", "project", observedURL)
	repair.Nodes = []*model.GraphNode{
		{ID: "resume", Type: model.GraphNodeTypeStart, ActionSpec: &model.GraphAction{Type: model.GraphActionNavigate}},
		{ID: "verify", Type: model.GraphNodeTypeEnd, ActionSpec: &model.GraphAction{Type: model.GraphActionInspect}},
	}
	events := []model.StageExecutionEvent{{
		Sequence: 10, NodeID: submit.ID, EventType: model.StageExecutionEventActionCompleted,
		Observation:  &model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser, URL: observedURL},
		EvidenceRefs: []model.EvidenceRef{evidence},
	}}
	inserted, err := synthesizeMissingAdaptiveContinuation(repair, source, events, observedURL, failed.ID, experiment.ObservationPlan{DeferAfterMS: 1_800_000})
	if err != nil || !inserted {
		t.Fatalf("missing continuation was not synthesized: inserted=%t err=%v", inserted, err)
	}
	if len(repair.Nodes) != 3 || repair.Nodes[1].ID != "business_stage_runtime_execution_continuation" {
		t.Fatalf("unexpected synthesized repair graph: %+v", repair.Nodes)
	}
	pending := repair.Nodes[1]
	if pending.InteractionContract == nil || pending.InteractionContract.ReplayPolicy != model.InteractionReplayOnceEffect || pending.Metadata["adaptive_pending_continuation"] != true {
		t.Fatalf("synthesized continuation is not a once-effect: %+v", pending)
	}
	if pending.ActionSpec.Parameters["target_wait_timeout_ms"] != "1800000" || pending.ActionSpec.Parameters["action_recipe"] != "continue_execution" {
		t.Fatalf("synthesized continuation lost runtime policy: %+v", pending.ActionSpec.Parameters)
	}
	if err := model.ValidateInteractionContract(*pending.InteractionContract); err != nil {
		t.Fatalf("synthesized continuation contract is invalid: %v", err)
	}
	state := &orchestrator.CascadeState{ProjectIntelligence: &model.ProjectIntelligencePack{BusinessStagePlan: &model.BusinessStagePlan{}}}
	if err := syncAdaptiveBusinessStagePlan(state, repair); err != nil {
		t.Fatalf("synthesized continuation business plan did not synchronize: %v", err)
	}
	if got := pending.Metadata["business_route_state"]; got != string(model.BusinessRouteStateBuildRunning) {
		t.Fatalf("synthesized continuation route state was not canonicalized: %v", got)
	}
	if got := pending.Metadata["business_stage_kind"]; got != string(model.BusinessStageKindBusinessSubmit) {
		t.Fatalf("synthesized continuation stage kind changed: %v", got)
	}
}

func TestSynthesizeMissingAdaptiveContinuationRequiresAuditedSubmitEvidence(t *testing.T) {
	source := model.NewDemoWorkflowGraph("source", "project", "https://app.example.com/workspace")
	source.Nodes = []*model.GraphNode{
		{ID: "submit", ActionSpec: &model.GraphAction{Type: model.GraphActionClick}},
		{ID: "surface", ActionSpec: &model.GraphAction{Type: model.GraphActionInspect}},
	}
	repair := model.NewDemoWorkflowGraph("repair", "project", "https://app.example.com/entity/runtime-42")
	repair.Nodes = []*model.GraphNode{{ID: "resume", ActionSpec: &model.GraphAction{Type: model.GraphActionNavigate}}, {ID: "verify", ActionSpec: &model.GraphAction{Type: model.GraphActionInspect}}}
	inserted, err := synthesizeMissingAdaptiveContinuation(repair, source, nil, "https://app.example.com/entity/runtime-42", "surface", experiment.ObservationPlan{})
	if err != nil || inserted || len(repair.Nodes) != 2 {
		t.Fatalf("continuation was synthesized without submit evidence: inserted=%t nodes=%d err=%v", inserted, len(repair.Nodes), err)
	}
}

func TestInterruptedStageSelectionFallsBackToUnfinishedAction(t *testing.T) {
	events := []model.StageExecutionEvent{
		{Sequence: 1, NodeID: "completed", EventType: model.StageExecutionEventActionStarted},
		{Sequence: 2, NodeID: "completed", EventType: model.StageExecutionEventActionCompleted},
		{Sequence: 3, NodeID: "completed", EventType: model.StageExecutionEventStageCompleted},
		{Sequence: 4, NodeID: "interrupted", EventType: model.StageExecutionEventActionStarted},
		{Sequence: 5, NodeID: "completed", EventType: model.StageExecutionEventStageFailed, Observation: &model.RuntimeObservation{Source: model.RuntimeObservationInsufficient}},
	}
	failed := selectAdaptiveInterruptedStage(events)
	if failed.NodeID != "interrupted" {
		t.Fatalf("unfinished action was not selected: %+v", failed)
	}
}

func TestAdaptiveFailureDiagnosticAdvancesPastCompletedStaleStage(t *testing.T) {
	diagnostic := &model.ScriptFailureDiagnostic{FailedNodeID: "resume_route", FailedStepOrder: 2}
	events := []model.StageExecutionEvent{
		{Sequence: 10, NodeID: "resume_route", EventType: model.StageExecutionEventActionStarted},
		{Sequence: 11, NodeID: "resume_route", EventType: model.StageExecutionEventActionCompleted},
		{Sequence: 12, NodeID: "resume_route", EventType: model.StageExecutionEventStageCompleted},
		{Sequence: 13, NodeID: "surface_ready", EventType: model.StageExecutionEventActionStarted},
	}
	source := &model.ClientExecutionPackage{ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{PlanJSON: &model.ExecutionScriptDocument{Steps: []model.ScriptStep{
		{NodeID: "resume_route", Order: 2},
		{NodeID: "surface_ready", Order: 3},
	}}}}
	reconcileAdaptiveFailureDiagnosticFromEvents(diagnostic, events, source)
	if diagnostic.FailedNodeID != "surface_ready" || diagnostic.FailedStepOrder != 3 {
		t.Fatalf("stale completed diagnostic was not advanced: %+v", diagnostic)
	}

	actualFailure := &model.ScriptFailureDiagnostic{FailedNodeID: "resume_route", FailedStepOrder: 2}
	reconcileAdaptiveFailureDiagnosticFromEvents(actualFailure, events[:1], source)
	if actualFailure.FailedNodeID != "resume_route" || actualFailure.FailedStepOrder != 2 {
		t.Fatalf("an unfinished diagnostic was incorrectly replaced: %+v", actualFailure)
	}
}

func TestObservedContinuationStopsAncestorFallback(t *testing.T) {
	continuation := &model.GraphNode{ID: "confirmed", ActionSpec: &model.GraphAction{Type: model.GraphActionClick, Parameters: map[string]any{"action_recipe": "continue_execution"}}}
	source := model.NewDemoWorkflowGraph("source", "project", "https://app.example.com/entity")
	source.Nodes = []*model.GraphNode{continuation}
	if !adaptiveContinuationEffectObserved(source, []model.StageExecutionEvent{{NodeID: continuation.ID, EventType: model.StageExecutionEventActionCompleted}}) {
		t.Fatal("completed continuation did not block ancestor fallback")
	}
	if adaptiveContinuationEffectObserved(source, []model.StageExecutionEvent{{NodeID: continuation.ID, EventType: model.StageExecutionEventStepSatisfied, HarnessDecision: &model.HarnessDecision{Kind: model.HarnessDecisionSkip}}}) {
		t.Fatal("skipped continuation incorrectly blocked recovery from the parent package")
	}
}

func TestPostContinuationObservationBecomesReplayDenialResumePoint(t *testing.T) {
	continuation := &model.GraphNode{ID: "confirmed", ActionSpec: &model.GraphAction{Type: model.GraphActionClick, Parameters: map[string]any{"action_recipe": "continue_execution"}}}
	observation := &model.GraphNode{ID: "surface_ready", ActionSpec: &model.GraphAction{Type: model.GraphActionInspect}, InteractionContract: &model.InteractionContract{ReplayPolicy: model.InteractionReplayObserveOnly}}
	source := model.NewDemoWorkflowGraph("source", "project", "https://app.example.com/entity")
	source.Nodes = []*model.GraphNode{{ID: "resume", ActionSpec: &model.GraphAction{Type: model.GraphActionNavigate}}, continuation, observation}
	if got := adaptivePostContinuationObservationNode(source); got == nil || got.ID != observation.ID {
		t.Fatalf("post-continuation observation was not selected: %+v", got)
	}
}

func TestInsertPendingAdaptiveContinuationDoesNotReplayCompletedOnceEffect(t *testing.T) {
	continuation := &model.GraphNode{
		ID: "continue_execution", Type: model.GraphNodeTypeAction,
		ActionSpec:          &model.GraphAction{Type: model.GraphActionClick, Parameters: map[string]any{"action_recipe": "continue_execution"}},
		InteractionContract: &model.InteractionContract{SchemaVersion: model.InteractionContractSchemaVersion, ContractID: "continue", ReplayPolicy: model.InteractionReplayOnceEffect},
	}
	failedObserve := &model.GraphNode{
		ID: "observe_result", Type: model.GraphNodeTypeEnd, ActionSpec: &model.GraphAction{Type: model.GraphActionInspect},
		InteractionContract: &model.InteractionContract{SchemaVersion: model.InteractionContractSchemaVersion, ContractID: "observe", ReplayPolicy: model.InteractionReplayObserveOnly},
	}
	source := model.NewDemoWorkflowGraph("source", "project", "https://app.example.com/entity/runtime-42")
	source.Nodes = []*model.GraphNode{continuation, failedObserve}
	resume := &model.GraphNode{ID: "resume", Type: model.GraphNodeTypeStart, ActionSpec: &model.GraphAction{Type: model.GraphActionNavigate}}
	verify := &model.GraphNode{ID: "verify", Type: model.GraphNodeTypeEnd, ActionSpec: &model.GraphAction{Type: model.GraphActionInspect}}
	repair := model.NewDemoWorkflowGraph("repair", "project", "https://app.example.com/entity/runtime-42")
	repair.Nodes = []*model.GraphNode{resume, verify}
	events := []model.StageExecutionEvent{
		{NodeID: continuation.ID, EventType: model.StageExecutionEventActionStarted},
		{NodeID: continuation.ID, EventType: model.StageExecutionEventActionCompleted},
	}
	inserted, err := insertPendingAdaptiveContinuation(repair, source, events, "https://app.example.com/entity/runtime-42", failedObserve.ID)
	if err != nil || inserted || len(repair.Nodes) != 2 {
		t.Fatalf("completed once-effect was made replayable: inserted=%t nodes=%+v err=%v", inserted, repair.Nodes, err)
	}
}

func TestApplyAdaptiveInteractionContractsPreservesPendingContinuation(t *testing.T) {
	observedURL := "https://app.example.com/entity/runtime-42"
	resume := &model.GraphNode{ID: "resume", Type: model.GraphNodeTypeStart, PageRef: observedURL, ActionSpec: &model.GraphAction{Type: model.GraphActionNavigate, Target: model.ActionTarget{URL: observedURL}}, Metadata: map[string]any{"adaptive_successor_resume": true}}
	pending := &model.GraphNode{
		ID: "continue_two", Type: model.GraphNodeTypeAction, PageRef: observedURL,
		ActionSpec:          &model.GraphAction{Type: model.GraphActionClick, Target: model.ActionTarget{URL: observedURL}, Parameters: map[string]any{"action_recipe": "continue_execution"}},
		InteractionContract: &model.InteractionContract{SchemaVersion: model.InteractionContractSchemaVersion, ContractID: "pending", ActionKind: model.GraphActionClick, ReplayPolicy: model.InteractionReplayOnceEffect},
		Metadata:            map[string]any{"adaptive_pending_continuation": true},
	}
	legacyVerify := &model.GraphNode{ID: "legacy_verify", Type: model.GraphNodeTypeEnd, ActionSpec: &model.GraphAction{Type: model.GraphActionInspect}}
	graph := model.NewDemoWorkflowGraph("repair", "project", observedURL)
	graph.Nodes = []*model.GraphNode{resume, pending, legacyVerify}
	plan := experiment.InteractionPlan{
		SchemaVersion: experiment.InteractionPlanSchemaVersion,
		PlanID:        "plan-preserve-pending",
		SurfaceKind:   "runtime_discovered",
		Steps: []experiment.InteractionStep{
			{StepID: "surface_ready", SemanticIntent: "verify the observed interactive surface", ReplayPolicy: experiment.ReplayObserveOnly, ExpectedChanges: []string{"visual"}, EvidenceSlots: []string{"surface"}, ProofRequirements: []experiment.ProofRequirement{{Kind: "all_evidence_slots"}}, Action: experiment.InteractionAction{Kind: "observe", TargetSemanticID: "surface"}},
			{StepID: "surface_stable", SemanticIntent: "verify the observed surface remains available", ReplayPolicy: experiment.ReplayObserveOnly, ExpectedChanges: []string{"visual"}, EvidenceSlots: []string{"stable"}, ProofRequirements: []experiment.ProofRequirement{{Kind: "all_evidence_slots"}}, Action: experiment.InteractionAction{Kind: "observe", TargetSemanticID: "surface"}},
		},
	}
	observationPlan := experiment.ObservationPlan{SchemaVersion: experiment.ObservationPlanSchemaVersion}
	if err := applyAdaptiveInteractionContracts(graph, plan, observationPlan, observedURL, nil); err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 4 || graph.Nodes[1].ID != pending.ID || !isAdaptivePendingContinuationGraphNode(graph.Nodes[1]) {
		t.Fatalf("pending once-effect was dropped while rebuilding the proof suffix: %+v", graph.Nodes)
	}
	if graph.Nodes[2].InteractionContract == nil || graph.Nodes[2].InteractionContract.ContractID != "experiment_interaction_surface_ready" {
		t.Fatalf("interaction proof suffix was not appended after the pending effect: %+v", graph.Nodes)
	}
}

func TestApplyAdaptiveInteractionContractsRenamesPromotedResumeCollision(t *testing.T) {
	observedURL := "https://app.example.com/entity/runtime-42"
	contractID := "experiment_interaction_touch"
	resume := &model.GraphNode{
		ID: "business_stage_contract_" + contractID, Type: model.GraphNodeTypeStart, PageRef: observedURL,
		ActionSpec: &model.GraphAction{Type: model.GraphActionNavigate, Target: model.ActionTarget{URL: observedURL}},
		Metadata: map[string]any{
			"adaptive_successor_resume": true,
			"business_stage_id":         "business_stage_contract_" + contractID,
		},
	}
	verify := &model.GraphNode{
		ID: "business_stage_contract_experiment_interaction_terminal_scenes", Type: model.GraphNodeTypeEnd,
		ActionSpec:          &model.GraphAction{Type: model.GraphActionClick},
		InteractionContract: &model.InteractionContract{SchemaVersion: model.InteractionContractSchemaVersion, ContractID: "experiment_interaction_terminal_scenes", ActionKind: model.GraphActionClick, ReplayPolicy: model.InteractionReplayIdempotentWrite, NonDestructive: true},
	}
	graph := model.NewDemoWorkflowGraph("repair", "project", observedURL)
	graph.Nodes = []*model.GraphNode{resume, verify}
	plan := experiment.InteractionPlan{
		SchemaVersion: experiment.InteractionPlanSchemaVersion, PlanID: "plan-resume-collision", SurfaceKind: "runtime_discovered",
		Steps: []experiment.InteractionStep{
			{StepID: "touch", SemanticIntent: "verify touch input", ReplayPolicy: experiment.ReplayObserveOnly, ExpectedChanges: []string{"visual"}, EvidenceSlots: []string{"touch"}, ProofRequirements: []experiment.ProofRequirement{{Kind: "all_evidence_slots"}}, Action: experiment.InteractionAction{Kind: "observe", TargetSemanticID: "surface"}},
			{StepID: "terminal_scenes", SemanticIntent: "verify terminal states", ReplayPolicy: experiment.ReplayIdempotentWrite, ExpectedChanges: []string{"visual"}, EvidenceSlots: []string{"victory", "terminal"}, ProofRequirements: []experiment.ProofRequirement{{Kind: "state_variants", MinCount: 2}}, Action: experiment.InteractionAction{Kind: "activate_state_variants", TargetSemanticID: "state_control", AllowedRoles: []string{"button"}, AllowedNames: []string{"demo"}}},
		},
	}
	if err := applyAdaptiveInteractionContracts(graph, plan, experiment.ObservationPlan{SchemaVersion: experiment.ObservationPlanSchemaVersion}, observedURL, nil); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, node := range graph.Nodes {
		if seen[node.ID] {
			t.Fatalf("adaptive continuation contains duplicate node id %q: %+v", node.ID, graph.Nodes)
		}
		seen[node.ID] = true
		if stageID, _ := node.Metadata["business_stage_id"].(string); stageID != node.ID {
			t.Fatalf("node and business stage identities diverged: node=%s stage=%s", node.ID, stageID)
		}
	}
	if graph.Nodes[0].ID != "business_stage_contract_experiment_interaction_touch_resume" {
		t.Fatalf("promoted route resume did not receive a distinct identity: %s", graph.Nodes[0].ID)
	}
	if !seen["business_stage_contract_experiment_interaction_touch"] {
		t.Fatal("frozen touch proof was not rebuilt after the resume")
	}
}

func TestPrependReusableSessionSetupRestoresOnlyApprovedAuthentication(t *testing.T) {
	evidence := model.EvidenceRef{ID: "evidence_login_form", Kind: "page_snapshot", ArtifactID: "login-form"}
	sourceGraph := model.NewDemoWorkflowGraph("source", "project", "https://app.example.com/login")
	sourceGraph.Nodes = []*model.GraphNode{{
		ID: "session", Type: model.GraphNodeTypeStart, Action: "inspect", Selector: "[data-stale-login-projection]", Metadata: map[string]any{
			"business_stage_kind": string(model.BusinessStageKindSessionSetup),
		},
	}}
	source := model.ClientExecutionPackage{
		ProjectContextSummary: model.ProjectContextSummary{ProductURL: "https://app.example.com"},
		WorkflowGraph:         sourceGraph,
		CredentialGrants:      []model.CredentialGrant{{CloudSecretRef: "credential://demo/session"}},
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{PlanJSON: &model.ExecutionScriptDocument{Steps: []model.ScriptStep{{
			NodeID: "session", StageKind: model.BusinessStageKindSessionSetup, RouteState: model.BusinessRouteStateWorkspace,
			Title: "Restore session", BusinessValue: "Reuse the approved authenticated session.", NonDestructive: true,
			PageTarget: model.ScriptPageTarget{URL: "https://app.example.com/login"},
			Action: model.ScriptActionInstruction{Type: model.GraphActionInspect, SecretRef: "credential://demo/session", Target: model.ActionTarget{
				URL: "https://app.example.com/login", SelectorAlternatives: []model.SelectorCandidate{{Kind: "css", Value: "[data-runtime-login]", EvidenceID: evidence.ID, EvidenceRefs: []model.EvidenceRef{evidence}}},
			}},
			ExpectedOutcome: "authenticated workspace visible", EvidenceRefs: []model.EvidenceRef{evidence},
		}}}},
	}
	destination := model.NewDemoWorkflowGraph("continuation", "project", "https://app.example.com/entity/current")
	destination.Nodes = []*model.GraphNode{{ID: "resume", ActionSpec: &model.GraphAction{Type: model.GraphActionNavigate}}, {ID: "verify", ActionSpec: &model.GraphAction{Type: model.GraphActionInspect}}}
	added, err := prependReusableSessionSetup(destination, source)
	if err != nil || !added {
		t.Fatalf("approved session setup was not restored: added=%t err=%v", added, err)
	}
	if len(destination.Nodes) != 3 || destination.Nodes[0].ID != "session" || destination.Nodes[0].ActionSpec == nil || destination.Nodes[0].ActionSpec.SecretRef != "credential://demo/session" {
		t.Fatalf("restored session step lost its approved action: %+v", destination.Nodes)
	}
	if len(destination.Nodes[0].ActionSpec.Target.SelectorAlternatives) != 1 || destination.Nodes[1].ID != "resume" {
		t.Fatalf("session provenance or continuation ordering changed: %+v", destination.Nodes)
	}
	if adaptive, _ := destination.Nodes[0].Metadata["runtime_adaptive"].(bool); !adaptive {
		t.Fatalf("restored session lost runtime-adaptive login discovery: %+v", destination.Nodes[0].Metadata)
	}
	if destination.Nodes[0].Selector != "" {
		t.Fatalf("restored session promoted a compact graph selector over the approved executable target: %q", destination.Nodes[0].Selector)
	}
	if destination.Nodes[0].Metadata["business_stage_entry_route"] != "https://app.example.com/login" {
		t.Fatalf("restored session lost its explicit authentication entry route: %+v", destination.Nodes[0].Metadata)
	}
	for _, node := range destination.Nodes[1:] {
		if node.ActionSpec != nil && (node.ActionSpec.Type == model.GraphActionClick || node.ActionSpec.Type == model.GraphActionFill || node.ActionSpec.Type == model.GraphActionSelect) {
			t.Fatalf("session restoration introduced a business write: %+v", node)
		}
	}
}

func TestHydrateAdaptiveSourceGraphRestoresCompactedExecutableAction(t *testing.T) {
	graph := model.NewDemoWorkflowGraph("source", "project", "https://app.example.com/workspace")
	graph.Nodes = []*model.GraphNode{{ID: "submit", Action: "click", Metadata: map[string]any{"business_stage_kind": string(model.BusinessStageKindBusinessSubmit)}}}
	source := model.ClientExecutionPackage{
		WorkflowGraph: graph,
		ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{PlanJSON: &model.ExecutionScriptDocument{Steps: []model.ScriptStep{{
			NodeID: "submit", StageKind: model.BusinessStageKindBusinessSubmit, RouteState: model.BusinessRouteStateWorkspace,
			Action:          model.ScriptActionInstruction{Type: model.GraphActionClick, Target: model.ActionTarget{Role: "button", EvidenceRefs: []model.EvidenceRef{{ID: "submit-evidence"}}}},
			ExpectedOutcome: "successor entity exists", Validations: []model.ValidationSpec{{ID: "successor", Kind: "url_matches", Expected: "/entity/:id", Required: true}},
		}}}},
	}
	hydrated, err := hydrateAdaptiveSourceGraph(source)
	if err != nil {
		t.Fatal(err)
	}
	if hydrated == graph || len(hydrated.Nodes) != 1 || hydrated.Nodes[0].ActionSpec == nil || hydrated.Nodes[0].ActionSpec.Type != model.GraphActionClick || hydrated.Nodes[0].ActionSpec.Target.Role != "button" {
		t.Fatalf("compacted graph action was not hydrated: %+v", hydrated)
	}
	if graph.Nodes[0].ActionSpec != nil {
		t.Fatal("source package graph was mutated during hydration")
	}
}

func TestSyncAdaptiveBusinessStagePlanKeepsDistinctLoginSuccessRoute(t *testing.T) {
	state := &orchestrator.CascadeState{ProjectIntelligence: &model.ProjectIntelligencePack{BusinessStagePlan: &model.BusinessStagePlan{}}}
	graph := model.NewDemoWorkflowGraph("continuation", "project", "https://app.example.com/entity/current")
	graph.Nodes = []*model.GraphNode{{
		ID: "session", Title: "Restore session", Goal: "Log in", PageRef: "/login", ActionSpec: &model.GraphAction{Type: model.GraphActionInspect},
		Validations: []model.ValidationSpec{{ID: "login_success", Kind: "url_matches", Target: model.ActionTarget{URL: "/app"}, Required: true}},
		Metadata:    map[string]any{"business_stage_kind": string(model.BusinessStageKindSessionSetup), "business_route_state": string(model.BusinessRouteStateUnauthenticated)},
	}, {
		ID: "resume", PageRef: "/entity/current", ActionSpec: &model.GraphAction{Type: model.GraphActionNavigate},
	}}
	if err := syncAdaptiveBusinessStagePlan(state, graph); err != nil {
		t.Fatal(err)
	}
	stage := state.ProjectIntelligence.BusinessStagePlan.Stages[0]
	if stage.EntryRoute != "/login" || stage.ExpectedRouteAfterAction != "/app" {
		t.Fatalf("login route transition collapsed during reconciliation: %+v", stage)
	}
}

func TestNormalizeAdaptiveReconciliationResumeAcceptsTerminalInteractionFailure(t *testing.T) {
	graph := model.NewDemoWorkflowGraph("graph_terminal_interaction_repair_1", "project", "https://app.example.com/workspace")
	graph.Nodes = []*model.GraphNode{{
		ID: "resume_terminal", ActionSpec: &model.GraphAction{Type: model.GraphActionNavigate, Target: model.ActionTarget{URL: "https://app.example.com/old"}, Parameters: map[string]any{"capability_layer": "core", "capability_score": 15}},
		InteractionContract: &model.InteractionContract{ContractID: "stale_interaction_contract", ReplayPolicy: model.InteractionReplayIdempotentWrite},
		Metadata:            map[string]any{"terminal_repair_resume": true},
	}, {
		ID: "verify", ActionSpec: &model.GraphAction{Type: model.GraphActionInspect},
	}}
	if !adaptiveReconciliationGraphSupported(graph) {
		t.Fatal("terminal interaction repair should be eligible for adaptive reconciliation")
	}
	observedURL := "https://app.example.com/entity/already-created"
	if err := normalizeAdaptiveReconciliationResume(graph, observedURL, []model.EvidenceRef{{ID: "runtime-route"}}); err != nil {
		t.Fatal(err)
	}
	resume := graph.Nodes[0]
	if resume.PageRef != observedURL || resume.ActionSpec.Target.URL != observedURL || resume.InteractionContract == nil || resume.InteractionContract.ReplayPolicy != model.InteractionReplayObserveOnly {
		t.Fatalf("terminal resume was not normalized to the observed entity: %+v", resume)
	}
	if len(resume.ActionSpec.Parameters) != 0 {
		t.Fatalf("recovery navigation retained capability weight from the promoted source proof: %+v", resume.ActionSpec.Parameters)
	}
	if value, _ := resume.Metadata["adaptive_successor_resume"].(bool); !value {
		t.Fatalf("normalized resume is not recognized by the adaptive graph compiler: %+v", resume.Metadata)
	}
	if adaptive, _ := resume.Metadata["runtime_adaptive"].(bool); !adaptive {
		t.Fatalf("normalized resume lost runtime page discovery authority: %+v", resume.Metadata)
	}
}

func TestTerminalInteractionRepairAcceptsOptionalEnhancementClick(t *testing.T) {
	now := timeNowUTC()
	resume := &model.GraphNode{
		ID: "resume", Type: model.GraphNodeTypeStart,
		ActionSpec: &model.GraphAction{Type: model.GraphActionNavigate, Target: model.ActionTarget{URL: "https://app.example.com/entity/1"}},
	}
	enhancement := &model.GraphNode{
		ID: "optional-control", Type: model.GraphNodeTypeEnd,
		ActionSpec:          &model.GraphAction{Type: model.GraphActionClick, Parameters: map[string]any{"capability_layer": "enhancement", "capability_score": 10}},
		InteractionContract: &model.InteractionContract{Parameters: map[string]any{"capability_layer": "enhancement", "capability_score": 10}},
	}
	state := &orchestrator.CascadeState{
		ProjectContext: &model.ProjectContext{ID: "project", ProductURL: "https://app.example.com/entity/1"},
		WorkflowGraph:  &model.DemoWorkflowGraph{ID: "graph", Nodes: []*model.GraphNode{resume, enhancement}},
	}
	result := model.RecordingResultPackage{FailureDiagnostic: &model.ScriptFailureDiagnostic{FailedNodeID: enhancement.ID, CurrentURL: "https://app.example.com/entity/1"}}
	graph, eligible, err := terminalInteractionVerificationRepairGraph(state, result, now)
	if err != nil || !eligible || graph == nil {
		t.Fatalf("optional enhancement click should reconcile as an observed terminal capability: eligible=%v err=%v graph=%+v", eligible, err, graph)
	}
	if len(graph.Nodes) < 2 || graph.Nodes[len(graph.Nodes)-1].ActionSpec.Type != model.GraphActionNavigate {
		t.Fatalf("enhancement click must be converted to an observed route restore, not replayed: %+v", graph.Nodes)
	}
}

func TestTerminalInteractionRepairReplaysOnlyApprovedStateVariantProof(t *testing.T) {
	now := timeNowUTC()
	stateVariant := &model.GraphNode{
		ID: "state-variants", Type: model.GraphNodeTypeEnd,
		ActionSpec: &model.GraphAction{Type: model.GraphActionClick, Parameters: map[string]any{"action_recipe": "activate_state_variants", "capability_layer": "core"}},
		InteractionContract: &model.InteractionContract{
			ReplayPolicy: model.InteractionReplayIdempotentWrite, NonDestructive: true,
			Parameters: map[string]any{"action_recipe": "activate_state_variants", "capability_layer": "core"},
		},
	}
	state := &orchestrator.CascadeState{
		ProjectContext: &model.ProjectContext{ID: "project", ProductURL: "https://app.example.com/entity/1"},
		WorkflowGraph:  &model.DemoWorkflowGraph{ID: "graph", Nodes: []*model.GraphNode{stateVariant}},
	}
	result := model.RecordingResultPackage{FailureDiagnostic: &model.ScriptFailureDiagnostic{FailedNodeID: stateVariant.ID, CurrentURL: "https://app.example.com/entity/1"}}
	graph, eligible, err := terminalInteractionVerificationRepairGraph(state, result, now)
	if err != nil || !eligible || graph == nil {
		t.Fatalf("approved state-variant proof should reconcile: eligible=%v err=%v graph=%+v", eligible, err, graph)
	}
	if len(graph.Nodes) != 2 || graph.Nodes[0].ActionSpec.Type != model.GraphActionNavigate || !replayableStateVariantProof(graph.Nodes[1]) {
		t.Fatalf("repair must navigate to the bound entity and replay only the idempotent state proof: %+v", graph.Nodes)
	}

	unsafe := *stateVariant
	unsafe.InteractionContract = &model.InteractionContract{ReplayPolicy: model.InteractionReplayOnceEffect, NonDestructive: true}
	unsafeState := &orchestrator.CascadeState{ProjectContext: state.ProjectContext, WorkflowGraph: &model.DemoWorkflowGraph{ID: "graph", Nodes: []*model.GraphNode{&unsafe}}}
	if _, eligible, err := terminalInteractionVerificationRepairGraph(unsafeState, result, now); err != nil || eligible {
		t.Fatalf("once-effect state control must not be admitted for automatic replay: eligible=%v err=%v", eligible, err)
	}
}

func TestTerminalStateVariantFallbackIgnoresEarlierDiagnosticBoundary(t *testing.T) {
	stateVariant := &model.GraphNode{
		ID: "state-variants", Type: model.GraphNodeTypeEnd,
		ActionSpec: &model.GraphAction{Type: model.GraphActionClick, Parameters: map[string]any{"action_recipe": "activate_state_variants"}},
		InteractionContract: &model.InteractionContract{
			ReplayPolicy: model.InteractionReplayIdempotentWrite, NonDestructive: true,
			Parameters: map[string]any{"action_recipe": "activate_state_variants"},
		},
	}
	earlier := &model.GraphNode{ID: "earlier-core-click", ActionSpec: &model.GraphAction{Type: model.GraphActionClick}}
	state := &orchestrator.CascadeState{
		ProjectContext: &model.ProjectContext{ID: "project", ProductURL: "https://app.example.com/entity/1"},
		WorkflowGraph:  &model.DemoWorkflowGraph{ID: "graph", Nodes: []*model.GraphNode{earlier, stateVariant}},
	}
	result := model.RecordingResultPackage{FailureDiagnostic: &model.ScriptFailureDiagnostic{FailedNodeID: earlier.ID, CurrentURL: "https://app.example.com/entity/1"}}
	graph, eligible, err := terminalStateVariantReconciliationFallback(state, result, stateVariant.ID, timeNowUTC())
	if err != nil || !eligible || graph == nil || len(graph.Nodes) != 2 || !replayableStateVariantProof(graph.Nodes[1]) {
		t.Fatalf("fallback did not recover the actual stopped state proof: eligible=%v err=%v graph=%+v", eligible, err, graph)
	}
}

func TestReconcileAdaptiveCapabilityScoreUsesLatestCompletedCoreStage(t *testing.T) {
	core := &model.GraphNode{
		ID: "core-stability", ActionSpec: &model.GraphAction{Type: model.GraphActionInspect, Parameters: map[string]any{"capability_layer": "core", "capability_score": 15}},
		InteractionContract: &model.InteractionContract{Parameters: map[string]any{"capability_layer": "core", "capability_score": 15}},
	}
	enhancement := &model.GraphNode{
		ID: "optional-undo", ActionSpec: &model.GraphAction{Type: model.GraphActionClick, Parameters: map[string]any{"capability_layer": "enhancement", "capability_score": 10}},
	}
	diagnostic := &model.ScriptFailureDiagnostic{FailedNodeID: enhancement.ID, FailedStepOrder: 7}
	events := []model.StageExecutionEvent{
		{Sequence: 10, NodeID: core.ID, EventType: model.StageExecutionEventStageCompleted, Observation: &model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser}},
		{Sequence: 20, NodeID: enhancement.ID, EventType: model.StageExecutionEventStageCompleted, Observation: &model.RuntimeObservation{Source: model.RuntimeObservationActualBrowser}},
		{Sequence: 21, EventType: model.StageExecutionEventCapabilityScored, CapabilityScore: &model.CapabilityScore{CorePassed: true, EligibleForFilm: true}},
	}
	source := &model.ClientExecutionPackage{ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{PlanJSON: &model.ExecutionScriptDocument{Steps: []model.ScriptStep{{NodeID: core.ID, Order: 6}}}}}
	reconcileAdaptiveCapabilityScoreFromEvents(diagnostic, events, &model.DemoWorkflowGraph{Nodes: []*model.GraphNode{core, enhancement}}, source)
	if diagnostic.FailedNodeID != core.ID || diagnostic.FailedStepOrder != 6 {
		t.Fatalf("completed core score did not move recovery to the safe core boundary: %+v", diagnostic)
	}
}

func TestPrepareAdaptiveDirectReconciliationBuildsObserveOnlyPackage(t *testing.T) {
	service, states, state, build := newDirectReunderstandingTestState(t)
	result := directReunderstandingFailedResult(build, false)
	failedNode := ""
	for _, stage := range build.Package.ExecutableScriptBundle.ScriptOutline.Stages {
		if stage.StageKind == model.BusinessStageKindBusinessSubmit {
			failedNode = stage.NodeID
			break
		}
	}
	if failedNode == "" {
		t.Fatal("fixture has no business submit stage")
	}
	result.FailureDiagnostic.FailedNodeID = failedNode
	result.FailureDiagnostic.CurrentURL = strings.TrimSuffix(state.ProjectContext.ProductURL, "/") + "/result/already-created"
	result.FailureDiagnostic.ScreenshotRefs = []model.PackageArtifactDescriptor{{ID: "observed_successor", Kind: "screenshot", URI: "artifact://observed-successor", SHA256: strings.Repeat("a", 64), SizeBytes: 128}}
	if err := service.persistCloudResult(t.Context(), state.ProjectID, defaultDesktopOrgID, result); err != nil {
		t.Fatal(err)
	}
	definition, err := experiment.LoadDefinition("../../../experiments", "2048-v2")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := service.prepareAdaptiveDirectReconciliation(t.Context(), state.ProjectID, result.CloudJobID, definition.InteractionPlan, definition.ObservationPlan)
	if err != nil {
		persisted, _ := states.Load(t.Context(), state.ProjectID)
		var terminal any
		var nonDestructive any
		if persisted != nil && persisted.ExecutableScriptBundle != nil && persisted.ExecutableScriptBundle.PlanJSON != nil {
			for _, step := range persisted.ExecutableScriptBundle.PlanJSON.Steps {
				if strings.Contains(step.NodeID, "terminal_scenes") {
					terminal = step.Validations
				}
			}
			bundle := persisted.ExecutableScriptBundle
			for index, step := range bundle.PlanJSON.Steps {
				if strings.Contains(step.NodeID, "surface_ready") && bundle.StageApprovalPlan != nil && bundle.ScriptOutline != nil {
					nonDestructive = []any{step.NonDestructive, bundle.StageApprovalPlan.Stages[index].Interaction.NonDestructive, bundle.ScriptOutline.Stages[index].Interactions[0].NonDestructive}
				}
			}
		}
		t.Fatalf("%v terminal_validations=%+v non_destructive=%+v", err, terminal, nonDestructive)
	}
	if prepared.Build.Package.ExecutableScriptBundle == nil || prepared.Build.Package.ExecutableScriptBundle.ScriptOutline == nil {
		t.Fatal("adaptive reconciliation package is incomplete")
	}
	if prepared.Build.Package.ConfidenceSummary == nil || prepared.Build.Package.ConfidenceSummary.Readiness == model.PackageReadinessBlocked {
		t.Fatalf("adaptive reconciliation package is blocked: %+v", prepared.Build.Package.ConfidenceSummary)
	}
	seenCapabilities := 0
	seenObserveOnlyResume := false
	for _, stage := range prepared.Build.Package.ExecutableScriptBundle.ScriptOutline.Stages {
		if stage.NodeID == "business_stage_observe_agent_progress" || stage.NodeID == "business_stage_final_observe" {
			t.Fatalf("continuation package retained an unbound legacy observation placeholder: %+v", stage)
		}
		if stage.StageKind == model.BusinessStageKindBusinessSubmit || stage.StageKind == model.BusinessStageKindBusinessInput || stage.StageKind == model.BusinessStageKindModeSelection {
			t.Fatalf("continuation package retained a creation write stage: %+v", stage)
		}
		if len(stage.Interactions) > 0 {
			if stage.Interactions[0].Kind == model.GraphActionNavigate && stage.InteractionContract != nil && stage.InteractionContract.ReplayPolicy == model.InteractionReplayObserveOnly {
				seenObserveOnlyResume = true
			}
			if _, ok := stage.Interactions[0].Parameters["capability_layer"]; ok {
				seenCapabilities++
			}
		}
	}
	if seenCapabilities != len(definition.InteractionPlan.Steps) {
		t.Fatalf("continuation package capability contracts=%d want=%d", seenCapabilities, len(definition.InteractionPlan.Steps))
	}
	if !seenObserveOnlyResume {
		t.Fatal("continuation package dropped its observe-only successor navigation")
	}
}

func TestDirectIssuesPromoteTerminalRepairNavigationFailure(t *testing.T) {
	bundle := &model.ExecutableRecordingScriptBundle{RepairLineage: &model.ScriptRepairLineage{SourceResultID: "source_result"}}
	result := model.RecordingResultPackage{FailureDiagnostic: &model.ScriptFailureDiagnostic{
		FailedNodeID: "business_stage_final_observe", FailedStepOrder: 2,
		Error: model.AgentError{Code: "browser_agent_observation_failed"},
	}}
	issues := directIssuesFromFailedResult(result, bundle)
	if len(issues) != 1 || !issues[0].Required || issues[0].ResponsibilityDomain != model.ValidationCheckDomainApp || issues[0].StageID != "stage_step_02_business_stage_final_observe" {
		t.Fatalf("terminal repair navigation failure was not promoted to an App repair issue: %+v", issues)
	}
}

func TestDirectFailureReunderstandingLifecycleAndIdempotency(t *testing.T) {
	service, states, state, build := newDirectReunderstandingTestState(t)
	service.approvedBuilds[state.ProjectID+"|old"] = approvedBuildCacheEntry{Build: build}
	result := directReunderstandingFailedResult(build, true)
	if err := service.persistCloudResult(t.Context(), state.ProjectID, defaultDesktopOrgID, result); err != nil {
		t.Fatal(err)
	}
	failed, err := states.Load(t.Context(), state.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.ExecutionPackageGeneration != 1 || failed.Approved || failed.DesktopCloudRun == nil || failed.DesktopCloudRun.BlockingErrorCode != "reunderstanding_required" || len(service.approvedBuilds) != 0 {
		t.Fatalf("authoritative App failure did not invalidate the old approval once: generation=%d run=%+v cache=%d", failed.ExecutionPackageGeneration, failed.DesktopCloudRun, len(service.approvedBuilds))
	}
	if err := service.persistCloudResult(t.Context(), state.ProjectID, defaultDesktopOrgID, result); err != nil {
		t.Fatal(err)
	}
	repeatedFailure, _ := states.Load(t.Context(), state.ProjectID)
	if repeatedFailure.ExecutionPackageGeneration != 1 {
		t.Fatalf("duplicate source_result_id incremented generation: %d", repeatedFailure.ExecutionPackageGeneration)
	}

	request := directReunderstandingRequest(t, repeatedFailure)
	stale := request
	stale.BaseGraphDigestSHA256 = "sha256:stale"
	if _, err := service.ReunderstandDirectBrowserAgentFailure(t.Context(), state.ProjectID, stale); bridgeErrorCode(err) != "package_preview_stale" {
		t.Fatalf("stale failed lineage was accepted: code=%q err=%v", bridgeErrorCode(err), err)
	}

	repaired, err := service.ReunderstandDirectBrowserAgentFailure(t.Context(), state.ProjectID, request)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Build == nil || repaired.NewPackageID == "" || repaired.NewPackageID == build.Package.PackageID || !repaired.RequiresReapproval || repaired.RepairLineage == nil || repaired.RepairLineage.SourceResultID != result.ResultID {
		t.Fatalf("reunderstanding did not produce a traceable unapproved draft: %+v", repaired)
	}
	if repaired.State == nil || repaired.State.Approved || repaired.State.CurrentNode != orchestrator.NodeHumanApprove || repaired.State.DesktopCloudRun == nil || repaired.State.DesktopCloudRun.Status != "not_uploaded" || len(repaired.State.DesktopCloudRun.RepairHistory) != 1 {
		t.Fatalf("reunderstanding state did not reset approval/upload lifecycle: %+v", repaired.State)
	}
	if repaired.State.DesktopCloudRun.OrgID != defaultDesktopOrgID {
		t.Fatalf("reunderstanding did not preserve the Direct organization binding: %+v", repaired.State.DesktopCloudRun)
	}

	idempotent, err := service.ReunderstandDirectBrowserAgentFailure(t.Context(), state.ProjectID, request)
	if err != nil || idempotent.NewPackageID != repaired.NewPackageID || idempotent.PackageDigestSHA256 != repaired.PackageDigestSHA256 {
		t.Fatalf("same idempotency request did not return the same draft: result=%+v err=%v", idempotent, err)
	}
	repaired.State.DesktopCloudRun.Stage = "reunderstanding_incomplete"
	repaired.State.DesktopCloudRun.Status = "failed"
	repaired.State.DesktopCloudRun.BlockingErrorCode = "reunderstanding_incomplete"
	repaired.State.DesktopCloudRun.PackageID = ""
	if err := states.Save(t.Context(), repaired.State); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.ReunderstandDirectBrowserAgentFailure(t.Context(), state.ProjectID, request)
	if err != nil || recovered.State.DesktopCloudRun.Stage != "local_generated" || recovered.State.DesktopCloudRun.Status != "not_uploaded" || recovered.State.DesktopCloudRun.BlockingErrorCode != "" || recovered.State.DesktopCloudRun.PackageID != repaired.NewPackageID {
		t.Fatalf("idempotent repair did not recover persisted package-gate state: result=%+v err=%v", recovered, err)
	}
	bridge := &DesktopBridge{service: service}
	wails := bridge.ReunderstandDirectBrowserAgentFailure(state.ProjectID, request)
	if !wails.OK {
		t.Fatalf("Wails reunderstanding did not share Service idempotency: %+v", wails)
	}
	var wailsResult DirectFailureReunderstandingResult
	if err := json.Unmarshal(wails.Data, &wailsResult); err != nil || wailsResult.NewPackageID != repaired.NewPackageID {
		t.Fatalf("Wails returned a different draft: result=%+v err=%v", wailsResult, err)
	}
	body, _ := json.Marshal(request)
	httpRequest := httptest.NewRequest(http.MethodPost, "/v1/desktop/projects/"+state.ProjectID+"/browser-agent-direct/reunderstand", bytes.NewReader(body))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse := httptest.NewRecorder()
	NewDevHTTPServer(service).Handler().ServeHTTP(httpResponse, httpRequest)
	if httpResponse.Code != http.StatusOK {
		t.Fatalf("HTTP reunderstanding did not share Service idempotency: status=%d body=%s", httpResponse.Code, httpResponse.Body.String())
	}
	var httpBridge BridgeResponse
	if err := json.Unmarshal(httpResponse.Body.Bytes(), &httpBridge); err != nil || !httpBridge.OK {
		t.Fatalf("invalid HTTP bridge response: %+v err=%v", httpBridge, err)
	}
	var httpResult DirectFailureReunderstandingResult
	if err := json.Unmarshal(httpBridge.Data, &httpResult); err != nil || httpResult.NewPackageID != repaired.NewPackageID {
		t.Fatalf("HTTP returned a different draft: result=%+v err=%v", httpResult, err)
	}
	conflict := request
	conflict.SelectedIssueIDs = append(conflict.SelectedIssueIDs, "issue_different")
	if _, err := service.ReunderstandDirectBrowserAgentFailure(t.Context(), state.ProjectID, conflict); bridgeErrorCode(err) != "idempotency_conflict" {
		t.Fatalf("idempotency conflict was not rejected: code=%q err=%v", bridgeErrorCode(err), err)
	}
}

func newDirectReunderstandingTestState(t *testing.T) (*Service, store.StateStore, *orchestrator.CascadeState, ClientExecutionPackageBuild) {
	t.Helper()
	product := httptest.NewServer(http.HandlerFunc(controlledBusinessFixtureHandler))
	t.Cleanup(product.Close)
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	states := store.NewMemoryStateStore()
	service, err := NewService(config.AppRuntimeConfig{DataRoot: t.TempDir(), CacheRoot: t.TempDir(), ArtifactRoot: t.TempDir(), LLMMode: config.LLMModeDeterministic, DevRepoRoot: repoRoot}, states)
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.CreateProject(context.Background(), orchestrator.UserInput{
		ProjectID: "direct-reunderstanding-service", Mode: model.AppModeDesktop, ProductURL: product.URL,
		ProductDescription: "Create a project and start the approved build.", TargetAudience: "product team",
		MustShow: []string{"create project", "start build"}, AllowedDomains: []string{"127.0.0.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	build, err := service.BuildClientExecutionPackage(t.Context(), state.ProjectID, defaultDesktopOrgID)
	if err != nil {
		t.Fatal(err)
	}
	state.DesktopCloudRun = &orchestrator.DesktopCloudRunState{
		SchemaVersion: desktopCloudRunSchemaVersion, OrgID: defaultDesktopOrgID,
		PackageID: build.Package.PackageID, PackageDigestSHA256: build.PackageDigestSHA256,
		GraphDigestSHA256: build.Package.Reproducibility.GraphHashSHA256,
		BundleHashSHA256:  build.Package.ExecutableScriptBundle.Reproducibility.BundleHashSHA256,
		PlanHashSHA256:    build.Package.ExecutableScriptBundle.Reproducibility.PlanHashSHA256,
	}
	if err := states.Save(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	return service, states, state, build
}

func directReunderstandingFailedResult(build ClientExecutionPackageBuild, authoritative bool) model.RecordingResultPackage {
	bundle := build.Package.ExecutableScriptBundle
	result := model.RecordingResultPackage{
		ResultID: "result_reunderstanding", SourcePackageID: build.Package.PackageID, CloudJobID: "job_reunderstanding",
		Status:            model.RecordingResultStatusFailed,
		FailureDiagnostic: &model.ScriptFailureDiagnostic{ID: "diagnostic_reunderstanding", SourcePackageID: build.Package.PackageID, CloudJobID: "job_reunderstanding", FailedNodeID: bundle.PlanJSON.Steps[0].NodeID, Error: model.AgentError{Code: "browser_agent_action_failed", Message: "redacted action failure"}, RedactionReport: model.DiagnosticRedactionReport{Applied: true}},
		RepairRequest:     &model.ScriptRepairRequest{ID: "repair_reunderstanding", SourceResultID: "result_reunderstanding", SourcePackageID: build.Package.PackageID, CloudJobID: "job_reunderstanding", FailedBundleHashSHA256: bundle.Reproducibility.BundleHashSHA256, FailedPlanHashSHA256: bundle.Reproducibility.PlanHashSHA256, ApprovalRequired: true, RepairAttempt: 1},
		AuditTrail:        model.CloudExecutionAuditTrail{GraphDigest: build.Package.Reproducibility.GraphHashSHA256},
	}
	if authoritative {
		result.ValidationReports = []model.ValidationReport{{Decision: model.ValidationDecisionReunderstandingRequired, Checks: []model.ValidationCheck{{ID: "check_route", Code: "selector_route_provenance_mismatch", NodeID: bundle.PlanJSON.Steps[0].NodeID, Severity: model.FindingSeverityBlocking, Required: true, Summary: "redacted route provenance mismatch", ResponsibilityDomain: model.ValidationCheckDomainApp}}}}
	}
	return result
}

func directReunderstandingRequest(t *testing.T, state *orchestrator.CascadeState) DirectFailureReunderstandingRequest {
	t.Helper()
	run := state.DesktopCloudRun
	diagnosticDigest, err := model.DigestCanonicalJSON(run.ResultPackage.FailureDiagnostic)
	if err != nil {
		t.Fatal(err)
	}
	issueIDs := make([]string, 0, len(run.ReunderstandingIssues))
	for _, issue := range run.ReunderstandingIssues {
		if issue.Required {
			issueIDs = append(issueIDs, directReunderstandingIssueID(issue))
		}
	}
	return DirectFailureReunderstandingRequest{
		SchemaVersion: DirectFailureReunderstandingSchemaVersion, SourceJobID: run.ResultPackage.CloudJobID,
		SourceResultID: run.ResultPackage.ResultID, SourcePackageID: run.ResultPackage.SourcePackageID,
		RepairRequestID: run.ResultPackage.RepairRequest.ID, BasePackageDigestSHA256: run.PackageDigestSHA256,
		BaseGraphDigestSHA256: run.GraphDigestSHA256, FailedBundleHashSHA256: run.BundleHashSHA256,
		FailedPlanHashSHA256: run.PlanHashSHA256, DiagnosticDigestSHA256: diagnosticDigest,
		SelectedIssueIDs: issueIDs, IdempotencyKey: "reunderstand-idempotency", UserConfirmed: true,
	}
}
