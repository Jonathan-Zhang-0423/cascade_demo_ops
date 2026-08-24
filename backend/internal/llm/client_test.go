package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
)

func TestRouterUsesConfiguredLLMProxy(t *testing.T) {
	providerReached := false
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerReached = true
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"proxied\"}"}}]}`))
	}))
	defer provider.Close()
	proxyReached := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyReached = true
		target, err := url.Parse(r.RequestURI)
		if err != nil {
			t.Fatal(err)
		}
		r.URL = target
		r.RequestURI = ""
		response, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	defer proxy.Close()
	runtime := testRuntime(config.ModelProviderKimi, provider.URL)
	runtime.LLMProxyURL = proxy.URL
	var output struct {
		Summary string `json:"summary"`
	}
	if _, err := NewRouter(runtime).GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &output); err != nil {
		t.Fatal(err)
	}
	if !proxyReached || !providerReached || output.Summary != "proxied" {
		t.Fatalf("configured model proxy was not used: proxy=%v provider=%v output=%+v", proxyReached, providerReached, output)
	}
}

func TestRouterGenerateJSONOpenAICompatibleRequest(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotModel string
	var gotTemperature float64
	var gotResponseFormat map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		gotModel, _ = payload["model"].(string)
		gotTemperature, _ = payload["temperature"].(float64)
		gotResponseFormat, _ = payload["response_format"].(map[string]any)
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
	if gotResponseFormat["type"] != "json_object" {
		t.Fatalf("expected json_object response_format, got %+v", gotResponseFormat)
	}
	if gotTemperature != 1 {
		t.Fatalf("Kimi code model temperature = %v, want 1", gotTemperature)
	}
	if out.Summary != "真实模型摘要" {
		t.Fatalf("summary = %q", out.Summary)
	}
	if trace.Provider != config.ModelProviderKimi || trace.OutputTokens != 6 {
		t.Fatalf("unexpected trace: %+v", trace)
	}
}

func TestKimiK3ForcesSupportedTemperature(t *testing.T) {
	var gotTemperature float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		gotTemperature, _ = payload["temperature"].(float64)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"ok\"}"}}]}`))
	}))
	defer server.Close()
	runtime := testRuntime(config.ModelProviderKimi, server.URL)
	route := runtime.ModelTaskRoutes[config.ModelTaskPlanning]
	route.Model = "kimi-k3"
	runtime.ModelTaskRoutes[config.ModelTaskPlanning] = route
	var out struct {
		Summary string `json:"summary"`
	}
	if _, err := NewRouter(runtime).GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u", Temperature: 0.1}, &out); err != nil {
		t.Fatal(err)
	}
	if gotTemperature != 1 {
		t.Fatalf("Kimi K3 temperature = %v, want 1", gotTemperature)
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

func TestRouterGenerateMultimodalTextDisablesJSONCoercion(t *testing.T) {
	var responseFormat any
	var userText string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		responseFormat = payload["response_format"]
		messages, _ := payload["messages"].([]any)
		if len(messages) == 2 {
			user, _ := messages[1].(map[string]any)
			content, _ := user["content"].([]any)
			if len(content) > 0 {
				part, _ := content[0].(map[string]any)
				userText, _ = part["text"].(string)
			}
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"DECISION=IN_PROGRESS"}}]}`))
	}))
	defer server.Close()

	runtime := testRuntime(config.ModelProviderGLM, server.URL)
	runtime.ModelTaskRoutes[config.ModelTaskBrowserVisualObservation] = config.ModelTaskRoute{
		Task: config.ModelTaskBrowserVisualObservation, Provider: config.ModelProviderGLM, Model: "glm-4.5v",
	}
	text, _, err := NewRouter(runtime).GenerateMultimodalText(context.Background(), config.ModelTaskBrowserVisualObservation, MultimodalRequest{
		System: "system", User: "LINE_PROTOCOL", Images: []ImageInput{{URL: "https://example.com/screenshot.png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if responseFormat != nil || userText != "LINE_PROTOCOL" || text != "DECISION=IN_PROGRESS" {
		t.Fatalf("text fallback retained JSON coercion: response_format=%+v user=%q text=%q", responseFormat, userText, text)
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

func TestRouterPlanningFallsBackOnlyToDiagnosedHealthyProvider(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":"1301","message":"content filtered"}}`, http.StatusBadRequest)
	}))
	defer primary.Close()
	secondaryCalls := 0
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalls++
		if r.Header.Get("Authorization") != "Bearer secondary-key" {
			t.Fatalf("fallback used the wrong provider credential")
		}
		if secondaryCalls == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"healthy fallback\"}"}}]}`))
	}))
	defer secondary.Close()

	runtime := testRuntime(config.ModelProviderKimi, primary.URL)
	runtime.ModelProviders[config.ModelProviderGLM] = config.ModelProviderCredential{
		Provider: config.ModelProviderGLM, APIKey: "secondary-key", BaseURL: secondary.URL, DefaultModel: "glm-5.2", Enabled: true,
	}
	runtime.ModelTaskRoutes[config.ModelTaskCodeReading] = config.ModelTaskRoute{Task: config.ModelTaskCodeReading, Provider: config.ModelProviderGLM, Model: "glm-5.2"}
	router := NewRouter(runtime)
	if diagnostic := router.DiagnoseTask(context.Background(), config.ModelTaskCodeReading); !diagnostic.OK {
		t.Fatalf("secondary provider diagnostic was not healthy: %+v", diagnostic)
	}
	var out struct {
		Summary string `json:"summary"`
	}
	trace, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary != "healthy fallback" || trace.Provider != config.ModelProviderGLM || !strings.Contains(trace.FallbackReason, "content_filtered") {
		t.Fatalf("planning did not use the diagnosed healthy provider: out=%+v trace=%+v", out, trace)
	}
}

func TestRouterPlanningDiagnosesHealthyFallbackAfterPrimaryTimeout(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer primary.Close()
	secondaryCalls := 0
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalls++
		content := "OK"
		if secondaryCalls > 1 {
			content = `{"summary":"timeout fallback"}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	defer secondary.Close()
	runtime := testRuntime(config.ModelProviderKimi, primary.URL)
	runtime.ModelProviders[config.ModelProviderGLM] = config.ModelProviderCredential{Provider: config.ModelProviderGLM, APIKey: "secondary-key", BaseURL: secondary.URL, DefaultModel: "glm-5.2", Enabled: true}
	runtime.ModelProviders[config.ModelProviderDeepSeek] = config.ModelProviderCredential{Provider: config.ModelProviderDeepSeek, APIKey: "slow-key", BaseURL: primary.URL, DefaultModel: "deepseek-v4-flash", Enabled: true}
	runtime.ModelTaskRoutes[config.ModelTaskCodeReading] = config.ModelTaskRoute{Task: config.ModelTaskCodeReading, Provider: config.ModelProviderGLM, Model: "glm-5.2"}
	router := NewRouter(runtime)
	router.http.Timeout = 40 * time.Millisecond
	var out struct {
		Summary string `json:"summary"`
	}
	trace, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary != "timeout fallback" || trace.Provider != config.ModelProviderGLM || !strings.Contains(trace.FallbackReason, "timeout") || secondaryCalls != 2 {
		t.Fatalf("timeout did not diagnose and use healthy fallback: out=%+v trace=%+v calls=%d", out, trace, secondaryCalls)
	}
}

