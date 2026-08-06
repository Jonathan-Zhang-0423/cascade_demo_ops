package media

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"cascade-demoops/backend/internal/config"
)

func TestMiniMaxH3ClientSubmitsAndNormalizesTaskID(t *testing.T) {
	var body miniMaxH3SubmitRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != miniMaxH3GenerationEndpoint {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer minimax-secret" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"task_h3_1"}`))
	}))
	defer server.Close()

	client := NewMiniMaxH3Client(MiniMaxH3ClientOptions{Mode: config.ArkMediaModeReal, APIKey: "minimax-secret", BaseURL: server.URL, HTTPClient: server.Client()})
	result, err := client.CreateContentGenerationTask(t.Context(), ContentGenerationTaskRequest{
		Model:      MiniMaxH3Model,
		Content:    []ContentPart{{Type: "text", Text: "Create a presentation-only transition."}},
		Resolution: "2K",
		Duration:   5,
		Ratio:      "16:9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if body.Model != MiniMaxH3Model || body.Resolution != "2K" || body.Duration != 5 {
		t.Fatalf("request body = %+v", body)
	}
	if result.Response == nil || result.Response.ID != "task_h3_1" || result.Response.Status != "queued" {
		t.Fatalf("normalized result = %+v", result)
	}
}

func TestMiniMaxH3ClientValidatesReferenceModes(t *testing.T) {
	client := NewMiniMaxH3Client(MiniMaxH3ClientOptions{Mode: config.ArkMediaModeDryRun})
	_, err := client.CreateContentGenerationTask(t.Context(), ContentGenerationTaskRequest{
		Content: []ContentPart{
			{Type: "text", Text: "Use this reference."},
			{Type: "image_url", ImageURL: &MediaURL{URL: "https://example.test/first.png"}, Role: "first_frame"},
			{Type: "video_url", VideoURL: &MediaURL{URL: "https://example.test/reference.mp4"}, Role: "reference_video"},
		},
		Resolution: "2K", Duration: 5, Ratio: "adaptive",
	})
	if err == nil {
		t.Fatal("expected mixed frame/reference mode to be rejected")
	}

	_, err = client.CreateContentGenerationTask(t.Context(), ContentGenerationTaskRequest{
		Content: []ContentPart{
			{Type: "text", Text: "Use this audio."},
			{Type: "audio_url", AudioURL: &MediaURL{URL: "https://example.test/reference.mp3"}, Role: "reference_audio"},
		},
		Resolution: "2K", Duration: 5, Ratio: "adaptive",
	})
	if err == nil {
		t.Fatal("expected audio-only reference mode to be rejected")
	}
}

func TestMiniMaxH3ClientQueriesAndNormalizesVideoOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != miniMaxH3QueryEndpoint+"/task_h3_1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer minimax-secret" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"task": {
				"id": "task_h3_1",
				"model": "MiniMax-H3",
				"status": "succeeded",
				"content": {"url": "https://cdn.example.test/generated/output.mp4"},
				"resolution": "2K",
				"duration": 5,
				"ratio": "16:9"
			}
		}`))
	}))
	defer server.Close()

	client := NewMiniMaxH3Client(MiniMaxH3ClientOptions{Mode: config.ArkMediaModeReal, APIKey: "minimax-secret", BaseURL: server.URL, HTTPClient: server.Client()})
	result, err := client.GetContentGenerationTask(t.Context(), "task_h3_1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Response == nil || result.Response.ID != "task_h3_1" || result.Response.Status != "succeeded" || result.Response.Model != MiniMaxH3Model {
		t.Fatalf("normalized response = %+v", result.Response)
	}
	if result.Response.Output["video_url"] != "https://cdn.example.test/generated/output.mp4" {
		t.Fatalf("normalized output = %+v", result.Response.Output)
	}
}

func TestMiniMaxH3ClientNormalizesCancelledSpelling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task":{"id":"task_h3_1","status":"canceled"}}`))
	}))
	defer server.Close()

	client := NewMiniMaxH3Client(MiniMaxH3ClientOptions{Mode: config.ArkMediaModeReal, APIKey: "minimax-secret", BaseURL: server.URL, HTTPClient: server.Client()})
	result, err := client.GetContentGenerationTask(t.Context(), "task_h3_1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Response == nil || result.Response.Status != MiniMaxH3TaskCancelled {
		t.Fatalf("normalized response = %+v", result.Response)
	}
}

