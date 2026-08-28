package model

import (
	"errors"
	"fmt"
	"strings"
)

const CreativeShotIntentSchemaVersion = "demoops.creative_shot_intent.v1"

type CreativeShotRole string

const (
	CreativeShotRoleEstablishing   CreativeShotRole = "establishing_shot"
	CreativeShotRoleActionFocus    CreativeShotRole = "action_focus"
	CreativeShotRoleResultReveal   CreativeShotRole = "result_reveal"
	CreativeShotRoleChapterOpen    CreativeShotRole = "chapter_open"
	CreativeShotRoleHeroTransition CreativeShotRole = "hero_transition"
	CreativeShotRoleEvidenceHold   CreativeShotRole = "evidence_hold"
	CreativeShotRoleBrandOutro     CreativeShotRole = "brand_outro"
)

type CreativeShotIntent struct {
	SchemaVersion        string           `json:"schema_version"`
	IntentID             string           `json:"intent_id"`
	Role                 CreativeShotRole `json:"role"`
	Purpose              string           `json:"purpose"`
	Prompt               string           `json:"prompt"`
	DurationSec          int              `json:"duration_sec"`
	AspectRatio          string           `json:"aspect_ratio"`
	ReferenceArtifactIDs []string         `json:"reference_artifact_ids,omitempty"`
	AllowedProviders     []string         `json:"allowed_providers"`
	FallbackPolicy       string           `json:"fallback_policy"`
	PresentationOnly     bool             `json:"presentation_only"`
	MayReplaceFacts      bool             `json:"may_replace_facts"`
}

const CreativeShotFallbackContinue = "continue_without_generated_candidate"

func ValidateCreativeShotIntent(intent CreativeShotIntent) error {
	if intent.SchemaVersion != CreativeShotIntentSchemaVersion {
		return errors.New("unsupported creative shot intent schema")
	}
	if strings.TrimSpace(intent.IntentID) == "" || strings.TrimSpace(intent.Prompt) == "" {
		return errors.New("intent_id and prompt are required")
	}
	if !creativeShotRoleAllowed(intent.Role) {
		return fmt.Errorf("unsupported creative shot role %q", intent.Role)
	}
	if intent.DurationSec < 4 || intent.DurationSec > 15 {
		return errors.New("duration_sec must be between 4 and 15")
	}
	if strings.TrimSpace(intent.AspectRatio) == "" {
		return errors.New("aspect_ratio is required")
	}
	if len(intent.AllowedProviders) == 0 {
		return errors.New("at least one allowed provider is required")
	}
	if intent.FallbackPolicy != CreativeShotFallbackContinue {
		return errors.New("fallback_policy must continue without generated candidate")
	}
	if !intent.PresentationOnly || intent.MayReplaceFacts {
		return errors.New("creative shots must be presentation-only and may not replace facts")
	}
	seen := map[string]struct{}{}
	for _, provider := range intent.AllowedProviders {
		provider = strings.TrimSpace(provider)
		if provider == "" {
			return errors.New("allowed_providers contains a blank provider")
		}
		if _, exists := seen[provider]; exists {
			return fmt.Errorf("allowed_providers contains duplicate %q", provider)
		}
		seen[provider] = struct{}{}
	}
	return nil
}

func creativeShotRoleAllowed(role CreativeShotRole) bool {
	switch role {
	case CreativeShotRoleEstablishing, CreativeShotRoleActionFocus, CreativeShotRoleResultReveal,
		CreativeShotRoleChapterOpen, CreativeShotRoleHeroTransition, CreativeShotRoleEvidenceHold,
		CreativeShotRoleBrandOutro:
		return true
	default:
		return false
	}
}
