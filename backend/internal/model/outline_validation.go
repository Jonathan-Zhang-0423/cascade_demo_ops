package model

import (
	"fmt"
	"net/url"
	"path"
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
		if selectors := distinctPrimarySelectors(step, stage, outline); len(selectors) > 1 {
			return &OutlineConsistencyError{Code: "selector_binding_mismatch", NodeID: step.NodeID, Reason: "has different primary selectors across plan_json, stage_approval_plan, and script_outline"}
		}
		if err := validateExecutionRouteConsistency(step, stage, outline); err != nil {
			return &OutlineConsistencyError{Code: "selector_route_provenance_mismatch", NodeID: step.NodeID, Reason: err.Error()}
		}
		if err := validatePrimarySelectorRouteProvenance(step, stage, outline); err != nil {
			return &OutlineConsistencyError{Code: "selector_route_provenance_mismatch", NodeID: step.NodeID, Reason: err.Error()}
		}
		if stageRequiresAuthenticationContext(step, stage, outline) {
			if !stageHasLoginEntryEvidence(step, stage, outline) {
				return &OutlineConsistencyError{Code: "login_entry_evidence_missing", NodeID: step.NodeID, Reason: "does not bind the approved authentication entry route to formal page-scan evidence"}
			}
			if !stageHasVerifiedAuthenticationContext(step, stage, outline) {
				return &OutlineConsistencyError{Code: "authentication_context_unverified", NodeID: step.NodeID, Reason: "does not prove an authentication page and password-bearing authentication form, or includes marketing email semantics"}
			}
			if !stageHasLoginSuccessValidation(step, stage, outline) {
				return &OutlineConsistencyError{Code: "login_success_validation_missing", NodeID: step.NodeID, Reason: "requires both a post-login route assertion and an authenticated workspace element assertion"}
			}
		}
		if step.RuntimeAdaptive {
			if selector := firstPrimarySelector(step, stage, outline); selector != "" && !bundleHasFormalSelectorCandidate(step, stage, outline, selector) {
				return &OutlineConsistencyError{Code: "selector_primary_provenance_missing", NodeID: step.NodeID, Reason: fmt.Sprintf("uses runtime-adaptive primary selector %q without a matching formal candidate", selector)}
			}
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

func validateExecutionRouteConsistency(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) error {
	routes := []struct {
		field string
		value string
	}{
		{"plan_json.steps[].page_target.url", step.PageTarget.URL},
		{"stage_approval_plan.stages[].entry_route", stage.EntryRoute},
		{"script_outline.stages[].route", outline.Route},
	}
	var base struct{ field, value string }
	for _, candidate := range routes {
		candidate.value = strings.TrimSpace(candidate.value)
		if candidate.value == "" {
			continue
		}
		if base.value == "" {
			base = candidate
			continue
		}
		if !routesEquivalent(base.value, candidate.value) {
			return fmt.Errorf("binds incompatible execution routes at %s and %s", base.field, candidate.field)
		}
	}
	return nil
}

func validatePrimarySelectorRouteProvenance(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) error {
	executionRoute := firstNonEmptyOutlineRoute(step.PageTarget.URL, stage.EntryRoute, outline.Route)
	if strings.TrimSpace(executionRoute) == "" {
		return nil
	}
	primary := firstPrimarySelector(step, stage, outline)
	if strings.TrimSpace(primary) == "" {
		return nil
	}
	for _, candidate := range primarySelectorCandidates(step, stage, outline) {
		if !selectorCandidateMatchesPrimary(candidate, primary) || strings.TrimSpace(candidate.SourceKind) != "page_scan" {
			continue
		}
		observedRoute := firstNonEmptyOutlineRoute(candidate.ObservedRouteTemplate, candidate.ObservedURL)
		if observedRoute == "" || !routesEquivalent(observedRoute, executionRoute) {
			return fmt.Errorf("uses primary selector %q observed on page-scan route %q, not execution route %q", primary, observedRoute, executionRoute)
		}
	}
	return nil
}

func primarySelectorCandidates(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) []SelectorCandidate {
	values := append([]SelectorCandidate{}, step.PageTarget.SelectorAlternatives...)
	values = append(values, step.Action.Target.SelectorAlternatives...)
	values = append(values, stage.Interaction.Target.SelectorAlternatives...)
	for _, interaction := range outline.Interactions {
		values = append(values, interaction.Target.SelectorAlternatives...)
	}
	for _, component := range outline.Components {
		values = append(values, component.SelectorAlternatives...)
	}
	return values
}

func firstNonEmptyOutlineRoute(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func routesEquivalent(left, right string) bool {
	return routeMatches(left, right) || routeMatches(right, left)
}

func routeMatches(observed, expected string) bool {
	observedURL, observedAbsolute, ok := parseOutlineRoute(observed)
	if !ok {
		return false
	}
	expectedURL, expectedAbsolute, ok := parseOutlineRoute(expected)
	if !ok {
		return false
	}
	if observedAbsolute && expectedAbsolute && !strings.EqualFold(observedURL.Scheme+"://"+observedURL.Host, expectedURL.Scheme+"://"+expectedURL.Host) {
		return false
	}
	observedSegments := outlineRouteSegments(observedURL.Path)
	expectedSegments := outlineRouteSegments(expectedURL.Path)
	if len(observedSegments) != len(expectedSegments) {
		return false
	}
	for index := range expectedSegments {
		expectedSegment := expectedSegments[index]
		if strings.HasPrefix(expectedSegment, "{") && strings.HasSuffix(expectedSegment, "}") && len(expectedSegment) > 2 {
			if observedSegments[index] == "" {
				return false
			}
			continue
		}
		if observedSegments[index] != expectedSegment {
			return false
		}
	}
	if (expectedURL.RawQuery != "" || observedURL.RawQuery != "") && observedURL.Query().Encode() != expectedURL.Query().Encode() {
		return false
	}
	return true
}

func parseOutlineRoute(value string) (*url.URL, bool, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false, false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, false, false
	}
	absolute := parsed.IsAbs() || parsed.Host != ""
	parsed.Fragment = ""
	parsed.Path = path.Clean("/" + strings.TrimPrefix(parsed.Path, "/"))
	return parsed, absolute, true
}

func outlineRouteSegments(value string) []string {
	value = strings.Trim(path.Clean("/"+strings.TrimPrefix(value, "/")), "/")
	if value == "" || value == "." {
		return nil
	}
	return strings.Split(value, "/")
}

func stageRequiresAuthenticationContext(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) bool {
	if step.StageKind != BusinessStageKindSessionSetup {
		return false
	}
	text := strings.ToLower(strings.Join([]string{
		step.Title, step.ExpectedOutcome, step.Action.Target.Label, step.Action.Target.Text,
		stage.Title, stage.Objective, stage.BusinessIntent, stage.SuccessState,
		outline.Objective, outline.SuccessState,
	}, " "))
	return stageHasSecretRef(step, stage) || containsAnyOutlineToken(text, "login", "sign in", "signin", "auth", "登录", "登陆", "登入", "密码", "password")
}

func stageHasLoginEntryEvidence(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) bool {
	authRoute := firstNonEmptyOutlineRoute(step.PageTarget.URL, stage.EntryRoute, outline.Route)
	if authRoute == "" {
		return false
	}
	for _, candidate := range primarySelectorCandidates(step, stage, outline) {
		if candidate.SourceKind != "page_scan" || candidate.ObservedURL == "" || candidate.EvidenceID == "" {
			continue
		}
		if routesEquivalent(candidate.ObservedURL, authRoute) {
			return true
		}
	}
	return false
}

func stageHasVerifiedAuthenticationContext(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) bool {
	targetNames := []string{}
	if step.TargetContract != nil {
		targetNames = append(targetNames, step.TargetContract.AllowedNames...)
	}
	if stage.TargetContract != nil {
		targetNames = append(targetNames, stage.TargetContract.AllowedNames...)
	}
	if outline.TargetContract != nil {
		targetNames = append(targetNames, outline.TargetContract.AllowedNames...)
	}
	for _, name := range targetNames {
		if marketingAuthenticationName(name) {
			return false
		}
	}
	for _, candidate := range primarySelectorCandidates(step, stage, outline) {
		if candidate.SourceKind != "page_scan" {
			continue
		}
		if marketingAuthenticationName(candidate.ObservedAccessibleName) {
			return false
		}
		if strings.EqualFold(strings.TrimSpace(candidate.ObservedPageRole), "authentication") && strings.EqualFold(strings.TrimSpace(candidate.ObservedFormRole), "authentication") && candidate.EvidenceDigestSHA256 != "" {
			return true
		}
	}
	return false
}

func stageHasLoginSuccessValidation(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) bool {
	authRoute := firstNonEmptyOutlineRoute(step.PageTarget.URL, stage.EntryRoute, outline.Route)
	hasPostLoginRoute := false
	hasWorkspaceState := false
	for _, validation := range step.Validations {
		if !validation.Required {
			continue
		}
		if validation.Kind == "url_matches" {
			target := strings.TrimSpace(validation.Target.URL)
			if target == "" {
				if expected, ok := validation.Expected.(string); ok {
					target = strings.TrimSpace(expected)
				}
			}
			if target != "" && authRoute != "" && !routesEquivalent(target, authRoute) {
				hasPostLoginRoute = true
			}
			continue
		}
		if validation.Target.Selector != "" || validation.Target.TestID != "" || validation.Target.Role != "" || validation.Target.Label != "" || validation.Target.Text != "" {
			hasWorkspaceState = true
		}
	}
	return hasPostLoginRoute && hasWorkspaceState
}

func marketingAuthenticationName(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return containsAnyOutlineToken(value, "newsletter", "waitlist", "subscribe", "marketing", "enter your email address", "订阅", "候补")
}

func containsAnyOutlineToken(value string, tokens ...string) bool {
	for _, token := range tokens {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
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
		candidate.ObservedAt == nil || candidate.ObservedAt.IsZero() {
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

func distinctPrimarySelectors(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) []string {
	values := []string{
		firstNonEmptyOutlineSelector(step.Action.Target.Selector, step.PageTarget.Selector),
		stage.Interaction.Target.Selector,
	}
	for _, interaction := range outline.Interactions {
		if interaction.Kind == step.Action.Type || len(outline.Interactions) == 1 {
			values = append(values, interaction.Target.Selector)
		}
	}
	seen := map[string]struct{}{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		normalized := strings.ToLower(value)
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, value)
	}
	return result
}

func firstPrimarySelector(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage) string {
	if values := distinctPrimarySelectors(step, stage, outline); len(values) > 0 {
		return values[0]
	}
	return ""
}

func bundleHasFormalSelectorCandidate(step ScriptStep, stage StageApprovalStage, outline BrowserAgentOutlineStage, selector string) bool {
	candidates := append([]SelectorCandidate{}, step.Action.Target.SelectorAlternatives...)
	candidates = append(candidates, step.PageTarget.SelectorAlternatives...)
	candidates = append(candidates, stage.Interaction.Target.SelectorAlternatives...)
	for _, interaction := range outline.Interactions {
		candidates = append(candidates, interaction.Target.SelectorAlternatives...)
	}
	for _, component := range outline.Components {
		candidates = append(candidates, component.SelectorAlternatives...)
	}
	for _, candidate := range candidates {
		if SelectorCandidateHasFormalProvenance(candidate) && selectorCandidateMatchesPrimary(candidate, selector) {
			return true
		}
	}
	return false
}

func selectorCandidateMatchesPrimary(candidate SelectorCandidate, selector string) bool {
	candidateValue := strings.TrimSpace(candidate.Value)
	selector = strings.TrimSpace(selector)
	if candidateValue == "" || selector == "" {
		return false
	}
	if strings.EqualFold(candidateValue, selector) {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(candidate.Kind), "testid") {
		return false
	}
	compact := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(selector, "\"", "'"), " ", ""))
	want := "data-testid='" + strings.ToLower(candidateValue) + "'"
	return strings.Contains(compact, want)
}

func firstNonEmptyOutlineSelector(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
