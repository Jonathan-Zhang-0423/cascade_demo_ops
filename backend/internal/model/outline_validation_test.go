package model

import "testing"

func TestValidateBrowserAgentOutlineConsistencyRejectsContractDrift(t *testing.T) {
	bundle := consistentOutlineBundleForTest()
	bundle.ScriptOutline.Stages[0].RouteState = BusinessRouteStateBuildRunning
	assertOutlineConsistencyCode(t, bundle, "route_state_mismatch")

	bundle = consistentOutlineBundleForTest()
	bundle.ScriptOutline.Stages[0].Interactions[0].NonDestructive = false
	assertOutlineConsistencyCode(t, bundle, "non_destructive_mismatch")
}

func TestValidateBrowserAgentOutlineConsistencyRejectsGenericObservationEvidence(t *testing.T) {
	bundle := consistentOutlineBundleForTest()
	bundle.PlanJSON.Steps[0].StageKind = BusinessStageKindObserveProgress
	bundle.StageApprovalPlan.Stages[0].StageKind = BusinessStageKindObserveProgress
	bundle.ScriptOutline.Stages[0].StageKind = BusinessStageKindObserveProgress
	bundle.PlanJSON.Steps[0].Action.Type = GraphActionWait
	bundle.PlanJSON.Steps[0].EvidenceRefs = []EvidenceRef{{ID: "ev_product_url", Kind: EvidenceKindBrowserScan, Summary: "产品 URL 输入"}}
	bundle.StageApprovalPlan.Stages[0].EvidenceRefs = bundle.PlanJSON.Steps[0].EvidenceRefs
	bundle.StageApprovalPlan.Stages[0].Interaction.EvidenceRefs = nil
	bundle.ScriptOutline.Stages[0].EvidenceRefs = bundle.PlanJSON.Steps[0].EvidenceRefs
	bundle.ScriptOutline.Stages[0].Interactions[0].EvidenceRefs = nil
	assertOutlineConsistencyCode(t, bundle, "runtime_page_evidence_missing")
}

func TestValidateBrowserAgentOutlineConsistencyRejectsWorkspaceWithoutCredentialOrCheckpoint(t *testing.T) {
	bundle := consistentOutlineBundleForTest()
	bundle.PlanJSON.Steps[0].Action.SecretRef = ""
	bundle.StageApprovalPlan.Stages[0].Interaction.SecretRef = ""
	bundle.ScriptOutline.Stages[0].Interactions[0].SecretRef = ""
	assertOutlineConsistencyCode(t, bundle, "session_auth_evidence_missing")
}

func TestValidateBrowserAgentOutlineConsistencyAcceptsCompleteRuntimeAdaptiveContractWithoutPageEvidence(t *testing.T) {
	bundle := runtimeAdaptiveOutlineBundleForTest()
	if err := ValidateBrowserAgentOutlineConsistency(bundle); err != nil {
		t.Fatalf("complete runtime-adaptive contract should defer page evidence to Browser Agent: %v", err)
	}
}

func TestValidateBrowserAgentOutlineConsistencyRejectsIncompleteRuntimeAdaptiveContract(t *testing.T) {
	bundle := runtimeAdaptiveOutlineBundleForTest()
	bundle.ScriptOutline.Stages[0].TargetContract = nil
	assertOutlineConsistencyCode(t, bundle, "runtime_adaptive_contract_incomplete")

	bundle = runtimeAdaptiveOutlineBundleForTest()
	bundle.ScriptOutline.Stages[0].RuntimeAdaptive = false
	assertOutlineConsistencyCode(t, bundle, "runtime_adaptive_mismatch")
}

func assertOutlineConsistencyCode(t *testing.T, bundle *ExecutableRecordingScriptBundle, want string) {
	t.Helper()
	err := ValidateBrowserAgentOutlineConsistency(bundle)
	consistency, ok := err.(*OutlineConsistencyError)
	if !ok || consistency.Code != want {
		t.Fatalf("expected %s, got %T: %v", want, err, err)
	}
}

