package finalfilm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

const generatedTrackRecordSchemaVersion = "demoops.final_film_generated_track.v1"

type GeneratedTrackRecord struct {
	SchemaVersion     string                                       `json:"schema_version"`
	DirectorPlanID    string                                       `json:"director_plan_id"`
	Intents           []media.GeneratedShotIntent                  `json:"intents"`
	Executions        []media.GeneratedShotProviderExecutionResult `json:"executions,omitempty"`
	Candidates        []media.GeneratedShotCandidate               `json:"candidates,omitempty"`
	StructuralReviews []media.GeneratedShotStructuralReview        `json:"structural_reviews,omitempty"`
	ContentReviews    []media.GeneratedShotContentReview           `json:"content_reviews,omitempty"`
	CandidateSets     []media.GeneratedShotCandidateSet            `json:"candidate_sets,omitempty"`
	Selections        []media.GeneratedShotSelection               `json:"selections,omitempty"`
	EditorApprovals   []media.GeneratedShotEditorApproval          `json:"editor_approvals,omitempty"`
	EditorAssetRefs   []media.GeneratedShotEditorAssetRef          `json:"editor_asset_refs,omitempty"`
	PatchProposals    []media.GeneratedShotEditPlanPatchProposal   `json:"patch_proposals,omitempty"`
	StartedAt         time.Time                                    `json:"started_at,omitempty"`
	UpdatedAt         time.Time                                    `json:"updated_at"`
}

func (s *Service) SubmitDirectorPlan(ctx context.Context, jobID string, expectedRevision int, plan model.FinalFilmDirectorPlan) (model.FinalFilmJob, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	if job.Revision != expectedRevision {
		return model.FinalFilmJob{}, fmt.Errorf("%w: expected %d current %d", ErrRevisionConflict, expectedRevision, job.Revision)
	}
	if job.State != model.FinalFilmJobAwaitingGenerationApproval || job.GenerationAuthorized {
		return model.FinalFilmJob{}, errors.New("director plan requires an unapproved job awaiting generation approval")
	}
	if err := model.ValidateFinalFilmDirectorPlan(plan, job.JobID, job.Constraints, job.PresentationIntents); err != nil {
		return model.FinalFilmJob{}, fmt.Errorf("validate director generated-shot plan: %w", err)
	}
	intents, err := compileGeneratedShotIntents(job, plan)
	if err != nil {
		return model.FinalFilmJob{}, fmt.Errorf("compile director generated-shot plan: %w", err)
	}
	now := s.now().UTC()
	record := GeneratedTrackRecord{
		SchemaVersion: generatedTrackRecordSchemaVersion, DirectorPlanID: plan.PlanID,
		Intents: intents, UpdatedAt: now,
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	next := job
	next.Revision++
	next.UpdatedAt = now
	next.Phase = "director_generated_shot_plan_ready"
	next.DirectorPlan = &plan
	next.GeneratedTrack = raw
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, "Director 展示镜头规格已校验并锁定，等待生成授权", map[string]any{
		"director_plan_id": plan.PlanID, "spec_count": len(plan.Specs), "provider_calls_made": 0,
	})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

// CandidateMediaPath resolves only a normalized candidate already persisted on
// the requested job. HTTP adapters can use it for human review without ever
// accepting an arbitrary filesystem path from the browser.
func (s *Service) CandidateMediaPath(ctx context.Context, jobID, candidateID string) (string, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return "", err
	}
	record, err := decodeGeneratedTrack(job.GeneratedTrack)
	if err != nil {
		return "", err
	}
	candidate := findGeneratedCandidate(record, strings.TrimSpace(candidateID))
	if candidate == nil {
		return "", errors.New("generated candidate not found")
	}
	if err := media.ValidateGeneratedShotCandidate(*candidate); err != nil {
		return "", fmt.Errorf("validate generated candidate media: %w", err)
	}
	path := filepath.Clean(strings.TrimSpace(candidate.NormalizedArtifact.Path))
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat generated candidate media: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("generated candidate media is not a regular file")
	}
	return path, nil
}

