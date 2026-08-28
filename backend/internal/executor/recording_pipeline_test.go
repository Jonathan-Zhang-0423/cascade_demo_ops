package executor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestRunClientExecutionRecordingAndRenderCallsExecutorInProtocolOrder(t *testing.T) {
	t.Setenv("CASCADE_ARK_MEDIA_MODE", "dry_run")
	t.Setenv("CASCADE_ARK_ASSET_PUBLIC_DIR", "")
	t.Setenv("CASCADE_ARK_ASSET_PUBLIC_BASE_URL", "")
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	now := time.Date(2026, 7, 9, 20, 0, 0, 0, time.UTC)
	renderDir := t.TempDir()
	writeTestFile(t, filepath.Join(renderDir, "final.mp4"), "final")
	writeTestFile(t, filepath.Join(renderDir, "source_reference.mp4"), "source")
	writeTestFile(t, filepath.Join(renderDir, "steps.md"), "# Steps\n")
	writeTestFile(t, filepath.Join(renderDir, "asset_timeline_catalog.json"), "{}\n")
	writeTestFile(t, filepath.Join(renderDir, "demo_edit_plan.json"), "{}\n")
	writeTestFile(t, filepath.Join(renderDir, "render_manifest.json"), "{}\n")
	writeTestFile(t, filepath.Join(renderDir, "media_normalization_report.json"), "{}\n")
	writeTestFile(t, filepath.Join(renderDir, "requirement_satisfaction_report.json"), "{}\n")
	sourceRange := model.MillisecondRange{0, 1200}
	service := &fakeRecordingRenderService{
		recordResult: RecordResult{
			RecordingPath:   "artifacts/recording/job_1/recording.webm",
			ScreenshotPaths: []string{"artifacts/recording/job_1/step-001.png"},
			TracePath:       "artifacts/recording/job_1/trace.zip",
			WorkerID:        "worker_1",
			RuntimeVersions: map[string]string{"runner": "test"},
			StartedAt:       now.Add(-time.Second),
			CompletedAt:     now,
		},
		renderResult: RenderResult{
			VideoPath:                    filepath.Join(renderDir, "final.mp4"),
			SourceReferenceVideoPath:     filepath.Join(renderDir, "source_reference.mp4"),
			StepByStepDocsPath:           filepath.Join(renderDir, "steps.md"),
			AssetTimelineCatalogPath:     filepath.Join(renderDir, "asset_timeline_catalog.json"),
			DemoEditPlanPath:             filepath.Join(renderDir, "demo_edit_plan.json"),
			RenderManifestPath:           filepath.Join(renderDir, "render_manifest.json"),
			MediaNormalizationReportPath: filepath.Join(renderDir, "media_normalization_report.json"),
			RequirementReportPath:        filepath.Join(renderDir, "requirement_satisfaction_report.json"),
			DemoEditPlan: &model.DemoEditPlan{
				SchemaVersion:        model.DemoEditPlanSchemaVersion,
				PlanID:               "plan_1",
				SourceAuthority:      model.DemoEditSourceAuthorityCustomerSideAgent,
				ModelRole:            model.DemoEditModelRolePresentationOptimizerOnly,
				SourceMaterialPolicy: model.DemoEditSourceMaterialPolicyExistingAssetsOnly,
				ScriptOrderPolicy:    model.DemoEditScriptOrderPolicyPreserveRequiredStepOrder,
				LockedFields:         model.DemoEditRequiredLockedFields,
				ModelEditableFields:  model.DemoEditAllowedModelEditableFields,
				Shots: []model.DemoEditShot{{
					ID:                "shot_001_node_start",
					SourceArtifactID:  "artifact_raw_recording",
					SourceStepID:      "node_start",
					SourceTimeRangeMS: &sourceRange,
					Purpose:           "Base product state",
				}},
			},
		},
	}

	result, err := RunClientExecutionRecordingAndRender(t.Context(), service, RecordingRenderPipelineRequest{
		SourcePackage:      &pkg,
		CloudJobID:         "job_1",
		RecordingOutputDir: "artifacts/recording/job_1",
		RenderOutputDir:    renderDir,
		ResultCreatedAt:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(service.calls) != 4 || service.calls[0] != "record" || service.calls[1] != "render" || service.calls[2] != "render" || service.calls[3] != "probe_media" {
		t.Fatalf("expected record, initial render, patched render, then media probe calls, got %+v", service.calls)
	}
	if service.recordRequest.SourcePackageID != pkg.PackageID || service.recordRequest.ExecutableScriptBundle == nil {
		t.Fatalf("record request did not use protocol package: %+v", service.recordRequest)
	}
	if service.recordRequest.RecordingMode != RecordingModePlaywright {
		t.Fatalf("pipeline should default to real Playwright recording, got %q", service.recordRequest.RecordingMode)
	}
	if result.RecordingResultPackage.SourcePackageID != pkg.PackageID || result.RecordingResultPackage.CloudJobID != "job_1" {
		t.Fatalf("recording result package identity mismatch: %+v", result.RecordingResultPackage)
	}
	if got := countArtifactsByKind(result.RecordingResultPackage.GeneratedAssets, "demo_video"); got != 1 {
		t.Fatalf("expected final demo video artifact in result package, got %d: %+v", got, result.RecordingResultPackage.GeneratedAssets)
	}
	demoVideo := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "demo_video")
	if demoVideo == nil || demoVideo.URI != filepath.Join(renderDir, "final.mp4") || demoVideo.Metadata["asset_role"] != "final_demo" || demoVideo.Metadata["include_in_demo"] != true {
		t.Fatalf("unexpected demo video artifact metadata: %+v", demoVideo)
	}
	sourceReference := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "source_reference_video")
	if sourceReference == nil || sourceReference.URI != filepath.Join(renderDir, "source_reference.mp4") || sourceReference.Metadata["asset_role"] != "model_reference_video" || sourceReference.Metadata["include_in_demo"] != false {
		t.Fatalf("unexpected source reference artifact metadata: %+v", sourceReference)
	}
	normalizationReport := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "media_normalization_report")
	if normalizationReport == nil || normalizationReport.URI != filepath.Join(renderDir, "media_normalization_report.json") || normalizationReport.Metadata["asset_role"] != "media_normalization" || normalizationReport.Metadata["include_in_demo"] != false {
		t.Fatalf("unexpected media normalization artifact metadata: %+v", normalizationReport)
	}
	requirementReport := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "requirement_satisfaction_report")
	if requirementReport == nil || requirementReport.URI != filepath.Join(renderDir, "requirement_satisfaction_report.json") || requirementReport.Metadata["asset_role"] != "requirement_satisfaction" || requirementReport.Metadata["include_in_demo"] != false {
		t.Fatalf("unexpected requirement satisfaction artifact metadata: %+v", requirementReport)
	}
	directorInputArtifact := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "director_input")
	if directorInputArtifact == nil || directorInputArtifact.Metadata["asset_role"] != "model_director_input" || directorInputArtifact.Metadata["include_in_demo"] != false {
		t.Fatalf("unexpected director input artifact metadata: %+v", directorInputArtifact)
	}
	arkPlanArtifact := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "ark_media_dry_run_plan")
	if arkPlanArtifact == nil || arkPlanArtifact.Metadata["asset_role"] != "media_generation_plan" || arkPlanArtifact.Metadata["include_in_demo"] != false {
		t.Fatalf("unexpected Ark media dry-run artifact metadata: %+v", arkPlanArtifact)
	}
	directorSuggestionArtifact := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "director_edit_suggestion")
	if directorSuggestionArtifact == nil || directorSuggestionArtifact.Metadata["asset_role"] != "model_director_suggestion" || directorSuggestionArtifact.Metadata["include_in_demo"] != false {
		t.Fatalf("unexpected director edit suggestion artifact metadata: %+v", directorSuggestionArtifact)
	}
	directorSuggestionValidationArtifact := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "director_edit_suggestion_validation")
	if directorSuggestionValidationArtifact == nil || directorSuggestionValidationArtifact.Metadata["asset_role"] != "model_director_suggestion_validation" || directorSuggestionValidationArtifact.Metadata["include_in_demo"] != false {
		t.Fatalf("unexpected director edit suggestion validation artifact metadata: %+v", directorSuggestionValidationArtifact)
	}
	directorPatchArtifact := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "director_edit_plan_patch")
	if directorPatchArtifact == nil || directorPatchArtifact.Metadata["asset_role"] != "model_director_edit_plan_patch" || directorPatchArtifact.Metadata["include_in_demo"] != false {
		t.Fatalf("unexpected director edit plan patch artifact metadata: %+v", directorPatchArtifact)
	}
	directorPatchValidationArtifact := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "director_edit_plan_patch_validation")
	if directorPatchValidationArtifact == nil || directorPatchValidationArtifact.Metadata["asset_role"] != "model_director_edit_plan_patch_validation" || directorPatchValidationArtifact.Metadata["include_in_demo"] != false {
		t.Fatalf("unexpected director edit plan patch validation artifact metadata: %+v", directorPatchValidationArtifact)
	}
	directorPatchApplyArtifact := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "director_edit_plan_patch_apply_result")
	if directorPatchApplyArtifact == nil || directorPatchApplyArtifact.Metadata["asset_role"] != "model_director_edit_plan_patch_apply_result" || directorPatchApplyArtifact.Metadata["include_in_demo"] != false {
		t.Fatalf("unexpected director edit plan patch apply result artifact metadata: %+v", directorPatchApplyArtifact)
	}
	arkPublicationPlanArtifact := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "ark_asset_publication_plan")
	if arkPublicationPlanArtifact == nil || arkPublicationPlanArtifact.Metadata["asset_role"] != "media_asset_publication_plan" || arkPublicationPlanArtifact.Metadata["include_in_demo"] != false {
		t.Fatalf("unexpected Ark asset publication artifact metadata: %+v", arkPublicationPlanArtifact)
	}
	arkPublicationResultArtifact := findPipelineArtifact(result.RecordingResultPackage.GeneratedAssets, "ark_asset_publication_result")
	if arkPublicationResultArtifact == nil || arkPublicationResultArtifact.Metadata["asset_role"] != "media_asset_publication_result" || arkPublicationResultArtifact.Metadata["include_in_demo"] != false {
		t.Fatalf("unexpected Ark asset publication result artifact metadata: %+v", arkPublicationResultArtifact)
	}
	finalVideoDescriptor := findPackageDescriptor(result.RecordingResultPackage.Delivery.AssetRefs, "artifact_pkg_1_demo_video_001")
	if finalVideoDescriptor == nil || finalVideoDescriptor.Role != model.ArtifactRoleFinalDemoVideo || finalVideoDescriptor.Kind != model.ArtifactKindVideo || !finalVideoDescriptor.Encrypted || !finalVideoDescriptor.Sensitive {
		t.Fatalf("expected encrypted final demo video delivery descriptor, got %+v", finalVideoDescriptor)
	}
	sourceReferenceDescriptor := findPackageDescriptor(result.RecordingResultPackage.Delivery.AssetRefs, "artifact_pkg_1_source_reference_video_001")
	if sourceReferenceDescriptor == nil || sourceReferenceDescriptor.Role != model.ArtifactRoleRecordingOutput || sourceReferenceDescriptor.MimeType != "video/mp4" {
		t.Fatalf("expected source reference video delivery descriptor, got %+v", sourceReferenceDescriptor)
	}
	if service.renderRequest.RecordingResultPackage == nil || service.renderRequest.RecordingResultPackage.SourcePackageID != pkg.PackageID {
		t.Fatalf("render request must consume recording result package: %+v", service.renderRequest)
	}
	if service.renderRequest.RecordingRunSpec == nil || service.renderRequest.RecordingRunSpec.RunID != pkg.RecordingRunSpec.RunID {
		t.Fatalf("render request must preserve recording_run_spec for requirement checks: %+v", service.renderRequest.RecordingRunSpec)
	}
	if service.renderRequest.OutputDir != renderDir || result.RenderResult.VideoPath == "" {
		t.Fatalf("unexpected render output: request=%+v result=%+v", service.renderRequest, result.RenderResult)
	}
	if result.RenderResult.DirectorInput == nil || result.RenderResult.DirectorInputPath == "" {
		t.Fatalf("director input was not generated: %+v", result.RenderResult)
	}
	if result.RenderResult.DirectorInput.SourceMaterialPolicy != model.DemoEditSourceMaterialPolicyExistingAssetsOnly || !result.RenderResult.DirectorInput.StorylinePolicy.PreserveStepOrder {
		t.Fatalf("director input lost source-only or script-order policy: %+v", result.RenderResult.DirectorInput)
	}
	if result.RenderResult.DirectorInput.Materials.SourceReferenceVideo == nil || result.RenderResult.DirectorInput.Materials.SourceReferenceVideo.MimeType != "video/mp4" {
		t.Fatalf("director input missing source reference video: %+v", result.RenderResult.DirectorInput.Materials)
	}
	if result.RenderResult.ArkMediaDryRunPlan == nil || result.RenderResult.ArkMediaDryRunPlan.VideoModel != "doubao-seedance-2-0-260128" {
		t.Fatalf("Ark media dry-run plan did not use Seedance 2.0 model: %+v", result.RenderResult.ArkMediaDryRunPlan)
	}
	if result.RenderResult.DirectorEditSuggestion == nil || result.RenderResult.DirectorEditSuggestion.Adapter.RealCallMade {
		t.Fatalf("director edit suggestion should be generated by dry-run adapter: %+v", result.RenderResult.DirectorEditSuggestion)
	}
	if result.RenderResult.DirectorEditSuggestion.ProviderGate == nil || result.RenderResult.DirectorEditSuggestion.ProviderGate.Status != "dry_run" || result.RenderResult.DirectorEditSuggestion.ProviderGate.CanAttemptRealCall {
		t.Fatalf("director edit suggestion should expose dry-run provider gate: %+v", result.RenderResult.DirectorEditSuggestion.ProviderGate)
	}
	if result.RenderResult.DirectorEditValidation == nil || !result.RenderResult.DirectorEditValidation.Valid {
		t.Fatalf("director edit suggestion validation should pass: %+v", result.RenderResult.DirectorEditValidation)
	}
	if result.RenderResult.DirectorEditPlanPatch == nil || result.RenderResult.DirectorEditPlanPatch.Status == "" {
		t.Fatalf("director edit plan patch should be generated: %+v", result.RenderResult.DirectorEditPlanPatch)
	}
	if result.RenderResult.DirectorEditPlanPatchValidation == nil || !result.RenderResult.DirectorEditPlanPatchValidation.Valid {
		t.Fatalf("director edit plan patch validation should pass: %+v", result.RenderResult.DirectorEditPlanPatchValidation)
	}
	if result.RenderResult.DirectorEditPlanPatchApplyResult == nil || !result.RenderResult.DirectorEditPlanPatchApplyResult.Applied || !result.RenderResult.DirectorEditPlanPatchApplyResult.Rerendered {
		t.Fatalf("director edit plan patch should be applied through controlled rerender: %+v", result.RenderResult.DirectorEditPlanPatchApplyResult)
	}
	if service.renderRequest.EditPlan == nil || service.renderRequest.EditPlan.PlanID != "plan_1_director_applied" {
		t.Fatalf("second render should receive the patched demo edit plan: %+v", service.renderRequest.EditPlan)
	}
	if service.renderRequest.ModelExecution == nil || service.renderRequest.ModelExecution.PlanSource != "server_director" || !service.renderRequest.ModelExecution.PatchApplied || service.renderRequest.ModelExecution.ProviderOutputAdopted {
		t.Fatalf("second render must distinguish deterministic Director adoption from provider-output adoption: %+v", service.renderRequest.ModelExecution)
	}
	if len(service.renderRequest.ModelExecution.AdoptedShotIDs) == 0 {
		t.Fatalf("second render must record the exact Director-patched shots: %+v", service.renderRequest.ModelExecution)
	}
	if len(result.RenderResult.DemoEditPlan.Shots) == 0 || len(result.RenderResult.DemoEditPlan.Shots[0].Overlays) == 0 {
		t.Fatalf("patched final demo edit plan should include director caption overlays: %+v", result.RenderResult.DemoEditPlan)
	}
	if result.RenderResult.ArkAssetPublicationPlan == nil || result.RenderResult.ArkAssetPublicationPlan.Status != "needs_publication" {
		t.Fatalf("Ark asset publication plan was not generated: %+v", result.RenderResult.ArkAssetPublicationPlan)
	}
	if result.RenderResult.ArkAssetPublicationResult == nil || result.RenderResult.ArkAssetPublicationResult.CanUseForRealCall || !result.RenderResult.ArkAssetPublicationResult.ContainsDryRunRefs {
		t.Fatalf("Ark asset publication dry-run result should not be real-call ready: %+v", result.RenderResult.ArkAssetPublicationResult)
	}
	var persistedDirector model.DirectorInput
	readJSONFile(t, result.RenderResult.DirectorInputPath, &persistedDirector)
	if persistedDirector.SchemaVersion != model.DirectorInputSchemaVersion || persistedDirector.SourcePackageID != pkg.PackageID {
		t.Fatalf("unexpected persisted director input: %+v", persistedDirector)
	}
	var persistedSuggestion model.DirectorEditSuggestion
	readJSONFile(t, result.RenderResult.DirectorEditSuggestionPath, &persistedSuggestion)
	if persistedSuggestion.SchemaVersion != model.DirectorEditSuggestionSchemaVersion || persistedSuggestion.SourcePackageID != pkg.PackageID || persistedSuggestion.Adapter.RealCallMade {
		t.Fatalf("unexpected persisted director edit suggestion: %+v", persistedSuggestion)
	}
	var persistedSuggestionValidation model.DirectorEditSuggestionValidationReport
	readJSONFile(t, result.RenderResult.DirectorEditValidationPath, &persistedSuggestionValidation)
	if persistedSuggestionValidation.SchemaVersion != model.DirectorEditSuggestionValidationSchemaVersion || !persistedSuggestionValidation.Valid {
		t.Fatalf("unexpected persisted director edit suggestion validation: %+v", persistedSuggestionValidation)
	}
	var persistedDirectorPatch model.DirectorEditPlanPatch
	readJSONFile(t, result.RenderResult.DirectorEditPlanPatchPath, &persistedDirectorPatch)
	if persistedDirectorPatch.SchemaVersion != model.DirectorEditPlanPatchSchemaVersion || persistedDirectorPatch.SourcePackageID != pkg.PackageID {
		t.Fatalf("unexpected persisted director edit plan patch: %+v", persistedDirectorPatch)
	}
	var persistedDirectorPatchValidation model.DirectorEditPlanPatchValidationReport
	readJSONFile(t, result.RenderResult.DirectorEditPlanPatchValidationPath, &persistedDirectorPatchValidation)
	if persistedDirectorPatchValidation.SchemaVersion != model.DirectorEditPlanPatchValidationSchemaVersion || !persistedDirectorPatchValidation.Valid {
		t.Fatalf("unexpected persisted director edit plan patch validation: %+v", persistedDirectorPatchValidation)
	}
	var persistedDirectorPatchApply model.DirectorEditPlanPatchApplyResult
	readJSONFile(t, result.RenderResult.DirectorEditPlanPatchApplyResultPath, &persistedDirectorPatchApply)
	if persistedDirectorPatchApply.SchemaVersion != model.DirectorEditPlanPatchApplyResultSchemaVersion || !persistedDirectorPatchApply.Applied || !persistedDirectorPatchApply.Rerendered {
		t.Fatalf("unexpected persisted director edit plan patch apply result: %+v", persistedDirectorPatchApply)
	}
	var persistedPublication model.ArkAssetPublicationPlan
	readJSONFile(t, result.RenderResult.ArkAssetPublicationPlanPath, &persistedPublication)
	if persistedPublication.SchemaVersion != model.ArkAssetPublicationPlanSchemaVersion || persistedPublication.SourcePackageID != pkg.PackageID {
		t.Fatalf("unexpected persisted Ark asset publication plan: %+v", persistedPublication)
	}
	var persistedPublicationResult model.ArkAssetPublicationResult
	readJSONFile(t, result.RenderResult.ArkAssetPublicationResultPath, &persistedPublicationResult)
	if persistedPublicationResult.SchemaVersion != model.ArkAssetPublicationResultSchemaVersion || persistedPublicationResult.SourcePackageID != pkg.PackageID || persistedPublicationResult.CanUseForRealCall {
		t.Fatalf("unexpected persisted Ark asset publication result: %+v", persistedPublicationResult)
	}
}

