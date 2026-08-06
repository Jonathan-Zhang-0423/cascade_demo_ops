package media

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	GeneratedShotCandidateSchemaVersion  = "demoops.generated_shot_candidate.v1"
	GeneratedShotCandidateReadyForReview = "normalized_candidate_ready_for_review"
	GeneratedShotNormalizationProfile    = "editor_mp4_h264_yuv420p_1920x1080_cfr30_v1"
	GeneratedShotNormalizedWidth         = 1920
	GeneratedShotNormalizedHeight        = 1080
	GeneratedShotNormalizedFPS           = 30
)

var generatedShotSHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// GeneratedShotCandidate is the provider-neutral handoff after a provider
// output has been downloaded and normalized. It is review input only: the
// contract intentionally has no editor placement, source_step_id, approval,
// or auto-apply authority.
type GeneratedShotCandidate struct {
	SchemaVersion          string                         `json:"schema_version"`
	CandidateID            string                         `json:"candidate_id"`
	IntentID               string                         `json:"intent_id"`
	Provider               string                         `json:"provider"`
	ProviderTaskID         string                         `json:"provider_task_id"`
	Status                 string                         `json:"status"`
	FailurePolicy          string                         `json:"failure_policy"`
	NonAuthoritative       bool                           `json:"non_authoritative"`
	PresentationOnly       bool                           `json:"presentation_only"`
	RequiresExplicitReview bool                           `json:"requires_explicit_review"`
	ApprovedForDemo        bool                           `json:"approved_for_demo"`
	IncludeInDemo          bool                           `json:"include_in_demo"`
	OriginalArtifact       GeneratedShotCandidateArtifact `json:"original_artifact"`
	NormalizedArtifact     GeneratedShotCandidateArtifact `json:"normalized_artifact"`
}

type GeneratedShotCandidateArtifact struct {
	Role                 string                  `json:"role"`
	Path                 string                  `json:"path"`
	MimeType             string                  `json:"mime_type"`
	SHA256               string                  `json:"sha256"`
	SizeBytes            int64                   `json:"size_bytes"`
	Probe                GeneratedShotMediaProbe `json:"probe"`
	NormalizationStatus  string                  `json:"normalization_status,omitempty"`
	NormalizationProfile string                  `json:"normalization_profile,omitempty"`
}

type GeneratedShotMediaProbe struct {
	Format      string  `json:"format,omitempty"`
	VideoCodec  string  `json:"video_codec,omitempty"`
	PixelFormat string  `json:"pixel_format,omitempty"`
	Width       int     `json:"width,omitempty"`
	Height      int     `json:"height,omitempty"`
	FPS         float64 `json:"fps,omitempty"`
	CFR         bool    `json:"cfr,omitempty"`
	DurationSec float64 `json:"duration_sec,omitempty"`
}

type GeneratedShotCandidateValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *GeneratedShotCandidateValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Field + ": " + e.Message
}