func consistentOutlineBundleForTest() *ExecutableRecordingScriptBundle {
	evidence := []EvidenceRef{{ID: "ev_runtime_page", Kind: EvidenceKindWebScreenshot}}
	step := ScriptStep{
		NodeID: "node_session", StageKind: BusinessStageKindSessionSetup, RouteState: BusinessRouteStateWorkspace, NonDestructive: true,
		Action:       ScriptActionInstruction{Type: GraphActionFill, SecretRef: "secret://demo/password", Target: ActionTarget{Label: "登录表单"}},
		Validations:  []ValidationSpec{{ID: "validate_workspace", Kind: "url_matches", Target: ActionTarget{URL: "https://app.example/workspace"}, Expected: "/workspace", Required: true}},
		EvidenceRefs: evidence,
	}
	interaction := BrowserAgentInteraction{Kind: GraphActionFill, SecretRef: "secret://demo/password", Target: ActionTarget{Label: "登录表单"}, NonDestructive: true, EvidenceRefs: evidence}
	return &ExecutableRecordingScriptBundle{
		PlanJSON:          &ExecutionScriptDocument{Steps: []ScriptStep{step}},
		StageApprovalPlan: &StageApprovalPlan{Stages: []StageApprovalStage{{NodeID: step.NodeID, StageKind: step.StageKind, RouteState: step.RouteState, Interaction: interaction, EvidenceRefs: evidence}}},
		ScriptOutline:     &BrowserAgentScriptOutline{Stages: []BrowserAgentOutlineStage{{NodeID: step.NodeID, StageKind: step.StageKind, RouteState: step.RouteState, Interactions: []BrowserAgentInteraction{interaction}, EvidenceRefs: evidence}}},
	}
}

func runtimeAdaptiveOutlineBundleForTest() *ExecutableRecordingScriptBundle {
	bundle := consistentOutlineBundleForTest()
	planEvidence := []EvidenceRef{{ID: "ev_user_requirement", Kind: EvidenceKindUserInput, Summary: "approved user requirement"}}
	target := &BrowserAgentTargetContract{SemanticID: "semantic_login", Purpose: "登录并进入工作台", AllowedRoles: []string{"form"}, AllowedNames: []string{"登录"}, ComponentRef: "login-form", EvidenceRefs: planEvidence}
	bundle.PlanJSON.SafetyPolicy.AllowedDomains = []string{"app.example"}
	bundle.PlanJSON.Steps[0].RuntimeAdaptive = true
	bundle.PlanJSON.Steps[0].TargetContract = target
	bundle.PlanJSON.Steps[0].ExpectedOutcome = "进入工作台"
	bundle.PlanJSON.Steps[0].EvidenceRefs = planEvidence
	bundle.StageApprovalPlan.Stages[0].RuntimeAdaptive = true
	bundle.StageApprovalPlan.Stages[0].EntryRoute = "/login"
	bundle.StageApprovalPlan.Stages[0].TargetContract = target
	bundle.StageApprovalPlan.Stages[0].SuccessState = "进入工作台"
	bundle.StageApprovalPlan.Stages[0].CapturePlan = &BrowserAgentCapturePlan{Intent: "记录登录成功状态", PrimaryArtifact: "screenshot", RequiredAssets: []string{"viewport_screenshot"}}
	bundle.StageApprovalPlan.Stages[0].EvidenceRefs = planEvidence
	bundle.StageApprovalPlan.Stages[0].Interaction.EvidenceRefs = planEvidence
	bundle.ScriptOutline.AllowedExplorationScope.AllowedOrigins = []string{"https://app.example"}
	bundle.ScriptOutline.Stages[0].RuntimeAdaptive = true
	bundle.ScriptOutline.Stages[0].EntryRoute = "/login"
	bundle.ScriptOutline.Stages[0].TargetContract = target
	bundle.ScriptOutline.Stages[0].SuccessState = "进入工作台"
	bundle.ScriptOutline.Stages[0].CapturePlan = &BrowserAgentCapturePlan{Intent: "记录登录成功状态", PrimaryArtifact: "screenshot", RequiredAssets: []string{"viewport_screenshot"}}
	bundle.ScriptOutline.Stages[0].CanModify = []string{"selector", "wait_conditions"}
	bundle.ScriptOutline.Stages[0].MustPreserve = []string{"success_state", "safety_policy"}
	bundle.ScriptOutline.Stages[0].EvidenceRefs = planEvidence
	bundle.ScriptOutline.Stages[0].Interactions[0].EvidenceRefs = planEvidence
	return bundle
}
