package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
	"cascade-demoops/backend/internal/sidecar"
)

type PageInteractionVerifierAgent struct {
	runtime config.AppRuntimeConfig
	manager *sidecar.Manager
}

const pageInteractionVerifierTimeoutMS = 60 * 1000

func NewPageInteractionVerifierAgent() *PageInteractionVerifierAgent {
	return &PageInteractionVerifierAgent{}
}

func NewPageInteractionVerifierAgentWithRuntime(runtime config.AppRuntimeConfig) *PageInteractionVerifierAgent {
	return &PageInteractionVerifierAgent{runtime: runtime, manager: sidecar.NewManager(runtime)}
}

func (a *PageInteractionVerifierAgent) VerifyInteractions(
	ctx context.Context,
	project *model.ProjectContext,
	brief *model.RequirementBrief,
	report *model.MultimodalUnderstandingReport,
	productMap *model.ProductMap,
	intelligence *model.ProjectIntelligencePack,
	credentials orchestrator.PageVerificationCredentials,
) (*model.VerifiedInteractionPlan, *model.MissingEvidenceReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if project == nil {
		return nil, nil, errors.New("project context is required")
	}
	if intelligence == nil {
		return nil, missingEvidence(project, nil, "project_intelligence_missing", "项目理解图谱缺失，无法验证交互动作。", "重新运行项目理解链路。"), nil
	}
	if intelligence.RunIntentScope == nil {
		intelligence.RunIntentScope = runIntentScopeForProject(project)
	}
	candidates := verifierCandidates(intelligence)
	if a.manager != nil && project.ProductURL != "" {
		plan, missing, err := a.verifyWithSidecar(ctx, project, intelligence, candidates, credentials)
		if err == nil && plan != nil {
			if plan.BusinessActionCount <= 0 {
				if fallbackPlan := fallbackPlanFromExplicitIntent(project, intelligence, candidates, errors.New("page scan did not verify a business action")); fallbackPlan != nil && fallbackPlan.BusinessActionCount > 0 {
					fallbackMissing := nonBlockingEvidenceFallbackReport(project, intelligence, "page_scan_no_business_action", "页面预扫描未确认业务控件，已按显式需求生成 runtime-adaptive 大纲。", missing)
					intelligence.VerifiedInteraction = fallbackPlan
					intelligence.MissingEvidenceReport = fallbackMissing
					return fallbackPlan, fallbackMissing, nil
				}
			}
			intelligence.VerifiedInteraction = plan
			intelligence.MissingEvidenceReport = missing
			return plan, missing, nil
		}
		fallbackPlan, fallbackMissing := verifyFromPageEvidence(project, intelligence, candidates)
		if fallbackPlan != nil && fallbackPlan.BusinessActionCount > 0 {
			intelligence.VerifiedInteraction = fallbackPlan
			intelligence.MissingEvidenceReport = fallbackMissing
			return fallbackPlan, fallbackMissing, nil
		}
		if err != nil {
			report := nonBlockingSidecarFailureReport(project, intelligence, err)
			fallbackPlan = fallbackPlanFromExplicitIntent(project, intelligence, candidates, err)
			if fallbackPlan == nil || fallbackPlan.BusinessActionCount == 0 {
				fallbackPlan = fallbackPlanFromCodeEvidence(project, intelligence, candidates, err)
			}
			intelligence.VerifiedInteraction = fallbackPlan
			intelligence.MissingEvidenceReport = report
			return fallbackPlan, report, nil
		}
	}
	plan, missing := verifyFromPageEvidence(project, intelligence, candidates)
	if project.ProductURL != "" && (plan == nil || plan.BusinessActionCount <= 0) {
		if fallbackPlan := fallbackPlanFromExplicitIntent(project, intelligence, candidates, errors.New("page material did not verify a business action")); fallbackPlan != nil && fallbackPlan.BusinessActionCount > 0 {
			fallbackMissing := nonBlockingEvidenceFallbackReport(project, intelligence, "page_material_no_business_action", "缺少页面预扫描证据，已按显式需求生成 runtime-adaptive 大纲。", missing)
			intelligence.VerifiedInteraction = fallbackPlan
			intelligence.MissingEvidenceReport = fallbackMissing
			return fallbackPlan, fallbackMissing, nil
		}
	}
	intelligence.VerifiedInteraction = plan
	intelligence.MissingEvidenceReport = missing
	_ = brief
	_ = report
	_ = productMap
	return plan, missing, nil
}

type interactionVerifierRequest struct {
	ProductURL           string                    `json:"product_url,omitempty"`
	TimeoutMS            int                       `json:"timeout_ms,omitempty"`
	Headless             bool                      `json:"headless"`
	AllowedDomains       []string                  `json:"allowed_domains,omitempty"`
	ForbiddenPaths       []string                  `json:"forbidden_path_prefixes,omitempty"`
	Candidates           []interactionVerifierItem `json:"candidates,omitempty"`
	IntentGoals          []interactionVerifierGoal `json:"intent_goals,omitempty"`
	SafeStateTransitions []interactionVerifierItem `json:"safe_state_transitions,omitempty"`
	DemoUsername         string                    `json:"demo_username,omitempty"`
	DemoPassword         string                    `json:"demo_password,omitempty"`
}

type interactionVerifierGoal struct {
	ID         string   `json:"id,omitempty"`
	Label      string   `json:"label,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	Keywords   []string `json:"keywords,omitempty"`
	Required   bool     `json:"required,omitempty"`
	Business   bool     `json:"business,omitempty"`
	InputValue string   `json:"input_value,omitempty"`
}

type interactionVerifierItem struct {
	ID           string `json:"id,omitempty"`
	IntentGoalID string `json:"intent_goal_id,omitempty"`
	Label        string `json:"label,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Selector     string `json:"selector,omitempty"`
	URL          string `json:"url,omitempty"`
}

type interactionVerifierResponse struct {
	OK               bool                              `json:"ok"`
	VerificationMode string                            `json:"verification_mode,omitempty"`
	BrowserScanID    string                            `json:"browser_scan_id,omitempty"`
	SourceURL        string                            `json:"source_url,omitempty"`
	Results          []interactionVerifierResult       `json:"results,omitempty"`
	Diagnostics      *interactionVerifierDiagnostics   `json:"diagnostics,omitempty"`
	Error            *interactionVerifierResponseError `json:"error,omitempty"`
}

type interactionVerifierDiagnostics struct {
	LoginAttempted                 bool                        `json:"login_attempted,omitempty"`
	LoginStatus                    string                      `json:"login_status,omitempty"`
	FinalURL                       string                      `json:"final_url,omitempty"`
	PageTitle                      string                      `json:"page_title,omitempty"`
	CandidateCount                 int                         `json:"candidate_count,omitempty"`
	VerifiedCandidateCount         int                         `json:"verified_candidate_count,omitempty"`
	DiscoveredBusinessControlCount int                         `json:"discovered_business_control_count,omitempty"`
	LoginTransitions               []string                    `json:"login_transitions,omitempty"`
	LoginEvidence                  []interactionVerifierResult `json:"login_evidence,omitempty"`
}

