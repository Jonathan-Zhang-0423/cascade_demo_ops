package agents

import (
	"context"
	"fmt"
	"time"

	"cascade-demoops/backend/internal/model"
)

type ProductMapAgent struct{}

func NewProductMapAgent() *ProductMapAgent { return &ProductMapAgent{} }

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
	return &model.ProductMap{
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
	}, nil
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
