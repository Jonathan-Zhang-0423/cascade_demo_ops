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

func (a *ProductMapAgent) ExploreProduct(ctx context.Context, project *model.ProjectContext, report *model.MultimodalUnderstandingReport) (*model.ProductMap, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entryPoint := productMapEntryPoint(project)
	if report != nil && len(report.PageSnapshots) > 0 && report.PageSnapshots[0].URL != "" {
		entryPoint = report.PageSnapshots[0].URL
	}
	pages := productPagesFromUnderstanding(project, report, entryPoint)
	features := productFeaturesFromUnderstanding(project, report)
	routes := routeNodesFromUnderstanding(report, pages)
	components := componentNodesFromUnderstanding(report)
	dataModels := dataModelNodesFromUnderstanding(report)
	workflows := workflowCandidatesFromUnderstanding(report, pages)
	evidenceRefs := []model.EvidenceRef{}
	summary := "基于多模态理解报告生成的产品地图。"
	if report != nil {
		evidenceRefs = report.EvidenceRefs
		if report.Summary != "" {
			summary = report.Summary
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
	trace, err := a.enhanceProductMapWithLLM(ctx, project, report, productMap)
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

func (a *ProductMapAgent) enhanceProductMapWithLLM(ctx context.Context, project *model.ProjectContext, report *model.MultimodalUnderstandingReport, productMap *model.ProductMap) (*llm.CallTrace, error) {
	if a.llm == nil || project == nil || productMap == nil {
		return nil, nil
	}
	payload := map[string]any{
		"target_audience": project.TargetAudience,
		"report_summary":  "",
		"pages":           productMap.Pages,
		"features":        productMap.Features,
		"routes":          productMap.Routes,
		"workflows":       productMap.Workflows,
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
