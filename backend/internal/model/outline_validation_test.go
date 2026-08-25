package model

import (
	"testing"
	"time"
)

func timePtr(value time.Time) *time.Time { return &value }

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
	bundle.PlanJSON.Steps[0].Action.Target.EvidenceRefs = nil
	bundle.PlanJSON.Steps[0].Action.Target.SelectorAlternatives = nil
	bundle.StageApprovalPlan.Stages[0].EvidenceRefs = bundle.PlanJSON.Steps[0].EvidenceRefs
	bundle.StageApprovalPlan.Stages[0].Interaction.EvidenceRefs = nil
	bundle.StageApprovalPlan.Stages[0].Interaction.Target.EvidenceRefs = nil
	bundle.StageApprovalPlan.Stages[0].Interaction.Target.SelectorAlternatives = nil
	bundle.ScriptOutline.Stages[0].EvidenceRefs = bundle.PlanJSON.Steps[0].EvidenceRefs
	bundle.ScriptOutline.Stages[0].Interactions[0].EvidenceRefs = nil
	bundle.ScriptOutline.Stages[0].Interactions[0].Target.EvidenceRefs = nil
	bundle.ScriptOutline.Stages[0].Interactions[0].Target.SelectorAlternatives = nil
	assertOutlineConsistencyCode(t, bundle, "runtime_page_evidence_missing")
}

func TestValidateBrowserAgentOutlineConsistencyRejectsWorkspaceWithoutCredentialOrCheckpoint(t *testing.T) {
	bundle := consistentOutlineBundleForTest()
	bundle.PlanJSON.Steps[0].Action.SecretRef = ""
	bundle.StageApprovalPlan.Stages[0].Interaction.SecretRef = ""
	bundle.ScriptOutline.Stages[0].Interactions[0].SecretRef = ""
	assertOutlineConsistencyCode(t, bundle, "session_auth_evidence_missing")
}

func TestValidateBrowserAgentOutlineConsistencyAcceptsSelectorFreeRuntimeAdaptiveLoginBootstrap(t *testing.T) {
	bundle := runtimeAdaptiveOutlineBundleForTest()
	clearSelectorProvenanceForTest(bundle)
	if err := ValidateBrowserAgentOutlineConsistency(bundle); err != nil {
		t.Fatalf("selector-free runtime authentication bootstrap should be observed by the isolated worker: %v", err)
	}
}

func TestValidateBrowserAgentOutlineConsistencyRejectsRuntimeAdaptiveLoginWithoutSuccessTransition(t *testing.T) {
	bundle := runtimeAdaptiveOutlineBundleForTest()
	clearSelectorProvenanceForTest(bundle)
	bundle.PlanJSON.Steps[0].Validations = nil
	assertOutlineConsistencyCode(t, bundle, "login_entry_evidence_missing")
}

func TestValidateBrowserAgentOutlineConsistencyAcceptsEvidenceBoundPostLoginRouteWithoutInventedWorkspaceElement(t *testing.T) {
	bundle := consistentOutlineBundleForTest()
	bundle.PlanJSON.Steps[0].Validations = bundle.PlanJSON.Steps[0].Validations[:1]
	if err := ValidateBrowserAgentOutlineConsistency(bundle); err != nil {
		t.Fatalf("evidence-bound post-login route should be sufficient without an invented workspace selector: %v", err)
	}
}

func TestValidateBrowserAgentOutlineConsistencyRejectsUnprovenPostLoginRoute(t *testing.T) {
	bundle := consistentOutlineBundleForTest()
	bundle.PlanJSON.Steps[0].Validations = bundle.PlanJSON.Steps[0].Validations[:1]
	bundle.PlanJSON.Steps[0].Validations[0].EvidenceRefs = nil
	assertOutlineConsistencyCode(t, bundle, "login_success_validation_missing")
}

