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
	if intelligence.DemoIntent != nil {
		featureValue = firstNonEmpty(intelligence.DemoIntent.Objective, featureValue)
	}
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
		ValueProposition:   firstNonEmpty(primaryOutcomeFromBrief(reportRequirementBrief(report)), featureValue),
		PrimaryFeatureRefs: []string{featureID},
		SuccessCriteria:    verifiedSuccessCriteria(plan),
		CTA:                "请复核已验证动作、hash、安全策略后审批上传。",
	}
	graph.Requirements = append(requirementsFromProject(project), graphRequirementsFromIntent(intelligence.DemoIntent)...)
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
	graph.Assets.TargetDurationSec = maxInt(len(graph.Nodes)*12, 60)
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
		ActionSpec:      &model.GraphAction{Type: actionType, Target: target, TimeoutMS: 30000, WaitUntil: "networkidle"},
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
		Narrative:      &model.NarrativeCue{Title: "从产品入口开始", Caption: "打开产品环境并等待页面稳定。", AudienceLens: project.TargetAudience},
		Capture:        &model.CaptureSpec{Screenshot: true, Video: true, AssetRole: "opening_context", MaskSelectors: maskSelectorsFromProject(project)},
		EvidenceRefs:   evidence,
		FailurePolicy:  &model.NodeFailurePolicy{RetryAttempts: 2, RepairPolicy: &model.RepairPolicy{AllowSelectorRepair: true, AllowDataRepair: false, AllowStepSkip: false, MaxAttempts: 2}},
		DurationHintMS: 12000,
		Metadata:       map[string]any{"verification_status": "entry_navigation"},
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
	actionType := graphActionTypeFromKind(action.Kind, selector)
	if selector == "" && actionType != model.GraphActionWait && actionType != model.GraphActionInspect {
		return nil
	}
	if businessActionNeedsExecutableSelector(actionType) && !selectorUsableForBusinessAction(selector) {
		return nil
	}
	adaptive := action.VerificationStatus == "runtime_adaptive"
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
		DurationHintMS: maxInt(action.DurationHintMS, 12000),
		Tags:           uniqueStrings([]string{"verified_interaction", action.IntentGoalID, action.VerificationSource, action.VerificationStatus}),
		Metadata: map[string]any{
			"verified_interaction_id": action.ID,
			"intent_goal_id":          action.IntentGoalID,
			"verification_status":     action.VerificationStatus,
			"verification_source":     action.VerificationSource,
			"selector_score":          action.SelectorScore,
			"runtime_adaptive":        adaptive,
			"sidecar_unavailable":     sidecarUnavailable,
		},
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
		Narrative:      &model.NarrativeCue{Title: "展示最终状态", Caption: "保留最终页面状态，方便审阅结果。", AudienceLens: project.TargetAudience},
		Capture:        &model.CaptureSpec{Screenshot: true, Video: true, AssetRole: "closing_state", MaskSelectors: maskSelectorsFromProject(project)},
		EvidenceRefs:   evidence,
		FailurePolicy:  &model.NodeFailurePolicy{RetryAttempts: 1},
		DurationHintMS: 10000,
		Metadata:       map[string]any{"verification_status": "non_blocking_observation"},
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

func graphRequirementsFromIntent(intent *model.DemoIntentSpec) []model.GraphRequirement {
	if intent == nil {
		return nil
	}
	requirements := []model.GraphRequirement{}
	for _, goal := range intent.Goals {
		requirements = append(requirements, model.GraphRequirement{
			ID:           goal.ID,
			Kind:         "demo_intent_goal",
			Description:  goal.Label,
			Required:     goal.Required,
			EvidenceRefs: goal.EvidenceRefs,
		})
	}
	return requirements
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
		graph.Assets.TargetDurationSec = 60
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
	if graph.Assets.TargetDurationSec < 60 {
		graph.Assets.TargetDurationSec = 60
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
			WaitUntil: "networkidle",
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
		DurationHintMS: 12000,
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
		DurationHintMS: 12000,
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
	if node.DurationHintMS < minMS {
		node.DurationHintMS = minMS
	}
	if node.ActionSpec != nil && node.ActionSpec.TimeoutMS < minMS {
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
