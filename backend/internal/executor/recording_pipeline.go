package executor

import (
	"context"
	"errors"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type RecordingRenderPipelineRequest struct {
	SourcePackage      *model.ClientExecutionPackage
	CloudJobID         string
	RecordingOutputDir string
	RenderOutputDir    string
	RecordingMode      RecordingMode
	ResultCreatedAt    time.Time
	Progress           func(stage string, message string, progress int)
}

type RecordingRenderPipelineResult struct {
	RecordRequest          RecordRequest
	RecordResult           RecordResult
	RecordingResultPackage model.RecordingResultPackage
	RenderRequest          RenderRequest
	RenderResult           RenderResult
}

func RunClientExecutionRecordingAndRender(ctx context.Context, service Service, request RecordingRenderPipelineRequest) (RecordingRenderPipelineResult, error) {
	if service == nil {
		return RecordingRenderPipelineResult{}, errors.New("executor service is required")
	}
	if strings.TrimSpace(request.CloudJobID) == "" {
		return RecordingRenderPipelineResult{}, errors.New("cloud_job_id is required")
	}
	if strings.TrimSpace(request.RenderOutputDir) == "" {
		return RecordingRenderPipelineResult{}, errors.New("render_output_dir is required")
	}

	recordRequest, err := NewRecordRequestFromClientExecutionPackage(request.SourcePackage, request.RecordingOutputDir)
	if err != nil {
		return RecordingRenderPipelineResult{}, err
	}
	if request.RecordingMode != "" {
		recordRequest.RecordingMode = request.RecordingMode
	}
	reportPipelineProgress(request, "running_script", "Running the protocol-provided Playwright script and recording browser artifacts.", 55)
	recordResult, err := service.Record(ctx, recordRequest)
	if err != nil {
		return RecordingRenderPipelineResult{}, err
	}
	reportPipelineProgress(request, "packaging_recording", "Packaging raw recording, screenshots, trace, and execution results.", 70)
	recordingResultPackage, err := NewRecordingResultPackageFromRecordResult(request.SourcePackage, recordResult, request.CloudJobID, request.ResultCreatedAt)
	if err != nil {
		return RecordingRenderPipelineResult{}, err
	}

	result := RecordingRenderPipelineResult{
		RecordRequest:          recordRequest,
		RecordResult:           recordResult,
		RecordingResultPackage: recordingResultPackage,
	}
	if recordingResultPackage.Status == model.RecordingResultStatusFailed {
		return result, nil
	}
	renderRequest, err := NewRenderRequestFromRecordingResult(request.SourcePackage, &result.RecordingResultPackage, request.RenderOutputDir)
	if err != nil {
		return RecordingRenderPipelineResult{}, err
	}
	reportPipelineProgress(request, "rendering", "Rendering the final demo video from existing captured assets.", 85)
	renderResult, err := service.Render(ctx, renderRequest)
	if err != nil {
		return RecordingRenderPipelineResult{}, err
	}
	result.RenderRequest = renderRequest
	result.RenderResult = renderResult
	attachRenderResultArtifacts(request.SourcePackage, &result.RecordingResultPackage, renderResult, result.RecordingResultPackage.CreatedAt)
	return result, nil
}

func reportPipelineProgress(request RecordingRenderPipelineRequest, stage string, message string, progress int) {
	if request.Progress != nil {
		request.Progress(stage, message, progress)
	}
}

func attachRenderResultArtifacts(source *model.ClientExecutionPackage, result *model.RecordingResultPackage, renderResult RenderResult, createdAt time.Time) {
	if source == nil || result == nil {
		return
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	artifacts := renderArtifactsFromResult(source, renderResult, createdAt)
	if len(artifacts) == 0 {
		return
	}
	result.GeneratedAssets = uniqueArtifactRefs(append(result.GeneratedAssets, artifacts...))
	result.Delivery.AssetRefs = appendUniqueArtifactDescriptors(result.Delivery.AssetRefs, assetDescriptorsFromArtifacts(source, artifacts, createdAt)...)
}

func renderArtifactsFromResult(source *model.ClientExecutionPackage, renderResult RenderResult, createdAt time.Time) []model.ArtifactRef {
	type renderArtifactSpec struct {
		path          string
		kind          string
		mimeFallback  string
		role          string
		includeInDemo bool
	}
	specs := []renderArtifactSpec{
		{path: renderResult.VideoPath, kind: "demo_video", mimeFallback: "video/mp4", role: "final_demo", includeInDemo: true},
		{path: renderResult.StepByStepDocsPath, kind: "step_by_step_docs", mimeFallback: "text/markdown", role: "step_by_step_docs"},
		{path: renderResult.AssetTimelineCatalogPath, kind: "asset_timeline_catalog", mimeFallback: "application/json", role: "render_metadata"},
		{path: renderResult.DemoEditPlanPath, kind: "demo_edit_plan", mimeFallback: "application/json", role: "render_plan"},
		{path: renderResult.ValidationReportPath, kind: "demo_edit_plan_validation", mimeFallback: "application/json", role: "render_validation"},
		{path: renderResult.RenderManifestPath, kind: "render_manifest", mimeFallback: "application/json", role: "render_manifest"},
	}
	artifacts := []model.ArtifactRef{}
	for _, spec := range specs {
		if spec.path == "" {
			continue
		}
		metadata := map[string]any{
			"asset_role":             spec.role,
			"include_in_demo":        spec.includeInDemo,
			"source_material_policy": "existing_assets_only",
		}
		if renderResult.RenderManifestPath != "" && spec.kind == "demo_video" {
			metadata["render_manifest_path"] = renderResult.RenderManifestPath
		}
		artifacts = append(artifacts, model.ArtifactRef{
			ID:        artifactID(source.PackageID, spec.kind, 1),
			Kind:      spec.kind,
			URI:       spec.path,
			MimeType:  mimeTypeForPath(spec.path, spec.mimeFallback),
			CreatedAt: createdAt,
			Metadata:  metadata,
		})
	}
	return artifacts
}

func appendUniqueArtifactDescriptors(left []model.PackageArtifactDescriptor, right ...model.PackageArtifactDescriptor) []model.PackageArtifactDescriptor {
	seen := map[string]bool{}
	out := append([]model.PackageArtifactDescriptor{}, left...)
	for _, descriptor := range out {
		key := descriptor.ID
		if key == "" {
			key = descriptor.URI
		}
		if key != "" {
			seen[key] = true
		}
	}
	for _, descriptor := range right {
		key := descriptor.ID
		if key == "" {
			key = descriptor.URI
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, descriptor)
	}
	return out
}
