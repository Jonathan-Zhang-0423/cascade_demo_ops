package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const CreativeDirectorPackageSchemaVersion = "demoops.creative_director_package.v1"

// CreativeDirectorPackage is the artifact boundary between the creative
// director and FinalFilm. It carries a bundle binding, not the bundle's
// mutable in-memory object.
type CreativeDirectorPackage struct {
	SchemaVersion string               `json:"schema_version"`
	PackageID     string               `json:"package_id"`
	BundleID      string               `json:"bundle_id"`
	BundleDigest  string               `json:"bundle_digest"`
	GeneratedAt   time.Time            `json:"generated_at"`
	ShotIntents   []CreativeShotIntent `json:"shot_intents"`
	StyleProfile  CreativeStyleProfile `json:"style_profile"`
}

type CreativeStyleProfile struct {
	Name            string `json:"name"`
	ColorGrade      string `json:"color_grade"`
	Pacing          string `json:"pacing"`
	TransitionStyle string `json:"transition_style"`
	AudioStrategy   string `json:"audio_strategy"`
}

func ValidateCreativeDirectorPackage(pkg CreativeDirectorPackage) error {
	if pkg.SchemaVersion != CreativeDirectorPackageSchemaVersion {
		return errors.New("unsupported creative director package schema")
	}
	if strings.TrimSpace(pkg.PackageID) == "" || strings.TrimSpace(pkg.BundleID) == "" || pkg.GeneratedAt.IsZero() {
		return errors.New("director package identity, bundle binding, and generated_at are required")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(pkg.BundleDigest)), "sha256:") {
		return errors.New("director package bundle_digest must be a sha256: digest")
	}
	if len(pkg.ShotIntents) == 0 {
		return errors.New("director package requires at least one shot intent")
	}
	seen := map[string]struct{}{}
	for i, intent := range pkg.ShotIntents {
		if err := ValidateCreativeShotIntent(intent); err != nil {
			return fmt.Errorf("shot_intents[%d]: %w", i, err)
		}
		if _, exists := seen[intent.IntentID]; exists {
			return fmt.Errorf("shot_intents[%d].intent_id is duplicated", i)
		}
		seen[intent.IntentID] = struct{}{}
	}
	if strings.TrimSpace(pkg.StyleProfile.Name) == "" || strings.TrimSpace(pkg.StyleProfile.Pacing) == "" {
		return errors.New("director package style profile name and pacing are required")
	}
	return nil
}
