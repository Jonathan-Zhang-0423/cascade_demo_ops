package executor

import (
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestNewDirectorEditPlanPatchFromSuggestionProducesSafePresentationPatch(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	sourceRange := model.MillisecondRange{0, 1800}
	basePlan := &model.DemoEditPlan{
		SchemaVersion:        model.DemoEditPlanSchemaVersion,
		PlanID:               "edit_plan_base",
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
			Purpose:           "Initial product state",
		}},
		GlobalStyle: &model.DemoEditGlobalStyle{ColorGrade: "neutral_product_ui", Pacing: "clear_and_direct", TransitionStyle: "simple_cut"},
	}
	input := minimalDirectorInputForAdapterTest()
	input.Workflow.Nodes[0].Verification = &model.DirectorInteractionVerification{
		Status:        "verified",
		Source:        "playwright_readonly_scan",
		SelectorScore: 95,
		Authority:     "verified_product_fact",
	}
	suggestion := model.DirectorEditSuggestion{
		SchemaVersion:   model.DirectorEditSuggestionSchemaVersion,
		SuggestionID:    "director_suggestion_1",
		SourcePackageID: source.PackageID,
		DirectorInputID: input.DirectorInputID,
		Style:           model.DirectorStyleSuggestion{ColorGrade: "neutral_enterprise", Pacing: "concise", TransitionStyle: "clean_cut"},
		RequirementHandling: []model.DirectorRequirementHandling{{
			RequirementID: "req_caption",
		}},
		ShotSuggestions: []model.DirectorShotSuggestion{{
			ID:             "shot_suggestion_1",
			SourceStepID:   "node_start",
			Operation:      "emphasize_verified_source",
			Purpose:        "Highlight the verified dashboard state.",
			PrioritySource: "client_execution_script",
			SourceTrace: []model.DirectorSourceTrace{{
				Source:     "playwright_readonly_scan",
				FieldPath:  "workflow.nodes.node_start",
				Confidence: "high",
			}},
		}},
		CaptionSuggestions: []model.DirectorCaptionSuggestion{{
			ID:             "caption_1",
			Text:           "Dashboard is ready for review.",
			AnchorStepID:   "node_start",
			PrioritySource: "user_explicit_metadata",
			SourceTrace: []model.DirectorSourceTrace{{
				Source:     "user_explicit_metadata",
				FieldPath:  "metadata.user_demo_intent.captions[0]",
				Confidence: "high",
			}},
		}},
	}

	patch := NewDirectorEditPlanPatchFromSuggestion(&source, basePlan, input, suggestion, time.Date(2026, 7, 15, 23, 0, 0, 0, time.UTC))
	report := ValidateDirectorEditPlanPatch(basePlan, input, patch, time.Date(2026, 7, 15, 23, 0, 1, 0, time.UTC))

	if patch.SchemaVersion != model.DirectorEditPlanPatchSchemaVersion || patch.Status != "proposed" || patch.BasePlanID != basePlan.PlanID {
		t.Fatalf("unexpected patch identity: %+v", patch)
	}
	if patch.Policy.AutoApply || !patch.Policy.PreserveLockedSourceFields || !patch.Policy.ExistingAssetsOnly {
		t.Fatalf("patch policy must keep director suggestions non-auto-applied and source-only: %+v", patch.Policy)
	}
	if len(patch.ShotPatches) != 1 {
		t.Fatalf("expected one shot patch, got %+v", patch.ShotPatches)
	}
	shotPatch := patch.ShotPatches[0]
	if shotPatch.SourceArtifactID != "artifact_raw_recording" || shotPatch.SourceStepID != "node_start" || !rangesEqual(shotPatch.SourceTimeRangeMS, &sourceRange) {
		t.Fatalf("shot patch must copy locked source fields unchanged: %+v", shotPatch)
	}
	if shotPatch.ProposedPurpose != "Highlight the verified dashboard state." {
		t.Fatalf("shot purpose should come from director suggestion: %+v", shotPatch)
	}
	if len(shotPatch.AddOverlays) != 1 || shotPatch.AddOverlays[0].Text != "Dashboard is ready for review." {
		t.Fatalf("caption suggestion should become a caption overlay patch: %+v", shotPatch.AddOverlays)
	}
	if len(shotPatch.AddOperations) != 1 || shotPatch.AddOperations[0].Type != model.EditOperationZoomPan {
		t.Fatalf("verified source emphasis should become a safe zoom_pan hint: %+v", shotPatch.AddOperations)
	}
	if patch.GlobalStylePatch == nil || patch.GlobalStylePatch.ColorGrade != "neutral_enterprise" {
		t.Fatalf("style suggestion should become a global style patch: %+v", patch.GlobalStylePatch)
	}
	if !report.Valid || report.SchemaVersion != model.DirectorEditPlanPatchValidationSchemaVersion {
		t.Fatalf("safe director patch should validate: %+v", report)
	}
}

