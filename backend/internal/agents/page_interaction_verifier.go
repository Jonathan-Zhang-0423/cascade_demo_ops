package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	candidates := verifierCandidates(intelligence)
	if len(candidates) == 0 {
		adaptivePlan, adaptiveMissing := adaptivePlanFromIntentEvidence(project, intelligence, nil, "intent_runtime_adaptive", nil)
		if adaptivePlan.BusinessActionCount > 0 {
			intelligence.VerifiedInteraction = adaptivePlan
			intelligence.MissingEvidenceReport = adaptiveMissing
			if a.manager == nil || project.ProductURL == "" {
				return adaptivePlan, adaptiveMissing, nil
			}
		}
	}
	if a.manager != nil && project.ProductURL != "" {
		plan, missing, err := a.verifyWithSidecar(ctx, project, intelligence, candidates, credentials)
		if err == nil && plan != nil {
			if plan.BusinessActionCount == 0 {
				adaptivePlan, adaptiveMissing := adaptivePlanFromIntentEvidence(project, intelligence, candidates, firstNonEmpty(plan.BrowserScanID, "browser_scan_unverified"), missing)
				if adaptivePlan.BusinessActionCount > 0 {
					intelligence.VerifiedInteraction = adaptivePlan
					intelligence.MissingEvidenceReport = adaptiveMissing
					return adaptivePlan, adaptiveMissing, nil
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
			report := missingEvidence(project, intelligence.DemoIntent, "page_scan_failed", "页面预扫描 sidecar 调用失败："+err.Error(), "确认 NODE_WORKER_PATH 指向 video-worker/dist/index.js，并确认本机 Playwright/Node 可运行。")
			adaptivePlan, adaptiveMissing := adaptivePlanFromIntentEvidence(project, intelligence, candidates, "sidecar_unavailable_runtime_adaptive", report)
			if adaptivePlan.BusinessActionCount > 0 {
				intelligence.VerifiedInteraction = adaptivePlan
				intelligence.MissingEvidenceReport = adaptiveMissing
				return adaptivePlan, adaptiveMissing, nil
			}
			intelligence.VerifiedInteraction = fallbackPlan
			report = nonBlockingMissingEvidenceReport(report)
			intelligence.MissingEvidenceReport = report
			return fallbackPlan, report, nil
		}
	}
	plan, missing := verifyFromPageEvidence(project, intelligence, candidates)
	if plan != nil && plan.BusinessActionCount == 0 {
		adaptivePlan, adaptiveMissing := adaptivePlanFromIntentEvidence(project, intelligence, candidates, "", missing)
		if adaptivePlan.BusinessActionCount > 0 {
			plan = adaptivePlan
			missing = adaptiveMissing
		}
	}
	intelligence.VerifiedInteraction = plan
	missing = nonBlockingMissingEvidenceReport(missing)
	intelligence.MissingEvidenceReport = missing
	_ = brief
	_ = report
	_ = productMap
	return plan, missing, nil
}

func adaptivePlanFromIntentEvidence(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, candidates []model.InteractionProbe, scanID string, previous *model.MissingEvidenceReport) (*model.VerifiedInteractionPlan, *model.MissingEvidenceReport) {
	plan := emptyVerifiedPlan(project, intelligence, "runtime_adaptive_discovery", scanID, project.ProductURL)
	plan.Confidence = 0.62
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if !candidate.IsBusiness || candidate.IsChrome {
			continue
		}
		action := adaptiveActionFromProbe(project, candidate, scanID)
		key := normalizeSelector(action.Selector + "|" + action.Label)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		plan.Actions = append(plan.Actions, action)
		plan.BusinessActionCount++
		plan.EvidenceRefs = append(plan.EvidenceRefs, action.EvidenceRefs...)
		if len(plan.Actions) >= 4 {
			break
		}
	}
	if plan.BusinessActionCount == 0 && intelligence != nil && intelligence.DemoIntent != nil {
		for _, goal := range intelligence.DemoIntent.Goals {
			if !goal.BusinessCritical || goal.Forbidden {
				continue
			}
			action := adaptiveActionFromGoal(project, goal, scanID)
			key := normalizeSelector(action.Selector + "|" + action.Label)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			plan.Actions = append(plan.Actions, action)
			plan.BusinessActionCount++
			plan.EvidenceRefs = append(plan.EvidenceRefs, action.EvidenceRefs...)
			if len(plan.Actions) >= 3 {
				break
			}
		}
	}
	plan.EvidenceRefs = uniqueEvidenceRefs(plan.EvidenceRefs)
	if plan.BusinessActionCount == 0 {
		return plan, previous
	}
	missing := nonBlockingAdaptiveEvidenceReport(project, intelligence, previous)
	return plan, missing
}

