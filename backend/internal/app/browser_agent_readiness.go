package app

import (
	"fmt"
	"net/url"
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
		if field, candidate, ok := browserAgentIncompleteSelectorCandidate(step, approved, outline); ok {
			report.addBlocker(BrowserAgentReadinessFinding{
				Code: "selector_provenance_incomplete", NodeID: approved.NodeID, Field: field,
				Message: "Selector candidate is not bound to complete App-approved source provenance: " + candidate.Value,
				Hint:    "Regenerate the candidate from a source scan, page scan, or approved manual annotation with evidence ID, source digest, observed role/name, and observation time. Do not emit generic selector guesses.",
			})
		}
		hasValidation := requiredBrowserValidation(step.Validations)
		if stageRequiresObservableOutcome(approved.StageKind) && !hasValidation {
			report.addBlocker(BrowserAgentReadinessFinding{
				Code: "stage_success_condition_missing", NodeID: approved.NodeID,
				Field:   "payload.executable_script_bundle.plan_json.steps[].validations",
				Message: "Observation stage has no required deterministic success condition.",
				Hint:    "Provide a required URL, element, text, value, attribute, count, title, or page_loaded validation.",
			})
		}
		if approved.StageKind == model.BusinessStageKindSessionSetup && approved.RouteState == model.BusinessRouteStateWorkspace && sessionSetupOnlyWaits(interactions) && !hasValidation {
			report.addBlocker(BrowserAgentReadinessFinding{
				Code: "workspace_login_path_missing", NodeID: approved.NodeID,
				Field:   "payload.executable_script_bundle.stage_approval_plan.stages[].interaction",
				Message: "Workspace session stage only waits; it does not define an approved login or workspace-entry action.",
				Hint:    "Add approved login/workspace transition actions and a deterministic workspace validation. Use a credential grant reference rather than plaintext if credentials are needed.",
			})
		}
		if stageHasSecretReference(approved, interactions, step) && len(pkg.CredentialGrants) == 0 {
			report.addBlocker(BrowserAgentReadinessFinding{
				Code: "credential_grant_missing", NodeID: approved.NodeID, Field: "payload.credential_grants",
				Message: "Stage references a secret but the package has no credential grant for isolated runtime use.",
				Hint:    "Provide a scoped, expiring credential grant with cloud_secret_ref or encrypted_secret_attachment_id; never include plaintext credentials.",
			})
		}
		if approved.StageKind == model.BusinessStageKindBusinessInput && !hasInputValue(approved, interactions, step) {
			report.addBlocker(BrowserAgentReadinessFinding{
				Code: "business_input_missing", NodeID: approved.NodeID,
				Field:   "payload.executable_script_bundle.stage_approval_plan.stages[].input_content",
				Message: "Business input stage has no approved input value, input reference, or secret reference.",
				Hint:    "Provide an approved input in input_content or the matching interaction, plus a required result validation.",
			})
		}
		if approved.RuntimeRouteVerificationRequired {
			if finding := browserAgentDynamicRouteFinding(approved, outline, step); finding != nil {
				report.addBlocker(*finding)
			}
		}
		if finding := browserAgentActionOutcomeFinding(approved, interactions, step); finding != nil {
			report.addBlocker(*finding)
		}
		if finding := browserAgentOutcomeUsesApprovedActionEvidence(approved, outline, interactions, step); finding != nil {
			report.addBlocker(*finding)
		}
		for _, finding := range browserAgentMissingBusinessInputFindings(approved, interactions, step, pkg) {
			report.addBlocker(finding)
		}
	}
	for _, finding := range browserAgentDeclaredInputFindings(pkg) {
		report.addBlocker(finding)
	}
	return report
}

