package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/llm"
	"cascade-demoops/backend/internal/model"
)

type ProductMapAgent struct {
	llm llm.Client
}

func NewProductMapAgent() *ProductMapAgent { return &ProductMapAgent{} }

func NewProductMapAgentWithLLM(client llm.Client) *ProductMapAgent {
	return &ProductMapAgent{llm: client}
}

type productMapLLMOutput struct {
	Summary string `json:"summary"`
	Pages   []struct {
		ID      string              `json:"id"`
		Title   string              `json:"title"`
		Purpose string              `json:"purpose"`
		Actions flexibleStringSlice `json:"actions"`
	} `json:"pages"`
	Features []struct {
		ID            string              `json:"id"`
		Name          string              `json:"name"`
		Kind          string              `json:"kind"`
		UserValue     string              `json:"user_value"`
		BusinessValue string              `json:"business_value"`
		Priority      string              `json:"priority"`
		KeyActions    flexibleStringSlice `json:"key_actions"`
	} `json:"features"`
	Workflows []struct {
		ID             string              `json:"id"`
		Name           string              `json:"name"`
		UseCase        flexibleDemoUseCase `json:"use_case"`
		EstimatedSteps int                 `json:"estimated_steps"`
		ValueScore     float64             `json:"value_score"`
		Feasibility    float64             `json:"feasibility"`
	} `json:"workflows"`
}

func (o *productMapLLMOutput) UnmarshalJSON(data []byte) error {
	type alias productMapLLMOutput
	var single alias
	if err := json.Unmarshal(data, &single); err == nil {
		*o = productMapLLMOutput(single)
		return nil
	}
	var items []alias
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	merged := productMapLLMOutput{}
	for _, item := range items {
		patch := productMapLLMOutput(item)
		if merged.Summary == "" {
			merged.Summary = patch.Summary
		}
		merged.Pages = append(merged.Pages, patch.Pages...)
		merged.Features = append(merged.Features, patch.Features...)
		merged.Workflows = append(merged.Workflows, patch.Workflows...)
	}
	*o = merged
	return nil
}

func (a *ProductMapAgent) ExploreProduct(ctx context.Context, project *model.ProjectContext, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack) (*model.ProductMap, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if intelligence == nil && project != nil {
		intelligence = project.ProjectIntelligence
	}
	entryPoint := productMapEntryPoint(project)
	if report != nil && len(report.PageSnapshots) > 0 && report.PageSnapshots[0].URL != "" {
		entryPoint = report.PageSnapshots[0].URL
	}
	pages := productPagesFromUnderstanding(project, report, entryPoint)
	pages = productPagesFromIntelligence(intelligence, pages, entryPoint)
	features := productFeaturesFromUnderstanding(project, report)
	features = productFeaturesFromIntelligence(project, intelligence, features)
	routes := routeNodesFromUnderstanding(report, pages)
	routes = routeNodesFromIntelligence(intelligence, routes, pages)
	components := componentNodesFromUnderstanding(report)
	components = componentNodesFromIntelligence(intelligence, components)
	dataModels := dataModelNodesFromUnderstanding(report)
	dataModels = dataModelNodesFromIntelligence(intelligence, dataModels)
	workflows := workflowCandidatesFromUnderstanding(report, pages)
	workflows = workflowCandidatesFromIntelligence(intelligence, workflows)
	evidenceRefs := []model.EvidenceRef{}
	summary := "基于多模态理解报告生成的产品地图。"
	if report != nil {
		evidenceRefs = report.EvidenceRefs
		if report.Summary != "" {
			summary = report.Summary
		}
	}
	if intelligence != nil {
		evidenceRefs = uniqueEvidenceRefs(append(evidenceRefs, intelligence.EvidenceRefs...))
		if intelligence.Architecture != nil && intelligence.Architecture.Summary != "" {
			if summary == "" {
				summary = "基于项目智能图谱生成的产品地图。"
			}
			summary += " 项目图谱：" + intelligence.Architecture.Summary
		}
	}
	productMap := &model.ProductMap{
		ID:           "product_map_" + project.ID,
		ProjectID:    project.ID,
		Version:      1,
		GeneratedAt:  time.Now().UTC(),
		Summary:      summary,
		Pages:        pages,
		Features:     features,
		Routes:       routes,
		Components:   components,
		DataModels:   dataModels,
		Workflows:    workflows,
		EvidenceRefs: evidenceRefs,
	}
	trace, err := a.enhanceProductMapWithLLM(ctx, project, report, intelligence, productMap)
	if err != nil && !llm.IsDeterministicFallback(err) {
		return nil, err
	}
	if trace != nil {
		productMap.EvidenceRefs = append(productMap.EvidenceRefs, model.EvidenceRef{
			ID:         "ev_model_product_map_" + shortHash(trace.Label()),
			Kind:       model.EvidenceKindUserInput,
			Summary:    "ProductMapAgent 模型路由：" + trace.Label(),
			FieldPath:  "model_trace.product_map",
			Confidence: 0.7,
		})
	}
	return productMap, nil
}

