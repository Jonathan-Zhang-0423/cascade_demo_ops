package model

import (
	"fmt"
	"strings"
)

type OutlineConsistencyError struct {
	Code   string
	NodeID string
	Reason string
}

func (e *OutlineConsistencyError) Error() string {
	if e.NodeID == "" {
		return e.Code + ": " + e.Reason
	}
	return fmt.Sprintf("%s: stage %q %s", e.Code, e.NodeID, e.Reason)
}

func ValidateBrowserAgentOutlineConsistency(bundle *ExecutableRecordingScriptBundle) error {
	if bundle == nil || bundle.PlanJSON == nil || bundle.StageApprovalPlan == nil || bundle.ScriptOutline == nil {
		return nil
	}
	stageByNode := make(map[string]StageApprovalStage, len(bundle.StageApprovalPlan.Stages))
	for _, stage := range bundle.StageApprovalPlan.Stages {
		stageByNode[stage.NodeID] = stage
	}
	outlineByNode := make(map[string]BrowserAgentOutlineStage, len(bundle.ScriptOutline.Stages))
	for _, stage := range bundle.ScriptOutline.Stages {
		outlineByNode[stage.NodeID] = stage
	}
	for _, step := range bundle.PlanJSON.Steps {
		stage, stageOK := stageByNode[step.NodeID]
		outline, outlineOK := outlineByNode[step.NodeID]
		if !stageOK || !outlineOK {
			continue
		}
		if field, candidate, ok := firstIncompleteSelectorCandidate(step, stage, outline); ok {
			return &OutlineConsistencyError{Code: "selector_provenance_incomplete", NodeID: step.NodeID, Reason: fmt.Sprintf("has selector candidate %q with incomplete provenance at %s", candidate.Value, field)}
		}
		legacyPlanStep := step.StageKind == "" && step.RouteState == ""
		if legacyPlanStep {
			continue
		}
		if !validBusinessStageKind(step.StageKind) || step.StageKind != stage.StageKind || step.StageKind != outline.StageKind {
			return &OutlineConsistencyError{Code: "stage_kind_mismatch", NodeID: step.NodeID, Reason: "has inconsistent stage_kind across plan_json, stage_approval_plan, and script_outline"}
		}
		if !validBusinessRouteState(step.RouteState) || step.RouteState != stage.RouteState || step.RouteState != outline.RouteState {
			return &OutlineConsistencyError{Code: "route_state_mismatch", NodeID: step.NodeID, Reason: "has inconsistent route_state across plan_json, stage_approval_plan, and script_outline"}
		}
		if step.NonDestructive != stage.Interaction.NonDestructive {
			return &OutlineConsistencyError{Code: "non_destructive_mismatch", NodeID: step.NodeID, Reason: "changes the App-approved non_destructive policy"}
		}
		if step.RuntimeAdaptive != stage.RuntimeAdaptive || step.RuntimeAdaptive != outline.RuntimeAdaptive {
			return &OutlineConsistencyError{Code: "runtime_adaptive_mismatch", NodeID: step.NodeID, Reason: "has inconsistent runtime_adaptive authority across plan_json, stage_approval_plan, and script_outline"}
		}
		for _, interaction := range outline.Interactions {
			if interaction.NonDestructive != step.NonDestructive {
				return &OutlineConsistencyError{Code: "non_destructive_mismatch", NodeID: step.NodeID, Reason: "changes the App-approved non_destructive policy in script_outline"}
			}
		}
		if stepRequiresBrowserAgentValidation(step) && !stepHasDeterministicBrowserAgentValidation(step) {
			return &OutlineConsistencyError{Code: "deterministic_validation_missing", NodeID: step.NodeID, Reason: "requires a concrete URL, element, text, attribute, value, count, or title assertion"}
		}
		if step.RuntimeAdaptive {
			if err := validateRuntimeAdaptiveExecutionContract(bundle, step, stage, outline); err != nil {
				return &OutlineConsistencyError{Code: "runtime_adaptive_contract_incomplete", NodeID: step.NodeID, Reason: err.Error()}
			}
		}
		if (step.StageKind == BusinessStageKindSessionSetup || step.StageKind == BusinessStageKindObserveProgress || step.StageKind == BusinessStageKindFinalObserve) && !stepHasRuntimePageEvidence(step, stage, outline) && !step.RuntimeAdaptive {
			return &OutlineConsistencyError{Code: "runtime_page_evidence_missing", NodeID: step.NodeID, Reason: "cannot rely on source or generic wait conditions to prove a runtime page state"}
		}
		if step.StageKind == BusinessStageKindSessionSetup && stageLooksAuthenticated(stage, outline) && !stageHasSecretRef(step, stage) && !stageHasHumanCheckpoint(bundle, step.NodeID) {
			return &OutlineConsistencyError{Code: "session_auth_evidence_missing", NodeID: step.NodeID, Reason: "claims an authenticated workspace without a secret_ref or human checkpoint"}
		}
	}
	return nil
}

