package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"cascade-demoops/backend/internal/model"
)

const AppName = "CascadeDemoOps"

type RuntimeProfile string

const (
	ProfileDev     RuntimeProfile = "dev"
	ProfileDesktop RuntimeProfile = "desktop"
	ProfileCloud   RuntimeProfile = "cloud"
)

type DatabaseDialect string

const (
	DatabasePostgres DatabaseDialect = "postgres"
	DatabaseSQLite   DatabaseDialect = "sqlite"
)

type LLMMode string

const (
	LLMModeAuto          LLMMode = "auto"
	LLMModeReal          LLMMode = "real"
	LLMModeDeterministic LLMMode = "deterministic"
)

const ModelAdapterVersion = "domestic-llm-adapter-v1"

type ModelProvider string

const (
	ModelProviderGLM      ModelProvider = "glm"
	ModelProviderKimi     ModelProvider = "kimi"
	ModelProviderMinimax  ModelProvider = "minimax"
	ModelProviderSeedance ModelProvider = "seedance"
	ModelProviderDoubao   ModelProvider = "doubao"
	ModelProviderDeepSeek ModelProvider = "deepseek"
)

type ModelProviderCredential struct {
	Provider           ModelProvider
	APIKey             string
	APIKeyEnv          string
	APIKeySourceEnv    string
	APIKeyFallbackEnvs []string
	BaseURL            string
	BaseURLEnv         string
	DefaultModel       string
	DefaultModelEnv    string
	Enabled            bool
}

type ModelTask string

const (
	ModelTaskPlanning                ModelTask = "planning"
	ModelTaskCodeReading             ModelTask = "code_reading"
	ModelTaskMultimodalUnderstanding ModelTask = "multimodal_understanding"
	ModelTaskVideoOperation          ModelTask = "video_operation"
)

type ModelTaskRoute struct {
	Task             ModelTask
	Provider         ModelProvider
	Model            string
	ProviderOverride string
	ModelOverride    string
}

type AppRuntimeConfig struct {
	Profile              RuntimeProfile
	Environment          string
	Mode                 model.AppMode
	DatabaseDialect      DatabaseDialect
	DatabaseURL          string
	SQLitePath           string
	DataRoot             string
	ArtifactRoot         string
	CacheRoot            string
	LogRoot              string
	ResourceRoot         string
	ResourceManifestPath string
	DevRepoRoot          string
	SidecarPaths         map[string]string
	NodeBinaryPath       string
	LLMMode              LLMMode
	ModelAdapterVersion  string
	ModelProviders       map[ModelProvider]ModelProviderCredential
	ModelTaskRoutes      map[ModelTask]ModelTaskRoute
	CloudExchangeBaseURL string
	CloudExchangeToken   string
}

type DesktopResourceManifest struct {
	App                     string            `json:"app"`
	ResourceContractVersion int               `json:"resource_contract_version"`
	Sidecars                map[string]string `json:"sidecars,omitempty"`
	Runtimes                map[string]string `json:"runtimes,omitempty"`
	Web                     string            `json:"web,omitempty"`
}

func RuntimeConfigFromEnv() (AppRuntimeConfig, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return AppRuntimeConfig{}, err
	}
	return RuntimeConfigFromEnvWithRoot(DiscoverDevRepoRoot(cwd))
}

