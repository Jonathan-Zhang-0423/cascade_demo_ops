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
	Name             string              `json:"name"`
	Summary          string              `json:"summary"`
	Objective        string              `json:"objective"`
	ValueProposition string              `json:"value_proposition"`
	CTA              string              `json:"cta"`
	SuccessCriteria  flexibleStringSlice `json:"success_criteria"`
	Steps            []struct {
		NodeID          string              `json:"node_id"`
		Title           string              `json:"title"`
		Goal            string              `json:"goal"`
		Action          string              `json:"action"`
		Selector        string              `json:"selector"`
		ExpectedOutcome string              `json:"expected_outcome"`
		Voiceover       string              `json:"voiceover"`
		Caption         string              `json:"caption"`
		Callout         string              `json:"callout"`
		DurationMS      int                 `json:"duration_ms"`
		Tags            flexibleStringSlice `json:"tags"`
	} `json:"steps"`
}

func (o *graphLLMOutput) UnmarshalJSON(data []byte) error {
	type alias graphLLMOutput
	var single alias
	if err := json.Unmarshal(data, &single); err == nil {
		*o = graphLLMOutput(single)
		return nil
	}
	var items []alias
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	merged := graphLLMOutput{}
	for _, item := range items {
		patch := graphLLMOutput(item)
		if merged.Name == "" {
			merged.Name = patch.Name
		}
		if merged.Summary == "" {
			merged.Summary = patch.Summary
		}
		if merged.Objective == "" {
			merged.Objective = patch.Objective
		}
		if merged.ValueProposition == "" {
			merged.ValueProposition = patch.ValueProposition
		}
		if merged.CTA == "" {
			merged.CTA = patch.CTA
		}
		merged.SuccessCriteria = append(merged.SuccessCriteria, patch.SuccessCriteria...)
		merged.Steps = append(merged.Steps, patch.Steps...)
	}
	merged.SuccessCriteria = flexibleStringSlice(uniqueStrings([]string(merged.SuccessCriteria)))
	*o = merged
	return nil
}