func firstIncompleteSelectorCandidate(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) (string, SelectorCandidate, bool) {
	groups := []struct {
		field      string
		candidates []SelectorCandidate
	}{
		{"plan_json.steps[].page_target.selector_alternatives", step.PageTarget.SelectorAlternatives},
		{"plan_json.steps[].action.target.selector_alternatives", step.Action.Target.SelectorAlternatives},
		{"stage_approval_plan.stages[].interaction.target.selector_alternatives", stage.Interaction.Target.SelectorAlternatives},
	}
	for index, validation := range step.Validations {
		groups = append(groups, struct {
			field      string
			candidates []SelectorCandidate
		}{fmt.Sprintf("plan_json.steps[].validations[%d].target.selector_alternatives", index), validation.Target.SelectorAlternatives})
	}
	for index, component := range outline.Components {
		groups = append(groups, struct {
			field      string
			candidates []SelectorCandidate
		}{fmt.Sprintf("script_outline.stages[].components[%d].selector_alternatives", index), component.SelectorAlternatives})
	}
	for index, interaction := range outline.Interactions {
		groups = append(groups, struct {
			field      string
			candidates []SelectorCandidate
		}{fmt.Sprintf("script_outline.stages[].interactions[%d].target.selector_alternatives", index), interaction.Target.SelectorAlternatives})
	}
	for _, group := range groups {
		for _, candidate := range group.candidates {
			if !SelectorCandidateHasFormalProvenance(candidate) {
				return group.field, candidate, true
			}
		}
	}
	return "", SelectorCandidate{}, false
}

func SelectorCandidateHasFormalProvenance(candidate SelectorCandidate) bool {
	if strings.TrimSpace(candidate.Kind) == "" || strings.TrimSpace(candidate.Value) == "" ||
		strings.TrimSpace(candidate.EvidenceID) == "" || strings.TrimSpace(candidate.SourceDigest) == "" ||
		strings.TrimSpace(candidate.ObservedRole) == "" || strings.TrimSpace(candidate.ObservedAccessibleName) == "" ||
		candidate.ObservedAt.IsZero() {
		return false
	}
	switch strings.TrimSpace(candidate.SourceKind) {
	case "source_scan", "page_scan", "approved_manual_annotation":
	default:
		return false
	}
	for _, ref := range candidate.EvidenceRefs {
		if strings.TrimSpace(ref.ID) == strings.TrimSpace(candidate.EvidenceID) {
			return true
		}
	}
	return false
}

func validateRuntimeAdaptiveExecutionContract(bundle *ExecutableRecordingScriptBundle, step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) error {
	if !step.NonDestructive || stage.TargetContract == nil || outline.TargetContract == nil || step.TargetContract == nil {
		return fmt.Errorf("requires a non-destructive semantic target contract in every execution layer")
	}
	semanticID := strings.TrimSpace(step.TargetContract.SemanticID)
	if semanticID == "" || semanticID != strings.TrimSpace(stage.TargetContract.SemanticID) || semanticID != strings.TrimSpace(outline.TargetContract.SemanticID) {
		return fmt.Errorf("requires one consistent semantic target id")
	}
	if stage.TargetContract.Destructive || outline.TargetContract.Destructive || step.TargetContract.Destructive || strings.TrimSpace(stage.TargetContract.Purpose) == "" {
		return fmt.Errorf("requires a non-destructive target purpose")
	}
	if !runtimeAdaptiveTargetDiscoverable(step, stage) {
		return fmt.Errorf("requires route and role, name, component, or approved locator discovery hints")
	}
	if strings.TrimSpace(stage.SuccessState) == "" || strings.TrimSpace(outline.SuccessState) == "" || strings.TrimSpace(step.ExpectedOutcome) == "" {
		return fmt.Errorf("requires an immutable success state in every execution layer")
	}
	if !validRuntimeAdaptiveCapturePlan(stage.CapturePlan) {
		return fmt.Errorf("requires an observable capture plan")
	}
	if !validRuntimeAdaptiveCapturePlan(outline.CapturePlan) {
		return fmt.Errorf("requires an observable outline capture plan")
	}
	if bundle == nil || bundle.PlanJSON == nil || len(bundle.PlanJSON.SafetyPolicy.AllowedDomains) == 0 || bundle.ScriptOutline == nil || len(bundle.ScriptOutline.AllowedExplorationScope.AllowedOrigins) == 0 {
		return fmt.Errorf("requires allowed domains and exploration origins")
	}
	if !containsContractField(outline.CanModify, "selector") || !containsContractField(outline.MustPreserve, "success_state") || !containsContractField(outline.MustPreserve, "safety_policy") {
		return fmt.Errorf("requires bounded locator repair and immutable success/safety fields")
	}
	return nil
}