func adaptiveActionFromProbe(project *model.ProjectContext, probe model.InteractionProbe, scanID string) model.VerifiedInteractionAction {
	label := firstNonEmpty(probe.Label, labelFromSelector(probe.Selector), "业务动作")
	selector := firstNonEmpty(probe.Selector, adaptiveSelectorForLabel(label))
	alternatives := adaptiveSelectorAlternatives(label, probe.Alternatives, selector)
	action := verifiedActionFromProbe(probe, "runtime_adaptive_discovery", scanID)
	action.Label = label
	action.Selector = selector
	action.URL = firstNonEmpty(action.URL, project.ProductURL)
	action.Alternatives = alternatives
	action.DurationHintMS = maxInt(action.DurationHintMS, 12000)
	action.IsBusiness = true
	action.VerificationStatus = "runtime_adaptive"
	action.VerificationSource = "code_intent_runtime_discovery"
	action.SuccessState = "运行时自适应找到目标控件并完成业务动作"
	action.ExpectedOutcome = firstNonEmpty(probe.Label, "完成需求目标："+label)
	action.SelectorScore = maxInt(action.SelectorScore, selectorQualityScore(selector))
	action.EvidenceRefs = uniqueEvidenceRefs(append(action.EvidenceRefs, model.EvidenceRef{
		ID:         "ev_runtime_adaptive_" + shortHash(project.ID+label+selector),
		Kind:       model.EvidenceKindCodeSnapshot,
		Summary:    "页面预扫描未确认控件；运行时将基于需求目标和代码 selector 证据自适应寻找：" + label,
		FieldPath:  "verified_interaction_plan.actions." + firstNonEmpty(probe.ID, safeID("adaptive", label)),
		Confidence: 0.62,
	}))
	return action
}

func adaptiveActionFromGoal(project *model.ProjectContext, goal model.DemoIntentGoal, scanID string) model.VerifiedInteractionAction {
	label := conciseAdaptiveGoalLabel(goal)
	selector := adaptiveSelectorForTerms(label, goal.TargetKeywords)
	kind := firstNonEmpty(goal.PreferredAction, goal.Kind, "click")
	actionKind := graphActionTypeFromKind(kind, selector)
	if !isBusinessAction(actionKind) {
		actionKind = model.GraphActionClick
	}
	return model.VerifiedInteractionAction{
		ID:                 safeID("adaptive_goal", firstNonEmpty(goal.ID, label)),
		IntentGoalID:       goal.ID,
		Label:              label,
		Kind:               string(actionKind),
		Selector:           selector,
		URL:                firstNonEmpty(goal.TargetPageHint, project.ProductURL),
		ExpectedOutcome:    firstNonEmpty(goal.SuccessState, "完成需求目标："+label),
		SuccessState:       firstNonEmpty(goal.SuccessState, "页面出现目标业务状态或后续步骤入口"),
		WaitConditions:     []string{"domcontentloaded", "networkidle"},
		DurationHintMS:     12000,
		IsBusiness:         true,
		VerificationStatus: "runtime_adaptive",
		VerificationSource: "intent_runtime_discovery",
		SelectorScore:      selectorQualityScore(selector),
		Alternatives:       adaptiveSelectorAlternativesFromTerms(label, goal.TargetKeywords, nil, selector),
		EvidenceRefs: uniqueEvidenceRefs(append(goal.EvidenceRefs, model.EvidenceRef{
			ID:         "ev_runtime_adaptive_goal_" + shortHash(project.ID+goal.ID+label),
			Kind:       model.EvidenceKindRequirementDoc,
			Summary:    "根据用户需求目标生成运行时自适应业务动作：" + label,
			FieldPath:  "project_intelligence.demo_intent.goals." + goal.ID,
			Confidence: 0.56,
		})),
		VerifiedAt: time.Now().UTC(),
	}
}