func browserAgentIncompleteSelectorCandidate(step model.ScriptStep, stage model.StageApprovalStage, outline model.BrowserAgentOutlineStage) (string, model.SelectorCandidate, bool) {
	groups := []struct {
		field      string
		candidates []model.SelectorCandidate
	}{
		{"payload.executable_script_bundle.plan_json.steps[].page_target.selector_alternatives", step.PageTarget.SelectorAlternatives},
		{"payload.executable_script_bundle.plan_json.steps[].action.target.selector_alternatives", step.Action.Target.SelectorAlternatives},
		{"payload.executable_script_bundle.stage_approval_plan.stages[].interaction.target.selector_alternatives", stage.Interaction.Target.SelectorAlternatives},
	}
	for _, component := range outline.Components {
		groups = append(groups, struct {
			field      string
			candidates []model.SelectorCandidate
		}{"payload.executable_script_bundle.script_outline.stages[].components[].selector_alternatives", component.SelectorAlternatives})
	}
	for _, interaction := range outline.Interactions {
		groups = append(groups, struct {
			field      string
			candidates []model.SelectorCandidate
		}{"payload.executable_script_bundle.script_outline.stages[].interactions[].target.selector_alternatives", interaction.Target.SelectorAlternatives})
	}
	for _, group := range groups {
		for _, candidate := range group.candidates {
			if !model.SelectorCandidateHasFormalProvenance(candidate) {
				return group.field, candidate, true
			}
		}
	}
	return "", model.SelectorCandidate{}, false
}

// browserAgentOutcomeUsesApprovedActionEvidence catches a subtle App export
// defect: the action may use an approved alternative (for example the
// button-new-project selector), while the required post-action validation
// points at that same action control. Once the click opens a dialog the
// control is hidden, so this validation cannot prove the intended transition.
// This is a formal readiness gate; the Server never invents a replacement target.
func browserAgentOutcomeUsesApprovedActionEvidence(approved model.StageApprovalStage, outline model.BrowserAgentOutlineStage, interactions []model.BrowserAgentInteraction, step model.ScriptStep) *BrowserAgentReadinessFinding {
	if step.Action.Type != model.GraphActionClick && step.Action.Type != model.GraphActionSelect && step.Action.Type != model.GraphActionUpload {
		return nil
	}
	if approved.StageKind == model.BusinessStageKindSessionSetup || approved.StageKind == model.BusinessStageKindObserveProgress || approved.StageKind == model.BusinessStageKindFinalObserve {
		return nil
	}
	action := step.Action.Target
	if len(interactions) > 0 && !actionTargetHasStableHandle(action) {
		action = interactions[0].Target
	}
	actionEvidence := evidenceIDSet(action.EvidenceRefs)
	if len(actionEvidence) == 0 && len(interactions) > 0 {
		actionEvidence = evidenceIDSet(interactions[0].Target.EvidenceRefs)
	}
	for _, validation := range step.Validations {
		if !validation.Required || validation.Kind != "element_visible" {
			continue
		}
		candidates := append([]model.SelectorCandidate{}, action.SelectorAlternatives...)
		// An App package may put the actual action selector on a sibling
		// component while retaining the same browser-scan evidence on the
		// interaction target. Treat that as evidence-bound action identity for
		// diagnostics, but never mutate or execute the validation target.
		for _, component := range outline.Components {
			// Only a component explicitly bound to the approved action contract
			// can expand action identity. Result-state components intentionally
			// have no action component_ref and may share the stage evidence set.
			if approved.TargetContract.ComponentRef != "" && component.ComponentRef != approved.TargetContract.ComponentRef {
				continue
			}
			if len(actionEvidence) == 0 || !evidenceRefsOverlap(actionEvidence, evidenceIDSet(component.EvidenceRefs)) {
				continue
			}
			if strings.TrimSpace(component.Selector) != "" {
				candidates = append(candidates, model.SelectorCandidate{Kind: "css", Value: component.Selector})
			}
			if strings.TrimSpace(component.TestID) != "" {
				candidates = append(candidates, model.SelectorCandidate{Kind: "testid", Value: component.TestID})
			}
		}
		for _, candidate := range candidates {
			candidateTarget := model.ActionTarget{}
			switch strings.ToLower(strings.TrimSpace(candidate.Kind)) {
			case "css", "selector":
				candidateTarget.Selector = candidate.Value
			case "testid":
				candidateTarget.TestID = candidate.Value
			default:
				continue
			}
			if sameStableTarget(candidateTarget, validation.Target) {
				return &BrowserAgentReadinessFinding{
					Code: "post_action_validation_reuses_approved_action_evidence", NodeID: approved.NodeID,
					Field:   "payload.executable_script_bundle.plan_json.steps[].validations[].target",
					Message: "Post-action validation reuses an App-approved action selector alternative; it may be hidden by the resulting dialog or state.",
					Hint:    "Regenerate the App package with a selector/evidence for the resulting dialog, route, status, or content. Server will not infer or invent that target.",
				}
			}
		}
		// Some App exports put the discovered action identity only in the
		// human-readable label (for example "新建项目 button-new-project") while
		// serializing a different container selector as action.target.selector.
		// If the required result target is that named control, report the same
		// defect without treating prose as an executable selector.
		if actionTargetIdentityMatchesLabel(action, validation.Target) {
			return &BrowserAgentReadinessFinding{
				Code: "post_action_validation_reuses_action_identity", NodeID: approved.NodeID,
				Field:   "payload.executable_script_bundle.plan_json.steps[].validations[].target",
				Message: "Post-action validation names the same control identity as the action instead of the resulting business state.",
				Hint:    "Regenerate the App package with an explicit result selector such as the opened dialog or input field; Server will not infer or invent it.",
			}
		}
	}
	return nil
}

