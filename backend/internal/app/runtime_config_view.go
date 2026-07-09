package app

import (
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
)

type RuntimeConfigView struct {
	Profile                config.RuntimeProfile             `json:"profile"`
	Environment            string                            `json:"environment"`
	Mode                   model.AppMode                     `json:"mode"`
	DatabaseDialect        config.DatabaseDialect            `json:"database_dialect"`
	DatabaseConfigured     bool                              `json:"database_configured"`
	LocalDataConfigured    bool                              `json:"local_data_configured"`
	ResourceRootConfigured bool                              `json:"resource_root_configured"`
	ResourceManifestLoaded bool                              `json:"resource_manifest_loaded"`
	NodeRuntimeConfigured  bool                              `json:"node_runtime_configured"`
	LLMMode                config.LLMMode                    `json:"llm_mode"`
	ModelAdapterVersion    string                            `json:"model_adapter_version"`
	Sidecars               map[string]bool                   `json:"sidecars"`
	ModelProviders         map[string]ProviderCredentialView `json:"model_providers"`
	ModelTaskRoutes        map[string]ModelTaskRouteView     `json:"model_task_routes"`
}

type ProviderCredentialView struct {
	APIKeyEnv              string   `json:"api_key_env"`
	APIKeySourceEnv        string   `json:"api_key_source_env,omitempty"`
	APIKeyFallbackEnvs     []string `json:"api_key_fallback_envs,omitempty"`
	Configured             bool     `json:"configured"`
	BaseURLConfigured      bool     `json:"base_url_configured"`
	DefaultModelConfigured bool     `json:"default_model_configured"`
}

type ModelTaskRouteView struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	ProviderOverride string `json:"provider_override"`
	ModelOverride    string `json:"model_override"`
}

func NewRuntimeConfigView(runtime config.AppRuntimeConfig) RuntimeConfigView {
	return RuntimeConfigView{
		Profile:                runtime.Profile,
		Environment:            runtime.Environment,
		Mode:                   runtime.Mode,
		DatabaseDialect:        runtime.DatabaseDialect,
		DatabaseConfigured:     databaseConfigured(runtime),
		LocalDataConfigured:    runtime.DataRoot != "" && runtime.ArtifactRoot != "" && runtime.CacheRoot != "" && runtime.LogRoot != "",
		ResourceRootConfigured: runtime.ResourceRoot != "",
		ResourceManifestLoaded: runtime.ResourceManifestPath != "",
		NodeRuntimeConfigured:  runtime.NodeBinaryPath != "",
		LLMMode:                runtime.LLMMode,
		ModelAdapterVersion:    runtime.ModelAdapterVersion,
		Sidecars:               sidecarConfigured(runtime.SidecarPaths),
		ModelProviders:         providerCredentialViews(runtime.ModelProviders),
		ModelTaskRoutes:        modelTaskRouteViews(runtime.ModelTaskRoutes),
	}
}

func databaseConfigured(runtime config.AppRuntimeConfig) bool {
	switch runtime.DatabaseDialect {
	case config.DatabasePostgres:
		return runtime.DatabaseURL != ""
	case config.DatabaseSQLite:
		return runtime.SQLitePath != ""
	default:
		return false
	}
}

func sidecarConfigured(sidecars map[string]string) map[string]bool {
	result := map[string]bool{}
	for name, path := range sidecars {
		result[name] = path != ""
	}
	return result
}

func providerCredentialViews(providers map[config.ModelProvider]config.ModelProviderCredential) map[string]ProviderCredentialView {
	result := map[string]ProviderCredentialView{}
	for provider, credential := range providers {
		result[string(provider)] = ProviderCredentialView{
			APIKeyEnv:              credential.APIKeyEnv,
			APIKeySourceEnv:        credential.APIKeySourceEnv,
			APIKeyFallbackEnvs:     append([]string{}, credential.APIKeyFallbackEnvs...),
			Configured:             credential.APIKey != "",
			BaseURLConfigured:      credential.BaseURL != "",
			DefaultModelConfigured: credential.DefaultModel != "",
		}
	}
	return result
}

func modelTaskRouteViews(routes map[config.ModelTask]config.ModelTaskRoute) map[string]ModelTaskRouteView {
	result := map[string]ModelTaskRouteView{}
	for task, route := range routes {
		result[string(task)] = ModelTaskRouteView{
			Provider:         string(route.Provider),
			Model:            route.Model,
			ProviderOverride: route.ProviderOverride,
			ModelOverride:    route.ModelOverride,
		}
	}
	return result
}
