package app

import (
	"context"
	"errors"
	"strings"

	"cascade-demoops/backend/internal/finalfilm"
	"cascade-demoops/backend/internal/model"
)

type FinalFilmCreateRequest struct {
	EditorSessionID     string                               `json:"editor_session_id"`
	ExpectedRevision    int                                  `json:"expected_revision"`
	SourcePackageID     string                               `json:"source_package_id,omitempty"`
	PresentationIntents []model.PresentationGenerationIntent `json:"presentation_generation_intents,omitempty"`
}

type FinalFilmGenerationDecisionRequest struct {
	ExpectedRevision int    `json:"expected_revision"`
	Approved         bool   `json:"approved"`
	Reason           string `json:"reason,omitempty"`
}

type FinalFilmDirectorPlanRequest struct {
	ExpectedRevision int                         `json:"expected_revision"`
	Plan             model.FinalFilmDirectorPlan `json:"plan"`
}

type FinalFilmGenerateRequest struct {
	ExpectedRevision  int    `json:"expected_revision"`
	PreferredProvider string `json:"preferred_provider,omitempty"`
}

type FinalFilmCancelRequest struct {
	ExpectedRevision int    `json:"expected_revision"`
	Reason           string `json:"reason"`
}

func (s *Service) CreateFinalFilmJob(ctx context.Context, request FinalFilmCreateRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	session, err := s.GetEditorSession(ctx, request.EditorSessionID)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	if request.ExpectedRevision != session.Revision {
		return model.FinalFilmJob{}, errors.New("editor revision conflict")
	}
	sourcePackageID := strings.TrimSpace(request.SourcePackageID)
	if sourcePackageID == "" && session.Automation != nil {
		sourcePackageID = strings.TrimSpace(session.Automation.SourcePackageID)
	}
	if sourcePackageID == "" {
		sourcePackageID = "editor_session:" + session.SessionID
	}
	return s.finalFilm.CreateJob(ctx, finalfilm.CreateJobRequest{
		EditorSessionID: session.SessionID, EditorRevision: session.Revision, SourcePackageID: sourcePackageID,
		Catalog: session.AssetCatalog, BaselinePlan: session.EditPlan, Intents: request.PresentationIntents, RenderProfile: session.FinalProfile,
	})
}

func (s *Service) RunFinalFilmBaseline(ctx context.Context, jobID string) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.RunBaseline(ctx, jobID)
}

func (s *Service) GetFinalFilmJob(ctx context.Context, jobID string) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.GetJob(ctx, jobID)
}

func (s *Service) ListFinalFilmEvents(ctx context.Context, jobID string) ([]model.FinalFilmEvent, error) {
	if s.finalFilm == nil {
		return nil, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.ListEvents(ctx, jobID)
}

func (s *Service) DecideFinalFilmGeneration(ctx context.Context, jobID string, request FinalFilmGenerationDecisionRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.DecideGeneration(ctx, jobID, request.ExpectedRevision, request.Approved, request.Reason)
}

func (s *Service) SubmitFinalFilmDirectorPlan(ctx context.Context, jobID string, request FinalFilmDirectorPlanRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.SubmitDirectorPlan(ctx, jobID, request.ExpectedRevision, request.Plan)
}

func (s *Service) RunFinalFilmGeneratedCandidates(ctx context.Context, jobID string, request FinalFilmGenerateRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.RunGeneratedCandidates(ctx, jobID, request.ExpectedRevision, request.PreferredProvider)
}

func (s *Service) CancelFinalFilmJob(ctx context.Context, jobID string, request FinalFilmCancelRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.Cancel(ctx, jobID, request.ExpectedRevision, request.Reason)
}