func TestRunClientExecutionRecordingAndRenderAllowsRecordingModeOverride(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	now := time.Date(2026, 7, 9, 20, 15, 0, 0, time.UTC)
	renderDir := t.TempDir()
	videoPath := filepath.Join(renderDir, "final.mp4")
	writeTestFile(t, videoPath, "final")
	service := &fakeRecordingRenderService{
		recordResult: RecordResult{
			TracePath:       "artifacts/recording/job_1/script_execution_trace.json",
			GeneratedAssets: []model.ArtifactRef{{ID: "artifact_trace", Kind: "execution_trace", URI: "artifacts/recording/job_1/script_execution_trace.json", MimeType: "application/json", CreatedAt: now}},
			StepResults:     []model.StepResult{{NodeID: "node_start", Status: "passed"}},
			StartedAt:       now.Add(-time.Second),
			CompletedAt:     now,
		},
		renderResult: RenderResult{
			VideoPath:          videoPath,
			StepByStepDocsPath: filepath.Join(renderDir, "steps.md"),
		},
	}

	_, err := RunClientExecutionRecordingAndRender(t.Context(), service, RecordingRenderPipelineRequest{
		SourcePackage:      &pkg,
		CloudJobID:         "job_1",
		RecordingOutputDir: "artifacts/recording/job_1",
		RenderOutputDir:    renderDir,
		RecordingMode:      RecordingModeDryRun,
		ResultCreatedAt:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.recordRequest.RecordingMode != RecordingModeDryRun {
		t.Fatalf("expected dry-run override to reach worker request, got %q", service.recordRequest.RecordingMode)
	}
}

func TestRenderArtifactsFromResultIncludesArkMediaCandidateArtifacts(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	now := time.Date(2026, 7, 15, 19, 45, 0, 0, time.UTC)
	renderResult := RenderResult{
		ArkMediaGenerationResult: &model.ArkMediaGenerationResult{
			CandidateArtifacts: []model.ArtifactRef{{
				ID:        "artifact_pkg_1_generated_video_candidate_001",
				Kind:      "generated_video_candidate",
				URI:       "https://assets.example.com/generated/demo.mp4",
				MimeType:  "video/mp4",
				CreatedAt: now,
				Metadata: map[string]any{
					"asset_role":             "director_preview_candidate",
					"include_in_demo":        false,
					"source_material_policy": "non_authoritative_generated_candidate",
				},
			}},
		},
	}

	artifacts := renderArtifactsFromResult(&pkg, renderResult, now)

	candidate := findPipelineArtifact(artifacts, "generated_video_candidate")
	if candidate == nil || candidate.URI != "https://assets.example.com/generated/demo.mp4" || candidate.Metadata["include_in_demo"] != false {
		t.Fatalf("expected non-authoritative generated candidate artifact: %+v", artifacts)
	}
}

func TestRenderArtifactsFromResultPrefersDownloadedArkMediaCandidateArtifacts(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	now := time.Date(2026, 7, 15, 20, 10, 0, 0, time.UTC)
	renderResult := RenderResult{
		ArkMediaGenerationResult: &model.ArkMediaGenerationResult{
			CandidateArtifacts: []model.ArtifactRef{{
				ID:       "artifact_remote_candidate",
				Kind:     "generated_video_candidate",
				URI:      "https://assets.example.com/generated/demo.mp4",
				MimeType: "video/mp4",
			}},
			DownloadedArtifacts: []model.ArtifactRef{{
				ID:       "artifact_downloaded_candidate",
				Kind:     "generated_video_candidate",
				URI:      "artifacts/render/ark-media/001-director-preview-candidate.mp4",
				MimeType: "video/mp4",
				Metadata: map[string]any{
					"provider_output_url":    "https://assets.example.com/generated/demo.mp4",
					"include_in_demo":        false,
					"source_material_policy": "non_authoritative_generated_candidate",
				},
			}},
		},
	}

	artifacts := renderArtifactsFromResult(&pkg, renderResult, now)

	candidate := findPipelineArtifact(artifacts, "generated_video_candidate")
	if candidate == nil || candidate.ID != "artifact_downloaded_candidate" || strings.HasPrefix(candidate.URI, "https://") {
		t.Fatalf("expected downloaded candidate artifact to be preferred: %+v", artifacts)
	}
}

func TestRenderArtifactsFromResultIncludesCandidateAssetReviewArtifact(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	now := time.Date(2026, 7, 15, 21, 15, 0, 0, time.UTC)
	reviewPath := filepath.Join(t.TempDir(), "candidate_asset_review.json")
	writeTestFile(t, reviewPath, "{}\n")
	renderResult := RenderResult{CandidateAssetReviewPath: reviewPath}

	artifacts := renderArtifactsFromResult(&pkg, renderResult, now)

	review := findPipelineArtifact(artifacts, "candidate_asset_review")
	if review == nil || review.URI != reviewPath || review.Metadata["asset_role"] != "candidate_asset_review" || review.Metadata["include_in_demo"] != false {
		t.Fatalf("expected candidate asset review artifact: %+v", artifacts)
	}
}

func TestRenderArtifactsFromResultIncludesCandidateAssetEditPatchArtifact(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	now := time.Date(2026, 7, 15, 22, 15, 0, 0, time.UTC)
	patchPath := filepath.Join(t.TempDir(), "candidate_asset_edit_plan_patch.json")
	writeTestFile(t, patchPath, "{}\n")
	renderResult := RenderResult{CandidateAssetEditPatchPath: patchPath}

	artifacts := renderArtifactsFromResult(&pkg, renderResult, now)

	patch := findPipelineArtifact(artifacts, "candidate_asset_edit_plan_patch")
	if patch == nil || patch.URI != patchPath || patch.Metadata["asset_role"] != "candidate_asset_edit_plan_patch" || patch.Metadata["include_in_demo"] != false {
		t.Fatalf("expected candidate asset edit patch artifact: %+v", artifacts)
	}
}

func TestRunClientExecutionRecordingAndRenderStopsBeforeRenderOnRecordFailure(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	service := &fakeRecordingRenderService{recordErr: errors.New("record failed")}

	_, err := RunClientExecutionRecordingAndRender(t.Context(), service, RecordingRenderPipelineRequest{
		SourcePackage:      &pkg,
		CloudJobID:         "job_1",
		RecordingOutputDir: "artifacts/recording/job_1",
		RenderOutputDir:    "artifacts/render/job_1",
	})
	if err == nil {
		t.Fatal("expected record failure")
	}
	if len(service.calls) != 1 || service.calls[0] != "record" {
		t.Fatalf("expected only record call, got %+v", service.calls)
	}
}

func TestRunClientExecutionRecordingAndRenderReturnsFailedResultPackageWithoutRender(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	now := time.Date(2026, 7, 9, 20, 45, 0, 0, time.UTC)
	service := &fakeRecordingRenderService{
		recordResult: RecordResult{
			GeneratedAssets: []model.ArtifactRef{{
				ID:           "artifact_failure_screenshot_001",
				Kind:         "failure_screenshot",
				URI:          "file:///tmp/failure-step-001.png",
				MimeType:     "image/png",
				SHA256:       "failure_hash",
				Sensitive:    true,
				SourceNodeID: "node_start",
				CreatedAt:    now,
				Metadata:     map[string]any{"asset_role": "failure_screenshot", "include_in_demo": false},
			}, {
				ID:        "artifact_browser_trace",
				Kind:      "browser_trace",
				URI:       "file:///tmp/trace.zip",
				MimeType:  "application/zip",
				SHA256:    "trace_hash",
				CreatedAt: now,
			}},
			StepResults: []model.StepResult{{NodeID: "node_start", Status: "failed", ObservedState: "Timeout waiting for selector"}},
			FailureDiagnostic: &model.ScriptFailureDiagnostic{
				ID:              "diag_node_start",
				SchemaVersion:   model.ScriptFailureDiagnosticSchemaVersion,
				FailedNodeID:    "node_start",
				Error:           model.AgentError{Code: "selector_timeout", Message: "Timeout waiting for selector", Retryable: true},
				CurrentURL:      "https://app.example.com/dashboard",
				PageTitle:       "Dashboard",
				RedactionReport: model.DiagnosticRedactionReport{Applied: true, FullHTMLIncluded: false},
				CapturedAt:      now,
			},
			StartedAt:   now.Add(-time.Second),
			CompletedAt: now,
		},
	}

	result, err := RunClientExecutionRecordingAndRender(t.Context(), service, RecordingRenderPipelineRequest{
		SourcePackage:      &pkg,
		CloudJobID:         "job_1",
		RecordingOutputDir: "artifacts/recording/job_1",
		RenderOutputDir:    "artifacts/render/job_1",
		ResultCreatedAt:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(service.calls) != 1 || service.calls[0] != "record" {
		t.Fatalf("failed recording should stop before render, got calls %+v", service.calls)
	}
	if result.RecordingResultPackage.Status != model.RecordingResultStatusFailed || result.RecordingResultPackage.FailureDiagnostic == nil {
		t.Fatalf("expected failed recording result package, got %+v", result.RecordingResultPackage)
	}
}

func TestRunClientExecutionRecordingAndRenderRejectsMissingRenderOutput(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	service := &fakeRecordingRenderService{}

	if _, err := RunClientExecutionRecordingAndRender(t.Context(), service, RecordingRenderPipelineRequest{
		SourcePackage:      &pkg,
		CloudJobID:         "job_1",
		RecordingOutputDir: "artifacts/recording/job_1",
	}); err == nil {
		t.Fatal("expected render output dir validation error")
	}
}

func TestValidateFinalMP4DeliveryRejectsWebMAndUnavailableProbe(t *testing.T) {
	service := &fakeRecordingRenderService{}
	if err := validateFinalMP4Delivery(t.Context(), service, nil, RenderResult{VideoPath: filepath.Join(t.TempDir(), "final.webm")}); err == nil || !strings.Contains(err.Error(), "final_video_not_mp4") {
		t.Fatalf("expected WebM delivery rejection, got %v", err)
	}

	path := filepath.Join(t.TempDir(), "final.mp4")
	writeTestFile(t, path, "mp4 fixture")
	service.probeResult = MediaProbeResult{Path: path, Format: "mov,mp4", VideoCodec: "h264", DurationMS: 1000, Width: 1280, Height: 720, FFProbeAvailable: false}
	if err := validateFinalMP4Delivery(t.Context(), service, nil, RenderResult{VideoPath: path}); err == nil || !strings.Contains(err.Error(), "final_video_probe_unavailable") {
		t.Fatalf("expected ffprobe availability rejection, got %v", err)
	}
}

func TestValidateFinalMP4DeliveryEnforcesRequested2KProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "final.mp4")
	writeTestFile(t, path, "mp4 fixture")
	service := &fakeRecordingRenderService{probeResult: MediaProbeResult{
		Path: path, Format: "mov,mp4", VideoCodec: "h264", DurationMS: 1000,
		Width: 1920, Height: 1080, FPS: 30, PixelFormat: "yuv420p", FFProbeAvailable: true,
	}}
	profile := &model.EditorRenderProfile{Mode: "final", Width: 2560, Height: 1440, FPS: 30, Format: "mp4", CRF: 18}
	if err := validateFinalMP4Delivery(t.Context(), service, profile, RenderResult{VideoPath: path}); err == nil || !strings.Contains(err.Error(), "final_video_resolution_mismatch") {
		t.Fatalf("expected 1080p output to fail the 2K delivery gate, got %v", err)
	}
	service.probeResult.Width, service.probeResult.Height = 2560, 1440
	if err := validateFinalMP4Delivery(t.Context(), service, profile, RenderResult{VideoPath: path}); err != nil {
		t.Fatalf("expected canonical 2K H.264 output to pass, got %v", err)
	}
}

func TestValidateDualFinalMP4DeliveryRejectsSwappedProfileLabels(t *testing.T) {
	result := RenderResult{
		DeliveryStatus: "complete",
		Deliverables: []RenderDeliverable{
			{ID: model.MediaOutputProfileMaster2K, Status: "complete", VideoPath: "master.mp4", Profile: model.EditorRenderProfile{Width: 1920, Height: 1080, FPS: 30, Format: "mp4"}},
			{ID: model.MediaOutputProfileDelivery1080, Status: "complete", VideoPath: "delivery.mp4", Profile: model.EditorRenderProfile{Width: 2560, Height: 1440, FPS: 30, Format: "mp4"}},
		},
	}
	err := validateDualFinalMP4Delivery(t.Context(), &fakeRecordingRenderService{}, result)
	if err == nil || !strings.Contains(err.Error(), "dual_delivery_profile_mismatch: final_master_2k") {
		t.Fatalf("swapped profile labels must block dual delivery, got %v", err)
	}
}

func TestValidateFinalMP4DeliveryRejectsUnsatisfiedRequirementReport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "final.mp4")
	reportPath := filepath.Join(dir, "requirement_satisfaction_report.json")
	writeTestFile(t, path, "mp4 fixture")
	writeTestFile(t, reportPath, `{"status":"not_satisfied","errors":[{"code":"edit_plan_timeline_duration_mismatch"}]}`)
	service := &fakeRecordingRenderService{probeResult: MediaProbeResult{
		Path: path, Format: "mov,mp4", VideoCodec: "h264", DurationMS: 1000,
		Width: 1920, Height: 1080, FPS: 30, PixelFormat: "yuv420p", FFProbeAvailable: true,
	}}
	err := validateFinalMP4Delivery(t.Context(), service, nil, RenderResult{VideoPath: path, RequirementReportPath: reportPath})
	if err == nil || !strings.Contains(err.Error(), "final_video_quality_gate: edit_plan_timeline_duration_mismatch") {
		t.Fatalf("expected unsatisfied requirement report to block delivery, got %v", err)
	}
}

