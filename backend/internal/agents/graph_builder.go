package agents

import (
	"context"
	"fmt"
	"time"

	"cascade-demoops/backend/internal/model"
)

type GraphBuilderAgent struct{}

func NewGraphBuilderAgent() *GraphBuilderAgent { return &GraphBuilderAgent{} }

func (a *GraphBuilderAgent) GenerateGraph(ctx context.Context, project *model.ProjectContext, productMap *model.ProductMap) (*model.DemoWorkflowGraph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	graphID := fmt.Sprintf("graph_%d", time.Now().UnixNano())
	return &model.DemoWorkflowGraph{
		ID:         graphID,
		Version:    1,
		EntryPoint: project.ProductURL,
		Assets:     model.NewMVPAssetManifest(),
		Nodes: []*model.GraphNode{
			{
				ID:              "start",
				Action:          "navigate",
				Selector:        project.ProductURL,
				ExpectedOutcome: "product entry is loaded",
				IsScreenshot:    true,
				HasZoom:         false,
				RetryPolicy:     2,
			},
			{
				ID:              "highlight_primary_value",
				Action:          "inspect",
				Selector:        "main",
				ExpectedOutcome: productMap.Features[0].UserValue,
				IsScreenshot:    true,
				HasZoom:         true,
				RetryPolicy:     2,
			},
			{
				ID:              "close",
				Action:          "assert",
				Selector:        "body",
				ExpectedOutcome: "demo story is complete",
				IsScreenshot:    false,
				HasZoom:         true,
				RetryPolicy:     1,
			},
		},
		Edges: []*model.GraphEdge{
			{ID: "e1", FromNode: "start", ToNode: "highlight_primary_value", Condition: "loaded"},
			{ID: "e2", FromNode: "highlight_primary_value", ToNode: "close", Condition: "validated"},
		},
	}, nil
}
