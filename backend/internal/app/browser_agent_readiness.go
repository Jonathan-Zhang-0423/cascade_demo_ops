package app

import (
	"fmt"
	"strings"

	"cascade-demoops/backend/internal/model"
)

// BrowserAgentReadinessReport separates a structurally valid exchange package
// from one that can safely start a real browser session.
type BrowserAgentReadinessReport struct {
	CanRun   bool                           `json:"can_run"`
	Blockers []BrowserAgentReadinessFinding `json:"blockers,omitempty"`
	Warnings []BrowserAgentReadinessFinding `json:"warnings,omitempty"`
}

type BrowserAgentReadinessFinding struct {
	Code    string `json:"code"`
	NodeID  string `json:"node_id,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func browserAgentReadiness(pkg *model.ClientExecutionPackage) BrowserAgentReadinessReport {
	report := BrowserAgentReadinessReport{CanRun: true, Blockers: []BrowserAgentReadinessFinding{}, Warnings: []BrowserAgentReadinessFinding{}}
	if pkg == nil || pkg.ExecutableScriptBundle == nil || pkg.ExecutableScriptBundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		return report
	}
	bundle := pkg.ExecutableScriptBundle
	if bundle.StageApprovalPlan == nil || bundle.ScriptOutline == nil || bundle.PlanJSON == nil {
		return report // Structural Intake reports incomplete contracts.
	}
	outlineByNode := map[string]model.BrowserAgentOutlineStage{}
	for _, stage := range bundle.ScriptOutline.Stages {
		outlineByNode[stage.NodeID] = stage
	}
	stepsByNode := map[string]model.ScriptStep{}
	for _, step := range bundle.PlanJSON.Steps {
		stepsByNode[step.NodeID] = step
	}
	for _, approved := range bundle.StageApprovalPlan.Stages {
		outline, hasOutline := outlineByNode[approved.NodeID]
		step, hasStep := stepsByNode[approved.NodeID]
		if !hasOutline || !hasStep {
			continue
		}
		interactions := outline.Interactions
		if len(interactions) == 0 {
			interactions = []model.BrowserAgentInteraction{approved.Interaction}
		}
		hasValidation := requiredBrowserValidation(step.Validations)
		if stageRequiresObservableOutcome(approved.StageKind) && !hasValidation {
			report.addBlocker(BrowserAgentReadinessFinding{
				Code: "stage_success_condition_missing", NodeID: approved.NodeID,
				Field: "payload.executable_script_bundle.plan_json.steps[].validations",
				Message: "Observation stage has no required deterministic success condition.",
				Hint: "Provide a required URL, element, text, value, attribute, count, title, or page_loaded validation.",
			})
		}
		if approved.StageKind == model.BusinessStageKindSessionSetup && approved.RouteState == model.BusinessRouteStateWorkspace && sessionSetupOnlyWaits(interactions) && !hasValidation {
			report.addBlocker(BrowserAgentReadinessFinding{
				Code: "workspace_login_path_missing", NodeID: approved.NodeID,
				Field: "payload.executable_script_bundle.stage_approval_plan.stages[].interaction",
				Message: "Workspace session stage only waits; it does not define an approved login or workspace-entry action.",
				Hint: "Add approved login/workspace transition actions and a deterministic workspace validation. Use a credential grant reference rather than plaintext if credentials are needed.",
			})
		}
		if stageHasSecretReference(approved, interactions, step) && len(pkg.CredentialGrants) == 0 {
			report.addBlocker(BrowserAgentReadinessFinding{
				Code: "credential_grant_missing", NodeID: approved.NodeID, Field: "payload.credential_grants",
				Message: "Stage references a secret but the package has no credential grant for isolated runtime use.",
				Hint: "Provide a scoped, expiring credential grant with cloud_secret_ref or encrypted_secret_attachment_id; never include plaintext credentials.",
			})
		}
		if approved.StageKind == model.BusinessStageKindBusinessInput && !hasInputValue(approved, interactions, step) {
			report.addBlocker(BrowserAgentReadinessFinding{
				Code: "business_input_missing", NodeID: approved.NodeID,
				Field: "payload.executable_script_bundle.stage_approval_plan.stages[].input_content",
				Message: "Business input stage has no approved input value, input reference, or secret reference.",
				Hint: "Provide an approved input in input_content or the matching interaction, plus a required result validation.",
			})
		}
	}
	return report
}

func (r *BrowserAgentReadinessReport) addBlocker(finding BrowserAgentReadinessFinding) {
	r.Blockers = append(r.Blockers, finding)
	r.CanRun = false
}

func stageRequiresObservableOutcome(kind model.BusinessStageKind) bool {
	return kind == model.BusinessStageKindSessionSetup || kind == model.BusinessStageKindObserveProgress || kind == model.BusinessStageKindFinalObserve
}

func requiredBrowserValidation(values []model.ValidationSpec) bool {
	for _, value := range values {
		if !value.Required {
			continue
		}
		switch value.Kind {
		case "url_matches", "element_visible", "element_hidden", "text_contains", "attribute_equals", "value_equals", "element_count", "page_title_contains", "page_loaded":
			return true
		}
	}
	return false
}

func sessionSetupOnlyWaits(values []model.BrowserAgentInteraction) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if value.Kind != model.GraphActionWait && value.Kind != model.GraphActionInspect && value.Kind != model.GraphActionAssert {
			return false
		}
	}
	return true
}

func stageHasSecretReference(approved model.StageApprovalStage, values []model.BrowserAgentInteraction, step model.ScriptStep) bool {
	if strings.TrimSpace(approved.Interaction.SecretRef) != "" || strings.TrimSpace(step.Action.SecretRef) != "" {
		return true
	}
	for _, input := range approved.InputContent {
		if strings.TrimSpace(input.SecretRef) != "" {
			return true
		}
	}
	for _, value := range values {
		if strings.TrimSpace(value.SecretRef) != "" {
			return true
		}
	}
	return false
}

func hasInputValue(approved model.StageApprovalStage, values []model.BrowserAgentInteraction, step model.ScriptStep) bool {
	if strings.TrimSpace(approved.Interaction.Value) != "" || strings.TrimSpace(approved.Interaction.InputRef) != "" || strings.TrimSpace(approved.Interaction.SecretRef) != "" || strings.TrimSpace(step.Action.Value) != "" || strings.TrimSpace(step.Action.InputRef) != "" || strings.TrimSpace(step.Action.SecretRef) != "" {
		return true
	}
	for _, input := range approved.InputContent {
		if strings.TrimSpace(input.Value) != "" || strings.TrimSpace(input.InputRef) != "" || strings.TrimSpace(input.SecretRef) != "" {
			return true
		}
	}
	for _, value := range values {
		if strings.TrimSpace(value.Value) != "" || strings.TrimSpace(value.InputRef) != "" || strings.TrimSpace(value.SecretRef) != "" {
			return true
		}
	}
	return false
}

func browserAgentReadinessError(report BrowserAgentReadinessReport) error {
	if report.CanRun || len(report.Blockers) == 0 {
		return nil
	}
	first := report.Blockers[0]
	return newRuntimeExecutionError("browser_agent_readiness_blocked", fmt.Errorf("%s: %s", first.Code, first.Message))
}