func (a *GraphBuilderAgent) GenerateGraph(ctx context.Context, project *model.ProjectContext, productMap *model.ProductMap, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack) (*model.DemoWorkflowGraph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if project == nil {
		return nil, errors.New("project context is required")
	}
	if intelligence == nil && project.ProjectIntelligence != nil {
		intelligence = project.ProjectIntelligence
	}
	intelligence = ensureProjectIntelligenceForGraph(project, intelligence)
	if intelligence != nil && intelligence.BusinessStagePlan != nil && len(intelligence.BusinessStagePlan.Stages) > 0 {
		return a.generateGraphFromBusinessStagePlan(ctx, project, productMap, report, intelligence)
	}
	if err := requireVerifiedInteractionPlan(project, intelligence); err != nil {
		return nil, err
	}
	if intelligence != nil && intelligence.VerifiedInteraction != nil {
		return a.generateGraphFromVerifiedInteractions(ctx, project, productMap, report, intelligence)
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
	primaryAction := primaryPageAction(productMap, report, intelligence)
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
				WaitUntil: "domcontentloaded",
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
			Selector:        primaryAction.Selector,
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
					Severity:  severityForAction(primaryActionType),
					Required:  primaryActionType != model.GraphActionInspect,
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
			Capture:      &model.CaptureSpec{Screenshot: true, Video: true, Zoom: true, Callout: true, FocusSelector: primaryAction.Selector, AssetRole: "hero_feature", MaskSelectors: maskSelectorsFromProject(project)},
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
	augmentGraphWithIntelligence(graph, project, productMap, report, intelligence)
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
	trace, err := a.enhanceGraphWithLLM(ctx, project, productMap, report, intelligence, graph)
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
	graph.EvidenceRefs = uniqueEvidenceRefs(append(reportEvidenceRefs(report), intelligenceEvidenceRefs(intelligence)...))
	if graph.Assets != nil {
		graph.Assets.Brand = project.BrandKit
		graph.Assets.Provenance = &model.AssetProvenance{WorkflowGraphID: graph.ID, GraphVersion: graph.Version, GeneratedBy: "GraphBuilderAgent"}
	}
	bindGraphRequirements(project, graph)
	return graph, nil
}

func requireVerifiedInteractionPlan(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack) error {
	if intelligence == nil {
		return errors.New("verified interaction plan is required before graph generation")
	}
	if intelligence.VerifiedInteraction == nil {
		if intelligence.MissingEvidenceReport != nil && intelligence.MissingEvidenceReport.Summary != "" {
			return fmt.Errorf("verified interaction plan is missing: %s", intelligence.MissingEvidenceReport.Summary)
		}
		return errors.New("verified interaction plan is missing; run PageInteractionVerifierAgent before GraphBuilderAgent")
	}
	if intelligence.VerifiedInteraction.BusinessActionCount <= 0 {
		if intelligence.MissingEvidenceReport != nil && intelligence.MissingEvidenceReport.Summary != "" {
			return fmt.Errorf("no verified business action: %s", intelligence.MissingEvidenceReport.Summary)
		}
		return errors.New("no verified business action; graph generation blocked to avoid fake scripts")
	}
	_ = project
	return nil
}

func ensureProjectIntelligenceForGraph(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack) *model.ProjectIntelligencePack {
	if project == nil {
		return intelligence
	}
	if intelligence == nil {
		intelligence = &model.ProjectIntelligencePack{
			ID:            "intel_graph_input_" + project.ID,
			ProjectID:     project.ID,
			SchemaVersion: model.ProjectIntelligencePackSchemaVersion,
			Confidence:    0.46,
			CreatedAt:     time.Now().UTC(),
		}
	}
	if intelligence.DemoIntent == nil {
		intelligence.DemoIntent = demoIntentFromProjectContext(project)
	}
	if intelligence.RunIntentScope == nil {
		intelligence.RunIntentScope = runIntentScopeForProject(project)
	}
	if intelligence.VerifiedInteraction != nil && intelligence.VerifiedInteraction.BusinessActionCount > 0 {
		return intelligence
	}
	return intelligence
}

func (a *GraphBuilderAgent) generateGraphFromBusinessStagePlan(ctx context.Context, project *model.ProjectContext, productMap *model.ProductMap, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack) (*model.DemoWorkflowGraph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stagePlan := intelligence.BusinessStagePlan
	if stagePlan == nil || len(stagePlan.Stages) == 0 {
		return nil, errors.New("business stage plan is required before graph generation")
	}
	entryPoint := scopedProductURL(project, intelligence.RunIntentScope, firstNonEmpty(project.ProductURL, graphEntryPoint(project, productMap)))
	if entryPoint == "" {
		entryPoint = graphEntryPoint(project, productMap)
	}
	featureID, featureValue := primaryFeature(productMap)
	featureValue = firstNonEmpty(project.ProductDescription, featureValue)
	useCase := model.DemoUseCaseLaunch
	if report != nil && report.RequirementBrief != nil && len(report.RequirementBrief.UseCases) > 0 {
		useCase = report.RequirementBrief.UseCases[0]
	} else if len(project.Goals) > 0 {
		useCase = project.Goals[0].UseCase
	}

	graphID := fmt.Sprintf("graph_%d", time.Now().UnixNano())
	graph := model.NewDemoWorkflowGraph(graphID, project.ID, entryPoint)
	graph.Status = model.GraphStatusReviewReady
	graph.Name = "业务阶段驱动的 Browser Agent 演示大纲"
	graph.Summary = "按用户需求生成业务阶段状态机，再绑定代码/页面证据，交给云端 browser agent 在受约束范围内自适应执行。"
	graph.Intent = &model.WorkflowIntent{
		UseCase:            useCase,
		Audience:           primaryAudience(project),
		Objective:          firstNonEmpty(stagePlanObjective(stagePlan), featureValue),
		ValueProposition:   featureValue,
		PrimaryFeatureRefs: []string{featureID},
		SuccessCriteria:    businessStageSuccessCriteria(stagePlan),
		CTA:                "请审核业务阶段、输入语义、路由状态和 server agent 可修改边界。",
	}
	graph.Requirements = requirementsFromProject(project)
	graph.Variables = append(graph.Variables, demoCredentialVariables(project)...)
	graph.Nodes = make([]*model.GraphNode, 0, len(stagePlan.Stages))
	for _, stage := range stagePlan.Stages {
		node := graphNodeFromBusinessStage(project, stage, entryPoint, featureID)
		if node != nil {
			graph.Nodes = append(graph.Nodes, node)
		}
	}
	if len(graph.Nodes) == 0 {
		return nil, errors.New("business stage plan produced no graph nodes")
	}
	graph.Edges = sequentialGraphEdges(graph.Nodes)
	graph.States = businessStageGraphStates(entryPoint, stagePlan, featureID)
	graph.Validations = verifiedGraphValidations(entryPoint)
	graph.Narratives = []*model.NarrativeSegment{{
		ID:           "narrative_business_stage_workflow",
		NodeRefs:     nodeIDs(graph.Nodes),
		Title:        "业务阶段主线",
		Summary:      firstNonEmpty(stagePlanObjective(stagePlan), featureValue),
		Voiceover:    featureValue,
		Tone:         project.BrandTone,
		AudienceLens: project.TargetAudience,
		EvidenceRefs: uniqueEvidenceRefs(append(reportEvidenceRefs(report), stagePlan.EvidenceRefs...)),
	}}
	graph.Assets = model.NewMVPAssetManifest()
	if totalMS := totalBusinessStageDurationMS(stagePlan); totalMS > 0 {
		graph.Assets.TargetDurationSec = ceilDurationSeconds(totalMS)
	}
	graph.Maintenance = &model.MaintenancePolicy{
		UpdateTriggers:   []string{"requirement_changed", "business_stage_changed", "route_changed", "browser_agent_diagnostic"},
		StalenessDays:    14,
		RegressionChecks: []string{"verify_business_stage_plan", "run_browser_agent_outline"},
	}
	graph.EvidenceRefs = uniqueEvidenceRefs(append(intelligenceEvidenceRefs(intelligence), stagePlan.EvidenceRefs...))
	graph.Provenance = &model.GraphProvenance{
		CreatedBy:    "GraphBuilderAgent",
		Model:        "deterministic_business_stage_plan_v1",
		ProductMapID: productMapID(productMap),
		EvidenceRefs: graph.EvidenceRefs,
	}
	if graph.Assets != nil {
		graph.Assets.Brand = project.BrandKit
		graph.Assets.Provenance = &model.AssetProvenance{WorkflowGraphID: graph.ID, GraphVersion: graph.Version, GeneratedBy: "GraphBuilderAgent"}
	}
	bindGraphRequirements(project, graph)
	return graph, nil
}

func graphNodeFromBusinessStage(project *model.ProjectContext, stage model.BusinessStage, entryPoint string, featureID string) *model.GraphNode {
	actionType := businessStageGraphActionType(stage)
	target := businessStageActionTarget(stage, entryPoint)
	selector := target.Selector
	targetURL := firstNonEmpty(target.URL, urlForBusinessStage(stage, entryPoint))
	if target.URL == "" {
		target.URL = targetURL
	}
	pageRef := firstNonEmpty(stage.EntryRoute, routePathFromCandidate(targetURL), entryPoint)
	nodeType := model.GraphNodeTypeAction
	if stage.Kind == model.BusinessStageKindSessionSetup {
		nodeType = model.GraphNodeTypeStart
	} else if stage.Kind == model.BusinessStageKindFinalObserve {
		nodeType = model.GraphNodeTypeEnd
	}
	if stage.Kind == model.BusinessStageKindObserveProgress || (stage.Kind == model.BusinessStageKindFinalObserve && actionType == model.GraphActionInspect) {
		actionType = model.GraphActionWait
		if stage.Kind == model.BusinessStageKindFinalObserve {
			actionType = model.GraphActionInspect
		}
		target.Selector = ""
		selector = ""
	}
	if actionType == model.GraphActionPress {
		// Keyboard actions target the approved page/preview route, not an
		// arbitrary DOM control. Preserve semantic evidence while ensuring the
		// Worker never tries to resolve or click an inferred selector.
		target.Selector = ""
		target.TestID = ""
		target.Role = ""
		target.ComponentRef = ""
		target.SelectorAlternatives = nil
		target.Label = stage.Action.Label
		target.Text = stage.Action.Label
		selector = ""
	}
	actionValue := stage.Action.InputValue
	if stage.Kind == model.BusinessStageKindSessionSetup {
		actionValue = ""
	}
	required := businessStageKindIsCoreForGraph(stage.Kind) || stage.Kind == model.BusinessStageKindSessionSetup || stage.Kind == model.BusinessStageKindFinalObserve
	validations := []model.ValidationSpec{businessStageValidation(stage, actionType, target, required)}
	if stage.Kind == model.BusinessStageKindSessionSetup {
		validations = append(validations, model.ValidationSpec{
			ID: "validate_authenticated_workspace_" + stage.ID, Kind: "element_visible",
			Target:    model.ActionTarget{Role: "main", Label: firstNonEmpty(stage.Action.SuccessState, "已认证工作区")},
			Assertion: "登录后必须出现已认证工作区标识", Expected: true, Severity: "blocking", Required: true,
			EvidenceRefs: stage.EvidenceRefs,
		})
	}
	metadata := map[string]any{
		"business_stage_id":           stage.ID,
		"business_stage_kind":         string(stage.Kind),
		"business_route_state":        string(stage.RouteState),
		"business_stage_order":        stage.Order,
		"non_destructive":             stage.Action.NonDestructive,
		"business_stage_entry_route":  stage.EntryRoute,
		"verification_status":         "business_stage_plan",
		"runtime_adaptive":            stage.Action.NonDestructive,
		"expected_route_after_action": stage.ExpectedRouteAfterAction,
	}
	if len(stage.Uncertainties) > 0 {
		metadata["uncertainty_count"] = len(stage.Uncertainties)
	}
	if stage.Kind == model.BusinessStageKindSessionSetup && project.DemoAccount != nil {
		metadata["demo_username_secret_ref"] = project.DemoAccount.UsernameSecretRef
		metadata["demo_password_secret_ref"] = project.DemoAccount.PasswordSecretRef
	}
	parameters := map[string]any{}
	for key, value := range stage.Action.Parameters {
		parameters[key] = value
	}
	return &model.GraphNode{
		ID:              stage.ID,
		Action:          string(actionType),
		Selector:        selector,
		InputData:       actionValue,
		ExpectedOutcome: firstNonEmpty(stage.Action.SuccessState, stage.Objective),
		IsScreenshot:    true,
		HasZoom:         required,
		RetryPolicy:     2,
		Type:            nodeType,
		Title:           firstNonEmpty(stage.Title, string(stage.Kind)),
		Goal:            firstNonEmpty(stage.Objective, stage.Action.Label),
		PageRef:         pageRef,
		FeatureRefs:     uniqueStrings([]string{featureID, stage.ID}),
		ActionSpec: &model.GraphAction{
			Type:       actionType,
			Target:     target,
			Value:      actionValue,
			InputRef:   stage.Action.InputRef,
			SecretRef:  stage.Action.SecretRef,
			Parameters: parameters,
			TimeoutMS:  stage.DurationMS,
			WaitUntil:  businessStageWaitUntil(stage),
		},
		StateAfter: []model.StateAssertion{{
			ID:           "state_after_" + stage.ID,
			Kind:         businessStageStateAssertionKind(stage, actionType),
			Target:       target,
			Operator:     "is_visible",
			Expected:     true,
			Required:     required && actionType != model.GraphActionWait,
			TimeoutMS:    12000,
			EvidenceRefs: stage.EvidenceRefs,
		}},
		Validations: validations,
		Narrative: &model.NarrativeCue{
			Title:        firstNonEmpty(stage.Title, stage.Action.Label),
			Voiceover:    firstNonEmpty(stage.Objective, stage.Action.SuccessState),
			Caption:      firstNonEmpty(stage.Action.SuccessState, stage.Objective),
			Callout:      firstNonEmpty(stage.Action.Label, stage.Title),
			Tone:         project.BrandTone,
			AudienceLens: project.TargetAudience,
		},
		Capture: &model.CaptureSpec{
			Screenshot:    true,
			Video:         true,
			Zoom:          required,
			Callout:       required,
			FocusSelector: selector,
			AssetRole:     "business_stage_" + string(stage.Kind),
			MaskSelectors: maskSelectorsFromProject(project),
		},
		EvidenceRefs: uniqueEvidenceRefs(stage.EvidenceRefs),
		FailurePolicy: &model.NodeFailurePolicy{
			RetryAttempts: 2,
			RepairPolicy: &model.RepairPolicy{
				AllowSelectorRepair: true,
				AllowDataRepair:     false,
				AllowStepSkip:       false,
				MaxAttempts:         2,
			},
		},
		DurationHintMS: stage.DurationMS,
		Sensitive:      stage.Kind == model.BusinessStageKindSessionSetup,
		Tags:           uniqueStrings([]string{"business_stage_plan", string(stage.Kind), string(stage.RouteState)}),
		Metadata:       metadata,
	}
}

func businessStageGraphActionType(stage model.BusinessStage) model.GraphActionType {
	switch stage.Kind {
	case model.BusinessStageKindSessionSetup:
		return model.GraphActionNavigate
	case model.BusinessStageKindBusinessInput:
		return model.GraphActionFill
	case model.BusinessStageKindModeSelection, model.BusinessStageKindBusinessAction, model.BusinessStageKindBusinessSubmit:
		return model.GraphActionClick
	case model.BusinessStageKindObserveProgress:
		return model.GraphActionWait
	case model.BusinessStageKindFinalObserve:
		if declared := graphActionTypeFromKind(stage.Action.Type, ""); declared == model.GraphActionPress {
			return declared
		}
		return model.GraphActionInspect
	default:
		return graphActionTypeFromKind(stage.Action.Type, "")
	}
}

func businessStageActionTarget(stage model.BusinessStage, entryPoint string) model.ActionTarget {
	target := model.ActionTarget{URL: urlForBusinessStage(stage, entryPoint)}
	if len(stage.Targets) == 0 {
		target.Label = stage.Action.Label
		target.Text = stage.Action.Label
		return target
	}
	bestIndex := -1
	for index, candidate := range stage.Targets {
		if !businessTargetEligibleForStageAction(stage, candidate) {
			continue
		}
		if bestIndex < 0 || businessStageTargetRank(stage, candidate) > businessStageTargetRank(stage, stage.Targets[bestIndex]) {
			bestIndex = index
		}
	}
	if bestIndex < 0 {
		// Fail closed when a business stage has only similarly named containers,
		// stale entities, or post-action result controls. Runtime repair may bind
		// a concrete action, but the App must not promote one of those guesses.
		target.Label = stage.Action.Label
		target.Text = stage.Action.Label
		return target
	}
	best := stage.Targets[bestIndex]
	// Session setup has two distinct routes: the authentication entry and the
	// authenticated workspace observed after submission. The action must start
	// from the former; the latter belongs to the success validation contract.
	if stage.Kind != model.BusinessStageKindSessionSetup {
		target.URL = firstNonEmpty(best.URL, urlForBusinessStage(stage, entryPoint))
	}
	// A selector is executable only when the exact value is represented by a
	// complete App-approved provenance candidate. TestID/role/label remain
	// semantic discovery hints; they are not silently promoted to a guessed CSS
	// locator.
	target.Selector = selectorForBusinessTarget(best)
	target.Label = firstNonEmpty(best.Label, stage.Action.Label)
	target.Text = best.Text
	target.Role = best.Role
	if target.Selector != "" {
		target.TestID = best.TestID
	}
	target.ComponentRef = best.ComponentRef
	target.Source = best.VerificationSource
	target.EvidenceRefs = best.EvidenceRefs
	target.SelectorAlternatives = append(target.SelectorAlternatives, formalBusinessSelectorAlternatives(best.Alternatives)...)
	target.SelectorAlternatives = uniqueSelectorCandidates(target.SelectorAlternatives)
	return target
}

func businessTargetRank(candidate model.BusinessTargetCandidate) int {
	rank := candidate.SelectorScore
	if candidate.IsVerified {
		rank += 100
	}
	semanticHandle := strings.ToLower(strings.Join([]string{candidate.TestID, candidate.Selector, candidate.ComponentRef}, " "))
	if containsAnyNormalized(semanticHandle, "button-", "input-", "new-project", "create-project", "build-mode", "start-build") {
		rank += 350
	}
	if containsAnyNormalized(semanticHandle, "card-project-", "text-project-name-", "emoji-project-", "project-menu-", "rename-project", "delete-project") {
		// These handles identify an existing project entity, not a creation
		// control or the result of opening the creation flow.
		rank -= 2000
	}
	if containsAnyNormalized(semanticHandle, "workspace", "project-list", "container", "page-root", "app-root") || selectorLooksGeneric(candidate.Selector) {
		rank -= 500
	}
	if len([]rune(strings.TrimSpace(candidate.Label))) > 80 {
		// A scan that concatenates the text of many descendant controls is a
		// container summary, not the accessible name of one actionable target.
		rank -= 400
	}
	for _, alternative := range candidate.Alternatives {
		if alternative.SourceKind == "page_scan" && model.SelectorCandidateHasFormalProvenance(alternative) && selectorCandidateMatchesValue(alternative, candidate.Selector) {
			rank += 1000
			break
		}
	}
	return rank
}

func businessStageTargetRank(stage model.BusinessStage, candidate model.BusinessTargetCandidate) int {
	rank := businessTargetRank(candidate)
	if isNewProjectEntryStage(stage) && isExplicitNewProjectEntryCandidate(candidate) {
		rank += 3000
	}
	return rank
}

func businessTargetEligibleForStageAction(stage model.BusinessStage, candidate model.BusinessTargetCandidate) bool {
	if !isNewProjectEntryStage(stage) {
		return true
	}
	return isExplicitNewProjectEntryCandidate(candidate)
}

func isNewProjectEntryStage(stage model.BusinessStage) bool {
	return stage.Kind == model.BusinessStageKindBusinessAction && strings.TrimPrefix(stage.ID, "business_stage_") == "new_project_entry"
}

func isExplicitNewProjectEntryCandidate(candidate model.BusinessTargetCandidate) bool {
	if graphActionTypeFromKind(candidate.Kind, candidate.Selector) != model.GraphActionClick {
		return false
	}
	structure := strings.ToLower(strings.Join([]string{candidate.TestID, candidate.Selector, candidate.ComponentRef}, " "))
	if containsAnyNormalized(structure,
		"card-project-", "text-project-name-", "emoji-project-", "project-menu-",
		"dashboard-page", "project-list", "rename-project", "delete-project",
	) {
		return false
	}
	if containsAnyNormalized(structure,
		"button-new-project", "new-project-button", "new-project-trigger", "create-project-trigger",
	) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(candidate.TestID), "new-project") {
		return true
	}
	semantics := strings.ToLower(strings.Join([]string{candidate.Label, candidate.Text, candidate.Role}, " "))
	return containsAnyNormalized(structure, "button") && containsAnyNormalized(semantics,
		"new project", "create project", "新建项目", "创建项目", "新增项目",
	)
}