func TestValidateBrowserAgentOutlineConsistencyRejectsIncompleteRuntimeAdaptiveContract(t *testing.T) {
	bundle := runtimeAdaptiveOutlineBundleForTest()
	bundle.ScriptOutline.Stages[0].TargetContract = nil
	assertOutlineConsistencyCode(t, bundle, "runtime_adaptive_contract_incomplete")

	bundle = runtimeAdaptiveOutlineBundleForTest()
	bundle.ScriptOutline.Stages[0].RuntimeAdaptive = false
	assertOutlineConsistencyCode(t, bundle, "runtime_adaptive_mismatch")
}

func TestValidateBrowserAgentOutlineConsistencyRequiresSelectorProvenance(t *testing.T) {
	bundle := consistentOutlineBundleForTest()
	bundle.PlanJSON.Steps[0].Action.Target.SelectorAlternatives = []SelectorCandidate{{Kind: "testid", Value: "login-submit"}}
	assertOutlineConsistencyCode(t, bundle, "selector_provenance_incomplete")

	evidence := EvidenceRef{ID: "ev_login_submit", Kind: EvidenceKindBrowserScan}
	bundle.PlanJSON.Steps[0].Action.Target.SelectorAlternatives[0] = SelectorCandidate{
		Kind: "testid", Value: "login-submit", EvidenceID: evidence.ID, SourceKind: "page_scan", SourceDigest: "sha256:page",
		ObservedRole: "button", ObservedAccessibleName: "Sign in", ObservedAt: timePtr(time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)), EvidenceRefs: []EvidenceRef{evidence},
	}
	if err := ValidateBrowserAgentOutlineConsistency(bundle); err != nil {
		t.Fatalf("complete selector provenance should be accepted: %v", err)
	}
}

func TestValidateBrowserAgentOutlineConsistencyRejectsUnprovenRuntimeAdaptivePrimarySelector(t *testing.T) {
	bundle := runtimeAdaptiveOutlineBundleForTest()
	selector := `[data-testid='guessed-login']`
	bundle.PlanJSON.Steps[0].Action.Target.Selector = selector
	bundle.StageApprovalPlan.Stages[0].Interaction.Target.Selector = selector
	bundle.ScriptOutline.Stages[0].Interactions[0].Target.Selector = selector
	assertOutlineConsistencyCode(t, bundle, "selector_primary_provenance_missing")

	evidence := EvidenceRef{ID: "ev_login_scan", Kind: EvidenceKindBrowserScan}
	candidate := SelectorCandidate{
		Kind: "testid", Value: "guessed-login", EvidenceID: evidence.ID, SourceKind: "page_scan", SourceDigest: "sha256:page",
		ObservedRole: "button", ObservedAccessibleName: "Sign in", ObservedURL: "https://app.example/login", ObservedRouteTemplate: "/login",
		ObservedPageRole: "authentication", ObservedFormRole: "authentication", EvidenceDigestSHA256: "sha256:page",
		ObservedAt: timePtr(time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)), EvidenceRefs: []EvidenceRef{evidence},
	}
	bundle.PlanJSON.Steps[0].Action.Target.SelectorAlternatives = []SelectorCandidate{candidate}
	bundle.StageApprovalPlan.Stages[0].Interaction.Target.SelectorAlternatives = []SelectorCandidate{candidate}
	bundle.ScriptOutline.Stages[0].Interactions[0].Target.SelectorAlternatives = []SelectorCandidate{candidate}
	if err := ValidateBrowserAgentOutlineConsistency(bundle); err != nil {
		t.Fatalf("runtime-adaptive primary selector with matching provenance should be accepted: %v", err)
	}
}

func TestValidateBrowserAgentOutlineConsistencyRejectsPrimarySelectorDrift(t *testing.T) {
	bundle := consistentOutlineBundleForTest()
	bundle.PlanJSON.Steps[0].Action.Target.Selector = "#login-email"
	bundle.StageApprovalPlan.Stages[0].Interaction.Target.Selector = "#login-email"
	bundle.ScriptOutline.Stages[0].Interactions[0].Target.Selector = "#marketing-email"
	assertOutlineConsistencyCode(t, bundle, "selector_binding_mismatch")
}

