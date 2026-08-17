package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const StoryboardConstraintSetSchemaVersion = "demoops.storyboard_constraint_set.v1"

var storyboardSHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type StoryboardConstraintSet struct {
	SchemaVersion        string                            `json:"schema_version"`
	ConstraintSetID      string                            `json:"constraint_set_id"`
	SourcePackageID      string                            `json:"source_package_id"`
	CatalogID            string                            `json:"catalog_id"`
	CatalogDigestSHA256  string                            `json:"catalog_digest_sha256"`
	RequiredStepOrder    []string                          `json:"required_step_order"`
	RequiredStepCoverage map[string]RequiredStepConstraint `json:"required_step_coverage"`
	AllowedArtifactIDs   []string                          `json:"allowed_artifact_ids"`
	PresentationSlots    []PresentationSlotConstraint      `json:"presentation_slots,omitempty"`
	TargetDurationMS     int                               `json:"target_duration_ms"`
	Canvas               RenderCanvas                      `json:"canvas"`
	FactTrackPolicy      FactTrackPolicy                   `json:"fact_track_policy"`
	GeneratedTrackPolicy GeneratedTrackPolicy              `json:"generated_track_policy"`
	RequirementBindings  []RequirementBinding              `json:"requirement_bindings"`
	CreatedAt            time.Time                         `json:"created_at"`
}

type RequiredStepConstraint struct {
	StepID            string            `json:"step_id"`
	Order             int               `json:"order"`
	SourceArtifactIDs []string          `json:"source_artifact_ids"`
	SourceTimeRangeMS *MillisecondRange `json:"source_time_range_ms,omitempty"`
	ExpectedOutcome   string            `json:"expected_outcome,omitempty"`
	ObservedState     string            `json:"observed_state,omitempty"`
}

type PresentationSlotConstraint struct {
	IntentID             string   `json:"intent_id"`
	Purpose              string   `json:"purpose"`
	PreferredDurationSec int      `json:"preferred_duration_sec"`
	AspectRatio          string   `json:"aspect_ratio"`
	ReferenceArtifactIDs []string `json:"reference_artifact_ids,omitempty"`
	Required             bool     `json:"required"`
}

type RenderCanvas struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	FPS    int    `json:"fps"`
	Format string `json:"format"`
}

type FactTrackPolicy struct {
	CapturedMediaOnly          bool `json:"captured_media_only"`
	PreserveRequiredStepOrder  bool `json:"preserve_required_step_order"`
	LockSourceArtifactBindings bool `json:"lock_source_artifact_bindings"`
	LockSourceTimeRanges       bool `json:"lock_source_time_ranges"`
	RequireValidatedOutcome    bool `json:"require_validated_outcome"`
}

type GeneratedTrackPolicy struct {
	Optional                 bool   `json:"optional"`
	PresentationOnly         bool   `json:"presentation_only"`
	MayRepresentBusinessStep bool   `json:"may_represent_business_step"`
	MayReplaceCapturedUI     bool   `json:"may_replace_captured_ui"`
	RequiresExplicitReview   bool   `json:"requires_explicit_review"`
	FailurePolicy            string `json:"failure_policy"`
}

type RequirementBinding struct {
	RequirementID string   `json:"requirement_id"`
	Source        string   `json:"source"`
	StepIDs       []string `json:"step_ids,omitempty"`
	IntentIDs     []string `json:"intent_ids,omitempty"`
}