// RunGeneratedCandidates is the only final-film service method allowed to
// invoke a video provider. It requires a persisted Director plan and a
// persisted explicit authorization reference from a later job revision.
func (s *Service) RunGeneratedCandidates(ctx context.Context, jobID string, expectedRevision int, preferredProvider string) (model.FinalFilmJob, error) {
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	if job.Revision != expectedRevision {
		return model.FinalFilmJob{}, fmt.Errorf("%w: expected %d current %d", ErrRevisionConflict, expectedRevision, job.Revision)
	}
	if job.State != model.FinalFilmJobGeneratingCandidates || !job.GenerationAuthorized || strings.TrimSpace(job.GenerationAuthorizationRef) == "" {
		return model.FinalFilmJob{}, errors.New("candidate generation requires the current authorized generating_candidates revision")
	}
	if s.providers == nil {
		return model.FinalFilmJob{}, errors.New("generated shot provider registry is unavailable")
	}
	record, err := decodeGeneratedTrack(job.GeneratedTrack)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	if job.DirectorPlan == nil || record.DirectorPlanID != job.DirectorPlan.PlanID || len(record.Intents) == 0 {
		return model.FinalFilmJob{}, errors.New("persisted director plan and generated intents are required")
	}
	record.StartedAt = s.now().UTC()
	for _, intent := range record.Intents {
		adapter, _, selectErr := s.providers.Select(ctx, intent, preferredProvider)
		if selectErr != nil {
			record.Executions = append(record.Executions, media.GeneratedShotProviderExecutionResult{
				SchemaVersion: media.GeneratedShotProviderExecutionSchemaVersion, IntentID: intent.IntentID,
				Status: media.GeneratedShotFailureContinue, FailurePolicy: media.GeneratedShotFailureContinue,
				ErrorClass: "provider_selection_failed", ErrorMessage: selectErr.Error(),
			})
			continue
		}
		idempotencyKey := generatedShotIdempotencyKey(job, intent)
		result, executeErr := adapter.Execute(ctx, media.GeneratedShotProviderExecutionRequest{
			Intent: intent, GenerationAuthorized: true, AuthorizationRef: job.GenerationAuthorizationRef,
			IdempotencyKey: idempotencyKey, AdmissionScope: job.JobID,
			OutputDir: filepath.Join(s.outputRoot, job.JobID, "generated", intent.IntentID, adapter.Descriptor().Provider),
			Timeout:   s.providerTimeout,
		})
		if executeErr != nil && result.ErrorMessage == "" {
			result.ErrorMessage = executeErr.Error()
		}
		record.Executions = append(record.Executions, result)
		if result.Candidate == nil {
			continue
		}
		if err := media.ValidateGeneratedShotCandidate(*result.Candidate); err != nil {
			record.Executions[len(record.Executions)-1].ErrorClass = "candidate_validation_failed"
			record.Executions[len(record.Executions)-1].ErrorMessage = err.Error()
			continue
		}
		review := result.StructuralReview
		if review == nil {
			reviewID, idErr := s.newID("structural_review")
			if idErr != nil {
				return model.FinalFilmJob{}, idErr
			}
			computed := media.ReviewGeneratedShotCandidateStructure(reviewID, intent, *result.Candidate)
			review = &computed
		}
		record.Candidates = append(record.Candidates, *result.Candidate)
		record.StructuralReviews = append(record.StructuralReviews, *review)
	}
	record.UpdatedAt = s.now().UTC()
	raw, err := json.Marshal(record)
	if err != nil {
		return model.FinalFilmJob{}, err
	}
	next := job
	next.Revision++
	next.UpdatedAt = record.UpdatedAt
	next.GeneratedTrack = raw
	message := "生成候选已持久化，等待人工内容审核"
	if len(record.Candidates) == 0 {
		next.State = model.FinalFilmJobCompletedWithoutGenerated
		next.Phase = "completed_without_generated_track"
		next.GenerationSkipReason = "all optional generated shot attempts failed or were unavailable"
		next.FinalRender = next.BaselineRender
		message = "可选生成轨未产出合格候选，已使用事实轨基线完成交付"
	} else {
		next.State = model.FinalFilmJobAwaitingContentReview
		next.Phase = "awaiting_generated_content_review"
	}
	if err := s.store.TransitionJob(ctx, job.JobID, job.Revision, next, s.event(next, next.Phase, message, map[string]any{
		"attempt_count": len(record.Executions), "candidate_count": len(record.Candidates),
	})); err != nil {
		return model.FinalFilmJob{}, err
	}
	return next, nil
}