func selectorCandidateMatchesValue(candidate model.SelectorCandidate, selector string) bool {
	if strings.EqualFold(strings.TrimSpace(candidate.Value), strings.TrimSpace(selector)) {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(candidate.Kind), "testid") {
		return false
	}
	compact := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(selector, "\"", "'"), " ", ""))
	return strings.Contains(compact, "data-testid='"+strings.ToLower(strings.TrimSpace(candidate.Value))+"'")
}

func selectorForBusinessTarget(candidate model.BusinessTargetCandidate) string {
	selector := strings.TrimSpace(candidate.Selector)
	if selector == "" {
		return ""
	}
	for _, alternative := range candidate.Alternatives {
		if !model.SelectorCandidateHasFormalProvenance(alternative) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(alternative.Value), selector) {
			return selector
		}
		if strings.EqualFold(strings.TrimSpace(alternative.Kind), "testid") && strings.Contains(strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(selector, "\"", "'"), " ", "")), "data-testid='"+strings.ToLower(strings.TrimSpace(alternative.Value))+"'") {
			return selector
		}
	}
	return ""
}

func formalBusinessSelectorAlternatives(values []model.SelectorCandidate) []model.SelectorCandidate {
	result := make([]model.SelectorCandidate, 0, len(values))
	for _, value := range values {
		if model.SelectorCandidateHasFormalProvenance(value) {
			result = append(result, value)
		}
	}
	return result
}

func businessStageValidation(stage model.BusinessStage, action model.GraphActionType, target model.ActionTarget, required bool) model.ValidationSpec {
	kind := "element_visible"
	expected := any(true)
	timeoutMS := 0
	if stage.Kind == model.BusinessStageKindSessionSetup {
		// Login controls are action targets. A visible email field (especially a
		// homepage waitlist field) cannot prove that authentication succeeded.
		// Bind the required validation to the approved post-login route/state.
		if route := strings.TrimSpace(stage.ExpectedRouteAfterAction); route != "" {
			kind = "url_matches"
			target = model.ActionTarget{URL: route}
			expected = route
		} else {
			kind = "text_contains"
			target = model.ActionTarget{Role: "main", Text: firstNonEmpty(stage.Action.SuccessState, stage.Objective, "工作台")}
			expected = firstNonEmpty(stage.Action.SuccessState, stage.Objective, "工作台")
		}
	} else if action == model.GraphActionNavigate {
		kind = "url_matches"
		expected = firstNonEmpty(target.URL, stage.EntryRoute)
	} else if stage.Kind == model.BusinessStageKindBusinessAction && strings.TrimPrefix(stage.ID, "business_stage_") == "new_project_entry" {
		// The clicked button is an action target, not proof that the creation
		// flow opened.  Prefer an App-verified dialog/input result target and
		// otherwise require a semantic dialog assertion instead of reusing the
		// button selector.
		resultTarget := businessStageResultTarget(stage)
		if resultTarget.TestID != "" || resultTarget.Selector != "" || resultTarget.Role != "" {
			target = resultTarget
			kind = "element_visible"
			expected = true
		} else {
			kind = "text_contains"
			target = model.ActionTarget{Role: "dialog", Label: "新建项目表单"}
			expected = "新建项目"
		}
	} else if stage.Kind == model.BusinessStageKindBusinessInput && strings.TrimSpace(stage.Action.InputValue) != "" {
		kind = "value_equals"
		expected = stage.Action.InputValue
	} else if stage.Kind == model.BusinessStageKindModeSelection {
		// A still-visible mode button is not proof that selection took effect.
		kind = "text_contains"
		target = model.ActionTarget{Text: firstNonEmpty(stage.Action.SuccessState, stage.Objective, stage.Title)}
		expected = firstNonEmpty(stage.Action.SuccessState, stage.Objective, stage.Title)
	} else if stage.Kind == model.BusinessStageKindBusinessSubmit && strings.TrimSpace(stage.ExpectedRouteAfterAction) != "" {
		kind = "url_matches"
		target = model.ActionTarget{URL: stage.ExpectedRouteAfterAction}
		expected = stage.ExpectedRouteAfterAction
	} else if stage.Kind == model.BusinessStageKindFinalObserve && strings.TrimPrefix(stage.ID, "business_stage_") == "final_observe" && wantsBuildCompletion(stage.UserIntent) {
		if completionTarget, ok := businessStageCompletionTarget(stage); ok {
			target = completionTarget
			kind = "element_visible"
			expected = true
		} else {
			// Fail closed on the immutable completion claim. The result text must
			// appear before the bounded timeout; a matching route alone is not a
			// completed build.
			kind = "text_contains"
			target = model.ActionTarget{Text: firstNonEmpty(stage.Action.SuccessState, "Agent 构建已完成")}
			expected = firstNonEmpty(stage.Action.SuccessState, "Agent 构建已完成")
		}
		timeoutMS = completionWaitTimeoutMS(stage.UserIntent)
	} else if action == model.GraphActionPress {
		kind = "page_changed"
		target = model.ActionTarget{URL: firstNonEmpty(stage.ExpectedRouteAfterAction, stage.EntryRoute, target.URL)}
		expected = true
	} else if stage.Kind == model.BusinessStageKindFinalObserve && strings.TrimPrefix(stage.ID, "business_stage_") == "playable_preview" {
		kind = "playable_surface_visible"
		target = model.ActionTarget{URL: firstNonEmpty(stage.ExpectedRouteAfterAction, stage.EntryRoute, target.URL)}
		expected = true
	} else if action == model.GraphActionWait || action == model.GraphActionInspect {
		if route := firstNonEmpty(stage.ExpectedRouteAfterAction, stage.EntryRoute); route != "" {
			kind = "url_matches"
			target = model.ActionTarget{URL: route}
			expected = route
		} else {
			kind = "text_contains"
			expected = firstNonEmpty(stage.Action.SuccessState, stage.Objective, stage.Title)
		}
	} else if action == model.GraphActionClick || action == model.GraphActionSelect || action == model.GraphActionUpload || action == model.GraphActionAPICall {
		kind = "text_contains"
		target = model.ActionTarget{Text: firstNonEmpty(stage.Action.SuccessState, stage.Objective, stage.Title)}
		expected = firstNonEmpty(stage.Action.SuccessState, stage.Objective, stage.Title)
	}
	return model.ValidationSpec{
		ID:           "validate_" + stage.ID,
		Kind:         kind,
		Target:       target,
		Assertion:    firstNonEmpty(stage.Action.SuccessState, stage.Objective),
		Expected:     expected,
		TimeoutMS:    timeoutMS,
		Severity:     severityForBusinessStage(stage, required),
		Required:     required,
		EvidenceRefs: stage.EvidenceRefs,
		RepairPolicy: &model.RepairPolicy{AllowSelectorRepair: true, AllowDataRepair: false, AllowStepSkip: false, MaxAttempts: 2},
	}
}

func businessStageCompletionTarget(stage model.BusinessStage) (model.ActionTarget, bool) {
	bestScore := -1
	best := model.ActionTarget{}
	for _, candidate := range stage.Targets {
		semantic := strings.ToLower(strings.Join([]string{candidate.ID, candidate.IntentGoalID, candidate.Label, candidate.Text, candidate.TestID, candidate.Selector, candidate.ComponentRef}, " "))
		if !containsAnyNormalized(semantic,
			"build-result", "build complete", "build_complete", "all complete", "all steps", "completed",
			"构建完成", "全部步骤完成", "所有步骤完成", "完成结果",
		) {
			continue
		}
		selector := selectorForBusinessTarget(candidate)
		if selector == "" && candidate.TestID == "" {
			continue
		}
		score := businessTargetRank(candidate)
		if containsAnyNormalized(semantic, "build-result-card", "build_complete", "all_complete", "构建完成") {
			score += 1000
		}
		if score <= bestScore {
			continue
		}
		bestScore = score
		best = model.ActionTarget{
			Selector: selector, Role: candidate.Role, Text: candidate.Text, Label: candidate.Label,
			TestID: candidate.TestID, ComponentRef: candidate.ComponentRef,
			SelectorAlternatives: formalBusinessSelectorAlternatives(candidate.Alternatives),
			EvidenceRefs:         candidate.EvidenceRefs,
		}
	}
	return best, bestScore >= 0
}

func businessStageResultTarget(stage model.BusinessStage) model.ActionTarget {
	bestIndex := -1
	for index, candidate := range stage.Targets {
		if !isExplicitNewProjectResultCandidate(candidate) {
			continue
		}
		if bestIndex < 0 || businessTargetRank(candidate) > businessTargetRank(stage.Targets[bestIndex]) {
			bestIndex = index
		}
	}
	if bestIndex >= 0 {
		candidate := stage.Targets[bestIndex]
		selector := selectorForBusinessTarget(candidate)
		if selector == "" {
			return model.ActionTarget{}
		}
		return model.ActionTarget{Selector: selector, Role: candidate.Role, Text: candidate.Text, Label: candidate.Label, TestID: candidate.TestID, ComponentRef: candidate.ComponentRef, SelectorAlternatives: formalBusinessSelectorAlternatives(candidate.Alternatives), EvidenceRefs: candidate.EvidenceRefs}
	}
	return model.ActionTarget{}
}

func isExplicitNewProjectResultCandidate(candidate model.BusinessTargetCandidate) bool {
	structure := strings.ToLower(strings.Join([]string{candidate.TestID, candidate.Selector, candidate.ComponentRef}, " "))
	if containsAnyNormalized(structure,
		"card-project-", "text-project-name-", "emoji-project-", "project-menu-",
		"dashboard-page", "project-list", "button-new-project", "rename-project", "delete-project",
	) {
		return false
	}
	if containsAnyNormalized(structure, "dialog-new-project", "new-project-dialog", "create-project-dialog", "new-project-modal", "create-project-modal") {
		return true
	}
	if graphActionTypeFromKind(candidate.Kind, candidate.Selector) != model.GraphActionFill || selectorLooksGeneric(candidate.Selector) {
		return false
	}
	if containsAnyNormalized(structure,
		"input-project-idea", "project-idea", "input-project-name", "project-name-input", "new-project-name", "create-project-name",
	) {
		return true
	}
	semantics := strings.ToLower(strings.Join([]string{candidate.Label, candidate.Text, candidate.Role}, " "))
	return containsAnyNormalized(semantics, "项目名称", "项目名", "project name", "project idea")
}

