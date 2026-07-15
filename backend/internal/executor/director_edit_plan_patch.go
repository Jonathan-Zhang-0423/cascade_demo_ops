package executor

import (
	"strconv"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

func NewDirectorEditPlanPatchFromSuggestion(source *model.ClientExecutionPackage, basePlan *model.DemoEditPlan, input model.DirectorInput, suggestion model.DirectorEditSuggestion, createdAt time.Time) model.DirectorEditPlanPatch {
	sourcePackageID := suggestion.SourcePackageID
	if sourcePackageID == "" && source != nil {
		sourcePackageID = source.PackageID
	}
	basePlanID := ""
	if basePlan != nil {
		basePlanID = basePlan.PlanID
	}
	patch := model.DirectorEditPlanPatch{
		SchemaVersion:   model.DirectorEditPlanPatchSchemaVersion,
		PatchID:         "director_edit_plan_patch_" + safeID(firstNonEmptyString(suggestion.SuggestionID, input.DirectorInputID, sourcePackageID, "unknown")),
		CreatedAt:       createdAt,
		SourcePackageID: sourcePackageID,
		DirectorInputID: firstNonEmptyString(suggestion.DirectorInputID, input.DirectorInputID),
		SuggestionID:    suggestion.SuggestionID,
		BasePlanID:      basePlanID,
		Status:          "no_safe_updates",
		ApplicationMode: "manual_review_or_controlled_apply_required",
		Policy:          directorEditPlanPatchPolicy(),
		Notes: []string{
			"This patch is a proposal only; it is not applied to the current final demo video.",
			"The patch may update presentation fields only: shot purpose, captions, global style, and safe operation hints.",
			"Locked source fields are copied only for audit and must match the base DemoEditPlan before any future application.",
		},
	}
	if suggestion.SuggestionID == "" {
		patch.Status = "no_suggestion"
		patch.Warnings = append(patch.Warnings, model.ArkMediaReadinessFinding{
			Code:    "director_suggestion_missing",
			Message: "director edit plan patch was skipped because director_edit_suggestion is missing",
		})
		return patch
	}
	if basePlan == nil {
		patch.Status = "no_base_plan"
		patch.Warnings = append(patch.Warnings, model.ArkMediaReadinessFinding{
			Code:    "base_demo_edit_plan_missing",
			Message: "director edit plan patch cannot target renderable shots because demo_edit_plan is missing",
		})
		return patch
	}

	builder := newDirectorPatchBuilder(basePlan, input)
	patch.GlobalStylePatch = directorGlobalStylePatch(basePlan, suggestion)
	for _, requirement := range suggestion.RequirementHandling {
		if requirement.RequirementID != "" {
			patch.RequirementRefs = append(patch.RequirementRefs, requirement.RequirementID)
		}
	}
	for _, shotSuggestion := range suggestion.ShotSuggestions {
		builder.applyShotSuggestion(shotSuggestion)
	}
	for _, captionSuggestion := range suggestion.CaptionSuggestions {
		builder.applyCaptionSuggestion(captionSuggestion)
	}
	patch.ShotPatches = builder.patches()
	patch.SkippedSuggestion = append(patch.SkippedSuggestion, builder.skipped...)
	patch.Warnings = append(patch.Warnings, builder.warnings...)
	if len(patch.ShotPatches) > 0 || patch.GlobalStylePatch != nil {
		patch.Status = "proposed"
	}
	return patch
}

func directorEditPlanPatchPolicy() model.DirectorEditPlanPatchPolicy {
	return model.DirectorEditPlanPatchPolicy{
		AutoApply:                                  false,
		RequiresValidation:                         true,
		RequiresRendererValidation:                 true,
		ExistingAssetsOnly:                         true,
		PreserveRequiredStepOrder:                  true,
		PreserveLockedSourceFields:                 true,
		RuntimeAdaptiveRequiresCaptureConfirmation: true,
		AllowedEditableFields:                      append([]string{}, model.DemoEditAllowedModelEditableFields...),
		LockedFields:                               append([]string{}, model.DemoEditRequiredLockedFields...),
	}
}

type directorPatchBuilder struct {
	basePlan      *model.DemoEditPlan
	input         model.DirectorInput
	patchesByShot map[string]*model.DirectorEditPlanShotPatch
	shotByStep    map[string]model.DemoEditShot
	shotByID      map[string]model.DemoEditShot
	stepByID      map[string]model.DirectorWorkflowStep
	skipped       []model.DirectorEditPlanPatchSkip
	warnings      []model.ArkMediaReadinessFinding
}

func newDirectorPatchBuilder(basePlan *model.DemoEditPlan, input model.DirectorInput) *directorPatchBuilder {
	builder := &directorPatchBuilder{
		basePlan:      basePlan,
		input:         input,
		patchesByShot: map[string]*model.DirectorEditPlanShotPatch{},
		shotByStep:    map[string]model.DemoEditShot{},
		shotByID:      map[string]model.DemoEditShot{},
		stepByID:      map[string]model.DirectorWorkflowStep{},
	}
	if basePlan != nil {
		for _, shot := range basePlan.Shots {
			builder.shotByID[shot.ID] = shot
			if shot.SourceStepID != "" {
				builder.shotByStep[shot.SourceStepID] = shot
			}
		}
	}
	for _, step := range input.Workflow.Nodes {
		if step.NodeID != "" {
			builder.stepByID[step.NodeID] = step
		}
	}
	return builder
}

func (b *directorPatchBuilder) applyShotSuggestion(suggestion model.DirectorShotSuggestion) {
	if strings.TrimSpace(suggestion.SourceStepID) == "" {
		b.skip("shot_suggestion", suggestion.ID, "shot suggestion has no source_step_id and cannot be matched without changing script ownership", "source_step_id", true)
		return
	}
	shot, ok := b.shotByStep[suggestion.SourceStepID]
	if !ok {
		b.skip("shot_suggestion", suggestion.ID, "shot suggestion references a source step that is not represented by the base DemoEditPlan", "source_step_id", true)
		return
	}
	patch := b.patchForShot(shot, suggestion.PrioritySource, suggestion.SourceTrace)
	if text := strings.TrimSpace(suggestion.Purpose); text != "" {
		patch.ProposedPurpose = text
	}
	if operation := directorPatchOperationForSuggestion(suggestion, b.stepByID[suggestion.SourceStepID]); operation != nil {
		patch.AddOperations = append(patch.AddOperations, *operation)
	}
}

func (b *directorPatchBuilder) applyCaptionSuggestion(suggestion model.DirectorCaptionSuggestion) {
	text := strings.TrimSpace(suggestion.Text)
	if text == "" {
		b.skip("caption_suggestion", suggestion.ID, "caption suggestion text is empty", "text", true)
		return
	}
	shot, ok := b.captionAnchorShot(suggestion.AnchorStepID)
	if !ok {
		b.skip("caption_suggestion", suggestion.ID, "caption suggestion cannot be anchored to a base DemoEditPlan shot", "anchor_step_id", true)
		return
	}
	patch := b.patchForShot(shot, suggestion.PrioritySource, suggestion.SourceTrace)
	durationMS := shotDurationMS(shot, 3000)
	startMS := 0
	endMS := positiveOrFallback(minInt(durationMS, 3000), 1500)
	patch.AddOverlays = append(patch.AddOverlays, model.EditOverlay{
		Type:         model.EditOverlayCaption,
		Text:         text,
		SourceStepID: shot.SourceStepID,
		StartMS:      &startMS,
		EndMS:        &endMS,
	})
}

func (b *directorPatchBuilder) captionAnchorShot(anchorStepID string) (model.DemoEditShot, bool) {
	if anchorStepID != "" {
		shot, ok := b.shotByStep[anchorStepID]
		return shot, ok
	}
	for _, shot := range b.basePlan.Shots {
		if shot.SourceStepID != "" {
			return shot, true
		}
	}
	return model.DemoEditShot{}, false
}

func (b *directorPatchBuilder) patchForShot(shot model.DemoEditShot, prioritySource string, traces []model.DirectorSourceTrace) *model.DirectorEditPlanShotPatch {
	if existing, ok := b.patchesByShot[shot.ID]; ok {
		if existing.PrioritySource == "" {
			existing.PrioritySource = prioritySource
		}
		existing.SourceTrace = uniquePatchSourceTraces(append(existing.SourceTrace, traces...))
		return existing
	}
	patch := &model.DirectorEditPlanShotPatch{
		ShotID:            shot.ID,
		SourceStepID:      shot.SourceStepID,
		SourceArtifactID:  shot.SourceArtifactID,
		SourceTimeRangeMS: cloneRange(shot.SourceTimeRangeMS),
		PrioritySource:    prioritySource,
		SourceTrace:       uniquePatchSourceTraces(traces),
		RuntimeAdaptive:   b.stepIsRuntimeAdaptive(shot.SourceStepID),
	}
	b.patchesByShot[shot.ID] = patch
	return patch
}

func (b *directorPatchBuilder) patches() []model.DirectorEditPlanShotPatch {
	out := []model.DirectorEditPlanShotPatch{}
	if b.basePlan == nil {
		return out
	}
	for _, shot := range b.basePlan.Shots {
		patch, ok := b.patchesByShot[shot.ID]
		if !ok {
			continue
		}
		out = append(out, *patch)
	}
	return out
}

func (b *directorPatchBuilder) stepIsRuntimeAdaptive(stepID string) bool {
	step, ok := b.stepByID[stepID]
	return ok && directorWorkflowStepIsRuntimeAdaptive(step)
}

func (b *directorPatchBuilder) skip(kind string, refID string, reason string, field string, blocked bool) {
	b.skipped = append(b.skipped, model.DirectorEditPlanPatchSkip{
		Kind:    kind,
		RefID:   refID,
		Reason:  reason,
		Field:   field,
		Blocked: blocked,
	})
}

func directorPatchOperationForSuggestion(suggestion model.DirectorShotSuggestion, step model.DirectorWorkflowStep) *model.EditOperation {
	switch suggestion.Operation {
	case "emphasize_verified_source":
		zoom := 1.12
		startMS := 0
		endMS := positiveOrFallback(step.DurationMS, 1800)
		operation := model.EditOperation{
			Type:    model.EditOperationZoomPan,
			StartMS: &startMS,
			EndMS:   &endMS,
			Zoom:    &zoom,
			Style:   "subtle_verified_source_emphasis",
		}
		if step.Capture != nil && step.Capture.FocusSelector != "" {
			operation.FocusSelector = step.Capture.FocusSelector
		} else if step.Target.Selector != "" {
			operation.FocusSelector = step.Target.Selector
		}
		return &operation
	case "capture_runtime_adaptive_intent":
		startMS := 0
		endMS := positiveOrFallback(step.DurationMS, 1800)
		operation := model.EditOperation{
			Type:    model.EditOperationHold,
			StartMS: &startMS,
			EndMS:   &endMS,
			Style:   "await_captured_confirmation",
		}
		return &operation
	default:
		return nil
	}
}

func directorGlobalStylePatch(basePlan *model.DemoEditPlan, suggestion model.DirectorEditSuggestion) *model.DemoEditGlobalStyle {
	style := model.DemoEditGlobalStyle{
		ColorGrade:      strings.TrimSpace(suggestion.Style.ColorGrade),
		Pacing:          strings.TrimSpace(suggestion.Style.Pacing),
		TransitionStyle: strings.TrimSpace(suggestion.Style.TransitionStyle),
	}
	if basePlan != nil && basePlan.GlobalStyle != nil {
		if style.ColorGrade == basePlan.GlobalStyle.ColorGrade {
			style.ColorGrade = ""
		}
		if style.Pacing == basePlan.GlobalStyle.Pacing {
			style.Pacing = ""
		}
		if style.TransitionStyle == basePlan.GlobalStyle.TransitionStyle {
			style.TransitionStyle = ""
		}
	}
	if style.ColorGrade == "" && style.Pacing == "" && style.TransitionStyle == "" {
		return nil
	}
	return &style
}

func ValidateDirectorEditPlanPatch(basePlan *model.DemoEditPlan, input model.DirectorInput, patch model.DirectorEditPlanPatch, checkedAt time.Time) model.DirectorEditPlanPatchValidationReport {
	report := model.DirectorEditPlanPatchValidationReport{
		SchemaVersion: model.DirectorEditPlanPatchValidationSchemaVersion,
		PatchID:       patch.PatchID,
		BasePlanID:    patch.BasePlanID,
		Valid:         true,
		CheckedAt:     checkedAt.UTC(),
		Policies: []string{
			"patch_is_not_auto_applied",
			"existing_assets_only",
			"preserve_required_step_order",
			"preserve_locked_source_fields",
			"runtime_adaptive_not_product_proof",
		},
	}
	if patch.SchemaVersion != model.DirectorEditPlanPatchSchemaVersion {
		report.Errors = append(report.Errors, editPlanPatchFinding("unsupported_schema", "director edit plan patch schema_version is invalid", "schema_version"))
	}
	if patch.Policy.AutoApply {
		report.Errors = append(report.Errors, editPlanPatchFinding("auto_apply_forbidden", "director edit plan patch must not auto-apply without a controlled validation step", "policy.auto_apply"))
	}
	if !patch.Policy.ExistingAssetsOnly || !patch.Policy.PreserveRequiredStepOrder || !patch.Policy.PreserveLockedSourceFields {
		report.Errors = append(report.Errors, editPlanPatchFinding("patch_policy_violation", "director edit plan patch must preserve existing assets, required step order, and locked source fields", "policy"))
	}
	if basePlan == nil {
		if len(patch.ShotPatches) > 0 {
			report.Errors = append(report.Errors, editPlanPatchFinding("base_plan_missing", "shot patches cannot be validated without a base DemoEditPlan", "base_plan_id"))
		}
		report.Valid = len(report.Errors) == 0
		return report
	}
	if patch.BasePlanID != "" && basePlan.PlanID != "" && patch.BasePlanID != basePlan.PlanID {
		report.Errors = append(report.Errors, editPlanPatchFinding("base_plan_mismatch", "director edit plan patch base_plan_id does not match the supplied DemoEditPlan", "base_plan_id"))
	}
	if missing := missingRequiredFields(model.DemoEditRequiredLockedFields, patch.Policy.LockedFields); len(missing) > 0 {
		report.Errors = append(report.Errors, editPlanPatchFinding("missing_locked_fields", "patch policy must declare locked fields: "+strings.Join(missing, ", "), "policy.locked_fields"))
	}
	if unsupported := unsupportedPatchEditableFields(patch.Policy.AllowedEditableFields); len(unsupported) > 0 {
		report.Errors = append(report.Errors, editPlanPatchFinding("unsupported_editable_fields", "patch policy contains unsupported editable fields: "+strings.Join(unsupported, ", "), "policy.allowed_editable_fields"))
	}

	shotByID := map[string]model.DemoEditShot{}
	for _, shot := range basePlan.Shots {
		shotByID[shot.ID] = shot
	}
	runtimeAdaptive := runtimeAdaptiveStepSet(input)
	for index, shotPatch := range patch.ShotPatches {
		path := "shot_patches[" + strconv.Itoa(index) + "]"
		baseShot, ok := shotByID[shotPatch.ShotID]
		if !ok {
			report.Errors = append(report.Errors, editPlanPatchFinding("unknown_shot", "director edit plan patch references a shot outside the base DemoEditPlan", path+".shot_id"))
			continue
		}
		if shotPatch.SourceStepID != baseShot.SourceStepID || shotPatch.SourceArtifactID != baseShot.SourceArtifactID || !rangesEqual(shotPatch.SourceTimeRangeMS, baseShot.SourceTimeRangeMS) {
			report.Errors = append(report.Errors, editPlanPatchFinding("locked_source_field_changed", "director edit plan patch must not change source_step_id, source_artifact_id, or source_time_range_ms", path))
		}
		for operationIndex, operation := range shotPatch.AddOperations {
			if !allowedEditOperation(operation.Type) {
				report.Errors = append(report.Errors, editPlanPatchFinding("unsupported_operation", "director edit plan patch contains unsupported operation "+string(operation.Type), path+".add_operations["+strconv.Itoa(operationIndex)+"].type"))
			}
		}
		for overlayIndex, overlay := range shotPatch.AddOverlays {
			if !allowedEditOverlay(overlay.Type) {
				report.Errors = append(report.Errors, editPlanPatchFinding("unsupported_overlay", "director edit plan patch contains unsupported overlay "+string(overlay.Type), path+".add_overlays["+strconv.Itoa(overlayIndex)+"].type"))
			}
			if strings.TrimSpace(overlay.Text) == "" && overlay.Type == model.EditOverlayCaption {
				report.Errors = append(report.Errors, editPlanPatchFinding("empty_caption_overlay", "caption overlays must include text", path+".add_overlays["+strconv.Itoa(overlayIndex)+"].text"))
			}
			if overlay.SourceStepID != "" && overlay.SourceStepID != baseShot.SourceStepID {
				report.Errors = append(report.Errors, editPlanPatchFinding("overlay_step_mismatch", "overlay source_step_id must match the base shot source_step_id", path+".add_overlays["+strconv.Itoa(overlayIndex)+"].source_step_id"))
			}
			if runtimeAdaptive[baseShot.SourceStepID] && containsProductProofLanguage(overlay.Text) {
				report.Warnings = append(report.Warnings, editPlanPatchFinding("runtime_adaptive_caption_overclaims_outcome", "runtime-adaptive caption should not claim a completed product outcome until captured artifacts confirm it", path+".add_overlays["+strconv.Itoa(overlayIndex)+"].text"))
			}
		}
		if runtimeAdaptive[baseShot.SourceStepID] && containsProductProofLanguage(shotPatch.ProposedPurpose) {
			report.Warnings = append(report.Warnings, editPlanPatchFinding("runtime_adaptive_purpose_overclaims_outcome", "runtime-adaptive shot purpose should remain conditional until captured artifacts confirm it", path+".proposed_purpose"))
		}
	}
	report.Valid = len(report.Errors) == 0
	return report
}

func ApplyDirectorEditPlanPatch(basePlan *model.DemoEditPlan, patch *model.DirectorEditPlanPatch, validation *model.DirectorEditPlanPatchValidationReport, createdAt time.Time) (model.DemoEditPlan, model.DirectorEditPlanPatchApplyResult) {
	result := model.DirectorEditPlanPatchApplyResult{
		SchemaVersion: model.DirectorEditPlanPatchApplyResultSchemaVersion,
		ResultID:      "director_edit_plan_patch_apply_" + safeID(firstNonEmptyString(patchID(patch), planID(basePlan), "unknown")),
		CreatedAt:     createdAt,
		PatchID:       patchID(patch),
		BasePlanID:    planID(basePlan),
		Status:        "not_applied",
		Applied:       false,
		Rerendered:    false,
		Notes: []string{
			"Controlled apply only touches presentation fields and never changes source artifacts, source steps, or source time ranges.",
			"Rendered output must still pass the worker DemoEditPlan validation before becoming the final video.",
		},
	}
	if basePlan == nil {
		result.Status = "skipped_missing_base_plan"
		result.Errors = append(result.Errors, editPlanPatchFinding("base_plan_missing", "cannot apply director patch without a base DemoEditPlan", "base_plan"))
		return model.DemoEditPlan{}, result
	}
	if patch == nil {
		result.Status = "skipped_missing_patch"
		result.Errors = append(result.Errors, editPlanPatchFinding("director_patch_missing", "cannot apply missing director edit plan patch", "director_edit_plan_patch"))
		return model.DemoEditPlan{}, result
	}
	result.PatchID = patch.PatchID
	result.BasePlanID = patch.BasePlanID
	if validation == nil || !validation.Valid {
		result.Status = "skipped_invalid_patch"
		result.Errors = append(result.Errors, editPlanPatchFinding("director_patch_validation_failed", "director edit plan patch must validate before controlled apply", "director_edit_plan_patch_validation"))
		if validation != nil {
			result.Errors = append(result.Errors, validation.Errors...)
			result.Warnings = append(result.Warnings, validation.Warnings...)
		}
		return model.DemoEditPlan{}, result
	}
	result.Warnings = append(result.Warnings, validation.Warnings...)
	if patch.Status != "proposed" {
		result.Status = "skipped_no_proposal"
		result.Warnings = append(result.Warnings, editPlanPatchFinding("director_patch_not_proposed", "director edit plan patch has no proposed renderable updates", "status"))
		return model.DemoEditPlan{}, result
	}
	if len(patch.ShotPatches) == 0 {
		result.Status = "skipped_no_shot_patches"
		result.Warnings = append(result.Warnings, editPlanPatchFinding("director_patch_no_shot_patches", "current controlled apply only re-renders shot-level caption and operation changes", "shot_patches"))
		return model.DemoEditPlan{}, result
	}

	plan := cloneDemoEditPlan(*basePlan)
	plan.PlanID = firstNonEmptyString(plan.PlanID, "edit_plan") + "_director_applied"
	result.OutputPlanID = plan.PlanID
	shotIndexByID := map[string]int{}
	for index, shot := range plan.Shots {
		shotIndexByID[shot.ID] = index
	}
	for _, shotPatch := range patch.ShotPatches {
		index, ok := shotIndexByID[shotPatch.ShotID]
		if !ok {
			result.SkippedShotIDs = append(result.SkippedShotIDs, shotPatch.ShotID)
			result.Warnings = append(result.Warnings, editPlanPatchFinding("patch_shot_missing_at_apply", "shot patch could not be matched during controlled apply", "shot_patches."+shotPatch.ShotID))
			continue
		}
		shot := plan.Shots[index]
		appliedShot := false
		if strings.TrimSpace(shotPatch.ProposedPurpose) != "" {
			if shotPatch.RuntimeAdaptive && containsProductProofLanguage(shotPatch.ProposedPurpose) {
				result.Warnings = append(result.Warnings, editPlanPatchFinding("runtime_adaptive_purpose_skipped", "runtime-adaptive purpose overclaims a product outcome and was not applied", "shot_patches."+shotPatch.ShotID+".proposed_purpose"))
			} else {
				shot.Purpose = shotPatch.ProposedPurpose
				appliedShot = true
			}
		}
		if len(shotPatch.AddOperations) > 0 {
			shot.Operations = mergeEditOperations(shot.Operations, shotPatch.AddOperations)
			appliedShot = true
		}
		for _, overlay := range shotPatch.AddOverlays {
			if overlay.Type == model.EditOverlayCaption && shotPatch.RuntimeAdaptive && containsProductProofLanguage(overlay.Text) {
				result.Warnings = append(result.Warnings, editPlanPatchFinding("runtime_adaptive_caption_skipped", "runtime-adaptive caption overclaims a product outcome and was not applied", "shot_patches."+shotPatch.ShotID+".add_overlays"))
				continue
			}
			shot.Overlays = mergeEditOverlay(shot.Overlays, overlay)
			appliedShot = true
		}
		plan.Shots[index] = shot
		if appliedShot {
			result.AppliedShotIDs = append(result.AppliedShotIDs, shotPatch.ShotID)
		} else {
			result.SkippedShotIDs = append(result.SkippedShotIDs, shotPatch.ShotID)
		}
	}
	if patch.GlobalStylePatch != nil {
		plan.GlobalStyle = mergeGlobalStyle(plan.GlobalStyle, patch.GlobalStylePatch)
	}
	if len(result.AppliedShotIDs) == 0 {
		result.Status = "skipped_no_applicable_shots"
		result.Warnings = append(result.Warnings, editPlanPatchFinding("director_patch_no_applicable_shots", "director patch contained shot patches, but none could be safely applied", "shot_patches"))
		return model.DemoEditPlan{}, result
	}
	result.Status = "applied_pending_rerender"
	result.Applied = true
	result.RerenderRequested = true
	return plan, result
}

func MarkDirectorPatchApplyRerendered(result model.DirectorEditPlanPatchApplyResult, renderResult RenderResult) model.DirectorEditPlanPatchApplyResult {
	result.Status = "applied_and_rerendered"
	result.Rerendered = true
	result.OutputVideoPath = renderResult.VideoPath
	result.OutputPlanPath = renderResult.DemoEditPlanPath
	result.RenderManifestPath = renderResult.RenderManifestPath
	return annotateDirectorPatchApplyRenderOutcome(result, renderResult)
}

func MarkDirectorPatchApplyRerenderFailed(result model.DirectorEditPlanPatchApplyResult, err error) model.DirectorEditPlanPatchApplyResult {
	result.Status = "rerender_failed"
	result.Rerendered = false
	if err != nil {
		result.Errors = append(result.Errors, editPlanPatchFinding("director_patch_rerender_failed", err.Error(), "render"))
	}
	return result
}

func editPlanPatchFinding(code string, message string, path string) model.DemoEditPlanValidationFinding {
	return model.DemoEditPlanValidationFinding{Code: code, Message: message, Path: path}
}

func patchID(patch *model.DirectorEditPlanPatch) string {
	if patch == nil {
		return ""
	}
	return patch.PatchID
}

func planID(plan *model.DemoEditPlan) string {
	if plan == nil {
		return ""
	}
	return plan.PlanID
}

func cloneDemoEditPlan(plan model.DemoEditPlan) model.DemoEditPlan {
	out := plan
	out.LockedFields = append([]string{}, plan.LockedFields...)
	out.ModelEditableFields = append([]string{}, plan.ModelEditableFields...)
	out.Shots = make([]model.DemoEditShot, 0, len(plan.Shots))
	for _, shot := range plan.Shots {
		out.Shots = append(out.Shots, cloneDemoEditShot(shot))
	}
	if plan.GlobalStyle != nil {
		style := *plan.GlobalStyle
		out.GlobalStyle = &style
	}
	return out
}

func cloneDemoEditShot(shot model.DemoEditShot) model.DemoEditShot {
	out := shot
	out.SourceTimeRangeMS = cloneRange(shot.SourceTimeRangeMS)
	out.Operations = append([]model.EditOperation{}, shot.Operations...)
	out.Overlays = append([]model.EditOverlay{}, shot.Overlays...)
	return out
}

func mergeEditOperations(existing []model.EditOperation, additions []model.EditOperation) []model.EditOperation {
	out := append([]model.EditOperation{}, existing...)
	for _, addition := range additions {
		if !allowedEditOperation(addition.Type) {
			continue
		}
		if editOperationExists(out, addition) {
			continue
		}
		out = append(out, addition)
	}
	return out
}

func editOperationExists(values []model.EditOperation, want model.EditOperation) bool {
	for _, value := range values {
		if value.Type == want.Type && value.FocusSelector == want.FocusSelector && value.Style == want.Style {
			return true
		}
	}
	return false
}

func mergeEditOverlay(existing []model.EditOverlay, addition model.EditOverlay) []model.EditOverlay {
	if !allowedEditOverlay(addition.Type) {
		return existing
	}
	out := append([]model.EditOverlay{}, existing...)
	if addition.Type == model.EditOverlayCaption {
		for index, overlay := range out {
			if overlay.Type == model.EditOverlayCaption && (addition.SourceStepID == "" || overlay.SourceStepID == "" || overlay.SourceStepID == addition.SourceStepID) {
				out[index] = addition
				return out
			}
		}
	}
	return append(out, addition)
}

func mergeGlobalStyle(existing *model.DemoEditGlobalStyle, patch *model.DemoEditGlobalStyle) *model.DemoEditGlobalStyle {
	if existing == nil {
		existing = &model.DemoEditGlobalStyle{}
	}
	out := *existing
	if patch == nil {
		return &out
	}
	if strings.TrimSpace(patch.ColorGrade) != "" {
		out.ColorGrade = patch.ColorGrade
	}
	if strings.TrimSpace(patch.Pacing) != "" {
		out.Pacing = patch.Pacing
	}
	if strings.TrimSpace(patch.TransitionStyle) != "" {
		out.TransitionStyle = patch.TransitionStyle
	}
	return &out
}

func cloneRange(value *model.MillisecondRange) *model.MillisecondRange {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func rangesEqual(left *model.MillisecondRange, right *model.MillisecondRange) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left[0] == right[0] && left[1] == right[1]
}

func shotDurationMS(shot model.DemoEditShot, fallback int) int {
	if shot.SourceTimeRangeMS == nil {
		return fallback
	}
	duration := shot.SourceTimeRangeMS[1] - shot.SourceTimeRangeMS[0]
	return positiveOrFallback(duration, fallback)
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func allowedEditOperation(value model.EditOperationType) bool {
	for _, allowed := range model.DemoEditAllowedOperations {
		if value == allowed {
			return true
		}
	}
	return false
}

func allowedEditOverlay(value model.EditOverlayType) bool {
	for _, allowed := range model.DemoEditAllowedOverlayTypes {
		if value == allowed {
			return true
		}
	}
	return false
}

func missingRequiredFields(required []string, actual []string) []string {
	missing := []string{}
	for _, value := range required {
		if !containsString(actual, value) {
			missing = append(missing, value)
		}
	}
	return missing
}

func unsupportedPatchEditableFields(values []string) []string {
	unsupported := []string{}
	for _, value := range values {
		if !containsString(model.DemoEditAllowedModelEditableFields, value) {
			unsupported = append(unsupported, value)
		}
	}
	return unsupported
}

func uniquePatchSourceTraces(values []model.DirectorSourceTrace) []model.DirectorSourceTrace {
	out := []model.DirectorSourceTrace{}
	seen := map[string]bool{}
	for _, value := range values {
		key := value.Source + "|" + value.FieldPath + "|" + value.Confidence
		if key == "||" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}
