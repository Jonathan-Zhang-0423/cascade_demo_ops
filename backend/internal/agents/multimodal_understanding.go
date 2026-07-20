package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

type MultimodalUnderstandingAgent struct {
	llm llm.Client
}

func NewMultimodalUnderstandingAgent() *MultimodalUnderstandingAgent {
	return &MultimodalUnderstandingAgent{}
}

func NewMultimodalUnderstandingAgentWithLLM(client llm.Client) *MultimodalUnderstandingAgent {
	return &MultimodalUnderstandingAgent{llm: client}
}

func (a *MultimodalUnderstandingAgent) BuildUnderstanding(
	ctx context.Context,
	project *model.ProjectContext,
	brief *model.RequirementBrief,
	codeSnapshots []model.CodeUnderstandingSnapshot,
	pageSnapshots []model.PageUnderstandingSnapshot,
	intelligence *model.ProjectIntelligencePack,
) (*model.MultimodalUnderstandingReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	report := &model.MultimodalUnderstandingReport{
		ID:                 "understanding_" + project.ID,
		ProjectID:          project.ID,
		SchemaVersion:      model.MultimodalUnderstandingReportSchemaVersion,
		RequirementBrief:   brief,
		CodeSnapshots:      codeSnapshots,
		PageSnapshots:      pageSnapshots,
		InputFingerprints:  inputFingerprints(project, codeSnapshots, pageSnapshots),
		SourceDigestSHA256: combinedSourceDigest(codeSnapshots),
		Summary:            understandingSummary(brief, codeSnapshots, pageSnapshots, intelligence),
		FeatureHypotheses:  featureHypotheses(project, brief, codeSnapshots, pageSnapshots, intelligence),
		WorkflowCandidates: workflowCandidates(project, brief, pageSnapshots, intelligence),
		EvidenceRefs:       combinedEvidenceRefs(brief, codeSnapshots, pageSnapshots, intelligence),
		SafetyReport:       safetyReportFromUnderstanding(project, codeSnapshots, pageSnapshots, intelligence),
		Confidence:         understandingConfidence(codeSnapshots, pageSnapshots, intelligence),
		CreatedAt:          now,
	}
	trace, err := a.enhanceReportWithLLM(ctx, project, brief, report)
	if err != nil && !llm.IsDeterministicFallback(err) {
		if report.SafetyReport != nil {
			report.SafetyReport.Notes = uniqueStrings(append(report.SafetyReport.Notes, "模型融合理解增强失败，已使用本地确定性理解报告继续。"))
		}
		err = nil
	}
	if trace != nil {
		report.EvidenceRefs = append(report.EvidenceRefs, model.EvidenceRef{
			ID:         "ev_model_understanding_" + shortHash(trace.Label()),
			Kind:       model.EvidenceKindBrowserScan,
			Summary:    "MultimodalUnderstandingAgent 模型路由：" + trace.Label(),
			FieldPath:  "model_trace.multimodal_understanding",
			Confidence: 0.7,
		})
	}
	project.KnowledgeRefs = append(project.KnowledgeRefs, report.EvidenceRefs...)
	return report, nil
}

type multimodalLLMOutput struct {
	Summary     string                  `json:"summary"`
	Features    []multimodalLLMFeature  `json:"features"`
	Workflows   []multimodalLLMWorkflow `json:"workflows"`
	SafetyNotes flexibleStringSlice     `json:"safety_notes"`
	Confidence  float64                 `json:"confidence"`
}

type multimodalLLMFeature struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Kind          string               `json:"kind"`
	UserValue     string               `json:"user_value"`
	BusinessValue string               `json:"business_value"`
	Priority      string               `json:"priority"`
	KeyActions    flexibleStringSlice  `json:"key_actions"`
	BestUseCases  flexibleDemoUseCases `json:"best_use_cases"`
	Risks         flexibleStringSlice  `json:"risks"`
}

type multimodalLLMWorkflow struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	UseCase        flexibleDemoUseCase `json:"use_case"`
	EstimatedSteps int                 `json:"estimated_steps"`
	ValueScore     float64             `json:"value_score"`
	Feasibility    float64             `json:"feasibility"`
	RiskNotes      flexibleStringSlice `json:"risk_notes"`
}