func businessStageStateAssertionKind(stage model.BusinessStage, action model.GraphActionType) string {
	if action == model.GraphActionNavigate {
		return "page_loaded"
	}
	if action == model.GraphActionPress {
		return "page_changed"
	}
	if stage.Kind == model.BusinessStageKindObserveProgress || stage.Kind == model.BusinessStageKindFinalObserve {
		return "observation"
	}
	return "dom_visible"
}

func severityForBusinessStage(stage model.BusinessStage, required bool) string {
	if required {
		return "blocking"
	}
	return "warning"
}

func businessStageWaitUntil(stage model.BusinessStage) string {
	for _, condition := range stage.Action.WaitConditions {
		if condition == "networkidle" || condition == "domcontentloaded" || condition == "load" {
			return condition
		}
	}
	return "domcontentloaded"
}

func urlForBusinessStage(stage model.BusinessStage, entryPoint string) string {
	if isHTTPURL(stage.EntryRoute) {
		return stage.EntryRoute
	}
	if isHTTPURL(entryPoint) && strings.HasPrefix(stage.EntryRoute, "/") {
		return strings.TrimRight(baseURL(entryPoint), "/") + stage.EntryRoute
	}
	return entryPoint
}

func businessStageKindIsCoreForGraph(kind model.BusinessStageKind) bool {
	switch kind {
	case model.BusinessStageKindBusinessAction, model.BusinessStageKindBusinessInput, model.BusinessStageKindModeSelection, model.BusinessStageKindBusinessSubmit, model.BusinessStageKindObserveProgress:
		return true
	default:
		return false
	}
}

func businessStageSuccessCriteria(plan *model.BusinessStagePlan) []string {
	out := []string{"业务阶段顺序必须与用户需求一致", "登录仅作为 session_setup 前置条件", "server browser agent 只能修正 selector/等待/探索路径"}
	if plan != nil {
		for _, stage := range plan.Stages {
			if stage.Action.SuccessState != "" {
				out = append(out, stage.Action.SuccessState)
			}
		}
	}
	return uniqueStrings(out)
}

func stagePlanObjective(plan *model.BusinessStagePlan) string {
	if plan == nil {
		return ""
	}
	parts := []string{}
	for _, stage := range plan.Stages {
		if stage.Kind == model.BusinessStageKindFinalObserve {
			continue
		}
		parts = append(parts, stage.Objective)
	}
	return strings.Join(nonEmptyStrings(parts...), "；")
}

func businessStageGraphStates(entryPoint string, plan *model.BusinessStagePlan, featureID string) []*model.GraphState {
	states := []*model.GraphState{{
		ID:          "state_product_entry",
		Name:        "产品入口已加载",
		Kind:        "page",
		URLPattern:  entryPoint,
		DOMHints:    []model.SelectorCandidate{{Kind: "css", Value: "body", Confidence: 0.6, Source: "business_stage_plan"}},
		FeatureRefs: []string{featureID},
	}}
	if plan == nil {
		return states
	}
	for _, stage := range plan.Stages {
		states = append(states, &model.GraphState{
			ID:           "state_" + stage.ID,
			Name:         firstNonEmpty(stage.Title, string(stage.RouteState)),
			Kind:         string(stage.RouteState),
			URLPattern:   firstNonEmpty(stage.EntryRoute, stage.ExpectedRouteAfterAction, entryPoint),
			FeatureRefs:  []string{featureID, stage.ID},
			EvidenceRefs: stage.EvidenceRefs,
		})
	}
	return states
}

func totalBusinessStageDurationMS(plan *model.BusinessStagePlan) int {
	total := 0
	if plan == nil {
		return total
	}
	for _, stage := range plan.Stages {
		total += stage.DurationMS
	}
	return total
}

func totalGraphNodeDurationMS(nodes []*model.GraphNode) int {
	total := 0
	for _, node := range nodes {
		if node != nil && node.DurationHintMS > 0 {
			total += node.DurationHintMS
		}
	}
	return total
}

func totalGraphNodeDurationSeconds(nodes []*model.GraphNode) int {
	return ceilDurationSeconds(totalGraphNodeDurationMS(nodes))
}

func ceilDurationSeconds(durationMS int) int {
	if durationMS <= 0 {
		return 0
	}
	return (durationMS + 999) / 1000
}

func demoIntentFromProjectContext(project *model.ProjectContext) *model.DemoIntentSpec {
	text := firstNonEmpty(project.ProductDescription, strings.Join(project.MustShow, " "), project.Name, "核心业务流程")
	goal := intentGoalFromText(text)
	if goal.Label == "" {
		goal = model.DemoIntentGoal{
			ID:               "intent_primary_business",
			Label:            "核心业务流程",
			Kind:             "business_action",
			Required:         true,
			BusinessCritical: true,
			TargetKeywords:   []string{"项目", "生成", "工作台", "project", "generate", "dashboard"},
			PreferredAction:  "click",
			SuccessState:     "目标业务状态可见",
			Confidence:       0.5,
		}
	}
	if !goal.BusinessCritical {
		goal.BusinessCritical = true
		goal.Required = true
		if goal.PreferredAction == "" || goal.PreferredAction == "inspect" {
			goal.PreferredAction = "click"
		}
	}
	return &model.DemoIntentSpec{
		ID:             "demo_intent_" + project.ID,
		ProjectID:      project.ID,
		SchemaVersion:  model.ProjectIntelligencePackSchemaVersion,
		Objective:      text,
		TargetAudience: project.TargetAudience,
		Goals:          []model.DemoIntentGoal{goal},
		Confidence:     0.52,
		CreatedAt:      time.Now().UTC(),
	}
}

func (a *GraphBuilderAgent) generateGraphFromVerifiedInteractions(ctx context.Context, project *model.ProjectContext, productMap *model.ProductMap, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack) (*model.DemoWorkflowGraph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plan := intelligence.VerifiedInteraction
	graphID := fmt.Sprintf("graph_%d", time.Now().UnixNano())
	entryPoint := scopedProductURL(project, intelligence.RunIntentScope, firstNonEmpty(plan.SourceURL, graphEntryPoint(project, productMap), project.ProductURL))
	if entryPoint == "" {
		entryPoint = "input://product_context"
	}
	featureID, featureValue := primaryFeature(productMap)
	featureValue = firstNonEmpty(project.ProductDescription, featureValue)
	useCase := model.DemoUseCaseLaunch
	if report != nil && report.RequirementBrief != nil && len(report.RequirementBrief.UseCases) > 0 {
		useCase = report.RequirementBrief.UseCases[0]
	} else if len(project.Goals) > 0 {
		useCase = project.Goals[0].UseCase
	}
	graph := model.NewDemoWorkflowGraph(graphID, project.ID, entryPoint)
	graph.Status = model.GraphStatusReviewReady
	graph.Name = "需求驱动的已验证演示流程"
	graph.Summary = "基于用户需求、代码证据和页面预扫描结果生成；页面证据不足时阻止脚本生成，避免访问控制面或无关控件。"
	graph.Intent = &model.WorkflowIntent{
		UseCase:            useCase,
		Audience:           primaryAudience(project),
		Objective:          featureValue,
		ValueProposition:   featureValue,
		PrimaryFeatureRefs: []string{featureID},
		SuccessCriteria:    verifiedSuccessCriteria(plan),
		CTA:                "请复核已验证动作、hash、安全策略后审批上传。",
	}
	graph.Requirements = requirementsFromProject(project)
	graph.Variables = append(graph.Variables, demoCredentialVariables(project)...)
	graph.Nodes = []*model.GraphNode{verifiedStartNode(project, entryPoint, featureID, reportEvidenceRefs(report))}
	businessNodeCount := 0
	for index, action := range plan.Actions {
		if !isURLAllowedByRunScope(intelligence.RunIntentScope, action.URL) || isControlPlaneSignal(intelligence.RunIntentScope, action.URL, action.Label, action.Selector) {
			continue
		}
		node := graphNodeFromVerifiedAction(project, action, index+1, featureID)
		if node != nil {
			graph.Nodes = append(graph.Nodes, node)
			if node.ActionSpec != nil && isBusinessAction(node.ActionSpec.Type) {
				businessNodeCount++
			}
		}
	}
	if businessNodeCount == 0 {
		return nil, errors.New("missing_product_evidence: no scoped product business action remained after filtering control-plane and chrome candidates")
	}
	graph.Nodes = append(graph.Nodes, verifiedCloseNode(project, featureID, reportEvidenceRefs(report)))
	graph.Edges = sequentialGraphEdges(graph.Nodes)
	graph.States = verifiedGraphStates(entryPoint, plan, featureID)
	graph.Validations = verifiedGraphValidations(entryPoint)
	graph.Narratives = []*model.NarrativeSegment{{
		ID:           "narrative_verified_workflow",
		NodeRefs:     nodeIDs(graph.Nodes),
		Title:        "需求驱动演示主线",
		Summary:      featureValue,
		Voiceover:    featureValue,
		Tone:         project.BrandTone,
		AudienceLens: project.TargetAudience,
		EvidenceRefs: uniqueEvidenceRefs(append(reportEvidenceRefs(report), plan.EvidenceRefs...)),
	}}
	graph.Assets = model.NewMVPAssetManifest()
	if totalMS := totalGraphNodeDurationMS(graph.Nodes); totalMS > 0 {
		graph.Assets.TargetDurationSec = ceilDurationSeconds(totalMS)
	}
	graph.Maintenance = &model.MaintenancePolicy{
		UpdateTriggers:   []string{"requirement_changed", "browser_scan_failed", "selector_validation_failed", "route_changed"},
		StalenessDays:    14,
		RegressionChecks: []string{"verify_interaction_plan", "rehearse_verified_workflow"},
	}
	graph.EvidenceRefs = uniqueEvidenceRefs(append(intelligenceEvidenceRefs(intelligence), plan.EvidenceRefs...))
	graph.Provenance = &model.GraphProvenance{
		CreatedBy:    "GraphBuilderAgent",
		Model:        "deterministic_verified_interaction_v1",
		ProductMapID: productMapID(productMap),
		EvidenceRefs: graph.EvidenceRefs,
	}
	if graph.Assets != nil {
		graph.Assets.Brand = project.BrandKit
		graph.Assets.Provenance = &model.AssetProvenance{WorkflowGraphID: graph.ID, GraphVersion: graph.Version, GeneratedBy: "GraphBuilderAgent"}
	}
	bindGraphRequirements(project, graph)
	return graph, nil
}

