package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/store"
)

func TestPlanningModelSettingsUpdateRuntimeWithoutPersistingAPIKey(t *testing.T) {
	root := t.TempDir()
	runtime := config.AppRuntimeConfig{
		DataRoot: root, LLMMode: config.LLMModeAuto, ModelAdapterVersion: config.ModelAdapterVersion,
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderKimi:     {Provider: config.ModelProviderKimi, BaseURL: "https://api.moonshot.cn/v1", DefaultModel: "kimi-k2.7-code", Enabled: true},
			config.ModelProviderDeepSeek: {Provider: config.ModelProviderDeepSeek, BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-v4-flash", Enabled: true},
		},
		ModelTaskRoutes: map[config.ModelTask]config.ModelTaskRoute{
			config.ModelTaskPlanning: {Task: config.ModelTaskPlanning, Provider: config.ModelProviderKimi, Model: "kimi-k2.7-code"},
		},
	}
	service, err := NewService(runtime, store.NewMemoryStateStore())
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	service.storeModelKey = func(provider, key string) error { keys[provider] = key; return nil }
	service.readModelKey = func(provider string) (string, error) { return keys[provider], nil }
	service.deleteModelKey = func(provider string) error { delete(keys, provider); return nil }

	const secret = "test-model-secret"
	if err := service.SavePlanningModelSettings(ModelSettingsRequest{Provider: "deepseek", Model: "deepseek-chat", APIKey: secret, ProxyURL: "http://127.0.0.1:7892"}); err != nil {
		t.Fatal(err)
	}
	configured := service.RuntimeConfig()
	if configured.ModelTaskRoutes[config.ModelTaskPlanning].Provider != config.ModelProviderDeepSeek || configured.ModelTaskRoutes[config.ModelTaskPlanning].Model != "deepseek-chat" {
		t.Fatalf("planning route was not updated: %+v", configured.ModelTaskRoutes[config.ModelTaskPlanning])
	}
	if configured.ModelProviders[config.ModelProviderDeepSeek].APIKey != secret || configured.ModelProviders[config.ModelProviderDeepSeek].APIKeySourceEnv != "windows_credential_manager" {
		t.Fatalf("runtime credential was not activated: %+v", configured.ModelProviders[config.ModelProviderDeepSeek])
	}
	data, err := os.ReadFile(filepath.Join(root, modelSettingsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || !strings.Contains(string(data), "deepseek-chat") {
		t.Fatalf("model settings file leaked secret or omitted non-secret metadata: %s", data)
	}
	if !strings.Contains(string(data), "127.0.0.1:7892") || configured.LLMProxyURL != "http://127.0.0.1:7892" {
		t.Fatalf("model proxy metadata was not persisted and activated: %s", data)
	}
	view := NewRuntimeConfigView(configured, ExchangeIdentityStatus{})
	encodedView := mustModelSettingsJSON(t, view)
	if strings.Contains(encodedView, secret) || !view.ModelProviders["deepseek"].Configured {
		t.Fatalf("runtime view leaked secret or omitted configured status: %s", encodedView)
	}
	if err := service.DeletePlanningModelSettings("deepseek"); err != nil {
		t.Fatal(err)
	}
	if service.RuntimeConfig().ModelProviders[config.ModelProviderDeepSeek].APIKey != "" || keys["deepseek"] != "" {
		t.Fatal("model credential was not removed from runtime and secure store")
	}
	if _, err := os.Stat(filepath.Join(root, modelSettingsFileName)); !os.IsNotExist(err) {
		t.Fatalf("model settings metadata remains after disconnect: %v", err)
	}
}

func TestPlanningModelSettingsHydrateRuntimeFromMetadataAndSecureKey(t *testing.T) {
	root := t.TempDir()
	runtime := config.AppRuntimeConfig{
		DataRoot: root,
		LLMMode:  config.LLMModeDeterministic,
		ModelProviders: map[config.ModelProvider]config.ModelProviderCredential{
			config.ModelProviderKimi: {Provider: config.ModelProviderKimi, BaseURL: "https://api.moonshot.cn/v1", DefaultModel: "old-model"},
		},
		ModelTaskRoutes: map[config.ModelTask]config.ModelTaskRoute{
			config.ModelTaskPlanning: {Task: config.ModelTaskPlanning, Provider: config.ModelProviderKimi, Model: "old-model"},
		},
	}
	if err := savePlanningModelSettingsFile(root, config.ModelProviderKimi, "kimi-restored", "http://127.0.0.1:7892"); err != nil {
		t.Fatal(err)
	}
	restored := hydrateRuntimeWithPlanningModelSettingsReader(runtime, func(provider string) (string, error) {
		if provider != "kimi" {
			t.Fatalf("unexpected provider lookup: %s", provider)
		}
		return "credential-manager-secret", nil
	})
	if restored.LLMMode != config.LLMModeAuto || restored.ModelTaskRoutes[config.ModelTaskPlanning].Model != "kimi-restored" {
		t.Fatalf("planning route was not restored: %+v", restored.ModelTaskRoutes[config.ModelTaskPlanning])
	}
	if restored.LLMProxyURL != "http://127.0.0.1:7892" {
		t.Fatalf("model proxy was not restored: %q", restored.LLMProxyURL)
	}
	credential := restored.ModelProviders[config.ModelProviderKimi]
	if credential.APIKey != "credential-manager-secret" || credential.APIKeySourceEnv != "windows_credential_manager" || !credential.Enabled {
		t.Fatalf("secure model credential was not restored: %+v", credential)
	}
	if runtime.ModelProviders[config.ModelProviderKimi].APIKey != "" || runtime.ModelTaskRoutes[config.ModelTaskPlanning].Model != "old-model" {
		t.Fatal("hydration mutated the caller runtime maps")
	}
}

func mustModelSettingsJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
