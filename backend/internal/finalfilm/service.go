package finalfilm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

const finalFilmEventSchemaVersion = "demoops.final_film_event.v1"

type Renderer interface {
	Render(context.Context, executor.RenderRequest) (executor.RenderResult, error)
	ValidateEditPlan(context.Context, executor.EditPlanValidationRequest) (model.DemoEditPlanValidationReport, error)
	ProbeMedia(context.Context, executor.MediaProbeRequest) (executor.MediaProbeResult, error)
}

type ServiceOptions struct {
	Store           Store
	Renderer        Renderer
	OutputRoot      string
	Now             func() time.Time
	NewID           func(string) (string, error)
	Providers       *media.GeneratedShotProviderRegistry
	ProviderTimeout time.Duration
}

type Service struct {
	store           Store
	renderer        Renderer
	outputRoot      string
	now             func() time.Time
	newID           func(string) (string, error)
	providers       *media.GeneratedShotProviderRegistry
	providerTimeout time.Duration
}

type CreateJobRequest struct {
	EditorSessionID string
	EditorRevision  int
	SourcePackageID string
	Catalog         model.AssetTimelineCatalog
	BaselinePlan    model.DemoEditPlan
	Intents         []model.PresentationGenerationIntent
	RenderProfile   model.EditorRenderProfile
}

