package finalfilm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

func (s *Service) RecordGeneratedContentReview(ctx context.Context, jobID string, expectedRevision int, decision media.GeneratedShotContentReviewDecision) (model.FinalFilmJob, error) {
	job, record, err := s.generatedTrackJob(ctx, jobID, expectedRevision, model.FinalFilmJobAwaitingContentReview)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	structural := findStructuralReview(record, decision.StructuralReviewID)
	if structural == nil {
		return model.FinalFilmJob{}, errors.New("structural review not found in generated track")
	}
	if findContentReviewByCandidate(record, decision.CandidateID) != nil {
		return model.FinalFilmJob{}, errors.New("candidate already has a content review")
	}
	review, err := media.RecordGeneratedShotContentReview(*structural, decision)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	record.ContentReviews = append(record.ContentReviews, review)
	nextState := model.FinalFilmJobAwaitingContentReview
	phase := "awaiting_generated_content_review"
	message := "已记录生成候选人工内容审核，仍有候选待审"
	if review.ContentApproved {
		intent := findGeneratedIntent(record, review.IntentID)
		candidate := findGeneratedCandidate(record, review.CandidateID)
		if intent == nil || candidate == nil {
			return model.FinalFilmJob{}, errors.New("approved content review lost its intent or candidate binding")
		}
		setID, err := s.newID("candidate_set")
		if err != nil {
			return model.FinalFilmJob{}, err
		}
		set, err := media.NewGeneratedShotCandidateSet(setID, *intent, media.GeneratedShotCandidateSetModeNormal, []media.GeneratedShotReviewedCandidate{{Candidate: *candidate, ContentReview: review}})
		if err != nil {
			return model.FinalFilmJob{}, err
		}
		record.CandidateSets = append(record.CandidateSets, set)
	}
	if allCandidatesContentReviewed(record) && len(record.CandidateSets) == 0 {
		return s.completeWithoutGeneratedTrack(ctx, job, record, "all generated candidates were rejected by human content review")
	}
	if allCandidatesContentReviewed(record) {
		nextState = model.FinalFilmJobAwaitingCandidateSelection
		phase = "awaiting_generated_candidate_selection"
		message = "所有生成候选已完成人工内容审核，等待人工选择"
	}
	return s.persistGeneratedTrackTransition(ctx, job, record, nextState, phase, message)
}

func (s *Service) RecordGeneratedSelection(ctx context.Context, jobID string, expectedRevision int, decision media.GeneratedShotSelectionDecision) (model.FinalFilmJob, error) {
	job, record, err := s.generatedTrackJob(ctx, jobID, expectedRevision, model.FinalFilmJobAwaitingCandidateSelection)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	set := findCandidateSet(record, decision.SetID)
	if set == nil {
		return model.FinalFilmJob{}, errors.New("candidate set not found in generated track")
	}
	for _, existing := range record.Selections {
		if existing.SetID == decision.SetID {
			return model.FinalFilmJob{}, errors.New("candidate set already has a selection")
		}
	}
	selection, err := media.RecordGeneratedShotSelection(*set, decision)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	record.Selections = append(record.Selections, selection)
	if len(record.Selections) < len(record.CandidateSets) {
		return s.persistGeneratedTrackTransition(ctx, job, record, model.FinalFilmJobAwaitingCandidateSelection, "awaiting_generated_candidate_selection", "已记录候选选择，仍有候选集待选择")
	}
	return s.persistGeneratedTrackTransition(ctx, job, record, model.FinalFilmJobAwaitingEditorApproval, "awaiting_generated_editor_approval", "所有候选集均已选择，等待独立 Editor 批准")
}

// RecordGeneratedEditorApproval creates a provider-neutral patch proposal. It
// still does not mutate EditorSession, apply a patch, or authorize rendering.
func (s *Service) RecordGeneratedEditorApproval(ctx context.Context, jobID string, expectedRevision int, decision media.GeneratedShotEditorApprovalDecision) (model.FinalFilmJob, error) {
	job, record, err := s.generatedTrackJob(ctx, jobID, expectedRevision, model.FinalFilmJobAwaitingEditorApproval)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	selection := findSelection(record, decision.SelectionID)
	if selection == nil {
		return model.FinalFilmJob{}, errors.New("selection not found in generated track")
	}
	for _, existing := range record.EditorApprovals {
		if existing.SelectionID == decision.SelectionID {
			return model.FinalFilmJob{}, errors.New("selection already has an editor approval")
		}
	}
	set := findCandidateSet(record, selection.SetID)
	intent := findGeneratedIntent(record, selection.IntentID)
	candidate := findGeneratedCandidate(record, selection.SelectedCandidateID)
	if set == nil || intent == nil || candidate == nil {
		return model.FinalFilmJob{}, errors.New("selection lost its candidate set, intent, or candidate binding")
	}
	if decision.TargetPlanID != job.BaselinePlan.PlanID || decision.ExpectedPlanRevision != job.EditorRevision {
		return model.FinalFilmJob{}, errors.New("editor approval must bind the baseline plan and captured editor revision")
	}
	approval, err := media.RecordGeneratedShotEditorApproval(*intent, *candidate, *set, *selection, decision)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	assetRef, err := media.CompileGeneratedShotEditorAssetRef(*intent, *candidate, *set, *selection, approval)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	proposal, err := media.CompileGeneratedShotEditPlanPatchProposal(*intent, *candidate, *set, *selection, approval, assetRef)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	record.EditorApprovals = append(record.EditorApprovals, approval)
	record.EditorAssetRefs = append(record.EditorAssetRefs, assetRef)
	record.PatchProposals = append(record.PatchProposals, proposal)
	if len(record.EditorApprovals) < len(record.Selections) {
		return s.persistGeneratedTrackTransition(ctx, job, record, model.FinalFilmJobAwaitingEditorApproval, "awaiting_generated_editor_approval", "已记录 Editor 批准，仍有已选候选待批准")
	}
	return s.persistGeneratedTrackTransition(ctx, job, record, model.FinalFilmJobAwaitingPatchApply, "awaiting_generated_patch_apply", "所有已选候选均经 Editor 批准并生成补丁提案，等待显式应用")
}

