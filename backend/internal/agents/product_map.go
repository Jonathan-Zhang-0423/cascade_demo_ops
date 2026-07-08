package agents

import (
	"context"

	"cascade-demoops/backend/internal/model"
)

type ProductMapAgent struct{}

func NewProductMapAgent() *ProductMapAgent { return &ProductMapAgent{} }

func (a *ProductMapAgent) ExploreProduct(ctx context.Context, project *model.ProjectContext) (*model.ProductMap, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &model.ProductMap{
		ProjectID: project.ID,
		Summary:   "MVP placeholder product map generated before Playwright exploration is wired.",
		Pages: []*model.ProductPage{
			{
				URL:            project.ProductURL,
				Title:          "Product entry",
				Purpose:        "Start the product demo from the provided staging or local URL.",
				Actions:        []string{"navigate", "inspect", "capture"},
				DemoValueScore: 0.8,
			},
		},
		Features: []*model.Feature{
			{
				Name:         "Primary workflow",
				UserValue:    "Shows the core product value in a short executable demo path.",
				BestAudience: []string{project.TargetAudience},
			},
		},
	}, nil
}
