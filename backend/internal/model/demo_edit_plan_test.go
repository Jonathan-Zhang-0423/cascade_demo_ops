package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDemoEditPlanJSONRoundTripPreservesSourceOnlyContract(t *testing.T) {
	now := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	catalog := AssetTimelineCatalog{
		SchemaVersion:   AssetTimelineCatalogSchemaVersion,
		CatalogID:       "catalog_run_1",
		WorkflowGraphID: "graph_1",
		GraphVersion:    1,
		RunID:           "run_1",
		Source: AssetTimelineSource{
			RecordingResultPackageID: "result_1",
			ExecutionTraceID:         "trace_1",
			GeneratedAt:              now,
		},
		Constraints: AssetTimelineConstraints{
			SourceMaterialOnly:                true,
			ProhibitNewImageOrVideoGeneration: true,
			ScriptIsPrimaryStoryline:          true,
			AllowedEditOperations:             DemoEditAllowedOperations,
			ProhibitedPlanKeys:                DemoEditProhibitedPlanKeys,
		},
		Timeline: AssetTimelineInfo{DurationMS: 2400, RecordingArtifactID: "artifact_raw_recording"},
		Steps: []TimelineStep{{
			StepID:          "node_open_dashboard",
			Order:           0,
			Action:          "navigate",
			Status:          "passed",
			Required:        true,
			StartMS:         0,
			EndMS:           1200,
			DurationMS:      1200,
			ExpectedOutcome: "Dashboard loads",
			SourceNode:      &TimelineSourceNode{Selector: "main", FocusSelector: "[data-testid='dashboard']"},
			Artifacts:       []string{"artifact_raw_recording"},
		}},
		Artifacts: []TimelineArtifact{{
			ID:            "artifact_raw_recording",
			Kind:          "raw_recording",
			URI:           "file:///tmp/demo.webm",
			MimeType:      "video/webm",
			Label:         "Raw browser recording",
			AssetRole:     "raw_recording",
			IncludeInDemo: true,
			CaptureScope:  "browser_context_video",
			Metadata:      map[string]any{"asset_role": "raw_recording", "include_in_demo": true},
			DurationMS:    2400,
		}},
	}

	startMS := 0
	endMS := 1200
	zoom := 1.18
	sourceRange := MillisecondRange{0, 1200}
	plan := DemoEditPlan{
		SchemaVersion:        DemoEditPlanSchemaVersion,
		PlanID:               "edit_plan_run_1",
		CatalogID:            catalog.CatalogID,
		Objective:            "Create a concise demo from existing recorded UI assets.",
		SourceAuthority:      DemoEditSourceAuthorityCustomerSideAgent,
		ModelRole:            DemoEditModelRolePresentationOptimizerOnly,
		SourceMaterialPolicy: DemoEditSourceMaterialPolicyExistingAssetsOnly,
		ScriptOrderPolicy:    DemoEditScriptOrderPolicyPreserveRequiredStepOrder,
		LockedFields:         DemoEditRequiredLockedFields,
		ModelEditableFields:  DemoEditAllowedModelEditableFields,
		TargetDurationMS:     60000,
		Shots: []DemoEditShot{{
			ID:                "shot_001_open_dashboard",
			SourceArtifactID:  "artifact_raw_recording",
			SourceStepID:      "node_open_dashboard",
			SourceTimeRangeMS: &sourceRange,
			Purpose:           "Show dashboard loading",
			Operations: []EditOperation{
				{Type: EditOperationTrim, StartMS: &startMS, EndMS: &endMS},
				{Type: EditOperationZoomPan, StartMS: &startMS, EndMS: &endMS, FocusSelector: "[data-testid='dashboard']", Zoom: &zoom},
			},
			Overlays: []EditOverlay{{
				Type:         EditOverlayCaption,
				Text:         "Dashboard loads",
				SourceStepID: "node_open_dashboard",
				StartMS:      &startMS,
				EndMS:        &endMS,
			}},
		}},
		GlobalStyle: &DemoEditGlobalStyle{ColorGrade: "neutral_product_ui", Pacing: "clear_and_direct", TransitionStyle: "simple_cut"},
	}

	catalogData, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var gotCatalog AssetTimelineCatalog
	if err := json.Unmarshal(catalogData, &gotCatalog); err != nil {
		t.Fatal(err)
	}
	if gotCatalog.SchemaVersion != AssetTimelineCatalogSchemaVersion || !gotCatalog.Constraints.SourceMaterialOnly {
		t.Fatalf("catalog lost source-only contract: %+v", gotCatalog)
	}
	if !gotCatalog.Constraints.ProhibitNewImageOrVideoGeneration || !gotCatalog.Constraints.ScriptIsPrimaryStoryline {
		t.Fatalf("catalog lost generation/order constraints: %+v", gotCatalog.Constraints)
	}
	if !containsEditOperation(gotCatalog.Constraints.AllowedEditOperations, EditOperationZoomPan) {
		t.Fatalf("catalog allowed operations missing zoom_pan: %+v", gotCatalog.Constraints.AllowedEditOperations)
	}
	if !containsString(gotCatalog.Constraints.ProhibitedPlanKeys, "image_prompt") || !containsString(gotCatalog.Constraints.ProhibitedPlanKeys, "new_ui_action") {
		t.Fatalf("catalog prohibited keys missing generation/action guards: %+v", gotCatalog.Constraints.ProhibitedPlanKeys)
	}
	if gotCatalog.Artifacts[0].AssetRole != "raw_recording" || !gotCatalog.Artifacts[0].IncludeInDemo || gotCatalog.Artifacts[0].Metadata["asset_role"] != "raw_recording" {
		t.Fatalf("catalog artifact metadata did not round-trip: %+v", gotCatalog.Artifacts[0])
	}

	planData, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var gotPlan DemoEditPlan
	if err := json.Unmarshal(planData, &gotPlan); err != nil {
		t.Fatal(err)
	}
	if gotPlan.SchemaVersion != DemoEditPlanSchemaVersion {
		t.Fatalf("plan schema = %q", gotPlan.SchemaVersion)
	}
	if gotPlan.SourceMaterialPolicy != DemoEditSourceMaterialPolicyExistingAssetsOnly {
		t.Fatalf("plan source policy = %q", gotPlan.SourceMaterialPolicy)
	}
	if gotPlan.ScriptOrderPolicy != DemoEditScriptOrderPolicyPreserveRequiredStepOrder {
		t.Fatalf("plan order policy = %q", gotPlan.ScriptOrderPolicy)
	}
	if gotPlan.SourceAuthority != DemoEditSourceAuthorityCustomerSideAgent {
		t.Fatalf("plan source authority = %q", gotPlan.SourceAuthority)
	}
	if gotPlan.ModelRole != DemoEditModelRolePresentationOptimizerOnly {
		t.Fatalf("plan model role = %q", gotPlan.ModelRole)
	}
	if !containsString(gotPlan.LockedFields, "source_artifact_id") || !containsString(gotPlan.LockedFields, "required_step_order") {
		t.Fatalf("plan locked fields lost source facts: %+v", gotPlan.LockedFields)
	}
	if !containsString(gotPlan.ModelEditableFields, "overlays.text") || !containsString(gotPlan.ModelEditableFields, "global_style.pacing") {
		t.Fatalf("plan editable fields lost presentation whitelist: %+v", gotPlan.ModelEditableFields)
	}
	if gotPlan.Shots[0].SourceTimeRangeMS == nil || gotPlan.Shots[0].SourceTimeRangeMS[0] != 0 || gotPlan.Shots[0].SourceTimeRangeMS[1] != 1200 {
		t.Fatalf("source time range did not round-trip: %+v", gotPlan.Shots[0].SourceTimeRangeMS)
	}
	if gotPlan.Shots[0].Operations[0].StartMS == nil || *gotPlan.Shots[0].Operations[0].StartMS != 0 {
		t.Fatalf("operation start_ms=0 did not round-trip: %+v", gotPlan.Shots[0].Operations[0])
	}
	if gotPlan.Shots[0].Overlays[0].StartMS == nil || *gotPlan.Shots[0].Overlays[0].StartMS != 0 {
		t.Fatalf("overlay start_ms=0 did not round-trip: %+v", gotPlan.Shots[0].Overlays[0])
	}
}

func TestDemoEditPlanValidationReportJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 9, 10, 30, 0, 0, time.UTC)
	report := DemoEditPlanValidationReport{
		SchemaVersion: DemoEditPlanValidationSchemaVersion,
		Valid:         false,
		CheckedAt:     now,
		PlanID:        "edit_plan_run_1",
		Errors: []DemoEditPlanValidationFinding{{
			Code:    "prohibited_generation_instruction",
			Message: "Edit plan cannot contain generation field image_prompt",
			Path:    "shots[0].image_prompt",
		}},
		Warnings: []DemoEditPlanValidationFinding{{
			Code:    "required_step_omitted",
			Message: "Required script step node_submit is not represented",
			Path:    "shots",
		}},
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var got DemoEditPlanValidationReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != DemoEditPlanValidationSchemaVersion || got.Valid {
		t.Fatalf("validation report lost status: %+v", got)
	}
	if got.CheckedAt.IsZero() || got.PlanID != "edit_plan_run_1" {
		t.Fatalf("validation report lost identity fields: %+v", got)
	}
	if got.Errors[0].Code != "prohibited_generation_instruction" || got.Warnings[0].Code != "required_step_omitted" {
		t.Fatalf("validation findings did not round-trip: %+v", got)
	}
}

func TestDirectorEditPlanPatchJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 15, 23, 10, 0, 0, time.UTC)
	startMS := 0
	endMS := 1200
	sourceRange := MillisecondRange{0, 1200}
	zoom := 1.12
	patch := DirectorEditPlanPatch{
		SchemaVersion:   DirectorEditPlanPatchSchemaVersion,
		PatchID:         "director_edit_plan_patch_1",
		CreatedAt:       now,
		SourcePackageID: "pkg_1",
		DirectorInputID: "director_input_1",
		SuggestionID:    "director_suggestion_1",
		BasePlanID:      "edit_plan_1",
		Status:          "proposed",
		ApplicationMode: "manual_review_or_controlled_apply_required",
		Policy: DirectorEditPlanPatchPolicy{
			AutoApply:                                  false,
			RequiresValidation:                         true,
			RequiresRendererValidation:                 true,
			ExistingAssetsOnly:                         true,
			PreserveRequiredStepOrder:                  true,
			PreserveLockedSourceFields:                 true,
			RuntimeAdaptiveRequiresCaptureConfirmation: true,
			AllowedEditableFields:                      DemoEditAllowedModelEditableFields,
			LockedFields:                               DemoEditRequiredLockedFields,
		},
		ShotPatches: []DirectorEditPlanShotPatch{{
			ShotID:            "shot_001",
			SourceArtifactID:  "artifact_raw_recording",
			SourceStepID:      "node_start",
			SourceTimeRangeMS: &sourceRange,
			ProposedPurpose:   "Highlight the verified source step.",
			AddOperations: []EditOperation{{
				Type:    EditOperationZoomPan,
				StartMS: &startMS,
				EndMS:   &endMS,
				Zoom:    &zoom,
			}},
			AddOverlays: []EditOverlay{{
				Type:         EditOverlayCaption,
				Text:         "Dashboard is ready.",
				SourceStepID: "node_start",
				StartMS:      &startMS,
				EndMS:        &endMS,
			}},
			PrioritySource: "user_explicit_metadata",
			SourceTrace: []DirectorSourceTrace{{
				Source:     "user_explicit_metadata",
				FieldPath:  "metadata.user_demo_intent.captions[0]",
				Confidence: "high",
			}},
		}},
		GlobalStylePatch: &DemoEditGlobalStyle{ColorGrade: "neutral_enterprise"},
	}
	report := DirectorEditPlanPatchValidationReport{
		SchemaVersion: DirectorEditPlanPatchValidationSchemaVersion,
		PatchID:       patch.PatchID,
		BasePlanID:    patch.BasePlanID,
		Valid:         true,
		CheckedAt:     now,
		Policies:      []string{"patch_is_not_auto_applied"},
	}
	applyResult := DirectorEditPlanPatchApplyResult{
		SchemaVersion:      DirectorEditPlanPatchApplyResultSchemaVersion,
		ResultID:           "director_patch_apply_1",
		CreatedAt:          now,
		PatchID:            patch.PatchID,
		BasePlanID:         patch.BasePlanID,
		OutputPlanID:       "edit_plan_1_director_applied",
		Status:             "applied_and_rerendered",
		Applied:            true,
		RerenderRequested:  true,
		Rerendered:         true,
		AppliedShotIDs:     []string{"shot_001"},
		OutputVideoPath:    "artifacts/render/demo_12s.mp4",
		OutputPlanPath:     "artifacts/render/demo_edit_plan.json",
		RenderManifestPath: "artifacts/render/render_manifest.json",
		Notes:              []string{"controlled apply"},
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	var gotPatch DirectorEditPlanPatch
	if err := json.Unmarshal(patchData, &gotPatch); err != nil {
		t.Fatal(err)
	}
	if gotPatch.SchemaVersion != DirectorEditPlanPatchSchemaVersion || gotPatch.Policy.AutoApply {
		t.Fatalf("director patch identity/policy did not round-trip: %+v", gotPatch)
	}
	if gotPatch.ShotPatches[0].SourceTimeRangeMS == nil || gotPatch.ShotPatches[0].SourceTimeRangeMS[1] != 1200 {
		t.Fatalf("director patch source range did not round-trip: %+v", gotPatch.ShotPatches[0])
	}
	if gotPatch.ShotPatches[0].AddOverlays[0].StartMS == nil || *gotPatch.ShotPatches[0].AddOverlays[0].StartMS != 0 {
		t.Fatalf("director patch overlay start_ms=0 did not round-trip: %+v", gotPatch.ShotPatches[0].AddOverlays[0])
	}

	reportData, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var gotReport DirectorEditPlanPatchValidationReport
	if err := json.Unmarshal(reportData, &gotReport); err != nil {
		t.Fatal(err)
	}
	if gotReport.SchemaVersion != DirectorEditPlanPatchValidationSchemaVersion || !gotReport.Valid || gotReport.PatchID != patch.PatchID {
		t.Fatalf("director patch validation report did not round-trip: %+v", gotReport)
	}

	applyData, err := json.Marshal(applyResult)
	if err != nil {
		t.Fatal(err)
	}
	var gotApply DirectorEditPlanPatchApplyResult
	if err := json.Unmarshal(applyData, &gotApply); err != nil {
		t.Fatal(err)
	}
	if gotApply.SchemaVersion != DirectorEditPlanPatchApplyResultSchemaVersion || !gotApply.Applied || !gotApply.Rerendered || gotApply.OutputPlanID == "" {
		t.Fatalf("director patch apply result did not round-trip: %+v", gotApply)
	}
}

