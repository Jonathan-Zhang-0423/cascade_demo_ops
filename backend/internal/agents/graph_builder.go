package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type GraphBuilderAgent struct{}

func NewGraphBuilderAgent() *GraphBuilderAgent { return &GraphBuilderAgent{} }

func (a *GraphBuilderAgent) GenerateGraph(ctx context.Context, project *model.ProjectContext, productMap *model.ProductMap, report *model.MultimodalUnderstandingReport) (*model.DemoWorkflowGraph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if project == nil {
		return nil, errors.New("project context is required")
	}
	graphID := fmt.Sprintf("graph_%d", time.Now().UnixNano())
	featureID, featureValue := primaryFeature(productMap)
	audience := primaryAudience(project)
	entryPoint := graphEntryPoint(project, productMap)
	startAction := "inspect"
	startActionType := model.GraphActionInspect
	startExpectedOutcome := "产品上下文可用于脚本生成"
	startTarget := model.ActionTarget{Selector: "body"}
	startSelector := "body"
	if isHTTPURL(entryPoint) {
		startAction = "navigate"
		startActionType = model.GraphActionNavigate
		startExpectedOutcome = "产品入口页面加载完成"
		startTarget = model.ActionTarget{URL: entryPoint}
		startSelector = entryPoint
	}
	useCase := model.DemoUseCaseLaunch
	if len(project.Goals) > 0 {
		useCase = project.Goals[0].UseCase
	}
	objective := "生成可审批、可复现、可执行的产品演示脚本。"
	if report != nil && report.RequirementBrief != nil {
		if len(report.RequirementBrief.UseCases) > 0 {
			useCase = report.RequirementBrief.UseCases[0]
		}
		objective = firstNonEmpty(report.RequirementBrief.Objective, objective)
	}
	primaryAction := primaryPageAction(productMap, report)
	primaryActionType := graphActionTypeFromKind(primaryAction.Kind, primaryAction.Selector)

	graph := model.NewDemoWorkflowGraph(graphID, project.ID, entryPoint)
	graph.Status = model.GraphStatusReviewReady
	graph.Name = "多模态理解生成的演示脚本流程"
	graph.Summary = firstNonEmpty(reportSummary(report), "基于需求、代码摘要和页面证据生成的可审批执行方案。")
	graph.Intent = &model.WorkflowIntent{
		UseCase:            useCase,
		Audience:           audience,
		Objective:          objective,
		ValueProposition:   featureValue,
		PrimaryFeatureRefs: []string{featureID},
		SuccessCriteria:    []string{"入口页面可打开", "核心动作可定位", "安全策略可复核", "脚本文档可审批"},
		CTA:                "请复核脚本文档并审批执行方案。",
	}
	graph.Requirements = requirementsFromProject(project)
	graph.States = []*model.GraphState{
		{
			ID:         "state_product_entry",
			Name:       "产品入口已加载",
			Kind:       "page",
			URLPattern: entryPoint,
			DOMHints: []model.SelectorCandidate{
				{Kind: "css", Value: "main", Confidence: 0.6, Source: "multimodal_understanding"},
				{Kind: "css", Value: "body", Confidence: 0.6, Source: "multimodal_understanding"},
			},
			FeatureRefs:  []string{featureID},
			EvidenceRefs: reportEvidenceRefs(report),
		},
	}
	graph.Validations = []*model.ValidationSpec{
		{
			ID:        "validate_entry_loaded",
			Kind:      "dom_visible",
			Target:    model.ActionTarget{Selector: "body"},
			Assertion: "入口页面加载后 body 可见",
			Expected:  "visible",
			Severity:  "blocking",
			Required:  true,
			RepairPolicy: &model.RepairPolicy{
				AllowSelectorRepair: true,
				AllowDataRepair:     false,
				AllowStepSkip:       false,
				MaxAttempts:         2,
			},
		},
	}
	graph.Narratives = []*model.NarrativeSegment{
		{
			ID:           "narrative_primary_value",
			NodeRefs:     []string{"start", "highlight_primary_value", "close"},
			Title:        "核心产品价值",
			Summary:      featureValue,
			Voiceover:    featureValue,
			Tone:         project.BrandTone,
			AudienceLens: project.TargetAudience,
			EvidenceRefs: reportEvidenceRefs(report),
		},
	}
	graph.Nodes = []*model.GraphNode{
		{
			ID:              "start",
			Action:          startAction,
			Selector:        startSelector,
			ExpectedOutcome: startExpectedOutcome,
			IsScreenshot:    true,
			HasZoom:         false,
			RetryPolicy:     2,
			Type:            model.GraphNodeTypeStart,
			Title:           "打开产品入口",
			Goal:            "在隔离浏览器会话中打开客户产品入口。",
			FeatureRefs:     []string{featureID},
			ActionSpec: &model.GraphAction{
				Type:      startActionType,
				Target:    startTarget,
				TimeoutMS: 30000,
				WaitUntil: "networkidle",
			},
			StateAfter: []model.StateAssertion{
				{
					ID:        "state_after_start_body_visible",
					Kind:      "dom_visible",
					Target:    model.ActionTarget{Selector: "body"},
					Operator:  "is_visible",
					Expected:  true,
					Required:  true,
					TimeoutMS: 10000,
				},
			},
			Validations: []model.ValidationSpec{
				{
					ID:        "node_start_loaded",
					Kind:      "page_loaded",
					Target:    model.ActionTarget{URL: entryPoint},
					Assertion: "产品入口可用于演示脚本规划",
					Expected:  true,
					Severity:  "blocking",
					Required:  true,
				},
			},
			Narrative: &model.NarrativeCue{
				Title:        "从产品入口开始",
				Caption:      "打开产品环境并确认演示上下文。",
				AudienceLens: project.TargetAudience,
			},
			Capture:      &model.CaptureSpec{Screenshot: true, Video: true, AssetRole: "opening_context"},
			EvidenceRefs: reportEvidenceRefs(report),
			FailurePolicy: &model.NodeFailurePolicy{
				RetryAttempts: 2,
				RepairPolicy:  &model.RepairPolicy{AllowSelectorRepair: true, AllowDataRepair: false, AllowStepSkip: false, MaxAttempts: 2},
			},
		},
		{
			ID:              "highlight_primary_value",
			Action:          string(primaryActionType),
			Selector:        firstNonEmpty(primaryAction.Selector, "main"),
			ExpectedOutcome: firstNonEmpty(primaryAction.ExpectedOutcome, featureValue),
			IsScreenshot:    true,
			HasZoom:         true,
			RetryPolicy:     2,
			Type:            model.GraphNodeTypeCapture,
			Title:           firstNonEmpty(primaryAction.Label, "展示核心产品价值"),
			Goal:            "捕捉对目标受众最有价值的产品动作或页面区域。",
			FeatureRefs:     []string{featureID},
			ActionSpec: &model.GraphAction{
				Type:      primaryActionType,
				Target:    primaryAction.Target,
				TimeoutMS: 10000,
			},
			Validations: []model.ValidationSpec{
				{
					ID:        "node_primary_value_visible",
					Kind:      "dom_visible",
					Target:    primaryAction.Target,
					Assertion: "核心演示目标可见或可执行",
					Expected:  true,
					Severity:  "blocking",
					Required:  true,
					RepairPolicy: &model.RepairPolicy{
						AllowSelectorRepair: true,
						AllowDataRepair:     false,
						AllowStepSkip:       false,
						MaxAttempts:         2,
					},
				},
			},
			Narrative: &model.NarrativeCue{
				Title:        "展示价值",
				Voiceover:    featureValue,
				Caption:      featureValue,
				Callout:      "核心价值",
				Tone:         project.BrandTone,
				AudienceLens: project.TargetAudience,
			},
			Capture:      &model.CaptureSpec{Screenshot: true, Video: true, Zoom: true, Callout: true, FocusSelector: firstNonEmpty(primaryAction.Selector, "main"), AssetRole: "hero_feature", MaskSelectors: maskSelectorsFromProject(project)},
			EvidenceRefs: primaryAction.EvidenceRefs,
			FailurePolicy: &model.NodeFailurePolicy{
				RetryAttempts: 2,
				RepairPolicy:  &model.RepairPolicy{AllowSelectorRepair: true, AllowDataRepair: true, AllowStepSkip: false, MaxAttempts: 2},
			},
		},
		{
			ID:              "close",
			Action:          "assert",
			Selector:        "body",
			ExpectedOutcome: "演示脚本形成可审批的闭环",
			IsScreenshot:    false,
			HasZoom:         true,
			RetryPolicy:     1,
			Type:            model.GraphNodeTypeEnd,
			Title:           "完成脚本闭环",
			Goal:            "确保执行方案有清晰终态，便于审批、打包和后续录制。",
			FeatureRefs:     []string{featureID},
			ActionSpec: &model.GraphAction{
				Type:      model.GraphActionAssert,
				Target:    model.ActionTarget{Selector: "body"},
				TimeoutMS: 5000,
			},
			Validations: []model.ValidationSpec{
				{
					ID:        "node_close_body_present",
					Kind:      "dom_present",
					Target:    model.ActionTarget{Selector: "body"},
					Assertion: "脚本结束时页面仍处于可检查状态",
					Expected:  true,
					Severity:  "warning",
					Required:  true,
				},
			},
			Narrative: &model.NarrativeCue{
				Title:        "收束演示",
				Caption:      "执行方案已准备进入人工审批。",
				AudienceLens: project.TargetAudience,
			},
			Capture:       &model.CaptureSpec{Screenshot: false, Video: true, Zoom: true, AssetRole: "closing_validation"},
			EvidenceRefs:  reportEvidenceRefs(report),
			FailurePolicy: &model.NodeFailurePolicy{RetryAttempts: 1},
		},
	}
	graph.Edges = []*model.GraphEdge{
		{ID: "e1", FromNode: "start", ToNode: "highlight_primary_value", Condition: "loaded", ConditionSpec: &model.EdgeCondition{Kind: "validation_passed", PassState: "state_product_entry"}, Priority: 1},
		{ID: "e2", FromNode: "highlight_primary_value", ToNode: "close", Condition: "validated", ConditionSpec: &model.EdgeCondition{Kind: "validation_passed"}, Priority: 1},
	}
	productMapID := ""
	if productMap != nil {
		productMapID = productMap.ID
	}
	graph.Provenance = &model.GraphProvenance{
		CreatedBy:    "GraphBuilderAgent",
		Model:        "deterministic_multimodal_mock_v1",
		ProductMapID: productMapID,
		EvidenceRefs: reportEvidenceRefs(report),
	}
	graph.Maintenance = &model.MaintenancePolicy{
		UpdateTriggers:   []string{"release_note_changed", "route_changed", "selector_validation_failed"},
		StalenessDays:    30,
		RegressionChecks: []string{"rehearse_primary_workflow"},
	}
	graph.EvidenceRefs = reportEvidenceRefs(report)
	if graph.Assets != nil {
		graph.Assets.Brand = project.BrandKit
		graph.Assets.Provenance = &model.AssetProvenance{WorkflowGraphID: graph.ID, GraphVersion: graph.Version, GeneratedBy: "GraphBuilderAgent"}
	}
	return graph, nil
}