func TestValidateBrowserAgentOutlineConsistencyRejectsActionComponentSelectorConflict(t *testing.T) {
	bundle := consistentOutlineBundleForTest()
	componentRef := "component:new-project"
	for index := range bundle.PlanJSON.Steps {
		bundle.PlanJSON.Steps[index].Action.Target.ComponentRef = componentRef
	}
	bundle.StageApprovalPlan.Stages[0].Interaction.Target.ComponentRef = componentRef
	bundle.ScriptOutline.Stages[0].Interactions[0].Target.ComponentRef = componentRef
	bundle.ScriptOutline.Stages[0].Components = []BrowserAgentComponentTarget{{
		ComponentRef: componentRef,
		Selector:     "[data-testid='button-new-project']",
	}}
	assertOutlineConsistencyCode(t, bundle, "action_component_binding_mismatch")
}

func TestSemanticValidationRejectsVisibleClickedControlAsOutcome(t *testing.T) {
	step := ScriptStep{
		StageKind: BusinessStageKindBusinessAction,
		Action: ScriptActionInstruction{Type: GraphActionClick, Target: ActionTarget{
			Selector: "[data-testid='button-new-project']", TestID: "button-new-project",
		}},
		Validations: []ValidationSpec{{
			ID: "wrong_result", Kind: "element_visible", Required: true,
			Target: ActionTarget{Selector: "[data-testid='button-new-project']", TestID: "button-new-project"},
		}},
	}
	if stepHasSemanticallyValidBrowserAgentValidation(step) {
		t.Fatal("a still-visible clicked control must not prove the business outcome")
	}
	step.Validations[0].Target = ActionTarget{Selector: "[data-testid='dialog-new-project']", TestID: "dialog-new-project"}
	if !stepHasSemanticallyValidBrowserAgentValidation(step) {
		t.Fatal("a concrete post-click result target should prove the business outcome")
	}
}

func TestSemanticValidationAcceptsBoundedKeyboardSurfaceChange(t *testing.T) {
	step := ScriptStep{
		StageKind: BusinessStageKindFinalObserve,
		Action: ScriptActionInstruction{
			Type:       GraphActionPress,
			Parameters: map[string]any{"keys": "ArrowLeft,ArrowRight"},
		},
		Validations: []ValidationSpec{{
			ID: "surface_change", Kind: "frame_surface_changed", Required: true, Expected: true,
		}},
	}
	if !stepHasSemanticallyValidBrowserAgentValidation(step) {
		t.Fatal("a bounded keyboard action with a frame before/after change must prove the business outcome")
	}

	step.Validations[0].Kind = "element_visible"
	if stepHasSemanticallyValidBrowserAgentValidation(step) {
		t.Fatal("a visible keyboard target must not substitute for observed surface change")
	}

	step.Validations[0].Kind = "visual_region_changed"
	step.Action.Parameters = nil
	if stepHasSemanticallyValidBrowserAgentValidation(step) {
		t.Fatal("an unbounded keyboard action must not pass even with a visual change assertion")
	}
}

