package agents

import (
	"context"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type RequirementReaderAgent struct{}

func NewRequirementReaderAgent() *RequirementReaderAgent { return &RequirementReaderAgent{} }

func (a *RequirementReaderAgent) ReadRequirements(ctx context.Context, project *model.ProjectContext) (*model.RequirementBrief, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	brief := &model.RequirementBrief{
		ID:             "requirement_brief_" + project.ID,
		ProjectID:      project.ID,
		SchemaVersion:  model.MultimodalUnderstandingReportSchemaVersion,
		TargetAudience: project.TargetAudience,
		Objective:      firstNonEmpty(project.ProductDescription, "生成一套可审批、可复现、可执行的产品演示脚本。"),
		PrimaryOutcome: "形成可被人工审批并打包给云端执行的 Demo Workflow Graph 和脚本文档。",
		MustShow:       append([]string{}, project.MustShow...),
		MustNotShow:    append([]string{}, project.MustNotShow...),
		ForbiddenPages: append([]string{}, project.ForbiddenPages...),
		ForbiddenData:  append([]string{}, project.ForbiddenData...),
		BrandTone:      project.BrandTone,
		RequiredAssets: []model.AssetKind{model.AssetKindDemoVideo, model.AssetKindStepByStepDocs},
		Confidence:     0.82,
		CreatedAt:      now,
	}
	if len(project.Goals) > 0 {
		for _, goal := range project.Goals {
			brief.UseCases = append(brief.UseCases, goal.UseCase)
			if goal.ValueProposition != "" {
				brief.Objective = goal.ValueProposition
			}
			brief.MustShow = append(brief.MustShow, goal.SuccessCriteria...)
		}
	}
	if project.Inputs != nil {
		brief.EvidenceRefs = append(brief.EvidenceRefs, model.EvidenceRef{
			ID:         "ev_user_prompt_" + shortHash(project.Inputs.RawUserPrompt),
			Kind:       model.EvidenceKindUserInput,
			Summary:    "用户原始演示需求",
			FieldPath:  "inputs.raw_user_prompt",
			Confidence: 0.8,
		})
		for _, doc := range project.Inputs.RequirementDocuments {
			text := firstNonEmpty(doc.Body, doc.Title)
			if text != "" && brief.Objective == "" {
				brief.Objective = text
			}
			brief.MustShow = append(brief.MustShow, doc.FocusAreas...)
			brief.EvidenceRefs = append(brief.EvidenceRefs, model.EvidenceRef{
				ID:         firstNonEmpty(doc.ID, "requirement_doc_"+shortHash(text)),
				Kind:       model.EvidenceKindRequirementDoc,
				Summary:    firstNonEmpty(doc.Title, "需求文档"),
				FieldPath:  "inputs.requirement_documents",
				ArtifactID: artifactID(doc.Artifact),
				Confidence: 0.88,
			})
		}
		for _, note := range project.Inputs.ReleaseNotes {
			brief.MustShow = append(brief.MustShow, note.FeatureRefs...)
			brief.EvidenceRefs = append(brief.EvidenceRefs, model.EvidenceRef{
				ID:         firstNonEmpty(note.ID, "release_note_"+shortHash(note.Body)),
				Kind:       model.EvidenceKindReleaseNote,
				Summary:    firstNonEmpty(note.Title, note.Version, "发布说明"),
				FieldPath:  "inputs.release_notes",
				Confidence: 0.78,
			})
		}
		for _, scenario := range project.Inputs.Scenarios {
			if scenario.UseCase != "" {
				brief.UseCases = append(brief.UseCases, scenario.UseCase)
			}
			if scenario.Objective != "" {
				brief.Objective = scenario.Objective
			}
			if scenario.PrimaryOutcome != "" {
				brief.PrimaryOutcome = scenario.PrimaryOutcome
			}
			brief.MustShow = append(brief.MustShow, scenario.MustShow...)
			brief.MustNotShow = append(brief.MustNotShow, scenario.MustAvoid...)
		}
		for _, requirement := range project.Inputs.Requirements {
			if requirement.Required {
				brief.MustShow = append(brief.MustShow, requirement.Description)
			} else {
				brief.MustNotShow = append(brief.MustNotShow, requirement.Description)
			}
			brief.EvidenceRefs = append(brief.EvidenceRefs, requirement.EvidenceRefs...)
		}
	}
	if len(brief.UseCases) == 0 {
		brief.UseCases = []model.DemoUseCase{defaultUseCaseForAudience(project.TargetAudience)}
	}
	brief.Scenario = scenarioName(brief.UseCases[0])
	brief.MustShow = uniqueStrings(brief.MustShow)
	brief.MustNotShow = uniqueStrings(brief.MustNotShow)
	brief.ForbiddenPages = uniqueStrings(brief.ForbiddenPages)
	brief.ForbiddenData = uniqueStrings(brief.ForbiddenData)
	brief.UseCases = uniqueUseCases(brief.UseCases)
	if strings.TrimSpace(brief.Objective) == "" {
		brief.Objective = "生成可审批、可复现、可执行的产品演示脚本。"
	}
	return brief, nil
}

func artifactID(ref *model.ArtifactRef) string {
	if ref == nil {
		return ""
	}
	return ref.ID
}

func uniqueUseCases(values []model.DemoUseCase) []model.DemoUseCase {
	seen := map[model.DemoUseCase]bool{}
	result := make([]model.DemoUseCase, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func scenarioName(useCase model.DemoUseCase) string {
	switch useCase {
	case model.DemoUseCaseSupport:
		return "AI 客服演示"
	case model.DemoUseCaseOnboarding:
		return "企业新人教程"
	case model.DemoUseCaseSales:
		return "销售演示"
	case model.DemoUseCaseInvestor:
		return "投资人演示"
	default:
		return "产品演示"
	}
}