func primaryFeature(productMap *model.ProductMap) (string, string) {
	if productMap != nil && len(productMap.Features) > 0 && productMap.Features[0] != nil {
		feature := productMap.Features[0]
		featureID := feature.ID
		if featureID == "" {
			featureID = "feature_primary_workflow"
		}
		if feature.UserValue != "" {
			return featureID, feature.UserValue
		}
		return featureID, feature.Name
	}
	return "feature_primary_workflow", "用最短路径展示产品核心价值，并形成可审批、可复现的演示脚本。"
}

func graphEntryPoint(project *model.ProjectContext, productMap *model.ProductMap) string {
	if productMap != nil && len(productMap.Pages) > 0 && productMap.Pages[0] != nil && productMap.Pages[0].URL != "" {
		return productMap.Pages[0].URL
	}
	if project.ProductURL != "" {
		return project.ProductURL
	}
	if project.Inputs != nil {
		if len(project.Inputs.WebpageScreenshots) > 0 {
			screenshot := project.Inputs.WebpageScreenshots[0]
			if screenshot.URL != "" {
				return screenshot.URL
			}
			return "input://webpage_screenshots/" + screenshot.ID
		}
		if len(project.Inputs.Code) > 0 {
			return "input://code/" + project.Inputs.Code[0].ID
		}
		if len(project.Inputs.RequirementDocuments) > 0 {
			return "input://requirements/" + project.Inputs.RequirementDocuments[0].ID
		}
	}
	return "input://product_context"
}