func actionTargetIdentityMatchesLabel(action, validation model.ActionTarget) bool {
	label := strings.ToLower(strings.TrimSpace(action.Label))
	if label == "" {
		return false
	}
	for _, identity := range []string{
		strings.TrimSpace(validation.TestID), strings.TrimSpace(validation.Selector),
		strings.TrimSpace(validation.Label), strings.TrimSpace(validation.Text),
	} {
		if identity == "" {
			continue
		}
		if strings.Contains(label, strings.ToLower(identity)) {
			return true
		}
		if strings.HasPrefix(identity, "[data-testid=\"") && strings.HasSuffix(identity, "\"]") {
			value := strings.TrimSuffix(strings.TrimPrefix(identity, "[data-testid=\""), "\"]")
			if value != "" && strings.Contains(label, strings.ToLower(value)) {
				return true
			}
		}
	}
	return false
}

func evidenceRefsOverlap(left, right map[string]struct{}) bool {
	for id := range left {
		if _, ok := right[id]; ok {
			return true
		}
	}
	return false
}

// browserAgentMissingBusinessInputFindings reports required natural-language
// inputs that are present in the project intent but absent from the executable
// click/fill graph. It deliberately inspects only package metadata and never
// reads source code or secrets.
func browserAgentMissingBusinessInputFindings(approved model.StageApprovalStage, interactions []model.BrowserAgentInteraction, step model.ScriptStep, pkg *model.ClientExecutionPackage) []BrowserAgentReadinessFinding {
	if pkg == nil || step.Action.Type == model.GraphActionFill || step.Action.Type == model.GraphActionSelect || step.Action.Type == model.GraphActionUpload {
		return nil
	}
	if approved.StageKind == model.BusinessStageKindSessionSetup || approved.StageKind == model.BusinessStageKindObserveProgress || approved.StageKind == model.BusinessStageKindFinalObserve {
		return nil
	}
	// A business-input stage with no value is already handled by the generic
	// readiness check. Here we only flag missing fill actions when the package
	// itself declares an input requirement for the same workflow.
	if approved.StageKind == model.BusinessStageKindBusinessInput {
		return []BrowserAgentReadinessFinding{{
			Code: "business_input_action_missing", NodeID: approved.NodeID,
			Field:   "payload.executable_script_bundle.plan_json.steps[].action",
			Message: "Business input stage is not a fill/select action, so the declared user input cannot be replayed.",
			Hint:    "Regenerate the App package with an explicit non-secret fill interaction and a required result validation.",
		}}
	}
	// Do not guess from free-form prose for ordinary action stages. The App is
	// authoritative for which inputs are executable and their exact semantics.
	return nil
}