// ValidateGeneratedShotCandidate enforces the common editor-side media gate.
// Passing this gate means ready for review, never approved or timeline-ready.
func ValidateGeneratedShotCandidate(candidate GeneratedShotCandidate) error {
	fail := func(field string, code string, message string) error {
		return &GeneratedShotCandidateValidationError{Field: field, Code: code, Message: message}
	}
	if candidate.SchemaVersion != GeneratedShotCandidateSchemaVersion {
		return fail("schema_version", "generated_candidate_schema_unsupported", "candidate schema version is unsupported")
	}
	if strings.TrimSpace(candidate.CandidateID) == "" || strings.TrimSpace(candidate.IntentID) == "" {
		return fail("candidate_id", "generated_candidate_identity_missing", "candidate_id and intent_id are required")
	}
	if candidate.Provider != GeneratedShotProviderSeedance20 && candidate.Provider != GeneratedShotProviderMiniMaxH3 {
		return fail("provider", "generated_candidate_provider_unsupported", "provider is unsupported by the current internal contract")
	}
	if strings.TrimSpace(candidate.ProviderTaskID) == "" {
		return fail("provider_task_id", "generated_candidate_task_missing", "provider_task_id is required for audit")
	}
	if candidate.Status != GeneratedShotCandidateReadyForReview {
		return fail("status", "generated_candidate_not_normalized", "candidate must be normalized and ready for review")
	}
	if candidate.FailurePolicy != GeneratedShotFailureContinue {
		return fail("failure_policy", "generated_candidate_failure_policy_unsafe", "candidate failure must not block deterministic delivery")
	}
	if !candidate.NonAuthoritative || !candidate.PresentationOnly || !candidate.RequiresExplicitReview {
		return fail("content_policy", "generated_candidate_authority_unsafe", "candidate must be non-authoritative, presentation-only, and explicitly reviewed")
	}
	if candidate.ApprovedForDemo || candidate.IncludeInDemo {
		return fail("approved_for_demo", "generated_candidate_approval_not_allowed", "normalization cannot approve or include a candidate")
	}
	if err := validateGeneratedShotCandidateArtifact(candidate.OriginalArtifact, "original_artifact", false); err != nil {
		return err
	}
	if err := validateGeneratedShotCandidateArtifact(candidate.NormalizedArtifact, "normalized_artifact", true); err != nil {
		return err
	}
	if filepath.Clean(candidate.OriginalArtifact.Path) == filepath.Clean(candidate.NormalizedArtifact.Path) {
		return fail("normalized_artifact.path", "generated_candidate_artifacts_not_distinct", "original and normalized paths must differ")
	}
	if candidate.OriginalArtifact.SHA256 == candidate.NormalizedArtifact.SHA256 {
		return fail("normalized_artifact.sha256", "generated_candidate_artifacts_not_distinct", "original and normalized SHA-256 values must differ")
	}
	return nil
}

func validateGeneratedShotCandidateArtifact(artifact GeneratedShotCandidateArtifact, field string, normalized bool) error {
	fail := func(suffix string, code string, message string) error {
		return &GeneratedShotCandidateValidationError{Field: field + suffix, Code: code, Message: message}
	}
	wantedRole := "original"
	if normalized {
		wantedRole = "normalized"
	}
	if artifact.Role != wantedRole {
		return fail(".role", "generated_candidate_artifact_role_invalid", "artifact role must be "+wantedRole)
	}
	path := strings.TrimSpace(artifact.Path)
	if path == "" || !filepath.IsAbs(path) || strings.Contains(path, "://") {
		return fail(".path", "generated_candidate_artifact_not_local", "artifact must use an absolute local path")
	}
	if !generatedShotSHA256Pattern.MatchString(strings.ToLower(strings.TrimSpace(artifact.SHA256))) {
		return fail(".sha256", "generated_candidate_integrity_invalid", "artifact must carry a 64-character SHA-256 digest")
	}
	if artifact.SizeBytes <= 0 {
		return fail(".size_bytes", "generated_candidate_integrity_invalid", "artifact size must be positive")
	}
	if !normalized {
		return nil
	}
	if strings.ToLower(strings.TrimSpace(artifact.MimeType)) != "video/mp4" {
		return fail(".mime_type", "generated_candidate_media_profile_invalid", "normalized artifact MIME must be video/mp4")
	}
	if artifact.NormalizationStatus != "ok" || artifact.NormalizationProfile != GeneratedShotNormalizationProfile {
		return fail(".normalization_status", "generated_candidate_media_profile_invalid", "normalized artifact must carry the locked editor normalization profile")
	}
	if err := validateGeneratedShotMediaProbe(artifact.Probe); err != nil {
		return fail(".probe", "generated_candidate_media_profile_invalid", err.Error())
	}
	return nil
}

func validateGeneratedShotMediaProbe(probe GeneratedShotMediaProbe) error {
	if !strings.Contains(strings.ToLower(probe.Format), "mp4") {
		return fmt.Errorf("normalized media container must be MP4, got %q", probe.Format)
	}
	if !strings.EqualFold(strings.TrimSpace(probe.VideoCodec), "h264") {
		return fmt.Errorf("normalized media codec must be H.264, got %q", probe.VideoCodec)
	}
	if !strings.EqualFold(strings.TrimSpace(probe.PixelFormat), "yuv420p") {
		return fmt.Errorf("normalized media pixel format must be yuv420p, got %q", probe.PixelFormat)
	}
	if probe.Width != GeneratedShotNormalizedWidth || probe.Height != GeneratedShotNormalizedHeight {
		return fmt.Errorf("normalized media dimensions must be %dx%d, got %dx%d", GeneratedShotNormalizedWidth, GeneratedShotNormalizedHeight, probe.Width, probe.Height)
	}
	if math.Abs(probe.FPS-GeneratedShotNormalizedFPS) > 0.01 || !probe.CFR {
		return fmt.Errorf("normalized media must be CFR %.0ffps, got %.3f CFR=%t", float64(GeneratedShotNormalizedFPS), probe.FPS, probe.CFR)
	}
	if probe.DurationSec <= 0 {
		return errors.New("normalized media duration must be positive")
	}
	return nil
}

