package media

import (
	"errors"
	"fmt"
	"strings"
)

const (
	GeneratedShotProviderSeedance20 = "seedance-2.0"
	GeneratedShotProviderMiniMaxH3  = "minimax-h3"
	Seedance20ServerModel           = "doubao-seedance-2-0-260128"
)

type GeneratedShotCapabilityProfile struct {
	Provider                 string   `json:"provider"`
	ProfileVersion           string   `json:"profile_version"`
	MinDurationSec           int      `json:"min_duration_sec"`
	MaxDurationSec           int      `json:"max_duration_sec"`
	AllowedAspectRatios      []string `json:"allowed_aspect_ratios"`
	MaxReferences            int      `json:"max_references"`
	MaxReferenceImages       int      `json:"max_reference_images"`
	MaxReferenceVideos       int      `json:"max_reference_videos"`
	GeneralReferencesEnabled bool     `json:"general_references_enabled"`
	FirstFrameEnabled        bool     `json:"first_frame_enabled"`
	FirstLastFrameEnabled    bool     `json:"first_last_frame_enabled"`
	LastFrameOnlyEnabled     bool     `json:"last_frame_only_enabled"`
	ReferenceAudioEnabled    bool     `json:"reference_audio_enabled"`
	NativeOutputFPS          *int     `json:"native_output_fps,omitempty"`
	NormalizedOutputProfile  string   `json:"normalized_output_profile"`
	FailurePolicy            string   `json:"failure_policy"`
	Notes                    []string `json:"notes,omitempty"`
}

type GeneratedShotCompiledRequest struct {
	Provider       string                       `json:"provider"`
	ProfileVersion string                       `json:"profile_version"`
	Model          string                       `json:"model"`
	IntentID       string                       `json:"intent_id"`
	Request        ContentGenerationTaskRequest `json:"request"`
	DryRunOnly     bool                         `json:"dry_run_only"`
	Warnings       []string                     `json:"warnings,omitempty"`
}

type GeneratedShotProviderCompiler interface {
	Profile() GeneratedShotCapabilityProfile
	Compile(intent GeneratedShotIntent) (GeneratedShotCompiledRequest, error)
}