func (o *multimodalLLMOutput) UnmarshalJSON(data []byte) error {
	type object multimodalLLMOutput
	var obj object
	if err := json.Unmarshal(data, &obj); err == nil {
		*o = multimodalLLMOutput(obj)
		return nil
	}
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	for index, item := range items {
		if o.Summary == "" {
			o.Summary = firstNonEmpty(stringMapValue(item, "summary"), stringMapValue(item, "description"))
		}
		if confidence := floatMapValue(item, "confidence"); confidence > 0 {
			o.Confidence = confidence
		}
		if safety := stringMapValue(item, "safety_note", "safety", "risk"); safety != "" {
			o.SafetyNotes = append(o.SafetyNotes, safety)
		}
		if looksLikeWorkflowItem(item) {
			o.Workflows = append(o.Workflows, multimodalWorkflowFromMap(item, index))
			continue
		}
		o.Features = append(o.Features, multimodalFeatureFromMap(item, index))
	}
	o.SafetyNotes = flexibleStringSlice(uniqueStrings([]string(o.SafetyNotes)))
	return nil
}

func stringMapValue(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringFromLLMValue(item[key]); value != "" {
			return value
		}
	}
	return ""
}

func floatMapValue(item map[string]any, keys ...string) float64 {
	for _, key := range keys {
		switch typed := item[key].(type) {
		case float64:
			return typed
		case int:
			return float64(typed)
		case json.Number:
			value, _ := typed.Float64()
			return value
		}
	}
	return 0
}

func intMapValue(item map[string]any, keys ...string) int {
	for _, key := range keys {
		switch typed := item[key].(type) {
		case float64:
			return int(typed)
		case int:
			return typed
		case json.Number:
			value, _ := typed.Int64()
			return int(value)
		}
	}
	return 0
}

func looksLikeWorkflowItem(item map[string]any) bool {
	for _, key := range []string{"estimated_steps", "value_score", "feasibility", "risk_notes", "workflow", "workflow_name"} {
		if _, ok := item[key]; ok {
			return true
		}
	}
	if kind := strings.ToLower(stringMapValue(item, "kind", "type")); strings.Contains(kind, "workflow") || strings.Contains(kind, "flow") || strings.Contains(kind, "path") {
		return true
	}
	return false
}

func multimodalFeatureFromMap(item map[string]any, index int) multimodalLLMFeature {
	name := firstNonEmpty(stringMapValue(item, "name", "title", "feature", "capability"), fmt.Sprintf("模型识别能力 %d", index+1))
	return multimodalLLMFeature{
		ID:            firstNonEmpty(stringMapValue(item, "id"), "feature_llm_"+shortHash(name)),
		Name:          name,
		Kind:          firstNonEmpty(stringMapValue(item, "kind", "type"), "supporting"),
		UserValue:     stringMapValue(item, "user_value", "value", "description", "summary"),
		BusinessValue: stringMapValue(item, "business_value", "business", "outcome"),
		Priority:      firstNonEmpty(stringMapValue(item, "priority"), "supporting"),
		KeyActions:    flexibleStringSlice(splitLLMStringList(stringMapValue(item, "key_actions", "actions"))),
		BestUseCases:  flexibleDemoUseCases{demoUseCaseFromLLMValue(item["use_case"])},
		Risks:         flexibleStringSlice(splitLLMStringList(stringMapValue(item, "risks", "risk"))),
	}
}

func multimodalWorkflowFromMap(item map[string]any, index int) multimodalLLMWorkflow {
	name := firstNonEmpty(stringMapValue(item, "name", "title", "workflow", "workflow_name"), fmt.Sprintf("模型建议流程 %d", index+1))
	return multimodalLLMWorkflow{
		ID:             firstNonEmpty(stringMapValue(item, "id"), "workflow_llm_"+shortHash(name)),
		Name:           name,
		UseCase:        flexibleDemoUseCase(demoUseCaseFromLLMValue(item)),
		EstimatedSteps: intMapValue(item, "estimated_steps", "steps"),
		ValueScore:     floatMapValue(item, "value_score", "score"),
		Feasibility:    floatMapValue(item, "feasibility"),
		RiskNotes:      flexibleStringSlice(splitLLMStringList(stringMapValue(item, "risk_notes", "risks", "risk"))),
	}
}