func verifiedStartNode(project *model.ProjectContext, entryPoint string, featureID string, evidence []model.EvidenceRef) *model.GraphNode {
	actionType := model.GraphActionInspect
	action := "inspect"
	target := model.ActionTarget{}
	selector := ""
	expected := "产品入口上下文可用于验证需求动作"
	if isHTTPURL(entryPoint) {
		actionType = model.GraphActionNavigate
		action = "navigate"
		target = model.ActionTarget{URL: entryPoint}
		selector = entryPoint
		expected = "产品入口页面加载完成"
	}
	return &model.GraphNode{
		ID:              "start",
		Action:          action,
		Selector:        selector,
		ExpectedOutcome: expected,
		IsScreenshot:    true,
		RetryPolicy:     2,
		Type:            model.GraphNodeTypeStart,
		Title:           "打开产品入口",
		Goal:            "进入页面并给后续已验证动作建立稳定上下文。",
		FeatureRefs:     []string{featureID},
		ActionSpec:      &model.GraphAction{Type: actionType, Target: target, TimeoutMS: 30000, WaitUntil: "domcontentloaded"},
		StateAfter: []model.StateAssertion{{
			ID:        "state_after_start_body_visible",
			Kind:      "dom_visible",
			Target:    model.ActionTarget{Selector: "body"},
			Operator:  "is_visible",
			Expected:  true,
			Required:  true,
			TimeoutMS: 10000,
		}},
		Validations: []model.ValidationSpec{{
			ID:        "node_start_loaded",
			Kind:      "page_loaded",
			Target:    target,
			Assertion: "产品入口可打开",
			Expected:  true,
			Severity:  "blocking",
			Required:  true,
		}},
		Narrative:     &model.NarrativeCue{Title: "从产品入口开始", Caption: "打开产品环境并等待页面稳定。", AudienceLens: project.TargetAudience},
		Capture:       &model.CaptureSpec{Screenshot: true, Video: true, AssetRole: "opening_context", MaskSelectors: maskSelectorsFromProject(project)},
		EvidenceRefs:  evidence,
		FailurePolicy: &model.NodeFailurePolicy{RetryAttempts: 2, RepairPolicy: &model.RepairPolicy{AllowSelectorRepair: true, AllowDataRepair: false, AllowStepSkip: false, MaxAttempts: 2}},
		Metadata:      map[string]any{"verification_status": "entry_navigation"},
	}
}

func demoCredentialVariables(project *model.ProjectContext) []model.GraphVariable {
	if project == nil || project.DemoAccount == nil {
		return nil
	}
	variables := []model.GraphVariable{}
	if project.DemoAccount.UsernameSecretRef != "" {
		variables = append(variables, model.GraphVariable{
			Name:      "demo_username",
			Kind:      "credential_username",
			SecretRef: project.DemoAccount.UsernameSecretRef,
			Required:  true,
			Sensitive: true,
		})
	}
	if project.DemoAccount.PasswordSecretRef != "" {
		variables = append(variables, model.GraphVariable{
			Name:      "demo_password",
			Kind:      "credential_password",
			SecretRef: project.DemoAccount.PasswordSecretRef,
			Required:  true,
			Sensitive: true,
		})
	}
	return variables
}

func graphNodeFromVerifiedAction(project *model.ProjectContext, action model.VerifiedInteractionAction, order int, featureID string) *model.GraphNode {
	selector := strings.TrimSpace(action.Selector)
	adaptive := action.VerificationStatus == "runtime_adaptive" && action.NonDestructive
	actionType := graphActionTypeFromKind(action.Kind, selector)
	if adaptive {
		actionType = declaredGraphActionType(action.Kind)
	}
	if selector != "" && businessActionNeedsExecutableSelector(actionType) && !selectorUsableForBusinessAction(selector) {
		if !adaptive {
			return nil
		}
		// Runtime-adaptive actions can be resolved from the approved semantic
		// target contract. Dropping a weak locator is safer than freezing it as
		// a misleading primary selector.
		selector = ""
	}
	if selector == "" && actionType != model.GraphActionWait && actionType != model.GraphActionInspect && !adaptive {
		return nil
	}
	sidecarUnavailable := action.VerificationStatus == "sidecar_unavailable"
	required := (action.IsBusiness || looksLikeLoginAction(action.Label, selector)) && !adaptive && !sidecarUnavailable
	severity := "warning"
	if required {
		severity = "blocking"
	}
	nodeID := safeID("verified", firstNonEmpty(action.ID, action.IntentGoalID, selector))
	target := model.ActionTarget{
		URL:                  action.URL,
		Selector:             selector,
		Source:               action.VerificationSource,
		Label:                action.Label,
		ComponentRef:         action.ComponentRef,
		SelectorAlternatives: action.Alternatives,
		EvidenceRefs:         action.EvidenceRefs,
	}
	return &model.GraphNode{
		ID:              nodeID,
		Action:          string(actionType),
		Selector:        selector,
		ExpectedOutcome: firstNonEmpty(action.ExpectedOutcome, action.SuccessState, "目标业务动作完成"),
		IsScreenshot:    true,
		HasZoom:         action.IsBusiness,
		RetryPolicy:     2,
		Type:            model.GraphNodeTypeAction,
		Title:           firstNonEmpty(action.Label, fmt.Sprintf("业务动作 %d", order)),
		Goal:            firstNonEmpty(action.SuccessState, "执行用户需求中对应的页面动作。"),
		PageRef:         action.RouteRef,
		FeatureRefs:     []string{featureID, action.IntentGoalID},
		ActionSpec: &model.GraphAction{
			Type:      actionType,
			Target:    target,
			Value:     action.InputValue,
			TimeoutMS: maxInt(action.DurationHintMS, 12000),
		},
		StateAfter: []model.StateAssertion{{
			ID:           "state_after_" + nodeID,
			Kind:         "dom_visible",
			Target:       target,
			Operator:     "is_visible",
			Expected:     true,
			Required:     required,
			TimeoutMS:    12000,
			EvidenceRefs: action.EvidenceRefs,
		}},
		Validations: []model.ValidationSpec{{
			ID:           "validate_" + nodeID,
			Kind:         firstNonEmpty(validationKindForInteraction(action), "verified_interaction"),
			Target:       target,
			Assertion:    firstNonEmpty(action.SuccessState, validationAssertionForInteraction(action)),
			Expected:     true,
			Severity:     severity,
			Required:     required,
			EvidenceRefs: action.EvidenceRefs,
			RepairPolicy: &model.RepairPolicy{AllowSelectorRepair: true, AllowDataRepair: false, AllowStepSkip: false, MaxAttempts: 2},
		}},
		Narrative: &model.NarrativeCue{
			Title:        firstNonEmpty(action.Label, "执行业务动作"),
			Voiceover:    firstNonEmpty(action.ExpectedOutcome, action.SuccessState, action.Label),
			Caption:      firstNonEmpty(action.ExpectedOutcome, action.SuccessState, action.Label),
			Callout:      firstNonEmpty(action.Label, "业务动作"),
			Tone:         project.BrandTone,
			AudienceLens: project.TargetAudience,
		},
		Capture: &model.CaptureSpec{
			Screenshot:    true,
			Video:         true,
			Zoom:          action.IsBusiness,
			Callout:       action.IsBusiness,
			FocusSelector: selector,
			AssetRole:     firstNonEmpty(assetRoleForInteraction(action), "verified_business_action"),
			MaskSelectors: maskSelectorsFromProject(project),
		},
		EvidenceRefs: action.EvidenceRefs,
		FailurePolicy: &model.NodeFailurePolicy{
			RetryAttempts: 2,
			RepairPolicy:  &model.RepairPolicy{AllowSelectorRepair: true, AllowDataRepair: false, AllowStepSkip: false, MaxAttempts: 2},
		},
		DurationHintMS: action.DurationHintMS,
		Tags:           uniqueStrings([]string{"verified_interaction", action.IntentGoalID, action.VerificationSource, action.VerificationStatus}),
		Metadata: map[string]any{
			"verified_interaction_id": action.ID,
			"intent_goal_id":          action.IntentGoalID,
			"verification_status":     action.VerificationStatus,
			"verification_source":     action.VerificationSource,
			"selector_score":          action.SelectorScore,
			"runtime_adaptive":        adaptive,
			"non_destructive":         action.NonDestructive,
			"sidecar_unavailable":     sidecarUnavailable,
		},
	}
}

func declaredGraphActionType(kind string) model.GraphActionType {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "click", "button", "cta":
		return model.GraphActionClick
	case "fill", "input", "type":
		return model.GraphActionFill
	case "select":
		return model.GraphActionSelect
	case "upload":
		return model.GraphActionUpload
	case "press", "keypress", "keyboard", "key_press":
		return model.GraphActionPress
	case "wait":
		return model.GraphActionWait
	case "navigate":
		return model.GraphActionNavigate
	case "api", "api_call":
		return model.GraphActionAPICall
	default:
		return model.GraphActionInspect
	}
}

func validationKindForInteraction(action model.VerifiedInteractionAction) string {
	if action.VerificationStatus == "runtime_adaptive" {
		return "runtime_adaptive_interaction"
	}
	if action.VerificationStatus == "sidecar_unavailable" {
		return "code_evidence_interaction"
	}
	return "verified_interaction"
}

func validationAssertionForInteraction(action model.VerifiedInteractionAction) string {
	if action.VerificationStatus == "runtime_adaptive" {
		return "运行时按需求目标和候选 selector 找到业务控件并执行"
	}
	if action.VerificationStatus == "sidecar_unavailable" {
		return "页面预扫描不可用，运行时按需求追踪后的代码 selector 证据执行"
	}
	return "页面预扫描确认 selector 可执行"
}

func assetRoleForInteraction(action model.VerifiedInteractionAction) string {
	if action.VerificationStatus == "runtime_adaptive" {
		return "adaptive_business_action"
	}
	if action.VerificationStatus == "sidecar_unavailable" {
		return "code_evidence_business_action"
	}
	return "verified_business_action"
}

func verifiedCloseNode(project *model.ProjectContext, featureID string, evidence []model.EvidenceRef) *model.GraphNode {
	return &model.GraphNode{
		ID:              "close",
		Action:          string(model.GraphActionInspect),
		Selector:        "",
		ExpectedOutcome: "已完成需求驱动的已验证演示路径",
		IsScreenshot:    true,
		RetryPolicy:     1,
		Type:            model.GraphNodeTypeEnd,
		Title:           "收束演示",
		Goal:            "停留在最终页面状态，给观众观察结果。",
		FeatureRefs:     []string{featureID},
		ActionSpec:      &model.GraphAction{Type: model.GraphActionInspect, Target: model.ActionTarget{}, TimeoutMS: 10000},
		Validations: []model.ValidationSpec{{
			ID:        "node_close_observe",
			Kind:      "observation",
			Assertion: "演示结束时保持页面上下文可观察",
			Expected:  true,
			Severity:  "warning",
			Required:  false,
		}},
		Narrative:     &model.NarrativeCue{Title: "展示最终状态", Caption: "保留最终页面状态，方便审阅结果。", AudienceLens: project.TargetAudience},
		Capture:       &model.CaptureSpec{Screenshot: true, Video: true, AssetRole: "closing_state", MaskSelectors: maskSelectorsFromProject(project)},
		EvidenceRefs:  evidence,
		FailurePolicy: &model.NodeFailurePolicy{RetryAttempts: 1},
		Metadata:      map[string]any{"verification_status": "non_blocking_observation"},
	}
}

