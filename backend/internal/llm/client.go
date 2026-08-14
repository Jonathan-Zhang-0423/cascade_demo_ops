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
	"sort"
	"strings"
	"sync"
	"time"

	"cascade-demoops/backend/internal/config"
)

const DefaultTimeout = 45 * time.Second

const (
	errorClassRouteNotConfigured = "route_not_configured"
	errorClassAPIKeyMissing      = "api_key_missing"
	errorClassBaseURLMissing     = "base_url_missing"
	errorClassModelMissing       = "model_missing"
	errorClassHTTPError          = "http_error"
	errorClassTimeout            = "timeout"
	errorClassResponseParse      = "response_parse_failed"
	errorClassJSONParse          = "json_parse_failed"
	errorClassContentFiltered    = "content_filtered"
)

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
	JSONMode    bool
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
	Adapter        string               `json:"adapter,omitempty"`
	Mode           config.LLMMode       `json:"mode"`
	FallbackReason string               `json:"fallback_reason,omitempty"`
	ErrorClass     string               `json:"error_class,omitempty"`
	LatencyMS      int                  `json:"latency_ms,omitempty"`
	InputTokens    int                  `json:"input_tokens,omitempty"`
	OutputTokens   int                  `json:"output_tokens,omitempty"`
}

type DiagnosticResult struct {
	Provider       config.ModelProvider `json:"provider"`
	Task           config.ModelTask     `json:"task,omitempty"`
	Model          string               `json:"model"`
	AdapterVersion string               `json:"adapter_version"`
	Mode           config.LLMMode       `json:"mode"`
	BaseURLHost    string               `json:"base_url_host"`
	BaseURLPath    string               `json:"base_url_path"`
	Configured     bool                 `json:"configured"`
	OK             bool                 `json:"ok"`
	HTTPStatus     int                  `json:"http_status,omitempty"`
	ErrorClass     string               `json:"error_class,omitempty"`
	Error          string               `json:"-"`
	LatencyMS      int                  `json:"latency_ms,omitempty"`
	CheckedAt      time.Time            `json:"checked_at"`
}

func (t *CallTrace) Label() string {
	if t == nil {
		return ""
	}
	parts := []string{string(t.Provider), t.Model, t.AdapterVersion}
	if t.Adapter != "" {
		parts = append(parts, "adapter="+t.Adapter)
	}
	if t.FallbackReason != "" {
		parts = append(parts, "fallback="+t.FallbackReason)
	}
	if t.ErrorClass != "" && t.FallbackReason == "" {
		parts = append(parts, "error="+t.ErrorClass)
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
	if t.Adapter != "" {
		metadata["adapter"] = t.Adapter
	}
	if t.FallbackReason != "" {
		metadata["fallback_reason"] = t.FallbackReason
	}
	if t.ErrorClass != "" {
		metadata["error_class"] = t.ErrorClass
	}
	if t.LatencyMS > 0 {
		metadata["latency_ms"] = t.LatencyMS
	}
	return metadata
}

type Router struct {
	mu          sync.RWMutex
	runtime     config.AppRuntimeConfig
	http        *http.Client
	policy      CallPolicy
	diagnostics map[string]DiagnosticResult
}

func (r *Router) UpdateRuntime(runtime config.AppRuntimeConfig) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.runtime = runtime
	r.http = httpClientForRuntime(runtime, r.policy.Timeout)
	r.diagnostics = map[string]DiagnosticResult{}
	r.mu.Unlock()
}