func TestValidateBrowserAgentEvidenceMasterRejectsUpscaledLowResolutionSource(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	pkg.ExecutableScriptBundle.ScriptManifest.Runtime = model.ExecutableScriptRuntimeBrowserAgentOutlineV1
	rawPath := filepath.Join(t.TempDir(), "recording.webm")
	writeTestFile(t, rawPath, "webm fixture")
	recording := &model.RecordingResultPackage{GeneratedAssets: []model.ArtifactRef{{
		ID: "raw_1", Kind: "raw_recording", URI: rawPath,
	}}}
	service := &fakeRecordingRenderService{probeResult: MediaProbeResult{
		Path: rawPath, Format: "matroska,webm", VideoCodec: "vp8", DurationMS: 1000,
		Width: 1440, Height: 900, FPS: 25, PixelFormat: "yuv420p", FFProbeAvailable: true,
	}}
	err := validateBrowserAgentEvidenceMaster(t.Context(), service, &pkg, recording)
	if err == nil || !strings.Contains(err.Error(), "recording_evidence_resolution_mismatch") {
		t.Fatalf("expected low-resolution Browser Agent evidence to be rejected, got %v", err)
	}
}

func TestValidateBrowserAgentEvidenceMasterAcceptsCanonical1080Capture(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	pkg.ExecutableScriptBundle.ScriptManifest.Runtime = model.ExecutableScriptRuntimeBrowserAgentOutlineV1
	rawPath := filepath.Join(t.TempDir(), "recording.webm")
	writeTestFile(t, rawPath, "webm fixture")
	recording := &model.RecordingResultPackage{GeneratedAssets: []model.ArtifactRef{{
		ID: "raw_1", Kind: "raw_recording", URI: rawPath,
	}}}
	service := &fakeRecordingRenderService{probeResult: MediaProbeResult{
		Path: rawPath, Format: "matroska,webm", VideoCodec: "vp8", DurationMS: 1000,
		Width: 1920, Height: 1080, FPS: 30, PixelFormat: "yuv420p", FFProbeAvailable: true,
	}}
	if err := validateBrowserAgentEvidenceMaster(t.Context(), service, &pkg, recording); err != nil {
		t.Fatalf("expected canonical 1080p Browser Agent capture to pass, got %v", err)
	}
}

