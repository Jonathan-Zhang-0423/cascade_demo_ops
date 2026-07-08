package agents

import (
	"context"
	"fmt"

	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

const (
	mvpTargetDurationSec = 60
	mvpMinDurationSec    = 58
	mvpMaxDurationSec    = 62
	mvpDissolveCount     = 3
	mvpZoomCount         = 2
)

type AssetGeneratorAgent struct{}

func NewAssetGeneratorAgent() *AssetGeneratorAgent { return &AssetGeneratorAgent{} }

func (a *AssetGeneratorAgent) GenerateAssets(ctx context.Context, graph *model.DemoWorkflowGraph, result orchestrator.RehearsalResult) (*orchestrator.GeneratedArtifacts, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if graph.Assets == nil {
		graph.Assets = model.NewMVPAssetManifest()
	}
	graph.Assets.TargetDurationSec = mvpTargetDurationSec
	if graph.Assets.TargetDurationSec < mvpMinDurationSec || graph.Assets.TargetDurationSec > mvpMaxDurationSec {
		return nil, fmt.Errorf("target duration must stay within %ds-%ds", mvpMinDurationSec, mvpMaxDurationSec)
	}
	ensureZoomMarkers(graph, mvpZoomCount)

	return &orchestrator.GeneratedArtifacts{
		VideoPath:          fmt.Sprintf("artifacts/%s/demo_%ds_%d_dissolves_%d_zooms.mp4", graph.ID, mvpTargetDurationSec, mvpDissolveCount, mvpZoomCount),
		ScreenshotPaths:    result.ScreenshotRefs,
		StepByStepDocsPath: fmt.Sprintf("artifacts/%s/step_by_step.md", graph.ID),
	}, nil
}

func ensureZoomMarkers(graph *model.DemoWorkflowGraph, desired int) {
	count := 0
	for _, node := range graph.Nodes {
		if node.HasZoom {
			count++
		}
	}
	for _, node := range graph.Nodes {
		if count >= desired {
			return
		}
		if !node.HasZoom {
			node.HasZoom = true
			count++
		}
	}
}