func runtimeAdaptiveTargetDiscoverable(step ScriptStep, stage StageApprovalStage) bool {
	target := step.Action.Target
	hasLocator := target.Selector != "" || target.TestID != "" || target.Role != "" || target.Label != "" || target.Text != "" || len(target.SelectorAlternatives) > 0
	contract := stage.TargetContract
	hasSemanticHints := contract != nil && (len(contract.AllowedRoles) > 0 || len(contract.AllowedNames) > 0 || strings.TrimSpace(contract.ComponentRef) != "")
	hasRoute := step.Action.Type == GraphActionNavigate && (target.URL != "" || step.PageTarget.URL != "")
	hasRoute = hasRoute || stage.EntryRoute != "" || stage.TargetRoute != "" || stage.TargetRouteTemplate != "" || len(stage.CandidateRoutes) > 0
	return hasRoute && (hasLocator || hasSemanticHints || step.Action.Type == GraphActionNavigate)
}

func validRuntimeAdaptiveCapturePlan(plan *BrowserAgentCapturePlan) bool {
	return plan != nil && strings.TrimSpace(plan.Intent) != "" && (strings.TrimSpace(plan.PrimaryArtifact) != "" || len(plan.RequiredAssets) > 0)
}

func containsContractField(values []string, field string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(strings.TrimSpace(value)), field) {
			return true
		}
	}
	return false
}

func stepHasDeterministicBrowserAgentValidation(step ScriptStep) bool {
	for _, validation := range step.Validations {
		if !validation.Required {
			continue
		}
		hasTarget := validation.Target.URL != "" || validation.Target.Selector != "" || validation.Target.TestID != "" || validation.Target.Role != "" || validation.Target.Label != "" || validation.Target.Text != ""
		hasExpected := strings.TrimSpace(validation.Assertion) != "" || validation.Expected != nil
		switch validation.Kind {
		case "url_matches", "element_visible", "element_hidden", "text_contains", "attribute_equals", "value_equals", "element_count", "page_title_contains":
			if hasTarget || hasExpected {
				return true
			}
		}
	}
	return false
}

func stepHasRuntimePageEvidence(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) bool {
	refs := append([]EvidenceRef{}, step.EvidenceRefs...)
	refs = append(refs, stage.EvidenceRefs...)
	refs = append(refs, stage.Interaction.EvidenceRefs...)
	refs = append(refs, stage.Interaction.Target.EvidenceRefs...)
	refs = append(refs, outline.EvidenceRefs...)
	for _, component := range outline.Components {
		refs = append(refs, component.EvidenceRefs...)
	}
	for _, ref := range refs {
		switch ref.Kind {
		case EvidenceKindWebScreenshot, EvidenceKindScreenshotOCR, EvidenceKindVisionFinding, EvidenceKindBrowserTrace:
			return true
		case EvidenceKindBrowserScan:
			if ref.ID != "ev_product_url" && !strings.Contains(strings.ToLower(ref.Summary), "url input") && !strings.Contains(ref.Summary, "产品 URL 输入") {
				return true
			}
		}
	}
	return false
}

func stageLooksAuthenticated(stage StageApprovalStage, outline BrowserAgentOutlineStage) bool {
	if stage.RouteState == BusinessRouteStateWorkspace || outline.RouteState == BusinessRouteStateWorkspace {
		return true
	}
	text := strings.ToLower(strings.Join([]string{stage.Objective, stage.SuccessState, outline.Objective, outline.SuccessState}, " "))
	return strings.Contains(text, "authenticated") || strings.Contains(text, "logged in") || strings.Contains(text, "工作台") || strings.Contains(text, "已登录")
}

func stageHasSecretRef(step ScriptStep, stage StageApprovalStage) bool {
	if step.Action.SecretRef != "" || step.Action.InputRef != "" || stage.Interaction.SecretRef != "" || stage.Interaction.InputRef != "" {
		return true
	}
	for _, input := range stage.InputContent {
		if input.SecretRef != "" {
			return true
		}
	}
	return false
}

func stageHasHumanCheckpoint(bundle *ExecutableRecordingScriptBundle, nodeID string) bool {
	for _, uncertainty := range append(append([]StageUncertainty{}, bundle.StageApprovalPlan.UncertaintyReport...), bundle.ScriptOutline.UncertaintyReport...) {
		text := strings.ToLower(uncertainty.Kind + " " + uncertainty.SuggestedAction)
		if (uncertainty.NodeID == "" || uncertainty.NodeID == nodeID) && (strings.Contains(text, "human") || strings.Contains(text, "人工")) {
			return true
		}
	}
	return false
}
