package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
)

const (
	MiniMaxH3Model              = "MiniMax-H3"
	miniMaxH3GenerationEndpoint = "/v2/video_generation"
	miniMaxH3QueryEndpoint      = "/v2/query/video_generation"
	defaultMiniMaxH3BaseURL     = "https://api.minimaxi.com"
)

// ErrMiniMaxH3LastFrameOnlyNotEnabled marks a request that the upstream H3 V2
// contract can express, but the current Server capability profile has not
// enabled or verified. It must be returned before any provider HTTP call.
var ErrMiniMaxH3LastFrameOnlyNotEnabled = errors.New("h3_last_frame_only_not_supported: MiniMax-H3 last_frame requires a first_frame in the current Server capability profile")

type MiniMaxH3ClientOptions struct {
	Mode       config.ArkMediaMode
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

type MiniMaxH3Client struct {
	mode    config.ArkMediaMode
	apiKey  string
	baseURL string
	http    *http.Client
}

type miniMaxH3SubmitRequest struct {
	Model         string        `json:"model"`
	Content       []ContentPart `json:"content"`
	Resolution    string        `json:"resolution"`
	Duration      int           `json:"duration"`
	Ratio         string        `json:"ratio,omitempty"`
	CallbackURL   string        `json:"callback_url,omitempty"`
	AIGCWatermark bool          `json:"aigc_watermark"`
}

type miniMaxH3SubmitResponse struct {
	TaskID string         `json:"task_id,omitempty"`
	Error  *ProviderError `json:"error,omitempty"`
}

type miniMaxH3QueryResponse struct {
	Task miniMaxH3Task `json:"task"`
}

type miniMaxH3Task struct {
	ID         string               `json:"id,omitempty"`
	Model      string               `json:"model,omitempty"`
	Status     string               `json:"status,omitempty"`
	Error      *ProviderError       `json:"error,omitempty"`
	CreatedAt  int64                `json:"created_at,omitempty"`
	UpdatedAt  int64                `json:"updated_at,omitempty"`
	Content    miniMaxH3TaskContent `json:"content,omitempty"`
	Resolution string               `json:"resolution,omitempty"`
	Duration   int                  `json:"duration,omitempty"`
	Usage      map[string]any       `json:"usage,omitempty"`
	Ratio      string               `json:"ratio,omitempty"`
	TaskType   string               `json:"task_type,omitempty"`
	Modality   string               `json:"modality,omitempty"`
}

type miniMaxH3TaskContent struct {
	URL string `json:"url,omitempty"`
}

func NewMiniMaxH3Client(options MiniMaxH3ClientOptions) *MiniMaxH3Client {
	mode := options.Mode
	if mode == "" {
		mode = config.ArkMediaModeDryRun
	}
	baseURL := strings.TrimRight(strings.TrimSpace(options.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultMiniMaxH3BaseURL
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &MiniMaxH3Client{mode: mode, apiKey: strings.TrimSpace(options.APIKey), baseURL: baseURL, http: httpClient}
}

func (c *MiniMaxH3Client) CreateContentGenerationTask(ctx context.Context, request ContentGenerationTaskRequest) (ContentGenerationTaskResult, error) {
	provider := config.ModelProvider("minimax-h3")
	endpoint := miniMaxH3Endpoint(c.baseURL, miniMaxH3GenerationEndpoint)
	trace := newTrace(c.mode, provider, MiniMaxH3Model, http.MethodPost, endpoint)
	result := ContentGenerationTaskResult{Mode: c.mode, Provider: provider, Model: MiniMaxH3Model, Trace: trace}
	if err := validateMiniMaxH3Request(request); err != nil {
		if errors.Is(err, ErrMiniMaxH3LastFrameOnlyNotEnabled) {
			result.Trace.ErrorClass = "provider_capability_not_enabled"
		} else {
			result.Trace.ErrorClass = "invalid_request"
		}
		return result, err
	}
	request.Model = MiniMaxH3Model

	submit := miniMaxH3SubmitRequest{
		Model:         MiniMaxH3Model,
		Content:       request.Content,
		Resolution:    "2K",
		Duration:      request.Duration,
		Ratio:         request.Ratio,
		AIGCWatermark: request.Watermark,
	}
	switch c.mode {
	case config.ArkMediaModeDisabled:
		result.Trace.ErrorClass = "disabled"
		return result, ErrArkMediaDisabled
	case config.ArkMediaModeDryRun, "":
		dryRun := request
		dryRun.Resolution = submit.Resolution
		result.Request = &dryRun
		return result, nil
	}
	if c.apiKey == "" {
		result.Trace.ErrorClass = "api_key_missing"
		return result, errors.New("MiniMax-H3 API key is missing")
	}

	var response miniMaxH3SubmitResponse
	trace, err := c.doJSON(ctx, http.MethodPost, endpoint, submit, &response, trace)
	result.Trace = trace
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(response.TaskID) == "" {
		result.Trace.ErrorClass = "task_id_missing"
		return result, errors.New("MiniMax-H3 submit response is missing task_id")
	}
	result.Response = &ContentGenerationTaskResponse{
		ID:     response.TaskID,
		Status: "queued",
		Model:  MiniMaxH3Model,
	}
	if response.Error != nil {
		result.Response.Error = response.Error
	}
	return result, nil
}

func (c *MiniMaxH3Client) GetContentGenerationTask(ctx context.Context, taskID string) (ContentGenerationTaskResult, error) {
	provider := config.ModelProvider("minimax-h3")
	taskID = strings.TrimSpace(taskID)
	endpoint := miniMaxH3Endpoint(c.baseURL, miniMaxH3QueryEndpoint+"/"+url.PathEscape(taskID))
	trace := newTrace(c.mode, provider, MiniMaxH3Model, http.MethodGet, endpoint)
	result := ContentGenerationTaskResult{Mode: c.mode, Provider: provider, Model: MiniMaxH3Model, Trace: trace}
	if taskID == "" {
		result.Trace.ErrorClass = "task_id_missing"
		return result, errors.New("task_id is required")
	}
	switch c.mode {
	case config.ArkMediaModeDisabled:
		result.Trace.ErrorClass = "disabled"
		return result, ErrArkMediaDisabled
	case config.ArkMediaModeDryRun, "":
		result.Response = &ContentGenerationTaskResponse{ID: taskID, Status: "dry_run", Model: MiniMaxH3Model}
		return result, nil
	}
	if c.apiKey == "" {
		result.Trace.ErrorClass = "api_key_missing"
		return result, errors.New("MiniMax-H3 API key is missing")
	}

	var response miniMaxH3QueryResponse
	trace, err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &response, trace)
	result.Trace = trace
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(response.Task.ID) == "" {
		result.Trace.ErrorClass = "task_id_missing"
		return result, errors.New("MiniMax-H3 query response is missing task.id")
	}
	output := map[string]any{}
	if videoURL := strings.TrimSpace(response.Task.Content.URL); videoURL != "" {
		output["video_url"] = videoURL
	}
	result.Response = &ContentGenerationTaskResponse{
		ID:     response.Task.ID,
		Status: strings.ToLower(strings.TrimSpace(response.Task.Status)),
		Model:  firstNonEmpty(response.Task.Model, MiniMaxH3Model),
		Output: output,
		Error:  response.Task.Error,
	}
	return result, nil
}

func validateMiniMaxH3Request(request ContentGenerationTaskRequest) error {
	if request.Model != "" && !strings.EqualFold(strings.TrimSpace(request.Model), MiniMaxH3Model) {
		return fmt.Errorf("MiniMax-H3 adapter only accepts model %s", MiniMaxH3Model)
	}
	if request.Duration < 4 || request.Duration > 15 {
		return errors.New("MiniMax-H3 duration must be an integer from 4 to 15 seconds")
	}
	if request.Resolution != "" && !strings.EqualFold(strings.TrimSpace(request.Resolution), "2K") {
		return errors.New("MiniMax-H3 resolution must be 2K")
	}
	if !miniMaxH3RatioAllowed(request.Ratio) {
		return errors.New("MiniMax-H3 ratio is unsupported")
	}
	if request.GenerateAudio {
		return errors.New("MiniMax-H3 generate_audio is not enabled because the supplied contract does not define it")
	}
	if request.ReturnLastFrame {
		return errors.New("MiniMax-H3 return_last_frame is not enabled because the supplied contract does not define it")
	}
	textFound := false
	referenceMode := false
	frameMode := false
	visualReferenceFound := false
	firstFrameCount := 0
	lastFrameCount := 0
	referenceImageCount := 0
	referenceVideoCount := 0
	referenceAudioCount := 0
	for _, part := range request.Content {
		switch part.Type {
		case "text":
			text := strings.TrimSpace(part.Text)
			if text != "" {
				textFound = true
			}
			if len([]rune(text)) > 7000 {
				return errors.New("MiniMax-H3 text prompt exceeds 7000 characters")
			}
		case "image_url":
			if part.ImageURL == nil || strings.TrimSpace(part.ImageURL.URL) == "" {
				return errors.New("MiniMax-H3 image_url content requires a URL")
			}
			switch part.Role {
			case "first_frame", "":
				frameMode = true
				firstFrameCount++
			case "last_frame":
				frameMode = true
				lastFrameCount++
			case "reference_image":
				referenceMode = true
				visualReferenceFound = true
				referenceImageCount++
			default:
				return errors.New("MiniMax-H3 image role is unsupported")
			}
		case "video_url":
			if part.VideoURL == nil || strings.TrimSpace(part.VideoURL.URL) == "" || part.Role != "reference_video" {
				return errors.New("MiniMax-H3 video_url requires role=reference_video and a URL")
			}
			referenceMode = true
			visualReferenceFound = true
			referenceVideoCount++
		case "audio_url":
			if part.AudioURL == nil || strings.TrimSpace(part.AudioURL.URL) == "" || part.Role != "reference_audio" {
				return errors.New("MiniMax-H3 audio_url requires role=reference_audio and a URL")
			}
			referenceMode = true
			referenceAudioCount++
		default:
			return fmt.Errorf("MiniMax-H3 content type %q is unsupported", part.Type)
		}
	}
	if !textFound {
		return errors.New("MiniMax-H3 content must contain a non-empty text prompt")
	}
	if frameMode && referenceMode {
		return errors.New("MiniMax-H3 frame mode and reference mode cannot be mixed")
	}
	if firstFrameCount > 1 || lastFrameCount > 1 {
		return errors.New("MiniMax-H3 accepts at most one first frame and one last frame")
	}
	if lastFrameCount > 0 && firstFrameCount == 0 {
		return ErrMiniMaxH3LastFrameOnlyNotEnabled
	}
	if referenceImageCount > 9 || referenceVideoCount > 3 || referenceAudioCount > 3 {
		return errors.New("MiniMax-H3 reference media count exceeds the documented limit")
	}
	if referenceMode && !visualReferenceFound {
		return errors.New("MiniMax-H3 reference_audio requires at least one reference image or video")
	}
	if !frameMode && !referenceMode && request.Ratio == "adaptive" {
		return errors.New("MiniMax-H3 text-to-video requires an explicit non-adaptive ratio")
	}
	return nil
}

func miniMaxH3RatioAllowed(ratio string) bool {
	switch strings.TrimSpace(ratio) {
	case "adaptive", "21:9", "16:9", "4:3", "1:1", "3:4", "9:16":
		return true
	default:
		return false
	}
}

func (c *MiniMaxH3Client) doJSON(ctx context.Context, method string, endpoint string, payload any, target any, trace ArkMediaCallTrace) (ArkMediaCallTrace, error) {
	started := time.Now()
	var requestBody io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			trace.ErrorClass = "request_marshal_failed"
			return trace, err
		}
		requestBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, requestBody)
	if err != nil {
		trace.ErrorClass = "request_build_failed"
		return trace, redactMiniMaxH3Secret(err, c.apiKey)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	trace.LatencyMS = int(time.Since(started).Milliseconds())
	if err != nil {
		trace.ErrorClass = "http_error"
		return trace, redactMiniMaxH3Secret(err, c.apiKey)
	}
	defer resp.Body.Close()
	trace.HTTPStatus = resp.StatusCode
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		trace.ErrorClass = "response_read_failed"
		return trace, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		trace.ErrorClass = fmt.Sprintf("http_%d", resp.StatusCode)
		return trace, redactMiniMaxH3Secret(fmt.Errorf("MiniMax-H3 HTTP %d: %s", resp.StatusCode, responseSnippet(responseBody)), c.apiKey)
	}
	if err := json.Unmarshal(responseBody, target); err != nil {
		trace.ErrorClass = "response_parse_failed"
		return trace, err
	}
	return trace, nil
}

func miniMaxH3Endpoint(baseURL string, endpoint string) string {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(baseURL, "/") + endpoint
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + endpoint
	return parsed.String()
}

func redactMiniMaxH3Secret(err error, secret string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if secret != "" {
		message = strings.ReplaceAll(message, secret, "[redacted]")
	}
	return errors.New(message)
}
