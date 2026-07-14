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
	"path"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
)

const (
	contentGenerationTasksEndpoint = "/contents/generations/tasks"
	imageGenerationsEndpoint       = "/images/generations"
	defaultHTTPTimeout             = 90 * time.Second
)

var (
	ErrArkMediaDisabled = errors.New("ark media client is disabled")
)

type ArkMediaClient interface {
	CreateContentGenerationTask(ctx context.Context, request ContentGenerationTaskRequest) (ContentGenerationTaskResult, error)
	GetContentGenerationTask(ctx context.Context, taskID string) (ContentGenerationTaskResult, error)
	GenerateImages(ctx context.Context, request ImageGenerationRequest) (ImageGenerationResult, error)
}

type Client struct {
	mode  config.ArkMediaMode
	http  *http.Client
	video config.ModelProviderCredential
	image config.ModelProviderCredential
}

type ContentGenerationTaskRequest struct {
	Model           string        `json:"model,omitempty"`
	Content         []ContentPart `json:"content,omitempty"`
	Resolution      string        `json:"resolution,omitempty"`
	Ratio           string        `json:"ratio,omitempty"`
	Duration        int           `json:"duration,omitempty"`
	GenerateAudio   bool          `json:"generate_audio"`
	ReturnLastFrame bool          `json:"return_last_frame"`
	Watermark       bool          `json:"watermark"`
}

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
}

type MediaURL struct {
	URL string `json:"url"`
}

type ContentGenerationTaskResponse struct {
	ID     string         `json:"id,omitempty"`
	Status string         `json:"status,omitempty"`
	Model  string         `json:"model,omitempty"`
	Output map[string]any `json:"output,omitempty"`
	Error  *ProviderError `json:"error,omitempty"`
}

type ContentGenerationTaskResult struct {
	Mode     config.ArkMediaMode            `json:"mode"`
	Provider config.ModelProvider           `json:"provider"`
	Model    string                         `json:"model"`
	Request  *ContentGenerationTaskRequest  `json:"request,omitempty"`
	Response *ContentGenerationTaskResponse `json:"response,omitempty"`
	Trace    ArkMediaCallTrace              `json:"trace"`
}