type interactionVerifierResult struct {
	interactionVerifierItem
	Status                 string    `json:"status,omitempty"`
	Visible                bool      `json:"visible,omitempty"`
	Enabled                bool      `json:"enabled,omitempty"`
	Editable               bool      `json:"editable,omitempty"`
	PageURL                string    `json:"page_url,omitempty"`
	PageTitle              string    `json:"page_title,omitempty"`
	Message                string    `json:"message,omitempty"`
	VerifiedAt             time.Time `json:"verified_at,omitempty"`
	EvidenceID             string    `json:"evidence_id,omitempty"`
	SourceKind             string    `json:"source_kind,omitempty"`
	SourceDigest           string    `json:"source_digest,omitempty"`
	ObservedRole           string    `json:"observed_role,omitempty"`
	ObservedAccessibleName string    `json:"observed_accessible_name,omitempty"`
	ObservedURL            string    `json:"observed_url,omitempty"`
	ObservedRouteTemplate  string    `json:"observed_route_template,omitempty"`
	ObservedPageRole       string    `json:"observed_page_role,omitempty"`
	ObservedFormRole       string    `json:"observed_form_role,omitempty"`
	EvidenceDigestSHA256   string    `json:"evidence_digest_sha256,omitempty"`
	ObservedAt             time.Time `json:"observed_at,omitempty"`
}

type interactionVerifierResponseError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func (a *PageInteractionVerifierAgent) verifyWithSidecar(ctx context.Context, project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, candidates []model.InteractionProbe, credentials orchestrator.PageVerificationCredentials) (*model.VerifiedInteractionPlan, *model.MissingEvidenceReport, error) {
	spec := a.manager.VideoWorkerSpec()
	if len(spec.Args) == 0 {
		return nil, nil, errors.New("video-worker sidecar path is missing")
	}
	if _, err := os.Stat(spec.Args[0]); err != nil {
		return nil, nil, err
	}
	items := make([]interactionVerifierItem, 0, len(candidates))
	byID := map[string]model.InteractionProbe{}
	for _, candidate := range candidates {
		if !isURLAllowedByRunScope(intelligence.RunIntentScope, candidate.URL) || isControlPlaneSignal(intelligence.RunIntentScope, candidate.URL, candidate.Label, candidate.Selector) {
			continue
		}
		items = append(items, interactionVerifierItem{
			ID:           candidate.ID,
			IntentGoalID: candidate.IntentGoalID,
			Label:        candidate.Label,
			Kind:         candidate.Kind,
			Selector:     candidate.Selector,
			URL:          firstNonEmpty(candidate.URL, project.ProductURL),
		})
		byID[candidate.ID] = candidate
	}
	var response interactionVerifierResponse
	err := a.manager.CallJSONRPC(ctx, spec, "verify_interactions", interactionVerifierRequest{
		ProductURL: project.ProductURL,
		// Production product pages can keep DOMContentLoaded pending while
		// authentication/runtime chunks warm up. Twenty seconds produced a false
		// page_unreachable result on an otherwise healthy page, discarding formal
		// login and business-control evidence. Keep this bounded but give the real
		// browser scan enough time to reach the authenticated workspace.
		TimeoutMS:            pageInteractionVerifierTimeoutMS,
		Headless:             true,
		AllowedDomains:       allowedDomainsFromScope(project, intelligence.RunIntentScope),
		ForbiddenPaths:       intelligence.RunIntentScope.ForbiddenPathPrefixes,
		Candidates:           items,
		IntentGoals:          verifierGoals(intelligence),
		SafeStateTransitions: verifierSafeStateTransitions(project, intelligence),
		DemoUsername:         credentials.DemoUsername,
		DemoPassword:         credentials.DemoPassword,
	}, &response)
	if err != nil {
		return nil, nil, err
	}
	if !response.OK && len(response.Results) == 0 {
		report := missingEvidence(project, intelligence.DemoIntent, firstNonEmpty(responseErrorCode(response.Error), "page_unreachable"), responseErrorMessage(response.Error), "确认产品 URL 可访问、登录态可用，并重新运行页面预扫描。")
		return emptyVerifiedPlan(project, intelligence, response.VerificationMode, response.BrowserScanID, response.SourceURL), report, nil
	}
	return verifiedPlanFromScanResults(project, intelligence, response, byID), missingEvidenceFromScanResults(project, intelligence, response, byID), nil
}

func verifierSafeStateTransitions(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack) []interactionVerifierItem {
	if project == nil || intelligence == nil || intelligence.BusinessStagePlan == nil {
		return nil
	}
	for _, stage := range intelligence.BusinessStagePlan.Stages {
		stageID := strings.TrimPrefix(stage.ID, "business_stage_")
		semanticText := strings.Join([]string{stage.Title, stage.Objective, stage.Action.Label, stage.Action.SuccessState}, " ")
		if stageID != "new_project_entry" || stage.Kind != model.BusinessStageKindBusinessAction ||
			model.GraphActionType(stage.Action.Type) != model.GraphActionClick || !stage.Action.NonDestructive ||
			containsAnyNormalized(semanticText, "delete", "remove", "destroy", "payment", "pay", "billing", "purchase", "refund", "permission", "role", "api key", "secret", "token", "删除", "移除", "销毁", "支付", "购买", "退款", "账单", "权限", "角色", "密钥", "令牌") {
			continue
		}
		bestIndex := -1
		for index, target := range stage.Targets {
			targetURL := firstNonEmpty(target.URL, stage.EntryRoute, project.ProductURL)
			if strings.TrimSpace(target.Selector) == "" || !isExplicitNewProjectEntryCandidate(target) ||
				!isURLAllowedByRunScope(intelligence.RunIntentScope, targetURL) ||
				isControlPlaneSignal(intelligence.RunIntentScope, targetURL, target.Label, target.Selector) {
				continue
			}
			if bestIndex < 0 || businessStageTargetRank(stage, target) > businessStageTargetRank(stage, stage.Targets[bestIndex]) {
				bestIndex = index
			}
		}
		if bestIndex >= 0 {
			target := stage.Targets[bestIndex]
			return []interactionVerifierItem{{
				ID: stage.ID, IntentGoalID: target.IntentGoalID, Label: firstNonEmpty(target.Label, stage.Action.Label),
				Kind: string(model.GraphActionClick), Selector: target.Selector, URL: firstNonEmpty(target.URL, stage.EntryRoute, project.ProductURL),
			}}
		}
	}
	return nil
}

func verifierCandidates(intelligence *model.ProjectIntelligencePack) []model.InteractionProbe {
	candidates := []model.InteractionProbe{}
	if intelligence == nil || intelligence.FeatureTrace == nil {
		return candidates
	}
	seen := map[string]bool{}
	for _, trace := range intelligence.FeatureTrace.Traces {
		for _, probe := range trace.SelectorEvidence {
			if probe.IntentGoalID == "" {
				probe.IntentGoalID = trace.IntentGoalID
			}
			if !probe.IsBusiness || probe.IsChrome || !selectorUsableForBusinessAction(probe.Selector) {
				continue
			}
			if !isURLAllowedByRunScope(intelligence.RunIntentScope, probe.URL) || isControlPlaneSignal(intelligence.RunIntentScope, probe.URL, probe.Label, probe.Selector) {
				continue
			}
			key := normalizeSelector(probe.Selector)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			candidates = append(candidates, probe)
		}
	}
	return candidates
}

