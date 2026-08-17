package media

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
)

func TestArkMediaClientDryRunBuildsSeedanceRequestWithoutLeakingKey(t *testing.T) {
	runtime := testArkRuntime(config.ArkMediaModeDryRun, "https://ark.example/api/v3")
	client := NewClient(runtime, nil)

	result, err := client.CreateContentGenerationTask(t.Context(), ContentGenerationTaskRequest{
		Content: []ContentPart{{Type: "text", Text: "Use captured product assets only."}},
		Ratio:   "16:9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != config.ArkMediaModeDryRun || result.Request == nil || result.Response != nil {
		t.Fatalf("unexpected dry-run result: %+v", result)
	}
	if result.Model != "doubao-seedance-2-0-260128" || result.Request.Model != result.Model {
		t.Fatalf("unexpected dry-run model: %+v", result)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "seedance-secret") || strings.Contains(string(payload), "Authorization") {
		t.Fatalf("dry-run result leaked secret material: %s", payload)
	}
	if result.Trace.EndpointHost != "ark.example" || result.Trace.EndpointPath != "/api/v3/contents/generations/tasks" {
		t.Fatalf("unexpected endpoint trace: %+v", result.Trace)
	}
}

func TestArkMediaClientRealCreateContentGenerationTaskUsesArkEndpointAndBearer(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody ContentGenerationTaskRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"task_1","status":"queued","model":"doubao-seedance-2-0-260128"}`))
	}))
	defer server.Close()

	runtime := testArkRuntime(config.ArkMediaModeReal, server.URL+"/api/v3")
	client := NewClient(runtime, server.Client())

	result, err := client.CreateContentGenerationTask(t.Context(), ContentGenerationTaskRequest{
		Content:    []ContentPart{{Type: "text", Text: "Plan pacing only."}},
		Resolution: "1080p",
		Ratio:      "16:9",
		Duration:   5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v3/contents/generations/tasks" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer seedance-secret" {
		t.Fatalf("authorization header = %q", gotAuth)
	}
	if gotBody.Model != "doubao-seedance-2-0-260128" || gotBody.Duration != 5 {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
	if result.Response == nil || result.Response.ID != "task_1" || result.Trace.HTTPStatus != http.StatusOK {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestArkMediaClientRealGetContentGenerationTaskParsesOutput(t *testing.T) {
	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"task_1","status":"succeeded","model":"doubao-seedance-2-0-260128","output":{"video_url":"https://asset.example/demo.mp4"}}`))
	}))
	defer server.Close()

	runtime := testArkRuntime(config.ArkMediaModeReal, server.URL+"/api/v3")
	client := NewClient(runtime, server.Client())

	result, err := client.GetContentGenerationTask(t.Context(), "task_1")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v3/contents/generations/tasks/task_1" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer seedance-secret" {
		t.Fatalf("authorization header = %q", gotAuth)
	}
	if result.Response == nil || result.Response.Status != "succeeded" || result.Response.Output["video_url"] != "https://asset.example/demo.mp4" {
		t.Fatalf("unexpected task result: %+v", result)
	}
}

func TestArkMediaClientNormalizesSeedanceContentAndTopLevelURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"task_1","status":"succeeded","content":{"video_url":"https://asset.example/content.mp4"},"last_frame_url":"https://asset.example/last.png"}`))
	}))
	defer server.Close()

	result, err := NewClient(testArkRuntime(config.ArkMediaModeReal, server.URL+"/api/v3"), server.Client()).GetContentGenerationTask(t.Context(), "task_1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Response == nil || result.Response.Output["last_frame_url"] != "https://asset.example/last.png" {
		t.Fatalf("top-level output URL was not normalized: %+v", result.Response)
	}
	content, ok := result.Response.Output["content"].(map[string]any)
	if !ok || content["video_url"] != "https://asset.example/content.mp4" {
		t.Fatalf("content output URL was not normalized: %+v", result.Response)
	}
}

func TestArkMediaClientRealGenerateImagesUsesSeedreamProvider(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody ImageGenerationRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1784100000,"data":[{"url":"https://asset.example/title.png"}]}`))
	}))
	defer server.Close()

	runtime := testArkRuntime(config.ArkMediaModeReal, server.URL+"/api/v3")
	client := NewClient(runtime, server.Client())

	result, err := client.GenerateImages(t.Context(), ImageGenerationRequest{
		Prompt:         "Title card only, no fake product UI.",
		Size:           "2K",
		ResponseFormat: "url",
		OutputFormat:   "png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v3/images/generations" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer seedream-secret" {
		t.Fatalf("authorization header = %q", gotAuth)
	}
	if gotBody.Model != "doubao-seedream-5-0-pro-260628" || gotBody.Prompt == "" {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
	if result.Response == nil || len(result.Response.Data) != 1 || result.Response.Data[0].URL == "" {
		t.Fatalf("unexpected image result: %+v", result)
	}
}

func TestArkMediaClientDisabledRejectsCalls(t *testing.T) {
	runtime := testArkRuntime(config.ArkMediaModeDisabled, "https://ark.example/api/v3")
	client := NewClient(runtime, nil)

	result, err := client.CreateContentGenerationTask(t.Context(), ContentGenerationTaskRequest{})
	if err == nil || result.Trace.ErrorClass != "disabled" {
		t.Fatalf("expected disabled error, got result=%+v err=%v", result, err)
	}
}

func TestArkMediaClientRealRedactsProviderErrors(t *testing.T) {
	runtime := testArkRuntime(config.ArkMediaModeReal, "http://seedance-secret.invalid")
	client := NewClient(runtime, &http.Client{Transport: failingRoundTripper{err: errors.New("dial seedance-secret failed")}})

	_, err := client.CreateContentGenerationTask(t.Context(), ContentGenerationTaskRequest{})
	if err == nil {
		t.Fatal("expected request error")
	}
	if strings.Contains(err.Error(), "seedance-secret") {
		t.Fatalf("error leaked secret: %v", err)
	}
}

type failingRoundTripper struct {
	err error
}

func (f failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, f.err
}

func testArkRuntime(mode config.ArkMediaMode, baseURL string) config.AppRuntimeConfig {
	return config.AppRuntimeConfig{
		ArkMediaMode: mode,
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderSeedance: {
				Provider:     config.ModelProviderSeedance,
				APIKey:       "seedance-secret",
				BaseURL:      baseURL,
				DefaultModel: "doubao-seedance-2-0-260128",
			},
			config.ModelProviderSeedream: {
				Provider:     config.ModelProviderSeedream,
				APIKey:       "seedream-secret",
				BaseURL:      baseURL,
				DefaultModel: "doubao-seedream-5-0-pro-260628",
			},
		},
	}
}
