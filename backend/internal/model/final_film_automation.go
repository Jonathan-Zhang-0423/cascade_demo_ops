package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	FinalFilmAutomationProfileGuidedDemoV1 = "guided-demo-v1"
	FinalFilmReviewScopeFinalOutput        = "final_output"
	FinalFilmAutomationPolicySchemaVersion = "demoops.final_film_automation_policy.v1"
	DirectorEvidenceDigestSchemaVersion    = "demoops.director_evidence_digest.v1"
	DirectorStoryPlanSchemaVersion         = "demoops.director_story_plan.v1"
	CandidateQualityReportSchemaVersion    = "demoops.candidate_quality_report.v1"
	FinalFilmReviewPackageSchemaVersion    = "demoops.final_film_review_package.v1"
)

type FinalFilmDurationRange struct {
	MinMS int `json:"min_ms"`
	MaxMS int `json:"max_ms"`
}

type FinalFilmProviderPolicy struct {
	IntroProvider      string `json:"intro_provider"`
	OutroProvider      string `json:"outro_provider"`
	TransitionProvider string `json:"transition_provider"`
	MaxAttemptsPerSlot int    `json:"max_attempts_per_slot"`
	FailurePolicy      string `json:"failure_policy"`
}

type FinalFilmAutomationPolicy struct {
	SchemaVersion    string                  `json:"schema_version"`
	Profile          string                  `json:"profile"`
	ReviewScope      string                  `json:"review_scope"`
	TargetDuration   FinalFilmDurationRange  `json:"target_duration"`
	ProviderPolicy   FinalFilmProviderPolicy `json:"provider_policy"`
	SkillVersions    map[string]string       `json:"skill_versions"`
	MaxProviderCalls int                     `json:"max_provider_calls"`
}

type FinalFilmRunAuthorization struct {
	AuthorizationRef  string    `json:"authorization_ref"`
	AuthorizedAt      time.Time `json:"authorized_at"`
	MaxProviderCalls  int       `json:"max_provider_calls"`
	ProviderCallsUsed int       `json:"provider_calls_used"`
}

