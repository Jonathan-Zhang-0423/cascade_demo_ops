package llm

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
	"regexp"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
)

const DefaultTimeout = 45 * time.Second

type Client interface {
	GenerateJSON(ctx context.Context, task config.ModelTask, req JSONRequest, target any) (*CallTrace, error)
	GenerateText(ctx context.Context, task config.ModelTask, req TextRequest) (string, *CallTrace, error)
	GenerateMultimodal(ctx context.Context, task config.ModelTask, req MultimodalRequest, target any) (*CallTrace, error)
}

type JSONRequest struct {
	System       string
	User         string
	SchemaName   string
	MaxTokens    int
	Temperature  float64
	ResponseHint string
}

type TextRequest struct {
	System      string
	User        string
	MaxTokens   int
	Temperature float64
}

type MultimodalRequest struct {
	System      string
	User        string
	Images      []ImageInput
	SchemaName  string
	MaxTokens   int
	Temperature float64
}

type ImageInput struct {
	MimeType string
	DataURI  string
	URL      string
	Label    string
}

type CallTrace struct {
	Provider       config.ModelProvider `json:"provider"`
	Model          string               `json:"model"`
	Task           config.ModelTask     `json:"task"`
	AdapterVersion string               `json:"adapter_version"`
	Mode           config.LLMMode       `json:"mode"`
	FallbackReason string               `json:"fallback_reason,omitempty"`
	LatencyMS      int                  `json:"latency_ms,omitempty"`
	InputTokens    int                  `json:"input_tokens,omitempty"`
	OutputTokens   int                  `json:"output_tokens,omitempty"`
}

func (t *CallTrace) Label() string {
	if t == nil {
		return ""
	}
	parts := []string{string(t.Provider), t.Model, t.AdapterVersion}
	if t.FallbackReason != "" {
		parts = append(parts, "fallback="+t.FallbackReason)
	}
	return strings.Join(parts, "/")
}

func (t *CallTrace) Metadata() map[string]any {
	if t == nil {
		return nil
	}
	metadata := map[string]any{
		"provider":        string(t.Provider),
		"model":           t.Model,
		"task":            string(t.Task),
		"adapter_version": t.AdapterVersion,
		"llm_mode":        string(t.Mode),
	}
	if t.FallbackReason != "" {
		metadata["fallback_reason"] = t.FallbackReason
	}
	if t.LatencyMS > 0 {
		metadata["latency_ms"] = t.LatencyMS
	}
	return metadata
}

type Router struct {
	runtime config.AppRuntimeConfig
	http    *http.Client
}

func NewRouter(runtime config.AppRuntimeConfig) *Router {
	timeout := DefaultTimeout
	return &Router{runtime: runtime, http: &http.Client{Timeout: timeout}}
}

func (r *Router) Mode() config.LLMMode {
	if r == nil || r.runtime.LLMMode == "" {
		return config.LLMModeAuto
	}
	return r.runtime.LLMMode
}

func (r *Router) GenerateJSON(ctx context.Context, task config.ModelTask, req JSONRequest, target any) (*CallTrace, error) {
	if r == nil {
		return nil, ErrDeterministicRequired{Reason: "llm router not configured"}
	}
	route, provider, trace, err := r.resolve(task)
	if err != nil {
		return trace, err
	}
	req.User = appendJSONInstruction(req.User, req.SchemaName, req.ResponseHint)
	text, callTrace, err := r.callText(ctx, route, provider, TextRequest{
		System:      req.System,
		User:        req.User,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	})
	if callTrace != nil {
		trace = callTrace
	}
	if err != nil {
		if r.Mode() == config.LLMModeReal {
			return trace, err
		}
		reason := "provider_error"
		if trace != nil && trace.FallbackReason != "" {
			reason = trace.FallbackReason
		}
		return traceWithFallback(trace, reason), ErrDeterministicRequired{Reason: reason}
	}
	if err := DecodeJSONContent(text, target); err != nil {
		if r.Mode() == config.LLMModeReal {
			return trace, fmt.Errorf("llm JSON parse failed: %w", err)
		}
		return traceWithFallback(trace, "json_parse_failed"), ErrDeterministicRequired{Reason: "json_parse_failed"}
	}
	return trace, nil
}

