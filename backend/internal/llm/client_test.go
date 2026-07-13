package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
)

func TestRouterGenerateJSONOpenAICompatibleRequest(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		gotModel, _ = payload["model"].(string)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"真实模型摘要\"}"}}],"usage":{"prompt_tokens":12,"completion_tokens":6}}`))
	}))
	defer server.Close()

	router := NewRouter(testRuntime(config.ModelProviderKimi, server.URL))
	var out struct {
		Summary string `json:"summary"`
	}
	trace, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{
		System:     "system",
		User:       "user",
		SchemaName: "TestSchema",
		MaxTokens:  100,
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/chat/completions" || gotAuth != "Bearer test-api-key" || gotModel != "kimi-k2.7-code" {
		t.Fatalf("unexpected request path/auth/model: path=%s auth=%s model=%s", gotPath, gotAuth, gotModel)
	}
	if out.Summary != "真实模型摘要" {
		t.Fatalf("summary = %q", out.Summary)
	}
	if trace.Provider != config.ModelProviderKimi || trace.OutputTokens != 6 {
		t.Fatalf("unexpected trace: %+v", trace)
	}
}

func TestRouterMiniMaxUsesIndependentAdapterShape(t *testing.T) {
	var gotModel string
	var gotExtraBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		gotModel, _ = payload["model"].(string)
		gotExtraBody, _ = payload["extra_body"].(map[string]any)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"MiniMax 多模态摘要\"}"}}]}`))
	}))
	defer server.Close()

	runtime := testRuntime(config.ModelProviderMinimax, server.URL)
	runtime.ModelTaskRoutes[config.ModelTaskMultimodalUnderstanding] = config.ModelTaskRoute{
		Task:     config.ModelTaskMultimodalUnderstanding,
		Provider: config.ModelProviderMinimax,
		Model:    "minimax-m3",
	}
	router := NewRouter(runtime)
	var out struct {
		Summary string `json:"summary"`
	}
	_, err := router.GenerateMultimodal(context.Background(), config.ModelTaskMultimodalUnderstanding, MultimodalRequest{
		System:     "system",
		User:       "user",
		SchemaName: "VisionSchema",
		Images:     []ImageInput{{URL: "https://example.com/screenshot.png"}},
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if gotModel != "minimax-m3" || out.Summary == "" || gotExtraBody["reasoning_split"] != true {
		t.Fatalf("unexpected minimax result: model=%s extra=%+v out=%+v", gotModel, gotExtraBody, out)
	}
}

func TestRouterGLMDisablesThinkingForStructuredOutput(t *testing.T) {
	var gotThinking map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		gotThinking, _ = payload["thinking"].(map[string]any)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"GLM 摘要\"}"}}]}`))
	}))
	defer server.Close()

	runtime := testRuntime(config.ModelProviderGLM, server.URL)
	runtime.ModelTaskRoutes[config.ModelTaskCodeReading] = config.ModelTaskRoute{
		Task:     config.ModelTaskCodeReading,
		Provider: config.ModelProviderGLM,
		Model:    "glm-5.2",
	}
	router := NewRouter(runtime)
	var out struct {
		Summary string `json:"summary"`
	}
	_, err := router.GenerateJSON(context.Background(), config.ModelTaskCodeReading, JSONRequest{System: "s", User: "u"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if gotThinking["type"] != "disabled" {
		t.Fatalf("expected GLM thinking disabled, got %+v", gotThinking)
	}
}

func TestDecodeJSONContentIgnoresTrailingModelText(t *testing.T) {
	var out struct {
		Summary string `json:"summary"`
	}
	err := DecodeJSONContent("```json\n{\"summary\":\"OK\"}\n```\n额外中文说明 å 不应进入 JSON 解析", &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary != "OK" {
		t.Fatalf("unexpected decoded output: %+v", out)
	}
}

func TestDecodeJSONContentKeepsBracesInsideStrings(t *testing.T) {
	var out struct {
		Markdown string `json:"markdown"`
	}
	err := DecodeJSONContent("{\"markdown\":\"包含 { 大括号 } 和 \\\"引号\\\"\"}\n后续说明", &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Markdown, "{ 大括号 }") {
		t.Fatalf("unexpected markdown: %q", out.Markdown)
	}
}

func TestRouterAutoFallsBackWithoutKey(t *testing.T) {
	runtime := testRuntime(config.ModelProviderKimi, "https://example.invalid/v1")
	runtime.ModelProviders[config.ModelProviderKimi] = config.ModelProviderCredential{
		Provider:     config.ModelProviderKimi,
		APIKeyEnv:    "KIMI_API_KEY",
		BaseURL:      "https://example.invalid/v1",
		DefaultModel: "kimi-k2.7-code",
	}
	router := NewRouter(runtime)
	var out struct {
		Summary string `json:"summary"`
	}
	trace, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &out)
	if !IsDeterministicFallback(err) {
		t.Fatalf("expected deterministic fallback, got trace=%+v err=%v", trace, err)
	}
	if trace == nil || trace.FallbackReason == "" {
		t.Fatalf("expected fallback trace, got %+v", trace)
	}
}

func TestRouterAutoDoesNotFallbackOnUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid authentication"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	router := NewRouter(testRuntime(config.ModelProviderKimi, server.URL))
	var out struct {
		Summary string `json:"summary"`
	}
	trace, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &out)
	if err == nil || IsDeterministicFallback(err) {
		t.Fatalf("expected hard unauthorized error, got trace=%+v err=%v", trace, err)
	}
	if trace == nil || trace.ErrorClass != "http_401" || trace.FallbackReason != "" {
		t.Fatalf("expected http_401 without fallback, got %+v", trace)
	}
}

func TestRouterAutoFallbacksOnRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"rate limit"}`, http.StatusTooManyRequests)
	}))
	defer server.Close()

	router := NewRouter(testRuntime(config.ModelProviderKimi, server.URL))
	var out struct {
		Summary string `json:"summary"`
	}
	trace, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &out)
	if !IsDeterministicFallback(err) {
		t.Fatalf("expected fallback on 429, got trace=%+v err=%v", trace, err)
	}
	if trace == nil || trace.FallbackReason != "http_429" {
		t.Fatalf("expected http_429 fallback trace, got %+v", trace)
	}
}

func TestRouterAutoDoesNotFallbackOnJSONParseFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not-json"}}]}`))
	}))
	defer server.Close()

	router := NewRouter(testRuntime(config.ModelProviderKimi, server.URL))
	var out struct {
		Summary string `json:"summary"`
	}
	trace, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &out)
	if err == nil || IsDeterministicFallback(err) {
		t.Fatalf("expected JSON parse hard error, got trace=%+v err=%v", trace, err)
	}
}

func TestParseChatResponseCanReadReasoningFields(t *testing.T) {
	content, _, err := parseChatResponse([]byte(`{"choices":[{"message":{"content":"","reasoning_content":"OK"}}]}`))
	if err != nil || content != "OK" {
		t.Fatalf("expected reasoning content fallback, content=%q err=%v", content, err)
	}
	content, _, err = parseChatResponse([]byte(`{"choices":[{"message":{"reasoning_details":[{"text":"MiniMax reasoning"}]}}]}`))
	if err != nil || content != "MiniMax reasoning" {
		t.Fatalf("expected reasoning detail fallback, content=%q err=%v", content, err)
	}
}

func TestRouterRealModeFailsWithoutKey(t *testing.T) {
	runtime := testRuntime(config.ModelProviderKimi, "https://example.invalid/v1")
	runtime.LLMMode = config.LLMModeReal
	runtime.ModelProviders[config.ModelProviderKimi] = config.ModelProviderCredential{
		Provider:     config.ModelProviderKimi,
		APIKeyEnv:    "KIMI_API_KEY",
		BaseURL:      "https://example.invalid/v1",
		DefaultModel: "kimi-k2.7-code",
	}
	router := NewRouter(runtime)
	var out struct {
		Summary string `json:"summary"`
	}
	_, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &out)
	if err == nil || IsDeterministicFallback(err) {
		t.Fatalf("expected real mode hard failure, got %v", err)
	}
}

func TestProviderErrorsAreRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad key sk-secret-value Authorization bearer-secret"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	router := NewRouter(testRuntime(config.ModelProviderKimi, server.URL))
	var out struct {
		Summary string `json:"summary"`
	}
	_, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &out)
	if err == nil || IsDeterministicFallback(err) {
		t.Fatalf("expected hard redacted provider error, got %v", err)
	}
	if strings.Contains(err.Error(), "sk-secret-value") {
		t.Fatalf("error leaked key: %v", err)
	}
}

func TestRouterDiagnoseTaskReturnsRedactedProviderStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Authorization"), "sk-") {
			http.Error(w, `{"error":"invalid key sk-secret-value Authorization bearer-secret"}`, http.StatusUnauthorized)
			return
		}
		http.Error(w, `{"error":"unexpected"}`, http.StatusBadRequest)
	}))
	defer server.Close()

	runtime := testRuntime(config.ModelProviderKimi, server.URL+"/v1")
	runtime.ModelProviders[config.ModelProviderKimi] = config.ModelProviderCredential{
		Provider:     config.ModelProviderKimi,
		APIKey:       "sk-secret-value",
		APIKeyEnv:    "KIMI_API_KEY",
		BaseURL:      server.URL + "/v1",
		DefaultModel: "kimi-k2.7-code",
		Enabled:      true,
	}
	router := NewRouter(runtime)

	result := router.DiagnoseTask(context.Background(), config.ModelTaskPlanning)

	if result.OK || result.HTTPStatus != http.StatusUnauthorized || result.ErrorClass != "http_401" {
		t.Fatalf("unexpected diagnostic result: %+v", result)
	}
	if result.BaseURLHost == "" || result.BaseURLPath != "/v1" {
		t.Fatalf("expected safe base URL parts, got %+v", result)
	}
	if strings.Contains(result.Error, "sk-secret-value") || strings.Contains(result.Error, "bearer-secret") {
		t.Fatalf("diagnostic leaked secret: %+v", result)
	}
}

func testRuntime(provider config.ModelProvider, baseURL string) config.AppRuntimeConfig {
	return config.AppRuntimeConfig{
		Profile:             config.ProfileDev,
		Environment:         "test",
		Mode:                model.AppModeDesktop,
		LLMMode:             config.LLMModeAuto,
		ModelAdapterVersion: config.ModelAdapterVersion,
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			provider: {
				Provider:     provider,
				APIKey:       "test-api-key",
				APIKeyEnv:    strings.ToUpper(string(provider)) + "_API_KEY",
				BaseURL:      baseURL,
				DefaultModel: defaultTestModel(provider),
				Enabled:      true,
			},
		},
		ModelTaskRoutes: map[config.ModelTask]config.ModelTaskRoute{
			config.ModelTaskPlanning: {Task: config.ModelTaskPlanning, Provider: provider, Model: defaultTestModel(provider)},
		},
	}
}

func defaultTestModel(provider config.ModelProvider) string {
	switch provider {
	case config.ModelProviderMinimax:
		return "minimax-m3"
	case config.ModelProviderGLM:
		return "glm-5.2"
	case config.ModelProviderDeepSeek:
		return "deepseek-v4-flash"
	default:
		return "kimi-k2.7-code"
	}
}
