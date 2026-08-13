package media

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
)

// AudioModelOperation is deliberately provider-neutral. A vendor model may
// support one or more operations, but it must not change the editor timeline
// directly; it returns a candidate asset or transcript for review.
type AudioModelOperation string

const (
	AudioModelOperationTranscribe   AudioModelOperation = "transcribe"
	AudioModelOperationSynthesize   AudioModelOperation = "synthesize"
	AudioModelOperationEnhance      AudioModelOperation = "enhance"
	AudioModelOperationVoiceConvert AudioModelOperation = "voice_convert"
)

var ErrAudioModelProtocolPending = errors.New("audio model protocol adapter is pending vendor documentation")

type AudioModelInput struct {
	Operation        AudioModelOperation `json:"operation"`
	Model            string              `json:"model,omitempty"`
	InputRef         string              `json:"input_ref,omitempty"`
	InputSHA256      string              `json:"input_sha256,omitempty"`
	Text             string              `json:"text,omitempty"`
	Language         string              `json:"language,omitempty"`
	VoiceID          string              `json:"voice_id,omitempty"`
	OutputFormat     string              `json:"output_format,omitempty"`
	SampleRateHZ     int                 `json:"sample_rate_hz,omitempty"`
	Channels         int                 `json:"channels,omitempty"`
	Speed            float64             `json:"speed,omitempty"`
	Pitch            float64             `json:"pitch,omitempty"`
	NoiseReduction   string              `json:"noise_reduction,omitempty"`
	PreserveDuration bool                `json:"preserve_duration,omitempty"`
	Options          map[string]any      `json:"options,omitempty"`
}

type AudioModelTranscriptSegment struct {
	StartMS    int     `json:"start_ms"`
	EndMS      int     `json:"end_ms"`
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence,omitempty"`
}

type AudioModelOutput struct {
	AssetRef     string                        `json:"asset_ref,omitempty"`
	DownloadURL  string                        `json:"download_url,omitempty"`
	LocalPath    string                        `json:"-"`
	MimeType     string                        `json:"mime_type,omitempty"`
	DurationMS   int                           `json:"duration_ms,omitempty"`
	SampleRateHZ int                           `json:"sample_rate_hz,omitempty"`
	Channels     int                           `json:"channels,omitempty"`
	SHA256       string                        `json:"sha256,omitempty"`
	Text         string                        `json:"text,omitempty"`
	Transcript   []AudioModelTranscriptSegment `json:"transcript,omitempty"`
}

type AudioModelProviderResponse struct {
	TaskID string            `json:"task_id,omitempty"`
	Status string            `json:"status,omitempty"`
	Output *AudioModelOutput `json:"output,omitempty"`
	Error  *ProviderError    `json:"error,omitempty"`
}

type AudioModelResult struct {
	Mode      config.ArkMediaMode         `json:"mode"`
	Provider  config.ModelProvider        `json:"provider"`
	Model     string                      `json:"model"`
	Operation AudioModelOperation         `json:"operation"`
	Request   *AudioModelInput            `json:"request,omitempty"`
	Response  *AudioModelProviderResponse `json:"response,omitempty"`
	Trace     ArkMediaCallTrace           `json:"trace"`
}

type AudioModelClient interface {
	Process(ctx context.Context, request AudioModelInput) (AudioModelResult, error)
}

type AudioClient struct {
	mode     config.ArkMediaMode
	http     *http.Client
	provider config.ModelProviderCredential
	tts      openSpeechTTSConfig
	sleep    func(context.Context, time.Duration) error
	probe    func(context.Context, string, string) (audioProbeResult, error)
}