func (r *Router) GenerateText(ctx context.Context, task config.ModelTask, req TextRequest) (string, *CallTrace, error) {
	if r == nil {
		return "", nil, ErrDeterministicRequired{Reason: "llm router not configured"}
	}
	route, provider, trace, err := r.resolve(task)
	if err != nil {
		return "", trace, err
	}
	text, trace, err := r.callText(ctx, route, provider, req)
	if err != nil {
		if r.Mode() == config.LLMModeReal {
			return "", trace, err
		}
		reason := "provider_error"
		if trace != nil && trace.FallbackReason != "" {
			reason = trace.FallbackReason
		}
		return "", traceWithFallback(trace, reason), ErrDeterministicRequired{Reason: reason}
	}
	return text, trace, nil
}

func (r *Router) GenerateMultimodal(ctx context.Context, task config.ModelTask, req MultimodalRequest, target any) (*CallTrace, error) {
	if r == nil {
		return nil, ErrDeterministicRequired{Reason: "llm router not configured"}
	}
	route, provider, trace, err := r.resolve(task)
	if err != nil {
		return trace, err
	}
	text, callTrace, err := r.callMultimodal(ctx, route, provider, req)
	if callTrace != nil {
		trace = callTrace
	}
	if err != nil {
		if r.Mode() == config.LLMModeReal {
			return trace, err
		}
		reason := "provider_error"
		if trace != nil && trace.FallbackReason != "" {
			reason = trace.FallbackReason
		}
		return traceWithFallback(trace, reason), ErrDeterministicRequired{Reason: reason}
	}
	if err := DecodeJSONContent(text, target); err != nil {
		if r.Mode() == config.LLMModeReal {
			return trace, fmt.Errorf("llm JSON parse failed: %w", err)
		}
		return traceWithFallback(trace, "json_parse_failed"), ErrDeterministicRequired{Reason: "json_parse_failed"}
	}
	return trace, nil
}

func (r *Router) resolve(task config.ModelTask) (config.ModelTaskRoute, config.ModelProviderCredential, *CallTrace, error) {
	mode := r.Mode()
	route := r.runtime.ModelTaskRoutes[task]
	provider := r.runtime.ModelProviders[route.Provider]
	modelName := firstNonEmpty(route.Model, provider.DefaultModel)
	trace := &CallTrace{
		Provider:       route.Provider,
		Model:          modelName,
		Task:           task,
		AdapterVersion: firstNonEmpty(r.runtime.ModelAdapterVersion, config.ModelAdapterVersion),
		Mode:           mode,
	}
	if mode == config.LLMModeDeterministic {
		return route, provider, traceWithFallback(trace, "deterministic_mode"), ErrDeterministicRequired{Reason: "deterministic_mode"}
	}
	if route.Provider == "" || provider.Provider == "" {
		return route, provider, traceWithFallback(trace, "route_not_configured"), fallbackOrError(mode, "model route not configured")
	}
	if strings.TrimSpace(provider.APIKey) == "" {
		return route, provider, traceWithFallback(trace, "api_key_missing"), fallbackOrError(mode, "API key missing for "+string(route.Provider))
	}
	if strings.TrimSpace(provider.BaseURL) == "" {
		return route, provider, traceWithFallback(trace, "base_url_missing"), fallbackOrError(mode, "base URL missing for "+string(route.Provider))
	}
	if modelName == "" {
		return route, provider, traceWithFallback(trace, "model_missing"), fallbackOrError(mode, "model missing for "+string(route.Provider))
	}
	route.Model = modelName
	return route, provider, trace, nil
}

