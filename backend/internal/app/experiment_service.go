package app

import (
	"context"
	"errors"

	"cascade-demoops/backend/internal/experiment"
)

type ExperimentResumeRequest struct {
	ExpectedRevision int `json:"expected_revision"`
}

type ExperimentCancelRequest struct {
	ExpectedRevision int    `json:"expected_revision"`
	Reason           string `json:"reason,omitempty"`
}

type ExperimentFinalReviewRequest struct {
	ExpectedRevision  int    `json:"expected_revision"`
	FinalFilmRevision int    `json:"final_film_revision"`
	Decision          string `json:"decision"`
	PackageID         string `json:"package_id"`
	ReviewerRef       string `json:"reviewer_ref"`
	Reason            string `json:"reason,omitempty"`
}

func (s *Service) CreateExperimentRun(ctx context.Context, request experiment.CreateRunRequest) (experiment.Run, error) {
	if s == nil || s.experiments == nil {
		return experiment.Run{}, errors.New("experiment coordinator is unavailable")
	}
	run, err := s.experiments.CreateRun(ctx, request)
	if err == nil && s.experimentAutoStart {
		s.enqueueExperimentRun(run.RunID)
	}
	return run, err
}

func (s *Service) GetExperimentRun(ctx context.Context, runID string) (experiment.Run, error) {
	if s == nil || s.experiments == nil {
		return experiment.Run{}, errors.New("experiment coordinator is unavailable")
	}
	return s.experiments.GetRun(ctx, runID)
}

func (s *Service) ListExperimentEvents(ctx context.Context, runID string) ([]experiment.Event, error) {
	if s == nil || s.experiments == nil {
		return nil, errors.New("experiment coordinator is unavailable")
	}
	return s.experiments.ListEvents(ctx, runID)
}

func (s *Service) ResumeExperimentRun(ctx context.Context, runID string, expectedRevision int) (experiment.Run, error) {
	if s == nil || s.experiments == nil {
		return experiment.Run{}, errors.New("experiment coordinator is unavailable")
	}
	run, err := s.experiments.Resume(ctx, runID, expectedRevision)
	if err == nil && s.experimentAutoStart && run.State == experiment.RunStateQueued {
		s.enqueueExperimentRun(run.RunID)
	}
	return run, err
}

func (s *Service) CancelExperimentRun(ctx context.Context, runID string, request ExperimentCancelRequest) (experiment.Run, error) {
	if s == nil || s.experiments == nil {
		return experiment.Run{}, errors.New("experiment coordinator is unavailable")
	}
	s.experimentRunMu.Lock()
	cancel := s.experimentRunCancels[runID]
	s.experimentRunMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return s.experiments.Cancel(ctx, runID, request.ExpectedRevision, request.Reason)
}

func (s *Service) enqueueExperimentRun(runID string) {
	if s == nil || s.experimentRunner == nil || s.experimentAdapter == nil {
		return
	}
	s.experimentRunMu.Lock()
	if _, exists := s.experimentRunCancels[runID]; exists {
		s.experimentRunMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.experimentRunCancels[runID] = cancel
	s.experimentRunMu.Unlock()
	go func() {
		defer func() {
			s.experimentRunMu.Lock()
			delete(s.experimentRunCancels, runID)
			s.experimentRunMu.Unlock()
			cancel()
		}()
		for {
			run, err := s.experiments.GetRun(ctx, runID)
			if err != nil || run.State == experiment.RunStateWaitingInput || run.State == experiment.RunStateFailed || run.State == experiment.RunStateCanceled || run.State == experiment.RunStateExpired || run.State == experiment.RunStateSucceeded {
				return
			}
			legID := ""
			for _, leg := range run.Legs {
				if leg.State == experiment.RunStateCreated || leg.State == experiment.RunStateQueued || leg.State == experiment.RunStateRunning || leg.State == experiment.RunStateWaitingExternal {
					legID = leg.LegID
					break
				}
			}
			if legID == "" {
				return
			}
			if _, err = s.experimentRunner.RunLeg(ctx, runID, legID, s.experimentAdapter); err != nil {
				return
			}
		}
	}()
}

func (s *Service) resumeRunnableExperiments() {
	runs, err := s.experiments.ListRuns(context.Background())
	if err != nil {
		return
	}
	for _, run := range runs {
		switch run.State {
		case experiment.RunStateQueued, experiment.RunStateRunning, experiment.RunStateWaitingExternal:
			s.enqueueExperimentRun(run.RunID)
		}
	}
}

func (s *Service) RecordExperimentFinalReview(ctx context.Context, runID string, request ExperimentFinalReviewRequest) (experiment.Run, error) {
	if s == nil || s.experiments == nil {
		return experiment.Run{}, errors.New("experiment coordinator is unavailable")
	}
	run, err := s.experiments.GetRun(ctx, runID)
	if err != nil {
		return experiment.Run{}, err
	}
	if run.Revision != request.ExpectedRevision || run.FinalFilm == nil || request.FinalFilmRevision < 1 || run.FinalFilm.JobID == "" {
		return experiment.Run{}, errors.New("experiment final review is not bound to the current run and FinalFilm job")
	}
	job, err := s.ReviewFinalFilmOutput(ctx, run.FinalFilm.JobID, FinalFilmFinalReviewRequest{
		ExpectedRevision: request.FinalFilmRevision, Decision: request.Decision, Reason: request.Reason,
		ReviewerRef: request.ReviewerRef, PackageID: request.PackageID,
	})
	if err != nil {
		return experiment.Run{}, err
	}
	if job.FinalReview == nil || job.FinalReview.Decision != request.Decision {
		return experiment.Run{}, errors.New("FinalFilm final review was not durably recorded")
	}
	return s.experiments.RecordFinalReview(ctx, runID, request.ExpectedRevision, request.Decision, request.PackageID)
}
