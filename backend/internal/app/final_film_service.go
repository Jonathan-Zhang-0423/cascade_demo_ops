package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"cascade-demoops/backend/internal/finalfilm"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

type FinalFilmCreateRequest struct {
	EditorSessionID      string                               `json:"editor_session_id"`
	ExpectedRevision     int                                  `json:"expected_revision"`
	SourcePackageID      string                               `json:"source_package_id,omitempty"`
	PresentationIntents  []model.PresentationGenerationIntent `json:"presentation_generation_intents,omitempty"`
	AutomationProfile    string                               `json:"automation_profile,omitempty"`
	ReviewSupplements    []model.FinalFilmReviewSupplement    `json:"review_supplements,omitempty"`
	PublicNarrativeFacts []model.PublicNarrativeFact          `json:"public_narrative_facts,omitempty"`
	MediaCoverage        *model.MediaCoverageReport           `json:"media_coverage,omitempty"`
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

type FinalFilmDirectorPlanningRequest struct {
	ExpectedRevision int `json:"expected_revision"`
}

type FinalFilmGenerateRequest struct {
	ExpectedRevision  int    `json:"expected_revision"`
	PreferredProvider string `json:"preferred_provider,omitempty"`
}

type FinalFilmContentReviewRequest struct {
	ExpectedRevision int                                      `json:"expected_revision"`
	Decision         media.GeneratedShotContentReviewDecision `json:"decision"`
}

type FinalFilmSelectionRequest struct {
	ExpectedRevision int                                  `json:"expected_revision"`
	Decision         media.GeneratedShotSelectionDecision `json:"decision"`
}

type FinalFilmEditorApprovalRequest struct {
	ExpectedRevision int                                       `json:"expected_revision"`
	Decision         media.GeneratedShotEditorApprovalDecision `json:"decision"`
}

type FinalFilmApplyPatchRequest struct {
	ExpectedRevision int      `json:"expected_revision"`
	PatchID          string   `json:"patch_id"`
	PatchIDs         []string `json:"patch_ids,omitempty"`
}

type FinalFilmCancelRequest struct {
	ExpectedRevision int    `json:"expected_revision"`
	Reason           string `json:"reason"`
}

type FinalFilmRunRequest struct {
	ExpectedRevision int    `json:"expected_revision"`
	AuthorizationRef string `json:"authorization_ref"`
	MaxProviderCalls int    `json:"max_provider_calls"`
}

type FinalFilmFinalReviewRequest struct {
	ExpectedRevision int    `json:"expected_revision"`
	Decision         string `json:"decision"`
	Reason           string `json:"reason,omitempty"`
	ReviewerRef      string `json:"reviewer_ref"`
	PackageID        string `json:"package_id"`
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
	if err := validateFinalFilmReviewSupplementRoots(s.runtime.ArtifactRoot, request.ReviewSupplements); err != nil {
		return model.FinalFilmJob{}, err
	}
	return s.finalFilm.CreateJob(ctx, finalfilm.CreateJobRequest{
		EditorSessionID: session.SessionID, EditorRevision: session.Revision, SourcePackageID: sourcePackageID,
		Catalog: session.AssetCatalog, BaselinePlan: session.EditPlan, Intents: request.PresentationIntents, RenderProfile: session.FinalProfile,
		AutomationProfile:    request.AutomationProfile,
		ReviewSupplements:    request.ReviewSupplements,
		PublicNarrativeFacts: request.PublicNarrativeFacts, MediaCoverage: request.MediaCoverage,
	})
}

func validateFinalFilmReviewSupplementRoots(root string, supplements []model.FinalFilmReviewSupplement) error {
	root = filepath.Clean(root)
	for _, supplement := range supplements {
		path := filepath.Clean(strings.TrimSpace(supplement.SourcePath))
		relative, err := filepath.Rel(root, path)
		if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("final film review supplement escapes artifact root")
		}
	}
	return nil
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

func (s *Service) FinalFilmCandidateMediaPath(ctx context.Context, jobID, candidateID string) (string, error) {
	if s.finalFilm == nil {
		return "", errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.CandidateMediaPath(ctx, jobID, candidateID)
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

func (s *Service) PlanFinalFilmDirectorShots(ctx context.Context, jobID string, request FinalFilmDirectorPlanningRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.PlanDirectorGeneratedShots(ctx, jobID, request.ExpectedRevision)
}

func (s *Service) RunFinalFilmGeneratedCandidates(ctx context.Context, jobID string, request FinalFilmGenerateRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.RunGeneratedCandidates(ctx, jobID, request.ExpectedRevision, request.PreferredProvider)
}

func (s *Service) ReviewFinalFilmGeneratedContent(ctx context.Context, jobID string, request FinalFilmContentReviewRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.RecordGeneratedContentReview(ctx, jobID, request.ExpectedRevision, request.Decision)
}

func (s *Service) SelectFinalFilmGeneratedCandidate(ctx context.Context, jobID string, request FinalFilmSelectionRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.RecordGeneratedSelection(ctx, jobID, request.ExpectedRevision, request.Decision)
}

func (s *Service) ApproveFinalFilmGeneratedCandidate(ctx context.Context, jobID string, request FinalFilmEditorApprovalRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.RecordGeneratedEditorApproval(ctx, jobID, request.ExpectedRevision, request.Decision)
}

func (s *Service) ApplyFinalFilmGeneratedPatch(ctx context.Context, jobID string, request FinalFilmApplyPatchRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	patchIDs := append([]string{}, request.PatchIDs...)
	if len(patchIDs) == 0 && strings.TrimSpace(request.PatchID) != "" {
		patchIDs = []string{request.PatchID}
	}
	return s.finalFilm.ApplyGeneratedPatches(ctx, jobID, request.ExpectedRevision, patchIDs)
}

func (s *Service) CancelFinalFilmJob(ctx context.Context, jobID string, request FinalFilmCancelRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.Cancel(ctx, jobID, request.ExpectedRevision, request.Reason)
}

func (s *Service) RunFinalFilmAutomation(ctx context.Context, jobID string, request FinalFilmRunRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	job, err := s.finalFilm.AuthorizeAutomation(ctx, jobID, request.ExpectedRevision, request.AuthorizationRef, request.MaxProviderCalls)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	go func() {
		if _, resumeErr := s.finalFilm.ResumeAutomation(context.Background(), jobID); resumeErr != nil {
			_, _ = s.finalFilm.MarkAutomationFailed(context.Background(), jobID, resumeErr)
		}
	}()
	return job, nil
}

func (s *Service) RecoverInterruptedFinalFilmBaseline(ctx context.Context, jobID string, expectedRevision int, request FinalFilmCreateRequest) (model.FinalFilmJob, error) {
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
	if err := validateFinalFilmReviewSupplementRoots(s.runtime.ArtifactRoot, request.ReviewSupplements); err != nil {
		return model.FinalFilmJob{}, err
	}
	job, err := s.finalFilm.RecoverInterruptedBaseline(ctx, jobID, expectedRevision, finalfilm.CreateJobRequest{
		EditorSessionID: session.SessionID, EditorRevision: session.Revision, SourcePackageID: request.SourcePackageID,
		Catalog: session.AssetCatalog, BaselinePlan: session.EditPlan, RenderProfile: session.FinalProfile,
		AutomationProfile: request.AutomationProfile, ReviewSupplements: request.ReviewSupplements,
		PublicNarrativeFacts: request.PublicNarrativeFacts, MediaCoverage: request.MediaCoverage,
	})
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	go func() {
		if _, resumeErr := s.finalFilm.ResumeAutomation(context.Background(), jobID); resumeErr != nil {
			_, _ = s.finalFilm.MarkAutomationFailed(context.Background(), jobID, resumeErr)
		}
	}()
	return job, nil
}

func (s *Service) ReviewFinalFilmOutput(ctx context.Context, jobID string, request FinalFilmFinalReviewRequest) (model.FinalFilmJob, error) {
	if s.finalFilm == nil {
		return model.FinalFilmJob{}, errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.RecordFinalReview(ctx, jobID, request.ExpectedRevision, request.Decision, request.Reason, request.ReviewerRef, request.PackageID)
}

func (s *Service) FinalFilmOutputMediaPath(ctx context.Context, jobID string) (string, error) {
	if s.finalFilm == nil {
		return "", errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.FinalMediaPath(ctx, jobID)
}

func (s *Service) FinalFilmReviewPackagePath(ctx context.Context, jobID string) (string, error) {
	if s.finalFilm == nil {
		return "", errors.New("final film workflow is unavailable")
	}
	return s.finalFilm.ReviewPackagePath(ctx, jobID)
}