// browserAgentDeclaredInputFindings compares only two package-owned facts:
// the explicit user requirement summary and the executable interaction graph.
// It does not infer a selector or synthesize a value. A package that says it
// must type user requirements but contains no fill interaction is incomplete
// and must fail closed before any formal browser run.
func browserAgentDeclaredInputFindings(pkg *model.ClientExecutionPackage) []BrowserAgentReadinessFinding {
	if pkg == nil || pkg.ExecutableScriptBundle == nil || pkg.ProjectContextSummary.ProductURL == "" {
		return nil
	}
	texts := []string{}
	for _, goal := range pkg.ProjectContextSummary.Goals {
		if strings.TrimSpace(goal.ValueProposition) != "" {
			texts = append(texts, strings.ToLower(goal.ValueProposition))
		}
	}
	if strings.TrimSpace(pkg.ProductMapSummary.Summary) != "" {
		texts = append(texts, strings.ToLower(pkg.ProductMapSummary.Summary))
	}
	declaresInput := false
	for _, text := range texts {
		if strings.Contains(text, "输入栏中输入") || strings.Contains(text, "输入框中输入") || strings.Contains(text, "input") && strings.Contains(text, "type") {
			declaresInput = true
			break
		}
	}
	if !declaresInput {
		return nil
	}
	fillCount := 0
	for _, step := range pkg.ExecutableScriptBundle.PlanJSON.Steps {
		if step.Action.Type == model.GraphActionFill {
			fillCount++
		}
	}
	for _, stage := range pkg.ExecutableScriptBundle.StageApprovalPlan.Stages {
		if stage.Interaction.Kind == model.GraphActionFill {
			fillCount++
		}
	}
	if fillCount > 0 {
		return nil
	}
	return []BrowserAgentReadinessFinding{{
		Code:    "declared_business_input_action_missing",
		Field:   "payload.executable_script_bundle.plan_json.steps[].action",
		Message: "用户需求声明包含页面输入，但执行图没有任何 fill 交互，无法按原始 App 需求完成录制。",
		Hint:    "请由 App 按正式规则重新生成原始包，加入真实输入控件、非敏感 input/value 或受控 input_ref，以及对应的 value_equals 或结果验证。Server 不会代填。",
	}}
}

func (r *BrowserAgentReadinessReport) addBlocker(finding BrowserAgentReadinessFinding) {
	r.Blockers = append(r.Blockers, finding)
	r.CanRun = false
}

func (r *BrowserAgentReadinessReport) addWarning(finding BrowserAgentReadinessFinding) {
	r.Warnings = append(r.Warnings, finding)
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
		case "url_matches", "element_visible", "element_hidden", "text_contains", "attribute_equals", "value_equals", "checked_equals", "element_count", "page_title_contains", "page_loaded", "page_changed", "state_changed", "dom_changed", "aria_changed", "network_settled", "visual_region_changed", "frame_surface_changed", "interactive_surface_visible", "playable_surface_visible":
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

// browserAgentDynamicRouteFinding rejects a literal route parameter in a
// required URL assertion. The Server may verify a runtime-bound route, but it
// must not invent the value of a dynamic segment on behalf of the App package.
func browserAgentDynamicRouteFinding(approved model.StageApprovalStage, outline model.BrowserAgentOutlineStage, step model.ScriptStep) *BrowserAgentReadinessFinding {
	for _, validation := range step.Validations {
		if !validation.Required || validation.Kind != "url_matches" {
			continue
		}
		value := strings.TrimSpace(validation.Target.URL)
		if value == "" {
			if expected, ok := validation.Expected.(string); ok {
				value = strings.TrimSpace(expected)
			} else {
				value = strings.TrimSpace(validation.Assertion)
			}
		}
		if hasLiteralDynamicRoute(value) && !dynamicRouteMatchesApprovedTemplate(value, approved, outline) {
			return &BrowserAgentReadinessFinding{
				Code: "dynamic_route_binding_missing", NodeID: approved.NodeID,
				Field:   "payload.executable_script_bundle.plan_json.steps[].validations[].target.url",
				Message: "Required URL validation contains a literal dynamic route parameter; the package does not bind the runtime resource ID.",
				Hint:    "Provide a route template plus an approved runtime binding (for example /project/{project_id}) and validate the observed URL after binding.",
			}
		}
	}
	return nil
}

// A dynamic URL assertion is valid when the App package also declares the
// same route template in its approved stage/outline. The runtime may bind the
// observed identifier in memory, but readiness must reject a template that is
// absent or inconsistent with the App-approved route contract.
func dynamicRouteMatchesApprovedTemplate(value string, approved model.StageApprovalStage, outline model.BrowserAgentOutlineStage) bool {
	for _, candidate := range []string{
		approved.TargetRouteTemplate,
		approved.ExpectedRouteAfterAction,
		approved.TargetRoute,
		outline.TargetRouteTemplate,
		outline.ExpectedRouteAfterAction,
		outline.Route,
	} {
		if strings.TrimSpace(candidate) != "" && routeTemplateEquivalent(value, candidate) {
			return true
		}
	}
	return false
}

func routeTemplateEquivalent(left, right string) bool {
	leftPath := normalizeRouteTemplatePath(left)
	rightPath := normalizeRouteTemplatePath(right)
	if leftPath == "" || rightPath == "" {
		return false
	}
	leftParts := strings.Split(strings.Trim(leftPath, "/"), "/")
	rightParts := strings.Split(strings.Trim(rightPath, "/"), "/")
	if len(leftParts) != len(rightParts) {
		return false
	}
	for i := range leftParts {
		if routeTemplateSegment(leftParts[i]) || routeTemplateSegment(rightParts[i]) {
			continue
		}
		if leftParts[i] != rightParts[i] {
			return false
		}
	}
	return true
}

func normalizeRouteTemplatePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Path != "" {
		value = parsed.Path
	}
	value = "/" + strings.Trim(value, "/")
	return strings.ToLower(value)
}