func (s *Service) generatedTrackJob(ctx context.Context, jobID string, expectedRevision int, state model.FinalFilmJobState) (model.FinalFilmJob, GeneratedTrackRecord, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return model.FinalFilmJob{}, GeneratedTrackRecord{}, err
	}
	if job.Revision != expectedRevision {
		return model.FinalFilmJob{}, GeneratedTrackRecord{}, fmt.Errorf("%w: expected %d current %d", ErrRevisionConflict, expectedRevision, job.Revision)
	}
	if job.State != state {
		return model.FinalFilmJob{}, GeneratedTrackRecord{}, fmt.Errorf("generated track operation requires state %s, got %s", state, job.State)
	}
	record, err := decodeGeneratedTrack(job.GeneratedTrack)
	return job, record, err
}

func (s *Service) persistGeneratedTrackTransition(ctx context.Context, job model.FinalFilmJob, record GeneratedTrackRecord, state model.FinalFilmJobState, phase, message string) (model.FinalFilmJob, error) {
	record.UpdatedAt = s.now().UTC()
	raw, err := json.Marshal(record)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	next := job
	next.Revision++
	next.UpdatedAt = record.UpdatedAt
	next.State = state
	next.Phase = phase
	next.GeneratedTrack = raw
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, phase, message, map[string]any{
		"content_reviews": len(record.ContentReviews), "candidate_sets": len(record.CandidateSets),
		"selections": len(record.Selections), "editor_approvals": len(record.EditorApprovals), "patch_proposals": len(record.PatchProposals),
	})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func (s *Service) completeWithoutGeneratedTrack(ctx context.Context, job model.FinalFilmJob, record GeneratedTrackRecord, reason string) (model.FinalFilmJob, error) {
	record.UpdatedAt = s.now().UTC()
	raw, err := json.Marshal(record)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	next := job
	next.Revision++
	next.UpdatedAt = record.UpdatedAt
	next.State = model.FinalFilmJobCompletedWithoutGenerated
	next.Phase = "completed_without_generated_track"
	next.GeneratedTrack = raw
	next.GenerationSkipReason = reason
	next.FinalRender = next.BaselineRender
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "生成候选未通过人工内容审核，已锁定事实轨基线为最终输出", map[string]any{"reason": reason})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func findStructuralReview(record GeneratedTrackRecord, id string) *media.GeneratedShotStructuralReview {
	for index := range record.StructuralReviews {
		if record.StructuralReviews[index].ReviewID == id {
			return &record.StructuralReviews[index]
		}
	}
	return nil
}
func findContentReviewByCandidate(record GeneratedTrackRecord, id string) *media.GeneratedShotContentReview {
	for index := range record.ContentReviews {
		if record.ContentReviews[index].CandidateID == id {
			return &record.ContentReviews[index]
		}
	}
	return nil
}
func findGeneratedIntent(record GeneratedTrackRecord, id string) *media.GeneratedShotIntent {
	for index := range record.Intents {
		if record.Intents[index].IntentID == id {
			return &record.Intents[index]
		}
	}
	return nil
}
func findGeneratedCandidate(record GeneratedTrackRecord, id string) *media.GeneratedShotCandidate {
	for index := range record.Candidates {
		if record.Candidates[index].CandidateID == id {
			return &record.Candidates[index]
		}
	}
	return nil
}
func findCandidateSet(record GeneratedTrackRecord, id string) *media.GeneratedShotCandidateSet {
	for index := range record.CandidateSets {
		if record.CandidateSets[index].SetID == id {
			return &record.CandidateSets[index]
		}
	}
	return nil
}
func findSelection(record GeneratedTrackRecord, id string) *media.GeneratedShotSelection {
	for index := range record.Selections {
		if record.Selections[index].SelectionID == id {
			return &record.Selections[index]
		}
	}
	return nil
}
func allCandidatesContentReviewed(record GeneratedTrackRecord) bool {
	return len(record.Candidates) > 0 && len(record.ContentReviews) >= len(record.Candidates)
}
