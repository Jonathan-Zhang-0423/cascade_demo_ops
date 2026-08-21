package finalfilm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

// ApplyGeneratedPatch preserves the original single-patch API as a wrapper.
func (s *Service) ApplyGeneratedPatch(ctx context.Context, jobID string, expectedRevision int, patchID string) (model.FinalFilmJob, error) {
	return s.ApplyGeneratedPatches(ctx, jobID, expectedRevision, []string{patchID})
}

// ApplyGeneratedPatches is the atomic explicit opt-in boundary. The request
// must name every approved proposal exactly once and in the desired order.
func (s *Service) ApplyGeneratedPatches(ctx context.Context, jobID string, expectedRevision int, patchIDs []string) (model.FinalFilmJob, error) {
	job, record, err := s.generatedTrackJob(ctx, jobID, expectedRevision, model.FinalFilmJobAwaitingPatchApply)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	proposals, err := selectAtomicPatchBatch(record, patchIDs)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	finalCatalog, finalPlan, shotIDs, provider, providerModel, err := compileAppliedGeneratedPlanBatch(job, record, proposals)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	patchAuditID := strings.Join(patchIDs, ",")
	if err := validateFactTrackUnchanged(job.BaselinePlan, finalPlan); err != nil {
		return model.FinalFilmJob{}, err
	}
	validation, err := s.renderer.ValidateEditPlan(ctx, executor.EditPlanValidationRequest{Catalog: finalCatalog, EditPlan: finalPlan})
	if err != nil {
		return model.FinalFilmJob{}, fmt.Errorf("validate generated patch final plan: %w", err)
	}
	if !validation.Valid {
		return model.FinalFilmJob{}, fmt.Errorf("generated patch final plan is invalid: %+v", validation.Errors)
	}
	rendering := job
	rendering.Revision++
	rendering.UpdatedAt = s.now().UTC()
	rendering.State = model.FinalFilmJobRendering
	rendering.Phase = "rendering_approved_generated_patch"
	rendering.FinalCatalog = &finalCatalog
	rendering.FinalPlan = &finalPlan
	rendering.AppliedGeneratedPatchID = patchAuditID
	rendering.AppliedGeneratedPatchIDs = append([]string{}, patchIDs...)
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, rendering, s.event(rendering, rendering.Phase, "已显式应用生成候选补丁，开始确定性最终合成", map[string]any{
		"patch_ids": patchIDs, "adopted_shot_ids": shotIDs,
	})); err != nil {
		return model.FinalFilmJob{}, err
	}
	result, renderErr := s.renderer.Render(ctx, executor.RenderRequest{
		OutputDir:   filepath.Join(s.outputRoot, job.JobID, fmt.Sprintf("final-r%d", rendering.Revision)),
		DurationSec: maxInt(1, (finalPlan.TargetDurationMS+999)/1000), GeneratedAssets: catalogArtifactRefs(finalCatalog),
		AssetTimelineCatalog: &finalCatalog, EditPlan: &finalPlan, RenderProfile: &job.RenderProfile,
		ModelExecution: &executor.RenderModelExecutionAudit{
			Invoked: true, Provider: provider, Model: providerModel, PlanSource: "explicitly_approved_generated_patch",
			ProviderCallStatus: "candidate_normalized_and_human_approved", RealCallMade: true,
			ProviderOutputAdopted: true, PatchID: patchAuditID, PatchApplied: true, AdoptedShotIDs: shotIDs,
			Note: "provider execution occurred before rendering; renderer performs deterministic composition only",
		},
	})
	if renderErr != nil || strings.TrimSpace(result.VideoPath) == "" || strings.TrimSpace(result.RenderManifestPath) == "" {
		if renderErr == nil {
			renderErr = errors.New("final render did not return video and manifest paths")
		}
		fallback := rendering
		fallback.Revision++
		fallback.UpdatedAt = s.now().UTC()
		fallback.State = model.FinalFilmJobCompletedWithoutGenerated
		fallback.Phase = "completed_without_generated_track"
		fallback.GenerationSkipReason = "approved generated patch render failed: " + renderErr.Error()
		fallback.FinalRender = fallback.BaselineRender
		if err := s.store.TransitionJob(context.Background(), job.JobID, rendering.Revision, fallback, s.event(fallback, fallback.Phase, "生成补丁最终合成失败，已回退事实轨基线", map[string]any{"patch_ids": patchIDs})); err != nil {
			return model.FinalFilmJob{}, err
		}
		return fallback, nil
	}
	outputValidation, outputErr := validateFinalFilmOutput(ctx, s.renderer, result, job.RenderProfile, finalPlan, s.requireTestNarration, s.now().UTC())
	if outputErr != nil {
		fallback := rendering
		fallback.Revision++
		fallback.UpdatedAt = s.now().UTC()
		fallback.State = model.FinalFilmJobCompletedWithoutGenerated
		fallback.Phase = "completed_without_generated_track"
		fallback.GenerationSkipReason = "approved generated patch output validation failed: " + outputErr.Error()
		fallback.FinalRender = fallback.BaselineRender
		fallback.FinalOutputValidation = &outputValidation
		if err := s.store.TransitionJob(context.Background(), job.JobID, rendering.Revision, fallback, s.event(fallback, fallback.Phase, "最终输出验收失败，已回退事实轨基线", map[string]any{"patch_ids": patchIDs})); err != nil {
			return model.FinalFilmJob{}, err
		}
		return fallback, nil
	}
	completed := rendering
	completed.Revision++
	completed.UpdatedAt = s.now().UTC()
	completed.State = model.FinalFilmJobCompleted
	completed.Phase = "completed_with_approved_generated_track"
	completed.FinalRender = model.FinalFilmRenderOutput{
		Status: "ready", VideoPath: result.VideoPath, RenderManifestPath: result.RenderManifestPath,
		PlanID: finalPlan.PlanID, PlanRevision: job.EditorRevision, CompletedAt: completed.UpdatedAt,
	}
	completed.FinalOutputValidation = &outputValidation
	if err := s.store.TransitionJob(ctx, job.JobID, rendering.Revision, completed, s.event(completed, completed.Phase, "已完成含人工批准展示镜头的确定性最终合成", map[string]any{
		"patch_ids": patchIDs, "video_path": result.VideoPath, "provider_output_adopted": true,
	})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return completed, nil
}