func adaptiveSelectorForLabel(label string) string {
	return adaptiveSelectorForTerms(label, nil)
}

func adaptiveSelectorForTerms(label string, terms []string) string {
	for _, term := range adaptiveSearchTerms(label, terms) {
		if term == "" {
			continue
		}
		return `button:has-text("` + escapeSelectorText(term) + `")`
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	return `button:has-text("` + escapeSelectorText(label) + `")`
}

func adaptiveSelectorAlternatives(label string, existing []model.SelectorCandidate, primary string) []model.SelectorCandidate {
	return adaptiveSelectorAlternativesFromTerms(label, nil, existing, primary)
}

func adaptiveSelectorAlternativesFromTerms(label string, terms []string, existing []model.SelectorCandidate, primary string) []model.SelectorCandidate {
	out := append([]model.SelectorCandidate{}, existing...)
	add := func(kind string, value string, confidence float64) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, candidate := range out {
			if normalizeSelector(candidate.Value) == normalizeSelector(value) {
				return
			}
		}
		out = append(out, model.SelectorCandidate{Kind: kind, Value: value, Confidence: confidence, StabilityScore: 0.5, Source: "runtime_adaptive"})
	}
	add("css", primary, 0.72)
	for _, term := range adaptiveSearchTerms(label, terms) {
		escaped := escapeSelectorText(term)
		add("css", `button:has-text("`+escaped+`")`, 0.7)
		add("css", `a:has-text("`+escaped+`")`, 0.68)
		add("css", `[role="button"]:has-text("`+escaped+`")`, 0.66)
		add("css", `[role="link"]:has-text("`+escaped+`")`, 0.62)
		add("css", `[data-testid*="`+escaped+`"]`, 0.61)
		add("css", `[data-test*="`+escaped+`"]`, 0.61)
		add("css", `[data-cy*="`+escaped+`"]`, 0.61)
		add("css", `[aria-label*="`+escaped+`"]`, 0.62)
		add("css", `[placeholder*="`+escaped+`"]`, 0.56)
		add("text", `text=`+term, 0.58)
	}
	return out
}

func conciseAdaptiveGoalLabel(goal model.DemoIntentGoal) string {
	for _, term := range adaptiveSearchTerms(goal.Label, goal.TargetKeywords) {
		if term != "" {
			return term
		}
	}
	return firstNonEmpty(goal.Label, strings.Join(goal.TargetKeywords, " "), "核心业务动作")
}

func adaptiveSearchTerms(label string, terms []string) []string {
	candidates := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if len([]rune(value)) > 28 {
			return
		}
		candidates = append(candidates, value)
	}
	for _, term := range terms {
		add(term)
	}
	for _, term := range intentKeywordsForText(label) {
		add(term)
	}
	lower := strings.ToLower(label + " " + strings.Join(terms, " "))
	if containsAny(lower, "项目", "project") {
		add("新建项目")
		add("创建项目")
		add("New project")
		add("Create project")
		add("Project")
	}
	if containsAny(lower, "生成", "generate", "内容", "视频", "脚本", "演示") {
		add("生成")
		add("开始生成")
		add("Generate")
		add("Start")
	}
	if containsAny(lower, "工作台", "dashboard", "console") {
		add("工作台")
		add("Dashboard")
		add("Console")
	}
	if containsAny(lower, "上传", "upload", "导入", "import") {
		add("上传")
		add("Upload")
		add("Import")
	}
	if containsAny(lower, "搜索", "search", "筛选", "filter") {
		add("搜索")
		add("Search")
		add("Filter")
	}
	if len(candidates) == 0 {
		add(label)
	}
	return limitStrings(uniqueStrings(candidates), 10)
}

