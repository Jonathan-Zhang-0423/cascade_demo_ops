package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

type GraphBuilderAgent struct {
	llm llm.Client
}

func NewGraphBuilderAgent() *GraphBuilderAgent { return &GraphBuilderAgent{} }

func NewGraphBuilderAgentWithLLM(client llm.Client) *GraphBuilderAgent {
	return &GraphBuilderAgent{llm: client}
}

type graphLLMOutput struct {
	Name             string   `json:"name"`
	Summary          string   `json:"summary"`
	Objective        string   `json:"objective"`
	ValueProposition string   `json:"value_proposition"`
	CTA              string   `json:"cta"`
	SuccessCriteria  []string `json:"success_criteria"`
	Steps            []struct {
		NodeID          string   `json:"node_id"`
		Title           string   `json:"title"`
		Goal            string   `json:"goal"`
		Action          string   `json:"action"`
		Selector        string   `json:"selector"`
		ExpectedOutcome string   `json:"expected_outcome"`
		Voiceover       string   `json:"voiceover"`
		Caption         string   `json:"caption"`
		Callout         string   `json:"callout"`
		DurationMS      int      `json:"duration_ms"`
		Tags            []string `json:"tags"`
	} `json:"steps"`
}

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
	trace, err := a.enhanceGraphWithLLM(ctx, project, productMap, report, graph)
	if err != nil && !llm.IsDeterministicFallback(err) {
		return nil, err
	}
	if trace != nil {
		graph.Provenance.Model = trace.Label()
		graph.EvidenceRefs = append(graph.EvidenceRefs, model.EvidenceRef{
			ID:         "ev_model_graph_" + shortHash(trace.Label()),
			Kind:       model.EvidenceKindUserInput,
			Summary:    "GraphBuilderAgent 模型路由：" + trace.Label(),
			FieldPath:  "model_trace.graph_builder",
			Confidence: 0.7,
		})
		graph.Provenance.EvidenceRefs = graph.EvidenceRefs
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

func (a *GraphBuilderAgent) enhanceGraphWithLLM(ctx context.Context, project *model.ProjectContext, productMap *model.ProductMap, report *model.MultimodalUnderstandingReport, graph *model.DemoWorkflowGraph) (*llm.CallTrace, error) {
	if a.llm == nil || project == nil || graph == nil {
		return nil, nil
	}
	payload := map[string]any{
		"target_audience": project.TargetAudience,
		"product_url":     project.ProductURL,
		"product_map":     productMap,
		"report_summary":  "",
		"brief":           nil,
		"current_graph":   graphPatchInput(graph),
	}
	if report != nil {
		payload["report_summary"] = report.Summary
		payload["brief"] = report.RequirementBrief
	}
	data, _ := json.Marshal(payload)
	var output graphLLMOutput
	trace, err := a.llm.GenerateJSON(ctx, config.ModelTaskPlanning, llm.JSONRequest{
		System:       "你是 Cascade DemoOps 的 Demo Workflow Graph agent。请把产品地图转成可审批、可执行、适合中国客户理解的演示流程。只能使用既有 node_id，并且 action 只能是 navigate/click/fill/select/upload/wait/assert/inspect/api_call。",
		User:         string(data),
		SchemaName:   "WorkflowGraphPatch",
		ResponseHint: "返回字段：name, summary, objective, value_proposition, cta, success_criteria, steps[{node_id,title,goal,action,selector,expected_outcome,voiceover,caption,callout,duration_ms,tags}]。",
		MaxTokens:    2200,
		Temperature:  0.2,
	}, &output)
	if err != nil {
		return trace, err
	}
	if output.Name != "" {
		graph.Name = output.Name
	}
	if output.Summary != "" {
		graph.Summary = output.Summary
	}
	if graph.Intent != nil {
		if output.Objective != "" {
			graph.Intent.Objective = output.Objective
		}
		if output.ValueProposition != "" {
			graph.Intent.ValueProposition = output.ValueProposition
		}
		if output.CTA != "" {
			graph.Intent.CTA = output.CTA
		}
		graph.Intent.SuccessCriteria = uniqueStrings(append(graph.Intent.SuccessCriteria, output.SuccessCriteria...))
	}
	byID := map[string]*model.GraphNode{}
	for _, node := range graph.Nodes {
		if node != nil {
			byID[node.ID] = node
		}
	}
	for _, step := range output.Steps {
		node := byID[step.NodeID]
		if node == nil {
			continue
		}
		if step.Title != "" {
			node.Title = step.Title
		}
		if step.Goal != "" {
			node.Goal = step.Goal
		}
		if action := validGraphAction(step.Action); action != "" {
			node.Action = string(action)
			if node.ActionSpec != nil {
				node.ActionSpec.Type = action
			}
		}
		if step.Selector != "" && !isHTTPURL(step.Selector) {
			node.Selector = step.Selector
			if node.ActionSpec != nil && node.ActionSpec.Target.Selector != "" {
				node.ActionSpec.Target.Selector = step.Selector
			}
		}
		if step.ExpectedOutcome != "" {
			node.ExpectedOutcome = step.ExpectedOutcome
		}
		if step.DurationMS > 0 && step.DurationMS <= 30000 {
			node.DurationHintMS = step.DurationMS
		}
		node.Tags = uniqueStrings(append(node.Tags, step.Tags...))
		if node.Narrative == nil {
			node.Narrative = &model.NarrativeCue{}
		}
		if step.Voiceover != "" {
			node.Narrative.Voiceover = step.Voiceover
		}
		if step.Caption != "" {
			node.Narrative.Caption = step.Caption
		}
		if step.Callout != "" {
			node.Narrative.Callout = step.Callout
		}
	}
	return trace, nil
}

func graphPatchInput(graph *model.DemoWorkflowGraph) map[string]any {
	nodes := []map[string]any{}
	for _, node := range graph.Nodes {
		if node == nil {
			continue
		}
		nodes = append(nodes, map[string]any{
			"id":               node.ID,
			"type":             node.Type,
			"title":            node.Title,
			"action":           node.Action,
			"selector":         node.Selector,
			"expected_outcome": node.ExpectedOutcome,
			"goal":             node.Goal,
		})
	}
	return map[string]any{
		"id":          graph.ID,
		"name":        graph.Name,
		"summary":     graph.Summary,
		"entry_point": graph.EntryPoint,
		"nodes":       nodes,
	}
}

func validGraphAction(value string) model.GraphActionType {
	switch model.GraphActionType(strings.TrimSpace(value)) {
	case model.GraphActionNavigate:
		return model.GraphActionNavigate
	case model.GraphActionClick:
		return model.GraphActionClick
	case model.GraphActionFill:
		return model.GraphActionFill
	case model.GraphActionSelect:
		return model.GraphActionSelect
	case model.GraphActionUpload:
		return model.GraphActionUpload
	case model.GraphActionWait:
		return model.GraphActionWait
	case model.GraphActionAssert:
		return model.GraphActionAssert
	case model.GraphActionInspect:
		return model.GraphActionInspect
	case model.GraphActionAPICall:
		return model.GraphActionAPICall
	default:
		return ""
	}
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