func verifiedGraphStates(entryPoint string, plan *model.VerifiedInteractionPlan, featureID string) []*model.GraphState {
	states := []*model.GraphState{{
		ID:          "state_product_entry",
		Name:        "产品入口已加载",
		Kind:        "page",
		URLPattern:  entryPoint,
		DOMHints:    []model.SelectorCandidate{{Kind: "css", Value: "body", Confidence: 0.6, Source: "graph_builder"}},
		FeatureRefs: []string{featureID},
	}}
	for _, action := range plan.Actions {
		if action.Selector == "" {
			continue
		}
		states = append(states, &model.GraphState{
			ID:           "state_" + safeID("", action.ID),
			Name:         firstNonEmpty(action.SuccessState, action.Label, "已验证动作状态"),
			Kind:         "verified_interaction",
			URLPattern:   firstNonEmpty(action.URL, entryPoint),
			DOMHints:     []model.SelectorCandidate{{Kind: "css", Value: action.Selector, Confidence: 0.9, StabilityScore: float64(action.SelectorScore) / 100, Source: action.VerificationSource, LastValidatedAt: action.VerifiedAt, EvidenceRefs: action.EvidenceRefs}},
			FeatureRefs:  []string{featureID, action.IntentGoalID},
			EvidenceRefs: action.EvidenceRefs,
		})
	}
	return states
}

func verifiedGraphValidations(entryPoint string) []*model.ValidationSpec {
	return []*model.ValidationSpec{{
		ID:        "validate_entry_loaded",
		Kind:      "page_loaded",
		Target:    model.ActionTarget{URL: entryPoint},
		Assertion: "入口页面可打开",
		Expected:  true,
		Severity:  "blocking",
		Required:  true,
		RepairPolicy: &model.RepairPolicy{
			AllowSelectorRepair: true,
			AllowDataRepair:     false,
			AllowStepSkip:       false,
			MaxAttempts:         2,
		},
	}}
}

func verifiedSuccessCriteria(plan *model.VerifiedInteractionPlan) []string {
	criteria := []string{"入口页面可打开", "脚本只围绕需求目标生成业务动作", "所有业务动作来自已验证的产品页面证据"}
	if plan != nil {
		for _, action := range plan.Actions {
			if action.IsBusiness {
				criteria = append(criteria, firstNonEmpty(action.SuccessState, action.ExpectedOutcome, action.Label))
			}
		}
	}
	return uniqueStrings(criteria)
}

func reportRequirementBrief(report *model.MultimodalUnderstandingReport) *model.RequirementBrief {
	if report == nil {
		return nil
	}
	return report.RequirementBrief
}