func (a *MultimodalUnderstandingAgent) enhanceReportWithLLM(ctx context.Context, project *model.ProjectContext, brief *model.RequirementBrief, report *model.MultimodalUnderstandingReport) (*llm.CallTrace, error) {
	if a.llm == nil || project == nil || report == nil {
		return nil, nil
	}
	payload := map[string]any{
		"brief":                brief,
		"code_summary":         compactCodeSnapshots(report.CodeSnapshots),
		"page_summary":         compactPageSnapshots(report.PageSnapshots),
		"project_intelligence": compactProjectIntelligence(report.ProjectID, project.ProjectIntelligence),
		"safety_policy":        project.SecurityPolicy,
		"target_audience":      project.TargetAudience,
	}
	data, _ := json.Marshal(payload)
	var output multimodalLLMOutput
	request := llm.JSONRequest{
		System:       "你是 Cascade DemoOps 的多模态理解 agent。请融合需求、代码结构摘要、页面/截图摘要，面向中国客户生成产品价值、功能优先级和演示工作流候选。不要输出源码、密钥或未脱敏客户数据。",
		User:         string(data),
		SchemaName:   "MultimodalUnderstandingPatch",
		ResponseHint: "返回字段：summary, features[], workflows[], safety_notes, confidence。",
		MaxTokens:    1800,
		Temperature:  0.2,
	}
	var trace *llm.CallTrace
	var err error
	images := imageInputsFromPages(report.PageSnapshots)
	if len(images) > 0 {
		trace, err = a.llm.GenerateMultimodal(ctx, config.ModelTaskMultimodalUnderstanding, llm.MultimodalRequest{
			System:      request.System,
			User:        request.User,
			SchemaName:  request.SchemaName,
			MaxTokens:   request.MaxTokens,
			Temperature: request.Temperature,
			Images:      images,
		}, &output)
	} else {
		trace, err = a.llm.GenerateJSON(ctx, config.ModelTaskPlanning, request, &output)
	}
	if err != nil {
		return trace, err
	}
	if output.Summary != "" {
		report.Summary = output.Summary
	}
	if len(output.Features) > 0 {
		report.FeatureHypotheses = append([]*model.Feature{}, report.FeatureHypotheses...)
		for _, feature := range output.Features {
			name := firstNonEmpty(feature.Name, "模型识别产品能力")
			report.FeatureHypotheses = append(report.FeatureHypotheses, &model.Feature{
				ID:            firstNonEmpty(feature.ID, "feature_llm_"+shortHash(name)),
				Name:          name,
				Kind:          firstNonEmpty(feature.Kind, "supporting"),
				UserValue:     feature.UserValue,
				BusinessValue: feature.BusinessValue,
				Priority:      firstNonEmpty(feature.Priority, "supporting"),
				BestAudience:  []string{project.TargetAudience},
				BestUseCases:  []model.DemoUseCase(feature.BestUseCases),
				KeyActions:    stringSlice(feature.KeyActions),
				Risks:         stringSlice(feature.Risks),
				EvidenceRefs:  report.EvidenceRefs,
			})
		}
	}
	if len(output.Workflows) > 0 {
		report.WorkflowCandidates = append([]*model.WorkflowCandidate{}, report.WorkflowCandidates...)
		for _, workflow := range output.Workflows {
			name := firstNonEmpty(workflow.Name, "模型建议演示流程")
			report.WorkflowCandidates = append(report.WorkflowCandidates, &model.WorkflowCandidate{
				ID:             firstNonEmpty(workflow.ID, "workflow_llm_"+shortHash(name)),
				Name:           name,
				UseCase:        demoUseCase(workflow.UseCase),
				AudienceID:     "audience_primary",
				FeatureRefs:    []string{"feature_primary_value"},
				EstimatedSteps: workflow.EstimatedSteps,
				ValueScore:     workflow.ValueScore,
				Feasibility:    workflow.Feasibility,
				RiskNotes:      stringSlice(workflow.RiskNotes),
				EvidenceRefs:   report.EvidenceRefs,
			})
		}
	}
	if report.SafetyReport != nil {
		report.SafetyReport.Notes = uniqueStrings(append(report.SafetyReport.Notes, stringSlice(output.SafetyNotes)...))
	}
	if output.Confidence > 0 {
		report.Confidence = output.Confidence
	}
	return trace, nil
}