func fallbackPlanFromCodeEvidence(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, candidates []model.InteractionProbe, cause error) *model.VerifiedInteractionPlan {
	plan := emptyVerifiedPlan(project, intelligence, "sidecar_unavailable_code_evidence", "", project.ProductURL)
	plan.Confidence = 0.58
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if !candidate.IsBusiness || candidate.IsChrome || !selectorUsableForBusinessAction(candidate.Selector) {
			continue
		}
		if !isURLAllowedByRunScope(intelligence.RunIntentScope, candidate.URL) || isControlPlaneSignal(intelligence.RunIntentScope, candidate.URL, candidate.Label, candidate.Selector) {
			continue
		}
		key := normalizeSelector(candidate.Selector)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		action := verifiedActionFromProbe(candidate, "feature_trace_code_evidence", "")
		action.VerificationStatus = "sidecar_unavailable"
		action.VerificationSource = "feature_trace_code_evidence"
		action.VerifiedAt = time.Time{}
		action.ExpectedOutcome = firstNonEmpty(action.ExpectedOutcome, "基于代码证据执行需求相关业务动作")
		action.SuccessState = firstNonEmpty(action.SuccessState, "运行时确认代码证据对应控件可执行")
		action.EvidenceRefs = uniqueEvidenceRefs(append(action.EvidenceRefs, model.EvidenceRef{
			ID:         "ev_sidecar_unavailable_" + shortHash(project.ID+candidate.Selector+fmt.Sprint(cause)),
			Kind:       model.EvidenceKindCodeSnapshot,
			Summary:    "页面预扫描 sidecar 异常，临时使用需求追踪后的代码 selector 证据继续生成脚本。",
			FieldPath:  "project_intelligence.feature_trace.selector_evidence",
			Confidence: 0.58,
		}))
		plan.Actions = append(plan.Actions, action)
		if action.IsBusiness {
			plan.BusinessActionCount++
		}
		plan.EvidenceRefs = append(plan.EvidenceRefs, action.EvidenceRefs...)
		if len(plan.Actions) >= 4 {
			break
		}
	}
	plan.EvidenceRefs = uniqueEvidenceRefs(plan.EvidenceRefs)
	return plan
}

func fallbackPlanFromExplicitIntent(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, candidates []model.InteractionProbe, cause error) *model.VerifiedInteractionPlan {
	intentText := explicitDemoIntentText(project, intelligence)
	if intentText == "" {
		return nil
	}
	plan := emptyVerifiedPlan(project, intelligence, "runtime_adaptive_intent_fallback", "", project.ProductURL)
	plan.Confidence = 0.66
	evidence := model.EvidenceRef{
		ID:         "ev_runtime_adaptive_intent_" + shortHash(project.ID+intentText+fmt.Sprint(cause)),
		Kind:       model.EvidenceKindRequirementDoc,
		Summary:    "页面预扫描不可用，按用户显式需求编译运行时语义动作；不使用全局 selector 池补动作。",
		FieldPath:  "project_intelligence.demo_intent",
		Confidence: 0.66,
	}
	add := func(action model.VerifiedInteractionAction) {
		action.VerificationStatus = "runtime_adaptive"
		action.VerificationSource = "runtime_adaptive_intent_fallback"
		action.VerifiedAt = time.Time{}
		action.EvidenceRefs = uniqueEvidenceRefs(append(action.EvidenceRefs, evidence))
		plan.Actions = append(plan.Actions, action)
		if action.IsBusiness {
			plan.BusinessActionCount++
		}
		plan.EvidenceRefs = append(plan.EvidenceRefs, action.EvidenceRefs...)
	}
	if containsAnyNormalized(intentText, "登录", "登陆", "登入", "login", "sign in", "signin") {
		add(intentWaitAction(project, "intent_login_observe", "演示登录完成并进入工作台", "登录完成，进入可演示的产品工作台上下文。", durationMSForIntentKeywords(intentText, "登录", "登陆", "登入", "login", "sign in", "signin"), false))
	}
	if containsAnyNormalized(intentText, "新建项目", "创建项目", "新增项目", "new project", "create project") {
		add(intentSelectorAction(project, candidates, "intent_new_project", "新建项目", "click", "点击新建项目入口", "进入新建项目流程。", durationMSForIntentKeywords(intentText, "新建项目", "创建项目", "新增项目", "new project", "create project"), true, []string{"新建项目", "创建项目", "新增项目", "new project", "create project"}))
	}
	projectName := intentProjectName(intentText)
	if projectName != "" {
		add(intentSelectorAction(project, candidates, "intent_project_name", "输入项目名称："+projectName, "fill", "填写项目名称", "项目名称已填写为 "+projectName+"。", durationMSForIntentKeywords(intentText, "项目名称", "项目名", "project name", "name", projectName), true, []string{"项目名称", "项目名", "project name", "name", projectName}, withIntentActionValue(projectName)))
	}
	if containsAnyNormalized(intentText, "构建模式", "build mode", "builder mode") {
		add(intentSelectorAction(project, candidates, "intent_build_mode", "选择构建模式", "click", "选择构建模式", "项目已切换到构建模式。", durationMSForIntentKeywords(intentText, "构建模式", "build mode", "builder mode"), true, []string{"构建模式", "build mode", "builder mode", "构建"}))
	}
	if containsAnyNormalized(intentText, "agent", "智能体", "实际构建", "开始构建", "run build", "start build", "生成") ||
		containsAnyNormalized(intentText, "构建") {
		add(intentSelectorAction(project, candidates, "intent_start_agent_build", "启动 agent 实际构建", "click", "启动 agent 构建", "agent 已开始根据需求实际构建项目。", durationMSForIntentKeywords(intentText, "启动", "开始", "提交", "agent", "智能体", "构建", "build", "run", "start"), true, []string{"agent", "智能体", "开始构建", "实际构建", "生成", "build", "run", "start"}))
	}
	if wantsBuildCompletion(intentText) {
		add(intentSelectorAction(project, candidates, "intent_agent_build_complete", "Agent 构建完成结果", "inspect", "等待产品明确报告构建完成", "Agent 构建已完成且最终结果可见。", 0, true, []string{"build-result", "build complete", "build_complete", "all complete", "completed", "构建完成", "全部步骤完成", "完成结果"}))
	}
	if waitMS := requiredObservationDurationMS(intentText); waitMS > 0 {
		add(intentWaitAction(project, "intent_agent_build_wait", fmt.Sprintf("等待 agent 实际构建 %d 秒", waitMS/1000), "持续观察 agent 构建过程，等待结果逐步出现。", waitMS, true))
	}
	plan.EvidenceRefs = uniqueEvidenceRefs(plan.EvidenceRefs)
	if len(plan.Actions) == 0 {
		return nil
	}
	return plan
}

type intentActionOption func(*model.VerifiedInteractionAction)

