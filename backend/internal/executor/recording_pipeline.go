package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	renderRequest, renderResult, err := RenderClientExecutionRecordingResult(ctx, service, request.SourcePackage, &result.RecordingResultPackage, request.RenderOutputDir, request.Progress)
	if err != nil {
		return RecordingRenderPipelineResult{}, err
	}
	result.RenderRequest = renderRequest
	result.RenderResult = renderResult
	return result, nil
}

func RenderClientExecutionRecordingResult(ctx context.Context, service DeliveryRenderService, source *model.ClientExecutionPackage, recording *model.RecordingResultPackage, outputDir string, progress func(string, string, int)) (RenderRequest, RenderResult, error) {
	if service == nil || source == nil || recording == nil {
		return RenderRequest{}, RenderResult{}, errors.New("render service, source package, and recording result are required")
	}
	request := RecordingRenderPipelineRequest{SourcePackage: source, RenderOutputDir: outputDir, Progress: progress}
	renderRequest, err := NewRenderRequestFromRecordingResult(source, recording, outputDir)
	if err != nil {
		return RenderRequest{}, RenderResult{}, err
	}
	reportPipelineProgress(request, "directing", "Preparing a source-bound edit plan from validated recording materials.", 78)
	reportPipelineProgress(request, "rendering", "Rendering the final demo video from existing captured assets.", 85)
	renderResult, err := service.Render(ctx, renderRequest)
	if err != nil {
		return RenderRequest{}, RenderResult{}, err
	}
	if err := enrichRenderResultForDirector(ctx, source, recording, &renderResult, outputDir, recording.CreatedAt); err != nil {
		return RenderRequest{}, RenderResult{}, err
	}
	renderResult, err = applyDirectorPatchAndRerender(ctx, service, request, renderRequest, renderResult)
	if err != nil {
		return RenderRequest{}, RenderResult{}, err
	}
	if err := validateFinalMP4Delivery(ctx, service, renderResult); err != nil {
		return RenderRequest{}, RenderResult{}, err
	}
	AttachRenderResultArtifacts(source, recording, renderResult, recording.CreatedAt)
	reportPipelineProgress(request, "quality_validation", "Validating required-step coverage, media decodability, redaction, and output checksums.", 98)
	return renderRequest, renderResult, nil
}

