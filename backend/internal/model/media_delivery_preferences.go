package model

import (
	"fmt"
	"strings"
)

const (
	MediaDeliveryPreferencesSchemaVersion = "demoops.media_delivery_preferences.v1"

	MediaNarrationModeAutoByProductStyle = "auto_by_product_style"
	MediaNarrationModeDisabled           = "disabled"
	MediaNarrationModeCustom             = "custom"

	MediaTOSRetentionModeStandard30Days = "standard_30d"
	MediaTOSRetentionModeCustom         = "custom"

	MediaCandidateProviderSeedance                  = "seedance"
	MediaCandidateAdoptionQualifiedPresentationAuto = "qualified_presentation_auto"

	MediaOutputProfileMaster2K     = "final_master_2k"
	MediaOutputProfileDelivery1080 = "final_delivery_1080p"
)

// MediaDeliveryPreferences is a client-visible delivery policy. It never
// contains provider credentials or a provider endpoint.
type MediaDeliveryPreferences struct {
	SchemaVersion            string                      `json:"schema_version"`
	Narration                MediaNarrationPreference    `json:"narration"`
	TOSRetention             MediaTOSRetentionPreference `json:"tos_retention"`
	OutputProfiles           []MediaOutputProfile        `json:"output_profiles"`
	DefaultCandidateProvider string                      `json:"default_candidate_provider"`
	CandidateAdoptionPolicy  string                      `json:"candidate_adoption_policy"`
}

type MediaNarrationPreference struct {
	Mode            string `json:"mode"`
	ClientSpecified bool   `json:"client_specified"`
}

type MediaTOSRetentionPreference struct {
	Mode                         string `json:"mode"`
	RetentionDays                int    `json:"retention_days"`
	Scope                        string `json:"scope"`
	ClientDisclosureAcknowledged bool   `json:"client_disclosure_acknowledged"`
}

type MediaOutputProfile struct {
	ID     string `json:"id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Format string `json:"format"`
}

func DefaultMediaDeliveryPreferences() MediaDeliveryPreferences {
	return MediaDeliveryPreferences{
		SchemaVersion: MediaDeliveryPreferencesSchemaVersion,
		Narration:     MediaNarrationPreference{Mode: MediaNarrationModeAutoByProductStyle},
		TOSRetention: MediaTOSRetentionPreference{
			Mode: MediaTOSRetentionModeStandard30Days, RetentionDays: 30, Scope: "all_task_artifacts",
		},
		OutputProfiles: []MediaOutputProfile{
			{ID: MediaOutputProfileMaster2K, Width: 2560, Height: 1440, Format: "mp4_h264_yuv420p_cfr30"},
			{ID: MediaOutputProfileDelivery1080, Width: 1920, Height: 1080, Format: "mp4_h264_yuv420p_cfr30"},
		},
		DefaultCandidateProvider: MediaCandidateProviderSeedance,
		CandidateAdoptionPolicy:  MediaCandidateAdoptionQualifiedPresentationAuto,
	}
}

func NormalizeMediaDeliveryPreferences(input *MediaDeliveryPreferences) MediaDeliveryPreferences {
	defaults := DefaultMediaDeliveryPreferences()
	if input == nil {
		return defaults
	}
	out := *input
	out.OutputProfiles = append([]MediaOutputProfile{}, input.OutputProfiles...)
	if strings.TrimSpace(out.SchemaVersion) == "" {
		out.SchemaVersion = defaults.SchemaVersion
	}
	if strings.TrimSpace(out.Narration.Mode) == "" {
		out.Narration.Mode = defaults.Narration.Mode
	}
	if strings.TrimSpace(out.TOSRetention.Mode) == "" {
		out.TOSRetention.Mode = defaults.TOSRetention.Mode
	}
	if out.TOSRetention.RetentionDays == 0 {
		out.TOSRetention.RetentionDays = defaults.TOSRetention.RetentionDays
	}
	if strings.TrimSpace(out.TOSRetention.Scope) == "" {
		out.TOSRetention.Scope = defaults.TOSRetention.Scope
	}
	if len(out.OutputProfiles) == 0 {
		out.OutputProfiles = defaults.OutputProfiles
	}
	if strings.TrimSpace(out.DefaultCandidateProvider) == "" {
		out.DefaultCandidateProvider = defaults.DefaultCandidateProvider
	}
	if strings.TrimSpace(out.CandidateAdoptionPolicy) == "" {
		out.CandidateAdoptionPolicy = defaults.CandidateAdoptionPolicy
	}
	return out
}

func ValidateMediaDeliveryPreferences(input *MediaDeliveryPreferences) error {
	if input == nil {
		return nil
	}
	pref := NormalizeMediaDeliveryPreferences(input)
	if pref.SchemaVersion != MediaDeliveryPreferencesSchemaVersion {
		return fmt.Errorf("media_delivery_preferences.schema_version must be %s", MediaDeliveryPreferencesSchemaVersion)
	}
	switch pref.Narration.Mode {
	case MediaNarrationModeAutoByProductStyle, MediaNarrationModeDisabled, MediaNarrationModeCustom:
	default:
		return fmt.Errorf("media_delivery_preferences.narration.mode is invalid")
	}
	switch pref.TOSRetention.Mode {
	case MediaTOSRetentionModeStandard30Days:
		if pref.TOSRetention.RetentionDays != 30 {
			return fmt.Errorf("media_delivery_preferences.tos_retention.retention_days must be 30 for standard_30d")
		}
	case MediaTOSRetentionModeCustom:
		if pref.TOSRetention.RetentionDays < 1 || pref.TOSRetention.RetentionDays > 365 {
			return fmt.Errorf("media_delivery_preferences.tos_retention.retention_days must be between 1 and 365")
		}
	default:
		return fmt.Errorf("media_delivery_preferences.tos_retention.mode is invalid")
	}
	if pref.TOSRetention.Scope != "all_task_artifacts" {
		return fmt.Errorf("media_delivery_preferences.tos_retention.scope must be all_task_artifacts")
	}
	if pref.DefaultCandidateProvider != MediaCandidateProviderSeedance {
		return fmt.Errorf("media_delivery_preferences.default_candidate_provider must be seedance")
	}
	if pref.CandidateAdoptionPolicy != MediaCandidateAdoptionQualifiedPresentationAuto {
		return fmt.Errorf("media_delivery_preferences.candidate_adoption_policy is server-owned")
	}
	if len(pref.OutputProfiles) != 2 || pref.OutputProfiles[0] != DefaultMediaDeliveryPreferences().OutputProfiles[0] || pref.OutputProfiles[1] != DefaultMediaDeliveryPreferences().OutputProfiles[1] {
		return fmt.Errorf("media_delivery_preferences.output_profiles must contain the approved 2K and 1080p profiles")
	}
	return nil
}