func withIntentActionValue(value string) intentActionOption {
	return func(action *model.VerifiedInteractionAction) {
		if action != nil {
			action.InputValue = value
			action.SuccessState = firstNonEmpty(action.SuccessState, "已输入 "+value)
		}
	}
}

func intentSelectorAction(
	project *model.ProjectContext,
	candidates []model.InteractionProbe,
	id string,
	label string,
	kind string,
	expected string,
	success string,
	durationMS int,
	business bool,
	keywords []string,
	options ...intentActionOption,
) model.VerifiedInteractionAction {
	selector := ""
	alternatives := []model.SelectorCandidate{}
	if probe, ok := bestIntentProbe(candidates, keywords, kind); ok {
		selector = probe.Selector
		for _, candidate := range probe.Alternatives {
			if model.SelectorCandidateHasFormalProvenance(candidate) {
				alternatives = append(alternatives, candidate)
			}
		}
	}
	action := model.VerifiedInteractionAction{
		ID:                 id,
		IntentGoalID:       id,
		Label:              label,
		Kind:               kind,
		Selector:           selector,
		URL:                project.ProductURL,
		RouteRef:           safeID("route", firstNonEmpty(project.ProductURL, "product")),
		ExpectedOutcome:    expected,
		SuccessState:       success,
		WaitConditions:     []string{"domcontentloaded"},
		DurationHintMS:     durationMS,
		IsBusiness:         business,
		NonDestructive:     true,
		VerificationStatus: "runtime_adaptive",
		VerificationSource: "runtime_adaptive_intent_fallback",
		SelectorScore:      selectorQualityScore(selector),
		Alternatives:       uniqueSelectorCandidates(alternatives),
	}
	for _, option := range options {
		option(&action)
	}
	return action
}

func intentWaitAction(project *model.ProjectContext, id string, label string, success string, durationMS int, business bool) model.VerifiedInteractionAction {
	return model.VerifiedInteractionAction{
		ID:                 id,
		IntentGoalID:       id,
		Label:              label,
		Kind:               "wait",
		URL:                project.ProductURL,
		RouteRef:           safeID("route", firstNonEmpty(project.ProductURL, "product")),
		ExpectedOutcome:    success,
		SuccessState:       success,
		WaitConditions:     []string{"domcontentloaded"},
		DurationHintMS:     durationMS,
		IsBusiness:         business,
		NonDestructive:     true,
		VerificationStatus: "runtime_adaptive",
		VerificationSource: "runtime_adaptive_intent_fallback",
	}
}

func bestIntentProbe(candidates []model.InteractionProbe, keywords []string, preferredKind string) (model.InteractionProbe, bool) {
	var best model.InteractionProbe
	bestScore := -1
	for _, candidate := range candidates {
		if !candidate.IsBusiness || candidate.IsChrome || !selectorUsableForBusinessAction(candidate.Selector) {
			continue
		}
		// A runtime-adaptive fallback must not promote a code guess or an
		// unverified primary selector into an executable locator. The primary
		// value is usable only when the same selector is represented by a
		// complete, evidence-bound candidate produced by a scan or approved
		// annotation.
		if !probeHasFormalSelectorCandidate(candidate) {
			continue
		}
		if !candidateKindMatchesIntent(candidate, preferredKind) || candidateLooksNegativeForIntent(candidate, keywords) {
			continue
		}
		text := normalizeIntentText(strings.Join([]string{candidate.Label, candidate.Selector, candidate.Kind, candidate.ComponentRef}, " "))
		if !containsAnyNormalized(text, keywords...) {
			continue
		}
		score := candidate.SelectorScore
		if score == 0 {
			score = selectorQualityScore(candidate.Selector)
		}
		if score > bestScore {
			best = candidate
			bestScore = score
		}
	}
	return best, bestScore >= 0
}

func probeHasFormalSelectorCandidate(probe model.InteractionProbe) bool {
	selector := strings.TrimSpace(probe.Selector)
	if selector == "" {
		return false
	}
	for _, candidate := range probe.Alternatives {
		if model.SelectorCandidateHasFormalProvenance(candidate) && selectorCandidateMatchesSelector(candidate, selector) {
			return true
		}
	}
	return false
}

func selectorCandidateMatchesSelector(candidate model.SelectorCandidate, selector string) bool {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(candidate.Value), selector) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(candidate.Kind), "testid") && strings.EqualFold(strings.TrimSpace(candidate.Value), testIDFromSelector(selector))
}

func candidateKindMatchesIntent(candidate model.InteractionProbe, preferredKind string) bool {
	preferred := graphActionTypeFromKind(preferredKind, "")
	if preferred == "" {
		return true
	}
	actual := graphActionTypeFromKind(candidate.Kind, candidate.Selector)
	if actual == preferred {
		return true
	}
	if preferred == model.GraphActionClick && actual == model.GraphActionInspect && containsAnyNormalized(candidate.Label+" "+candidate.Selector, "button", "btn", "submit", "start", "create", "new", "build", "generate") {
		return true
	}
	return false
}

func candidateLooksNegativeForIntent(candidate model.InteractionProbe, keywords []string) bool {
	text := normalizeIntentText(strings.Join([]string{candidate.Label, candidate.Selector, candidate.Kind, candidate.ComponentRef}, " "))
	if text == "" {
		return false
	}
	if !containsAnyNormalized(text, "cancel", "取消", "stop", "停止", "delete", "删除", "remove", "移除", "close", "关闭", "regenerate", "重新生成") {
		return false
	}
	return !containsAnyNormalized(strings.Join(keywords, " "), "cancel", "取消", "stop", "停止", "delete", "删除", "remove", "移除", "close", "关闭", "regenerate", "重新生成")
}

func explicitDemoIntentText(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack) string {
	parts := []string{}
	if project != nil {
		parts = append(parts,
			project.ProductDescription,
			project.TargetAudience,
			strings.Join(project.MustShow, " "),
			strings.Join(project.MustNotShow, " "),
		)
	}
	if intelligence != nil && intelligence.DemoIntent != nil {
		parts = append(parts, intelligence.DemoIntent.Objective, intelligence.DemoIntent.TargetAudience)
		for _, goal := range intelligence.DemoIntent.Goals {
			parts = append(parts, goal.Label, goal.Kind, goal.PreferredAction, goal.TargetPageHint, goal.SuccessState, strings.Join(goal.TargetKeywords, " "))
		}
	}
	return normalizeIntentText(strings.Join(parts, " "))
}

var intentProjectNamePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?:项目名称|项目名)\s*(?:为|是|[:：=])\s*([^，。；;,\n]{1,48})`),
	regexp.MustCompile(`(?:项目名称|项目名)\s*([^，。；;,\n\s（(]{1,48})`),
	regexp.MustCompile(`(?:新建|创建|新增)(?:一个)?(?:名为|名称为|项目名为)\s*[“”"'‘’]*([^“”"'‘’，。；;,\n]{1,48})[“”"'‘’]*(?:的)?项目`),
	// Chinese requests commonly put the desired name before “项目”, for
	// example “创建俄罗斯方块项目”. Match that form before the generic
	// “创建项目 <token>” form so the following operation (“启动 Agent”) is
	// never mistaken for the project name.
	regexp.MustCompile(`(?:新建|创建|新增)(?:一个)?([^，。；;,\n\s（）()]{1,32})项目`),
	regexp.MustCompile(`(?:新建|创建|新增)(?:一个)?项目\s*([^，。；;,\n\s（(]{1,48})`),
	regexp.MustCompile(`project\s+(?:named|called)\s+([a-z0-9][a-z0-9 _-]{0,47})`),
}

var intentDurationOnlyPattern = regexp.MustCompile(`^\d+(?:\.\d+)?\s*(?:秒|s|sec|secs|second|seconds)$`)
var intentProjectDetailsPattern = regexp.MustCompile(`(?:新建|创建|新增)(?:一个)?项目\s*[（(]([^）)]{1,96})[）)]`)

func intentProjectName(intentText string) string {
	intentText = normalizeIntentText(intentText)
	for _, match := range intentProjectDetailsPattern.FindAllStringSubmatch(intentText, -1) {
		if len(match) < 2 {
			continue
		}
		for _, part := range strings.FieldsFunc(match[1], func(r rune) bool { return r == ',' || r == '，' || r == ';' || r == '；' }) {
			if candidate := normalizeIntentProjectNameCandidate(part); candidate != "" {
				return candidate
			}
		}
	}
	for _, pattern := range intentProjectNamePatterns {
		for _, match := range pattern.FindAllStringSubmatch(intentText, -1) {
			if len(match) < 2 {
				continue
			}
			if candidate := normalizeIntentProjectNameCandidate(match[1]); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func normalizeIntentProjectNameCandidate(value string) string {
	candidate := strings.Trim(strings.TrimSpace(value), `"'“”‘’()（）:：=-`)
	if candidate == "" || intentDurationOnlyPattern.MatchString(candidate) || containsAnyNormalized(candidate,
		"新建项目", "创建项目", "新增项目", "new project", "create project",
		"构建模式", "build mode", "builder mode", "等待", "wait", "agent", "智能体",
		"启动", "开始", "输入", "填写", "选择", "打开", "进入", "查看", "提交", "创建", "新建", "新增",
	) {
		return ""
	}
	return candidate
}

func verifyFromPageEvidence(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, candidates []model.InteractionProbe) (*model.VerifiedInteractionPlan, *model.MissingEvidenceReport) {
	plan := emptyVerifiedPlan(project, intelligence, "page_material_evidence", "", project.ProductURL)
	missing := &model.MissingEvidenceReport{
		ID:            "missing_evidence_" + project.ID,
		ProjectID:     project.ID,
		SchemaVersion: model.ProjectIntelligencePackSchemaVersion,
		IntentID:      intentID(intelligence),
		CreatedAt:     time.Now().UTC(),
	}
	for _, candidate := range candidates {
		if candidate.Source != "page_reader" {
			missing.Items = append(missing.Items, missingEvidenceItem(project, candidate.IntentGoalID, "page_scan_unavailable", "候选 selector 仅来自代码摘要，尚未经过页面预扫描确认："+candidate.Selector, "打开产品 URL 完成只读 DOM/a11y 预扫描。"))
			continue
		}
		action := verifiedActionFromProbe(candidate, "page_material_evidence", "")
		plan.Actions = append(plan.Actions, action)
		if action.IsBusiness {
			plan.BusinessActionCount++
		}
		plan.EvidenceRefs = append(plan.EvidenceRefs, action.EvidenceRefs...)
	}
	if plan.BusinessActionCount == 0 {
		if len(missing.Items) == 0 {
			missing.Items = append(missing.Items, missingEvidenceItem(project, "", "selector", "no verified business action: 页面材料没有确认任何可执行业务动作 selector。", "补充真实页面预扫描、截图标注或稳定业务 selector 后重试。"))
		}
		missing.Blocking = true
		missing.Summary = "no verified business action: 页面材料没有确认任何可执行业务动作，已阻止生成录制脚本。"
	} else if len(missing.Items) > 0 {
		downgradeMissingEvidenceItems(missing.Items)
		missing.Summary = "部分候选动作缺少页面预扫描，已只保留页面材料验证过的动作。"
	}
	if len(missing.Items) > 0 {
		missing.Blocking = missing.Blocking || plan.BusinessActionCount == 0
		return plan, missing
	}
	return plan, nil
}

func nonBlockingSidecarFailureReport(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, cause error) *model.MissingEvidenceReport {
	report := &model.MissingEvidenceReport{
		ID:            "missing_evidence_" + project.ID,
		ProjectID:     project.ID,
		SchemaVersion: model.ProjectIntelligencePackSchemaVersion,
		IntentID:      intentID(intelligence),
		Blocking:      false,
		Summary:       "页面预扫描 sidecar 调用失败，已优先降级为显式需求语义脚本生成：" + cause.Error(),
		CreatedAt:     time.Now().UTC(),
	}
	report.Items = append(report.Items, model.MissingEvidenceItem{
		ID:              "missing_" + shortHash(project.ID+cause.Error()),
		MissingKind:     "page_scan_sidecar_failed",
		Severity:        "warning",
		Message:         "页面预扫描 sidecar 调用失败：" + cause.Error(),
		SuggestedAction: "继续生成围绕用户需求的 runtime-adaptive 脚本；若录制阶段 selector 失败，使用服务器 failure_diagnostic 修复。请同时检查 video-worker/dist/index.js 与 Playwright 运行环境。",
		FieldPath:       "project_intelligence.verified_interaction_plan",
	})
	return report
}

func nonBlockingEvidenceFallbackReport(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, kind string, summary string, original *model.MissingEvidenceReport) *model.MissingEvidenceReport {
	report := &model.MissingEvidenceReport{
		ID:            "missing_evidence_" + project.ID,
		ProjectID:     project.ID,
		SchemaVersion: model.ProjectIntelligencePackSchemaVersion,
		IntentID:      intentID(intelligence),
		Blocking:      false,
		Summary:       summary,
		CreatedAt:     time.Now().UTC(),
	}
	if original != nil {
		for _, item := range original.Items {
			item.Severity = "warning"
			if item.SuggestedAction == "" {
				item.SuggestedAction = "服务器 browser agent 将在产品域内自适应探索；如仍失败会返回 failure_diagnostic 和 repair_request。"
			}
			report.Items = append(report.Items, item)
		}
	}
	if len(report.Items) == 0 {
		report.Items = append(report.Items, model.MissingEvidenceItem{
			ID:              "missing_" + shortHash(project.ID+kind+summary),
			MissingKind:     kind,
			Severity:        "warning",
			Message:         summary,
			SuggestedAction: "继续生成受 StageApprovalPlan 约束的 runtime-adaptive 大纲；服务器端只允许在产品域内修正 selector、等待和探索路径。",
			FieldPath:       "project_intelligence.verified_interaction_plan",
		})
	}
	return report
}