func (r *Router) httpClientSnapshot() *http.Client {
	if r == nil {
		return http.DefaultClient
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.http
}

func (r *Router) runtimeSnapshot() config.AppRuntimeConfig {
	if r == nil {
		return config.AppRuntimeConfig{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.runtime
}

type CallPolicy struct {
	Timeout              time.Duration
	TransientStatusCodes map[int]bool
	TransientSubstrings  []string
}

func DefaultCallPolicy() CallPolicy {
	return CallPolicy{
		Timeout: DefaultTimeout,
		TransientStatusCodes: map[int]bool{
			http.StatusTooManyRequests:    true,
			http.StatusServiceUnavailable: true,
		},
		TransientSubstrings: []string{
			"rate limit",
			"ratelimit",
			"overload",
			"overloaded",
			"quota",
			"too many requests",
			"temporarily unavailable",
			"timeout",
			"deadline exceeded",
			"connection reset",
			"socket hang up",
		},
	}
}

func (p CallPolicy) AllowsFallback(class string, err error) bool {
	// A malformed provider response cannot safely enrich an execution plan. In
	// auto mode, retain the locally generated deterministic plan instead.
	if class == errorClassJSONParse || class == errorClassResponseParse {
		return true
	}
	if class == errorClassContentFiltered {
		return true
	}
	if class == errorClassTimeout || class == "http_429" || class == "http_503" {
		return true
	}
	status := httpStatusFromFallback(class)
	if status > 0 && p.TransientStatusCodes[status] {
		return true
	}
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, token := range p.TransientSubstrings {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}

type ProviderAdapter interface {
	Name() string
	BuildTextPayload(route config.ModelTaskRoute, req TextRequest) any
	BuildMultimodalPayload(route config.ModelTaskRoute, req MultimodalRequest) any
	Endpoint(provider config.ModelProviderCredential) string
	ParseResponse(data []byte) (string, *openAIUsage, error)
}

type openAICompatibleAdapter struct {
	name     string
	provider config.ModelProvider
}

func newProviderAdapter(provider config.ModelProvider) ProviderAdapter {
	switch provider {
	case config.ModelProviderMinimax:
		return minimaxAdapter{openAICompatibleAdapter{name: "minimax-openai-compatible", provider: provider}}
	case config.ModelProviderGLM:
		return openAICompatibleAdapter{name: "glm-openai-compatible", provider: provider}
	case config.ModelProviderKimi:
		return openAICompatibleAdapter{name: "kimi-openai-compatible", provider: provider}
	case config.ModelProviderDeepSeek:
		return openAICompatibleAdapter{name: "deepseek-openai-compatible", provider: provider}
	case config.ModelProviderDoubao:
		return openAICompatibleAdapter{name: "doubao-ark-openai-compatible", provider: provider}
	case config.ModelProviderSeedance:
		return openAICompatibleAdapter{name: "seedance-ark-openai-compatible", provider: provider}
	default:
		return openAICompatibleAdapter{name: "openai-compatible", provider: provider}
	}
}

func (a openAICompatibleAdapter) Name() string {
	return a.name
}

func (a openAICompatibleAdapter) Endpoint(provider config.ModelProviderCredential) string {
	return chatCompletionsURL(provider.BaseURL)
}

func (a openAICompatibleAdapter) BuildTextPayload(route config.ModelTaskRoute, req TextRequest) any {
	payload := openAIChatRequest{
		Model:       route.Model,
		Messages:    []openAIMessage{{Role: "system", Content: req.System}, {Role: "user", Content: req.User}},
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	if req.JSONMode {
		payload.ResponseFormat = &openAIResponseFormat{Type: "json_object"}
	}
	applyProviderRequestOptions(a.provider, route.Model, &payload)
	return payload
}

func (a openAICompatibleAdapter) BuildMultimodalPayload(route config.ModelTaskRoute, req MultimodalRequest) any {
	content := []openAIContentPart{{Type: "text", Text: appendJSONInstruction(req.User, req.SchemaName, "")}}
	for _, image := range req.Images {
		ref := firstNonEmpty(image.DataURI, image.URL)
		if ref == "" {
			continue
		}
		content = append(content, openAIContentPart{Type: "image_url", ImageURL: &openAIImageURL{URL: ref}})
	}
	payload := openAIChatRequest{
		Model:          route.Model,
		Messages:       []openAIMessage{{Role: "system", Content: req.System}, {Role: "user", Content: content}},
		Temperature:    req.Temperature,
		MaxTokens:      req.MaxTokens,
		ResponseFormat: &openAIResponseFormat{Type: "json_object"},
	}
	applyProviderRequestOptions(a.provider, route.Model, &payload)
	return payload
}

func (a openAICompatibleAdapter) ParseResponse(data []byte) (string, *openAIUsage, error) {
	return parseChatResponse(data)
}

type minimaxAdapter struct {
	openAICompatibleAdapter
}

func (a minimaxAdapter) BuildTextPayload(route config.ModelTaskRoute, req TextRequest) any {
	return minimaxChatRequest{
		Model: route.Model,
		Messages: []openAIMessage{
			{Role: "system", Content: req.System},
			{Role: "user", Content: req.User},
		},
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		ExtraBody:   map[string]any{"reasoning_split": true},
	}
}

func (a minimaxAdapter) BuildMultimodalPayload(route config.ModelTaskRoute, req MultimodalRequest) any {
	content := []openAIContentPart{{Type: "text", Text: appendJSONInstruction(req.User, req.SchemaName, "")}}
	for _, image := range req.Images {
		ref := firstNonEmpty(image.DataURI, image.URL)
		if ref == "" {
			continue
		}
		content = append(content, openAIContentPart{Type: "image_url", ImageURL: &openAIImageURL{URL: ref}})
	}
	return minimaxChatRequest{
		Model:       route.Model,
		Messages:    []openAIMessage{{Role: "system", Content: req.System}, {Role: "user", Content: content}},
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		ExtraBody:   map[string]any{"reasoning_split": true},
	}
}

func NewRouter(runtime config.AppRuntimeConfig) *Router {
	policy := DefaultCallPolicy()
	return &Router{runtime: runtime, http: httpClientForRuntime(runtime, policy.Timeout), policy: policy, diagnostics: map[string]DiagnosticResult{}}
}

func httpClientForRuntime(runtime config.AppRuntimeConfig, timeout time.Duration) *http.Client {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	}
	if proxyURL := strings.TrimSpace(runtime.LLMProxyURL); proxyURL != "" {
		if parsed, err := url.Parse(proxyURL); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "socks5") && parsed.Host != "" {
			transport.Proxy = http.ProxyURL(parsed)
		}
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

func (r *Router) DiagnoseProviders(ctx context.Context) []DiagnosticResult {
	if r == nil {
		return nil
	}
	runtime := r.runtimeSnapshot()
	tasks := make([]config.ModelTask, 0, len(runtime.ModelTaskRoutes))
	for task := range runtime.ModelTaskRoutes {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i] < tasks[j] })
	results := make([]DiagnosticResult, 0, len(tasks))
	seen := map[string]bool{}
	for _, task := range tasks {
		route := runtime.ModelTaskRoutes[task]
		key := string(route.Provider) + "|" + route.Model
		if seen[key] {
			continue
		}
		seen[key] = true
		results = append(results, r.DiagnoseTask(ctx, task))
	}
	return results
}

func (r *Router) DiagnoseTask(ctx context.Context, task config.ModelTask) DiagnosticResult {
	now := time.Now().UTC()
	if r == nil {
		return DiagnosticResult{
			Task:       task,
			Mode:       config.LLMModeAuto,
			Configured: false,
			OK:         false,
			ErrorClass: "router_missing",
			Error:      "LLM router is not configured",
			CheckedAt:  now,
		}
	}
	route, provider, trace, err := r.resolve(task)
	result := diagnosticResultForRoute(r.runtimeSnapshot(), route, provider, task, trace, now)
	if err != nil {
		result.ErrorClass = fallbackReason(trace, "route_error")
		r.rememberDiagnostic(result)
		return result
	}
	if route.Provider == config.ModelProviderSeedance {
		result = r.diagnoseSeedance(ctx, route, provider, result)
		r.rememberDiagnostic(result)
		return result
	}
	text, callTrace, err := r.callText(ctx, route, provider, TextRequest{
		System:      "You are a provider diagnostic probe. Return only OK.",
		User:        "Return OK. Do not include secrets or explanations.",
		MaxTokens:   256,
		Temperature: 0,
	})
	if callTrace != nil {
		result.LatencyMS = callTrace.LatencyMS
	}
	if err != nil {
		result.ErrorClass = fallbackReason(callTrace, "provider_error")
		result.HTTPStatus = httpStatusFromFallback(result.ErrorClass)
		r.rememberDiagnostic(result)
		return result
	}
	if strings.TrimSpace(text) == "" {
		result.ErrorClass = "empty_response"
		r.rememberDiagnostic(result)
		return result
	}
	result.OK = true
	r.rememberDiagnostic(result)
	return result
}

func (r *Router) rememberDiagnostic(result DiagnosticResult) {
	if r == nil || result.Provider == "" || result.Model == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.diagnostics == nil {
		r.diagnostics = map[string]DiagnosticResult{}
	}
	r.diagnostics[diagnosticKey(result.Provider, result.Model)] = result
}

// Seedance is an asynchronous media API, not a chat-completions model. A
// missing-task lookup verifies authentication without creating a paid task.
func (r *Router) diagnoseSeedance(ctx context.Context, route config.ModelTaskRoute, provider config.ModelProviderCredential, result DiagnosticResult) DiagnosticResult {
	endpoint := strings.TrimRight(provider.BaseURL, "/") + "/contents/generations/tasks/cascade-diagnostic-probe-nonexistent"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		result.ErrorClass = errorClassHTTPError
		return result
	}
	request.Header.Set("Authorization", "Bearer "+provider.APIKey)
	started := time.Now()
	response, err := r.http.Do(request)
	result.LatencyMS = int(time.Since(started).Milliseconds())
	if err != nil {
		result.ErrorClass = errorClassHTTPError
		return result
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if readErr != nil {
		result.ErrorClass = "read_error"
		return result
	}
	result.HTTPStatus = response.StatusCode
	if response.StatusCode == http.StatusOK {
		result.OK = true
		return result
	}
	if response.StatusCode == http.StatusNotFound && seedanceDiagnosticTaskNotFound(data) {
		result.OK = true
		return result
	}
	result.ErrorClass = fmt.Sprintf("http_%d", response.StatusCode)
	return result
}

func seedanceDiagnosticTaskNotFound(data []byte) bool {
	var body struct {
		Code  string `json:"code"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return false
	}
	code := strings.TrimSpace(body.Code)
	if body.Error != nil && strings.TrimSpace(body.Error.Code) != "" {
		code = strings.TrimSpace(body.Error.Code)
	}
	return strings.EqualFold(code, "ResourceNotFound")
}

func (r *Router) Mode() config.LLMMode {
	runtime := r.runtimeSnapshot()
	if runtime.LLMMode == "" {
		return config.LLMModeAuto
	}
	return runtime.LLMMode
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
		JSONMode:    true,
	})
	if callTrace != nil {
		trace = callTrace
	}
	if err != nil {
		class := fallbackReason(trace, "provider_error")
		if fallbackText, fallbackTrace, ok := r.tryHealthyPlanningFallback(ctx, task, route, class, TextRequest{System: req.System, User: req.User, MaxTokens: req.MaxTokens, Temperature: req.Temperature, JSONMode: true}); ok {
			if decodeErr := DecodeJSONContent(fallbackText, target); decodeErr == nil {
				return fallbackTrace, nil
			}
		}
		if !r.shouldFallback(class, err) {
			return trace, err
		}
		return traceWithFallback(trace, class), ErrDeterministicRequired{Reason: class}
	}
	if err := DecodeJSONContent(text, target); err != nil {
		if fallbackText, fallbackTrace, ok := r.tryHealthyPlanningFallback(ctx, task, route, errorClassJSONParse, TextRequest{System: req.System, User: req.User, MaxTokens: req.MaxTokens, Temperature: req.Temperature, JSONMode: true}); ok {
			if decodeErr := DecodeJSONContent(fallbackText, target); decodeErr == nil {
				return fallbackTrace, nil
			}
		}
		if !r.shouldFallback(errorClassJSONParse, err) {
			return trace, fmt.Errorf("llm JSON parse failed: %w", err)
		}
		return traceWithFallback(trace, errorClassJSONParse), ErrDeterministicRequired{Reason: errorClassJSONParse}
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
		class := fallbackReason(trace, "provider_error")
		if fallbackText, fallbackTrace, ok := r.tryHealthyPlanningFallback(ctx, task, route, class, req); ok {
			return fallbackText, fallbackTrace, nil
		}
		if !r.shouldFallback(class, err) {
			return "", trace, err
		}
		return "", traceWithFallback(trace, class), ErrDeterministicRequired{Reason: class}
	}
	return text, trace, nil
}

func (r *Router) tryHealthyPlanningFallback(ctx context.Context, task config.ModelTask, failedRoute config.ModelTaskRoute, class string, req TextRequest) (string, *CallTrace, bool) {
	if r == nil || task != config.ModelTaskPlanning || !r.policy.AllowsFallback(class, errors.New(class)) || class == "http_401" {
		return "", nil, false
	}
	r.diagnosePlanningFallbackCandidates(ctx, failedRoute.Provider)
	runtime := r.runtimeSnapshot()
	type candidate struct {
		route    config.ModelTaskRoute
		provider config.ModelProviderCredential
	}
	candidates := []candidate{}
	r.mu.RLock()
	for providerID, provider := range runtime.ModelProviders {
		if providerID == failedRoute.Provider || !provider.Enabled || strings.TrimSpace(provider.APIKey) == "" || strings.TrimSpace(provider.BaseURL) == "" {
			continue
		}
		modelName := strings.TrimSpace(provider.DefaultModel)
		if modelName == "" || !r.diagnostics[diagnosticKey(providerID, modelName)].OK {
			continue
		}
		candidates = append(candidates, candidate{route: config.ModelTaskRoute{Task: task, Provider: providerID, Model: modelName}, provider: provider})
	}
	r.mu.RUnlock()
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].route.Provider < candidates[j].route.Provider })
	for _, item := range candidates {
		text, trace, err := r.callText(ctx, item.route, item.provider, req)
		if err != nil {
			continue
		}
		copy := *trace
		copy.FallbackReason = "planning_provider_fallback_from_" + string(failedRoute.Provider) + "_" + class
		copy.ErrorClass = ""
		return text, &copy, true
	}
	return "", nil, false
}

func (r *Router) diagnosePlanningFallbackCandidates(ctx context.Context, failedProvider config.ModelProvider) {
	runtime := r.runtimeSnapshot()
	type candidate struct {
		id       config.ModelProvider
		provider config.ModelProviderCredential
		routed   bool
	}
	candidates := []candidate{}
	for providerID, provider := range runtime.ModelProviders {
		if providerID == failedProvider || !provider.Enabled || strings.TrimSpace(provider.APIKey) == "" || strings.TrimSpace(provider.BaseURL) == "" || strings.TrimSpace(provider.DefaultModel) == "" {
			continue
		}
		if providerID == config.ModelProviderSeedance || providerID == config.ModelProviderSeedream {
			continue
		}
		routed := false
		for _, route := range runtime.ModelTaskRoutes {
			routed = routed || route.Provider == providerID
		}
		candidates = append(candidates, candidate{id: providerID, provider: provider, routed: routed})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].routed != candidates[j].routed {
			return candidates[i].routed
		}
		return candidates[i].id < candidates[j].id
	})
	for _, item := range candidates {
		providerID, provider := item.id, item.provider
		key := diagnosticKey(providerID, provider.DefaultModel)
		r.mu.RLock()
		existing, alreadyChecked := r.diagnostics[key]
		r.mu.RUnlock()
		if alreadyChecked {
			if existing.OK {
				return
			}
			continue
		}
		diagnosticCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		started := time.Now()
		_, trace, err := r.callText(diagnosticCtx, config.ModelTaskRoute{Task: config.ModelTaskPlanning, Provider: providerID, Model: provider.DefaultModel}, provider, TextRequest{
			System: "You are a provider diagnostic probe. Return only OK.", User: "Return OK.", MaxTokens: 32,
		})
		cancel()
		result := diagnosticResultForRoute(runtime, config.ModelTaskRoute{Task: config.ModelTaskPlanning, Provider: providerID, Model: provider.DefaultModel}, provider, config.ModelTaskPlanning, trace, started.UTC())
		result.LatencyMS = int(time.Since(started).Milliseconds())
		result.OK = err == nil
		if err != nil {
			result.ErrorClass = fallbackReason(trace, "provider_error")
			result.HTTPStatus = httpStatusFromFallback(result.ErrorClass)
		}
		r.rememberDiagnostic(result)
		if result.OK {
			return
		}
	}
}

func diagnosticKey(provider config.ModelProvider, model string) string {
	return string(provider) + "|" + strings.TrimSpace(model)
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
		class := fallbackReason(trace, "provider_error")
		if !r.shouldFallback(class, err) {
			return trace, err
		}
		return traceWithFallback(trace, class), ErrDeterministicRequired{Reason: class}
	}
	if err := DecodeJSONContent(text, target); err != nil {
		if !r.shouldFallback(errorClassJSONParse, err) {
			return trace, fmt.Errorf("llm JSON parse failed: %w", err)
		}
		return traceWithFallback(trace, errorClassJSONParse), ErrDeterministicRequired{Reason: errorClassJSONParse}
	}
	return trace, nil
}

func (r *Router) resolve(task config.ModelTask) (config.ModelTaskRoute, config.ModelProviderCredential, *CallTrace, error) {
	runtime := r.runtimeSnapshot()
	mode := runtime.LLMMode
	if mode == "" {
		mode = config.LLMModeAuto
	}
	route := runtime.ModelTaskRoutes[task]
	provider := runtime.ModelProviders[route.Provider]
	modelName := firstNonEmpty(route.Model, provider.DefaultModel)
	trace := &CallTrace{
		Provider:       route.Provider,
		Model:          modelName,
		Task:           task,
		AdapterVersion: firstNonEmpty(runtime.ModelAdapterVersion, config.ModelAdapterVersion),
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

func (r *Router) shouldFallback(class string, err error) bool {
	if r == nil {
		return false
	}
	switch r.Mode() {
	case config.LLMModeDeterministic:
		return true
	case config.LLMModeReal:
		return class == errorClassJSONParse
	default:
		return r.policy.AllowsFallback(class, err)
	}
}

func (r *Router) callText(ctx context.Context, route config.ModelTaskRoute, provider config.ModelProviderCredential, req TextRequest) (string, *CallTrace, error) {
	adapter := newProviderAdapter(route.Provider)
	return r.doChat(ctx, route, provider, adapter, adapter.BuildTextPayload(route, req))
}

func (r *Router) callMultimodal(ctx context.Context, route config.ModelTaskRoute, provider config.ModelProviderCredential, req MultimodalRequest) (string, *CallTrace, error) {
	adapter := newProviderAdapter(route.Provider)
	return r.doChat(ctx, route, provider, adapter, adapter.BuildMultimodalPayload(route, req))
}

func (r *Router) doChat(ctx context.Context, route config.ModelTaskRoute, provider config.ModelProviderCredential, adapter ProviderAdapter, payload any) (string, *CallTrace, error) {
	start := time.Now()
	runtime := r.runtimeSnapshot()
	mode := runtime.LLMMode
	if mode == "" {
		mode = config.LLMModeAuto
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, err
	}
	endpoint := adapter.Endpoint(provider)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+provider.APIKey)

	resp, err := r.httpClientSnapshot().Do(httpReq)
	trace := &CallTrace{
		Provider:       route.Provider,
		Model:          route.Model,
		Task:           route.Task,
		AdapterVersion: firstNonEmpty(runtime.ModelAdapterVersion, config.ModelAdapterVersion),
		Adapter:        adapter.Name(),
		Mode:           mode,
		LatencyMS:      int(time.Since(start).Milliseconds()),
	}
	if err != nil {
		class := errorClassHTTPError
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
			class = errorClassTimeout
		}
		return "", traceWithError(trace, class), redactError(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", traceWithError(trace, "read_error"), redactError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		class := fmt.Sprintf("http_%d", resp.StatusCode)
		if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden && providerResponseWasContentFiltered(data) {
			class = errorClassContentFiltered
		}
		return "", traceWithError(trace, class), redactProviderHTTPError(resp.StatusCode, data)
	}
	content, usage, err := adapter.ParseResponse(data)
	if usage != nil {
		trace.InputTokens = usage.PromptTokens
		trace.OutputTokens = usage.CompletionTokens
	}
	if err != nil {
		return "", traceWithError(trace, errorClassResponseParse), err
	}
	return content, trace, nil
}

func providerResponseWasContentFiltered(data []byte) bool {
	lower := strings.ToLower(string(data))
	for _, marker := range []string{"content_filter", "content filter", "content filtered", "sensitive content", `"code":"1301"`, `"code":1301`} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func diagnosticResultForRoute(runtime config.AppRuntimeConfig, route config.ModelTaskRoute, provider config.ModelProviderCredential, task config.ModelTask, trace *CallTrace, checkedAt time.Time) DiagnosticResult {
	modelName := firstNonEmpty(route.Model, provider.DefaultModel)
	if trace != nil && trace.Model != "" {
		modelName = trace.Model
	}
	mode := runtime.LLMMode
	if mode == "" {
		mode = config.LLMModeAuto
	}
	host, pathValue := safeURLParts(provider.BaseURL)
	return DiagnosticResult{
		Provider:       route.Provider,
		Task:           task,
		Model:          modelName,
		AdapterVersion: firstNonEmpty(runtime.ModelAdapterVersion, config.ModelAdapterVersion),
		Mode:           mode,
		BaseURLHost:    host,
		BaseURLPath:    pathValue,
		Configured:     strings.TrimSpace(provider.APIKey) != "" && strings.TrimSpace(provider.BaseURL) != "" && strings.TrimSpace(modelName) != "",
		OK:             false,
		CheckedAt:      checkedAt,
	}
}

func fallbackReason(trace *CallTrace, fallback string) string {
	if trace != nil && trace.FallbackReason != "" {
		return trace.FallbackReason
	}
	if trace != nil && trace.ErrorClass != "" {
		return trace.ErrorClass
	}
	return fallback
}

func httpStatusFromFallback(reason string) int {
	if !strings.HasPrefix(reason, "http_") {
		return 0
	}
	var status int
	if _, err := fmt.Sscanf(reason, "http_%d", &status); err != nil {
		return 0
	}
	return status
}

func safeURLParts(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", ""
	}
	return parsed.Host, parsed.EscapedPath()
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
	copy.ErrorClass = reason
	return &copy
}

func traceWithError(trace *CallTrace, class string) *CallTrace {
	if trace == nil {
		return nil
	}
	copy := *trace
	copy.ErrorClass = class
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
	jsonText, err := extractJSONValue(content)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(jsonText))
	return decoder.Decode(target)
}

func extractJSONValue(content string) (string, error) {
	trimmed := strings.TrimSpace(content)
	for start := 0; start < len(trimmed); start++ {
		if trimmed[start] != '{' && trimmed[start] != '[' {
			continue
		}
		if end, ok := balancedJSONEnd(trimmed, start); ok {
			candidate := trimmed[start:end]
			if json.Valid([]byte(candidate)) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("no valid JSON object found in model response: %s", redactSensitive(responseSnippet(trimmed)))
}

func balancedJSONEnd(value string, start int) (int, bool) {
	if start < 0 || start >= len(value) {
		return 0, false
	}
	stack := []byte{value[start]}
	inString := false
	escaped := false
	for i := start + 1; i < len(value); i++ {
		ch := value[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, ch)
		case '}', ']':
			if len(stack) == 0 || !matchingJSONBracket(stack[len(stack)-1], ch) {
				return 0, false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

func matchingJSONBracket(open byte, close byte) bool {
	return (open == '{' && close == '}') || (open == '[' && close == ']')
}

func responseSnippet(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "(empty response)"
	}
	if len(value) > 240 {
		return value[:240] + "..."
	}
	return value
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
	message := response.Choices[0].Message
	content := message.Content
	if content == "" {
		content = message.ReasoningContent
	}
	if content == "" {
		for _, detail := range message.ReasoningDetails {
			if strings.TrimSpace(detail.Text) != "" {
				content = detail.Text
				break
			}
		}
	}
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
	Model          string                `json:"model"`
	Messages       []openAIMessage       `json:"messages"`
	Temperature    float64               `json:"temperature,omitempty"`
	MaxTokens      int                   `json:"max_tokens,omitempty"`
	Thinking       *openAIThinking       `json:"thinking,omitempty"`
	ResponseFormat *openAIResponseFormat `json:"response_format,omitempty"`
}

type openAIThinking struct {
	Type string `json:"type"`
}

type openAIResponseFormat struct {
	Type string `json:"type"`
}

func applyProviderRequestOptions(provider config.ModelProvider, modelName string, payload *openAIChatRequest) {
	if payload == nil {
		return
	}
	if provider == config.ModelProviderGLM {
		payload.Thinking = &openAIThinking{Type: "disabled"}
	}
	if provider == config.ModelProviderKimi {
		model := strings.ToLower(strings.TrimSpace(modelName))
		if strings.HasPrefix(model, "kimi-k2.7-code") || strings.HasPrefix(model, "kimi-k3") {
			payload.Temperature = 1
		}
	}
}

type minimaxChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	ExtraBody   map[string]any  `json:"extra_body,omitempty"`
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
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ReasoningDetails []struct {
				Text string `json:"text"`
			} `json:"reasoning_details"`
		} `json:"message"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
