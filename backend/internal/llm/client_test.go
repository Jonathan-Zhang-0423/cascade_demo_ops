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
	if gotPath != "/chat/completions" || gotAuth != "Bearer test-api-key" || gotModel != "kimi-2.5" {
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		gotModel, _ = payload["model"].(string)
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
	if gotModel != "minimax-m3" || out.Summary == "" {
		t.Fatalf("unexpected minimax result: model=%s out=%+v", gotModel, out)
	}
}

func TestRouterAutoFallsBackWithoutKey(t *testing.T) {
	runtime := testRuntime(config.ModelProviderKimi, "https://example.invalid/v1")
	runtime.ModelProviders[config.ModelProviderKimi] = config.ModelProviderCredential{
		Provider:     config.ModelProviderKimi,
		APIKeyEnv:    "KIMI_API_KEY",
		BaseURL:      "https://example.invalid/v1",
		DefaultModel: "kimi-2.5",
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

func TestRouterRealModeFailsWithoutKey(t *testing.T) {
	runtime := testRuntime(config.ModelProviderKimi, "https://example.invalid/v1")
	runtime.LLMMode = config.LLMModeReal
	runtime.ModelProviders[config.ModelProviderKimi] = config.ModelProviderCredential{
		Provider:     config.ModelProviderKimi,
		APIKeyEnv:    "KIMI_API_KEY",
		BaseURL:      "https://example.invalid/v1",
		DefaultModel: "kimi-2.5",
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
	if !IsDeterministicFallback(err) {
		t.Fatalf("expected fallback error, got %v", err)
	}
	if strings.Contains(err.Error(), "sk-secret-value") {
		t.Fatalf("error leaked key: %v", err)
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
	default:
		return "kimi-2.5"
	}
}