func downgradeMissingEvidenceItems(items []model.MissingEvidenceItem) {
	for index := range items {
		items[index].Severity = "warning"
		if items[index].SuggestedAction == "" {
			items[index].SuggestedAction = "服务器 browser agent 将在产品域内自适应探索；如仍失败会返回 failure_diagnostic 和 repair_request。"
		}
	}
}

func verifiedPlanFromScanResults(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, response interactionVerifierResponse, byID map[string]model.InteractionProbe) *model.VerifiedInteractionPlan {
	plan := emptyVerifiedPlan(project, intelligence, firstNonEmpty(response.VerificationMode, "playwright_readonly_scan"), response.BrowserScanID, firstNonEmpty(response.SourceURL, project.ProductURL))
	if response.Diagnostics != nil && response.Diagnostics.LoginAttempted && response.Diagnostics.LoginStatus == "submitted_navigation_observed" {
		evidence := model.EvidenceRef{
			ID:         "ev_browser_scan_login_" + shortHash(response.BrowserScanID+response.SourceURL),
			Kind:       model.EvidenceKindBrowserScan,
			Summary:    "本地浏览器预扫描确认登录提交后已离开登录页并进入产品上下文。",
			FieldPath:  "verified_interaction_plan.actions.intent_login_observe",
			Confidence: 0.92,
		}
		loginAction := model.VerifiedInteractionAction{
			ID: "intent_login_observe", IntentGoalID: "intent_login_observe", Label: "演示登录完成并进入工作台",
			Kind: "wait", URL: firstNonEmpty(response.Diagnostics.FinalURL, response.SourceURL, project.ProductURL),
			RouteRef:        safeID("route", firstNonEmpty(response.Diagnostics.FinalURL, response.SourceURL, project.ProductURL)),
			ExpectedOutcome: "登录完成并进入工作台", SuccessState: "浏览器已离开登录页并进入经扫描的产品页面。",
			WaitConditions: []string{"domcontentloaded"}, VerificationStatus: "verified",
			NonDestructive:     true,
			VerificationSource: firstNonEmpty(response.VerificationMode, "playwright_readonly_scan"), VerifiedAt: time.Now().UTC(),
			EvidenceRefs: []model.EvidenceRef{evidence},
		}
		for _, loginResult := range response.Diagnostics.LoginEvidence {
			loginEvidence := model.EvidenceRef{
				ID:   firstNonEmpty(loginResult.EvidenceID, "ev_browser_scan_login_control_"+shortHash(response.BrowserScanID+loginResult.ID)),
				Kind: model.EvidenceKindBrowserScan, Summary: "本地浏览器预扫描确认认证入口或密码表单控件。",
				FieldPath: "verified_interaction_plan.actions.intent_login_observe.selector_alternatives", Confidence: 0.92,
			}
			if candidate, ok := selectorCandidateFromVerifierResult(loginResult, loginEvidence); ok {
				loginAction.Alternatives = uniqueSelectorCandidates(append(loginAction.Alternatives, candidate))
				loginAction.EvidenceRefs = append(loginAction.EvidenceRefs, loginEvidence)
				plan.EvidenceRefs = append(plan.EvidenceRefs, loginEvidence)
			}
		}
		loginAction.EvidenceRefs = uniqueEvidenceRefs(loginAction.EvidenceRefs)
		plan.Actions = append(plan.Actions, loginAction)
		plan.EvidenceRefs = append(plan.EvidenceRefs, evidence)
	}
	for _, result := range response.Results {
		if result.Status != "verified" {
			continue
		}
		probe := probeFromVerifierResult(result, byID)
		if !verifiedProbeSemanticallyValid(result, probe, intelligence) || !probe.IsBusiness || probe.IsChrome || !selectorUsableForBusinessAction(probe.Selector) ||
			!isURLAllowedByRunScope(intelligence.RunIntentScope, probe.URL) ||
			isControlPlaneSignal(intelligence.RunIntentScope, probe.URL, probe.Label, probe.Selector) {
			continue
		}
		action := verifiedActionFromProbe(probe, firstNonEmpty(response.VerificationMode, "playwright_readonly_scan"), response.BrowserScanID)
		action.URL = firstNonEmpty(result.PageURL, action.URL, project.ProductURL)
		action.VerifiedAt = result.VerifiedAt
		if action.VerifiedAt.IsZero() {
			action.VerifiedAt = time.Now().UTC()
		}
		evidence := model.EvidenceRef{
			ID:         firstNonEmpty(result.EvidenceID, "ev_browser_scan_"+shortHash(response.BrowserScanID+result.ID)),
			Kind:       model.EvidenceKindBrowserScan,
			Summary:    "只读页面预扫描确认 selector 可见可用：" + result.Selector,
			FieldPath:  "verified_interaction_plan.actions." + result.ID,
			Confidence: 0.9,
		}
		action.EvidenceRefs = append(action.EvidenceRefs, evidence)
		action.Alternatives = formalSelectorCandidates(action.Alternatives)
		if candidate, ok := selectorCandidateFromVerifierResult(result, evidence); ok {
			action.Alternatives = uniqueSelectorCandidates(append(action.Alternatives, candidate))
		}
		plan.Actions = append(plan.Actions, action)
		if action.IsBusiness {
			plan.BusinessActionCount++
		}
		plan.EvidenceRefs = append(plan.EvidenceRefs, action.EvidenceRefs...)
	}
	plan.EvidenceRefs = uniqueEvidenceRefs(plan.EvidenceRefs)
	if plan.BusinessActionCount > 0 {
		plan.Confidence = 0.86
	}
	return plan
}

func verifiedProbeSemanticallyValid(result interactionVerifierResult, probe model.InteractionProbe, intelligence *model.ProjectIntelligencePack) bool {
	kind := strings.ToLower(strings.TrimSpace(firstNonEmpty(result.Kind, probe.Kind)))
	selectorText := normalizeIntentText(strings.Join([]string{result.Label, result.Selector, probe.Label, probe.Selector}, " "))
	if kind == "fill" && (!result.Editable || containsAnyNormalized(selectorText, "button", "role button", "has text", "a href")) {
		return false
	}
	if containsAnyNormalized(selectorText, "忘记密码", "找回密码", "重置密码", "forgot password", "reset password", "返回登录", "back to login", "注册", "sign up", "register", "create account") {
		return false
	}
	if probe.IntentGoalID == "" || intelligence == nil || intelligence.DemoIntent == nil {
		return true
	}
	for _, goal := range intelligence.DemoIntent.Goals {
		if goal.ID != probe.IntentGoalID {
			continue
		}
		want := strings.ToLower(strings.TrimSpace(firstNonEmpty(goal.PreferredAction, goal.Kind)))
		if want == "business_action" || want == "auth" || want == "" {
			return true
		}
		return graphActionTypeFromKind(kind, probe.Selector) == model.GraphActionType(want)
	}
	return false
}