type ImageGenerationRequest struct {
	Model          string `json:"model,omitempty"`
	Prompt         string `json:"prompt"`
	Size           string `json:"size,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	OutputFormat   string `json:"output_format,omitempty"`
	Watermark      bool   `json:"watermark"`
}

type ImageGenerationResponse struct {
	Created int64                 `json:"created,omitempty"`
	Data    []ImageGenerationItem `json:"data,omitempty"`
	Error   *ProviderError        `json:"error,omitempty"`
}

type ImageGenerationItem struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type ImageGenerationResult struct {
	Mode     config.ArkMediaMode      `json:"mode"`
	Provider config.ModelProvider     `json:"provider"`
	Model    string                   `json:"model"`
	Request  *ImageGenerationRequest  `json:"request,omitempty"`
	Response *ImageGenerationResponse `json:"response,omitempty"`
	Trace    ArkMediaCallTrace        `json:"trace"`
}

type ProviderError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Type    string `json:"type,omitempty"`
}

type ArkMediaCallTrace struct {
	Provider     config.ModelProvider `json:"provider"`
	Model        string               `json:"model,omitempty"`
	Mode         config.ArkMediaMode  `json:"mode"`
	Method       string               `json:"method"`
	EndpointHost string               `json:"endpoint_host,omitempty"`
	EndpointPath string               `json:"endpoint_path,omitempty"`
	HTTPStatus   int                  `json:"http_status,omitempty"`
	ErrorClass   string               `json:"error_class,omitempty"`
	LatencyMS    int                  `json:"latency_ms,omitempty"`
}

func NewClient(runtime config.AppRuntimeConfig, httpClient *http.Client) *Client {
	mode := runtime.ArkMediaMode
	if mode == "" {
		mode = config.ArkMediaModeDryRun
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	video := runtime.ModelProviders[config.ModelProviderSeedance]
	image := runtime.ModelProviders[config.ModelProviderSeedream]
	if image.Provider == "" {
		image = runtime.ModelProviders[config.ModelProviderDoubao]
	}
	return &Client{mode: mode, http: httpClient, video: video, image: image}
}

func (c *Client) CreateContentGenerationTask(ctx context.Context, request ContentGenerationTaskRequest) (ContentGenerationTaskResult, error) {
	provider := c.video
	modelName := firstNonEmpty(request.Model, provider.DefaultModel)
	request.Model = modelName
	endpoint := endpointURL(provider.BaseURL, contentGenerationTasksEndpoint)
	trace := newTrace(c.mode, provider.Provider, modelName, http.MethodPost, endpoint)
	result := ContentGenerationTaskResult{Mode: c.mode, Provider: provider.Provider, Model: modelName, Trace: trace}

	switch c.mode {
	case config.ArkMediaModeDisabled:
		result.Trace.ErrorClass = "disabled"
		return result, ErrArkMediaDisabled
	case config.ArkMediaModeDryRun, "":
		result.Request = &request
		return result, nil
	}
	if err := validateProvider(provider, modelName); err != nil {
		result.Trace.ErrorClass = classifyConfigError(err)
		return result, err
	}
	var response ContentGenerationTaskResponse
	trace, err := c.doJSON(ctx, provider, http.MethodPost, endpoint, request, &response, trace)
	result.Trace = trace
	if err != nil {
		return result, err
	}
	result.Response = &response
	return result, nil
}

func (c *Client) GetContentGenerationTask(ctx context.Context, taskID string) (ContentGenerationTaskResult, error) {
	provider := c.video
	modelName := provider.DefaultModel
	endpoint := endpointURL(provider.BaseURL, path.Join(contentGenerationTasksEndpoint, taskID))
	trace := newTrace(c.mode, provider.Provider, modelName, http.MethodGet, endpoint)
	result := ContentGenerationTaskResult{Mode: c.mode, Provider: provider.Provider, Model: modelName, Trace: trace}

	if strings.TrimSpace(taskID) == "" {
		result.Trace.ErrorClass = "task_id_missing"
		return result, errors.New("task_id is required")
	}
	switch c.mode {
	case config.ArkMediaModeDisabled:
		result.Trace.ErrorClass = "disabled"
		return result, ErrArkMediaDisabled
	case config.ArkMediaModeDryRun, "":
		result.Response = &ContentGenerationTaskResponse{ID: taskID, Status: "dry_run", Model: modelName}
		return result, nil
	}
	if err := validateProvider(provider, modelName); err != nil {
		result.Trace.ErrorClass = classifyConfigError(err)
		return result, err
	}
	var response ContentGenerationTaskResponse
	trace, err := c.doJSON(ctx, provider, http.MethodGet, endpoint, nil, &response, trace)
	result.Trace = trace
	if err != nil {
		return result, err
	}
	result.Response = &response
	return result, nil
}

func (c *Client) GenerateImages(ctx context.Context, request ImageGenerationRequest) (ImageGenerationResult, error) {
	provider := c.image
	modelName := firstNonEmpty(request.Model, provider.DefaultModel)
	request.Model = modelName
	endpoint := endpointURL(provider.BaseURL, imageGenerationsEndpoint)
	trace := newTrace(c.mode, provider.Provider, modelName, http.MethodPost, endpoint)
	result := ImageGenerationResult{Mode: c.mode, Provider: provider.Provider, Model: modelName, Trace: trace}

	switch c.mode {
	case config.ArkMediaModeDisabled:
		result.Trace.ErrorClass = "disabled"
		return result, ErrArkMediaDisabled
	case config.ArkMediaModeDryRun, "":
		result.Request = &request
		return result, nil
	}
	if err := validateProvider(provider, modelName); err != nil {
		result.Trace.ErrorClass = classifyConfigError(err)
		return result, err
	}
	var response ImageGenerationResponse
	trace, err := c.doJSON(ctx, provider, http.MethodPost, endpoint, request, &response, trace)
	result.Trace = trace
	if err != nil {
		return result, err
	}
	result.Response = &response
	return result, nil
}

func (c *Client) doJSON(ctx context.Context, provider config.ModelProviderCredential, method string, endpoint string, payload any, target any, trace ArkMediaCallTrace) (ArkMediaCallTrace, error) {
	start := time.Now()
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			trace.ErrorClass = "request_marshal_failed"
			return trace, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		trace.ErrorClass = "request_build_failed"
		return trace, redactKnownSecrets(err, provider)
	}
	req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	trace.LatencyMS = int(time.Since(start).Milliseconds())
	if err != nil {
		trace.ErrorClass = "http_error"
		return trace, redactKnownSecrets(err, provider)
	}
	defer resp.Body.Close()
	trace.HTTPStatus = resp.StatusCode
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		trace.ErrorClass = "response_read_failed"
		return trace, redactKnownSecrets(err, provider)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		trace.ErrorClass = fmt.Sprintf("http_%d", resp.StatusCode)
		return trace, redactKnownSecrets(fmt.Errorf("ark media HTTP %d: %s", resp.StatusCode, responseSnippet(data)), provider)
	}
	if target == nil || len(strings.TrimSpace(string(data))) == 0 {
		return trace, nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		trace.ErrorClass = "response_parse_failed"
		return trace, redactKnownSecrets(err, provider)
	}
	return trace, nil
}

func validateProvider(provider config.ModelProviderCredential, modelName string) error {
	if strings.TrimSpace(provider.APIKey) == "" {
		return errors.New("ark media API key is missing")
	}
	if strings.TrimSpace(provider.BaseURL) == "" {
		return errors.New("ark media base URL is missing")
	}
	if strings.TrimSpace(modelName) == "" {
		return errors.New("ark media model is missing")
	}
	return nil
}

func classifyConfigError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "api key"):
		return "api_key_missing"
	case strings.Contains(message, "base url"):
		return "base_url_missing"
	case strings.Contains(message, "model"):
		return "model_missing"
	default:
		return "config_error"
	}
}

func newTrace(mode config.ArkMediaMode, provider config.ModelProvider, modelName string, method string, endpoint string) ArkMediaCallTrace {
	host, pathValue := safeURLParts(endpoint)
	return ArkMediaCallTrace{
		Provider:     provider,
		Model:        modelName,
		Mode:         mode,
		Method:       method,
		EndpointHost: host,
		EndpointPath: pathValue,
	}
}

func endpointURL(baseURL string, endpoint string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return strings.TrimLeft(endpoint, "/")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return baseURL + "/" + strings.TrimLeft(endpoint, "/")
	}
	parsed.Path = path.Join(parsed.Path, strings.TrimLeft(endpoint, "/"))
	return parsed.String()
}

func safeURLParts(raw string) (string, string) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	return parsed.Host, parsed.EscapedPath()
}

func redactKnownSecrets(err error, providers ...config.ModelProviderCredential) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, provider := range providers {
		for _, secret := range []string{provider.APIKey} {
			if strings.TrimSpace(secret) != "" {
				message = strings.ReplaceAll(message, secret, "[redacted]")
			}
		}
	}
	return errors.New(message)
}

func responseSnippet(data []byte) string {
	value := strings.Join(strings.Fields(string(data)), " ")
	if len(value) > 600 {
		return value[:600] + "..."
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
