package executor

import (
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestNewDirectorInputPreservesSourceOnlyBoundary(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	recording := model.RecordingResultPackage{
		ResultID:        "result_1",
		SourcePackageID: source.PackageID,
		ExecutionTrace: &model.ExecutionTrace{
			ID:              "trace_1",
			WorkflowGraphID: source.WorkflowGraph.ID,
			GraphVersion:    source.WorkflowGraph.Version,
			PassRate:        1,
			StepResults: []model.StepResult{{
				NodeID:        "node_start",
				Status:        "passed",
				DurationMS:    1200,
				ObservedState: "Dashboard loaded",
			}},
			Artifacts: []model.ArtifactRef{{
				ID:           "artifact_raw_recording",
				Kind:         "raw_recording",
				URI:          "artifacts/recording/raw.webm",
				MimeType:     "video/webm",
				SourceNodeID: "node_start",
				Metadata:     map[string]any{"include_in_demo": true, "asset_role": "raw_recording"},
			}},
		},
		GeneratedAssets: []model.ArtifactRef{{
			ID:           "artifact_screenshot",
			Kind:         "screenshot",
			URI:          "artifacts/recording/step-001.png",
			MimeType:     "image/png",
			SourceNodeID: "node_start",
			Metadata:     map[string]any{"include_in_demo": true, "asset_role": "primary"},
		}},
	}
	renderResult := RenderResult{
		VideoPath:                "artifacts/render/final.mp4",
		SourceReferenceVideoPath: "artifacts/render/source_reference.mp4",
		DemoEditPlan: &model.DemoEditPlan{
			PlanID:               "plan_1",
			SourceAuthority:      model.DemoEditSourceAuthorityCustomerSideAgent,
			ModelRole:            model.DemoEditModelRolePresentationOptimizerOnly,
			SourceMaterialPolicy: model.DemoEditSourceMaterialPolicyExistingAssetsOnly,
			ScriptOrderPolicy:    model.DemoEditScriptOrderPolicyPreserveRequiredStepOrder,
		},
	}

	input := NewDirectorInput(&source, &recording, renderResult, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))

	if input.SchemaVersion != model.DirectorInputSchemaVersion {
		t.Fatalf("schema version = %q", input.SchemaVersion)
	}
	if input.SourceMaterialPolicy != model.DemoEditSourceMaterialPolicyExistingAssetsOnly || input.ModelRole != model.DemoEditModelRolePresentationOptimizerOnly {
		t.Fatalf("director input lost collaboration boundary: %+v", input)
	}
	if !input.StorylinePolicy.PreserveStepOrder || len(input.StorylinePolicy.RequiredStepOrder) == 0 || input.StorylinePolicy.RequiredStepOrder[0] != "node_start" {
		t.Fatalf("director input must preserve script step order: %+v", input.StorylinePolicy)
	}
	cannotDo := strings.Join(input.ModelBoundaries.CannotDo, "\n")
	if !strings.Contains(cannotDo, "generate or replace product UI") || !strings.Contains(cannotDo, "create new browser actions") {
		t.Fatalf("director boundaries do not forbid UI hallucination or action changes: %+v", input.ModelBoundaries)
	}
	if input.Materials.SourceReferenceVideo == nil || input.Materials.SourceReferenceVideo.MimeType != "video/mp4" {
		t.Fatalf("source reference video was not promoted for model input: %+v", input.Materials)
	}
	if len(input.Materials.Screenshots) != 1 || input.Materials.Screenshots[0].SourceNodeID != "node_start" {
		t.Fatalf("screenshots were not preserved as source material: %+v", input.Materials.Screenshots)
	}
}

