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
		for _, interaction := range outline.Interactions {
			if interaction.NonDestructive != step.NonDestructive {
				return &OutlineConsistencyError{Code: "non_destructive_mismatch", NodeID: step.NodeID, Reason: "changes the App-approved non_destructive policy in script_outline"}
			}
		}
		if stepRequiresBrowserAgentValidation(step) && !stepHasDeterministicBrowserAgentValidation(step) {
			return &OutlineConsistencyError{Code: "deterministic_validation_missing", NodeID: step.NodeID, Reason: "requires a concrete URL, element, text, attribute, value, count, or title assertion"}
		}
		if (step.StageKind == BusinessStageKindSessionSetup || step.StageKind == BusinessStageKindObserveProgress || step.StageKind == BusinessStageKindFinalObserve) && !stepHasRuntimePageEvidence(step, stage, outline) {
			return &OutlineConsistencyError{Code: "runtime_page_evidence_missing", NodeID: step.NodeID, Reason: "cannot rely on source or generic wait conditions to prove a runtime page state"}
		}
		if step.StageKind == BusinessStageKindSessionSetup && stageLooksAuthenticated(stage, outline) && !stageHasSecretRef(step, stage) && !stageHasHumanCheckpoint(bundle, step.NodeID) {
			return &OutlineConsistencyError{Code: "session_auth_evidence_missing", NodeID: step.NodeID, Reason: "claims an authenticated workspace without a secret_ref or human checkpoint"}
		}
	}
	return nil
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