func NewAudioClient(runtime config.AppRuntimeConfig, httpClient *http.Client) *AudioClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	provider := runtime.ModelProviders[config.ModelProviderDoubao]
	if provider.Provider == "" {
		provider.Provider = config.ModelProviderDoubao
	}
	provider.DefaultModel = firstNonEmpty(
		os.Getenv("DOUBAO_AUDIO_MODEL"),
		os.Getenv("CASCADE_AUDIO_MODEL"),
		os.Getenv("VOLC_TTS_RESOURCE_ID"),
		provider.DefaultModel,
	)
	mode := runtime.ArkMediaMode
	if mode == "" {
		mode = config.ArkMediaModeDryRun
	}
	return &AudioClient{
		mode: mode, http: httpClient, provider: provider,
		tts:   openSpeechTTSConfigFromEnv(runtime),
		sleep: sleepWithContext,
		probe: probeAudioCandidate,
	}
}

func (c *AudioClient) Process(ctx context.Context, request AudioModelInput) (AudioModelResult, error) {
	modelName := firstNonEmpty(request.Model, c.provider.DefaultModel)
	request.Model = modelName
	result := AudioModelResult{
		Mode: c.mode, Provider: c.provider.Provider, Model: modelName,
		Operation: request.Operation, Trace: newTrace(c.mode, c.provider.Provider, modelName, http.MethodPost, c.provider.BaseURL),
	}
	if err := validateAudioModelInput(request, modelName); err != nil {
		result.Trace.ErrorClass = "invalid_request"
		return result, err
	}
	switch c.mode {
	case config.ArkMediaModeDisabled:
		result.Trace.ErrorClass = "disabled"
		return result, ErrArkMediaDisabled
	case config.ArkMediaModeDryRun, "":
		result.Request = &request
		return result, nil
	case config.ArkMediaModeReal:
		if request.Operation != AudioModelOperationSynthesize {
			result.Trace.ErrorClass = "protocol_pending"
			return result, ErrAudioModelProtocolPending
		}
		response, trace, err := c.processDoubaoTTS(ctx, request, result.Trace)
		result.Trace = trace
		if err != nil {
			return result, err
		}
		result.Response = response
		return result, nil
	default:
		result.Trace.ErrorClass = "unsupported_mode"
		return result, fmt.Errorf("unsupported audio model mode %q", c.mode)
	}
}

func validateAudioModelInput(request AudioModelInput, modelName string) error {
	if strings.TrimSpace(modelName) == "" {
		return errors.New("audio model is required")
	}
	switch request.Operation {
	case AudioModelOperationTranscribe, AudioModelOperationEnhance, AudioModelOperationVoiceConvert:
		if strings.TrimSpace(request.InputRef) == "" {
			return errors.New("audio input_ref is required")
		}
		if looksLikeLocalPath(request.InputRef) {
			return errors.New("audio model input_ref must be a provider asset reference or HTTPS URL, not a local path")
		}
	case AudioModelOperationSynthesize:
		if strings.TrimSpace(request.Text) == "" {
			return errors.New("audio synthesis text is required")
		}
	default:
		return fmt.Errorf("unsupported audio model operation %q", request.Operation)
	}
	if request.SampleRateHZ != 0 && (request.SampleRateHZ < 8000 || request.SampleRateHZ > 96000) {
		return errors.New("audio sample_rate_hz must be between 8000 and 96000")
	}
	if request.Channels != 0 && request.Channels != 1 && request.Channels != 2 {
		return errors.New("audio channels must be 1 or 2")
	}
	if request.Speed != 0 && (request.Speed < 0.25 || request.Speed > 4 || math.IsNaN(request.Speed) || math.IsInf(request.Speed, 0)) {
		return errors.New("audio speed must be between 0.25 and 4")
	}
	if request.Pitch != 0 && (request.Pitch < -24 || request.Pitch > 24 || math.IsNaN(request.Pitch) || math.IsInf(request.Pitch, 0)) {
		return errors.New("audio pitch must be between -24 and 24 semitones")
	}
	return nil
}

func looksLikeLocalPath(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, `\\`) || strings.Contains(value, `:\`) || strings.HasPrefix(strings.ToLower(value), "file://")
}