func nonBlockingAdaptiveEvidenceReport(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, previous *model.MissingEvidenceReport) *model.MissingEvidenceReport {
	report := &model.MissingEvidenceReport{
		ID:            "adaptive_evidence_" + project.ID,
		ProjectID:     project.ID,
		SchemaVersion: model.ProjectIntelligencePackSchemaVersion,
		IntentID:      intentID(intelligence),
		Blocking:      false,
		Summary:       "页面预扫描未确认业务控件，已降级为运行时自适应发现；脚本会按需求目标、代码 selector 证据和文本候选在执行时定位控件。",
		CreatedAt:     time.Now().UTC(),
	}
	if previous != nil && previous.Summary != "" {
		report.Items = append(report.Items, model.MissingEvidenceItem{
			ID:              "adaptive_previous_" + shortHash(previous.Summary),
			MissingKind:     "page_scan_unverified",
			Severity:        "warning",
			Message:         previous.Summary,
			SuggestedAction: "继续允许运行时自适应执行；若运行时失败，使用服务器 failure_diagnostic 修复 selector。",
			FieldPath:       "project_intelligence.verified_interaction_plan",
		})
	}
	report.Items = append(report.Items, model.MissingEvidenceItem{
		ID:              "adaptive_runtime_discovery_" + project.ID,
		MissingKind:     "runtime_adaptive",
		Severity:        "warning",
		Message:         "未把预扫描失败当作上传前阻断；脚本将执行需求驱动的运行时控件发现。",
		SuggestedAction: "审批时重点检查 adaptive selector 候选和目标文案，运行失败后进入修复闭环。",
		FieldPath:       "project_intelligence.verified_interaction_plan",
	})
	return report
}

func nonBlockingMissingEvidenceReport(report *model.MissingEvidenceReport) *model.MissingEvidenceReport {
	if report == nil {
		return nil
	}
	report.Blocking = false
	if report.Summary != "" && !strings.Contains(report.Summary, "运行时自适应") {
		report.Summary += " 已改为运行时自适应发现，不阻塞执行包生成。"
	}
	for i := range report.Items {
		if report.Items[i].Severity == "blocking" {
			report.Items[i].Severity = "warning"
		}
		if report.Items[i].SuggestedAction != "" && !strings.Contains(report.Items[i].SuggestedAction, "运行时自适应") {
			report.Items[i].SuggestedAction += "；当前会继续运行时自适应发现，失败后使用诊断修复。"
		}
	}
	return report
}

func escapeSelectorText(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
}

type interactionVerifierRequest struct {
	ProductURL     string                    `json:"product_url,omitempty"`
	TimeoutMS      int                       `json:"timeout_ms,omitempty"`
	Headless       bool                      `json:"headless"`
	AllowedDomains []string                  `json:"allowed_domains,omitempty"`
	Candidates     []interactionVerifierItem `json:"candidates,omitempty"`
	IntentGoals    []interactionVerifierGoal `json:"intent_goals,omitempty"`
	DemoUsername   string                    `json:"demo_username,omitempty"`
	DemoPassword   string                    `json:"demo_password,omitempty"`
}