func isHTTPURL(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(normalized, "http://") || strings.HasPrefix(normalized, "https://")
}

func primaryAudience(project *model.ProjectContext) *model.AudienceProfile {
	if project != nil && len(project.Audiences) > 0 {
		return &project.Audiences[0]
	}
	return &model.AudienceProfile{ID: "audience_primary", Name: "目标受众"}
}

type graphPrimaryAction struct {
	Label           string
	Kind            string
	Selector        string
	Target          model.ActionTarget
	ExpectedOutcome string
	EvidenceRefs    []model.EvidenceRef
}

func primaryPageAction(productMap *model.ProductMap, report *model.MultimodalUnderstandingReport) graphPrimaryAction {
	if productMap != nil {
		for _, page := range productMap.Pages {
			if page == nil {
				continue
			}
			for _, action := range page.PrimaryActions {
				target := model.ActionTarget{
					URL:          action.TargetRoute,
					Selector:     action.Selector,
					Label:        action.Label,
					ComponentRef: action.ID,
					EvidenceRefs: action.EvidenceRefs,
				}
				if target.Selector == "" {
					target.Selector = "main"
					target.SelectorAlternatives = []model.SelectorCandidate{{Kind: "css", Value: "[role='main']", Confidence: 0.5}}
				}
				return graphPrimaryAction{
					Label:           firstNonEmpty(action.Label, "展示核心产品价值"),
					Kind:            firstNonEmpty(action.Kind, "inspect"),
					Selector:        target.Selector,
					Target:          target,
					ExpectedOutcome: "关键动作或页面区域可见",
					EvidenceRefs:    action.EvidenceRefs,
				}
			}
		}
	}
	if report != nil {
		for _, page := range report.PageSnapshots {
			for _, action := range page.Actions {
				target := model.ActionTarget{
					URL:          action.TargetURL,
					Selector:     action.SelectorHint,
					Label:        action.Label,
					EvidenceRefs: action.EvidenceRefs,
				}
				if target.Selector == "" {
					target.Selector = "main"
					target.SelectorAlternatives = []model.SelectorCandidate{{Kind: "css", Value: "[role='main']", Confidence: 0.5}}
				}
				return graphPrimaryAction{
					Label:           firstNonEmpty(action.Label, "展示页面关键动作"),
					Kind:            firstNonEmpty(action.Kind, "inspect"),
					Selector:        target.Selector,
					Target:          target,
					ExpectedOutcome: firstNonEmpty(page.VisionSummary, "页面关键动作可见"),
					EvidenceRefs:    action.EvidenceRefs,
				}
			}
		}
	}
	return graphPrimaryAction{
		Label:           "展示核心产品价值",
		Kind:            "inspect",
		Selector:        "main",
		Target:          model.ActionTarget{Selector: "main", SelectorAlternatives: []model.SelectorCandidate{{Kind: "css", Value: "[role='main']", Confidence: 0.5}}},
		ExpectedOutcome: "核心产品区域可见",
	}
}

