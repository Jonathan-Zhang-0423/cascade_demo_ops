package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const FinalFilmDirectorPlanSchemaVersion = "demoops.final_film_director_plan.v1"

// FinalFilmDirectorPlan is the audited boundary between Director and provider
// execution. It describes presentation-only shots, but deliberately cannot
// select a video provider, model, endpoint, or bind a factual workflow step.
type FinalFilmDirectorPlan struct {
	SchemaVersion   string                   `json:"schema_version"`
	PlanID          string                   `json:"plan_id"`
	JobID           string                   `json:"job_id"`
	ConstraintSetID string                   `json:"constraint_set_id"`
	DirectorRunID   string                   `json:"director_run_id"`
	GeneratedAt     time.Time                `json:"generated_at"`
	Specs           []FinalFilmGeneratedSpec `json:"specs"`
}

type FinalFilmGeneratedSpec struct {
	SpecID               string                              `json:"spec_id"`
	IntentID             string                              `json:"intent_id"`
	Purpose              string                              `json:"purpose"`
	Prompt               string                              `json:"prompt"`
	PromptSHA256         string                              `json:"prompt_sha256"`
	DurationSec          int                                 `json:"duration_sec"`
	AspectRatio          string                              `json:"aspect_ratio"`
	ReferenceArtifactIDs []string                            `json:"reference_artifact_ids,omitempty"`
	ContentPolicy        FinalFilmGeneratedSpecContentPolicy `json:"content_policy"`
	FailurePolicy        string                              `json:"failure_policy"`
	// Decode-only prohibited fields keep unsafe Director output observable.
	SourceStepID   string `json:"source_step_id,omitempty"`
	BusinessStepID string `json:"business_step_id,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	APIEndpoint    string `json:"api_endpoint,omitempty"`
}

type FinalFilmGeneratedSpecContentPolicy struct {
	PresentationOnly       bool `json:"presentation_only"`
	NoCapturedUIRecreation bool `json:"no_captured_ui_recreation"`
	NoBusinessFactClaims   bool `json:"no_business_fact_claims"`
	NoUnverifiedText       bool `json:"no_unverified_text"`
	RequiresExplicitReview bool `json:"requires_explicit_review"`
}

func FinalFilmPromptSHA256(prompt string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(prompt)))
	return hex.EncodeToString(digest[:])
}

func ValidateFinalFilmDirectorPlan(plan FinalFilmDirectorPlan, jobID string, constraints StoryboardConstraintSet, intents []PresentationGenerationIntent) error {
	if plan.SchemaVersion != FinalFilmDirectorPlanSchemaVersion {
		return errors.New("unsupported final film director plan schema")
	}
	if strings.TrimSpace(plan.PlanID) == "" || strings.TrimSpace(plan.DirectorRunID) == "" || plan.GeneratedAt.IsZero() {
		return errors.New("director plan identity, run identity, and generated_at are required")
	}
	if plan.JobID != jobID || plan.ConstraintSetID != constraints.ConstraintSetID {
		return errors.New("director plan must bind the current job and immutable constraint set")
	}
	if len(plan.Specs) != len(intents) {
		return errors.New("director plan must contain exactly one spec for every presentation intent")
	}
	intentByID := make(map[string]PresentationGenerationIntent, len(intents))
	for _, intent := range intents {
		intentByID[intent.IntentID] = intent
	}
	allowedArtifacts := make(map[string]bool, len(constraints.AllowedArtifactIDs))
	for _, artifactID := range constraints.AllowedArtifactIDs {
		allowedArtifacts[artifactID] = true
	}
	seenSpecs := map[string]bool{}
	seenIntents := map[string]bool{}
	for index, spec := range plan.Specs {
		prefix := fmt.Sprintf("specs[%d]", index)
		if strings.TrimSpace(spec.SpecID) == "" || seenSpecs[spec.SpecID] {
			return fmt.Errorf("%s.spec_id is blank or duplicated", prefix)
		}
		seenSpecs[spec.SpecID] = true
		intent, ok := intentByID[spec.IntentID]
		if !ok || seenIntents[spec.IntentID] {
			return fmt.Errorf("%s.intent_id is unknown or duplicated", prefix)
		}
		seenIntents[spec.IntentID] = true
		prompt := strings.TrimSpace(spec.Prompt)
		if prompt == "" || len([]rune(prompt)) > 4000 || spec.PromptSHA256 != FinalFilmPromptSHA256(prompt) {
			return fmt.Errorf("%s.prompt is blank, too long, or its SHA-256 binding is invalid", prefix)
		}
		if spec.Purpose != intent.Purpose || spec.DurationSec != intent.RequestedSlot.PreferredDurationSec || spec.AspectRatio != intent.RequestedSlot.AspectRatio {
			return fmt.Errorf("%s changes the approved purpose, duration, or aspect ratio", prefix)
		}
		if !sameStringSet(spec.ReferenceArtifactIDs, intent.ReferenceAssetRefs) {
			return fmt.Errorf("%s changes the approved reference artifact set", prefix)
		}
		for _, artifactID := range spec.ReferenceArtifactIDs {
			if !allowedArtifacts[artifactID] {
				return fmt.Errorf("%s references disallowed artifact %s", prefix, artifactID)
			}
		}
		policy := spec.ContentPolicy
		if !policy.PresentationOnly || !policy.NoCapturedUIRecreation || !policy.NoBusinessFactClaims || !policy.NoUnverifiedText || !policy.RequiresExplicitReview {
			return fmt.Errorf("%s violates the generated presentation safety policy", prefix)
		}
		if spec.FailurePolicy != PresentationGenerationFailureContinue {
			return fmt.Errorf("%s.failure_policy must continue without generated media", prefix)
		}
		if strings.TrimSpace(spec.SourceStepID) != "" || strings.TrimSpace(spec.BusinessStepID) != "" || strings.TrimSpace(spec.Provider) != "" || strings.TrimSpace(spec.Model) != "" || strings.TrimSpace(spec.APIEndpoint) != "" {
			return fmt.Errorf("%s contains prohibited fact bindings or provider-owned parameters", prefix)
		}
	}
	return nil
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[strings.TrimSpace(value)]++
	}
	for _, value := range right {
		value = strings.TrimSpace(value)
		if counts[value] == 0 {
			return false
		}
		counts[value]--
	}
	return true
}
