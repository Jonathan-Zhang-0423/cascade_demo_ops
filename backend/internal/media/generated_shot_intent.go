package media

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	GeneratedShotFailureContinue = "continue_without_generated_candidate"

	GeneratedShotPurposeIntro           = "intro"
	GeneratedShotPurposeOutro           = "outro"
	GeneratedShotPurposeSectionDivider  = "section_divider"
	GeneratedShotPurposeAbstractBRoll   = "abstract_b_roll"
	GeneratedShotPurposeBrandAtmosphere = "brand_atmosphere"
	GeneratedShotPurposeTransition      = "transition"

	GeneratedShotReferenceGeneral    = "reference"
	GeneratedShotReferenceFirstFrame = "first_frame"
	GeneratedShotReferenceLastFrame  = "last_frame"
)

// GeneratedShotIntent is the Server-internal, provider-neutral representation
// of an approved presentation shot. It contains no provider or model ID. App
// artifact references must be resolved and revalidated by Server before an
// intent reaches a provider-specific compiler.
type GeneratedShotIntent struct {
	IntentID      string                     `json:"intent_id"`
	Purpose       string                     `json:"purpose"`
	Prompt        string                     `json:"prompt"`
	Required      bool                       `json:"required"`
	DurationSec   int                        `json:"duration_sec"`
	AspectRatio   string                     `json:"aspect_ratio"`
	References    []GeneratedShotReference   `json:"references,omitempty"`
	ContentPolicy GeneratedShotContentPolicy `json:"content_policy"`
	FailurePolicy string                     `json:"failure_policy"`
}

type GeneratedShotReference struct {
	ArtifactID string `json:"artifact_id"`
	URI        string `json:"uri"`
	MimeType   string `json:"mime_type"`
	Usage      string `json:"usage"`
}

type GeneratedShotContentPolicy struct {
	PresentationOnly         bool `json:"presentation_only"`
	MayRepresentBusinessStep bool `json:"may_represent_business_step"`
	MayReplaceCapturedUI     bool `json:"may_replace_captured_ui"`
	RequiresExplicitReview   bool `json:"requires_explicit_review"`
}

type GeneratedShotIntentValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *GeneratedShotIntentValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Field + ": " + e.Message
}

func ValidateGeneratedShotIntent(intent GeneratedShotIntent) error {
	fail := func(field string, code string, message string) error {
		return &GeneratedShotIntentValidationError{Field: field, Code: code, Message: message}
	}
	if strings.TrimSpace(intent.IntentID) == "" {
		return fail("intent_id", "generated_shot_intent_invalid", "intent_id is required")
	}
	if !generatedShotPurposeAllowed(intent.Purpose) {
		return fail("purpose", "generated_shot_purpose_unsupported", "purpose is not an approved presentation-only purpose")
	}
	if strings.TrimSpace(intent.Prompt) == "" {
		return fail("prompt", "generated_shot_intent_invalid", "prompt is required")
	}
	if intent.Required {
		return fail("required", "generated_shot_must_be_optional", "generated model output cannot be required for business delivery")
	}
	if intent.DurationSec < 4 || intent.DurationSec > 15 {
		return fail("duration_sec", "generated_shot_duration_out_of_range", "duration_sec must be an integer from 4 to 15")
	}
	if !generatedShotAspectRatioAllowed(intent.AspectRatio) {
		return fail("aspect_ratio", "generated_shot_ratio_unsupported", "aspect_ratio is unsupported")
	}
	if !intent.ContentPolicy.PresentationOnly || intent.ContentPolicy.MayRepresentBusinessStep || intent.ContentPolicy.MayReplaceCapturedUI || !intent.ContentPolicy.RequiresExplicitReview {
		return fail("content_policy", "generated_shot_content_policy_unsafe", "generated shots must be presentation-only, non-factual, unable to replace captured UI, and explicitly reviewed")
	}
	if intent.FailurePolicy != GeneratedShotFailureContinue {
		return fail("failure_policy", "generated_shot_failure_policy_unsafe", "failure_policy must continue without the generated candidate")
	}
	if len(intent.References) > 4 {
		return fail("references", "generated_shot_reference_limit_exceeded", "the current Server common profile accepts at most four references")
	}
	seen := map[string]struct{}{}
	for index, ref := range intent.References {
		field := fmt.Sprintf("references[%d]", index)
		artifactID := strings.TrimSpace(ref.ArtifactID)
		if artifactID == "" {
			return fail(field+".artifact_id", "generated_shot_reference_invalid", "artifact_id is required")
		}
		if _, exists := seen[artifactID]; exists {
			return fail(field+".artifact_id", "generated_shot_reference_duplicate", "artifact_id must be unique")
		}
		seen[artifactID] = struct{}{}
		if !generatedShotReferenceURIAllowed(ref.URI) {
			return fail(field+".uri", "generated_shot_reference_uri_invalid", "reference URI must be HTTPS or an asset:// provider reference")
		}
		if !generatedShotReferenceMIMEAllowed(ref.MimeType) {
			return fail(field+".mime_type", "generated_shot_reference_mime_unsupported", "common profile accepts PNG, JPEG, MP4, or MOV references")
		}
		switch ref.Usage {
		case GeneratedShotReferenceGeneral, GeneratedShotReferenceFirstFrame, GeneratedShotReferenceLastFrame:
		default:
			return fail(field+".usage", "generated_shot_reference_usage_unsupported", "reference usage is unsupported")
		}
		if (ref.Usage == GeneratedShotReferenceFirstFrame || ref.Usage == GeneratedShotReferenceLastFrame) && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ref.MimeType)), "image/") {
			return fail(field+".usage", "generated_shot_frame_must_be_image", "first_frame and last_frame references must be images")
		}
	}
	return nil
}

func generatedShotPurposeAllowed(purpose string) bool {
	switch strings.TrimSpace(purpose) {
	case GeneratedShotPurposeIntro, GeneratedShotPurposeOutro, GeneratedShotPurposeSectionDivider, GeneratedShotPurposeAbstractBRoll, GeneratedShotPurposeBrandAtmosphere, GeneratedShotPurposeTransition:
		return true
	default:
		return false
	}
}

func generatedShotAspectRatioAllowed(ratio string) bool {
	switch strings.TrimSpace(ratio) {
	case "21:9", "16:9", "4:3", "1:1", "3:4", "9:16", "adaptive":
		return true
	default:
		return false
	}
}

func generatedShotReferenceURIAllowed(raw string) bool {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "asset://") && len(strings.TrimPrefix(raw, "asset://")) > 0 {
		return true
	}
	parsed, err := url.Parse(raw)
	return err == nil && strings.EqualFold(parsed.Scheme, "https") && strings.TrimSpace(parsed.Host) != ""
}

func generatedShotReferenceMIMEAllowed(mimeType string) bool {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png", "image/jpeg", "video/mp4", "video/quicktime":
		return true
	default:
		return false
	}
}

func asGeneratedShotValidationError(err error) *GeneratedShotIntentValidationError {
	var target *GeneratedShotIntentValidationError
	if errors.As(err, &target) {
		return target
	}
	return nil
}