func (a *ProductMapAgent) enhanceProductMapWithLLM(ctx context.Context, project *model.ProjectContext, report *model.MultimodalUnderstandingReport, intelligence *model.ProjectIntelligencePack, productMap *model.ProductMap) (*llm.CallTrace, error) {
	if a.llm == nil || project == nil || productMap == nil {
		return nil, nil
	}
	payload := map[string]any{
		"target_audience":      project.TargetAudience,
		"report_summary":       "",
		"project_intelligence": compactProductIntelligenceForProductMap(intelligence),
		"pages":                productMap.Pages,
		"features":             productMap.Features,
		"routes":               productMap.Routes,
		"workflows":            productMap.Workflows,
	}
	if report != nil {
		payload["report_summary"] = report.Summary
		payload["brief"] = report.RequirementBrief
	}
	data, _ := json.Marshal(payload)
	var output productMapLLMOutput
	trace, err := a.llm.GenerateJSON(ctx, config.ModelTaskPlanning, llm.JSONRequest{
		System:       "你是 Cascade DemoOps 的产品地图 agent。请基于理解报告把页面、功能和 workflow 命名成适合中国产品/运营团队审批的结构化产品地图。不要输出源码或密钥。",
		User:         string(data),
		SchemaName:   "ProductMapPatch",
		ResponseHint: "返回字段：summary, pages[{id,title,purpose,actions}], features[{id,name,kind,user_value,business_value,priority,key_actions}], workflows[{id,name,use_case,estimated_steps,value_score,feasibility}]。",
		MaxTokens:    1800,
		Temperature:  0.2,
	}, &output)
	if err != nil {
		return trace, err
	}
	if output.Summary != "" {
		productMap.Summary = output.Summary
	}
	for _, patch := range output.Pages {
		for _, page := range productMap.Pages {
			if page == nil || page.ID != patch.ID {
				continue
			}
			if patch.Title != "" {
				page.Title = patch.Title
			}
			if patch.Purpose != "" {
				page.Purpose = patch.Purpose
			}
			page.Actions = uniqueStrings(append(page.Actions, stringSlice(patch.Actions)...))
		}
	}
	for _, patch := range output.Features {
		name := firstNonEmpty(patch.Name, "模型识别功能")
		productMap.Features = append(productMap.Features, &model.Feature{
			ID:            firstNonEmpty(patch.ID, "feature_llm_map_"+shortHash(name)),
			Name:          name,
			Kind:          firstNonEmpty(patch.Kind, "supporting"),
			UserValue:     patch.UserValue,
			BusinessValue: patch.BusinessValue,
			Priority:      firstNonEmpty(patch.Priority, "supporting"),
			BestAudience:  []string{project.TargetAudience},
			KeyActions:    stringSlice(patch.KeyActions),
			EvidenceRefs:  productMap.EvidenceRefs,
		})
	}
	for _, patch := range output.Workflows {
		name := firstNonEmpty(patch.Name, "模型建议流程")
		productMap.Workflows = append(productMap.Workflows, &model.WorkflowCandidate{
			ID:             firstNonEmpty(patch.ID, "workflow_llm_map_"+shortHash(name)),
			Name:           name,
			UseCase:        demoUseCase(patch.UseCase),
			AudienceID:     "audience_primary",
			EstimatedSteps: patch.EstimatedSteps,
			ValueScore:     patch.ValueScore,
			Feasibility:    patch.Feasibility,
			EvidenceRefs:   productMap.EvidenceRefs,
		})
	}
	return trace, nil
}

