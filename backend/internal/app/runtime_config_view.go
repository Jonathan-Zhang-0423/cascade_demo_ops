package app

import (
	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
)

type RuntimeConfigView struct {
	Profile                config.RuntimeProfile  `json:"profile"`
	Environment            string                 `json:"environment"`
	Mode                   model.AppMode          `json:"mode"`
	DatabaseDialect        config.DatabaseDialect `json:"database_dialect"`
	DatabaseConfigured     bool                   `json:"database_configured"`
	LocalDataConfigured    bool                   `json:"local_data_configured"`
	ResourceRootConfigured bool                   `json:"resource_root_configured"`
	ResourceManifestLoaded bool                   `json:"resource_manifest_loaded"`
	NodeRuntimeConfigured  bool                   `json:"node_runtime_configured"`
	Sidecars               map[string]bool        `json:"sidecars"`
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
		Sidecars:               sidecarConfigured(runtime.SidecarPaths),
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