func routeTemplateSegment(value string) bool {
	return value == "*" || strings.HasPrefix(value, ":") || (strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}"))
}

func hasLiteralDynamicRoute(value string) bool {
	path := normalizeRouteTemplatePath(value)
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		if routeTemplateSegment(segment) {
			return true
		}
	}
	return false
}

// A click/submit stage must normally validate the state it caused, not merely
// prove that the clicked control is still visible. This catches the common App
// package defect where the post-action validation accidentally copies the
// action selector instead of independently observing the resulting surface.
func browserAgentActionOutcomeFinding(approved model.StageApprovalStage, interactions []model.BrowserAgentInteraction, step model.ScriptStep) *BrowserAgentReadinessFinding {
	if step.Action.Type != model.GraphActionClick && step.Action.Type != model.GraphActionSelect && step.Action.Type != model.GraphActionUpload {
		return nil
	}
	if approved.StageKind == model.BusinessStageKindSessionSetup || approved.StageKind == model.BusinessStageKindObserveProgress || approved.StageKind == model.BusinessStageKindFinalObserve {
		return nil
	}
	action := step.Action.Target
	if len(interactions) > 0 && !actionTargetHasStableHandle(action) {
		action = interactions[0].Target
	}
	for _, validation := range step.Validations {
		if !validation.Required || validation.Kind != "element_visible" || !sameStableTarget(action, validation.Target) {
			continue
		}
		return &BrowserAgentReadinessFinding{
			Code: "post_action_validation_reuses_action_target", NodeID: approved.NodeID,
			Field:   "payload.executable_script_bundle.plan_json.steps[].validations[].target",
			Message: "Post-action validation targets the same control used for the action, so it cannot prove the intended state transition.",
			Hint:    "Declare the resulting dialog, route, status, or content target as the required validation; Server will not infer it from the clicked control.",
		}
	}
	return nil
}

func sameStableTarget(a, b model.ActionTarget) bool {
	if strings.TrimSpace(a.TestID) != "" || strings.TrimSpace(b.TestID) != "" {
		return strings.TrimSpace(a.TestID) != "" && a.TestID == b.TestID
	}
	if strings.TrimSpace(a.Selector) != "" || strings.TrimSpace(b.Selector) != "" {
		return strings.TrimSpace(a.Selector) != "" && a.Selector == b.Selector
	}
	if strings.TrimSpace(a.ComponentRef) != "" || strings.TrimSpace(b.ComponentRef) != "" {
		return strings.TrimSpace(a.ComponentRef) != "" && a.ComponentRef == b.ComponentRef
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
