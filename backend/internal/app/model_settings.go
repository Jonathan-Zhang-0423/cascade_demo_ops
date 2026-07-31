package app

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/credentialstore"
)

const modelSettingsFileName = "model_settings.json"

type planningModelSettingsFile struct {
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	ProxyURL string    `json:"proxy_url,omitempty"`
	SavedAt  time.Time `json:"saved_at"`
}

type ModelSettingsRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
	ProxyURL string `json:"proxy_url,omitempty"`
}

func (s *Service) SavePlanningModelSettings(request ModelSettingsRequest) error {
	provider := config.ModelProvider(strings.ToLower(strings.TrimSpace(request.Provider)))
	modelName := strings.TrimSpace(request.Model)
	proxyURL, err := validatePlanningModelProxyURL(request.ProxyURL)
	if err != nil {
		return err
	}
	if modelName == "" || len(modelName) > 160 || strings.ContainsAny(modelName, "\r\n\x00") {
		return errors.New("model name has an invalid format")
	}
	if s.storeModelKey == nil || s.readModelKey == nil || s.deleteModelKey == nil {
		return errors.New("model credential store is unavailable")
	}
	s.controlPlaneMu.RLock()
	_, supported := s.runtime.ModelProviders[provider]
	s.controlPlaneMu.RUnlock()
	if !supported {
		return errors.New("unsupported model provider")
	}
	if err := s.storeModelKey(string(provider), request.APIKey); err != nil {
		return err
	}
	apiKey, err := s.readModelKey(string(provider))
	if err != nil {
		_ = s.deleteModelKey(string(provider))
		return err
	}
	if err := savePlanningModelSettingsFile(s.runtime.DataRoot, provider, modelName, proxyURL); err != nil {
		_ = s.deleteModelKey(string(provider))
		return err
	}
	s.controlPlaneMu.Lock()
	s.runtime = cloneModelRuntime(s.runtime)
	if s.runtime.ModelProviders == nil {
		s.runtime.ModelProviders = map[config.ModelProvider]config.ModelProviderCredential{}
	}
	credential := s.runtime.ModelProviders[provider]
	credential.Provider = provider
	credential.APIKey = apiKey
	credential.APIKeySourceEnv = "windows_credential_manager"
	credential.DefaultModel = modelName
	credential.Enabled = true
	s.runtime.ModelProviders[provider] = credential
	if s.runtime.ModelTaskRoutes == nil {
		s.runtime.ModelTaskRoutes = map[config.ModelTask]config.ModelTaskRoute{}
	}
	route := s.runtime.ModelTaskRoutes[config.ModelTaskPlanning]
	route.Task = config.ModelTaskPlanning
	route.Provider = provider
	route.Model = modelName
	s.runtime.ModelTaskRoutes[config.ModelTaskPlanning] = route
	s.runtime.LLMMode = config.LLMModeAuto
	s.runtime.LLMProxyURL = proxyURL
	runtime := s.runtime
	s.controlPlaneMu.Unlock()
	if s.llm != nil {
		s.llm.UpdateRuntime(runtime)
	}
	return nil
}

func (s *Service) DeletePlanningModelSettings(providerName string) error {
	provider := config.ModelProvider(strings.ToLower(strings.TrimSpace(providerName)))
	if s.deleteModelKey == nil {
		return errors.New("model credential store is unavailable")
	}
	s.controlPlaneMu.RLock()
	_, supported := s.runtime.ModelProviders[provider]
	s.controlPlaneMu.RUnlock()
	if !supported {
		return errors.New("unsupported model provider")
	}
	if err := s.deleteModelKey(string(provider)); err != nil {
		return err
	}
	if err := removePlanningModelSettingsFile(s.runtime.DataRoot, provider); err != nil {
		return err
	}
	s.controlPlaneMu.Lock()
	s.runtime = cloneModelRuntime(s.runtime)
	credential := s.runtime.ModelProviders[provider]
	credential.APIKey = ""
	credential.APIKeySourceEnv = ""
	s.runtime.ModelProviders[provider] = credential
	s.runtime.LLMProxyURL = ""
	runtime := s.runtime
	s.controlPlaneMu.Unlock()
	if s.llm != nil {
		s.llm.UpdateRuntime(runtime)
	}
	return nil
}