func TestMiniMaxH3ClientRejectsUnknownProviderStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task":{"id":"task_h3_1","status":"mystery"}}`))
	}))
	defer server.Close()

	client := NewMiniMaxH3Client(MiniMaxH3ClientOptions{Mode: config.ArkMediaModeReal, APIKey: "minimax-secret", BaseURL: server.URL, HTTPClient: server.Client()})
	result, err := client.GetContentGenerationTask(t.Context(), "task_h3_1")
	if err == nil || result.Trace.ErrorClass != "provider_status_unknown" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestMiniMaxH3ClientCancelsOrDeletesTask(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != miniMaxH3GenerationEndpoint+"/task_h3_1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"task_h3_1","action":"cancel","status":"cancelled"}`))
	}))
	defer server.Close()

	client := NewMiniMaxH3Client(MiniMaxH3ClientOptions{Mode: config.ArkMediaModeReal, APIKey: "minimax-secret", BaseURL: server.URL, HTTPClient: server.Client()})
	result, err := client.CancelOrDeleteContentGenerationTask(t.Context(), "task_h3_1")
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID != "task_h3_1" || result.Action != "cancel" || result.Status != MiniMaxH3TaskCancelled {
		t.Fatalf("result = %+v", result)
	}
}

func TestMiniMaxH3ClientClassifiesProviderErrors(t *testing.T) {
	tests := []struct {
		status int
		class  string
	}{
		{http.StatusBadRequest, "provider_invalid_request"},
		{http.StatusUnauthorized, "provider_auth_failed"},
		{http.StatusPaymentRequired, "provider_insufficient_balance"},
		{http.StatusUnprocessableEntity, "provider_unprocessable_request"},
		{http.StatusTooManyRequests, "provider_rate_limited"},
		{http.StatusInternalServerError, "provider_server_error"},
		{529, "provider_overloaded"},
	}
	for _, test := range tests {
		t.Run(test.class, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"error":{"message":"provider error"}}`))
			}))
			defer server.Close()
			client := NewMiniMaxH3Client(MiniMaxH3ClientOptions{Mode: config.ArkMediaModeReal, APIKey: "minimax-secret", BaseURL: server.URL, HTTPClient: server.Client()})
			result, err := client.GetContentGenerationTask(t.Context(), "task_h3_1")
			if err == nil || result.Trace.ErrorClass != test.class {
				t.Fatalf("status=%d result=%+v err=%v", test.status, result, err)
			}
		})
	}
}

func TestMiniMaxH3ClientQueryDryRunMakesNoProviderCall(t *testing.T) {
	var providerCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewMiniMaxH3Client(MiniMaxH3ClientOptions{Mode: config.ArkMediaModeDryRun, BaseURL: server.URL, HTTPClient: server.Client()})
	result, err := client.GetContentGenerationTask(t.Context(), "task_h3_dry_run")
	if err != nil || result.Response == nil || result.Response.Status != "dry_run" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if got := providerCalls.Load(); got != 0 {
		t.Fatalf("provider HTTP calls = %d, want 0", got)
	}
}

func TestMiniMaxH3ClientRejectsUnsupportedOrQualityRiskingOptions(t *testing.T) {
	client := NewMiniMaxH3Client(MiniMaxH3ClientOptions{Mode: config.ArkMediaModeDryRun})
	base := ContentGenerationTaskRequest{
		Content:  []ContentPart{{Type: "text", Text: "Create a controlled transition."}},
		Duration: 5,
		Ratio:    "16:9",
	}

	withAudio := base
	withAudio.GenerateAudio = true
	if _, err := client.CreateContentGenerationTask(t.Context(), withAudio); err == nil {
		t.Fatal("expected undocumented generate_audio option to be rejected")
	}

	withLastFrameReturn := base
	withLastFrameReturn.ReturnLastFrame = true
	if _, err := client.CreateContentGenerationTask(t.Context(), withLastFrameReturn); err == nil {
		t.Fatal("expected undocumented return_last_frame option to be rejected")
	}

	lastFrameOnly := base
	lastFrameOnly.Ratio = "adaptive"
	lastFrameOnly.Content = append(lastFrameOnly.Content, ContentPart{
		Type: "image_url", ImageURL: &MediaURL{URL: "https://example.test/last.png"}, Role: "last_frame",
	})
	if _, err := client.CreateContentGenerationTask(t.Context(), lastFrameOnly); err == nil {
		t.Fatal("expected last_frame without first_frame to be rejected")
	}

	wrongModel := base
	wrongModel.Model = "another-video-model"
	if _, err := client.CreateContentGenerationTask(t.Context(), wrongModel); err == nil {
		t.Fatal("expected non-H3 model to be rejected before request normalization")
	}
}

func TestMiniMaxH3ClientRejectsLastFrameOnlyBeforeProviderCall(t *testing.T) {
	var providerCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"must_not_be_created"}`))
	}))
	defer server.Close()

	client := NewMiniMaxH3Client(MiniMaxH3ClientOptions{
		Mode:       config.ArkMediaModeReal,
		APIKey:     "minimax-secret",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	result, err := client.CreateContentGenerationTask(t.Context(), ContentGenerationTaskRequest{
		Content: []ContentPart{
			{Type: "text", Text: "End at this frame."},
			{Type: "image_url", ImageURL: &MediaURL{URL: "https://example.test/last.png"}, Role: "last_frame"},
		},
		Resolution: "2K",
		Duration:   5,
		Ratio:      "adaptive",
	})

	if !errors.Is(err, ErrMiniMaxH3LastFrameOnlyNotEnabled) {
		t.Fatalf("expected capability error, got result=%+v err=%v", result, err)
	}
	if result.Trace.ErrorClass != "provider_capability_not_enabled" {
		t.Fatalf("error class = %q", result.Trace.ErrorClass)
	}
	if result.Response != nil {
		t.Fatalf("provider response must remain nil for a blocked request: %+v", result.Response)
	}
	if got := providerCalls.Load(); got != 0 {
		t.Fatalf("provider HTTP calls = %d, want 0", got)
	}
}

func TestMiniMaxH3ClientEnforcesConservativeReferenceProfileBeforeProviderCall(t *testing.T) {
	var providerCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := NewMiniMaxH3Client(MiniMaxH3ClientOptions{Mode: config.ArkMediaModeReal, APIKey: "minimax-secret", BaseURL: server.URL, HTTPClient: server.Client()})

	_, err := client.CreateContentGenerationTask(t.Context(), ContentGenerationTaskRequest{
		Content: []ContentPart{
			{Type: "text", Text: "Use controlled references."},
			{Type: "image_url", ImageURL: &MediaURL{URL: "https://example.test/1.png"}, Role: "reference_image"},
			{Type: "image_url", ImageURL: &MediaURL{URL: "https://example.test/2.png"}, Role: "reference_image"},
			{Type: "image_url", ImageURL: &MediaURL{URL: "https://example.test/3.png"}, Role: "reference_image"},
			{Type: "image_url", ImageURL: &MediaURL{URL: "https://example.test/4.png"}, Role: "reference_image"},
			{Type: "video_url", VideoURL: &MediaURL{URL: "https://example.test/1.mp4"}, Role: "reference_video"},
		},
		Duration: 5, Ratio: "adaptive",
	})
	if err == nil || !strings.Contains(err.Error(), "current Server capability profile") {
		t.Fatalf("expected conservative reference limit error, got %v", err)
	}

	_, err = client.CreateContentGenerationTask(t.Context(), ContentGenerationTaskRequest{
		Content: []ContentPart{
			{Type: "text", Text: "Use controlled references."},
			{Type: "image_url", ImageURL: &MediaURL{URL: "https://example.test/1.png"}, Role: "reference_image"},
			{Type: "audio_url", AudioURL: &MediaURL{URL: "https://example.test/1.mp3"}, Role: "reference_audio"},
		},
		Duration: 5, Ratio: "adaptive",
	})
	if !errors.Is(err, ErrMiniMaxH3ReferenceAudioNotEnabled) {
		t.Fatalf("expected reference audio capability error, got %v", err)
	}
	if got := providerCalls.Load(); got != 0 {
		t.Fatalf("provider HTTP calls = %d, want 0", got)
	}
}