func TestNewArkMediaDryRunPlanUsesArkV3AndNoRealCall(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	input := model.DirectorInput{
		SourcePackageID: source.PackageID,
		Project:         model.DirectorProjectContext{ProductName: "Cascade"},
		Materials: model.DirectorMaterialSet{
			SourceReferenceVideo: &model.DirectorMaterialRef{ID: "source_reference", Kind: "source_reference_video", URI: "artifacts/render/source_reference.mp4", MimeType: "video/mp4"},
			Screenshots:          []model.DirectorMaterialRef{{ID: "shot_1", Kind: "screenshot", URI: "artifacts/recording/step-001.png", MimeType: "image/png"}},
		},
	}
	directorRef := model.DirectorMaterialRef{ID: "director_input", Kind: "director_input", URI: "artifacts/render/director_input.json", MimeType: "application/json"}

	plan := NewArkMediaDryRunPlan(&source, directorRef, input, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))

	if plan.Mode != "dry_run" || plan.VideoModel != "doubao-seedance-2-0-260128" {
		t.Fatalf("unexpected dry-run plan identity: %+v", plan)
	}
	if len(plan.RecommendedTasks) != 2 {
		t.Fatalf("expected Seedance and Seedream dry-run tasks, got %+v", plan.RecommendedTasks)
	}
	if !strings.Contains(plan.RecommendedTasks[0].Endpoint, "/api/v3/contents/generations/tasks") {
		t.Fatalf("Seedance endpoint is not Ark v3 content generation: %+v", plan.RecommendedTasks[0])
	}
	if bodyModel, _ := plan.RecommendedTasks[0].RequestBody["model"].(string); bodyModel != plan.VideoModel {
		t.Fatalf("Seedance request body model mismatch: %+v", plan.RecommendedTasks[0].RequestBody)
	}
	if len(plan.RequiredBeforeRealCall) == 0 || !strings.Contains(strings.Join(plan.RequiredBeforeRealCall, "\n"), "download returned") {
		t.Fatalf("dry-run plan must state real-call prerequisites: %+v", plan.RequiredBeforeRealCall)
	}
	if plan.ProviderConstraints.Seedance.Model != "doubao-seedance-2-0-260128" || plan.ProviderConstraints.Seedance.MaxDurationSec != 15 {
		t.Fatalf("dry-run plan must expose Seedance constraints: %+v", plan.ProviderConstraints)
	}
	if len(plan.SourceAssetRequirements) == 0 {
		t.Fatalf("dry-run plan must expose source asset requirements: %+v", plan)
	}
	if plan.RealCallReadiness.CanCallNow || plan.RealCallReadiness.CanCallWhenEnabled {
		t.Fatalf("local dry-run source assets must not be marked callable: %+v", plan.RealCallReadiness)
	}
	if len(plan.RealCallReadiness.Blockers) == 0 || plan.RealCallReadiness.Blockers[0].Code != "needs_public_uri" {
		t.Fatalf("expected local source reference to block real Ark calls: %+v", plan.RealCallReadiness)
	}
	if !plan.OutputHandling.MustNotReplaceCapturedUI || plan.OutputHandling.ExpectedOutputs[0].IncludeInDemo {
		t.Fatalf("Ark output handling must keep generated candidates non-authoritative: %+v", plan.OutputHandling)
	}
}

func TestNewArkMediaDryRunPlanReportsReadyWhenSourceAssetsArePublic(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	input := model.DirectorInput{
		SourcePackageID: source.PackageID,
		Project:         model.DirectorProjectContext{ProductName: "Cascade"},
		Materials: model.DirectorMaterialSet{
			SourceReferenceVideo: &model.DirectorMaterialRef{
				ID:       "source_reference",
				Kind:     "source_reference_video",
				URI:      "https://assets.example.com/source_reference.mp4",
				MimeType: "video/mp4",
			},
			Screenshots: []model.DirectorMaterialRef{{
				ID:       "shot_1",
				Kind:     "screenshot",
				URI:      "https://assets.example.com/step-001.png",
				MimeType: "image/png",
			}},
		},
	}
	directorRef := model.DirectorMaterialRef{ID: "director_input", Kind: "director_input", URI: "https://assets.example.com/director_input.json", MimeType: "application/json"}

	plan := NewArkMediaDryRunPlan(&source, directorRef, input, time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC))

	if plan.RealCallReadiness.Status != "ready_when_enabled" || plan.RealCallReadiness.CanCallNow || !plan.RealCallReadiness.CanCallWhenEnabled {
		t.Fatalf("public source assets should be ready only after mode gate is enabled: %+v", plan.RealCallReadiness)
	}
	if len(plan.RealCallReadiness.Blockers) != 0 {
		t.Fatalf("did not expect blockers for public source assets: %+v", plan.RealCallReadiness.Blockers)
	}
	for _, requirement := range plan.SourceAssetRequirements {
		if requirement.Required && requirement.Status != "ready" {
			t.Fatalf("required public asset should be ready: %+v", requirement)
		}
	}
}

