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
	if intelligence.RunIntentScope == nil {
		intelligence.RunIntentScope = runIntentScopeForProject(project)
	}
	candidates := verifierCandidates(intelligence)
	if a.manager != nil && project.ProductURL != "" {
		plan, missing, err := a.verifyWithSidecar(ctx, project, intelligence, candidates, credentials)
		if err == nil && plan != nil {
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
			intelligence.VerifiedInteraction = fallbackPlan
			intelligence.MissingEvidenceReport = report
			return fallbackPlan, report, nil
		}
	}
	plan, missing := verifyFromPageEvidence(project, intelligence, candidates)
	intelligence.VerifiedInteraction = plan
	intelligence.MissingEvidenceReport = missing
	_ = brief
	_ = report
	_ = productMap
	return plan, missing, nil
}

type interactionVerifierRequest struct {
	ProductURL     string                    `json:"product_url,omitempty"`
	TimeoutMS      int                       `json:"timeout_ms,omitempty"`
	Headless       bool                      `json:"headless"`
	AllowedDomains []string                  `json:"allowed_domains,omitempty"`
	ForbiddenPaths []string                  `json:"forbidden_path_prefixes,omitempty"`
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
		ProductURL:     project.ProductURL,
		TimeoutMS:      20000,
		Headless:       true,
		AllowedDomains: allowedDomainsFromScope(project, intelligence.RunIntentScope),
		ForbiddenPaths: intelligence.RunIntentScope.ForbiddenPathPrefixes,
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
		if !probe.IsBusiness || probe.IsChrome || !selectorUsableForBusinessAction(probe.Selector) ||
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
