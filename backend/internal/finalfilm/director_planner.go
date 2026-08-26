package finalfilm

import (
	"context"
	"errors"

	"cascade-demoops/backend/internal/model"
)

type DirectorPlanRequest struct {
	JobID             string
	Constraints       model.StoryboardConstraintSet
	Intents           []model.PresentationGenerationIntent
	Catalog           model.AssetTimelineCatalog
	Baseline          model.DemoEditPlan
	AutomationProfile string
	EvidenceDigest    *model.DirectorEvidenceDigest
	StoryPlan         *model.DirectorStoryPlan
}

type DirectorPlanner interface {
	PlanGeneratedShots(context.Context, DirectorPlanRequest) (model.FinalFilmDirectorPlan, error)
}

func (s *Service) PlanDirectorGeneratedShots(ctx context.Context, jobID string, expectedRevision int) (model.FinalFilmJob, error) {
	if s.planner == nil {
		return model.FinalFilmJob{}, errors.New("final film Director planner is unavailable")
	}
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	manualState := job.State == model.FinalFilmJobAwaitingGenerationApproval && job.AutomationProfile == ""
	automatedState := job.State == model.FinalFilmJobPlanning && job.AutomationProfile == model.FinalFilmAutomationProfileGuidedDemoV1 && job.EvidenceDigest != nil
	if job.Revision != expectedRevision || (!manualState && !automatedState) || job.GenerationAuthorized {
		return model.FinalFilmJob{}, errors.New("Director planning requires the current eligible planning revision")
	}
	var storyPlan *model.DirectorStoryPlan
	if automatedState {
		story, storyErr := buildDirectorStoryPlan(job, *job.EvidenceDigest)
		if storyErr != nil {
			return model.FinalFilmJob{}, storyErr
		}
		storyPlan = &story
	}
	plan, err := s.planner.PlanGeneratedShots(ctx, DirectorPlanRequest{
		JobID: job.JobID, Constraints: job.Constraints, Intents: append([]model.PresentationGenerationIntent{}, job.PresentationIntents...),
		Catalog: job.Catalog, Baseline: job.BaselinePlan,
		AutomationProfile: job.AutomationProfile, EvidenceDigest: job.EvidenceDigest, StoryPlan: storyPlan,
	})
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	if automatedState {
		plan.AutomationProfile = job.AutomationProfile
		plan.EvidenceDigestID = job.EvidenceDigest.DigestID
		if plan.StoryPlan == nil {
			return model.FinalFilmJob{}, errors.New("automated Director omitted its story and material decisions")
		}
	}
	return s.SubmitDirectorPlan(ctx, jobID, expectedRevision, plan)
}