func inputFingerprints(project *model.ProjectContext, code []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot) map[string]string {
	fingerprints := map[string]string{}
	if project.ProductURL != "" {
		fingerprints["product_url"] = hashString(project.ProductURL)
	}
	if project.ProductDescription != "" {
		fingerprints["product_description"] = hashString(project.ProductDescription)
	}
	for _, snapshot := range code {
		if snapshot.SourceDigestSHA256 != "" {
			fingerprints["code:"+snapshot.ID] = snapshot.SourceDigestSHA256
		}
	}
	for _, page := range pages {
		digestSource := page.URL + page.Title + page.VisionSummary + page.OCRText
		if page.ScreenshotRef != nil && page.ScreenshotRef.SHA256 != "" {
			digestSource += page.ScreenshotRef.SHA256
		}
		fingerprints["page:"+page.ID] = hashString(digestSource)
	}
	return fingerprints
}

func combinedSourceDigest(code []model.CodeUnderstandingSnapshot) string {
	parts := []string{}
	for _, snapshot := range code {
		if snapshot.SourceDigestSHA256 != "" {
			parts = append(parts, snapshot.SourceDigestSHA256)
		}
	}
	return hashString(strings.Join(uniqueStrings(parts), "|"))
}

func understandingSummary(brief *model.RequirementBrief, code []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot, intelligence *model.ProjectIntelligencePack) string {
	objective := "产品演示脚本"
	if brief != nil && brief.Objective != "" {
		objective = brief.Objective
	}
	if intelligence != nil && intelligence.Architecture != nil && intelligence.Architecture.Summary != "" {
		return "已融合需求、代码结构摘要、页面/截图证据和项目理解图谱，架构摘要：" + intelligence.Architecture.Summary + "；目标是：" + objective
	}
	return "已融合需求、代码结构摘要和页面/截图证据，目标是：" + objective
}

func featureHypotheses(project *model.ProjectContext, brief *model.RequirementBrief, code []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot, intelligence *model.ProjectIntelligencePack) []*model.Feature {
	features := []*model.Feature{}
	if intelligence != nil {
		for _, capability := range intelligence.FeatureCapabilities {
			features = append(features, &model.Feature{
				ID:              firstNonEmpty(capability.ID, "feature_capability_"+shortHash(capability.Name)),
				Name:            firstNonEmpty(capability.Name, "项目理解功能能力"),
				Kind:            firstNonEmpty(capability.Kind, "supporting"),
				UserValue:       firstNonEmpty(capability.UserValue, capability.BusinessValue, "该功能能力可用于构造演示路径。"),
				BusinessValue:   capability.BusinessValue,
				Priority:        firstNonEmpty(capability.Priority, "supporting"),
				BestAudience:    []string{project.TargetAudience},
				BestUseCases:    useCasesFromBrief(brief),
				SupportingPages: append([]string{}, capability.SupportingPageRefs...),
				KeyActions:      append([]string{}, capability.KeyActions...),
				Risks:           append([]string{}, capability.Risks...),
				EvidenceRefs:    capability.EvidenceRefs,
			})
			if len(features) >= 8 {
				return features
			}
		}
	}
	if brief != nil {
		features = append(features, &model.Feature{
			ID:            "feature_primary_value",
			Name:          firstNonEmpty(brief.Scenario, "核心产品价值"),
			Kind:          "hero",
			UserValue:     brief.Objective,
			BusinessValue: brief.PrimaryOutcome,
			Priority:      "hero",
			BestAudience:  []string{project.TargetAudience},
			BestUseCases:  brief.UseCases,
			KeyActions:    brief.MustShow,
			Risks:         append(append([]string{}, brief.ForbiddenPages...), brief.ForbiddenData...),
			EvidenceRefs:  brief.EvidenceRefs,
		})
	}
	for _, snapshot := range code {
		for _, component := range snapshot.Components {
			features = append(features, &model.Feature{
				ID:              "feature_component_" + shortHash(component.ID),
				Name:            component.Name,
				Kind:            "supporting",
				UserValue:       "该组件可作为演示中的可操作能力或视觉证明。",
				Priority:        "supporting",
				BestAudience:    []string{project.TargetAudience},
				KeyActions:      component.ActionLabels,
				SupportingPages: []string{},
				EvidenceRefs:    component.EvidenceRefs,
			})
			if len(features) >= 6 {
				return features
			}
		}
	}
	for _, page := range pages {
		for _, action := range page.Actions {
			features = append(features, &model.Feature{
				ID:           "feature_page_action_" + shortHash(action.ID),
				Name:         firstNonEmpty(action.Label, "页面关键动作"),
				Kind:         "supporting",
				UserValue:    "页面动作可用于构造可执行演示路径。",
				Priority:     "supporting",
				BestAudience: []string{project.TargetAudience},
				KeyActions:   []string{action.Kind},
				EvidenceRefs: action.EvidenceRefs,
			})
			if len(features) >= 6 {
				return features
			}
		}
	}
	return features
}