func productMapEntryPoint(project *model.ProjectContext) string {
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

func productPagesFromUnderstanding(project *model.ProjectContext, report *model.MultimodalUnderstandingReport, entryPoint string) []*model.ProductPage {
	pages := []*model.ProductPage{}
	if report != nil {
		for i, page := range report.PageSnapshots {
			pageID := firstNonEmpty(page.ID, fmt.Sprintf("page_%d", i+1))
			actions := []string{"inspect", "capture"}
			primaryActions := []model.UIActionRef{}
			for _, action := range page.Actions {
				if action.Kind != "" {
					actions = append(actions, action.Kind)
				}
				primaryActions = append(primaryActions, model.UIActionRef{
					ID:           action.ID,
					Label:        action.Label,
					Kind:         action.Kind,
					Selector:     action.SelectorHint,
					TargetRoute:  action.TargetURL,
					EvidenceRefs: action.EvidenceRefs,
				})
			}
			pages = append(pages, &model.ProductPage{
				ID:             pageID,
				URL:            firstNonEmpty(page.URL, entryPoint),
				Title:          firstNonEmpty(page.Title, "产品页面"),
				Purpose:        firstNonEmpty(page.VisionSummary, "用于构造可执行演示脚本的页面证据。"),
				Actions:        uniqueStrings(actions),
				PrimaryActions: primaryActions,
				States:         page.States,
				FeatureRefs:    []string{"feature_primary_value"},
				EvidenceRefs:   page.EvidenceRefs,
				DemoValueScore: 0.82,
			})
		}
	}
	if len(pages) == 0 {
		pages = append(pages, &model.ProductPage{
			ID:             "page_entry",
			URL:            entryPoint,
			Title:          "产品入口",
			Purpose:        "从提供的产品地址、截图或代码摘要启动演示脚本。",
			Actions:        []string{"navigate", "inspect", "capture"},
			FeatureRefs:    []string{"feature_primary_value"},
			DemoValueScore: 0.7,
		})
	}
	return pages
}

func productFeaturesFromUnderstanding(project *model.ProjectContext, report *model.MultimodalUnderstandingReport) []*model.Feature {
	if report != nil && len(report.FeatureHypotheses) > 0 {
		return report.FeatureHypotheses
	}
	return []*model.Feature{{
		ID:           "feature_primary_value",
		Name:         "核心产品价值",
		Kind:         "hero",
		UserValue:    firstNonEmpty(project.ProductDescription, "展示产品核心价值路径。"),
		Priority:     "hero",
		BestAudience: []string{project.TargetAudience},
		BestUseCases: []model.DemoUseCase{model.DemoUseCaseLaunch},
		KeyActions:   []string{"navigate", "inspect", "validate"},
	}}
}

func productPagesFromIntelligence(intelligence *model.ProjectIntelligencePack, existing []*model.ProductPage, entryPoint string) []*model.ProductPage {
	if intelligence == nil {
		return existing
	}
	intentText := projectIntelligenceIntentText(intelligence)
	seen := map[string]bool{}
	for _, page := range existing {
		if page == nil {
			continue
		}
		seen[firstNonEmpty(page.ID, page.URL, page.RoutePattern)] = true
	}
	for _, surface := range intelligence.InteractionSurfaces {
		key := firstNonEmpty(surface.PageID, surface.ID, surface.URL)
		if key == "" {
			continue
		}
		actions := []string{}
		primaryActions := []model.UIActionRef{}
		for _, action := range surface.Actions {
			if !actionAllowedForIntentEvidenceForIntent(action.Label, action.Selector, intentText) {
				continue
			}
			actions = append(actions, firstNonEmpty(action.Kind, "inspect"))
			primaryActions = append(primaryActions, action)
		}
		if len(actions) == 0 {
			actions = []string{"inspect", "capture"}
		}
		if seen[key] {
			for _, page := range existing {
				if page == nil || firstNonEmpty(page.ID, page.URL, page.RoutePattern) != key {
					continue
				}
				page.Actions = uniqueStrings(append(page.Actions, actions...))
				page.PrimaryActions = append(page.PrimaryActions, primaryActions...)
				page.States = uniqueStrings(append(page.States, surface.States...))
				page.FeatureRefs = uniqueStrings(append(page.FeatureRefs, surface.FeatureRefs...))
				page.EvidenceRefs = uniqueEvidenceRefs(append(page.EvidenceRefs, surface.EvidenceRefs...))
				break
			}
			continue
		}
		existing = append(existing, &model.ProductPage{
			ID:             firstNonEmpty(surface.PageID, surface.ID),
			URL:            firstNonEmpty(surface.URL, entryPoint),
			Title:          firstNonEmpty(surface.Title, "项目理解页面"),
			Purpose:        firstNonEmpty(surface.PageRole, "由 ProjectIntelligenceGraph 识别的可演示交互面。"),
			Actions:        uniqueStrings(actions),
			PrimaryActions: append([]model.UIActionRef{}, primaryActions...),
			States:         append([]string{}, surface.States...),
			FeatureRefs:    append([]string{}, surface.FeatureRefs...),
			EvidenceRefs:   surface.EvidenceRefs,
			DemoValueScore: maxFloat(surface.Confidence, 0.68),
		})
		seen[key] = true
	}
	return existing
}

func productFeaturesFromIntelligence(project *model.ProjectContext, intelligence *model.ProjectIntelligencePack, existing []*model.Feature) []*model.Feature {
	if intelligence == nil {
		return existing
	}
	seen := map[string]bool{}
	for _, feature := range existing {
		if feature != nil {
			seen[firstNonEmpty(feature.ID, feature.Name)] = true
		}
	}
	for _, capability := range intelligence.FeatureCapabilities {
		key := firstNonEmpty(capability.ID, capability.Name)
		if key == "" || seen[key] {
			continue
		}
		existing = append(existing, &model.Feature{
			ID:              firstNonEmpty(capability.ID, "feature_capability_"+shortHash(capability.Name)),
			Name:            firstNonEmpty(capability.Name, "项目理解功能能力"),
			Kind:            firstNonEmpty(capability.Kind, "supporting"),
			UserValue:       firstNonEmpty(capability.UserValue, capability.BusinessValue, "项目智能图谱识别的可演示能力。"),
			BusinessValue:   capability.BusinessValue,
			Priority:        firstNonEmpty(capability.Priority, "supporting"),
			BestAudience:    []string{project.TargetAudience},
			BestUseCases:    productUseCases(project),
			SupportingPages: append([]string{}, capability.SupportingPageRefs...),
			KeyActions:      append([]string{}, capability.KeyActions...),
			Risks:           append([]string{}, capability.Risks...),
			EvidenceRefs:    capability.EvidenceRefs,
		})
		seen[key] = true
	}
	return existing
}

func productUseCases(project *model.ProjectContext) []model.DemoUseCase {
	if project == nil {
		return []model.DemoUseCase{model.DemoUseCaseLaunch}
	}
	if len(project.Goals) > 0 {
		useCases := make([]model.DemoUseCase, 0, len(project.Goals))
		for _, goal := range project.Goals {
			if goal.UseCase != "" {
				useCases = append(useCases, goal.UseCase)
			}
		}
		if len(useCases) > 0 {
			return uniqueUseCases(useCases)
		}
	}
	return []model.DemoUseCase{model.DemoUseCaseLaunch}
}

func routeNodesFromIntelligence(intelligence *model.ProjectIntelligencePack, existing []*model.RouteMapNode, pages []*model.ProductPage) []*model.RouteMapNode {
	if intelligence == nil || intelligence.Architecture == nil {
		return existing
	}
	seen := map[string]bool{}
	for _, route := range existing {
		if route != nil {
			seen[firstNonEmpty(route.ID, route.Path)] = true
		}
	}
	for _, route := range intelligence.Architecture.RouteTree {
		key := firstNonEmpty(route.ID, route.Path)
		if key == "" || seen[key] {
			continue
		}
		existing = append(existing, &model.RouteMapNode{
			ID:           firstNonEmpty(route.ID, safeID("route", route.Path)),
			Path:         route.Path,
			Name:         firstNonEmpty(route.Name, routeNameFromPath(route.Path)),
			ParentID:     route.ParentPath,
			ComponentIDs: append([]string{}, route.ComponentRefs...),
			AuthRequired: route.AuthRequired,
			EvidenceRefs: route.EvidenceRefs,
		})
		seen[key] = true
	}
	for _, page := range pages {
		if page == nil {
			continue
		}
		key := firstNonEmpty(page.ID, page.URL)
		if key == "" || seen[key] {
			continue
		}
		existing = append(existing, &model.RouteMapNode{
			ID:           firstNonEmpty(page.ID, safeID("route", page.URL)),
			Path:         firstNonEmpty(page.RoutePattern, pathFromURL(page.URL)),
			Name:         firstNonEmpty(page.Title, routeNameFromPath(pathFromURL(page.URL))),
			PageID:       page.ID,
			AuthRequired: true,
			EvidenceRefs: page.EvidenceRefs,
		})
		seen[key] = true
	}
	return existing
}

func componentNodesFromIntelligence(intelligence *model.ProjectIntelligencePack, existing []*model.ComponentNode) []*model.ComponentNode {
	if intelligence == nil {
		return existing
	}
	seen := map[string]bool{}
	for _, component := range existing {
		if component != nil {
			seen[firstNonEmpty(component.ID, component.Name)] = true
		}
	}
	for _, surface := range intelligence.InteractionSurfaces {
		key := firstNonEmpty(surface.ID, surface.PageID, surface.URL)
		if key == "" || seen[key] {
			continue
		}
		selectors := []string{}
		for _, selector := range surface.StableSelectors {
			if selector.Value != "" {
				selectors = append(selectors, selector.Value)
			}
		}
		actions := make([]model.UIActionRef, 0, len(surface.Actions))
		for _, action := range surface.Actions {
			if !actionAllowedForIntentEvidenceForIntent(action.Label, action.Selector, projectIntelligenceIntentText(intelligence)) {
				continue
			}
			actions = append(actions, model.UIActionRef{
				ID:           action.ID,
				Label:        action.Label,
				Kind:         action.Kind,
				Selector:     action.Selector,
				TargetRoute:  action.TargetRoute,
				EvidenceRefs: action.EvidenceRefs,
			})
		}
		existing = append(existing, &model.ComponentNode{
			ID:           firstNonEmpty(surface.ID, safeID("component", surface.Title)),
			Name:         firstNonEmpty(surface.Title, "交互面"),
			Kind:         firstNonEmpty(surface.PageRole, "page_surface"),
			Selectors:    uniqueStrings(selectors),
			Actions:      actions,
			FeatureRefs:  append([]string{}, surface.FeatureRefs...),
			EvidenceRefs: surface.EvidenceRefs,
		})
		seen[key] = true
	}
	return existing
}

func dataModelNodesFromIntelligence(intelligence *model.ProjectIntelligencePack, existing []*model.DataModelNode) []*model.DataModelNode {
	if intelligence == nil {
		return existing
	}
	seen := map[string]bool{}
	for _, dataModel := range existing {
		if dataModel != nil {
			seen[firstNonEmpty(dataModel.ID, dataModel.Name)] = true
		}
	}
	for _, summary := range intelligence.DataModels {
		key := firstNonEmpty(summary.ID, summary.Name)
		if key == "" || seen[key] {
			continue
		}
		existing = append(existing, &model.DataModelNode{
			ID:           firstNonEmpty(summary.ID, safeID("model", summary.Name)),
			Name:         firstNonEmpty(summary.Name, "数据模型"),
			Kind:         firstNonEmpty(summary.Kind, "domain_model"),
			Fields:       append([]model.DataField{}, summary.Fields...),
			EvidenceRefs: summary.EvidenceRefs,
		})
		seen[key] = true
	}
	return existing
}

func workflowCandidatesFromIntelligence(intelligence *model.ProjectIntelligencePack, existing []*model.WorkflowCandidate) []*model.WorkflowCandidate {
	if intelligence == nil {
		return existing
	}
	seen := map[string]bool{}
	for _, workflow := range existing {
		if workflow != nil {
			seen[firstNonEmpty(workflow.ID, workflow.Name)] = true
		}
	}
	for _, plan := range intelligence.DemoScenarioPlans {
		key := firstNonEmpty(plan.ID, plan.Name)
		if key == "" || seen[key] {
			continue
		}
		existing = append(existing, &model.WorkflowCandidate{
			ID:             firstNonEmpty(plan.ID, safeID("workflow", plan.Name)),
			Name:           firstNonEmpty(plan.Name, "项目智能候选路径"),
			UseCase:        plan.UseCase,
			AudienceID:     firstNonEmpty(plan.AudienceID, "audience_primary"),
			FeatureRefs:    append([]string{}, plan.FeatureRefs...),
			PageRefs:       append([]string{}, plan.PageRefs...),
			EstimatedSteps: plan.EstimatedSteps,
			ValueScore:     plan.ValueScore,
			Feasibility:    plan.Feasibility,
			RiskNotes:      append([]string{}, plan.RiskNotes...),
			EvidenceRefs:   plan.EvidenceRefs,
		})
		seen[key] = true
	}
	return existing
}

func compactProductIntelligenceForProductMap(intelligence *model.ProjectIntelligencePack) map[string]any {
	if intelligence == nil {
		return nil
	}
	return map[string]any{
		"architecture": map[string]any{
			"summary": func() string {
				if intelligence.Architecture != nil {
					return intelligence.Architecture.Summary
				}
				return ""
			}(),
			"frameworks": func() []string {
				if intelligence.Architecture != nil {
					return intelligence.Architecture.Frameworks
				}
				return nil
			}(),
			"languages": func() []string {
				if intelligence.Architecture != nil {
					return intelligence.Architecture.Languages
				}
				return nil
			}(),
			"module_count": func() int {
				if intelligence.Architecture != nil {
					return len(intelligence.Architecture.Modules)
				}
				return 0
			}(),
			"route_count": func() int {
				if intelligence.Architecture != nil {
					return len(intelligence.Architecture.RouteTree)
				}
				return 0
			}(),
		},
		"feature_capabilities": intelligence.FeatureCapabilities,
		"interaction_surfaces": intelligence.InteractionSurfaces,
		"api_count":            len(intelligence.APIContracts),
		"data_model_count":     len(intelligence.DataModels),
		"demo_scenario_plans":  intelligence.DemoScenarioPlans,
		"script_readiness":     intelligence.ScriptReadinessReport,
		"confidence":           intelligence.Confidence,
	}
}

func routeNodesFromUnderstanding(report *model.MultimodalUnderstandingReport, pages []*model.ProductPage) []*model.RouteMapNode {
	routes := []*model.RouteMapNode{}
	if report != nil {
		for _, snapshot := range report.CodeSnapshots {
			for _, route := range snapshot.Routes {
				routes = append(routes, &model.RouteMapNode{
					ID:           route.ID,
					Path:         route.Path,
					Name:         route.Name,
					AuthRequired: route.AuthRequired,
					EvidenceRefs: route.EvidenceRefs,
				})
			}
		}
	}
	if len(routes) == 0 {
		for _, page := range pages {
			routes = append(routes, &model.RouteMapNode{
				ID:           "route_" + page.ID,
				Path:         firstNonEmpty(page.RoutePattern, page.URL),
				Name:         page.Title,
				PageID:       page.ID,
				AuthRequired: true,
				EvidenceRefs: page.EvidenceRefs,
			})
		}
	}
	return routes
}

func componentNodesFromUnderstanding(report *model.MultimodalUnderstandingReport) []*model.ComponentNode {
	components := []*model.ComponentNode{}
	if report == nil {
		return components
	}
	for _, snapshot := range report.CodeSnapshots {
		for _, component := range snapshot.Components {
			actions := []model.UIActionRef{}
			for _, selector := range component.SelectorHints {
				actions = append(actions, model.UIActionRef{ID: "action_" + shortHash(selector), Kind: "inspect", Selector: selector})
			}
			components = append(components, &model.ComponentNode{
				ID:           component.ID,
				Name:         component.Name,
				Kind:         component.Kind,
				Selectors:    component.SelectorHints,
				Actions:      actions,
				EvidenceRefs: component.EvidenceRefs,
			})
		}
	}
	return components
}

func dataModelNodesFromUnderstanding(report *model.MultimodalUnderstandingReport) []*model.DataModelNode {
	dataModels := []*model.DataModelNode{}
	if report == nil {
		return dataModels
	}
	for _, snapshot := range report.CodeSnapshots {
		for _, dataModel := range snapshot.DataModels {
			dataModels = append(dataModels, &model.DataModelNode{
				ID:           dataModel.ID,
				Name:         dataModel.Name,
				Kind:         dataModel.Kind,
				Fields:       dataModel.Fields,
				EvidenceRefs: dataModel.EvidenceRefs,
			})
		}
	}
	return dataModels
}

func workflowCandidatesFromUnderstanding(report *model.MultimodalUnderstandingReport, pages []*model.ProductPage) []*model.WorkflowCandidate {
	if report != nil && len(report.WorkflowCandidates) > 0 {
		return report.WorkflowCandidates
	}
	pageRefs := []string{}
	for _, page := range pages {
		pageRefs = append(pageRefs, page.ID)
	}
	return []*model.WorkflowCandidate{{
		ID:             "workflow_script_ready_demo",
		Name:           "脚本文档预览流程",
		AudienceID:     "audience_primary",
		FeatureRefs:    []string{"feature_primary_value"},
		PageRefs:       pageRefs,
		EstimatedSteps: 3,
		ValueScore:     0.8,
		Feasibility:    0.72,
	}}
}
