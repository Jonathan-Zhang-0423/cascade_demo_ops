package agents

import (
	"context"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type MultimodalUnderstandingAgent struct{}

func NewMultimodalUnderstandingAgent() *MultimodalUnderstandingAgent {
	return &MultimodalUnderstandingAgent{}
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
	project.KnowledgeRefs = append(project.KnowledgeRefs, report.EvidenceRefs...)
	return report, nil
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