func productMapID(productMap *model.ProductMap) string {
	if productMap == nil {
		return ""
	}
	return productMap.ID
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

func (a *GraphBuilderAgent) enhanceGraphWithLLM(ctx context.Context, project *model.ProjectContext, productMap *model.ProductMap, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack, graph *model.DemoWorkflowGraph) (*llm.CallTrace, error) {
	if a.llm == nil || project == nil || graph == nil {
		return nil, nil
	}
	payload := map[string]any{
		"target_audience":      project.TargetAudience,
		"product_url":          project.ProductURL,
		"product_map":          productMap,
		"project_intelligence": compactGraphProjectIntelligence(intelligence),
		"report_summary":       "",
		"brief":                nil,
		"current_graph":        graphPatchInput(graph),
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
		graph.Intent.SuccessCriteria = uniqueStrings(append(graph.Intent.SuccessCriteria, stringSlice(output.SuccessCriteria)...))
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
		node.Tags = uniqueStrings(append(node.Tags, stringSlice(step.Tags)...))
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

func augmentGraphWithIntelligence(graph *model.DemoWorkflowGraph, project *model.ProjectContext, productMap *model.ProductMap, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack) {
	if graph == nil || len(graph.Nodes) < 3 {
		return
	}
	if graph.Assets == nil {
		graph.Assets = model.NewMVPAssetManifest()
	}
	if intelligence != nil && intelligence.ScriptReadinessReport != nil && intelligence.ScriptReadinessReport.SuggestedTargetDurationSec > 0 {
		graph.Assets.TargetDurationSec = maxInt(graph.Assets.TargetDurationSec, intelligence.ScriptReadinessReport.SuggestedTargetDurationSec)
	}
	if graph.Assets.TargetDurationSec <= 0 {
		graph.Assets.TargetDurationSec = totalGraphNodeDurationSeconds(graph.Nodes)
	}

	startNode := graph.Nodes[0]
	primaryNode := graph.Nodes[1]
	closeNode := graph.Nodes[len(graph.Nodes)-1]
	ensureNodeDuration(startNode, 12000)
	ensureNodeDuration(primaryNode, 12000)
	ensureNodeDuration(closeNode, 10000)

	nodes := []*model.GraphNode{startNode}
	if node := stabilizationGraphNode(project, intelligence); node != nil {
		nodes = append(nodes, node)
	}
	nodes = append(nodes, primaryNode)
	if node := supportingGraphNode(project, productMap, report, intelligence); node != nil {
		nodes = append(nodes, node)
	}
	nodes = append(nodes, closeNode)
	graph.Nodes = nodes
	graph.Edges = sequentialGraphEdges(nodes)
	if len(graph.Narratives) > 0 {
		graph.Narratives[0].NodeRefs = nodeIDs(nodes)
	}
}

func stabilizationGraphNode(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack) *model.GraphNode {
	selector := ""
	title := "等待页面稳定"
	caption := "等待页面稳定，给观众一个自然的观察窗口。"
	evidence := intelligenceEvidenceRefs(intelligence)
	if surface := primarySurfaceFromIntelligence(intelligence); surface != nil {
		title = firstNonEmpty(surface.Title, title)
		caption = firstNonEmpty(surface.PageRole, caption)
		if len(surface.StableSelectors) > 0 && surface.StableSelectors[0].Value != "" {
			selector = surface.StableSelectors[0].Value
		}
		evidence = uniqueEvidenceRefs(append(evidence, surface.EvidenceRefs...))
	}
	node := &model.GraphNode{
		ID:              "stabilize_entry",
		Action:          string(model.GraphActionWait),
		Selector:        selector,
		ExpectedOutcome: "页面稳定后再继续演示",
		IsScreenshot:    true,
		HasZoom:         false,
		RetryPolicy:     1,
		Type:            model.GraphNodeTypeAction,
		Title:           title,
		Goal:            "给录制留出稳定过渡，避免画面闪烁。",
		FeatureRefs:     nodeFeatureRefsFromProjectIntelligence(intelligence, project),
		ActionSpec: &model.GraphAction{
			Type:      model.GraphActionWait,
			Target:    model.ActionTarget{Selector: selector, EvidenceRefs: evidence},
			TimeoutMS: 12000,
			WaitUntil: "domcontentloaded",
		},
		Validations: []model.ValidationSpec{{
			ID:        "validate_stabilize_entry",
			Kind:      "page_stable",
			Target:    model.ActionTarget{Selector: selector, EvidenceRefs: evidence},
			Assertion: "页面已稳定，适合继续录制",
			Expected:  true,
			Severity:  "warning",
			Required:  true,
		}},
		Narrative: &model.NarrativeCue{
			Title:        "稳定过渡",
			Caption:      caption,
			AudienceLens: project.TargetAudience,
		},
		Capture:      &model.CaptureSpec{Screenshot: true, Video: true, AssetRole: "stabilization", MaskSelectors: maskSelectorsFromProject(project)},
		EvidenceRefs: evidence,
		FailurePolicy: &model.NodeFailurePolicy{
			RetryAttempts: 1,
		},
	}
	return node
}

type supportingActionCandidate struct {
	Label           string
	Kind            string
	Selector        string
	ExpectedOutcome string
	Caption         string
	Callout         string
	EvidenceRefs    []model.EvidenceRef
}

func supportingGraphNode(project *model.ProjectContext, productMap *model.ProductMap, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack) *model.GraphNode {
	candidate := secondaryActionCandidate(productMap, report, intelligence)
	if candidate == nil {
		return nil
	}
	selector := candidate.Selector
	evidence := uniqueEvidenceRefs(append(candidate.EvidenceRefs, intelligenceEvidenceRefs(intelligence)...))
	node := &model.GraphNode{
		ID:              "supporting_capability",
		Action:          string(graphActionTypeFromKind(candidate.Kind, selector)),
		Selector:        selector,
		ExpectedOutcome: firstNonEmpty(candidate.ExpectedOutcome, "辅助能力可见"),
		IsScreenshot:    true,
		HasZoom:         true,
		RetryPolicy:     2,
		Type:            model.GraphNodeTypeCapture,
		Title:           firstNonEmpty(candidate.Label, "展开辅助能力"),
		Goal:            "在核心价值之后再展开一个补充能力，形成更自然的人类式演示节奏。",
		FeatureRefs:     nodeFeatureRefsFromProductMap(productMap, intelligence),
		ActionSpec: &model.GraphAction{
			Type:      graphActionTypeFromKind(candidate.Kind, selector),
			Target:    model.ActionTarget{Selector: selector, EvidenceRefs: evidence},
			TimeoutMS: 12000,
		},
		Validations: []model.ValidationSpec{{
			ID:        "validate_supporting_capability",
			Kind:      "surface_visible",
			Target:    model.ActionTarget{Selector: selector, EvidenceRefs: evidence},
			Assertion: "补充能力可见且可录制",
			Expected:  true,
			Severity:  "warning",
			Required:  false,
		}},
		Narrative: &model.NarrativeCue{
			Title:        "展开补充能力",
			Caption:      firstNonEmpty(candidate.Caption, candidate.ExpectedOutcome, "展示一个次级功能点。"),
			Callout:      firstNonEmpty(candidate.Callout, "辅助能力"),
			AudienceLens: project.TargetAudience,
		},
		Capture:      &model.CaptureSpec{Screenshot: true, Video: true, Zoom: true, Callout: true, FocusSelector: selector, AssetRole: "supporting_capability", MaskSelectors: maskSelectorsFromProject(project)},
		EvidenceRefs: evidence,
		FailurePolicy: &model.NodeFailurePolicy{
			RetryAttempts: 2,
		},
	}
	return node
}

func secondaryActionCandidate(productMap *model.ProductMap, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack) *supportingActionCandidate {
	if intelligence != nil {
		if action, ok := bestIntelligenceAction(intelligence, true); ok {
			return &supportingActionCandidate{
				Label:           action.Label,
				Kind:            action.Kind,
				Selector:        action.Selector,
				ExpectedOutcome: action.ExpectedOutcome,
				Caption:         action.ExpectedOutcome,
				Callout:         "辅助能力",
				EvidenceRefs:    action.EvidenceRefs,
			}
		}
		for _, capability := range intelligence.FeatureCapabilities {
			if len(capability.KeyActions) == 0 && len(capability.SupportingPageRefs) == 0 && len(capability.SupportingRouteRefs) == 0 {
				continue
			}
			return &supportingActionCandidate{
				Label:           capability.Name,
				Kind:            firstString(capability.KeyActions, "inspect"),
				Selector:        firstSelectorFromIntelligence(intelligence),
				ExpectedOutcome: firstNonEmpty(capability.UserValue, capability.BusinessValue, "辅助能力可见"),
				Caption:         firstNonEmpty(capability.UserValue, capability.BusinessValue, capability.Name),
				Callout:         firstNonEmpty(capability.Kind, "辅助能力"),
				EvidenceRefs:    capability.EvidenceRefs,
			}
		}
	}
	if productMap != nil {
		for _, page := range productMap.Pages {
			if page == nil {
				continue
			}
			for _, action := range page.PrimaryActions {
				return &supportingActionCandidate{
					Label:           firstNonEmpty(action.Label, page.Title),
					Kind:            firstNonEmpty(action.Kind, "inspect"),
					Selector:        action.Selector,
					ExpectedOutcome: firstNonEmpty(page.Purpose, "补充功能可见"),
					Caption:         firstNonEmpty(page.Purpose, page.Title),
					Callout:         "补充能力",
					EvidenceRefs:    action.EvidenceRefs,
				}
			}
		}
	}
	if report != nil {
		for _, page := range report.PageSnapshots {
			for _, action := range page.Actions {
				return &supportingActionCandidate{
					Label:           firstNonEmpty(action.Label, page.Title),
					Kind:            firstNonEmpty(action.Kind, "inspect"),
					Selector:        action.SelectorHint,
					ExpectedOutcome: firstNonEmpty(page.VisionSummary, "补充页面区域可见"),
					Caption:         firstNonEmpty(page.VisionSummary, page.Title),
					Callout:         "补充能力",
					EvidenceRefs:    action.EvidenceRefs,
				}
			}
		}
	}
	return nil
}

func sequentialGraphEdges(nodes []*model.GraphNode) []*model.GraphEdge {
	edges := []*model.GraphEdge{}
	for i := 0; i < len(nodes)-1; i++ {
		from := nodes[i]
		to := nodes[i+1]
		if from == nil || to == nil {
			continue
		}
		edges = append(edges, &model.GraphEdge{
			ID:        fmt.Sprintf("e%d", i+1),
			FromNode:  from.ID,
			ToNode:    to.ID,
			Condition: "validated",
			ConditionSpec: &model.EdgeCondition{
				Kind:      "validation_passed",
				PassState: to.ID,
			},
			Priority: 1,
		})
	}
	return edges
}

func ensureNodeDuration(node *model.GraphNode, minMS int) {
	if node == nil {
		return
	}
	if node.ActionSpec != nil && node.ActionSpec.TimeoutMS > 0 && node.ActionSpec.TimeoutMS < minMS {
		node.ActionSpec.TimeoutMS = minMS
	}
}

func severityForAction(action model.GraphActionType) string {
	if action == model.GraphActionInspect || action == model.GraphActionWait {
		return "warning"
	}
	return "blocking"
}

func primarySurfaceFromIntelligence(intelligence *model.ProjectIntelligencePack) *model.InteractionSurface {
	if intelligence == nil {
		return nil
	}
	for i := range intelligence.InteractionSurfaces {
		return &intelligence.InteractionSurfaces[i]
	}
	return nil
}

func nodeIDs(nodes []*model.GraphNode) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node != nil && node.ID != "" {
			ids = append(ids, node.ID)
		}
	}
	return ids
}

func firstString(values []string, fallback string) string {
	if len(values) == 0 {
		return fallback
	}
	return values[0]
}

func firstSelectorFromIntelligence(intelligence *model.ProjectIntelligencePack) string {
	if intelligence != nil {
		for _, surface := range intelligence.InteractionSurfaces {
			if selector := bestSelectorCandidate(surface.StableSelectors); selector != "" {
				return selector
			}
			for _, action := range surface.Actions {
				if selectorUsableForBusinessAction(action.Selector) {
					return action.Selector
				}
			}
		}
	}
	return ""
}

func nodeFeatureRefsFromProjectIntelligence(intelligence *model.ProjectIntelligencePack, project *model.ProjectContext) []string {
	if intelligence == nil {
		return []string{"feature_primary_value"}
	}
	refs := []string{}
	for _, feature := range intelligence.FeatureCapabilities {
		if feature.ID != "" {
			refs = append(refs, feature.ID)
		}
	}
	if len(refs) == 0 {
		return []string{"feature_primary_value"}
	}
	return uniqueStrings(refs)
}

func nodeFeatureRefsFromProductMap(productMap *model.ProductMap, intelligence *model.ProjectIntelligencePack) []string {
	refs := []string{}
	if productMap != nil {
		for _, feature := range productMap.Features {
			if feature != nil && feature.ID != "" {
				refs = append(refs, feature.ID)
			}
		}
	}
	if len(refs) == 0 && intelligence != nil {
		for _, feature := range intelligence.FeatureCapabilities {
			if feature.ID != "" {
				refs = append(refs, feature.ID)
			}
		}
	}
	if len(refs) == 0 {
		return []string{"feature_primary_value"}
	}
	return uniqueStrings(refs)
}

func intelligenceEvidenceRefs(intelligence *model.ProjectIntelligencePack) []model.EvidenceRef {
	if intelligence == nil {
		return nil
	}
	return intelligence.EvidenceRefs
}

func compactGraphProjectIntelligence(intelligence *model.ProjectIntelligencePack) map[string]any {
	if intelligence == nil {
		return nil
	}
	return map[string]any{
		"architecture_summary": func() string {
			if intelligence.Architecture != nil {
				return intelligence.Architecture.Summary
			}
			return ""
		}(),
		"feature_count":  len(intelligence.FeatureCapabilities),
		"surface_count":  len(intelligence.InteractionSurfaces),
		"scenario_count": len(intelligence.DemoScenarioPlans),
		"script_ready":   intelligence.ScriptReadinessReport != nil && intelligence.ScriptReadinessReport.CanProceed,
		"target_duration_sec": func() int {
			if intelligence.ScriptReadinessReport != nil {
				return intelligence.ScriptReadinessReport.SuggestedTargetDurationSec
			}
			return 0
		}(),
		"confidence": intelligence.Confidence,
	}
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
	case model.GraphActionPress:
		return model.GraphActionPress
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

func primaryPageAction(productMap *model.ProductMap, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack) graphPrimaryAction {
	if intelligence != nil {
		if action, ok := bestIntelligenceAction(intelligence, false); ok {
			return action
		}
	}
	if productMap != nil {
		if action, ok := bestProductMapAction(productMap, false); ok {
			return action
		}
		if action, ok := bestProductMapAction(productMap, true); ok {
			return action
		}
	}
	if report != nil {
		if action, ok := bestPageSnapshotAction(report, false); ok {
			return action
		}
		if action, ok := bestPageSnapshotAction(report, true); ok {
			return action
		}
	}
	if intelligence != nil {
		if action, ok := bestIntelligenceAction(intelligence, true); ok {
			return action
		}
	}
	return graphPrimaryAction{
		Label:           "展示核心产品价值",
		Kind:            "inspect",
		ExpectedOutcome: "缺少可执行页面动作证据，当前只能观察入口页面",
	}
}

func bestIntelligenceAction(intelligence *model.ProjectIntelligencePack, allowObservation bool) (graphPrimaryAction, bool) {
	if intelligence == nil {
		return graphPrimaryAction{}, false
	}
	var best *graphPrimaryAction
	bestScore := -1
	for _, surface := range intelligence.InteractionSurfaces {
		for _, action := range surface.Actions {
			if !actionAllowedForPass(action.Kind, action.Label, action.Selector, "business", allowObservation) &&
				!actionAllowedForPass(action.Kind, action.Label, action.Selector, "any", allowObservation) {
				continue
			}
			actionType := graphActionTypeFromKind(action.Kind, action.Selector)
			score := selectorQualityScore(action.Selector)
			if isBusinessAction(actionType) {
				score += 100
			}
			if looksLikeLoginAction(action.Label, action.Selector) {
				score -= 80
			}
			if score <= bestScore {
				continue
			}
			target := model.ActionTarget{
				URL:          firstNonEmpty(action.TargetRoute, surface.URL),
				Selector:     action.Selector,
				Label:        action.Label,
				ComponentRef: action.ID,
				EvidenceRefs: uniqueEvidenceRefs(append(action.EvidenceRefs, surface.EvidenceRefs...)),
			}
			candidate := graphPrimaryAction{
				Label:           firstNonEmpty(action.Label, surface.Title, "展示核心产品价值"),
				Kind:            firstNonEmpty(action.Kind, "inspect"),
				Selector:        target.Selector,
				Target:          target,
				ExpectedOutcome: firstNonEmpty(surface.PageRole, "项目智能图谱识别的关键动作可见"),
				EvidenceRefs:    target.EvidenceRefs,
			}
			best = &candidate
			bestScore = score
		}
	}
	if best == nil {
		return graphPrimaryAction{}, false
	}
	return *best, true
}

func bestProductMapAction(productMap *model.ProductMap, allowObservation bool) (graphPrimaryAction, bool) {
	if productMap == nil {
		return graphPrimaryAction{}, false
	}
	for _, pass := range []string{"business", "login", "any"} {
		for _, page := range productMap.Pages {
			if page == nil {
				continue
			}
			for _, action := range page.PrimaryActions {
				if !actionAllowedForPass(action.Kind, action.Label, action.Selector, pass, allowObservation) {
					continue
				}
				target := model.ActionTarget{
					URL:          action.TargetRoute,
					Selector:     action.Selector,
					Label:        action.Label,
					ComponentRef: action.ID,
					EvidenceRefs: action.EvidenceRefs,
				}
				return graphPrimaryAction{
					Label:           firstNonEmpty(action.Label, "展示核心产品价值"),
					Kind:            firstNonEmpty(action.Kind, "inspect"),
					Selector:        target.Selector,
					Target:          target,
					ExpectedOutcome: "关键动作或页面区域可见",
					EvidenceRefs:    action.EvidenceRefs,
				}, true
			}
		}
	}
	return graphPrimaryAction{}, false
}

func bestPageSnapshotAction(report *model.MultimodalUnderstandingReport, allowObservation bool) (graphPrimaryAction, bool) {
	if report == nil {
		return graphPrimaryAction{}, false
	}
	for _, pass := range []string{"business", "login", "any"} {
		for _, page := range report.PageSnapshots {
			for _, action := range page.Actions {
				if !actionAllowedForPass(action.Kind, action.Label, action.SelectorHint, pass, allowObservation) {
					continue
				}
				target := model.ActionTarget{
					URL:          action.TargetURL,
					Selector:     action.SelectorHint,
					Label:        action.Label,
					EvidenceRefs: action.EvidenceRefs,
				}
				return graphPrimaryAction{
					Label:           firstNonEmpty(action.Label, "展示页面关键动作"),
					Kind:            firstNonEmpty(action.Kind, "inspect"),
					Selector:        target.Selector,
					Target:          target,
					ExpectedOutcome: firstNonEmpty(page.VisionSummary, "页面关键动作可见"),
					EvidenceRefs:    action.EvidenceRefs,
				}, true
			}
		}
	}
	return graphPrimaryAction{}, false
}

func actionAllowedForPass(kind string, label string, selector string, pass string, allowObservation bool) bool {
	if actionLooksLikeChromeControl(label, selector) {
		return false
	}
	actionType := graphActionTypeFromKind(kind, selector)
	business := isBusinessAction(actionType)
	if !allowObservation && !business {
		return false
	}
	login := looksLikeLoginAction(label, selector)
	switch pass {
	case "business":
		return !login && business
	case "login":
		return login && business
	default:
		if business {
			return true
		}
		return allowObservation && !login && strings.TrimSpace(selector) != ""
	}
}

func looksLikeLoginAction(values ...string) bool {
	joined := strings.ToLower(strings.Join(values, " "))
	return strings.Contains(joined, "login") ||
		strings.Contains(joined, "signin") ||
		strings.Contains(joined, "sign-in") ||
		strings.Contains(joined, "password") ||
		strings.Contains(joined, "username")
}

func graphActionTypeFromKind(kind string, selector string) model.GraphActionType {
	normalized := strings.ToLower(strings.TrimSpace(kind))
	switch normalized {
	case "click", "button", "cta":
		if !selectorUsableForBusinessAction(selector) {
			return model.GraphActionInspect
		}
		return model.GraphActionClick
	case "fill", "input", "type":
		if !selectorUsableForBusinessAction(selector) {
			return model.GraphActionInspect
		}
		return model.GraphActionFill
	case "select":
		if !selectorUsableForBusinessAction(selector) {
			return model.GraphActionInspect
		}
		return model.GraphActionSelect
	case "upload":
		if !selectorUsableForBusinessAction(selector) {
			return model.GraphActionInspect
		}
		return model.GraphActionUpload
	case "press", "keypress", "keyboard", "key_press":
		return model.GraphActionPress
	case "wait":
		return model.GraphActionWait
	case "assert", "validate":
		if !selectorUsableForBlockingAssertion(selector) {
			return model.GraphActionInspect
		}
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
	if project == nil {
		return nil
	}
	if project.Inputs != nil && len(project.Inputs.Requirements) > 0 {
		out := make([]model.GraphRequirement, 0, len(project.Inputs.Requirements))
		for _, item := range project.Inputs.Requirements {
			evidence := append([]model.EvidenceRef{}, item.EvidenceRefs...)
			if len(evidence) == 0 {
				evidence = []model.EvidenceRef{requirementInputEvidence(item.ID)}
			}
			out = append(out, model.GraphRequirement{ID: item.ID, Kind: item.Kind, Description: item.Description, Required: item.Kind == "must_show" && item.Required, EvidenceRefs: evidence})
		}
		return out
	}
	requirements := make([]model.GraphRequirement, 0, len(project.MustShow)+len(project.MustNotShow)+len(project.ForbiddenPages)+len(project.ForbiddenData))
	for i, item := range project.MustShow {
		requirements = append(requirements, model.GraphRequirement{
			ID:           stableLegacyRequirementID("must_show", item, i),
			Kind:         "must_show",
			Description:  item,
			Required:     true,
			EvidenceRefs: []model.EvidenceRef{requirementInputEvidence(stableLegacyRequirementID("must_show", item, i))},
		})
	}
	for i, item := range project.MustNotShow {
		requirements = append(requirements, model.GraphRequirement{
			ID:          fmt.Sprintf("must_not_show_%d", i+1),
			Kind:        "must_not_show",
			Description: item,
			Required:    false,
		})
	}
	for i, item := range project.ForbiddenPages {
		requirements = append(requirements, model.GraphRequirement{
			ID:          fmt.Sprintf("forbidden_page_%d", i+1),
			Kind:        "forbidden_page",
			Description: item,
			Required:    false,
		})
	}
	for i, item := range project.ForbiddenData {
		requirements = append(requirements, model.GraphRequirement{
			ID:          fmt.Sprintf("forbidden_data_%d", i+1),
			Kind:        "forbidden_data",
			Description: item,
			// Forbidden data is a safety constraint, never positive business
			// coverage.  It is enforced by the security policy validator.
			Required: false,
		})
	}
	return requirements
}

func stableLegacyRequirementID(kind, description string, index int) string {
	digest := strings.TrimPrefix(model.SHA256Hex([]byte(kind+"\x00"+strings.TrimSpace(description))), "sha256:")
	if len(digest) >= 12 {
		return "requirement_" + kind + "_" + digest[:12]
	}
	return fmt.Sprintf("%s_%d", kind, index+1)
}

func requirementInputEvidence(requirementID string) model.EvidenceRef {
	return model.EvidenceRef{ID: "ev_" + requirementID, Kind: model.EvidenceKindUserInput, Summary: "用户明确提交的结构化演示要求", FieldPath: "project_input_bundle.requirements." + requirementID, Confidence: 1}
}

// RebindGraphRequirementsForPackage upgrades legacy projects on an in-memory
// package clone without mutating the authoritative saved graph.
func RebindGraphRequirementsForPackage(project *model.ProjectContext, graph *model.DemoWorkflowGraph) {
	bindGraphRequirements(project, graph)
}

func bindGraphRequirements(project *model.ProjectContext, graph *model.DemoWorkflowGraph) {
	if graph == nil {
		return
	}
	graph.Requirements = requirementsFromProject(project)
	for index := range graph.Requirements {
		requirement := &graph.Requirements[index]
		requirement.NodeRefs = cleanGraphNodeRefs(requirement.NodeRefs, graph)
		if !requirement.Required || requirement.Kind != "must_show" {
			requirement.NodeRefs = nil
			continue
		}
		keywords := intentKeywordsForText(requirement.Description)
		preferredKinds := requirementStageKinds(requirement.Description)
		bestScore := 0
		var best *model.GraphNode
		for _, node := range graph.Nodes {
			if node == nil || !graphNodeHasRequiredValidation(node) {
				continue
			}
			stageKind, hasStageKind := graphNodeBusinessStageKind(node)
			stageKindScore := preferredKinds[stageKind]
			if len(preferredKinds) > 0 && (!hasStageKind || stageKindScore == 0) {
				continue
			}
			refs := graphNodeEvidenceRefs(node)
			if len(refs) == 0 && !graphNodeHasPlanBackedRuntimeContract(node) {
				continue
			}
			text := strings.Join([]string{node.ID, node.Title, node.Goal, node.Description, node.Action, node.InputData, node.ExpectedOutcome, narrativeCaption(node), narrativeCallout(node)}, " ")
			score := keywordMatchScore(keywords, text)
			score += stageKindScore
			if score > bestScore {
				bestScore, best = score, node
			}
		}
		if best != nil && bestScore > 0 {
			requirement.NodeRefs = []string{best.ID}
			requirement.EvidenceRefs = uniqueEvidenceRefs(append(requirement.EvidenceRefs, graphNodeEvidenceRefs(best)...))
		}
	}
}

func graphNodeHasPlanBackedRuntimeContract(node *model.GraphNode) bool {
	if node == nil || node.Metadata == nil || node.Metadata["runtime_adaptive"] != true || node.ActionSpec == nil || node.Capture == nil {
		return false
	}
	nonDestructive, ok := node.Metadata["non_destructive"].(bool)
	if !ok || !nonDestructive || strings.TrimSpace(node.ExpectedOutcome) == "" || (!node.Capture.Screenshot && !node.Capture.Video) {
		return false
	}
	hasRoute := strings.TrimSpace(node.PageRef) != "" || strings.TrimSpace(node.ActionSpec.Target.URL) != ""
	hasSemanticTarget := strings.TrimSpace(node.ActionSpec.Target.Selector) != "" || strings.TrimSpace(node.ActionSpec.Target.TestID) != "" || strings.TrimSpace(node.ActionSpec.Target.Role) != "" || strings.TrimSpace(node.ActionSpec.Target.Label) != "" || strings.TrimSpace(node.ActionSpec.Target.Text) != "" || strings.TrimSpace(node.Title) != "" || strings.TrimSpace(node.Goal) != ""
	return hasRoute && hasSemanticTarget && graphNodeHasRequiredValidation(node)
}

func cleanGraphNodeRefs(values []string, graph *model.DemoWorkflowGraph) []string {
	valid := map[string]bool{}
	if graph != nil {
		for _, node := range graph.Nodes {
			if node != nil && strings.TrimSpace(node.ID) != "" {
				valid[node.ID] = true
			}
		}
	}
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] || !valid[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func graphNodeBusinessStageKind(node *model.GraphNode) (model.BusinessStageKind, bool) {
	if node == nil || node.Metadata == nil {
		return "", false
	}
	value, ok := node.Metadata["business_stage_kind"].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", false
	}
	return model.BusinessStageKind(value), true
}

func requirementStageKinds(description string) map[model.BusinessStageKind]int {
	match := func(values ...string) bool { return containsAnyNormalized(description, values...) }
	kinds := map[model.BusinessStageKind]int{}
	switch {
	case match("登录", "登入", "sign in", "signin", "login", "authenticated", "authentication"):
		kinds[model.BusinessStageKindSessionSetup] = 120
	case match("按左", "按右", "按下", "旋转", "方向键", "键盘", "方块位置", "方块形状", "keyboard", "arrowleft", "arrowright", "arrowdown", "arrowup"):
		kinds[model.BusinessStageKindFinalObserve] = 160
	case match("最终预览", "棋盘", "得分", "操作说明", "试玩", "可玩", "tetris", "board", "score", "controls", "playable"):
		kinds[model.BusinessStageKindFinalObserve] = 150
	case match("最多", "至多", "不超过", "最长", "超时", "构建完成", "全部步骤完成", "编写完", "maximum", "timeout", "wait until complete"):
		kinds[model.BusinessStageKindFinalObserve] = 140
		kinds[model.BusinessStageKindObserveProgress] = 110
	case match("最终实际", "实际效果", "运行效果", "最终效果", "最终结果", "成品", "actual result", "final result", "final output", "working result"):
		kinds[model.BusinessStageKindFinalObserve] = 120
		kinds[model.BusinessStageKindObserveProgress] = 100
	case match("进度", "编程过程", "构建过程", "执行过程", "等待 agent", "progress", "building", "running"):
		kinds[model.BusinessStageKindObserveProgress] = 120
	case match("agent 已启动", "agent已启动", "启动 agent", "启动agent", "开始 agent", "开始agent", "开始编程", "启动编程", "start agent", "agent started", "start build", "build started"):
		kinds[model.BusinessStageKindBusinessSubmit] = 120
	case match("新建项目", "创建项目", "新增项目", "new project", "create project"):
		kinds[model.BusinessStageKindBusinessAction] = 120
		kinds[model.BusinessStageKindBusinessInput] = 100
	case match("项目名", "项目名称", "填写", "输入", "project name", "enter name", "fill"):
		kinds[model.BusinessStageKindBusinessInput] = 120
	case match("模式", "mode", "选择"):
		kinds[model.BusinessStageKindModeSelection] = 120
	}
	return kinds
}

func narrativeCaption(node *model.GraphNode) string {
	if node == nil || node.Narrative == nil {
		return ""
	}
	return node.Narrative.Caption
}

func narrativeCallout(node *model.GraphNode) string {
	if node == nil || node.Narrative == nil {
		return ""
	}
	return node.Narrative.Callout
}

func graphNodeHasRequiredValidation(node *model.GraphNode) bool {
	for _, validation := range node.Validations {
		if validation.Required {
			return true
		}
	}
	return false
}

func graphNodeEvidenceRefs(node *model.GraphNode) []model.EvidenceRef {
	if node == nil {
		return nil
	}
	refs := append([]model.EvidenceRef{}, node.EvidenceRefs...)
	if node.ActionSpec != nil {
		refs = append(refs, node.ActionSpec.Target.EvidenceRefs...)
	}
	for _, validation := range node.Validations {
		refs = append(refs, validation.EvidenceRefs...)
	}
	return uniqueEvidenceRefs(refs)
}