func fallbackOrError(mode config.LLMMode, reason string) error {
	if mode == config.LLMModeReal {
		return errors.New(reason)
	}
	return ErrDeterministicRequired{Reason: reason}
}

func (r *Router) callText(ctx context.Context, route config.ModelTaskRoute, provider config.ModelProviderCredential, req TextRequest) (string, *CallTrace, error) {
	if route.Provider == config.ModelProviderMinimax {
		return r.callMiniMaxText(ctx, route, provider, req)
	}
	return r.callOpenAIText(ctx, route, provider, req)
}

func (r *Router) callMultimodal(ctx context.Context, route config.ModelTaskRoute, provider config.ModelProviderCredential, req MultimodalRequest) (string, *CallTrace, error) {
	if route.Provider == config.ModelProviderMinimax {
		return r.callMiniMaxMultimodal(ctx, route, provider, req)
	}
	return r.callOpenAIMultimodal(ctx, route, provider, req)
}

func (r *Router) callOpenAIText(ctx context.Context, route config.ModelTaskRoute, provider config.ModelProviderCredential, req TextRequest) (string, *CallTrace, error) {
	payload := openAIChatRequest{
		Model:       route.Model,
		Messages:    []openAIMessage{{Role: "system", Content: req.System}, {Role: "user", Content: req.User}},
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	return r.doChat(ctx, route, provider, chatCompletionsURL(provider.BaseURL), payload)
}

func (r *Router) callOpenAIMultimodal(ctx context.Context, route config.ModelTaskRoute, provider config.ModelProviderCredential, req MultimodalRequest) (string, *CallTrace, error) {
	content := []openAIContentPart{{Type: "text", Text: appendJSONInstruction(req.User, req.SchemaName, "")}}
	for _, image := range req.Images {
		ref := firstNonEmpty(image.DataURI, image.URL)
		if ref == "" {
			continue
		}
		content = append(content, openAIContentPart{Type: "image_url", ImageURL: &openAIImageURL{URL: ref}})
	}
	payload := openAIChatRequest{
		Model:       route.Model,
		Messages:    []openAIMessage{{Role: "system", Content: req.System}, {Role: "user", Content: content}},
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	return r.doChat(ctx, route, provider, chatCompletionsURL(provider.BaseURL), payload)
}

func (r *Router) callMiniMaxText(ctx context.Context, route config.ModelTaskRoute, provider config.ModelProviderCredential, req TextRequest) (string, *CallTrace, error) {
	payload := minimaxChatRequest{
		Model: route.Model,
		Messages: []openAIMessage{
			{Role: "system", Content: req.System},
			{Role: "user", Content: req.User},
		},
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	return r.doChat(ctx, route, provider, chatCompletionsURL(provider.BaseURL), payload)
}

func (r *Router) callMiniMaxMultimodal(ctx context.Context, route config.ModelTaskRoute, provider config.ModelProviderCredential, req MultimodalRequest) (string, *CallTrace, error) {
	content := []openAIContentPart{{Type: "text", Text: appendJSONInstruction(req.User, req.SchemaName, "")}}
	for _, image := range req.Images {
		ref := firstNonEmpty(image.DataURI, image.URL)
		if ref == "" {
			continue
		}
		content = append(content, openAIContentPart{Type: "image_url", ImageURL: &openAIImageURL{URL: ref}})
	}
	payload := minimaxChatRequest{
		Model:       route.Model,
		Messages:    []openAIMessage{{Role: "system", Content: req.System}, {Role: "user", Content: content}},
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	return r.doChat(ctx, route, provider, chatCompletionsURL(provider.BaseURL), payload)
}

func (r *Router) doChat(ctx context.Context, route config.ModelTaskRoute, provider config.ModelProviderCredential, endpoint string, payload any) (string, *CallTrace, error) {
	start := time.Now()
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	resp, err := r.http.Do(httpReq)
	trace := &CallTrace{
		Provider:       route.Provider,
		Model:          route.Model,
		Task:           route.Task,
		AdapterVersion: firstNonEmpty(r.runtime.ModelAdapterVersion, config.ModelAdapterVersion),
		Mode:           r.Mode(),
		LatencyMS:      int(time.Since(start).Milliseconds()),
	}
	if err != nil {
		return "", traceWithFallback(trace, "http_error"), redactError(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", traceWithFallback(trace, "read_error"), redactError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", traceWithFallback(trace, fmt.Sprintf("http_%d", resp.StatusCode)), redactProviderHTTPError(resp.StatusCode, data)
	}
	content, usage, err := parseChatResponse(data)
	if usage != nil {
		trace.InputTokens = usage.PromptTokens
		trace.OutputTokens = usage.CompletionTokens
	}
	if err != nil {
		return "", traceWithFallback(trace, "response_parse_failed"), err
	}
	return content, trace, nil
}

type ErrDeterministicRequired struct {
	Reason string
}

func (e ErrDeterministicRequired) Error() string {
	return "deterministic fallback required: " + e.Reason
}

func IsDeterministicFallback(err error) bool {
	var fallback ErrDeterministicRequired
	return errors.As(err, &fallback)
}

func traceWithFallback(trace *CallTrace, reason string) *CallTrace {
	if trace == nil {
		return nil
	}
	copy := *trace
	copy.FallbackReason = reason
	return &copy
}

func appendJSONInstruction(user string, schemaName string, hint string) string {
	var builder strings.Builder
	builder.WriteString(user)
	builder.WriteString("\n\n请只返回一个合法 JSON 对象，不要使用 Markdown 代码块，不要输出额外解释。")
	if schemaName != "" {
		builder.WriteString("\nJSON schema name: ")
		builder.WriteString(schemaName)
	}
	if hint != "" {
		builder.WriteString("\n字段要求：")
		builder.WriteString(hint)
	}
	return builder.String()
}

func DecodeJSONContent(content string, target any) error {
	if target == nil {
		return nil
	}
	jsonText := extractJSONObject(content)
	decoder := json.NewDecoder(strings.NewReader(jsonText))
	return decoder.Decode(target)
}

func extractJSONObject(content string) string {
	trimmed := strings.TrimSpace(content)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		return trimmed[start : end+1]
	}
	return trimmed
}

func chatCompletionsURL(base string) string {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return strings.TrimRight(base, "/") + "/chat/completions"
	}
	if strings.HasSuffix(parsed.Path, "/chat/completions") {
		return parsed.String()
	}
	parsed.Path = path.Join(parsed.Path, "chat/completions")
	return parsed.String()
}

func parseChatResponse(data []byte) (string, *openAIUsage, error) {
	var response openAIChatResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return "", nil, err
	}
	if len(response.Choices) == 0 {
		return "", &response.Usage, errors.New("chat response has no choices")
	}
	content := response.Choices[0].Message.Content
	if content == "" {
		return "", &response.Usage, errors.New("chat response content is empty")
	}
	return content, &response.Usage, nil
}

func redactProviderHTTPError(status int, data []byte) error {
	message := strings.TrimSpace(string(data))
	if len(message) > 600 {
		message = message[:600]
	}
	return fmt.Errorf("provider HTTP %d: %s", status, redactSensitive(message))
}

func redactError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(redactSensitive(err.Error()))
}

var secretLikePattern = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_\-]{8,}|Bearer\s+[A-Za-z0-9._\-]+|api[_-]?key["'\s:=]+[A-Za-z0-9._\-]+|authorization["'\s:=]+[A-Za-z0-9._\-]+)`)

func redactSensitive(value string) string {
	return secretLikePattern.ReplaceAllString(value, "[redacted]")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
}

type minimaxChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type openAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openAIImageURL `json:"image_url,omitempty"`
}

type openAIImageURL struct {
	URL string `json:"url"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
