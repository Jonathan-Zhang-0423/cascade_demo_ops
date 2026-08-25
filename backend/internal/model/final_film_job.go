package model

import (
	"encoding/json"
	"time"
)

const FinalFilmJobSchemaVersion = "demoops.final_film_job.v1"

type FinalFilmJobState string

const (
	FinalFilmJobBaselineReady              FinalFilmJobState = "baseline_ready"
	FinalFilmJobRenderingBaseline          FinalFilmJobState = "rendering_baseline"
	FinalFilmJobAwaitingGenerationApproval FinalFilmJobState = "awaiting_generation_approval"
	FinalFilmJobGeneratingCandidates       FinalFilmJobState = "generating_candidates"
	FinalFilmJobAwaitingContentReview      FinalFilmJobState = "awaiting_generated_content_review"
	FinalFilmJobAwaitingCandidateSelection FinalFilmJobState = "awaiting_generated_candidate_selection"
	FinalFilmJobAwaitingEditorApproval     FinalFilmJobState = "awaiting_generated_editor_approval"
	FinalFilmJobAwaitingPatchApply         FinalFilmJobState = "awaiting_generated_patch_apply"
	FinalFilmJobGeneratedTrackSkipped      FinalFilmJobState = "generated_track_skipped"
	FinalFilmJobValidatingFinalPlan        FinalFilmJobState = "validating_final_plan"
	FinalFilmJobRendering                  FinalFilmJobState = "rendering"
	FinalFilmJobValidatingOutput           FinalFilmJobState = "validating_output"
	FinalFilmJobCompleted                  FinalFilmJobState = "completed"
	FinalFilmJobCompletedWithoutGenerated  FinalFilmJobState = "completed_without_generated_track"
	FinalFilmJobFailed                     FinalFilmJobState = "failed"
	FinalFilmJobCancelled                  FinalFilmJobState = "cancelled"
)

type FinalFilmJob struct {
	SchemaVersion              string                         `json:"schema_version"`
	JobID                      string                         `json:"job_id"`
	EditorSessionID            string                         `json:"editor_session_id"`
	EditorRevision             int                            `json:"editor_revision"`
	SourcePackageID            string                         `json:"source_package_id"`
	State                      FinalFilmJobState              `json:"state"`
	Phase                      string                         `json:"phase,omitempty"`
	Revision                   int                            `json:"revision"`
	CreatedAt                  time.Time                      `json:"created_at"`
	UpdatedAt                  time.Time                      `json:"updated_at"`
	Constraints                StoryboardConstraintSet        `json:"constraints"`
	Catalog                    AssetTimelineCatalog           `json:"catalog"`
	BaselinePlan               DemoEditPlan                   `json:"baseline_plan"`
	FinalCatalog               *AssetTimelineCatalog          `json:"final_catalog,omitempty"`
	FinalPlan                  *DemoEditPlan                  `json:"final_plan,omitempty"`
	AppliedGeneratedPatchID    string                         `json:"applied_generated_patch_id,omitempty"`
	AppliedGeneratedPatchIDs   []string                       `json:"applied_generated_patch_ids,omitempty"`
	RenderProfile              EditorRenderProfile            `json:"render_profile"`
	PresentationIntents        []PresentationGenerationIntent `json:"presentation_intents,omitempty"`
	DirectorPlan               *FinalFilmDirectorPlan         `json:"director_plan,omitempty"`
	GenerationAuthorized       bool                           `json:"generation_authorized"`
	GenerationAuthorizedAt     time.Time                      `json:"generation_authorized_at,omitempty"`
	GenerationAuthorizationRef string                         `json:"generation_authorization_ref,omitempty"`
	GeneratedTrack             json.RawMessage                `json:"generated_track,omitempty"`
	GenerationSkipReason       string                         `json:"generation_skip_reason,omitempty"`
	BaselineRender             FinalFilmRenderOutput          `json:"baseline_render,omitempty"`
	FinalRender                FinalFilmRenderOutput          `json:"final_render,omitempty"`
	FinalOutputValidation      *FinalFilmOutputValidation     `json:"final_output_validation,omitempty"`
	LastError                  *FinalFilmJobError             `json:"last_error,omitempty"`
}

type FinalFilmRenderOutput struct {
	Status             string    `json:"status,omitempty"`
	VideoPath          string    `json:"video_path,omitempty"`
	RenderManifestPath string    `json:"render_manifest_path,omitempty"`
	PlanID             string    `json:"plan_id,omitempty"`
	PlanRevision       int       `json:"plan_revision,omitempty"`
	CompletedAt        time.Time `json:"completed_at,omitempty"`
}

type FinalFilmOutputValidation struct {
	Status                  string    `json:"status"`
	DeliveryStatus          string    `json:"delivery_status,omitempty"`
	QualityTier             string    `json:"quality_tier,omitempty"`
	Degradations            []string  `json:"degradations,omitempty"`
	BlockingFailures        []string  `json:"blocking_failures,omitempty"`
	VideoSHA256             string    `json:"video_sha256,omitempty"`
	VideoSizeBytes          int64     `json:"video_size_bytes,omitempty"`
	Width                   int       `json:"width,omitempty"`
	Height                  int       `json:"height,omitempty"`
	FPS                     float64   `json:"fps,omitempty"`
	DurationMS              int       `json:"duration_ms,omitempty"`
	CompositorQualityStatus string    `json:"compositor_quality_status,omitempty"`
	RequirementReportStatus string    `json:"requirement_report_status,omitempty"`
	CheckedAt               time.Time `json:"checked_at"`
	Error                   string    `json:"error,omitempty"`
}

type FinalFilmJobError struct {
	Code       string    `json:"code"`
	Message    string    `json:"message"`
	Retryable  bool      `json:"retryable"`
	OccurredAt time.Time `json:"occurred_at"`
}

type FinalFilmEvent struct {
	SchemaVersion string            `json:"schema_version"`
	EventID       string            `json:"event_id"`
	JobID         string            `json:"job_id"`
	Sequence      int               `json:"sequence"`
	State         FinalFilmJobState `json:"state"`
	Phase         string            `json:"phase,omitempty"`
	Message       string            `json:"message"`
	Metadata      map[string]any    `json:"metadata,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
}