func TestDemoEditPlanWorkerJSONCompatibility(t *testing.T) {
	catalogJSON := []byte(`{
  "schema_version": "demoops.asset_timeline_catalog.v1",
  "catalog_id": "catalog_run_1",
  "workflow_graph_id": "graph_1",
  "graph_version": 1,
  "run_id": "run_1",
  "source": {
    "recording_result_package_id": "result_1",
    "execution_trace_id": "trace_1",
    "generated_at": "2026-07-09T10:00:00.000Z"
  },
  "constraints": {
    "source_material_only": true,
    "prohibit_new_image_or_video_generation": true,
    "script_is_primary_storyline": true,
    "allowed_edit_operations": ["trim", "zoom_pan", "caption"],
    "prohibited_plan_keys": ["image_prompt", "video_prompt", "new_ui_action"]
  },
  "timeline": {
    "duration_ms": 1200,
    "recording_artifact_id": "artifact_raw_recording"
  },
  "steps": [{
    "step_id": "open",
    "order": 0,
    "action": "navigate",
    "status": "passed",
    "required": true,
    "start_ms": 0,
    "end_ms": 1200,
    "duration_ms": 1200,
    "expected_outcome": "Product entry loads",
    "source_node": {
      "selector": "main",
      "focus_selector": "main"
    },
    "artifacts": ["artifact_raw_recording"]
  }],
  "artifacts": [{
    "id": "artifact_raw_recording",
    "kind": "raw_recording",
    "uri": "file:///tmp/demo.webm",
    "mime_type": "video/webm",
    "asset_role": "raw_recording",
    "include_in_demo": true,
    "capture_scope": "browser_context_video",
    "metadata": {
      "asset_role": "raw_recording",
      "include_in_demo": true
    },
    "duration_ms": 1200,
    "local_path": "/tmp/demo.webm"
  }]
}`)
	planJSON := []byte(`{
  "schema_version": "demoops.demo_edit_plan.v1",
  "plan_id": "edit_plan_run_1",
  "catalog_id": "catalog_run_1",
  "objective": "Create a concise demo from the recorded product interaction. Do not create new images or video.",
  "source_authority": "customer_side_agent",
  "model_role": "presentation_optimizer_only",
  "source_material_policy": "existing_assets_only",
  "script_order_policy": "preserve_required_step_order",
  "locked_fields": ["source_authority", "model_role", "source_material_policy", "script_order_policy", "source_artifact_id", "source_step_id", "source_time_range_ms", "required_step_order"],
  "model_editable_fields": ["purpose", "overlays.text", "global_style.color_grade", "global_style.pacing", "global_style.transition_style", "operations.zoom", "operations.speed", "operations.style"],
  "target_duration_ms": 60000,
  "shots": [{
    "id": "shot_001_open",
    "source_artifact_id": "artifact_raw_recording",
    "source_step_id": "open",
    "source_time_range_ms": [0, 1200],
    "purpose": "Product entry loads",
    "operations": [{
      "type": "trim",
      "start_ms": 0,
      "end_ms": 1200
    }, {
      "type": "zoom_pan",
      "start_ms": 0,
      "end_ms": 1200,
      "focus_selector": "main",
      "zoom": 1.18
    }],
    "overlays": [{
      "type": "caption",
      "text": "Product entry loads",
      "source_step_id": "open",
      "start_ms": 0,
      "end_ms": 1200
    }]
  }],
  "global_style": {
    "color_grade": "neutral_product_ui",
    "pacing": "clear_and_direct",
    "transition_style": "simple_cut"
  }
}`)
	reportJSON := []byte(`{
  "schema_version": "demoops.demo_edit_plan_validation.v1",
  "valid": true,
  "checked_at": "2026-07-09T10:00:01.000Z",
  "plan_id": "edit_plan_run_1",
  "errors": [],
  "warnings": []
}`)

	var catalog AssetTimelineCatalog
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		t.Fatal(err)
	}
	var plan DemoEditPlan
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		t.Fatal(err)
	}
	var report DemoEditPlanValidationReport
	if err := json.Unmarshal(reportJSON, &report); err != nil {
		t.Fatal(err)
	}

	if catalog.Source.GeneratedAt.IsZero() || catalog.Timeline.RecordingArtifactID != "artifact_raw_recording" {
		t.Fatalf("worker catalog JSON did not map to Go DTO: %+v", catalog)
	}
	if catalog.Artifacts[0].AssetRole != "raw_recording" || !catalog.Artifacts[0].IncludeInDemo || catalog.Artifacts[0].CaptureScope != "browser_context_video" {
		t.Fatalf("worker catalog artifact purpose metadata did not map to Go DTO: %+v", catalog.Artifacts[0])
	}
	if plan.Shots[0].SourceTimeRangeMS == nil || plan.Shots[0].SourceTimeRangeMS[0] != 0 {
		t.Fatalf("worker plan time range did not map to Go DTO: %+v", plan.Shots[0].SourceTimeRangeMS)
	}
	if plan.SourceAuthority != DemoEditSourceAuthorityCustomerSideAgent || plan.ModelRole != DemoEditModelRolePresentationOptimizerOnly {
		t.Fatalf("worker plan collaboration boundary did not map to Go DTO: %+v", plan)
	}
	if !containsString(plan.LockedFields, "source_time_range_ms") || !containsString(plan.ModelEditableFields, "operations.zoom") {
		t.Fatalf("worker plan field ownership did not map to Go DTO: locked=%+v editable=%+v", plan.LockedFields, plan.ModelEditableFields)
	}
	if plan.Shots[0].Operations[1].Zoom == nil || *plan.Shots[0].Operations[1].Zoom != 1.18 {
		t.Fatalf("worker plan zoom operation did not map to Go DTO: %+v", plan.Shots[0].Operations[1])
	}
	if !report.Valid || report.CheckedAt.IsZero() {
		t.Fatalf("worker validation report JSON did not map to Go DTO: %+v", report)
	}
}

func containsEditOperation(values []EditOperationType, want EditOperationType) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