func TestValidateDirectorEditPlanPatchWarnsOnRuntimeAdaptiveOverclaim(t *testing.T) {
	sourceRange := model.MillisecondRange{0, 1500}
	basePlan := &model.DemoEditPlan{
		PlanID: "edit_plan_runtime",
		Shots: []model.DemoEditShot{{
			ID:                "shot_runtime",
			SourceArtifactID:  "artifact_raw_recording",
			SourceStepID:      "node_adaptive",
			SourceTimeRangeMS: &sourceRange,
		}},
	}
	input := model.DirectorInput{
		Workflow: model.DirectorWorkflowSummary{Nodes: []model.DirectorWorkflowStep{{
			NodeID: "node_adaptive",
			Verification: &model.DirectorInteractionVerification{
				RuntimeAdaptive: true,
				Authority:       "runtime_adaptive_executable_intent",
			},
		}}},
	}
	patch := model.DirectorEditPlanPatch{
		SchemaVersion: model.DirectorEditPlanPatchSchemaVersion,
		PatchID:       "patch_runtime",
		BasePlanID:    basePlan.PlanID,
		Policy:        directorEditPlanPatchPolicy(),
		ShotPatches: []model.DirectorEditPlanShotPatch{{
			ShotID:            "shot_runtime",
			SourceArtifactID:  "artifact_raw_recording",
			SourceStepID:      "node_adaptive",
			SourceTimeRangeMS: &sourceRange,
			ProposedPurpose:   "Show the verified successful generation completed.",
			AddOverlays: []model.EditOverlay{{
				Type:         model.EditOverlayCaption,
				Text:         "Generation completed successfully.",
				SourceStepID: "node_adaptive",
			}},
			RuntimeAdaptive: true,
		}},
	}

	report := ValidateDirectorEditPlanPatch(basePlan, input, patch, time.Date(2026, 7, 15, 23, 5, 0, 0, time.UTC))

	if !report.Valid {
		t.Fatalf("runtime-adaptive overclaim should warn but not invalidate source-safe patch: %+v", report)
	}
	warnings := validationFindingCodes(report.Warnings)
	if !strings.Contains(warnings, "runtime_adaptive_caption_overclaims_outcome") || !strings.Contains(warnings, "runtime_adaptive_purpose_overclaims_outcome") {
		t.Fatalf("expected runtime-adaptive overclaim warnings, got %+v", report.Warnings)
	}
}

func TestApplyDirectorEditPlanPatchMergesCaptionAndKeepsLockedFields(t *testing.T) {
	sourceRange := model.MillisecondRange{100, 1600}
	startMS := 0
	endMS := 1200
	basePlan := &model.DemoEditPlan{
		PlanID:               "edit_plan_base",
		LockedFields:         model.DemoEditRequiredLockedFields,
		ModelEditableFields:  model.DemoEditAllowedModelEditableFields,
		SourceMaterialPolicy: model.DemoEditSourceMaterialPolicyExistingAssetsOnly,
		Shots: []model.DemoEditShot{{
			ID:                "shot_001",
			SourceArtifactID:  "artifact_raw_recording",
			SourceStepID:      "node_start",
			SourceTimeRangeMS: &sourceRange,
			Purpose:           "Old purpose",
			Overlays: []model.EditOverlay{{
				Type:         model.EditOverlayCaption,
				Text:         "Old caption",
				SourceStepID: "node_start",
			}},
		}},
	}
	zoom := 1.12
	patch := &model.DirectorEditPlanPatch{
		SchemaVersion: model.DirectorEditPlanPatchSchemaVersion,
		PatchID:       "patch_1",
		BasePlanID:    basePlan.PlanID,
		Status:        "proposed",
		Policy:        directorEditPlanPatchPolicy(),
		ShotPatches: []model.DirectorEditPlanShotPatch{{
			ShotID:            "shot_001",
			SourceArtifactID:  "artifact_raw_recording",
			SourceStepID:      "node_start",
			SourceTimeRangeMS: &sourceRange,
			ProposedPurpose:   "New director purpose",
			AddOperations: []model.EditOperation{{
				Type:    model.EditOperationZoomPan,
				StartMS: &startMS,
				EndMS:   &endMS,
				Zoom:    &zoom,
			}},
			AddOverlays: []model.EditOverlay{{
				Type:         model.EditOverlayCaption,
				Text:         "New director caption",
				SourceStepID: "node_start",
				StartMS:      &startMS,
				EndMS:        &endMS,
			}},
		}},
	}
	validation := ValidateDirectorEditPlanPatch(basePlan, model.DirectorInput{}, *patch, time.Date(2026, 7, 15, 23, 20, 0, 0, time.UTC))

	appliedPlan, result := ApplyDirectorEditPlanPatch(basePlan, patch, &validation, time.Date(2026, 7, 15, 23, 20, 1, 0, time.UTC))

	if !result.Applied || !result.RerenderRequested || result.OutputPlanID != "edit_plan_base_director_applied" {
		t.Fatalf("expected controlled apply result to request rerender: %+v", result)
	}
	if len(appliedPlan.Shots) != 1 {
		t.Fatalf("expected one shot in applied plan: %+v", appliedPlan.Shots)
	}
	shot := appliedPlan.Shots[0]
	if shot.SourceArtifactID != "artifact_raw_recording" || shot.SourceStepID != "node_start" || !rangesEqual(shot.SourceTimeRangeMS, &sourceRange) {
		t.Fatalf("apply must preserve locked source fields: %+v", shot)
	}
	if shot.Purpose != "New director purpose" || len(shot.Operations) != 1 || shot.Operations[0].Type != model.EditOperationZoomPan {
		t.Fatalf("apply should merge director purpose and operation: %+v", shot)
	}
	if len(shot.Overlays) != 1 || shot.Overlays[0].Text != "New director caption" {
		t.Fatalf("apply should replace existing caption overlay with director caption: %+v", shot.Overlays)
	}
	if basePlan.Shots[0].Purpose != "Old purpose" || basePlan.Shots[0].Overlays[0].Text != "Old caption" {
		t.Fatalf("apply must not mutate base plan: %+v", basePlan.Shots[0])
	}
}

func validationFindingCodes(findings []model.DemoEditPlanValidationFinding) string {
	values := []string{}
	for _, finding := range findings {
		values = append(values, finding.Code)
	}
	return strings.Join(values, "\n")
}