func RuntimeConfigFromEnvWithRoot(devRepoRoot string) (AppRuntimeConfig, error) {
	profile := RuntimeProfile(envOrDefault("CASCADE_PROFILE", string(ProfileDev)))
	if !validProfile(profile) {
		return AppRuntimeConfig{}, errors.New("unsupported CASCADE_PROFILE")
	}

	mode := model.AppMode(envOrDefault("APP_MODE", string(model.AppModeDesktop)))
	if mode != model.AppModeDesktop && mode != model.AppModeWeb {
		return AppRuntimeConfig{}, errors.New("unsupported APP_MODE")
	}

	dataRoot := envOrDefault("CASCADE_DATA_ROOT", DefaultUserDataRoot(AppName))
	resourceRoot := resolveResourceRoot(profile, devRepoRoot, os.Getenv("CASCADE_RESOURCE_ROOT"))
	dialect := DatabaseDialect(os.Getenv("DATABASE_DIALECT"))
	if dialect == "" {
		dialect = defaultDatabaseDialect(profile)
	}
	if dialect != DatabasePostgres && dialect != DatabaseSQLite {
		return AppRuntimeConfig{}, errors.New("unsupported DATABASE_DIALECT")
	}
	llmMode := LLMMode(envOrDefault("CASCADE_LLM_MODE", string(LLMModeAuto)))
	if llmMode != LLMModeAuto && llmMode != LLMModeReal && llmMode != LLMModeDeterministic {
		return AppRuntimeConfig{}, errors.New("unsupported CASCADE_LLM_MODE")
	}

	sqlitePath := envOrDefault("SQLITE_PATH", filepath.Join(dataRoot, "cascade_demoops.db"))
	cfg := AppRuntimeConfig{
		Profile:         profile,
		Environment:     envOrDefault("APP_ENV", defaultEnvironment(profile)),
		Mode:            mode,
		DatabaseDialect: dialect,
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		SQLitePath:      sqlitePath,
		DataRoot:        dataRoot,
		ArtifactRoot:    envOrDefault("CASCADE_ARTIFACT_ROOT", filepath.Join(dataRoot, "artifacts")),
		CacheRoot:       envOrDefault("CASCADE_CACHE_ROOT", filepath.Join(dataRoot, "cache")),
		LogRoot:         envOrDefault("CASCADE_LOG_ROOT", filepath.Join(dataRoot, "logs")),
		ResourceRoot:    resourceRoot,
		DevRepoRoot:     devRepoRoot,
		SidecarPaths: map[string]string{
			"video-worker": os.Getenv("NODE_WORKER_PATH"),
		},
		NodeBinaryPath:       os.Getenv("NODE_BINARY_PATH"),
		LLMMode:              llmMode,
		ModelAdapterVersion:  ModelAdapterVersion,
		ModelProviders:       modelProviderCredentialsFromEnv(),
		ModelTaskRoutes:      modelTaskRoutesFromEnv(),
		CloudExchangeBaseURL: strings.TrimRight(strings.TrimSpace(envOrDefault("CASCADE_CLOUD_EXCHANGE_BASE_URL", os.Getenv("CASCADE_SERVER_BASE_URL"))), "/"),
		CloudExchangeToken:   strings.TrimSpace(envOrDefault("CASCADE_CLOUD_EXCHANGE_TOKEN", os.Getenv("CASCADE_SERVER_TOKEN"))),
	}
	applyDesktopResourceManifest(&cfg)
	return cfg, nil
}

func modelTaskRoutesFromEnv() map[ModelTask]ModelTaskRoute {
	defaults := []ModelTaskRoute{
		{
			Task:             ModelTaskPlanning,
			Provider:         ModelProviderKimi,
			Model:            "kimi-k2.7-code",
			ProviderOverride: "CASCADE_PLANNING_PROVIDER",
			ModelOverride:    "CASCADE_PLANNING_MODEL",
		},
		{
			Task:             ModelTaskCodeReading,
			Provider:         ModelProviderGLM,
			Model:            "glm-5.2",
			ProviderOverride: "CASCADE_CODE_READING_PROVIDER",
			ModelOverride:    "CASCADE_CODE_READING_MODEL",
		},
		{
			Task:             ModelTaskMultimodalUnderstanding,
			Provider:         ModelProviderMinimax,
			Model:            "minimax-m3",
			ProviderOverride: "CASCADE_MULTIMODAL_PROVIDER",
			ModelOverride:    "CASCADE_MULTIMODAL_MODEL",
		},
		{
			Task:             ModelTaskVideoOperation,
			Provider:         ModelProviderSeedance,
			Model:            "seedance-2.0",
			ProviderOverride: "CASCADE_VIDEO_PROVIDER",
			ModelOverride:    "CASCADE_VIDEO_MODEL",
		},
	}
	routes := make(map[ModelTask]ModelTaskRoute, len(defaults))
	for _, route := range defaults {
		if provider := os.Getenv(route.ProviderOverride); provider != "" {
			route.Provider = ModelProvider(provider)
		}
		if modelName := os.Getenv(route.ModelOverride); modelName != "" {
			route.Model = modelName
		}
		routes[route.Task] = route
	}
	return routes
}