func selectAtomicPatchBatch(record GeneratedTrackRecord, patchIDs []string) ([]media.GeneratedShotEditPlanPatchProposal, error) {
	if len(patchIDs) == 0 || len(patchIDs) != len(record.PatchProposals) {
		return nil, errors.New("atomic patch application must name every approved generated patch")
	}
	seen := map[string]bool{}
	selected := make([]media.GeneratedShotEditPlanPatchProposal, 0, len(patchIDs))
	for _, rawID := range patchIDs {
		patchID := strings.TrimSpace(rawID)
		if patchID == "" || seen[patchID] {
			return nil, errors.New("patch_ids contains a blank or duplicate patch")
		}
		seen[patchID] = true
		proposal := findPatchProposal(record, patchID)
		if proposal == nil {
			return nil, fmt.Errorf("generated patch proposal %s not found", patchID)
		}
		if err := media.ValidateGeneratedShotEditPlanPatchProposal(*proposal); err != nil {
			return nil, err
		}
		selected = append(selected, *proposal)
	}
	return selected, nil
}

func compileAppliedGeneratedPlanBatch(job model.FinalFilmJob, record GeneratedTrackRecord, proposals []media.GeneratedShotEditPlanPatchProposal) (model.AssetTimelineCatalog, model.DemoEditPlan, []string, string, string, error) {
	catalog := job.Catalog
	catalog.Artifacts = append([]model.TimelineArtifact{}, job.Catalog.Artifacts...)
	plan := job.BaselinePlan
	plan.Shots = append([]model.DemoEditShot{}, job.BaselinePlan.Shots...)
	prefix := []model.DemoEditShot{}
	suffix := []model.DemoEditShot{}
	afterStep := map[string][]model.DemoEditShot{}
	shotIDs := make([]string, 0, len(proposals))
	providers := map[string]bool{}
	models := map[string]bool{}
	for _, proposal := range proposals {
		ref := findEditorAssetRef(record, proposal.AssetRefID)
		if ref == nil || ref.TargetPlanID != job.BaselinePlan.PlanID || ref.ExpectedPlanRevision != job.EditorRevision {
			return model.AssetTimelineCatalog{}, model.DemoEditPlan{}, nil, "", "", errors.New("generated patch lost its approved editor asset or plan binding")
		}
		if proposal.CandidateID != ref.CandidateID || proposal.IntentID != ref.IntentID || proposal.AnchorAfterStepID != ref.AnchorAfterStepID {
			return model.AssetTimelineCatalog{}, model.DemoEditPlan{}, nil, "", "", errors.New("generated patch proposal and editor asset reference do not match")
		}
		durationMS := int(math.Round(ref.DurationSec * 1000))
		if durationMS <= 0 || durationMS != proposal.DurationMS {
			return model.AssetTimelineCatalog{}, model.DemoEditPlan{}, nil, "", "", errors.New("generated patch duration binding is invalid")
		}
		for _, artifact := range catalog.Artifacts {
			if artifact.ID == ref.AssetRefID {
				return model.AssetTimelineCatalog{}, model.DemoEditPlan{}, nil, "", "", fmt.Errorf("generated asset %s is duplicated", ref.AssetRefID)
			}
		}
		catalog.Artifacts = append(catalog.Artifacts, generatedTimelineArtifact(*ref, durationMS))
		rangeMS := model.MillisecondRange{0, durationMS}
		start, end := 0, durationMS
		shotID := "generated_shot_" + proposal.CandidateID
		shot := model.DemoEditShot{
			ID: shotID, SourceArtifactID: ref.AssetRefID, SourceTimeRangeMS: &rangeMS,
			Purpose:    "Optional presentation-only segment approved by human content review and Editor approval.",
			Operations: []model.EditOperation{{Type: model.EditOperationTrim, StartMS: &start, EndMS: &end}},
		}
		shotIDs = append(shotIDs, shotID)
		switch proposal.Placement {
		case "before_first_required_step":
			prefix = append(prefix, shot)
		case "after_last_required_step":
			suffix = append(suffix, shot)
		case "between_sections", "presentation_gap":
			if !requiredStepExists(job.Constraints.RequiredStepOrder, proposal.AnchorAfterStepID) {
				return model.AssetTimelineCatalog{}, model.DemoEditPlan{}, nil, "", "", fmt.Errorf("generated patch anchor %s is not a required step", proposal.AnchorAfterStepID)
			}
			afterStep[proposal.AnchorAfterStepID] = append(afterStep[proposal.AnchorAfterStepID], shot)
		default:
			return model.AssetTimelineCatalog{}, model.DemoEditPlan{}, nil, "", "", fmt.Errorf("unsupported generated patch placement %s", proposal.Placement)
		}
		providers[proposal.Provider] = true
		if execution := findProviderExecution(record, proposal.CandidateID); execution != nil {
			providers[execution.Provider] = true
			if execution.Model != "" {
				models[execution.Model] = true
			}
		}
	}
	lastShotForStep := map[string]int{}
	for index, shot := range plan.Shots {
		if shot.SourceStepID != "" {
			lastShotForStep[shot.SourceStepID] = index
		}
	}
	assembled := append([]model.DemoEditShot{}, prefix...)
	for index, shot := range plan.Shots {
		assembled = append(assembled, shot)
		if shot.SourceStepID != "" && lastShotForStep[shot.SourceStepID] == index {
			assembled = append(assembled, afterStep[shot.SourceStepID]...)
		}
	}
	assembled = append(assembled, suffix...)
	plan.Shots = assembled
	digest, err := digestJSON(proposals)
	if err != nil {
		return model.AssetTimelineCatalog{}, model.DemoEditPlan{}, nil, "", "", err
	}
	plan.PlanID = job.BaselinePlan.PlanID + "+generated_" + digest[:12]
	plan.TargetDurationMS = timelineDuration(plan)
	return catalog, plan, shotIDs, collapsedIdentity(providers), collapsedIdentity(models), nil
}