func cloneModelRuntime(runtime config.AppRuntimeConfig) config.AppRuntimeConfig {
	providers := make(map[config.ModelProvider]config.ModelProviderCredential, len(runtime.ModelProviders))
	for provider, credential := range runtime.ModelProviders {
		credential.APIKeyFallbackEnvs = append([]string(nil), credential.APIKeyFallbackEnvs...)
		providers[provider] = credential
	}
	routes := make(map[config.ModelTask]config.ModelTaskRoute, len(runtime.ModelTaskRoutes))
	for task, route := range runtime.ModelTaskRoutes {
		routes[task] = route
	}
	runtime.ModelProviders = providers
	runtime.ModelTaskRoutes = routes
	return runtime
}

func hydrateRuntimeWithPlanningModelSettings(runtime config.AppRuntimeConfig) config.AppRuntimeConfig {
	return hydrateRuntimeWithPlanningModelSettingsReader(runtime, credentialstore.ReadModelAPIKey)
}

func hydrateRuntimeWithPlanningModelSettingsReader(runtime config.AppRuntimeConfig, readModelKey func(string) (string, error)) config.AppRuntimeConfig {
	data, err := os.ReadFile(filepath.Join(runtime.DataRoot, modelSettingsFileName))
	if err != nil {
		return runtime
	}
	var settings planningModelSettingsFile
	if json.Unmarshal(data, &settings) != nil {
		return runtime
	}
	provider := config.ModelProvider(strings.ToLower(strings.TrimSpace(settings.Provider)))
	modelName := strings.TrimSpace(settings.Model)
	proxyURL, proxyErr := validatePlanningModelProxyURL(settings.ProxyURL)
	credential, ok := runtime.ModelProviders[provider]
	if !ok || modelName == "" || proxyErr != nil {
		return runtime
	}
	if readModelKey == nil {
		return runtime
	}
	apiKey, err := readModelKey(string(provider))
	if err != nil || apiKey == "" {
		return runtime
	}
	runtime = cloneModelRuntime(runtime)
	credential.Provider = provider
	credential.APIKey = apiKey
	credential.APIKeySourceEnv = "windows_credential_manager"
	credential.DefaultModel = modelName
	credential.Enabled = true
	runtime.ModelProviders[provider] = credential
	route := runtime.ModelTaskRoutes[config.ModelTaskPlanning]
	route.Task = config.ModelTaskPlanning
	route.Provider = provider
	route.Model = modelName
	runtime.ModelTaskRoutes[config.ModelTaskPlanning] = route
	runtime.LLMMode = config.LLMModeAuto
	runtime.LLMProxyURL = proxyURL
	return runtime
}

func removePlanningModelSettingsFile(dataRoot string, provider config.ModelProvider) error {
	path := filepath.Join(dataRoot, modelSettingsFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var settings planningModelSettingsFile
	if json.Unmarshal(data, &settings) == nil && config.ModelProvider(strings.ToLower(strings.TrimSpace(settings.Provider))) != provider {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func savePlanningModelSettingsFile(dataRoot string, provider config.ModelProvider, modelName, proxyURL string) error {
	if strings.TrimSpace(dataRoot) == "" {
		return errors.New("local data root is not configured")
	}
	path := filepath.Join(dataRoot, modelSettingsFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(planningModelSettingsFile{Provider: string(provider), Model: modelName, ProxyURL: proxyURL, SavedAt: time.Now().UTC()}, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, payload, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			_ = os.Remove(temporary)
			return removeErr
		}
		if retryErr := os.Rename(temporary, path); retryErr != nil {
			_ = os.Remove(temporary)
			return retryErr
		}
	}
	return nil
}

func validatePlanningModelProxyURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5") {
		return "", errors.New("model proxy must use http, https, or socks5 URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return "", errors.New("model proxy URL cannot contain credentials, path, query, or fragment")
	}
	if len(raw) > 512 || strings.ContainsAny(raw, "\r\n\x00") {
		return "", errors.New("model proxy URL has an invalid format")
	}
	return strings.TrimRight(raw, "/"), nil
}
