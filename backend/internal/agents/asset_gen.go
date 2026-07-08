package agents

import (
	"context"
	"fmt"
	"time"

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
	videoPath := fmt.Sprintf("artifacts/%s/demo_%ds_%d_dissolves_%d_zooms.mp4", graph.ID, mvpTargetDurationSec, mvpDissolveCount, mvpZoomCount)
	docsPath := fmt.Sprintf("artifacts/%s/step_by_step.md", graph.ID)
	graph.Assets.GeneratedAssets = []model.AssetRef{
		{
			ID:             "asset_demo_video_60s",
			Kind:           model.AssetKindDemoVideo,
			URI:            videoPath,
			ExecutionRunID: "mvp_rehearsal",
		},
		{
			ID:             "asset_step_by_step_docs",
			Kind:           model.AssetKindStepByStepDocs,
			URI:            docsPath,
			ExecutionRunID: "mvp_rehearsal",
		},
	}
	for i, screenshot := range result.ScreenshotRefs {
		graph.Assets.GeneratedAssets = append(graph.Assets.GeneratedAssets, model.AssetRef{
			ID:             fmt.Sprintf("asset_screenshot_%d", i+1),
			Kind:           model.AssetKindScreenshotPack,
			URI:            screenshot,
			ExecutionRunID: "mvp_rehearsal",
		})
	}
	graph.Assets.Provenance = &model.AssetProvenance{
		WorkflowGraphID: graph.ID,
		GraphVersion:    graph.Version,
		ExecutionRunID:  "mvp_rehearsal",
		GeneratedBy:     "AssetGeneratorAgent",
		GeneratedAt:     time.Now().UTC(),
	}

	return &orchestrator.GeneratedArtifacts{
		VideoPath:          videoPath,
		ScreenshotPaths:    result.ScreenshotRefs,
		StepByStepDocsPath: docsPath,
	}, nil
}

func ensureZoomMarkers(graph *model.DemoWorkflowGraph, desired int) {
	count := 0
	for _, node := range graph.Nodes {
		if node.HasZoom || (node.Capture != nil && node.Capture.Zoom) {
			count++
		}
	}
	for _, node := range graph.Nodes {
		if count >= desired {
			return
		}
		if !node.HasZoom {
			node.HasZoom = true
			if node.Capture != nil {
				node.Capture.Zoom = true
			}
			count++
		}
	}
}