type FinalFilmProviderAttempt struct {
	IntentID       string    `json:"intent_id"`
	Provider       string    `json:"provider"`
	Attempt        int       `json:"attempt"`
	IdempotencyKey string    `json:"idempotency_key"`
	ProviderTaskID string    `json:"provider_task_id,omitempty"`
	Status         string    `json:"status"`
	RecoveryCount  int       `json:"recovery_count,omitempty"`
	AdmittedAt     time.Time `json:"admitted_at"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
}

type DirectorEvidenceDigest struct {
	SchemaVersion      string                      `json:"schema_version"`
	DigestID           string                      `json:"digest_id"`
	Objective          string                      `json:"objective"`
	SourceDurationMS   int                         `json:"source_duration_ms"`
	RequiredSteps      []DirectorEvidenceStep      `json:"required_steps"`
	WaitRanges         []MillisecondRange          `json:"wait_ranges,omitempty"`
	InteractionDensity float64                     `json:"interaction_density"`
	VisualStyle        DirectorVisualStyleEvidence `json:"visual_style"`
	Audio              DirectorAudioEvidence       `json:"audio"`
	CreatedAt          time.Time                   `json:"created_at"`
}

type DirectorEvidenceStep struct {
	StepID          string           `json:"step_id"`
	Order           int              `json:"order"`
	Action          string           `json:"action,omitempty"`
	ExpectedOutcome string           `json:"expected_outcome,omitempty"`
	ObservedState   string           `json:"observed_state,omitempty"`
	SourceRangeMS   MillisecondRange `json:"source_range_ms"`
	ArtifactIDs     []string         `json:"artifact_ids"`
}

type DirectorVisualStyleEvidence struct {
	DominantColors []string `json:"dominant_colors,omitempty"`
	Tone           string   `json:"tone"`
	MotionLevel    string   `json:"motion_level"`
}

type DirectorAudioEvidence struct {
	SourceAudioPresent bool `json:"source_audio_present"`
	NarrationPresent   bool `json:"narration_present"`
}

type DirectorStoryPlan struct {
	SchemaVersion    string                    `json:"schema_version"`
	TargetDurationMS int                       `json:"target_duration_ms"`
	Beats            []DirectorStoryBeat       `json:"beats"`
	Timeline         []DirectorTimelineSegment `json:"timeline"`
	AudioPlan        DirectorAudioPlan         `json:"audio_plan"`
	SkillVersions    map[string]string         `json:"skill_versions"`
	DecisionLog      []string                  `json:"decision_log"`
}

type DirectorStoryBeat struct {
	BeatID     string   `json:"beat_id"`
	Purpose    string   `json:"purpose"`
	StepIDs    []string `json:"step_ids,omitempty"`
	Caption    string   `json:"caption,omitempty"`
	DurationMS int      `json:"duration_ms"`
}

type DirectorTimelineSegment struct {
	SegmentID         string            `json:"segment_id"`
	Kind              string            `json:"kind"`
	SourceArtifactID  string            `json:"source_artifact_id,omitempty"`
	SourceStepID      string            `json:"source_step_id,omitempty"`
	SourceRangeMS     *MillisecondRange `json:"source_range_ms,omitempty"`
	IntentID          string            `json:"intent_id,omitempty"`
	Placement         string            `json:"placement"`
	AnchorAfterStepID string            `json:"anchor_after_step_id,omitempty"`
	Speed             float64           `json:"speed"`
	OutputDurationMS  int               `json:"output_duration_ms"`
	Caption           string            `json:"caption,omitempty"`
}

type DirectorAudioPlan struct {
	TargetLUFS     float64 `json:"target_lufs"`
	TruePeakDB     float64 `json:"true_peak_db"`
	PreserveSource bool    `json:"preserve_source"`
	BackgroundMode string  `json:"background_mode"`
}

type CandidateQualityReport struct {
	SchemaVersion string    `json:"schema_version"`
	ReportID      string    `json:"report_id"`
	IntentID      string    `json:"intent_id"`
	CandidateID   string    `json:"candidate_id,omitempty"`
	Provider      string    `json:"provider"`
	Attempt       int       `json:"attempt"`
	TechnicalPass bool      `json:"technical_pass"`
	ContentPass   bool      `json:"content_pass"`
	Score         float64   `json:"score"`
	Findings      []string  `json:"findings,omitempty"`
	Decision      string    `json:"decision"`
	CheckedAt     time.Time `json:"checked_at"`
}

type FinalFilmReviewPackage struct {
	SchemaVersion string                       `json:"schema_version"`
	PackageID     string                       `json:"package_id"`
	JobID         string                       `json:"job_id"`
	JobRevision   int                          `json:"job_revision"`
	DirectoryPath string                       `json:"directory_path"`
	ZIPPath       string                       `json:"zip_path"`
	ManifestPath  string                       `json:"manifest_path"`
	Files         []FinalFilmReviewPackageFile `json:"files"`
	CreatedAt     time.Time                    `json:"created_at"`
}

type FinalFilmReviewPackageFile struct {
	Role         string `json:"role"`
	RelativePath string `json:"relative_path"`
	Source       string `json:"source"`
	Required     bool   `json:"required"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256,omitempty"`
}

type FinalFilmFinalReview struct {
	Decision    string    `json:"decision"`
	Reason      string    `json:"reason,omitempty"`
	ReviewerRef string    `json:"reviewer_ref"`
	JobRevision int       `json:"job_revision"`
	PackageID   string    `json:"package_id"`
	ReviewedAt  time.Time `json:"reviewed_at"`
}

