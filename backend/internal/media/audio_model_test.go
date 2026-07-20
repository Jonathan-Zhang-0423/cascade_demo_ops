package media

import (
	"context"
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

func TestAudioClientDoesNotGuessRealVendorProtocol(t *testing.T) {
	client := NewAudioClient(config.AppRuntimeConfig{
		ArkMediaMode: config.ArkMediaModeReal,
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderDoubao: {Provider: config.ModelProviderDoubao, APIKey: "test", BaseURL: "https://ark.example", DefaultModel: "doubao-seed-2-0-mini-260428"},
		},
	}, nil)
	_, err := client.Process(context.Background(), AudioModelInput{Operation: AudioModelOperationSynthesize, Text: "测试配音"})
	if err != ErrAudioModelProtocolPending {
		t.Fatalf("expected protocol-pending guard, got %v", err)
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
