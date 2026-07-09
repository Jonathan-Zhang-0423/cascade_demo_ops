package agents

import (
	"context"
	"encoding/json"
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
		Summary:            understandingSummary(brief, codeSnapshots, pageSnapshots),
		FeatureHypotheses:  featureHypotheses(project, brief, codeSnapshots, pageSnapshots),
		WorkflowCandidates: workflowCandidates(project, brief, pageSnapshots),
		EvidenceRefs:       combinedEvidenceRefs(brief, codeSnapshots, pageSnapshots),
		SafetyReport:       safetyReportFromUnderstanding(project, codeSnapshots, pageSnapshots),
		Confidence:         understandingConfidence(codeSnapshots, pageSnapshots),
		CreatedAt:          now,
	}
	trace, err := a.enhanceReportWithLLM(ctx, project, brief, report)
	if err != nil && !llm.IsDeterministicFallback(err) {
		return nil, err
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
	Summary  string `json:"summary"`
	Features []struct {
		ID            string              `json:"id"`
		Name          string              `json:"name"`
		Kind          string              `json:"kind"`
		UserValue     string              `json:"user_value"`
		BusinessValue string              `json:"business_value"`
		Priority      string              `json:"priority"`
		KeyActions    []string            `json:"key_actions"`
		BestUseCases  []model.DemoUseCase `json:"best_use_cases"`
		Risks         []string            `json:"risks"`
	} `json:"features"`
	Workflows []struct {
		ID             string            `json:"id"`
		Name           string            `json:"name"`
		UseCase        model.DemoUseCase `json:"use_case"`
		EstimatedSteps int               `json:"estimated_steps"`
		ValueScore     float64           `json:"value_score"`
		Feasibility    float64           `json:"feasibility"`
		RiskNotes      []string          `json:"risk_notes"`
	} `json:"workflows"`
	SafetyNotes []string `json:"safety_notes"`
	Confidence  float64  `json:"confidence"`
}

func (a *MultimodalUnderstandingAgent) enhanceReportWithLLM(ctx context.Context, project *model.ProjectContext, brief *model.RequirementBrief, report *model.MultimodalUnderstandingReport) (*llm.CallTrace, error) {
	if a.llm == nil || project == nil || report == nil {
		return nil, nil
	}
	payload := map[string]any{
		"brief":           brief,
		"code_summary":    compactCodeSnapshots(report.CodeSnapshots),
		"page_summary":    compactPageSnapshots(report.PageSnapshots),
		"safety_policy":   project.SecurityPolicy,
		"target_audience": project.TargetAudience,
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
	if len(report.PageSnapshots) > 0 {
		trace, err = a.llm.GenerateMultimodal(ctx, config.ModelTaskMultimodalUnderstanding, llm.MultimodalRequest{
			System:      request.System,
			User:        request.User,
			SchemaName:  request.SchemaName,
			MaxTokens:   request.MaxTokens,
			Temperature: request.Temperature,
			Images:      imageInputsFromPages(report.PageSnapshots),
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
				BestUseCases:  feature.BestUseCases,
				KeyActions:    feature.KeyActions,
				Risks:         feature.Risks,
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
				UseCase:        workflow.UseCase,
				AudienceID:     "audience_primary",
				FeatureRefs:    []string{"feature_primary_value"},
				EstimatedSteps: workflow.EstimatedSteps,
				ValueScore:     workflow.ValueScore,
				Feasibility:    workflow.Feasibility,
				RiskNotes:      workflow.RiskNotes,
				EvidenceRefs:   report.EvidenceRefs,
			})
		}
	}
	if report.SafetyReport != nil {
		report.SafetyReport.Notes = uniqueStrings(append(report.SafetyReport.Notes, output.SafetyNotes...))
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

func understandingSummary(brief *model.RequirementBrief, code []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot) string {
	objective := "产品演示脚本"
	if brief != nil && brief.Objective != "" {
		objective = brief.Objective
	}
	return "已融合需求、代码结构摘要和页面/截图证据，目标是：" + objective
}

func featureHypotheses(project *model.ProjectContext, brief *model.RequirementBrief, code []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot) []*model.Feature {
	features := []*model.Feature{}
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

func workflowCandidates(project *model.ProjectContext, brief *model.RequirementBrief, pages []model.PageUnderstandingSnapshot) []*model.WorkflowCandidate {
	useCase := model.DemoUseCaseLaunch
	if brief != nil && len(brief.UseCases) > 0 {
		useCase = brief.UseCases[0]
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

func combinedEvidenceRefs(brief *model.RequirementBrief, code []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot) []model.EvidenceRef {
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
	return refs
}

func safetyReportFromUnderstanding(project *model.ProjectContext, code []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot) *model.SafetyReport {
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
	return &model.SafetyReport{
		AllowedToProceed: true,
		PolicyFindings:   findings,
		MaskedFields:     append([]string{}, project.ForbiddenData...),
		Notes:            []string{"默认仅上传结构摘要和脱敏证据，不上传完整源码。"},
	}
}

func understandingConfidence(code []model.CodeUnderstandingSnapshot, pages []model.PageUnderstandingSnapshot) float64 {
	confidence := 0.72
	if len(code) > 0 {
		confidence += 0.08
	}
	if len(pages) > 0 {
		confidence += 0.08
	}
	if confidence > 0.92 {
		return 0.92
	}
	return confidence
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
			"source_digest": snapshot.SourceDigestSHA256,
		})
	}
	return result
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
