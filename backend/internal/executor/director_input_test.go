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
}