func missingEvidenceFromScanResults(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, response interactionVerifierResponse, byID map[string]model.InteractionProbe) *model.MissingEvidenceReport {
	report := &model.MissingEvidenceReport{
		ID:            "missing_evidence_" + project.ID,
		ProjectID:     project.ID,
		SchemaVersion: model.ProjectIntelligencePackSchemaVersion,
		IntentID:      intentID(intelligence),
		CreatedAt:     time.Now().UTC(),
	}
	for _, result := range response.Results {
		if result.Status == "verified" {
			continue
		}
		probe := probeFromVerifierResult(result, byID)
		message := fmt.Sprintf("页面预扫描未验证目标 %q 的 selector：%s（%s）。", firstNonEmpty(probe.Label, result.Label), result.Selector, result.Status)
		if result.Message != "" {
			message += " " + result.Message
		}
		report.Items = append(report.Items, missingEvidenceItem(project, firstNonEmpty(probe.IntentGoalID, result.IntentGoalID), firstNonEmpty(result.Status, "not_verified"), message, "确认目标页面状态、登录态和 selector 是否与需求匹配。"))
	}
	if response.Error != nil {
		report.Items = append(report.Items, missingEvidenceItem(project, "", firstNonEmpty(response.Error.Code, "page_scan_failed"), response.Error.Message, "确认产品 URL 可访问后重试页面预扫描。"))
	}
	if response.Diagnostics != nil && response.Diagnostics.LoginAttempted && response.Diagnostics.LoginStatus != "submitted_navigation_observed" {
		report.Items = append(report.Items, missingEvidenceItem(
			project,
			"intent_login_observe",
			firstNonEmpty(response.Diagnostics.LoginStatus, "login_not_verified"),
			scanDiagnosticSummary(response.Diagnostics),
			"确认公开产品入口是否能进入登录页，以及测试账号是否可完成登录；不得把登录页营销控件作为登录成功证据。",
		))
	}
	verified := 0
	for _, result := range response.Results {
		if result.Status == "verified" {
			verified++
		}
	}
	if verified == 0 && len(report.Items) == 0 {
		report.Items = append(report.Items, missingEvidenceItem(project, "", "selector", "页面预扫描没有发现或验证任何业务动作 selector。", "确认已提供登录凭据、目标页面可达，并为核心业务控件补充 data-testid/role/name。"))
	}
	if verified == 0 {
		if summary := scanDiagnosticSummary(response.Diagnostics); summary != "" {
			report.Items = append(report.Items, missingEvidenceItem(project, "", "page_scan_diagnostic", summary, "确认登录后是否进入工作台、目标业务控件是否在当前页面可见；必要时补充截图标注或稳定 data-testid/role/name。"))
		}
	}
	loginFailed := response.Diagnostics != nil && response.Diagnostics.LoginAttempted && response.Diagnostics.LoginStatus != "submitted_navigation_observed"
	report.Blocking = loginFailed || verified == 0 && len(report.Items) > 0
	if report.Blocking {
		report.Summary = "no verified business action: 页面预扫描没有确认任何业务动作，已阻止生成录制脚本。"
		if summary := scanDiagnosticSummary(response.Diagnostics); summary != "" {
			report.Summary += " " + summary
		}
	} else if len(report.Items) > 0 {
		report.Summary = "部分需求目标缺少页面验证证据。"
	}
	if len(report.Items) == 0 {
		return nil
	}
	return report
}

func verifierGoals(intelligence *model.ProjectIntelligencePack) []interactionVerifierGoal {
	if intelligence == nil || intelligence.DemoIntent == nil {
		return nil
	}
	goals := make([]interactionVerifierGoal, 0, len(intelligence.DemoIntent.Goals))
	for _, goal := range intelligence.DemoIntent.Goals {
		if goal.Forbidden {
			continue
		}
		goals = append(goals, interactionVerifierGoal{
			ID:         goal.ID,
			Label:      goal.Label,
			Kind:       firstNonEmpty(goal.PreferredAction, goal.Kind),
			Keywords:   goal.TargetKeywords,
			Required:   goal.Required,
			Business:   goal.BusinessCritical,
			InputValue: verifierGoalInputValue(goal),
		})
	}
	return goals
}

func verifierGoalInputValue(goal model.DemoIntentGoal) string {
	if goal.ID != "intent_project_requirement_input" || goal.PreferredAction != "fill" || !goal.Required || !goal.BusinessCritical {
		return ""
	}
	const prefix = "填写项目需求："
	value := strings.TrimSpace(strings.TrimPrefix(goal.Label, prefix))
	if value == goal.Label || value == "" || len([]rune(value)) > 512 || containsAnyNormalized(value, "password", "passwd", "secret", "token", "api key", "密码", "口令", "密钥", "令牌") {
		return ""
	}
	return value
}

func probeFromVerifierResult(result interactionVerifierResult, byID map[string]model.InteractionProbe) model.InteractionProbe {
	if probe, ok := byID[result.ID]; ok {
		return probe
	}
	kind := firstNonEmpty(result.Kind, actionKindFromSelector(result.Selector))
	return model.InteractionProbe{
		ID:             firstNonEmpty(result.ID, "probe_browser_scan_"+shortHash(result.Selector+result.Label)),
		IntentGoalID:   result.IntentGoalID,
		Label:          firstNonEmpty(result.ObservedAccessibleName, result.Label, labelFromSelector(result.Selector)),
		Kind:           kind,
		Selector:       result.Selector,
		URL:            result.PageURL,
		RouteRef:       safeID("route", pathFromURL(result.PageURL)),
		Source:         "browser_scan_discovery",
		IsBusiness:     isBusinessAction(graphActionTypeFromKind(kind, result.Selector)),
		IsChrome:       actionLooksLikeChromeControl(result.Label, result.Selector),
		SelectorScore:  selectorQualityScore(result.Selector),
		WaitConditions: []string{"domcontentloaded"},
	}
}