func modelProviderCredentialsFromEnv() map[ModelProvider]ModelProviderCredential {
	const (
		defaultGLMBaseURL      = "https://open.bigmodel.cn/api/paas/v4"
		defaultKimiBaseURL     = "https://api.moonshot.cn/v1"
		defaultMinimaxBaseURL  = "https://api.minimaxi.com/v1"
		defaultArkBaseURL      = "https://ark.cn-beijing.volces.com/api/v3"
		defaultDeepSeekBaseURL = "https://api.deepseek.com"
	)
	specs := []struct {
		provider           ModelProvider
		apiKeyEnv          string
		apiKeyFallbackEnvs []string
		baseURLEnv         string
		fallbackBaseURL    string
		defaultModelEnv    string
		fallbackModel      string
	}{
		{ModelProviderGLM, "GLM_API_KEY", nil, "GLM_BASE_URL", defaultGLMBaseURL, "GLM_MODEL", "glm-5.2"},
		{ModelProviderKimi, "KIMI_API_KEY", []string{"MOONSHOT_API_KEY"}, "KIMI_BASE_URL", defaultKimiBaseURL, "KIMI_MODEL", "kimi-k2.7-code"},
		{ModelProviderMinimax, "MINIMAX_API_KEY", nil, "MINIMAX_BASE_URL", defaultMinimaxBaseURL, "MINIMAX_MODEL", "minimax-m3"},
		{ModelProviderSeedance, "SEEDANCE_API_KEY", []string{"DOUBAO_API_KEY", "ARK_API_KEY"}, "SEEDANCE_BASE_URL", defaultArkBaseURL, "SEEDANCE_MODEL", "seedance-2.0"},
		{ModelProviderDoubao, "DOUBAO_API_KEY", []string{"ARK_API_KEY"}, "DOUBAO_BASE_URL", defaultArkBaseURL, "DOUBAO_MODEL", ""},
		{ModelProviderDeepSeek, "DEEPSEEK_API_KEY", nil, "DEEPSEEK_BASE_URL", defaultDeepSeekBaseURL, "DEEPSEEK_MODEL", "deepseek-v4-flash"},
	}
	providers := make(map[ModelProvider]ModelProviderCredential, len(specs))
	for _, spec := range specs {
		apiKey, apiKeySourceEnv := envWithFallback(spec.apiKeyEnv, spec.apiKeyFallbackEnvs...)
		baseURL := envOrDefault(spec.baseURLEnv, spec.fallbackBaseURL)
		defaultModel := envOrDefault(spec.defaultModelEnv, spec.fallbackModel)
		providers[spec.provider] = ModelProviderCredential{
			Provider:           spec.provider,
			APIKey:             apiKey,
			APIKeyEnv:          spec.apiKeyEnv,
			APIKeySourceEnv:    apiKeySourceEnv,
			APIKeyFallbackEnvs: append([]string{}, spec.apiKeyFallbackEnvs...),
			BaseURL:            baseURL,
			BaseURLEnv:         spec.baseURLEnv,
			DefaultModel:       defaultModel,
			DefaultModelEnv:    spec.defaultModelEnv,
			Enabled:            apiKey != "",
		}
	}
	return providers
}

func DefaultUserDataRoot(appName string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", appName)
	}
	return DefaultUserDataRootFor(runtime.GOOS, home, appName)
}

func DefaultUserDataRootFor(goos string, homeDir string, appName string) string {
	switch strings.ToLower(goos) {
	case "windows":
		return filepath.Join(homeDir, "AppData", "Roaming", appName)
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", appName)
	default:
		return filepath.Join(homeDir, ".config", appName)
	}
}

