package agents

import (
	"context"
	"time"

	"cascade-demoops/backend/internal/model"
)

type ProductMapAgent struct{}

func NewProductMapAgent() *ProductMapAgent { return &ProductMapAgent{} }

func (a *ProductMapAgent) ExploreProduct(ctx context.Context, project *model.ProjectContext) (*model.ProductMap, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pageID := "page_entry"
	featureID := "feature_primary_workflow"
	entryPoint := productMapEntryPoint(project)
	return &model.ProductMap{
		ID:          "product_map_" + project.ID,
		ProjectID:   project.ID,
		Version:     1,
		GeneratedAt: time.Now().UTC(),
		Summary:     "MVP placeholder product map generated before Playwright exploration is wired.",
		Pages: []*model.ProductPage{
			{
				ID:             pageID,
				URL:            entryPoint,
				Title:          "Product entry",
				Purpose:        "Start the product demo from the provided staging or local URL.",
				Actions:        []string{"navigate", "inspect", "capture"},
				FeatureRefs:    []string{featureID},
				DemoValueScore: 0.8,
			},
		},
		Features: []*model.Feature{
			{
				ID:           featureID,
				Name:         "Primary workflow",
				Kind:         "hero",
				UserValue:    "Shows the core product value in a short executable demo path.",
				BestAudience: []string{project.TargetAudience},
				BestUseCases: []model.DemoUseCase{model.DemoUseCaseLaunch},
				KeyActions:   []string{"navigate", "inspect", "validate"},
			},
		},
		Routes: []*model.RouteMapNode{
			{
				ID:           "route_entry",
				Path:         entryPoint,
				Name:         "Product entry",
				PageID:       pageID,
				AuthRequired: project.DemoAccount != nil,
			},
		},
		Workflows: []*model.WorkflowCandidate{
			{
				ID:             "workflow_primary_demo",
				Name:           "Primary product value demo",
				AudienceID:     "audience_primary",
				FeatureRefs:    []string{featureID},
				PageRefs:       []string{pageID},
				EstimatedSteps: 3,
				ValueScore:     0.8,
				Feasibility:    0.7,
			},
		},
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