func TestRouterPlanningNeverHidesUnauthorizedBehindHealthyFallback(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid authentication content_filter"}`, http.StatusUnauthorized)
	}))
	defer primary.Close()
	secondaryCalls := 0
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCalls++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
	}))
	defer secondary.Close()

	runtime := testRuntime(config.ModelProviderKimi, primary.URL)
	runtime.ModelProviders[config.ModelProviderGLM] = config.ModelProviderCredential{Provider: config.ModelProviderGLM, APIKey: "secondary-key", BaseURL: secondary.URL, DefaultModel: "glm-5.2", Enabled: true}
	runtime.ModelTaskRoutes[config.ModelTaskCodeReading] = config.ModelTaskRoute{Task: config.ModelTaskCodeReading, Provider: config.ModelProviderGLM, Model: "glm-5.2"}
	router := NewRouter(runtime)
	if diagnostic := router.DiagnoseTask(context.Background(), config.ModelTaskCodeReading); !diagnostic.OK {
		t.Fatalf("secondary provider diagnostic was not healthy: %+v", diagnostic)
	}
	var out struct {
		Summary string `json:"summary"`
	}
	trace, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &out)
	if err == nil || IsDeterministicFallback(err) || trace == nil || trace.ErrorClass != "http_401" {
		t.Fatalf("401 must remain a credential error: trace=%+v err=%v", trace, err)
	}
	if secondaryCalls != 1 {
		t.Fatalf("healthy secondary was called after 401; calls=%d", secondaryCalls)
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

func TestRouterAutoFallbacksOnJSONParseFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"not-json"}}]}`))
	}))
	defer server.Close()

	router := NewRouter(testRuntime(config.ModelProviderKimi, server.URL))
	var out struct {
		Summary string `json:"summary"`
	}
	trace, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &out)
	if !IsDeterministicFallback(err) {
		t.Fatalf("expected JSON parse fallback, got trace=%+v err=%v", trace, err)
	}
	if trace == nil || trace.FallbackReason != errorClassJSONParse {
		t.Fatalf("expected json_parse fallback trace, got %+v", trace)
	}
}

func TestRouterAutoFallbacksOnProviderResponseParseFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"unexpected":"provider response shape"}`))
	}))
	defer server.Close()

	router := NewRouter(testRuntime(config.ModelProviderKimi, server.URL))
	var out struct {
		Summary string `json:"summary"`
	}
	trace, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &out)
	if !IsDeterministicFallback(err) {
		t.Fatalf("expected provider response parse fallback, got trace=%+v err=%v", trace, err)
	}
	if trace == nil || trace.FallbackReason != errorClassResponseParse {
		t.Fatalf("expected response_parse fallback trace, got %+v", trace)
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

func TestRouterRealModeFallsBackOnJSONParseFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"[{\"summary\":\"array\"}]"}}]}`))
	}))
	defer server.Close()

	runtime := testRuntime(config.ModelProviderKimi, server.URL)
	runtime.LLMMode = config.LLMModeReal
	router := NewRouter(runtime)
	var out struct {
		Summary string `json:"summary"`
	}
	trace, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &out)
	if !IsDeterministicFallback(err) {
		t.Fatalf("expected real mode JSON parse fallback, got trace=%+v err=%v", trace, err)
	}
	if trace == nil || trace.FallbackReason != errorClassJSONParse {
		t.Fatalf("expected json_parse fallback trace, got %+v", trace)
	}
}

func TestRouterRealModeFallsBackOnProviderTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(250 * time.Millisecond):
		}
	}))
	defer server.Close()

	runtime := testRuntime(config.ModelProviderKimi, server.URL)
	runtime.LLMMode = config.LLMModeReal
	router := NewRouter(runtime)
	router.http.Timeout = 25 * time.Millisecond
	var out struct {
		Summary string `json:"summary"`
	}
	trace, err := router.GenerateJSON(context.Background(), config.ModelTaskPlanning, JSONRequest{System: "s", User: "u"}, &out)
	if !IsDeterministicFallback(err) {
		t.Fatalf("expected real mode timeout to retain the deterministic plan, got trace=%+v err=%v", trace, err)
	}
	if trace == nil || trace.FallbackReason != errorClassTimeout || trace.ErrorClass != errorClassTimeout {
		t.Fatalf("expected a redacted timeout fallback trace, got %+v", trace)
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
	serialized, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "provider HTTP") || strings.Contains(string(serialized), "invalid key") || strings.Contains(string(serialized), `"error"`) {
		t.Fatalf("diagnostic response exposed provider error text instead of status-only fields: %s", serialized)
	}
}

func TestRouterDiagnoseSeedanceUsesReadOnlyTaskLookup(t *testing.T) {
	var gotMethod string
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Fatalf("unexpected authorization header")
		}
		http.Error(w, `{"error":{"code":"ResourceNotFound"}}`, http.StatusNotFound)
	}))
	defer server.Close()

	runtime := testRuntime(config.ModelProviderSeedance, server.URL+"/api/v3")
	runtime.ModelTaskRoutes[config.ModelTaskVideoOperation] = config.ModelTaskRoute{
		Task: config.ModelTaskVideoOperation, Provider: config.ModelProviderSeedance, Model: "doubao-seedance-2-0-260128",
	}
	router := NewRouter(runtime)
	result := router.DiagnoseTask(context.Background(), config.ModelTaskVideoOperation)

	if !result.OK || result.HTTPStatus != http.StatusNotFound {
		t.Fatalf("unexpected Seedance diagnostic: %+v", result)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/v3/contents/generations/tasks/cascade-diagnostic-probe-nonexistent" {
		t.Fatalf("unexpected Seedance probe: %s %s", gotMethod, gotPath)
	}
}

func TestRouterDiagnoseSeedanceRejectsUnstructuredNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "route not found", http.StatusNotFound)
	}))
	defer server.Close()

	runtime := testRuntime(config.ModelProviderSeedance, server.URL+"/api/v3")
	runtime.ModelTaskRoutes[config.ModelTaskVideoOperation] = config.ModelTaskRoute{
		Task: config.ModelTaskVideoOperation, Provider: config.ModelProviderSeedance, Model: "doubao-seedance-2-0-260128",
	}
	result := NewRouter(runtime).DiagnoseTask(context.Background(), config.ModelTaskVideoOperation)

	if result.OK || result.HTTPStatus != http.StatusNotFound || result.ErrorClass != "http_404" {
		t.Fatalf("unstructured 404 must not pass Seedance diagnostics: %+v", result)
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