func generatedTimelineArtifact(ref media.GeneratedShotEditorAssetRef, durationMS int) model.TimelineArtifact {
	return model.TimelineArtifact{
		ID: ref.AssetRefID, Kind: media.GeneratedShotEditorAssetKind, URI: ref.URI, LocalPath: ref.URI,
		MimeType: ref.MimeType, SHA256: ref.SHA256, SizeBytes: ref.SizeBytes, DurationMS: durationMS,
		AssetRole: "presentation_generated_candidate", IncludeInDemo: true,
		Metadata: map[string]any{
			"media_eligible": true, "approved_for_demo": true, "approval_mode": "explicit_user_review",
			"explicit_review_required": true, "presentation_only": true, "non_authoritative": true,
			"source_material_policy": media.GeneratedShotSourceMaterialPolicy, "artifact_variant": "normalized",
			"normalization_status": "ok", "media_probe_status": "ok", "normalization_profile": ref.NormalizationProfile,
			"width": ref.Width, "height": ref.Height, "fps": ref.FPS, "cfr": ref.CFR,
			"candidate_id": ref.CandidateID, "intent_id": ref.IntentID, "editor_approval_id": ref.EditorApprovalID,
		},
	}
}

func requiredStepExists(required []string, stepID string) bool {
	for _, value := range required {
		if value == stepID {
			return true
		}
	}
	return false
}

