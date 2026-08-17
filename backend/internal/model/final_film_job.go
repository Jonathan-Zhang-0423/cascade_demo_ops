package model

import "time"

const FinalFilmJobSchemaVersion = "demoops.final_film_job.v1"

type FinalFilmJobState string

const (
	FinalFilmJobBaselineReady              FinalFilmJobState = "baseline_ready"
	FinalFilmJobRenderingBaseline          FinalFilmJobState = "rendering_baseline"
	FinalFilmJobAwaitingGenerationApproval FinalFilmJobState = "awaiting_generation_approval"
	FinalFilmJobGeneratingCandidates       FinalFilmJobState = "generating_candidates"
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
	SchemaVersion          string                         `json:"schema_version"`
	JobID                  string                         `json:"job_id"`
	EditorSessionID        string                         `json:"editor_session_id"`
	EditorRevision         int                            `json:"editor_revision"`
	SourcePackageID        string                         `json:"source_package_id"`
	State                  FinalFilmJobState              `json:"state"`
	Phase                  string                         `json:"phase,omitempty"`
	Revision               int                            `json:"revision"`
	CreatedAt              time.Time                      `json:"created_at"`
	UpdatedAt              time.Time                      `json:"updated_at"`
	Constraints            StoryboardConstraintSet        `json:"constraints"`
	Catalog                AssetTimelineCatalog           `json:"catalog"`
	BaselinePlan           DemoEditPlan                   `json:"baseline_plan"`
	RenderProfile          EditorRenderProfile            `json:"render_profile"`
	PresentationIntents    []PresentationGenerationIntent `json:"presentation_intents,omitempty"`
	GenerationAuthorized   bool                           `json:"generation_authorized"`
	GenerationAuthorizedAt time.Time                      `json:"generation_authorized_at,omitempty"`
	GenerationSkipReason   string                         `json:"generation_skip_reason,omitempty"`
	BaselineRender         FinalFilmRenderOutput          `json:"baseline_render,omitempty"`
	FinalRender            FinalFilmRenderOutput          `json:"final_render,omitempty"`
	LastError              *FinalFilmJobError             `json:"last_error,omitempty"`
}

type FinalFilmRenderOutput struct {
	Status             string    `json:"status,omitempty"`
	VideoPath          string    `json:"video_path,omitempty"`
	RenderManifestPath string    `json:"render_manifest_path,omitempty"`
	PlanID             string    `json:"plan_id,omitempty"`
	PlanRevision       int       `json:"plan_revision,omitempty"`
	CompletedAt        time.Time `json:"completed_at,omitempty"`
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
