package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	if err := validateBrowserAgentEvidenceMaster(ctx, service, source, recording); err != nil {
		return RenderRequest{}, RenderResult{}, err
	}
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
	if err := validateFinalMP4Delivery(ctx, service, renderRequest.RenderProfile, renderResult); err != nil {
		return RenderRequest{}, RenderResult{}, err
	}
	if renderResult.DeliveryStatus == "complete" {
		if err := validateDualFinalMP4Delivery(ctx, service, renderResult); err != nil {
			return RenderRequest{}, RenderResult{}, err
		}
	}
	AttachRenderResultArtifacts(source, recording, renderResult, recording.CreatedAt)
	reportPipelineProgress(request, "quality_validation", "Validating required-step coverage, media decodability, redaction, and output checksums.", 98)
	return renderRequest, renderResult, nil
}

func validateDualFinalMP4Delivery(ctx context.Context, service DeliveryRenderService, result RenderResult) error {
	if result.DeliveryStatus != "complete" {
		return errors.New("dual_delivery_incomplete: delivery status is not complete")
	}
	if len(result.Deliverables) != 2 {
		return fmt.Errorf("dual_delivery_incomplete: got %d deliverables, want 2", len(result.Deliverables))
	}
	seen := map[string]bool{}
	for _, deliverable := range result.Deliverables {
		if deliverable.Status != "complete" || deliverable.VideoPath == "" {
			return fmt.Errorf("dual_delivery_incomplete: deliverable %q is not complete", deliverable.ID)
		}
		if seen[deliverable.ID] {
			return fmt.Errorf("dual_delivery_invalid: duplicate deliverable %q", deliverable.ID)
		}
		seen[deliverable.ID] = true
		if err := validateRequiredDualDeliveryProfile(deliverable); err != nil {
			return err
		}
		if err := validateFinalMP4Delivery(ctx, service, &deliverable.Profile, RenderResult{VideoPath: deliverable.VideoPath}); err != nil {
			return fmt.Errorf("dual_delivery_%s: %w", deliverable.ID, err)
		}
	}
	for _, id := range []string{"final_master_2k", "final_delivery_1080p"} {
		if !seen[id] {
			return fmt.Errorf("dual_delivery_incomplete: missing %s", id)
		}
	}
	return nil
}

func validateRequiredDualDeliveryProfile(deliverable RenderDeliverable) error {
	expected, ok := requiredDualDeliveryProfiles()[deliverable.ID]
	if !ok {
		return fmt.Errorf("dual_delivery_invalid: unexpected deliverable %q", deliverable.ID)
	}
	profile := deliverable.Profile
	if profile.Width != expected.Width || profile.Height != expected.Height || profile.FPS != expected.FPS || !strings.EqualFold(strings.TrimSpace(profile.Format), expected.Format) {
		return fmt.Errorf("dual_delivery_profile_mismatch: %s got=%dx%d@%d/%s want=%dx%d@%d/%s", deliverable.ID, profile.Width, profile.Height, profile.FPS, profile.Format, expected.Width, expected.Height, expected.FPS, expected.Format)
	}
	return nil
}

func requiredDualDeliveryProfiles() map[string]model.EditorRenderProfile {
	return map[string]model.EditorRenderProfile{
		model.MediaOutputProfileMaster2K: {
			ID: model.MediaOutputProfileMaster2K, Width: 2560, Height: 1440, FPS: 30, Format: "mp4",
		},
		model.MediaOutputProfileDelivery1080: {
			ID: model.MediaOutputProfileDelivery1080, Width: 1920, Height: 1080, FPS: 30, Format: "mp4",
		},
	}
}