func formalSelectorCandidates(values []model.SelectorCandidate) []model.SelectorCandidate {
	out := make([]model.SelectorCandidate, 0, len(values))
	for _, candidate := range values {
		if model.SelectorCandidateHasFormalProvenance(candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

func selectorCandidateFromVerifierResult(result interactionVerifierResult, evidence model.EvidenceRef) (model.SelectorCandidate, bool) {
	if strings.TrimSpace(result.Selector) == "" {
		return model.SelectorCandidate{}, false
	}
	kind, value := selectorCandidateIdentity(result.Selector)
	observedAt := result.ObservedAt
	if observedAt.IsZero() {
		observedAt = result.VerifiedAt
	}
	candidate := model.SelectorCandidate{
		Kind:                   kind,
		Value:                  value,
		Confidence:             0.9,
		StabilityScore:         float64(selectorQualityScore(result.Selector)) / 100,
		Source:                 firstNonEmpty(result.SourceKind, "page_scan"),
		EvidenceID:             firstNonEmpty(result.EvidenceID, evidence.ID),
		SourceKind:             firstNonEmpty(result.SourceKind, "page_scan"),
		SourceDigest:           result.SourceDigest,
		ObservedRole:           result.ObservedRole,
		ObservedAccessibleName: firstNonEmpty(result.ObservedAccessibleName, result.Label),
		ObservedURL:            firstNonEmpty(result.ObservedURL, result.PageURL),
		ObservedRouteTemplate:  result.ObservedRouteTemplate,
		ObservedPageRole:       result.ObservedPageRole,
		ObservedFormRole:       result.ObservedFormRole,
		EvidenceDigestSHA256:   firstNonEmpty(result.EvidenceDigestSHA256, result.SourceDigest),
		ObservedAt:             &observedAt,
		LastValidatedAt:        result.VerifiedAt,
		EvidenceRefs:           []model.EvidenceRef{evidence},
	}
	return candidate, model.SelectorCandidateHasFormalProvenance(candidate)
}

func selectorCandidateIdentity(value string) (string, string) {
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(lower, "data-testid") {
		if testID := testIDFromSelector(value); testID != "" {
			return "testid", testID
		}
	}
	return "css", value
}

func emptyVerifiedPlan(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, mode string, scanID string, sourceURL string) *model.VerifiedInteractionPlan {
	return &model.VerifiedInteractionPlan{
		ID:               "verified_interactions_" + project.ID,
		ProjectID:        project.ID,
		SchemaVersion:    model.ProjectIntelligencePackSchemaVersion,
		IntentID:         intentID(intelligence),
		VerificationMode: firstNonEmpty(mode, "unverified"),
		BrowserScanID:    scanID,
		SourceURL:        sourceURL,
		Confidence:       0.42,
		CreatedAt:        time.Now().UTC(),
	}
}

func verifiedActionFromProbe(probe model.InteractionProbe, source string, scanID string) model.VerifiedInteractionAction {
	return model.VerifiedInteractionAction{
		ID:                 firstNonEmpty(probe.ID, "verified_"+shortHash(probe.Selector)),
		IntentGoalID:       probe.IntentGoalID,
		Label:              firstNonEmpty(probe.Label, labelFromSelector(probe.Selector)),
		Kind:               firstNonEmpty(probe.Kind, actionKindFromSelector(probe.Selector)),
		Selector:           probe.Selector,
		URL:                probe.URL,
		RouteRef:           probe.RouteRef,
		ComponentRef:       probe.ComponentRef,
		ExpectedOutcome:    firstNonEmpty(probe.Label, "目标业务动作已执行或可见"),
		SuccessState:       "页面出现目标业务状态或后续步骤入口",
		WaitConditions:     probe.WaitConditions,
		DurationHintMS:     0,
		IsBusiness:         probe.IsBusiness && !probe.IsChrome && !looksLikeLoginAction(probe.Label, probe.Selector),
		NonDestructive:     !actionLooksUnsafeOrOffIntent(probe.Label, probe.Selector),
		VerificationStatus: "verified",
		VerificationSource: firstNonEmpty(source, probe.Source),
		VerifiedAt:         time.Now().UTC(),
		SelectorScore:      probe.SelectorScore,
		EvidenceRefs:       probe.EvidenceRefs,
		Alternatives:       probe.Alternatives,
	}
}

func missingEvidence(project *model.ProjectContext, intent *model.DemoIntentSpec, kind string, message string, suggestion string) *model.MissingEvidenceReport {
	report := &model.MissingEvidenceReport{
		ID:            "missing_evidence_" + project.ID,
		ProjectID:     project.ID,
		SchemaVersion: model.ProjectIntelligencePackSchemaVersion,
		IntentID:      "",
		Blocking:      true,
		Summary:       message,
		CreatedAt:     time.Now().UTC(),
	}
	if intent != nil {
		report.IntentID = intent.ID
		for _, goal := range intent.Goals {
			if !goal.BusinessCritical {
				continue
			}
			report.Items = append(report.Items, model.MissingEvidenceItem{
				ID:              "missing_" + shortHash(goal.ID+kind),
				IntentGoalID:    goal.ID,
				IntentLabel:     goal.Label,
				MissingKind:     kind,
				Severity:        "blocking",
				Message:         message,
				SuggestedAction: suggestion,
				FieldPath:       "project_intelligence.demo_intent.goals." + goal.ID,
			})
		}
	}
	if len(report.Items) == 0 {
		report.Items = append(report.Items, model.MissingEvidenceItem{
			ID:              "missing_" + shortHash(kind+message),
			MissingKind:     kind,
			Severity:        "blocking",
			Message:         message,
			SuggestedAction: suggestion,
			FieldPath:       "project_intelligence",
		})
	}
	return report
}

func missingEvidenceItem(project *model.ProjectContext, intentGoalID string, kind string, message string, suggestion string) model.MissingEvidenceItem {
	return model.MissingEvidenceItem{
		ID:              "missing_" + shortHash(project.ID+intentGoalID+kind+message),
		IntentGoalID:    intentGoalID,
		MissingKind:     kind,
		Severity:        "blocking",
		Message:         message,
		SuggestedAction: suggestion,
		FieldPath:       "project_intelligence.verified_interaction_plan",
	}
}

func allowedDomainsFromScope(project *model.ProjectContext, scope *model.RunIntentScope) []string {
	domains := []string{}
	if project != nil && project.AccessPolicy != nil {
		domains = append(domains, project.AccessPolicy.AllowedDomains...)
	}
	if scope != nil {
		for _, origin := range scope.AllowedOrigins {
			domains = append(domains, strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://"))
		}
	}
	return uniqueStrings(domains)
}

func intentID(intelligence *model.ProjectIntelligencePack) string {
	if intelligence != nil && intelligence.DemoIntent != nil {
		return intelligence.DemoIntent.ID
	}
	return ""
}

func responseErrorCode(err *interactionVerifierResponseError) string {
	if err == nil {
		return ""
	}
	return err.Code
}

func responseErrorMessage(err *interactionVerifierResponseError) string {
	if err == nil || strings.TrimSpace(err.Message) == "" {
		return "页面预扫描失败，无法验证业务 selector。"
	}
	return err.Message
}

func scanDiagnosticSummary(diagnostics *interactionVerifierDiagnostics) string {
	if diagnostics == nil {
		return ""
	}
	parts := []string{}
	if diagnostics.LoginAttempted {
		parts = append(parts, "登录状态="+firstNonEmpty(diagnostics.LoginStatus, "unknown"))
	} else {
		parts = append(parts, "未尝试登录")
	}
	if diagnostics.FinalURL != "" {
		parts = append(parts, "最终URL="+redactBridgeLikeURL(diagnostics.FinalURL))
	}
	if diagnostics.PageTitle != "" {
		parts = append(parts, "页面标题="+trimForDiagnostic(diagnostics.PageTitle, 60))
	}
	parts = append(parts, fmt.Sprintf("候选selector=%d", diagnostics.CandidateCount))
	parts = append(parts, fmt.Sprintf("验证通过=%d", diagnostics.VerifiedCandidateCount))
	parts = append(parts, fmt.Sprintf("发现业务控件=%d", diagnostics.DiscoveredBusinessControlCount))
	return "扫描诊断：" + strings.Join(parts, "，") + "。"
}

func redactBridgeLikeURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if index := strings.Index(value, "?"); index >= 0 {
		value = value[:index]
	}
	if index := strings.Index(value, "#"); index >= 0 {
		value = value[:index]
	}
	return trimForDiagnostic(value, 120)
}

func trimForDiagnostic(value string, max int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if max <= 0 || len([]rune(value)) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max]) + "..."
}