func DefaultFinalFilmAutomationPolicy() FinalFilmAutomationPolicy {
	return FinalFilmAutomationPolicy{
		SchemaVersion: FinalFilmAutomationPolicySchemaVersion,
		Profile:       FinalFilmAutomationProfileGuidedDemoV1, ReviewScope: FinalFilmReviewScopeFinalOutput,
		TargetDuration: FinalFilmDurationRange{MinMS: 90_000, MaxMS: 120_000},
		ProviderPolicy: FinalFilmProviderPolicy{
			IntroProvider: "minimax-h3", OutroProvider: "minimax-h3", TransitionProvider: "seedance-2.5",
			MaxAttemptsPerSlot: 2, FailurePolicy: PresentationGenerationFailureContinue,
		},
		SkillVersions: map[string]string{
			"director-evidence-story": "1.1.0", "director-generated-shots": "1.1.0",
			"director-timeline-compose": "1.1.0", "director-quality-gate": "1.1.0",
			"final-film-director-harness": "1.1.0",
		},
		MaxProviderCalls: 8,
	}
}

func ValidateFinalFilmAutomationPolicy(policy FinalFilmAutomationPolicy) error {
	if policy.SchemaVersion != FinalFilmAutomationPolicySchemaVersion || policy.Profile != FinalFilmAutomationProfileGuidedDemoV1 || policy.ReviewScope != FinalFilmReviewScopeFinalOutput {
		return errors.New("unsupported final film automation policy")
	}
	if policy.TargetDuration.MinMS != 90_000 || policy.TargetDuration.MaxMS != 120_000 {
		return errors.New("guided demo target duration must be 90-120 seconds")
	}
	providers := policy.ProviderPolicy
	if providers.IntroProvider != "minimax-h3" || providers.OutroProvider != "minimax-h3" || providers.TransitionProvider != "seedance-2.5" || providers.MaxAttemptsPerSlot != 2 || providers.FailurePolicy != PresentationGenerationFailureContinue {
		return errors.New("guided demo provider roles or retry policy are invalid")
	}
	if policy.MaxProviderCalls < 1 || policy.MaxProviderCalls > 8 || len(policy.SkillVersions) < 5 {
		return errors.New("guided demo provider budget or skill versions are incomplete")
	}
	return nil
}

func ValidateDirectorEvidenceDigest(digest DirectorEvidenceDigest) error {
	if digest.SchemaVersion != DirectorEvidenceDigestSchemaVersion || strings.TrimSpace(digest.DigestID) == "" || strings.TrimSpace(digest.Objective) == "" || digest.SourceDurationMS <= 0 || digest.CreatedAt.IsZero() {
		return errors.New("director evidence digest identity, objective, duration, and created_at are required")
	}
	if len(digest.RequiredSteps) == 0 {
		return errors.New("director evidence digest requires factual steps")
	}
	for index, step := range digest.RequiredSteps {
		if strings.TrimSpace(step.StepID) == "" || step.Order != index+1 || step.SourceRangeMS[0] < 0 || step.SourceRangeMS[1] <= step.SourceRangeMS[0] || len(step.ArtifactIDs) == 0 {
			return fmt.Errorf("director evidence step %d is invalid", index)
		}
	}
	return nil
}

func ValidateDirectorStoryPlan(plan DirectorStoryPlan, duration FinalFilmDurationRange) error {
	if plan.SchemaVersion != DirectorStoryPlanSchemaVersion || plan.TargetDurationMS < duration.MinMS || plan.TargetDurationMS > duration.MaxMS {
		return errors.New("director story plan schema or target duration is invalid")
	}
	if len(plan.Beats) == 0 || len(plan.Timeline) == 0 || len(plan.SkillVersions) < 5 || len(plan.DecisionLog) == 0 {
		return errors.New("director story plan is incomplete")
	}
	for _, segment := range plan.Timeline {
		if strings.TrimSpace(segment.SegmentID) == "" || segment.Speed < 0.5 || segment.Speed > 12 || segment.OutputDurationMS <= 0 {
			return errors.New("director story timeline contains an invalid segment")
		}
	}
	return nil
}