func TestValidateBrowserAgentEvidenceMasterAcceptsPlaywright25FPSCapture(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	pkg.ExecutableScriptBundle.ScriptManifest.Runtime = model.ExecutableScriptRuntimeBrowserAgentOutlineV1
	rawPath := filepath.Join(t.TempDir(), "recording.webm")
	writeTestFile(t, rawPath, "webm fixture")
	recording := &model.RecordingResultPackage{GeneratedAssets: []model.ArtifactRef{{
		ID: "raw_1", Kind: "raw_recording", URI: rawPath,
	}}}
	service := &fakeRecordingRenderService{probeResult: MediaProbeResult{
		Path: rawPath, Format: "matroska,webm", VideoCodec: "vp8", DurationMS: 1000,
		Width: 1920, Height: 1080, FPS: 25, PixelFormat: "yuv420p", FFProbeAvailable: true,
	}}
	if err := validateBrowserAgentEvidenceMaster(t.Context(), service, &pkg, recording); err != nil {
		t.Fatalf("expected Playwright's stable 25fps Browser Agent capture to pass, got %v", err)
	}
}

func TestValidateBrowserAgentEvidenceMasterRejectsUnexpectedCaptureFPS(t *testing.T) {
	pkg := sampleClientExecutionPackageForExecutorTest(t)
	pkg.ExecutableScriptBundle.ScriptManifest.Runtime = model.ExecutableScriptRuntimeBrowserAgentOutlineV1
	rawPath := filepath.Join(t.TempDir(), "recording.webm")
	writeTestFile(t, rawPath, "webm fixture")
	recording := &model.RecordingResultPackage{GeneratedAssets: []model.ArtifactRef{{
		ID: "raw_1", Kind: "raw_recording", URI: rawPath,
	}}}
	service := &fakeRecordingRenderService{probeResult: MediaProbeResult{
		Path: rawPath, Format: "matroska,webm", VideoCodec: "vp8", DurationMS: 1000,
		Width: 1920, Height: 1080, FPS: 20, PixelFormat: "yuv420p", FFProbeAvailable: true,
	}}
	err := validateBrowserAgentEvidenceMaster(t.Context(), service, &pkg, recording)
	if err == nil || !strings.Contains(err.Error(), "recording_evidence_fps_mismatch") {
		t.Fatalf("expected unsupported Browser Agent capture cadence to be rejected, got %v", err)
	}
}