func compileGeneratedShotIntents(job model.FinalFilmJob, plan model.FinalFilmDirectorPlan) ([]media.GeneratedShotIntent, error) {
	artifacts := make(map[string]model.TimelineArtifact, len(job.Catalog.Artifacts))
	for _, artifact := range job.Catalog.Artifacts {
		artifacts[artifact.ID] = artifact
	}
	result := make([]media.GeneratedShotIntent, 0, len(plan.Specs))
	for _, spec := range plan.Specs {
		intent := media.GeneratedShotIntent{
			IntentID: spec.IntentID, Purpose: generatedShotPurpose(spec.Purpose), Prompt: strings.TrimSpace(spec.Prompt),
			DurationSec: spec.DurationSec, AspectRatio: spec.AspectRatio,
			ContentPolicy: media.GeneratedShotContentPolicy{PresentationOnly: true, RequiresExplicitReview: true},
			FailurePolicy: media.GeneratedShotFailureContinue,
		}
		for _, artifactID := range spec.ReferenceArtifactIDs {
			artifact, ok := artifacts[artifactID]
			if !ok || artifact.Sensitive || !artifact.IncludeInDemo {
				return nil, fmt.Errorf("reference artifact %s is missing, sensitive, or excluded", artifactID)
			}
			uri := strings.TrimSpace(artifact.URI)
			parsed, parseErr := url.Parse(uri)
			if parseErr != nil || !strings.EqualFold(parsed.Scheme, "https") || strings.TrimSpace(parsed.Host) == "" {
				return nil, fmt.Errorf("reference artifact %s must be published as an HTTPS provider-readable asset before generation", artifactID)
			}
			intent.References = append(intent.References, media.GeneratedShotReference{
				ArtifactID: artifact.ID, URI: uri, MimeType: artifact.MimeType, Usage: media.GeneratedShotReferenceGeneral,
			})
		}
		if err := media.ValidateGeneratedShotIntent(intent); err != nil {
			return nil, fmt.Errorf("intent %s: %w", intent.IntentID, err)
		}
		result = append(result, intent)
	}
	return result, nil
}

func generatedShotPurpose(value string) string {
	if value == "abstract_broll" {
		return media.GeneratedShotPurposeAbstractBRoll
	}
	return value
}

func generatedShotIdempotencyKey(job model.FinalFilmJob, intent media.GeneratedShotIntent) string {
	sum := sha256.Sum256([]byte(job.JobID + "\x00" + job.GenerationAuthorizationRef + "\x00" + intent.IntentID + "\x00" + strings.TrimSpace(intent.Prompt)))
	return "finalfilm_" + hex.EncodeToString(sum[:16])
}

func decodeGeneratedTrack(raw json.RawMessage) (GeneratedTrackRecord, error) {
	if len(raw) == 0 {
		return GeneratedTrackRecord{}, errors.New("generated track record is missing")
	}
	var record GeneratedTrackRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return GeneratedTrackRecord{}, fmt.Errorf("decode generated track record: %w", err)
	}
	if record.SchemaVersion != generatedTrackRecordSchemaVersion || strings.TrimSpace(record.DirectorPlanID) == "" {
		return GeneratedTrackRecord{}, errors.New("generated track record schema or director binding is invalid")
	}
	return record, nil
}