func collapsedIdentity(values map[string]bool) string {
	result := []string{}
	for value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	if len(result) == 1 {
		return result[0]
	}
	if len(result) > 1 {
		return "multiple"
	}
	return ""
}

func validateFactTrackUnchanged(baseline, final model.DemoEditPlan) error {
	position := 0
	for _, wanted := range baseline.Shots {
		found := false
		for position < len(final.Shots) {
			candidate := final.Shots[position]
			position++
			if candidate.ID != wanted.ID {
				continue
			}
			if candidate.SourceArtifactID != wanted.SourceArtifactID || candidate.SourceStepID != wanted.SourceStepID || !sameRange(candidate.SourceTimeRangeMS, wanted.SourceTimeRangeMS) {
				return fmt.Errorf("fact shot %s changed its locked source binding or range", wanted.ID)
			}
			found = true
			break
		}
		if !found {
			return fmt.Errorf("fact shot %s is missing or reordered", wanted.ID)
		}
	}
	return nil
}

func sameRange(left, right *model.MillisecondRange) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left[0] == right[0] && left[1] == right[1]
}

func findPatchProposal(record GeneratedTrackRecord, id string) *media.GeneratedShotEditPlanPatchProposal {
	for index := range record.PatchProposals {
		if record.PatchProposals[index].PatchID == id {
			return &record.PatchProposals[index]
		}
	}
	return nil
}

func findEditorAssetRef(record GeneratedTrackRecord, id string) *media.GeneratedShotEditorAssetRef {
	for index := range record.EditorAssetRefs {
		if record.EditorAssetRefs[index].AssetRefID == id {
			return &record.EditorAssetRefs[index]
		}
	}
	return nil
}

func findProviderExecution(record GeneratedTrackRecord, candidateID string) *media.GeneratedShotProviderExecutionResult {
	for index := range record.Executions {
		if record.Executions[index].Candidate != nil && record.Executions[index].Candidate.CandidateID == candidateID {
			return &record.Executions[index]
		}
	}
	return nil
}
