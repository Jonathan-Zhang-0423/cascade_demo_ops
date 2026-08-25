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
	for _, repairedNode := range repaired.Nodes {
		if repairedNode.ID == "open_creation" || repairedNode.ID == "fill_request" || (repairedNode.ActionSpec != nil && repairedNode.ActionSpec.Type == model.GraphActionClick) {
			t.Fatalf("repair retained a creation or submit action: %+v", repairedNode)
		}
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
	seenCapabilities := 0
	for _, stage := range prepared.Build.Package.ExecutableScriptBundle.ScriptOutline.Stages {
		if stage.StageKind == model.BusinessStageKindBusinessSubmit || stage.StageKind == model.BusinessStageKindBusinessInput || stage.StageKind == model.BusinessStageKindModeSelection {
			t.Fatalf("continuation package retained a creation write stage: %+v", stage)
		}
		if len(stage.Interactions) > 0 {
			if _, ok := stage.Interactions[0].Parameters["capability_layer"]; ok {
				seenCapabilities++
			}
		}
	}
	if seenCapabilities != len(definition.InteractionPlan.Steps) {
		t.Fatalf("continuation package capability contracts=%d want=%d", seenCapabilities, len(definition.InteractionPlan.Steps))
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