func TestValidateBrowserAgentOutlineConsistencyRejectsBusinessInputValueDrift(t *testing.T) {
	bundle := consistentOutlineBundleForTest()
	step := &bundle.PlanJSON.Steps[0]
	step.StageKind = BusinessStageKindBusinessInput
	step.Action.Type = GraphActionFill
	step.Action.SecretRef = ""
	step.Action.Value = "贪吃蛇游戏"
	step.Validations = []ValidationSpec{{ID: "value", Kind: "value_equals", Target: step.Action.Target, Expected: "贪吃蛇游戏", Required: true}}
	stage := &bundle.StageApprovalPlan.Stages[0]
	stage.StageKind = BusinessStageKindBusinessInput
	stage.Interaction.Kind = GraphActionFill
	stage.Interaction.SecretRef = ""
	stage.Interaction.Value = "入口"
	stage.InputContent = []StageInputContent{{Kind: "project_name", Value: "贪吃蛇游戏"}}
	outline := &bundle.ScriptOutline.Stages[0]
	outline.StageKind = BusinessStageKindBusinessInput
	outline.Interactions[0].Kind = GraphActionFill
	outline.Interactions[0].SecretRef = ""
	outline.Interactions[0].Value = "贪吃蛇游戏"
	assertOutlineConsistencyCode(t, bundle, "business_input_binding_mismatch")
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
	evidence := []EvidenceRef{{ID: "ev_runtime_page", Kind: EvidenceKindBrowserScan}}
	observedAt := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	candidate := SelectorCandidate{
		Kind: "testid", Value: "login-password", EvidenceID: "ev_runtime_page", SourceKind: "page_scan", SourceDigest: "sha256:login-page",
		ObservedRole: "textbox", ObservedAccessibleName: "Password", ObservedURL: "https://app.example/login", ObservedRouteTemplate: "/login",
		ObservedPageRole: "authentication", ObservedFormRole: "authentication", EvidenceDigestSHA256: "sha256:login-page", ObservedAt: &observedAt,
		EvidenceRefs: evidence,
	}
	target := ActionTarget{Selector: "[data-testid='login-password']", Label: "登录表单", SelectorAlternatives: []SelectorCandidate{candidate}, EvidenceRefs: evidence}
	step := ScriptStep{
		NodeID: "node_session", StageKind: BusinessStageKindSessionSetup, RouteState: BusinessRouteStateWorkspace, NonDestructive: true,
		PageTarget: ScriptPageTarget{URL: "https://app.example/login"},
		Action:     ScriptActionInstruction{Type: GraphActionFill, SecretRef: "secret://demo/password", Target: target},
		Validations: []ValidationSpec{
			{ID: "validate_workspace_route", Kind: "url_matches", Target: ActionTarget{URL: "https://app.example/workspace"}, Expected: "/workspace", Required: true, EvidenceRefs: evidence},
			{ID: "validate_workspace_root", Kind: "element_visible", Target: ActionTarget{Selector: "[data-testid='workspace-root']"}, Required: true},
		},
		EvidenceRefs: evidence,
	}
	interaction := BrowserAgentInteraction{Kind: GraphActionFill, SecretRef: "secret://demo/password", Target: target, NonDestructive: true, EvidenceRefs: evidence}
	return &ExecutableRecordingScriptBundle{
		PlanJSON:          &ExecutionScriptDocument{Steps: []ScriptStep{step}},
		StageApprovalPlan: &StageApprovalPlan{Stages: []StageApprovalStage{{NodeID: step.NodeID, StageKind: step.StageKind, RouteState: step.RouteState, EntryRoute: "https://app.example/login", TargetURL: "https://app.example/login", ExpectedRouteAfterAction: "https://app.example/workspace", Interaction: interaction, EvidenceRefs: evidence}}},
		ScriptOutline: &BrowserAgentScriptOutline{Stages: []BrowserAgentOutlineStage{{
			NodeID: step.NodeID, StageKind: step.StageKind, RouteState: step.RouteState, EntryRoute: "https://app.example/login", Route: "https://app.example/login", URL: "https://app.example/login",
			ExpectedRouteAfterAction: "https://app.example/workspace", Interactions: []BrowserAgentInteraction{interaction}, EvidenceRefs: evidence,
		}}},
	}
}

func clearSelectorProvenanceForTest(bundle *ExecutableRecordingScriptBundle) {
	bundle.PlanJSON.Steps[0].Action.Target.Selector = ""
	bundle.PlanJSON.Steps[0].Action.Target.SelectorAlternatives = nil
	bundle.StageApprovalPlan.Stages[0].Interaction.Target.Selector = ""
	bundle.StageApprovalPlan.Stages[0].Interaction.Target.SelectorAlternatives = nil
	bundle.ScriptOutline.Stages[0].Interactions[0].Target.Selector = ""
	bundle.ScriptOutline.Stages[0].Interactions[0].Target.SelectorAlternatives = nil
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
