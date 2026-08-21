package media

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
)

func TestAudioClientDryRunPreservesProviderNeutralRequest(t *testing.T) {
	client := NewAudioClient(config.AppRuntimeConfig{
		ArkMediaMode: config.ArkMediaModeDryRun,
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderDoubao: {Provider: config.ModelProviderDoubao, DefaultModel: "doubao-seed-2-0-mini-260428"},
		},
	}, nil)
	result, err := client.Process(context.Background(), AudioModelInput{
		Operation: AudioModelOperationTranscribe, InputRef: "asset://audio_001", InputSHA256: "sha256:test", Language: "zh-CN",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "doubao-seed-2-0-mini-260428" || result.Request == nil || result.Request.InputRef != "asset://audio_001" {
		t.Fatalf("unexpected dry-run audio request: %+v", result)
	}
}

func TestAudioClientKeepsUnsupportedRealOperationsProtocolPending(t *testing.T) {
	client := NewAudioClient(config.AppRuntimeConfig{
		ArkMediaMode: config.ArkMediaModeReal,
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderDoubao: {Provider: config.ModelProviderDoubao, APIKey: "test", BaseURL: "https://ark.example", DefaultModel: "doubao-seed-2-0-mini-260428"},
		},
	}, nil)
	_, err := client.Process(context.Background(), AudioModelInput{Operation: AudioModelOperationTranscribe, InputRef: "https://assets.example/audio.mp3"})
	if err != ErrAudioModelProtocolPending {
		t.Fatalf("expected protocol-pending guard, got %v", err)
	}
}