func workflowCandidates(project *model.ProjectContext, brief *model.RequirementBrief, pages []model.PageUnderstandingSnapshot, intelligence *model.ProjectIntelligencePack) []*model.WorkflowCandidate {
	useCase := model.DemoUseCaseLaunch
	if brief != nil && len(brief.UseCases) > 0 {
		useCase = brief.UseCases[0]
	}
	candidates := []*model.WorkflowCandidate{}
	if intelligence != nil {
		for _, scenario := range intelligence.DemoScenarioPlans {
			candidates = append(candidates, &model.WorkflowCandidate{
				ID:             firstNonEmpty(scenario.ID, "workflow_scenario_"+shortHash(scenario.Name)),
				Name:           firstNonEmpty(scenario.Name, "项目智能候选演示路径"),
				UseCase:        firstNonEmptyUseCase(scenario.UseCase, useCase),
				AudienceID:     firstNonEmpty(scenario.AudienceID, "audience_primary"),
				FeatureRefs:    append([]string{}, scenario.FeatureRefs...),
				PageRefs:       append([]string{}, scenario.PageRefs...),
				EstimatedSteps: scenario.EstimatedSteps,
				ValueScore:     scenario.ValueScore,
				Feasibility:    scenario.Feasibility,
				RiskNotes:      append([]string{}, scenario.RiskNotes...),
				EvidenceRefs:   scenario.EvidenceRefs,
			})
		}
		if len(candidates) > 0 {
			return candidates
		}
	}
	pageRefs := []string{}
	for _, page := range pages {
		pageRefs = append(pageRefs, page.ID)
	}
	return []*model.WorkflowCandidate{{
		ID:             "workflow_script_ready_demo",
		Name:           "脚本文档预览流程",
		UseCase:        useCase,
		AudienceID:     "audience_primary",
		FeatureRefs:    []string{"feature_primary_value"},
		PageRefs:       pageRefs,
		EstimatedSteps: 3,
		ValueScore:     0.82,
		Feasibility:    0.74,
		RiskNotes:      append([]string{}, project.ForbiddenPages...),
	}}
}

func combinedEvidenceRefs(brief *model.RequirementBrief, code []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot, intelligence *model.ProjectIntelligencePack) []model.EvidenceRef {
	refs := []model.EvidenceRef{}
	if brief != nil {
		refs = append(refs, brief.EvidenceRefs...)
	}
	for _, snapshot := range code {
		refs = append(refs, snapshot.EvidenceRefs...)
	}
	for _, page := range pages {
		refs = append(refs, page.EvidenceRefs...)
	}
	if intelligence != nil {
		refs = append(refs, intelligence.EvidenceRefs...)
	}
	return uniqueEvidenceRefs(refs)
}

func safetyReportFromUnderstanding(project *model.ProjectContext, code []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot, intelligence *model.ProjectIntelligencePack) *model.SafetyReport {
	findings := []model.AgentFinding{}
	for _, snapshot := range code {
		for _, sensitive := range snapshot.SensitiveFields {
			findings = append(findings, model.AgentFinding{
				ID:         "finding_sensitive_code_" + shortHash(sensitive.Name),
				Kind:       "sensitive_code_field",
				Severity:   model.FindingSeverityWarning,
				Summary:    "代码结构摘要中发现可能敏感字段：" + sensitive.Name,
				Confidence: 0.72,
			})
		}
	}
	for _, page := range pages {
		findings = append(findings, page.RiskFindings...)
	}
	maskedFields := append([]string{}, project.ForbiddenData...)
	notes := []string{"默认仅上传结构摘要和脱敏证据，不上传完整源码。"}
	if intelligence != nil && intelligence.SafetyReport != nil {
		findings = append(findings, intelligence.SafetyReport.PolicyFindings...)
		maskedFields = append(maskedFields, intelligence.SafetyReport.MaskedFields...)
		notes = append(notes, intelligence.SafetyReport.Notes...)
	}
	return &model.SafetyReport{
		AllowedToProceed: true,
		PolicyFindings:   findings,
		MaskedFields:     uniqueStrings(maskedFields),
		Notes:            uniqueStrings(notes),
	}
}

