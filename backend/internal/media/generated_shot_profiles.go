package media

import (
	"errors"
	"fmt"
	"strings"
)

const (
	GeneratedShotProviderSeedance20 = "seedance-2.0"
	GeneratedShotProviderSeedance25 = "seedance-2.5"
	GeneratedShotProviderMiniMaxH3  = "minimax-h3"
	Seedance20ServerModel           = "doubao-seedance-2-0-260128"
	Seedance25ServerModel           = "doubao-seedance-2-5-260628"
)

func isGeneratedShotSeedanceProvider(provider string) bool {
	return provider == GeneratedShotProviderSeedance20 || provider == GeneratedShotProviderSeedance25
}

func isSupportedGeneratedShotProvider(provider string) bool {
	return isGeneratedShotSeedanceProvider(provider) || provider == GeneratedShotProviderMiniMaxH3
}

func generatedShotProviderSetHasSeedance(providers map[string]struct{}) bool {
	for provider := range providers {
		if isGeneratedShotSeedanceProvider(provider) {
			return true
		}
	}
	return false
}

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

// Seedance25GeneratedShotCompiler compiles only presentation-only candidate
// requests. It deliberately remains dry-run-only until the Server's provider
// admission, TOS publication, budget, callback, and real-call preflight gates
// have all been enabled independently.
type Seedance25GeneratedShotCompiler struct{}

func (Seedance25GeneratedShotCompiler) Profile() GeneratedShotCapabilityProfile {
	fps := 24
	return GeneratedShotCapabilityProfile{
		Provider: GeneratedShotProviderSeedance25, ProfileVersion: "demoops.seedance_2_5_generated_shot.v1",
		MinDurationSec: 4, MaxDurationSec: 15,
		AllowedAspectRatios: []string{"21:9", "16:9", "4:3", "1:1", "3:4", "9:16", "adaptive"},
		MaxReferences:       4, MaxReferenceImages: 4, MaxReferenceVideos: 3,
		GeneralReferencesEnabled: true, FirstFrameEnabled: true, FirstLastFrameEnabled: true,
		LastFrameOnlyEnabled: false, ReferenceAudioEnabled: false, NativeOutputFPS: &fps,
		NormalizedOutputProfile: "MP4/H.264/yuv420p/1920x1080/CFR30",
		FailurePolicy:           GeneratedShotFailureContinue,
		Notes: []string{
			"Seedance 2.5 supports broader vendor inputs; the Server compiler intentionally accepts only the common reviewed presentation-shot envelope.",
			"Video-reference candidate requests use omni_reference_task_type=reference; video editing and extension are not enabled by this compiler.",
			"The native 1080p output can be 10-bit/H.265; the source download remains immutable and a separate editor-compatible MP4 derivative is required before review.",
		},
	}
}

func (compiler Seedance25GeneratedShotCompiler) Compile(intent GeneratedShotIntent) (GeneratedShotCompiledRequest, error) {
	profile := compiler.Profile()
	if err := ValidateGeneratedShotIntent(intent); err != nil {
		return GeneratedShotCompiledRequest{}, err
	}
	if err := validateIntentAgainstGeneratedShotProfile(intent, profile); err != nil {
		return GeneratedShotCompiledRequest{}, err
	}

	content := []ContentPart{{Type: "text", Text: strings.TrimSpace(intent.Prompt)}}
	frameMode := false
	generalReferences := false
	for _, ref := range intent.References {
		mimeType := strings.ToLower(strings.TrimSpace(ref.MimeType))
		uri := strings.TrimSpace(ref.URI)
		switch ref.Usage {
		case GeneratedShotReferenceFirstFrame, GeneratedShotReferenceLastFrame:
			frameMode = true
			content = append(content, ContentPart{Type: "image_url", ImageURL: &MediaURL{URL: uri}, Role: ref.Usage})
		case GeneratedShotReferenceGeneral:
			generalReferences = true
			switch mimeType {
			case "image/png", "image/jpeg":
				content = append(content, ContentPart{Type: "image_url", ImageURL: &MediaURL{URL: uri}, Role: "reference_image"})
			case "video/mp4", "video/quicktime":
				content = append(content, ContentPart{Type: "video_url", VideoURL: &MediaURL{URL: uri}, Role: "reference_video"})
			default:
				return GeneratedShotCompiledRequest{}, providerShotError(profile.Provider, "references", "seedance_2_5_reference_mime_unsupported", "reference MIME is not enabled by the Seedance 2.5 project profile")
			}
		}
	}

	ratio := intent.AspectRatio
	if frameMode {
		ratio = "adaptive"
	}
	taskType := "auto"
	if generalReferences {
		taskType = "reference"
	}
	request := ContentGenerationTaskRequest{
		Model: Seedance25ServerModel, Content: content, OmniReferenceTaskType: taskType,
		Resolution: "1080p", Ratio: ratio, Duration: intent.DurationSec,
		GenerateAudio: false, ReturnLastFrame: true, Watermark: false, OutputFormat: "mp4",
	}
	if err := validateSeedance25GeneratedShotRequest(request, frameMode, generalReferences); err != nil {
		return GeneratedShotCompiledRequest{}, providerShotError(profile.Provider, "request", "seedance_2_5_request_compile_invalid", err.Error())
	}
	return GeneratedShotCompiledRequest{
		Provider: profile.Provider, ProfileVersion: profile.ProfileVersion, Model: Seedance25ServerModel,
		IntentID: intent.IntentID, Request: request, DryRunOnly: true,
		Warnings: []string{"dry-run compiler only; Seedance 2.5 is not registered with an active Server route or editor timeline"},
	}, nil
}

func validateSeedance25GeneratedShotRequest(request ContentGenerationTaskRequest, frameMode bool, generalReferences bool) error {
	if request.Model != Seedance25ServerModel || request.Resolution != "1080p" || request.OutputFormat != "mp4" {
		return errors.New("model, 1080p resolution, and mp4 output are required by the current Seedance 2.5 project profile")
	}
	if request.GenerateAudio || request.Watermark || !request.ReturnLastFrame {
		return errors.New("native audio and watermark must remain disabled and a last-frame reference must be requested")
	}
	if request.Duration < 4 || request.Duration > 15 {
		return errors.New("duration must remain within the common 4-15 second candidate profile")
	}
	if frameMode && request.Ratio != "adaptive" {
		return errors.New("first-frame or first-last-frame requests require adaptive ratio")
	}
	if generalReferences && request.OmniReferenceTaskType != "reference" {
		return errors.New("general references require omni_reference_task_type=reference")
	}
	if !generalReferences && request.OmniReferenceTaskType != "auto" {
		return errors.New("text and frame-mode requests require omni_reference_task_type=auto")
	}
	return nil
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

func isGeneratedShotProviderSupported(provider string) bool {
	switch provider {
	case GeneratedShotProviderSeedance20, GeneratedShotProviderSeedance25, GeneratedShotProviderMiniMaxH3:
		return true
	default:
		return false
	}
}

func hasSeedanceGeneratedShotProvider(providers map[string]struct{}) bool {
	_, has20 := providers[GeneratedShotProviderSeedance20]
	_, has25 := providers[GeneratedShotProviderSeedance25]
	return has20 || has25
}

func asGeneratedShotProviderError(err error) *GeneratedShotProviderValidationError {
	var target *GeneratedShotProviderValidationError
	if errors.As(err, &target) {
		return target
	}
	return nil
}
