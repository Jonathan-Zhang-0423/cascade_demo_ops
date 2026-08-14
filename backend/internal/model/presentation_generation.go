package model

import (
	"errors"
	"fmt"
	"strings"
)

const (
	PresentationVideoCandidateCapability  = "presentation_video_candidate"
	PresentationGenerationFailureContinue = "continue_without_generated_candidate"
)

var allowedPresentationGenerationPurposes = map[string]bool{
	"intro": true, "outro": true, "section_divider": true,
	"abstract_broll": true, "brand_atmosphere": true,
}

func ValidatePresentationGenerationIntents(intents []PresentationGenerationIntent) error {
	seen := map[string]bool{}
	for index, intent := range intents {
		prefix := fmt.Sprintf("presentation_generation_intents[%d]", index)
		id := strings.TrimSpace(intent.IntentID)
		if id == "" {
			return fmt.Errorf("%s.intent_id is required", prefix)
		}
		if seen[id] {
			return fmt.Errorf("%s.intent_id is duplicated", prefix)
		}
		seen[id] = true
		if intent.Capability != PresentationVideoCandidateCapability {
			return fmt.Errorf("%s.capability must be %s", prefix, PresentationVideoCandidateCapability)
		}
		if !allowedPresentationGenerationPurposes[intent.Purpose] {
			return fmt.Errorf("%s.purpose is not an allowed presentation purpose", prefix)
		}
		if intent.Required {
			return fmt.Errorf("%s.required must be false", prefix)
		}
		if len(intent.ReferenceAssetRefs) > 4 {
			return fmt.Errorf("%s.reference_asset_refs exceeds 4", prefix)
		}
		if hasBlankOrDuplicateString(intent.ReferenceAssetRefs) {
			return fmt.Errorf("%s.reference_asset_refs contains a blank or duplicate ref", prefix)
		}
		if intent.RequestedSlot.PreferredDurationSec < 4 || intent.RequestedSlot.PreferredDurationSec > 15 {
			return fmt.Errorf("%s.requested_slot.preferred_duration_sec must be between 4 and 15", prefix)
		}
		if intent.RequestedSlot.AspectRatio != "16:9" {
			return fmt.Errorf("%s.requested_slot.aspect_ratio must be 16:9", prefix)
		}
		policy := intent.ContentPolicy
		if !policy.PresentationOnly || policy.MayRepresentBusinessStep || policy.MayReplaceCapturedUI || !policy.RequiresExplicitReview {
			return fmt.Errorf("%s.content_policy violates the presentation-only boundary", prefix)
		}
		if intent.FailurePolicy != PresentationGenerationFailureContinue {
			return fmt.Errorf("%s.failure_policy must be %s", prefix, PresentationGenerationFailureContinue)
		}
		if strings.TrimSpace(intent.SourceStepID) != "" || strings.TrimSpace(intent.BusinessStepID) != "" {
			return fmt.Errorf("%s must not bind a business or source step", prefix)
		}
		if strings.TrimSpace(intent.Provider) != "" || strings.TrimSpace(intent.Model) != "" || strings.TrimSpace(intent.ModelID) != "" || strings.TrimSpace(intent.APIEndpoint) != "" || intent.GenerationFPS != 0 || intent.GenerateAudio != nil {
			return fmt.Errorf("%s contains server-owned provider or model parameters", prefix)
		}
	}
	return nil
}

func hasBlankOrDuplicateString(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func PresentationGenerationIntentDefaults(intentID, purpose string, referenceAssetRefs []string) (PresentationGenerationIntent, error) {
	intent := PresentationGenerationIntent{
		IntentID: intentID, Capability: PresentationVideoCandidateCapability, Purpose: purpose,
		ReferenceAssetRefs: append([]string{}, referenceAssetRefs...),
		RequestedSlot:      PresentationGenerationRequestedSlot{PreferredDurationSec: 5, AspectRatio: "16:9"},
		ContentPolicy:      PresentationGenerationContentPolicy{PresentationOnly: true, RequiresExplicitReview: true},
		FailurePolicy:      PresentationGenerationFailureContinue,
	}
	if err := ValidatePresentationGenerationIntents([]PresentationGenerationIntent{intent}); err != nil {
		return PresentationGenerationIntent{}, err
	}
	return intent, nil
}

var ErrPresentationCandidateExplicitReviewRequired = errors.New("presentation candidate requires explicit user review")