func validateFinalMP4Delivery(ctx context.Context, service DeliveryRenderService, result RenderResult) error {
	videoPath := strings.TrimSpace(result.VideoPath)
	if !strings.EqualFold(filepath.Ext(videoPath), ".mp4") {
		return fmt.Errorf("final_video_not_mp4: renderer returned %q", filepath.Ext(videoPath))
	}
	info, err := os.Stat(videoPath)
	if err != nil {
		return fmt.Errorf("final_video_missing: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("final_video_invalid: rendered MP4 is empty or not a regular file")
	}
	probe, err := service.ProbeMedia(ctx, MediaProbeRequest{Path: videoPath})
	if err != nil {
		return fmt.Errorf("final_video_probe_failed: %w", err)
	}
	if !probe.FFProbeAvailable {
		return errors.New("final_video_probe_unavailable: ffprobe is required before delivery")
	}
	if probe.VideoCodec == "" || probe.DurationMS <= 0 || probe.Width <= 0 || probe.Height <= 0 {
		return fmt.Errorf("final_video_not_decodable: format=%q codec=%q duration_ms=%d dimensions=%dx%d", probe.Format, probe.VideoCodec, probe.DurationMS, probe.Width, probe.Height)
	}
	format := strings.ToLower(probe.Format)
	if format != "" && !strings.Contains(format, "mp4") && !strings.Contains(format, "mov") {
		return fmt.Errorf("final_video_container_mismatch: ffprobe format=%q", probe.Format)
	}
	return nil
}

func applyDirectorPatchAndRerender(ctx context.Context, service RenderService, request RecordingRenderPipelineRequest, renderRequest RenderRequest, renderResult RenderResult) (RenderResult, error) {
	createdAt := time.Now().UTC()
	if renderRequest.RecordingResultPackage != nil && !renderRequest.RecordingResultPackage.CreatedAt.IsZero() {
		createdAt = renderRequest.RecordingResultPackage.CreatedAt
	}
	patchedPlan, applyResult := ApplyDirectorEditPlanPatch(renderResult.DemoEditPlan, renderResult.DirectorEditPlanPatch, renderResult.DirectorEditPlanPatchValidation, createdAt)
	if !applyResult.Applied || !applyResult.RerenderRequested {
		return persistDirectorPatchApplyResult(request.RenderOutputDir, renderResult, applyResult)
	}
	reportPipelineProgress(request, "applying_director_patch", "Applying validated director captions and shot hints, then re-rendering from captured assets.", 96)
	patchedRequest := renderRequest
	patchedRequest.EditPlan = &patchedPlan
	patchedRenderResult, err := service.Render(ctx, patchedRequest)
	if err != nil {
		applyResult = MarkDirectorPatchApplyRerenderFailed(applyResult, err)
		withApplyResult, persistErr := persistDirectorPatchApplyResult(request.RenderOutputDir, renderResult, applyResult)
		if persistErr != nil {
			return withApplyResult, persistErr
		}
		return withApplyResult, nil
	}
	patchedRenderResult = carryDirectorArtifacts(renderResult, patchedRenderResult)
	applyResult = MarkDirectorPatchApplyRerendered(applyResult, patchedRenderResult)
	return persistDirectorPatchApplyResult(request.RenderOutputDir, patchedRenderResult, applyResult)
}

func carryDirectorArtifacts(from RenderResult, to RenderResult) RenderResult {
	to.DirectorInputPath = from.DirectorInputPath
	to.DirectorInput = from.DirectorInput
	to.DirectorEditSuggestionPath = from.DirectorEditSuggestionPath
	to.DirectorEditSuggestion = from.DirectorEditSuggestion
	to.DirectorEditValidationPath = from.DirectorEditValidationPath
	to.DirectorEditValidation = from.DirectorEditValidation
	to.DirectorEditPlanPatchPath = from.DirectorEditPlanPatchPath
	to.DirectorEditPlanPatch = from.DirectorEditPlanPatch
	to.DirectorEditPlanPatchValidationPath = from.DirectorEditPlanPatchValidationPath
	to.DirectorEditPlanPatchValidation = from.DirectorEditPlanPatchValidation
	to.ArkMediaDryRunPlanPath = from.ArkMediaDryRunPlanPath
	to.ArkMediaDryRunPlan = from.ArkMediaDryRunPlan
	to.ArkAssetPublicationPlanPath = from.ArkAssetPublicationPlanPath
	to.ArkAssetPublicationPlan = from.ArkAssetPublicationPlan
	to.ArkAssetPublicationResultPath = from.ArkAssetPublicationResultPath
	to.ArkAssetPublicationResult = from.ArkAssetPublicationResult
	to.ArkMediaGenerationResultPath = from.ArkMediaGenerationResultPath
	to.ArkMediaGenerationResult = from.ArkMediaGenerationResult
	to.CandidateAssetReviewPath = from.CandidateAssetReviewPath
	to.CandidateAssetReview = from.CandidateAssetReview
	to.CandidateAssetEditPatchPath = from.CandidateAssetEditPatchPath
	to.CandidateAssetEditPatch = from.CandidateAssetEditPatch
	return to
}

func persistDirectorPatchApplyResult(outputDir string, renderResult RenderResult, applyResult model.DirectorEditPlanPatchApplyResult) (RenderResult, error) {
	if strings.TrimSpace(outputDir) == "" {
		outputDir = "."
	}
	path := outputDir + string(os.PathSeparator) + "director_edit_plan_patch_apply_result.json"
	if err := writeIndentedJSONFile(path, applyResult); err != nil {
		return renderResult, err
	}
	renderResult.DirectorEditPlanPatchApplyResult = &applyResult
	renderResult.DirectorEditPlanPatchApplyResultPath = path
	return renderResult, nil
}

func reportPipelineProgress(request RecordingRenderPipelineRequest, stage string, message string, progress int) {
	if request.Progress != nil {
		request.Progress(stage, message, progress)
	}
}

// AttachRenderResultArtifacts makes renderer output part of the same delivery
// package as the recording evidence, regardless of which approved runtime
// produced that recording.
func AttachRenderResultArtifacts(source *model.ClientExecutionPackage, result *model.RecordingResultPackage, renderResult RenderResult, createdAt time.Time) {
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
		{path: renderResult.SourceReferenceVideoPath, kind: "source_reference_video", mimeFallback: "video/mp4", role: "model_reference_video"},
		{path: renderResult.StepByStepDocsPath, kind: "step_by_step_docs", mimeFallback: "text/markdown", role: "step_by_step_docs"},
		{path: renderResult.AssetTimelineCatalogPath, kind: "asset_timeline_catalog", mimeFallback: "application/json", role: "render_metadata"},
		{path: renderResult.DemoEditPlanPath, kind: "demo_edit_plan", mimeFallback: "application/json", role: "render_plan"},
		{path: renderResult.DirectorInputPath, kind: "director_input", mimeFallback: "application/json", role: "model_director_input"},
		{path: renderResult.DirectorEditSuggestionPath, kind: "director_edit_suggestion", mimeFallback: "application/json", role: "model_director_suggestion"},
		{path: renderResult.DirectorEditValidationPath, kind: "director_edit_suggestion_validation", mimeFallback: "application/json", role: "model_director_suggestion_validation"},
		{path: renderResult.DirectorEditPlanPatchPath, kind: "director_edit_plan_patch", mimeFallback: "application/json", role: "model_director_edit_plan_patch"},
		{path: renderResult.DirectorEditPlanPatchValidationPath, kind: "director_edit_plan_patch_validation", mimeFallback: "application/json", role: "model_director_edit_plan_patch_validation"},
		{path: renderResult.DirectorEditPlanPatchApplyResultPath, kind: "director_edit_plan_patch_apply_result", mimeFallback: "application/json", role: "model_director_edit_plan_patch_apply_result"},
		{path: renderResult.ArkMediaDryRunPlanPath, kind: "ark_media_dry_run_plan", mimeFallback: "application/json", role: "media_generation_plan"},
		{path: renderResult.ArkAssetPublicationPlanPath, kind: "ark_asset_publication_plan", mimeFallback: "application/json", role: "media_asset_publication_plan"},
		{path: renderResult.ArkAssetPublicationResultPath, kind: "ark_asset_publication_result", mimeFallback: "application/json", role: "media_asset_publication_result"},
		{path: renderResult.ArkMediaGenerationResultPath, kind: "ark_media_generation_result", mimeFallback: "application/json", role: "media_generation_result"},
		{path: renderResult.CandidateAssetReviewPath, kind: "candidate_asset_review", mimeFallback: "application/json", role: "candidate_asset_review"},
		{path: renderResult.CandidateAssetEditPatchPath, kind: "candidate_asset_edit_plan_patch", mimeFallback: "application/json", role: "candidate_asset_edit_plan_patch"},
		{path: renderResult.ValidationReportPath, kind: "demo_edit_plan_validation", mimeFallback: "application/json", role: "render_validation"},
		{path: renderResult.RenderManifestPath, kind: "render_manifest", mimeFallback: "application/json", role: "render_manifest"},
		{path: renderResult.MediaNormalizationReportPath, kind: "media_normalization_report", mimeFallback: "application/json", role: "media_normalization"},
		{path: renderResult.RequirementReportPath, kind: "requirement_satisfaction_report", mimeFallback: "application/json", role: "requirement_satisfaction"},
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
		sha, size := localFileDigest(spec.path)
		artifacts = append(artifacts, model.ArtifactRef{
			ID:        artifactID(source.PackageID, spec.kind, 1),
			Kind:      spec.kind,
			URI:       spec.path,
			MimeType:  mimeTypeForPath(spec.path, spec.mimeFallback),
			SHA256:    sha,
			SizeBytes: size,
			CreatedAt: createdAt,
			Metadata:  metadata,
		})
	}
	if renderResult.ArkMediaGenerationResult != nil {
		if len(renderResult.ArkMediaGenerationResult.DownloadedArtifacts) > 0 {
			artifacts = append(artifacts, renderResult.ArkMediaGenerationResult.DownloadedArtifacts...)
		} else {
			artifacts = append(artifacts, renderResult.ArkMediaGenerationResult.CandidateArtifacts...)
		}
	}
	return artifacts
}

func localFileDigest(path string) (string, int64) {
	if path == "" {
		return "", 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), int64(len(data))
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