func NewService(options ServiceOptions) (*Service, error) {
	if options.Store == nil || options.Renderer == nil {
		return nil, errors.New("final film store and renderer are required")
	}
	if strings.TrimSpace(options.OutputRoot) == "" {
		return nil, errors.New("final film output root is required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	newID := options.NewID
	if newID == nil {
		newID = randomID
	}
	providerTimeout := options.ProviderTimeout
	if providerTimeout <= 0 {
		providerTimeout = 20 * time.Minute
	}
	return &Service{store: options.Store, renderer: options.Renderer, outputRoot: filepath.Clean(options.OutputRoot), now: now, newID: newID, providers: options.Providers, providerTimeout: providerTimeout}, nil
}

// CreateJob compiles and validates all immutable fact-track constraints before
// persisting anything. A rejected constraint or edit plan therefore leaves no
// resumable job and cannot later reach a provider.
func (s *Service) CreateJob(ctx context.Context, request CreateJobRequest) (model.FinalFilmJob, error) {
	if strings.TrimSpace(request.EditorSessionID) == "" || request.EditorRevision < 1 {
		return model.FinalFilmJob{}, errors.New("editor_session_id and positive editor_revision are required")
	}
	if request.RenderProfile.Width <= 0 || request.RenderProfile.Height <= 0 || request.RenderProfile.FPS <= 0 || strings.TrimSpace(request.RenderProfile.Format) == "" {
		return model.FinalFilmJob{}, errors.New("a complete render_profile is required")
	}
	now := s.now().UTC()
	constraints, err := CompileStoryboardConstraints(ConstraintCompileInput{
		SourcePackageID: request.SourcePackageID, Catalog: request.Catalog, BaselinePlan: request.BaselinePlan,
		Intents: request.Intents, Canvas: model.RenderCanvas{Width: request.RenderProfile.Width, Height: request.RenderProfile.Height, FPS: request.RenderProfile.FPS, Format: request.RenderProfile.Format}, Now: now,
	})
	if err != nil {
		return model.FinalFilmJob{}, fmt.Errorf("compile final film constraints: %w", err)
	}
	validation, err := s.renderer.ValidateEditPlan(ctx, executor.EditPlanValidationRequest{Catalog: request.Catalog, EditPlan: request.BaselinePlan})
	if err != nil {
		return model.FinalFilmJob{}, fmt.Errorf("validate baseline edit plan: %w", err)
	}
	if !validation.Valid {
		return model.FinalFilmJob{}, fmt.Errorf("baseline edit plan is invalid: %+v", validation.Errors)
	}
	jobID, err := s.newID("finalfilm")
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	job := model.FinalFilmJob{
		SchemaVersion: model.FinalFilmJobSchemaVersion, JobID: jobID, EditorSessionID: request.EditorSessionID,
		EditorRevision: request.EditorRevision, SourcePackageID: request.SourcePackageID,
		State: model.FinalFilmJobBaselineReady, Phase: "baseline_validated", Revision: 1, CreatedAt: now, UpdatedAt: now,
		Constraints: constraints, Catalog: request.Catalog, BaselinePlan: request.BaselinePlan,
		RenderProfile: request.RenderProfile, PresentationIntents: append([]model.PresentationGenerationIntent{}, request.Intents...),
	}
	if err := ValidateJob(job); err != nil {
		return model.FinalFilmJob{}, err
	}
	event := s.event(job, "baseline_validated", "事实轨约束和基线编辑计划已通过验证", map[string]any{
		"constraint_set_id": constraints.ConstraintSetID, "plan_id": request.BaselinePlan.PlanID,
		"provider_calls_made": 0, "generation_authorized": false,
	})
	if err := s.store.CreateJobWithEvent(ctx, job, event); err != nil {
		return model.FinalFilmJob{}, err
	}
	return job, nil
}

// RunBaseline renders the factual timeline before any optional provider work.
// If presentation intents exist, the durable baseline remains available while
// the job waits for an explicit generation decision.
func (s *Service) RunBaseline(ctx context.Context, jobID string) (model.FinalFilmJob, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	if job.State != model.FinalFilmJobBaselineReady {
		return model.FinalFilmJob{}, fmt.Errorf("baseline render requires state %s, got %s", model.FinalFilmJobBaselineReady, job.State)
	}
	rendering := job
	rendering.State = model.FinalFilmJobRenderingBaseline
	rendering.Phase = "rendering_fact_track"
	rendering.Revision++
	rendering.UpdatedAt = s.now().UTC()
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, rendering, s.event(rendering, rendering.Phase, "开始渲染确定性事实轨基线", map[string]any{"provider_calls_made": 0})); err != nil {
		return model.FinalFilmJob{}, err
	}

	result, renderErr := s.renderer.Render(ctx, executor.RenderRequest{
		OutputDir:   filepath.Join(s.outputRoot, job.JobID, fmt.Sprintf("baseline-r%d", rendering.Revision)),
		DurationSec: maxInt(1, (job.Constraints.TargetDurationMS+999)/1000), GeneratedAssets: catalogArtifactRefs(job.Catalog),
		AssetTimelineCatalog: &job.Catalog, EditPlan: &job.BaselinePlan, RenderProfile: &job.RenderProfile,
		ModelExecution: &executor.RenderModelExecutionAudit{Invoked: false, PlanSource: "validated_fact_track_baseline", ProviderOutputAdopted: false, Note: "baseline render forbids provider execution"},
	})
	if renderErr != nil {
		failed := rendering
		failed.State = model.FinalFilmJobFailed
		failed.Phase = "baseline_render_failed"
		failed.Revision++
		failed.UpdatedAt = s.now().UTC()
		failed.LastError = &model.FinalFilmJobError{Code: "baseline_render_failed", Message: renderErr.Error(), Retryable: true, OccurredAt: failed.UpdatedAt}
		_ = s.store.TransitionJob(context.Background(), failed.JobID, rendering.Revision, failed, s.event(failed, failed.Phase, "确定性事实轨基线渲染失败", nil))
		return failed, renderErr
	}

	completedAt := s.now().UTC()
	next := rendering
	next.Revision++
	next.UpdatedAt = completedAt
	next.BaselineRender = model.FinalFilmRenderOutput{
		Status: "ready", VideoPath: result.VideoPath, RenderManifestPath: result.RenderManifestPath,
		PlanID: job.BaselinePlan.PlanID, PlanRevision: job.EditorRevision, CompletedAt: completedAt,
	}
	message := "确定性事实轨基线已完成"
	if len(job.PresentationIntents) > 0 {
		next.State = model.FinalFilmJobAwaitingGenerationApproval
		next.Phase = "baseline_ready_awaiting_generation_approval"
		message = "事实轨基线已完成，等待可选展示镜头生成授权"
	} else {
		next.State = model.FinalFilmJobCompleted
		next.Phase = "completed_fact_track_only"
		next.FinalRender = next.BaselineRender
	}
	if err := s.store.TransitionJob(ctx, next.JobID, rendering.Revision, next, s.event(next, next.Phase, message, map[string]any{
		"provider_calls_made": 0, "video_path": result.VideoPath,
	})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func (s *Service) DecideGeneration(ctx context.Context, jobID string, expectedRevision int, approve bool, reason string) (model.FinalFilmJob, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	if job.Revision != expectedRevision {
		return model.FinalFilmJob{}, fmt.Errorf("%w: expected %d current %d", ErrRevisionConflict, expectedRevision, job.Revision)
	}
	if job.State != model.FinalFilmJobAwaitingGenerationApproval {
		return model.FinalFilmJob{}, errors.New("generation decision requires awaiting_generation_approval state")
	}
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	if approve {
		if job.DirectorPlan == nil {
			return model.FinalFilmJob{}, errors.New("generation approval requires a validated persisted director plan")
		}
		if _, err := decodeGeneratedTrack(job.GeneratedTrack); err != nil {
			return model.FinalFilmJob{}, err
		}
		next.GenerationAuthorized = true
		next.GenerationAuthorizedAt = next.UpdatedAt
		next.GenerationAuthorizationRef = fmt.Sprintf("finalfilm:%s:r%d", job.JobID, next.Revision)
		next.State = model.FinalFilmJobGeneratingCandidates
		next.Phase = "generation_authorized_pending_provider_runner"
	} else {
		if strings.TrimSpace(reason) == "" {
			return model.FinalFilmJob{}, errors.New("generation rejection reason is required")
		}
		next.GenerationSkipReason = strings.TrimSpace(reason)
		next.State = model.FinalFilmJobCompletedWithoutGenerated
		next.Phase = "completed_without_generated_track"
		next.FinalRender = next.BaselineRender
	}
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "已记录展示镜头生成决策", map[string]any{"approved": approve, "provider_calls_made": 0})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

// RecordGenerationFailure preserves the already rendered baseline and closes
// the optional generated track without turning the delivery into a failure.
func (s *Service) RecordGenerationFailure(ctx context.Context, jobID string, expectedRevision int, reason string) (model.FinalFilmJob, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	if job.Revision != expectedRevision || job.State != model.FinalFilmJobGeneratingCandidates {
		return model.FinalFilmJob{}, errors.New("generation fallback requires the current generating_candidates revision")
	}
	if strings.TrimSpace(reason) == "" {
		return model.FinalFilmJob{}, errors.New("generation failure reason is required")
	}
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.State = model.FinalFilmJobCompletedWithoutGenerated
	next.Phase = "completed_without_generated_track"
	next.GenerationSkipReason = strings.TrimSpace(reason)
	next.FinalRender = next.BaselineRender
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "可选生成轨失败，已使用事实轨基线完成交付", map[string]any{"failure_policy": model.PresentationGenerationFailureContinue})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func (s *Service) GetJob(ctx context.Context, jobID string) (model.FinalFilmJob, error) {
	return s.store.GetJob(ctx, jobID)
}

func (s *Service) ListEvents(ctx context.Context, jobID string) ([]model.FinalFilmEvent, error) {
	return s.store.ListEvents(ctx, jobID)
}

func (s *Service) Cancel(ctx context.Context, jobID string, expectedRevision int, reason string) (model.FinalFilmJob, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	if job.Revision != expectedRevision {
		return model.FinalFilmJob{}, fmt.Errorf("%w: expected %d current %d", ErrRevisionConflict, expectedRevision, job.Revision)
	}
	if finalFilmTerminalState(job.State) {
		return model.FinalFilmJob{}, errors.New("completed, failed, or cancelled final film job cannot be cancelled")
	}
	if strings.TrimSpace(reason) == "" {
		return model.FinalFilmJob{}, errors.New("cancellation reason is required")
	}
	next := job
	next.Revision++
	next.UpdatedAt = s.now().UTC()
	next.State = model.FinalFilmJobCancelled
	next.Phase = "cancelled"
	next.LastError = &model.FinalFilmJobError{Code: "cancelled", Message: strings.TrimSpace(reason), Retryable: false, OccurredAt: next.UpdatedAt}
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "最终成片作业已取消", map[string]any{"reason": strings.TrimSpace(reason)})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func ValidateJob(job model.FinalFilmJob) error {
	if job.SchemaVersion != model.FinalFilmJobSchemaVersion || strings.TrimSpace(job.JobID) == "" || job.Revision < 1 || job.CreatedAt.IsZero() || job.UpdatedAt.IsZero() {
		return errors.New("invalid final film job identity or revision metadata")
	}
	if err := model.ValidateStoryboardConstraintSet(job.Constraints); err != nil {
		return err
	}
	if job.Catalog.CatalogID != job.Constraints.CatalogID || job.BaselinePlan.CatalogID != job.Catalog.CatalogID {
		return errors.New("final film job catalog, constraints, and baseline plan are not bound")
	}
	if err := model.ValidatePresentationGenerationIntents(job.PresentationIntents); err != nil {
		return err
	}
	if job.GenerationAuthorized && job.GenerationAuthorizedAt.IsZero() {
		return errors.New("authorized generation requires an authorization timestamp")
	}
	if job.GenerationAuthorized && (strings.TrimSpace(job.GenerationAuthorizationRef) == "" || job.DirectorPlan == nil) {
		return errors.New("authorized generation requires a persisted director plan and authorization reference")
	}
	if job.DirectorPlan != nil {
		if err := model.ValidateFinalFilmDirectorPlan(*job.DirectorPlan, job.JobID, job.Constraints, job.PresentationIntents); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) event(job model.FinalFilmJob, phase string, message string, metadata map[string]any) model.FinalFilmEvent {
	eventID, err := s.newID("event")
	if err != nil {
		eventID = fmt.Sprintf("event_%s_r%d", job.JobID, job.Revision)
	}
	return model.FinalFilmEvent{
		SchemaVersion: finalFilmEventSchemaVersion, EventID: eventID, JobID: job.JobID, State: job.State,
		Phase: phase, Message: message, Metadata: metadata, CreatedAt: s.now().UTC(),
	}
}

func catalogArtifactRefs(catalog model.AssetTimelineCatalog) []model.ArtifactRef {
	result := make([]model.ArtifactRef, 0, len(catalog.Artifacts))
	for _, artifact := range catalog.Artifacts {
		result = append(result, model.ArtifactRef{
			ID: artifact.ID, Kind: artifact.Kind, URI: artifact.URI, MimeType: artifact.MimeType, Label: artifact.Label,
			SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes, Sensitive: artifact.Sensitive,
			Metadata: map[string]any{"include_in_demo": artifact.IncludeInDemo, "asset_role": artifact.AssetRole, "duration_ms": artifact.DurationMS},
		})
	}
	return result
}

func randomID(prefix string) (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return strings.TrimSpace(prefix) + "_" + hex.EncodeToString(buffer), nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func finalFilmTerminalState(state model.FinalFilmJobState) bool {
	switch state {
	case model.FinalFilmJobCompleted, model.FinalFilmJobCompletedWithoutGenerated, model.FinalFilmJobFailed, model.FinalFilmJobCancelled:
		return true
	default:
		return false
	}
}