func TestNewArkAssetPublicationPlanMarksLocalAssetsForPublication(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	arkPlan := model.ArkMediaDryRunPlan{
		SourcePackageID: source.PackageID,
		SourceAssetRequirements: []model.ArkMediaSourceAssetRequirement{{
			Ref:               model.DirectorMaterialRef{ID: "source_reference", Kind: "source_reference_video", URI: "artifacts/render/source_reference.mp4", MimeType: "video/mp4"},
			TaskID:            "seedance_reference_director_preview",
			Usage:             "required_reference_video",
			Required:          true,
			AcceptedMimeTypes: []string{"video/mp4", "video/quicktime"},
			RequiresPublicURI: true,
			Status:            "needs_public_uri",
		}, {
			Ref:               model.DirectorMaterialRef{ID: "shot_1", Kind: "screenshot", URI: "artifacts/recording/step-001.png", MimeType: "image/png"},
			TaskID:            "seedance_reference_director_preview",
			Usage:             "reference_video_or_image",
			AcceptedMimeTypes: []string{"image/png", "image/jpeg"},
			RequiresPublicURI: true,
			Status:            "needs_public_uri",
		}, {
			Ref:               model.DirectorMaterialRef{ID: "shot_1", Kind: "screenshot", URI: "artifacts/recording/step-001.png", MimeType: "image/png"},
			TaskID:            "seedream_non_product_visuals",
			Usage:             "optional_style_reference_image",
			AcceptedMimeTypes: []string{"image/png", "image/jpeg"},
			RequiresPublicURI: true,
			Status:            "needs_public_uri",
		}},
	}
	arkPlanRef := model.DirectorMaterialRef{ID: "ark_plan", Kind: "ark_media_dry_run_plan", URI: "artifacts/render/ark_media_dry_run_plan.json", MimeType: "application/json"}

	plan := NewArkAssetPublicationPlan(&source, arkPlanRef, arkPlan, time.Date(2026, 7, 14, 12, 30, 0, 0, time.UTC))

	if plan.SchemaVersion != model.ArkAssetPublicationPlanSchemaVersion || plan.Status != "needs_publication" {
		t.Fatalf("unexpected publication plan identity/status: %+v", plan)
	}
	if len(plan.Blockers) != 0 {
		t.Fatalf("local supported assets should need publication, not block: %+v", plan.Blockers)
	}
	if len(plan.Items) != 2 {
		t.Fatalf("duplicate screenshot requirements should be merged by asset, got %+v", plan.Items)
	}
	sourceItem := plan.Items[0]
	if !sourceItem.Required || !sourceItem.NeedsPublication || sourceItem.NeedsConversion || sourceItem.Status != "ready_after_publication" {
		t.Fatalf("source reference should be ready after publication: %+v", sourceItem)
	}
	if !strings.HasPrefix(sourceItem.ExpectedPublicURI, "https://<asset-host>/ark-inputs/") || sourceItem.RecommendedFileName != "source_reference.mp4" {
		t.Fatalf("unexpected publication target: %+v", sourceItem)
	}
	screenshotItem := plan.Items[1]
	if len(screenshotItem.TaskIDs) != 2 {
		t.Fatalf("screenshot should be reusable across Ark tasks: %+v", screenshotItem)
	}
}

func TestNewArkAssetPublicationPlanBlocksUnsupportedRequiredAssets(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	arkPlan := model.ArkMediaDryRunPlan{
		SourcePackageID: source.PackageID,
		SourceAssetRequirements: []model.ArkMediaSourceAssetRequirement{{
			Ref:               model.DirectorMaterialRef{ID: "source_reference", Kind: "source_reference_video", URI: "https://assets.example.com/source_reference.webm", MimeType: "video/webm"},
			TaskID:            "seedance_reference_director_preview",
			Usage:             "required_reference_video",
			Required:          true,
			AcceptedMimeTypes: []string{"video/mp4", "video/quicktime"},
			RequiresPublicURI: true,
			Status:            "unsupported_mime_type",
		}},
	}

	plan := NewArkAssetPublicationPlan(&source, model.DirectorMaterialRef{ID: "ark_plan", Kind: "ark_media_dry_run_plan", URI: "https://assets.example.com/ark_plan.json", MimeType: "application/json"}, arkPlan, time.Date(2026, 7, 14, 12, 45, 0, 0, time.UTC))

	if plan.Status != "blocked" || len(plan.Blockers) != 1 || plan.Blockers[0].Code != "needs_conversion" {
		t.Fatalf("unsupported required source should block publication: %+v", plan)
	}
	if len(plan.Items) != 1 || !plan.Items[0].NeedsConversion || plan.Items[0].NeedsPublication {
		t.Fatalf("unsupported public source should need conversion, not publication: %+v", plan.Items)
	}
}

func TestNewArkAssetPublicationPlanReportsReadyForPublicHTTPSAssets(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	arkPlan := model.ArkMediaDryRunPlan{
		SourcePackageID: source.PackageID,
		SourceAssetRequirements: []model.ArkMediaSourceAssetRequirement{{
			Ref:               model.DirectorMaterialRef{ID: "source_reference", Kind: "source_reference_video", URI: "https://assets.example.com/source_reference.mp4", MimeType: "video/mp4"},
			TaskID:            "seedance_reference_director_preview",
			Usage:             "required_reference_video",
			Required:          true,
			AcceptedMimeTypes: []string{"video/mp4", "video/quicktime"},
			RequiresPublicURI: true,
			Status:            "ready",
		}},
	}

	plan := NewArkAssetPublicationPlan(&source, model.DirectorMaterialRef{ID: "ark_plan", Kind: "ark_media_dry_run_plan", URI: "https://assets.example.com/ark_plan.json", MimeType: "application/json"}, arkPlan, time.Date(2026, 7, 14, 13, 0, 0, 0, time.UTC))

	if plan.Status != "ready" || len(plan.Blockers) != 0 || len(plan.Warnings) != 0 {
		t.Fatalf("public supported assets should be ready: %+v", plan)
	}
	if len(plan.Items) != 1 || !plan.Items[0].CurrentURIIsPublic || plan.Items[0].ExpectedPublicURI != "https://assets.example.com/source_reference.mp4" {
		t.Fatalf("public source URI should be preserved: %+v", plan.Items)
	}
}