func graphActionTypeFromKind(kind string, selector string) model.GraphActionType {
	normalized := strings.ToLower(strings.TrimSpace(kind))
	switch normalized {
	case "click", "button", "cta":
		return model.GraphActionClick
	case "fill", "input", "type":
		return model.GraphActionFill
	case "select":
		return model.GraphActionSelect
	case "upload":
		return model.GraphActionUpload
	case "wait":
		return model.GraphActionWait
	case "assert", "validate":
		return model.GraphActionAssert
	case "navigate":
		return model.GraphActionNavigate
	case "api", "api_call":
		return model.GraphActionAPICall
	default:
		if selector != "" {
			return model.GraphActionInspect
		}
		return model.GraphActionInspect
	}
}

func reportSummary(report *model.MultimodalUnderstandingReport) string {
	if report == nil {
		return ""
	}
	return report.Summary
}

func reportEvidenceRefs(report *model.MultimodalUnderstandingReport) []model.EvidenceRef {
	if report == nil {
		return nil
	}
	return report.EvidenceRefs
}

func maskSelectorsFromProject(project *model.ProjectContext) []string {
	selectors := []string{"[data-sensitive]"}
	if project != nil && project.SecurityPolicy != nil {
		selectors = append(selectors, project.SecurityPolicy.MaskSelectors...)
	}
	return uniqueStrings(selectors)
}

func requirementsFromProject(project *model.ProjectContext) []model.GraphRequirement {
	requirements := make([]model.GraphRequirement, 0, len(project.MustShow)+len(project.MustNotShow)+len(project.ForbiddenPages)+len(project.ForbiddenData))
	for i, item := range project.MustShow {
		requirements = append(requirements, model.GraphRequirement{
			ID:          fmt.Sprintf("must_show_%d", i+1),
			Kind:        "must_show",
			Description: item,
			Required:    true,
		})
	}
	for i, item := range project.MustNotShow {
		requirements = append(requirements, model.GraphRequirement{
			ID:          fmt.Sprintf("must_not_show_%d", i+1),
			Kind:        "must_not_show",
			Description: item,
			Required:    true,
		})
	}
	for i, item := range project.ForbiddenPages {
		requirements = append(requirements, model.GraphRequirement{
			ID:          fmt.Sprintf("forbidden_page_%d", i+1),
			Kind:        "forbidden_page",
			Description: item,
			Required:    true,
		})
	}
	for i, item := range project.ForbiddenData {
		requirements = append(requirements, model.GraphRequirement{
			ID:          fmt.Sprintf("forbidden_data_%d", i+1),
			Kind:        "forbidden_data",
			Description: item,
			Required:    true,
		})
	}
	return requirements
}
