package media

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestMiniMaxH3ClientBlocksUnverifiedQueryContract(t *testing.T) {
	client := NewMiniMaxH3Client(MiniMaxH3ClientOptions{Mode: config.ArkMediaModeReal, APIKey: "minimax-secret"})
	result, err := client.GetContentGenerationTask(t.Context(), "task_h3_1")
	if !errors.Is(err, ErrMiniMaxH3QueryContractMissing) || result.Trace.ErrorClass != "query_contract_missing" {
		t.Fatalf("result=%+v err=%v", result, err)
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
}