type fakeRecordingRenderService struct {
	calls          []string
	recordRequest  RecordRequest
	renderRequest  RenderRequest
	renderRequests []RenderRequest
	recordResult   RecordResult
	renderResult   RenderResult
	recordErr      error
	renderErr      error
	probeResult    MediaProbeResult
	probeErr       error
}

func (s *fakeRecordingRenderService) ProbeMedia(ctx context.Context, request MediaProbeRequest) (MediaProbeResult, error) {
	s.calls = append(s.calls, "probe_media")
	if s.probeErr != nil {
		return MediaProbeResult{}, s.probeErr
	}
	if s.probeResult.VideoCodec != "" || s.probeResult.FFProbeAvailable {
		return s.probeResult, nil
	}
	return MediaProbeResult{Path: request.Path, Format: "mov,mp4,m4a,3gp,3g2,mj2", DurationMS: 1200, VideoCodec: "h264", Width: 2560, Height: 1440, FPS: 30, PixelFormat: "yuv420p", FFProbeAvailable: true}, nil
}

func (s *fakeRecordingRenderService) Record(ctx context.Context, request RecordRequest) (RecordResult, error) {
	s.calls = append(s.calls, "record")
	s.recordRequest = request
	if s.recordErr != nil {
		return RecordResult{}, s.recordErr
	}
	return s.recordResult, nil
}

func (s *fakeRecordingRenderService) Render(ctx context.Context, request RenderRequest) (RenderResult, error) {
	s.calls = append(s.calls, "render")
	s.renderRequest = request
	s.renderRequests = append(s.renderRequests, request)
	if s.renderErr != nil {
		return RenderResult{}, s.renderErr
	}
	result := s.renderResult
	if request.EditPlan != nil {
		result.DemoEditPlan = request.EditPlan
	}
	return result, nil
}

func findPipelineArtifact(artifacts []model.ArtifactRef, kind string) *model.ArtifactRef {
	for index := range artifacts {
		if artifacts[index].Kind == kind {
			return &artifacts[index]
		}
	}
	return nil
}

func findPackageDescriptor(descriptors []model.PackageArtifactDescriptor, id string) *model.PackageArtifactDescriptor {
	for index := range descriptors {
		if descriptors[index].ID == id {
			return &descriptors[index]
		}
	}
	return nil
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSONFile(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatal(err)
	}
}