func understandingConfidence(code []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot, intelligence *model.ProjectIntelligencePack) float64 {
	confidence := 0.72
	if len(code) > 0 {
		confidence += 0.08
	}
	if len(pages) > 0 {
		confidence += 0.08
	}
	if intelligence != nil && intelligence.Confidence > 0 {
		confidence += 0.04
	}
	if confidence > 0.92 {
		return 0.92
	}
	return confidence
}

func useCasesFromBrief(brief *model.RequirementBrief) []model.DemoUseCase {
	if brief != nil && len(brief.UseCases) > 0 {
		return append([]model.DemoUseCase{}, brief.UseCases...)
	}
	return []model.DemoUseCase{model.DemoUseCaseLaunch}
}

func firstNonEmptyUseCase(value model.DemoUseCase, fallback model.DemoUseCase) model.DemoUseCase {
	if value != "" {
		return value
	}
	return fallback
}

func compactProjectIntelligence(projectID string, intelligence *model.ProjectIntelligencePack) map[string]any {
	if intelligence == nil {
		return nil
	}
	architecture := map[string]any{}
	if intelligence.Architecture != nil {
		architecture = map[string]any{
			"summary":      intelligence.Architecture.Summary,
			"frameworks":   intelligence.Architecture.Frameworks,
			"languages":    intelligence.Architecture.Languages,
			"module_count": len(intelligence.Architecture.Modules),
			"route_count":  len(intelligence.Architecture.RouteTree),
		}
	}
	return map[string]any{
		"project_id":           projectID,
		"architecture":         architecture,
		"feature_capabilities": compactCapabilities(intelligence.FeatureCapabilities),
		"interaction_surfaces": compactSurfaces(intelligence.InteractionSurfaces),
		"api_count":            len(intelligence.APIContracts),
		"data_model_count":     len(intelligence.DataModels),
		"demo_scenario_plans":  limitScenarioPlans(intelligence.DemoScenarioPlans, 3),
		"script_readiness":     intelligence.ScriptReadinessReport,
		"source_digest_sha256": intelligence.SourceDigestSHA256,
		"confidence":           intelligence.Confidence,
	}
}

func limitScenarioPlans(plans []model.DemoScenarioPlan, maxItems int) []model.DemoScenarioPlan {
	if len(plans) <= maxItems {
		return plans
	}
	return plans[:maxItems]
}

func compactCodeSnapshots(snapshots []model.CodeUnderstandingSnapshot) []map[string]any {
	result := make([]map[string]any, 0, len(snapshots))
	for _, snapshot := range snapshots {
		result = append(result, map[string]any{
			"id":            snapshot.ID,
			"summary":       snapshot.Summary,
			"file_count":    snapshot.FileCount,
			"languages":     snapshot.Languages,
			"frameworks":    snapshot.Frameworks,
			"routes":        limitRoutes(snapshot.Routes, 30),
			"components":    limitComponents(snapshot.Components, 30),
			"selectors":     limitSelectors(snapshot.Selectors, 30),
			"api_endpoints": limitAPIs(snapshot.APIEndpoints, 30),
			"data_models":   limitDataModels(snapshot.DataModels, 20),
			"investigation": compactCodeInvestigationTrace(snapshot.InvestigationTrace),
			"source_digest": snapshot.SourceDigestSHA256,
		})
	}
	return result
}

