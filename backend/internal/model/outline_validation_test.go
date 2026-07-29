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