func validateBrowserAgentEvidenceMaster(ctx context.Context, service DeliveryRenderService, source *model.ClientExecutionPackage, recording *model.RecordingResultPackage) error {
	if source.ExecutableScriptBundle == nil || source.ExecutableScriptBundle.ScriptManifest.Runtime != model.ExecutableScriptRuntimeBrowserAgentOutlineV1 {
		return nil
	}
	var raw *model.ArtifactRef
	for index := range recording.GeneratedAssets {
		if strings.EqualFold(strings.TrimSpace(recording.GeneratedAssets[index].Kind), "raw_recording") {
			raw = &recording.GeneratedAssets[index]
			break
		}
	}
	if raw == nil {
		return errors.New("recording_evidence_missing: browser-agent delivery requires a raw_recording evidence master")
	}
	path := ""
	if raw.Metadata != nil {
		if localPath, ok := raw.Metadata["local_path"].(string); ok {
			path = strings.TrimSpace(localPath)
		}
	}
	if path == "" {
		path = strings.TrimSpace(raw.URI)
	}
	path = normalizedArtifactURI(path)
	if !filepath.IsAbs(path) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("recording_evidence_not_local: raw recording path could not be resolved: %w", err)
		}
		path = absolute
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("recording_evidence_not_local: raw recording is not available for quality validation: %w", err)
	}
	probe, err := service.ProbeMedia(ctx, MediaProbeRequest{Path: path})
	if err != nil {
		return fmt.Errorf("recording_evidence_probe_failed: %w", err)
	}
	if !probe.FFProbeAvailable {
		return errors.New("recording_evidence_probe_unavailable: ffprobe is required before editing browser evidence")
	}
	// Browser evidence keeps a 2K CSS viewport for stable layout/geometry while
	// the Worker records a capped 1080p performance profile. Requiring the CSS
	// viewport dimensions from the encoded WebM would reject every legitimate
	// capped capture and force expensive long-form 2K encoding.
	const expectedWidth, expectedHeight = 1920, 1080
	if probe.Width != expectedWidth || probe.Height != expectedHeight {
		return fmt.Errorf("recording_evidence_resolution_mismatch: got=%dx%d want=%dx%d; encoded evidence must match the 1080p capture profile", probe.Width, probe.Height, expectedWidth, expectedHeight)
	}
	if probe.DurationMS <= 0 || strings.TrimSpace(probe.VideoCodec) == "" {
		return fmt.Errorf("recording_evidence_not_decodable: codec=%q duration_ms=%d", probe.VideoCodec, probe.DurationMS)
	}
	// Playwright's encoded WebM capture uses a stable 25fps cadence, while
	// imported/native Browser Agent sources may already be 30fps. Both are
	// deterministic source masters; the renderer remains responsible for the
	// required CFR30 delivery. Reject other or variable-looking cadences here.
	if probe.FPS > 0 {
		stable25 := probe.FPS >= 24.75 && probe.FPS <= 25.25
		stable30 := probe.FPS >= 29.75 && probe.FPS <= 30.25
		if !stable25 && !stable30 {
			return fmt.Errorf("recording_evidence_fps_mismatch: got=%.3f want=25_or_30", probe.FPS)
		}
	}
	return nil
}

