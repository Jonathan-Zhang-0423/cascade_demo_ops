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

// ApplyGeneratedPatch is the explicit opt-in boundary. It imports exactly one
// normalized, reviewed candidate into an isolated final catalog, validates the
// resulting deterministic plan, and only then renders it. The baseline plan
// and catalog remain immutable in the job for audit and fallback.
func (s *Service) ApplyGeneratedPatch(ctx context.Context, jobID string, expectedRevision int, patchID string) (model.FinalFilmJob, error) {
	job, record, err := s.generatedTrackJob(ctx, jobID, expectedRevision, model.FinalFilmJobAwaitingPatchApply)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	proposal := findPatchProposal(record, strings.TrimSpace(patchID))
	if proposal == nil {
		return model.FinalFilmJob{}, errors.New("generated patch proposal not found")
	}
	if err := media.ValidateGeneratedShotEditPlanPatchProposal(*proposal); err != nil {
		return model.FinalFilmJob{}, err
	}
	if len(record.PatchProposals) != 1 {
		return model.FinalFilmJob{}, errors.New("atomic multi-patch application is required when more than one presentation intent is approved; this renderer revision supports one approved patch per job")
	}
	assetRef := findEditorAssetRef(record, proposal.AssetRefID)
	if assetRef == nil || assetRef.TargetPlanID != job.BaselinePlan.PlanID || assetRef.ExpectedPlanRevision != job.EditorRevision {
		return model.FinalFilmJob{}, errors.New("generated patch lost its approved editor asset or plan binding")
	}
	finalCatalog, finalPlan, shotID, err := compileAppliedGeneratedPlan(job, *proposal, *assetRef)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
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
	rendering.AppliedGeneratedPatchID = proposal.PatchID
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, rendering, s.event(rendering, rendering.Phase, "已显式应用生成候选补丁，开始确定性最终合成", map[string]any{
		"patch_id": proposal.PatchID, "candidate_id": proposal.CandidateID, "adopted_shot_id": shotID,
	})); err != nil {
		return model.FinalFilmJob{}, err
	}
	execution := findProviderExecution(record, proposal.CandidateID)
	provider, providerModel := proposal.Provider, ""
	if execution != nil {
		provider, providerModel = execution.Provider, execution.Model
	}
	result, renderErr := s.renderer.Render(ctx, executor.RenderRequest{
		OutputDir:   filepath.Join(s.outputRoot, job.JobID, fmt.Sprintf("final-r%d", rendering.Revision)),
		DurationSec: maxInt(1, (finalPlan.TargetDurationMS+999)/1000), GeneratedAssets: catalogArtifactRefs(finalCatalog),
		AssetTimelineCatalog: &finalCatalog, EditPlan: &finalPlan, RenderProfile: &job.RenderProfile,
		ModelExecution: &executor.RenderModelExecutionAudit{
			Invoked: true, Provider: provider, Model: providerModel, PlanSource: "explicitly_approved_generated_patch",
			ProviderCallStatus: "candidate_normalized_and_human_approved", RealCallMade: true,
			ProviderOutputAdopted: true, PatchID: proposal.PatchID, PatchApplied: true, AdoptedShotIDs: []string{shotID},
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
		if err := s.store.TransitionJob(context.Background(), job.JobID, rendering.Revision, fallback, s.event(fallback, fallback.Phase, "生成补丁最终合成失败，已回退事实轨基线", map[string]any{"patch_id": proposal.PatchID})); err != nil {
			return model.FinalFilmJob{}, err
		}
		return fallback, nil
	}
	outputValidation, outputErr := validateFinalFilmOutput(ctx, s.renderer, result, job.RenderProfile, s.now().UTC())
	if outputErr != nil {
		fallback := rendering
		fallback.Revision++
		fallback.UpdatedAt = s.now().UTC()
		fallback.State = model.FinalFilmJobCompletedWithoutGenerated
		fallback.Phase = "completed_without_generated_track"
		fallback.GenerationSkipReason = "approved generated patch output validation failed: " + outputErr.Error()
		fallback.FinalRender = fallback.BaselineRender
		fallback.FinalOutputValidation = &outputValidation
		if err := s.store.TransitionJob(context.Background(), job.JobID, rendering.Revision, fallback, s.event(fallback, fallback.Phase, "最终输出验收失败，已回退事实轨基线", map[string]any{"patch_id": proposal.PatchID})); err != nil {
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
		"patch_id": proposal.PatchID, "video_path": result.VideoPath, "provider_output_adopted": true,
	})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return completed, nil
}

func compileAppliedGeneratedPlan(job model.FinalFilmJob, proposal media.GeneratedShotEditPlanPatchProposal, ref media.GeneratedShotEditorAssetRef) (model.AssetTimelineCatalog, model.DemoEditPlan, string, error) {
	if proposal.CandidateID != ref.CandidateID || proposal.IntentID != ref.IntentID || proposal.PatchID == "" {
		return model.AssetTimelineCatalog{}, model.DemoEditPlan{}, "", errors.New("generated patch proposal and editor asset reference do not match")
	}
	durationMS := int(math.Round(ref.DurationSec * 1000))
	if durationMS <= 0 || durationMS != proposal.DurationMS {
		return model.AssetTimelineCatalog{}, model.DemoEditPlan{}, "", errors.New("generated patch duration binding is invalid")
	}
	catalog := job.Catalog
	catalog.Artifacts = append([]model.TimelineArtifact{}, job.Catalog.Artifacts...)
	catalog.Artifacts = append(catalog.Artifacts, model.TimelineArtifact{
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
	})
	plan := job.BaselinePlan
	plan.Shots = append([]model.DemoEditShot{}, job.BaselinePlan.Shots...)
	plan.PlanID = job.BaselinePlan.PlanID + "+" + proposal.PatchID
	rangeMS := model.MillisecondRange{0, durationMS}
	start, end := 0, durationMS
	shotID := "generated_shot_" + proposal.CandidateID
	shot := model.DemoEditShot{
		ID: shotID, SourceArtifactID: ref.AssetRefID, SourceTimeRangeMS: &rangeMS,
		Purpose:    "Optional presentation-only segment approved by human content review and Editor approval.",
		Operations: []model.EditOperation{{Type: model.EditOperationTrim, StartMS: &start, EndMS: &end}},
	}
	switch proposal.Placement {
	case "before_first_required_step":
		plan.Shots = append([]model.DemoEditShot{shot}, plan.Shots...)
	case "after_last_required_step":
		plan.Shots = append(plan.Shots, shot)
	default:
		return model.AssetTimelineCatalog{}, model.DemoEditPlan{}, "", errors.New("this final-film version requires an explicit section anchor for between_sections or presentation_gap placement")
	}
	plan.TargetDurationMS = effectiveTargetDuration(plan)
	return catalog, plan, shotID, nil
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