type GeneratedShotProviderValidationError struct {
	Provider string `json:"provider"`
	Field    string `json:"field"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

func (e *GeneratedShotProviderValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Provider + ": " + e.Code + ": " + e.Field + ": " + e.Message
}

type Seedance20GeneratedShotCompiler struct{}

func (Seedance20GeneratedShotCompiler) Profile() GeneratedShotCapabilityProfile {
	fps := 24
	return GeneratedShotCapabilityProfile{
		Provider: GeneratedShotProviderSeedance20, ProfileVersion: "demoops.seedance_2_0_generated_shot.v1",
		MinDurationSec: 4, MaxDurationSec: 15, AllowedAspectRatios: []string{"16:9"},
		MaxReferences: 4, MaxReferenceImages: 4, MaxReferenceVideos: 3,
		GeneralReferencesEnabled: true, FirstFrameEnabled: false, FirstLastFrameEnabled: false,
		LastFrameOnlyEnabled: false, ReferenceAudioEnabled: false, NativeOutputFPS: &fps,
		NormalizedOutputProfile: "MP4/H.264/yuv420p/1920x1080/CFR30",
		FailurePolicy:           GeneratedShotFailureContinue,
		Notes: []string{
			"The project profile is narrower than the vendor capability: the current Server route is verified at 1080p, 16:9, and general image/video references only.",
			"Frame-role requests remain disabled until independently tested and enabled in a profile revision.",
		},
	}
}

func (compiler Seedance20GeneratedShotCompiler) Compile(intent GeneratedShotIntent) (GeneratedShotCompiledRequest, error) {
	profile := compiler.Profile()
	if err := ValidateGeneratedShotIntent(intent); err != nil {
		return GeneratedShotCompiledRequest{}, err
	}
	if err := validateIntentAgainstGeneratedShotProfile(intent, profile); err != nil {
		return GeneratedShotCompiledRequest{}, err
	}
	content := []ContentPart{{Type: "text", Text: strings.TrimSpace(intent.Prompt)}}
	for _, ref := range intent.References {
		switch strings.ToLower(strings.TrimSpace(ref.MimeType)) {
		case "image/png", "image/jpeg":
			content = append(content, ContentPart{Type: "image_url", ImageURL: &MediaURL{URL: strings.TrimSpace(ref.URI)}})
		case "video/mp4", "video/quicktime":
			content = append(content, ContentPart{Type: "video_url", VideoURL: &MediaURL{URL: strings.TrimSpace(ref.URI)}})
		default:
			return GeneratedShotCompiledRequest{}, providerShotError(profile.Provider, "references", "seedance_reference_mime_unsupported", "reference MIME is not enabled by the Seedance 2.0 project profile")
		}
	}
	request := ContentGenerationTaskRequest{
		Model: Seedance20ServerModel, Content: content, Resolution: "1080p", Ratio: intent.AspectRatio,
		Duration: intent.DurationSec, GenerateAudio: false, ReturnLastFrame: true, Watermark: false,
	}
	return GeneratedShotCompiledRequest{
		Provider: profile.Provider, ProfileVersion: profile.ProfileVersion, Model: Seedance20ServerModel,
		IntentID: intent.IntentID, Request: request, DryRunOnly: true,
		Warnings: []string{"dry-run compiler only; this output is not registered with the active Seedance route"},
	}, nil
}

type MiniMaxH3GeneratedShotCompiler struct{}

func (MiniMaxH3GeneratedShotCompiler) Profile() GeneratedShotCapabilityProfile {
	return GeneratedShotCapabilityProfile{
		Provider: GeneratedShotProviderMiniMaxH3, ProfileVersion: "demoops.minimax_h3_generated_shot.v1",
		MinDurationSec: 4, MaxDurationSec: 15,
		AllowedAspectRatios: []string{"21:9", "16:9", "4:3", "1:1", "3:4", "9:16", "adaptive"},
		MaxReferences:       4, MaxReferenceImages: 4, MaxReferenceVideos: 3,
		GeneralReferencesEnabled: true, FirstFrameEnabled: true, FirstLastFrameEnabled: true,
		LastFrameOnlyEnabled: false, ReferenceAudioEnabled: false, NativeOutputFPS: nil,
		NormalizedOutputProfile: "MP4/H.264/yuv420p/1920x1080/CFR30",
		FailurePolicy:           GeneratedShotFailureContinue,
		Notes: []string{
			"H3 native output FPS is not requested by App and is not assumed by Server.",
			"last_frame without first_frame and reference audio are disabled before provider calls.",
		},
	}
}

func (compiler MiniMaxH3GeneratedShotCompiler) Compile(intent GeneratedShotIntent) (GeneratedShotCompiledRequest, error) {
	profile := compiler.Profile()
	if err := ValidateGeneratedShotIntent(intent); err != nil {
		return GeneratedShotCompiledRequest{}, err
	}
	if err := validateIntentAgainstGeneratedShotProfile(intent, profile); err != nil {
		return GeneratedShotCompiledRequest{}, err
	}
	content := []ContentPart{{Type: "text", Text: strings.TrimSpace(intent.Prompt)}}
	frameMode := false
	for _, ref := range intent.References {
		mimeType := strings.ToLower(strings.TrimSpace(ref.MimeType))
		switch ref.Usage {
		case GeneratedShotReferenceFirstFrame, GeneratedShotReferenceLastFrame:
			frameMode = true
			content = append(content, ContentPart{Type: "image_url", ImageURL: &MediaURL{URL: strings.TrimSpace(ref.URI)}, Role: ref.Usage})
		case GeneratedShotReferenceGeneral:
			switch mimeType {
			case "image/png", "image/jpeg":
				content = append(content, ContentPart{Type: "image_url", ImageURL: &MediaURL{URL: strings.TrimSpace(ref.URI)}, Role: "reference_image"})
			case "video/mp4", "video/quicktime":
				content = append(content, ContentPart{Type: "video_url", VideoURL: &MediaURL{URL: strings.TrimSpace(ref.URI)}, Role: "reference_video"})
			default:
				return GeneratedShotCompiledRequest{}, providerShotError(profile.Provider, "references", "h3_reference_mime_unsupported", "reference MIME is not enabled by the H3 project profile")
			}
		}
	}
	ratio := intent.AspectRatio
	if frameMode {
		ratio = "adaptive"
	}
	request := ContentGenerationTaskRequest{
		Model: MiniMaxH3Model, Content: content, Resolution: "2K", Ratio: ratio,
		Duration: intent.DurationSec, GenerateAudio: false, ReturnLastFrame: false, Watermark: false,
	}
	if err := validateMiniMaxH3Request(request); err != nil {
		return GeneratedShotCompiledRequest{}, providerShotError(profile.Provider, "request", "h3_request_compile_invalid", err.Error())
	}
	return GeneratedShotCompiledRequest{
		Provider: profile.Provider, ProfileVersion: profile.ProfileVersion, Model: MiniMaxH3Model,
		IntentID: intent.IntentID, Request: request, DryRunOnly: true,
		Warnings: []string{"dry-run compiler only; this output is not registered with the H3 sidecar or any Server route"},
	}, nil
}

func validateIntentAgainstGeneratedShotProfile(intent GeneratedShotIntent, profile GeneratedShotCapabilityProfile) error {
	if intent.DurationSec < profile.MinDurationSec || intent.DurationSec > profile.MaxDurationSec {
		return providerShotError(profile.Provider, "duration_sec", "provider_duration_out_of_range", fmt.Sprintf("duration must be %d-%d seconds", profile.MinDurationSec, profile.MaxDurationSec))
	}
	if !containsGeneratedShotValue(profile.AllowedAspectRatios, intent.AspectRatio) {
		return providerShotError(profile.Provider, "aspect_ratio", "provider_ratio_not_enabled", "aspect ratio is not enabled by the current provider project profile")
	}
	if len(intent.References) > profile.MaxReferences {
		return providerShotError(profile.Provider, "references", "provider_reference_limit_exceeded", "reference count exceeds the current provider project profile")
	}
	images := 0
	videos := 0
	firstFrames := 0
	lastFrames := 0
	general := 0
	for _, ref := range intent.References {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ref.MimeType)), "image/") {
			images++
		} else if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ref.MimeType)), "video/") {
			videos++
		}
		switch ref.Usage {
		case GeneratedShotReferenceFirstFrame:
			firstFrames++
		case GeneratedShotReferenceLastFrame:
			lastFrames++
		case GeneratedShotReferenceGeneral:
			general++
		}
	}
	if images > profile.MaxReferenceImages || videos > profile.MaxReferenceVideos {
		return providerShotError(profile.Provider, "references", "provider_reference_type_limit_exceeded", "reference type count exceeds the current provider project profile")
	}
	if general > 0 && !profile.GeneralReferencesEnabled {
		return providerShotError(profile.Provider, "references", "provider_general_references_not_enabled", "general references are not enabled")
	}
	if firstFrames > 1 || lastFrames > 1 {
		return providerShotError(profile.Provider, "references", "provider_frame_count_invalid", "at most one first frame and one last frame are accepted")
	}
	if firstFrames > 0 && lastFrames == 0 && !profile.FirstFrameEnabled {
		return providerShotError(profile.Provider, "references", "provider_first_frame_not_enabled", "first-frame generation is not enabled by the current provider project profile")
	}
	if firstFrames > 0 && lastFrames > 0 && !profile.FirstLastFrameEnabled {
		return providerShotError(profile.Provider, "references", "provider_first_last_frame_not_enabled", "first-last-frame generation is not enabled by the current provider project profile")
	}
	if lastFrames > 0 && firstFrames == 0 && !profile.LastFrameOnlyEnabled {
		return providerShotError(profile.Provider, "references", "provider_last_frame_only_not_enabled", "last-frame-only generation is not enabled by the current provider project profile")
	}
	if general > 0 && (firstFrames > 0 || lastFrames > 0) {
		return providerShotError(profile.Provider, "references", "provider_reference_modes_mixed", "general reference and frame modes cannot be mixed")
	}
	return nil
}

func providerShotError(provider string, field string, code string, message string) error {
	return &GeneratedShotProviderValidationError{Provider: provider, Field: field, Code: code, Message: message}
}

func containsGeneratedShotValue(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func asGeneratedShotProviderError(err error) *GeneratedShotProviderValidationError {
	var target *GeneratedShotProviderValidationError
	if errors.As(err, &target) {
		return target
	}
	return nil
}