func compactCodeInvestigationTrace(trace *model.CodeInvestigationTrace) map[string]any {
	if trace == nil {
		return nil
	}
	toolCalls := make([]map[string]any, 0, minInt(len(trace.ToolCalls), 8))
	for _, call := range trace.ToolCalls {
		if len(toolCalls) >= 8 {
			break
		}
		toolCalls = append(toolCalls, map[string]any{
			"tool":                call.Tool,
			"purpose":             call.Purpose,
			"query":               call.Query,
			"output_summary":      call.OutputSummary,
			"matched_file_count":  call.MatchedFileCount,
			"selected_file_count": call.SelectedFileCount,
			"snippet_count":       len(call.SnippetRefs),
			"snippet_refs":        compactCodeSnippetRefs(call.SnippetRefs, 6),
			"confidence":          call.Confidence,
			"fallback_reason":     call.FallbackReason,
			"metadata":            compactInvestigationToolMetadata(call.Metadata),
		})
	}
	return map[string]any{
		"mode":                   trace.Mode,
		"summary":                trace.Summary,
		"questions":              compactCodeInvestigationQuestions(trace.Questions, 6),
		"total_files_discovered": trace.TotalFilesDiscovered,
		"total_files_searched":   trace.TotalFilesSearched,
		"total_files_selected":   trace.TotalFilesSelected,
		"tool_calls":             toolCalls,
	}
}

func compactCodeInvestigationQuestions(questions []model.CodeInvestigationQuestion, maxItems int) []map[string]any {
	if len(questions) == 0 || maxItems <= 0 {
		return nil
	}
	out := make([]map[string]any, 0, minInt(len(questions), maxItems))
	for _, question := range questions {
		if len(out) >= maxItems {
			break
		}
		out = append(out, map[string]any{
			"id":                question.ID,
			"question":          question.Question,
			"intent_label":      question.IntentLabel,
			"expected_evidence": question.ExpectedEvidence,
			"status":            question.Status,
			"remaining_gaps":    question.RemainingGaps,
			"evidence_summary":  question.EvidenceSummary,
			"confidence":        question.Confidence,
		})
	}
	return out
}

func compactCodeSnippetRefs(refs []model.CodeSnippetRef, maxItems int) []map[string]any {
	if len(refs) == 0 || maxItems <= 0 {
		return nil
	}
	out := make([]map[string]any, 0, minInt(len(refs), maxItems))
	for _, ref := range refs {
		if len(out) >= maxItems {
			break
		}
		out = append(out, map[string]any{
			"id":             ref.ID,
			"path_hash":      ref.PathHashSHA256,
			"line_start":     ref.LineStart,
			"line_end":       ref.LineEnd,
			"matched_terms":  ref.MatchedTerms,
			"signal_kinds":   ref.SignalKinds,
			"content_digest": ref.ContentSHA256,
		})
	}
	return out
}

func compactInvestigationToolMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	out := map[string]any{}
	for _, key := range []string{"package_managers", "script_names", "dependency_names", "manifest_count", "git_tracked_file_count", "config_files"} {
		value, ok := metadata[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case []string:
			out[key] = limitStrings(typed, 20)
		default:
			out[key] = typed
		}
	}
	return out
}

func compactPageSnapshots(snapshots []model.PageUnderstandingSnapshot) []map[string]any {
	result := make([]map[string]any, 0, len(snapshots))
	for _, page := range snapshots {
		result = append(result, map[string]any{
			"id":               page.ID,
			"url":              page.URL,
			"title":            page.Title,
			"page_role":        page.PageRole,
			"ocr_text":         truncateString(page.OCRText, 1200),
			"vision_summary":   page.VisionSummary,
			"actions":          page.Actions,
			"stable_selectors": page.StableSelectors,
			"states":           page.States,
			"risk_findings":    page.RiskFindings,
		})
	}
	return result
}

func imageInputsFromPages(pages []model.PageUnderstandingSnapshot) []llm.ImageInput {
	images := []llm.ImageInput{}
	for _, page := range pages {
		if page.ScreenshotRef == nil {
			continue
		}
		ref := page.ScreenshotRef.URI
		if ref == "" {
			continue
		}
		image := llm.ImageInput{MimeType: page.ScreenshotRef.MimeType, Label: page.Title}
		if strings.HasPrefix(ref, "data:") {
			image.DataURI = ref
		} else if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
			image.URL = ref
		}
		if image.DataURI != "" || image.URL != "" {
			images = append(images, image)
		}
	}
	return images
}

func truncateString(value string, maxLen int) string {
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}