func validateFinalMP4Delivery(ctx context.Context, service DeliveryRenderService, expected *model.EditorRenderProfile, result RenderResult) error {
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
	if expected != nil {
		if expected.Width > 0 && expected.Height > 0 && (probe.Width != expected.Width || probe.Height != expected.Height) {
			return fmt.Errorf("final_video_resolution_mismatch: got=%dx%d want=%dx%d", probe.Width, probe.Height, expected.Width, expected.Height)
		}
		if expected.FPS > 0 && probe.FPS > 0 && (probe.FPS < float64(expected.FPS)-0.25 || probe.FPS > float64(expected.FPS)+0.25) {
			return fmt.Errorf("final_video_fps_mismatch: got=%.3f want=%d", probe.FPS, expected.FPS)
		}
	}
	if !strings.Contains(strings.ToLower(probe.VideoCodec), "h264") {
		return fmt.Errorf("final_video_codec_mismatch: got=%q want=h264", probe.VideoCodec)
	}
	if probe.PixelFormat != "" && !strings.EqualFold(probe.PixelFormat, "yuv420p") {
		return fmt.Errorf("final_video_pixel_format_mismatch: got=%q want=yuv420p", probe.PixelFormat)
	}
	format := strings.ToLower(probe.Format)
	if format != "" && !strings.Contains(format, "mp4") && !strings.Contains(format, "mov") {
		return fmt.Errorf("final_video_container_mismatch: ffprobe format=%q", probe.Format)
	}
	if result.RequirementReportPath != "" {
		var report struct {
			Status string `json:"status"`
			Errors []struct {
				Code string `json:"code"`
			} `json:"errors"`
		}
		if data, readErr := os.ReadFile(result.RequirementReportPath); readErr == nil {
			if unmarshalErr := json.Unmarshal(data, &report); unmarshalErr == nil {
				if strings.EqualFold(strings.TrimSpace(report.Status), "not_satisfied") || len(report.Errors) > 0 {
					code := "requirement_report_not_satisfied"
					if len(report.Errors) > 0 && strings.TrimSpace(report.Errors[0].Code) != "" {
						code = strings.TrimSpace(report.Errors[0].Code)
					}
					return fmt.Errorf("final_video_quality_gate: %s", code)
				}
			}
		}
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
	patchedRequest.ModelExecution = renderModelExecutionAudit(renderResult, applyResult)
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

func renderModelExecutionAudit(renderResult RenderResult, applyResult model.DirectorEditPlanPatchApplyResult) *RenderModelExecutionAudit {
	suggestion := renderResult.DirectorEditSuggestion
	if suggestion == nil {
		return nil
	}
	audit := &RenderModelExecutionAudit{
		Invoked: suggestion.Adapter.RealCallMade, Provider: suggestion.Adapter.Provider, Model: suggestion.Adapter.Model,
		PlanSource: "server_director", RealCallMade: suggestion.Adapter.RealCallMade,
		ProviderOutputAdopted: false, SuggestionOrigin: "deterministic_server_director",
		SuggestionID: suggestion.SuggestionID, PatchApplied: applyResult.Applied,
		Note: "Director suggestions are presentation-only; this audit separately records provider invocation and admission into the rendered edit plan.",
	}
	if renderResult.DirectorEditPlanPatch != nil {
		audit.PatchID = renderResult.DirectorEditPlanPatch.PatchID
	}
	if suggestion.ProviderCall != nil {
		audit.ProviderCallStatus = suggestion.ProviderCall.Status
		audit.RealCallMade = suggestion.ProviderCall.RealCallMade
		audit.Invoked = suggestion.ProviderCall.RealCallMade
		audit.Provider = suggestion.ProviderCall.Provider
		audit.Model = suggestion.ProviderCall.Model
		audit.RequestTraceID = suggestion.ProviderCall.TaskID
	}
	if applyResult.Applied {
		audit.AdoptedShotIDs = append(audit.AdoptedShotIDs, applyResult.AppliedShotIDs...)
	}
	return audit
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
	descriptors := assetDescriptorsFromArtifacts(source, artifacts, createdAt)
	if result.Delivery.RecipientKind == "local_test_only" {
		waiverID, _ := result.Delivery.ResultPackageRef.Metadata["waiver_id"].(string)
		for index := range descriptors {
			descriptors[index].Encrypted = false
			descriptors[index].RecipientKeyID = ""
			if descriptors[index].Metadata == nil {
				descriptors[index].Metadata = map[string]any{}
			}
			descriptors[index].Metadata["dev_test_only"] = true
			descriptors[index].Metadata["not_for_exchange_upload"] = true
			descriptors[index].Metadata["waiver_id"] = waiverID
		}
	}
	result.Delivery.AssetRefs = appendUniqueArtifactDescriptors(result.Delivery.AssetRefs, descriptors...)
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
		{path: renderResult.DeliveryManifestPath, kind: "deliverables_manifest", mimeFallback: "application/json", role: "deliverables_manifest"},
		{path: renderResult.MediaNormalizationReportPath, kind: "media_normalization_report", mimeFallback: "application/json", role: "media_normalization"},
		{path: renderResult.RequirementReportPath, kind: "requirement_satisfaction_report", mimeFallback: "application/json", role: "requirement_satisfaction"},
	}
	for _, deliverable := range renderResult.Deliverables {
		if strings.TrimSpace(deliverable.VideoPath) == "" {
			continue
		}
		specs = append(specs, renderArtifactSpec{
			path:          deliverable.VideoPath,
			kind:          "final_video_" + deliverable.ID,
			mimeFallback:  "video/mp4",
			role:          deliverable.ID,
			includeInDemo: true,
		})
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
