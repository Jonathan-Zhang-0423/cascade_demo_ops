package finalfilm

import (
	"context"
	"errors"

	"cascade-demoops/backend/internal/model"
)

type DirectorPlanRequest struct {
	JobID       string
	Constraints model.StoryboardConstraintSet
	Intents     []model.PresentationGenerationIntent
	Catalog     model.AssetTimelineCatalog
	Baseline    model.DemoEditPlan
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
	if job.Revision != expectedRevision || job.State != model.FinalFilmJobAwaitingGenerationApproval || job.GenerationAuthorized {
		return model.FinalFilmJob{}, errors.New("Director planning requires the current unapproved awaiting_generation_approval revision")
	}
	plan, err := s.planner.PlanGeneratedShots(ctx, DirectorPlanRequest{
		JobID: job.JobID, Constraints: job.Constraints, Intents: append([]model.PresentationGenerationIntent{}, job.PresentationIntents...),
		Catalog: job.Catalog, Baseline: job.BaselinePlan,
	})
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	return s.SubmitDirectorPlan(ctx, jobID, expectedRevision, plan)
}