func TestOpenSpeechTTSSynthesizesPollsDownloadsAndRedactsTransientURL(t *testing.T) {
	t.Skip("superseded by the Agent Plan v3 stream contract")
	t.Setenv("VOLC_TTS_APP_ID", "tts-app-test")
	t.Setenv("VOLC_TTS_ACCESS_KEY", "tts-secret-test")
	t.Setenv("VOLC_TTS_RESOURCE_ID", "seed-tts-2.0")
	t.Setenv("VOLC_TTS_SPEAKER", "zh_female_shuangyue_bigtts")
	t.Setenv("CASCADE_TTS_POLL_ATTEMPTS", "2")
	t.Setenv("CASCADE_TTS_POLL_INTERVAL_MS", "0")

	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/audio.mp3" {
			_, _ = w.Write([]byte("synthetic-test-audio"))
			return
		}
		if r.Header.Get("X-Api-App-Id") != "tts-app-test" || r.Header.Get("X-Api-Access-Key") != "tts-secret-test" || r.Header.Get("X-Api-Resource-Id") != "seed-tts-2.0" {
			t.Errorf("missing OpenSpeech token headers")
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("Token mode must not send Authorization")
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "tts-secret-test") {
			t.Errorf("access key leaked into request body")
		}
		if r.URL.Path == "/api/v3/tts/submit" {
			var request struct {
				User struct {
					UID string `json:"uid"`
				} `json:"user"`
				UniqueID  string `json:"unique_id"`
				ReqParams struct {
					Additions   string `json:"additions"`
					AudioParams struct {
						SpeechRate   int `json:"speech_rate"`
						LoudnessRate int `json:"loudness_rate"`
					} `json:"audio_params"`
				} `json:"req_params"`
			}
			if err := json.Unmarshal(body, &request); err != nil || !strings.HasPrefix(request.User.UID, "server-tts_") || !strings.HasPrefix(request.UniqueID, "tts_") || request.ReqParams.AudioParams.SpeechRate != 0 || request.ReqParams.AudioParams.LoudnessRate != 0 || request.ReqParams.Additions != `{"silence_duration":0}` {
				t.Errorf("unexpected latest async TTS request: body=%s err=%v parsed=%+v", body, err, request)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/tts/submit":
			if r.URL.Query().Get("Action") != "SubmitAsyncTtsTask" {
				t.Errorf("unexpected submit query: %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"code":20000000,"data":{"task_id":"task-test","task_status":1}}`)
		case "/api/v3/tts/query":
			if !strings.Contains(string(body), `"task_id":"task-test"`) {
				t.Errorf("query must use official task_id field: %s", body)
			}
			_, _ = io.WriteString(w, `{"code":20000000,"data":{"task_id":"task-test","task_status":2,"audio_url":"`+server.URL+`/audio.mp3","sentences":[{"text":"测试配音","begin_time":0.25,"end_time":1.75}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("VOLC_TTS_BASE_URL", server.URL)
	t.Setenv("CASCADE_TTS_OUTPUT_DIR", t.TempDir())

	client := NewAudioClient(config.AppRuntimeConfig{
		ArkMediaMode: config.ArkMediaModeReal,
		ArtifactRoot: t.TempDir(),
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderDoubao: {Provider: config.ModelProviderDoubao, DefaultModel: "generic-model-must-not-win"},
		},
	}, server.Client())
	client.probe = func(_ context.Context, _, path string) (audioProbeResult, error) {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "synthetic-test-audio" {
			t.Fatalf("candidate was not downloaded: %v", err)
		}
		return audioProbeResult{DurationMS: 1750, SampleRateHZ: 24000, Channels: 1, MimeType: "audio/mpeg"}, nil
	}
	result, err := client.Process(context.Background(), AudioModelInput{Operation: AudioModelOperationSynthesize, Text: "测试配音"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "seed-tts-2.0" || result.Response == nil || result.Response.Output == nil {
		t.Fatalf("unexpected TTS result: %+v", result)
	}
	output := result.Response.Output
	if output.DownloadURL != "" || output.LocalPath == "" || len(output.SHA256) != 64 || len(output.Transcript) != 1 || output.Transcript[0].StartMS != 250 || output.Transcript[0].EndMS != 1750 {
		t.Fatalf("unexpected normalized output: %+v", output)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(encoded)
	if strings.Contains(serialized, server.URL) || strings.Contains(serialized, "tts-secret-test") || strings.Contains(serialized, output.LocalPath) {
		t.Fatalf("sensitive or transient value leaked into result: %s", serialized)
	}
}

func TestOpenSpeechAgentPlanTTSSynthesizesAndRedactsCandidate(t *testing.T) {
	t.Setenv("VOLC_TTS_ACCESS_KEY", "tts-plan-secret-test")
	t.Setenv("VOLC_TTS_RESOURCE_ID", "seed-tts-2.0")
	t.Setenv("VOLC_TTS_SPEAKER", "BV701_V2_streaming")
	t.Setenv("CASCADE_TTS_OUTPUT_DIR", t.TempDir())
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/plan/tts/unidirectional" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Api-Key") != "tts-plan-secret-test" || r.Header.Get("X-Api-Resource-Id") != "seed-tts-2.0" {
			t.Error("missing Agent Plan headers")
		}
		if r.Header.Get("X-Api-App-Id") != "" || r.Header.Get("X-Api-Access-Key") != "" || r.Header.Get("Authorization") != "" {
			t.Error("legacy auth headers must be absent")
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "tts-plan-secret-test") || !strings.Contains(string(body), `"enable_subtitle":true`) {
			t.Errorf("unexpected request body: %s", body)
		}
		_, _ = io.WriteString(w, `{"code":0,"data":"c3ludGhldGljLXRlc3QtYXVkaW8="}`+"\n")
		_, _ = io.WriteString(w, `{"code":0,"data":"","sentence":{"text":"narration","words":[{"word":"narration","startTime":0.25,"endTime":1.75}]}}`+"\n")
		_, _ = io.WriteString(w, `{"code":20000000,"message":"OK","data":""}`+"\n")
	}))
	defer server.Close()
	t.Setenv("VOLC_TTS_AGENT_PLAN_BASE_URL", server.URL+"/api/v3/plan/tts/unidirectional")
	client := NewAudioClient(config.AppRuntimeConfig{ArkMediaMode: config.ArkMediaModeReal, ArtifactRoot: t.TempDir(), ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{config.ModelProviderDoubao: {Provider: config.ModelProviderDoubao}}}, server.Client())
	client.probe = func(_ context.Context, _, path string) (audioProbeResult, error) {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "synthetic-test-audio" {
			t.Fatalf("candidate was not assembled: %v", err)
		}
		return audioProbeResult{DurationMS: 1750, SampleRateHZ: 24000, Channels: 1, MimeType: "audio/mpeg"}, nil
	}
	result, err := client.Process(context.Background(), AudioModelInput{Operation: AudioModelOperationSynthesize, Text: "narration"})
	if err != nil {
		t.Fatal(err)
	}
	output := result.Response.Output
	if output.LocalPath == "" || len(output.SHA256) != 64 || len(output.Transcript) != 1 || output.Transcript[0].StartMS != 250 || output.Transcript[0].EndMS != 1750 {
		t.Fatalf("unexpected output: %+v", output)
	}
	serialized, _ := json.Marshal(result)
	if strings.Contains(string(serialized), server.URL) || strings.Contains(string(serialized), "tts-plan-secret-test") || strings.Contains(string(serialized), output.LocalPath) {
		t.Fatalf("sensitive value leaked: %s", serialized)
	}
}

func TestOpenSpeechTTSHTTPErrorRedactsConfiguredCredentials(t *testing.T) {
	err := openSpeechTTSHTTPError(500, []byte(`{"message":"app-app access-secret invalid"}`), "app-app", "access-secret")
	if err == nil || strings.Contains(err.Error(), "app-app") || strings.Contains(err.Error(), "access-secret") || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("HTTP diagnostic leaked a credential or omitted status: %v", err)
	}
}

func TestAudioClientRejectsLocalPathLeak(t *testing.T) {
	client := NewAudioClient(config.AppRuntimeConfig{
		ArkMediaMode: config.ArkMediaModeDryRun,
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderDoubao: {Provider: config.ModelProviderDoubao, DefaultModel: "audio-test"},
		},
	}, nil)
	_, err := client.Process(context.Background(), AudioModelInput{Operation: AudioModelOperationEnhance, InputRef: `D:\recordings\voice.wav`})
	if err == nil {
		t.Fatal("expected local path rejection")
	}
}