type interactionVerifierGoal struct {
	ID       string   `json:"id,omitempty"`
	Label    string   `json:"label,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	Required bool     `json:"required,omitempty"`
	Business bool     `json:"business,omitempty"`
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
	LoginAttempted                 bool   `json:"login_attempted,omitempty"`
	LoginStatus                    string `json:"login_status,omitempty"`
	FinalURL                       string `json:"final_url,omitempty"`
	PageTitle                      string `json:"page_title,omitempty"`
	CandidateCount                 int    `json:"candidate_count,omitempty"`
	VerifiedCandidateCount         int    `json:"verified_candidate_count,omitempty"`
	DiscoveredBusinessControlCount int    `json:"discovered_business_control_count,omitempty"`
}

type interactionVerifierResult struct {
	interactionVerifierItem
	Status     string    `json:"status,omitempty"`
	Visible    bool      `json:"visible,omitempty"`
	Enabled    bool      `json:"enabled,omitempty"`
	Editable   bool      `json:"editable,omitempty"`
	PageURL    string    `json:"page_url,omitempty"`
	PageTitle  string    `json:"page_title,omitempty"`
	Message    string    `json:"message,omitempty"`
	VerifiedAt time.Time `json:"verified_at,omitempty"`
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
		ProductURL:     project.ProductURL,
		TimeoutMS:      20000,
		Headless:       true,
		AllowedDomains: allowedDomainsFromProject(project),
		Candidates:     items,
		IntentGoals:    verifierGoals(intelligence),
		DemoUsername:   credentials.DemoUsername,
		DemoPassword:   credentials.DemoPassword,
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
			missing.Items = append(missing.Items, missingEvidenceItem(project, "", "selector", "没有页面验证过的业务动作 selector，将继续运行时自适应发现。", "补充真实页面预扫描、截图标注或稳定业务 selector 可提高命中率。"))
		}
		missing.Blocking = true
		missing.Summary = "没有页面验证过的业务动作，将继续运行时自适应发现。"
	} else if len(missing.Items) > 0 {
		missing.Summary = "部分候选动作缺少页面预扫描，已只保留页面材料验证过的动作。"
	}
	if len(missing.Items) > 0 {
		missing.Blocking = missing.Blocking || plan.BusinessActionCount == 0
		return plan, missing
	}
	return plan, nil
}

func verifiedPlanFromScanResults(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, response interactionVerifierResponse, byID map[string]model.InteractionProbe) *model.VerifiedInteractionPlan {
	plan := emptyVerifiedPlan(project, intelligence, firstNonEmpty(response.VerificationMode, "playwright_readonly_scan"), response.BrowserScanID, firstNonEmpty(response.SourceURL, project.ProductURL))
	for _, result := range response.Results {
		if result.Status != "verified" {
			continue
		}
		probe := probeFromVerifierResult(result, byID)
		if !probe.IsBusiness || probe.IsChrome || !selectorUsableForBusinessAction(probe.Selector) {
			continue
		}
		action := verifiedActionFromProbe(probe, firstNonEmpty(response.VerificationMode, "playwright_readonly_scan"), response.BrowserScanID)
		action.URL = firstNonEmpty(result.PageURL, action.URL, project.ProductURL)
		action.VerifiedAt = result.VerifiedAt
		if action.VerifiedAt.IsZero() {
			action.VerifiedAt = time.Now().UTC()
		}
		action.EvidenceRefs = append(action.EvidenceRefs, model.EvidenceRef{
			ID:         "ev_browser_scan_" + shortHash(response.BrowserScanID+result.ID),
			Kind:       model.EvidenceKindBrowserScan,
			Summary:    "只读页面预扫描确认 selector 可见可用：" + result.Selector,
			FieldPath:  "verified_interaction_plan.actions." + result.ID,
			Confidence: 0.9,
		})
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
	report.Blocking = verified == 0 && len(report.Items) > 0
	if report.Blocking {
		report.Summary = "页面预扫描没有确认任何业务动作，将继续运行时自适应发现。"
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
			ID:       goal.ID,
			Label:    goal.Label,
			Kind:     goal.Kind,
			Keywords: goal.TargetKeywords,
			Required: goal.Required,
			Business: goal.BusinessCritical,
		})
	}
	return goals
}

func probeFromVerifierResult(result interactionVerifierResult, byID map[string]model.InteractionProbe) model.InteractionProbe {
	if probe, ok := byID[result.ID]; ok {
		return probe
	}
	kind := firstNonEmpty(result.Kind, actionKindFromSelector(result.Selector))
	return model.InteractionProbe{
		ID:             firstNonEmpty(result.ID, "probe_browser_scan_"+shortHash(result.Selector+result.Label)),
		IntentGoalID:   result.IntentGoalID,
		Label:          firstNonEmpty(result.Label, labelFromSelector(result.Selector)),
		Kind:           kind,
		Selector:       result.Selector,
		URL:            result.PageURL,
		RouteRef:       safeID("route", pathFromURL(result.PageURL)),
		Source:         "browser_scan_discovery",
		IsBusiness:     isBusinessAction(graphActionTypeFromKind(kind, result.Selector)),
		IsChrome:       actionLooksLikeChromeControl(result.Label, result.Selector),
		SelectorScore:  selectorQualityScore(result.Selector),
		WaitConditions: []string{"domcontentloaded", "networkidle"},
	}
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
	duration := 12000
	if looksLikeLoginAction(probe.Label, probe.Selector) {
		duration = 10000
	}
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
		DurationHintMS:     duration,
		IsBusiness:         probe.IsBusiness && !probe.IsChrome && !looksLikeLoginAction(probe.Label, probe.Selector),
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

func allowedDomainsFromProject(project *model.ProjectContext) []string {
	if project == nil || project.AccessPolicy == nil {
		return nil
	}
	return append([]string{}, project.AccessPolicy.AllowedDomains...)
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