// NewGeneratedShotCandidateFromMiniMaxH3 converts only a successfully
// normalized H3 result. It does not approve, publish, or register the result.
func NewGeneratedShotCandidateFromMiniMaxH3(candidateID string, intentID string, result MiniMaxH3PipelineResult) (GeneratedShotCandidate, error) {
	if result.Status != GeneratedShotCandidateReadyForReview || result.ErrorClass != "" || result.OriginalArtifact == nil || result.Normalized == nil {
		return GeneratedShotCandidate{}, &GeneratedShotCandidateValidationError{
			Field: "pipeline_result", Code: "generated_candidate_pipeline_incomplete",
			Message: "H3 pipeline must finish normalization without an error before candidate conversion",
		}
	}
	if !result.OriginalArtifact.Presentation || result.OriginalArtifact.Authoritative || !result.Normalized.Presentation || result.Normalized.Authoritative {
		return GeneratedShotCandidate{}, &GeneratedShotCandidateValidationError{
			Field: "pipeline_result", Code: "generated_candidate_pipeline_authority_unsafe",
			Message: "H3 pipeline artifacts must be presentation-only and non-authoritative",
		}
	}
	if strings.TrimSpace(result.TaskID) == "" || result.OriginalArtifact.SourceTaskID != result.TaskID || result.Normalized.SourceTaskID != result.TaskID {
		return GeneratedShotCandidate{}, &GeneratedShotCandidateValidationError{
			Field: "provider_task_id", Code: "generated_candidate_pipeline_audit_mismatch",
			Message: "pipeline and artifact task IDs must be present and identical",
		}
	}
	candidate := GeneratedShotCandidate{
		SchemaVersion: GeneratedShotCandidateSchemaVersion,
		CandidateID:   candidateID, IntentID: intentID, Provider: GeneratedShotProviderMiniMaxH3,
		ProviderTaskID: result.TaskID, Status: GeneratedShotCandidateReadyForReview,
		FailurePolicy: GeneratedShotFailureContinue, NonAuthoritative: true, PresentationOnly: true,
		RequiresExplicitReview: true, ApprovedForDemo: false, IncludeInDemo: false,
		OriginalArtifact:   generatedShotArtifactFromMiniMaxH3(*result.OriginalArtifact, false),
		NormalizedArtifact: generatedShotArtifactFromMiniMaxH3(*result.Normalized, true),
	}
	if err := ValidateGeneratedShotCandidate(candidate); err != nil {
		return GeneratedShotCandidate{}, err
	}
	return candidate, nil
}

func generatedShotArtifactFromMiniMaxH3(artifact MiniMaxH3Artifact, normalized bool) GeneratedShotCandidateArtifact {
	converted := GeneratedShotCandidateArtifact{
		Role: artifact.Role, Path: artifact.Path, MimeType: artifact.MimeType,
		SHA256: strings.ToLower(strings.TrimSpace(artifact.SHA256)), SizeBytes: artifact.SizeBytes,
	}
	if artifact.Probe != nil {
		converted.Probe = GeneratedShotMediaProbe{
			Format: artifact.Probe.Format, VideoCodec: artifact.Probe.VideoCodec, PixelFormat: artifact.Probe.PixelFormat,
			Width: artifact.Probe.Width, Height: artifact.Probe.Height, FPS: artifact.Probe.FPS,
			CFR: artifact.Probe.CFR, DurationSec: artifact.Probe.DurationSec,
		}
	}
	if normalized {
		converted.NormalizationStatus = "ok"
		converted.NormalizationProfile = GeneratedShotNormalizationProfile
	}
	return converted
}

func asGeneratedShotCandidateValidationError(err error) *GeneratedShotCandidateValidationError {
	var target *GeneratedShotCandidateValidationError
	if errors.As(err, &target) {
		return target
	}
	return nil
}