func defaultDatabaseDialect(profile RuntimeProfile) DatabaseDialect {
	if profile == ProfileCloud {
		return DatabasePostgres
	}
	return DatabaseSQLite
}

func defaultEnvironment(profile RuntimeProfile) string {
	if profile == ProfileCloud {
		return "production"
	}
	return "development"
}

func resolveResourceRoot(profile RuntimeProfile, devRepoRoot string, explicit string) string {
	if explicit != "" {
		return filepath.Clean(explicit)
	}
	return defaultResourceRoot(profile, devRepoRoot)
}

func defaultResourceRoot(profile RuntimeProfile, devRepoRoot string) string {
	if profile == ProfileDesktop {
		exe, err := os.Executable()
		if err == nil && exe != "" {
			return resolveDesktopResourceRootFromExeDir(filepath.Dir(exe), devRepoRoot)
		}
	}
	return devRepoRoot
}

func resolveDesktopResourceRootFromExeDir(exeDir string, devRepoRoot string) string {
	candidates := []string{
		filepath.Join(exeDir, "resources"),
		filepath.Join(exeDir, "..", "resources"),
		filepath.Join(exeDir, "..", "..", "package", "resources"),
		exeDir,
	}
	for _, candidate := range candidates {
		clean := filepath.Clean(candidate)
		if hasDesktopResourceManifest(clean) {
			return clean
		}
	}
	if exeDir != "" {
		return filepath.Clean(filepath.Join(exeDir, "resources"))
	}
	return devRepoRoot
}

func LoadDesktopResourceManifest(resourceRoot string) (DesktopResourceManifest, string, error) {
	path := filepath.Join(resourceRoot, "desktop-runtime.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return DesktopResourceManifest{}, path, err
	}
	var manifest DesktopResourceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return DesktopResourceManifest{}, path, err
	}
	return manifest, path, nil
}

func DiscoverDevRepoRoot(start string) string {
	dir := filepath.Clean(start)
	for {
		if hasFile(dir, "pnpm-workspace.yaml") && hasFile(filepath.Join(dir, "backend"), "go.mod") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Clean(start)
		}
		dir = parent
	}
}

func applyDesktopResourceManifest(cfg *AppRuntimeConfig) {
	if cfg.Profile != ProfileDesktop || cfg.ResourceRoot == "" {
		return
	}
	manifest, path, err := LoadDesktopResourceManifest(cfg.ResourceRoot)
	if err != nil {
		return
	}
	cfg.ResourceManifestPath = path
	if cfg.SidecarPaths == nil {
		cfg.SidecarPaths = map[string]string{}
	}
	for name, relativePath := range manifest.Sidecars {
		if cfg.SidecarPaths[name] == "" {
			cfg.SidecarPaths[name] = resourcePath(cfg.ResourceRoot, relativePath)
		}
	}
	if cfg.NodeBinaryPath == "" && manifest.Runtimes["node"] != "" {
		cfg.NodeBinaryPath = resourcePath(cfg.ResourceRoot, manifest.Runtimes["node"])
	}
}

func resourcePath(resourceRoot string, value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(resourceRoot, filepath.FromSlash(value))
}

func hasDesktopResourceManifest(resourceRoot string) bool {
	info, err := os.Stat(filepath.Join(resourceRoot, "desktop-runtime.json"))
	return err == nil && !info.IsDir()
}

func hasFile(dir string, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && !info.IsDir()
}

func validProfile(profile RuntimeProfile) bool {
	switch profile {
	case ProfileDev, ProfileDesktop, ProfileCloud:
		return true
	default:
		return false
	}
}

func envOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envWithFallback(primary string, fallbacks ...string) (string, string) {
	if value := os.Getenv(primary); value != "" {
		return value, primary
	}
	for _, fallback := range fallbacks {
		if value := os.Getenv(fallback); value != "" {
			return value, fallback
		}
	}
	return "", ""
}