func ValidateStoryboardConstraintSet(set StoryboardConstraintSet) error {
	if set.SchemaVersion != StoryboardConstraintSetSchemaVersion {
		return errors.New("unsupported storyboard constraint schema_version")
	}
	if strings.TrimSpace(set.ConstraintSetID) == "" || strings.TrimSpace(set.SourcePackageID) == "" || strings.TrimSpace(set.CatalogID) == "" {
		return errors.New("constraint_set_id, source_package_id, and catalog_id are required")
	}
	if !storyboardSHA256Pattern.MatchString(strings.ToLower(strings.TrimSpace(set.CatalogDigestSHA256))) {
		return errors.New("catalog_digest_sha256 must be a lowercase SHA-256 digest")
	}
	if set.CreatedAt.IsZero() || set.TargetDurationMS <= 0 {
		return errors.New("created_at and a positive target_duration_ms are required")
	}
	if set.Canvas.Width <= 0 || set.Canvas.Height <= 0 || set.Canvas.FPS <= 0 || strings.TrimSpace(set.Canvas.Format) == "" {
		return errors.New("render canvas width, height, fps, and format are required")
	}
	if !set.FactTrackPolicy.CapturedMediaOnly || !set.FactTrackPolicy.PreserveRequiredStepOrder || !set.FactTrackPolicy.LockSourceArtifactBindings || !set.FactTrackPolicy.LockSourceTimeRanges || !set.FactTrackPolicy.RequireValidatedOutcome {
		return errors.New("fact_track_policy must lock captured media, required order, artifact bindings, time ranges, and validated outcomes")
	}
	generated := set.GeneratedTrackPolicy
	if !generated.Optional || !generated.PresentationOnly || generated.MayRepresentBusinessStep || generated.MayReplaceCapturedUI || !generated.RequiresExplicitReview || generated.FailurePolicy != PresentationGenerationFailureContinue {
		return errors.New("generated_track_policy violates the optional presentation-only boundary")
	}

	allowedArtifacts, err := uniqueNonBlankValues(set.AllowedArtifactIDs)
	if err != nil || len(allowedArtifacts) == 0 {
		return errors.New("allowed_artifact_ids must contain unique non-empty values")
	}
	allowed := make(map[string]struct{}, len(allowedArtifacts))
	for _, artifactID := range allowedArtifacts {
		allowed[artifactID] = struct{}{}
	}

	stepIDs, err := uniqueNonBlankValues(set.RequiredStepOrder)
	if err != nil {
		return errors.New("required_step_order must contain unique non-empty values")
	}
	if len(stepIDs) != len(set.RequiredStepCoverage) {
		return errors.New("required_step_coverage must exactly match required_step_order")
	}
	for index, stepID := range stepIDs {
		coverage, ok := set.RequiredStepCoverage[stepID]
		if !ok || coverage.StepID != stepID || coverage.Order != index+1 {
			return fmt.Errorf("required step %s has missing or inconsistent coverage", stepID)
		}
		if coverage.SourceTimeRangeMS == nil || coverage.SourceTimeRangeMS[0] < 0 || coverage.SourceTimeRangeMS[1] <= coverage.SourceTimeRangeMS[0] {
			return fmt.Errorf("required step %s must have a positive source time range", stepID)
		}
		artifacts, artifactErr := uniqueNonBlankValues(coverage.SourceArtifactIDs)
		if artifactErr != nil || len(artifacts) == 0 {
			return fmt.Errorf("required step %s must have unique source artifacts", stepID)
		}
		for _, artifactID := range artifacts {
			if _, ok := allowed[artifactID]; !ok {
				return fmt.Errorf("required step %s references disallowed artifact %s", stepID, artifactID)
			}
		}
	}

	intents := make([]PresentationGenerationIntent, 0, len(set.PresentationSlots))
	for _, slot := range set.PresentationSlots {
		intent := PresentationGenerationIntent{
			IntentID: slot.IntentID, Capability: PresentationVideoCandidateCapability, Purpose: slot.Purpose,
			Required: slot.Required, ReferenceAssetRefs: append([]string{}, slot.ReferenceArtifactIDs...),
			RequestedSlot: PresentationGenerationRequestedSlot{PreferredDurationSec: slot.PreferredDurationSec, AspectRatio: slot.AspectRatio},
			ContentPolicy: PresentationGenerationContentPolicy{PresentationOnly: true, RequiresExplicitReview: true},
			FailurePolicy: PresentationGenerationFailureContinue,
		}
		intents = append(intents, intent)
		for _, artifactID := range slot.ReferenceArtifactIDs {
			if _, ok := allowed[artifactID]; !ok {
				return fmt.Errorf("presentation intent %s references disallowed artifact %s", slot.IntentID, artifactID)
			}
		}
	}
	if err := ValidatePresentationGenerationIntents(intents); err != nil {
		return err
	}
	if len(set.RequirementBindings) < len(stepIDs) {
		return errors.New("requirement_bindings must trace every required step")
	}
	return nil
}

func uniqueNonBlankValues(values []string) ([]string, error) {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New("blank value")
		}
		if _, exists := seen[value]; exists {
			return nil, errors.New("duplicate value")
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}
