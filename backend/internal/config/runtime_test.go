package config

import (
	"os"
	"path/filepath"
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestRuntimeConfigDefaultsToDevDesktopSQLite(t *testing.T) {
	t.Setenv("CASCADE_PROFILE", "")
	t.Setenv("APP_MODE", "")
	t.Setenv("DATABASE_DIALECT", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SQLITE_PATH", "")
	t.Setenv("NODE_WORKER_PATH", "")
	t.Setenv("CASCADE_ARK_MEDIA_MODE", "")

	cfg, err := RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != ProfileDev {
		t.Fatalf("profile = %s", cfg.Profile)
	}
	if cfg.Mode != model.AppModeDesktop {
		t.Fatalf("mode = %s", cfg.Mode)
	}
	if cfg.DatabaseDialect != DatabaseSQLite {
		t.Fatalf("dialect = %s", cfg.DatabaseDialect)
	}
	if cfg.SidecarPaths["video-worker"] != "" {
		t.Fatalf("unexpected sidecar override %q", cfg.SidecarPaths["video-worker"])
	}
	if cfg.LLMMode != LLMModeAuto || cfg.ArkMediaMode != ArkMediaModeDryRun || cfg.ModelAdapterVersion != ModelAdapterVersion {
		t.Fatalf("unexpected model config: llm_mode=%s ark_media_mode=%s adapter=%s", cfg.LLMMode, cfg.ArkMediaMode, cfg.ModelAdapterVersion)
	}
}

func TestRuntimeConfigHonorsEnvOverrides(t *testing.T) {
	clearModelProviderEnv(t)
	t.Setenv("CASCADE_PROFILE", "cloud")
	t.Setenv("APP_MODE", "web")
	t.Setenv("APP_ENV", "staging")
	t.Setenv("DATABASE_DIALECT", "postgres")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("CASCADE_DATA_ROOT", filepath.Join("tmp", "data"))
	t.Setenv("CASCADE_ARTIFACT_ROOT", filepath.Join("tmp", "artifacts"))
	t.Setenv("NODE_WORKER_PATH", filepath.Join("sidecars", "video-worker", "dist", "index.js"))
	t.Setenv("GLM_API_KEY", "glm-test-key")
	t.Setenv("KIMI_API_KEY", "kimi-test-key")
	t.Setenv("MINIMAX_API_KEY", "minimax-test-key")
	t.Setenv("SEEDANCE_API_KEY", "seedance-test-key")
	t.Setenv("SEEDREAM_API_KEY", "seedream-test-key")
	t.Setenv("DOUBAO_API_KEY", "doubao-test-key")
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-test-key")
	t.Setenv("DEEPSEEK_MODEL", "deepseek-chat")
	t.Setenv("CASCADE_ARK_MEDIA_MODE", "real")

	cfg, err := RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != ProfileCloud || cfg.Mode != model.AppModeWeb || cfg.Environment != "staging" {
		t.Fatalf("unexpected runtime config: %+v", cfg)
	}
	if cfg.DatabaseURL != "postgres://example" {
		t.Fatalf("database url = %q", cfg.DatabaseURL)
	}
	if cfg.ArtifactRoot != filepath.Join("tmp", "artifacts") {
		t.Fatalf("artifact root = %q", cfg.ArtifactRoot)
	}
	if cfg.SidecarPaths["video-worker"] == "" {
		t.Fatal("expected video-worker sidecar override")
	}
	for _, provider := range []ModelProvider{ModelProviderGLM, ModelProviderKimi, ModelProviderMinimax, ModelProviderSeedance, ModelProviderSeedream, ModelProviderDoubao, ModelProviderDeepSeek} {
		credential := cfg.ModelProviders[provider]
		if !credential.Enabled || credential.APIKey == "" || credential.APIKeyEnv == "" {
			t.Fatalf("expected %s provider credential placeholder to be enabled: %+v", provider, credential)
		}
	}
	if cfg.ArkMediaMode != ArkMediaModeReal {
		t.Fatalf("ark media mode = %s", cfg.ArkMediaMode)
	}
	if cfg.ModelProviders[ModelProviderDeepSeek].DefaultModel != "deepseek-chat" {
		t.Fatalf("deepseek model = %q", cfg.ModelProviders[ModelProviderDeepSeek].DefaultModel)
	}
}

func TestRuntimeConfigDefaultsModelTaskRoutes(t *testing.T) {
	clearModelProviderEnv(t)
	cfg, err := RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	expected := map[ModelTask]ModelTaskRoute{
		ModelTaskPlanning:                {Provider: ModelProviderKimi, Model: "kimi-k2.7-code"},
		ModelTaskCodeReading:             {Provider: ModelProviderGLM, Model: "glm-5.2"},
		ModelTaskMultimodalUnderstanding: {Provider: ModelProviderMinimax, Model: "minimax-m3"},
		ModelTaskVideoOperation:          {Provider: ModelProviderSeedance, Model: "doubao-seedance-2-0-260128"},
	}
	for task, want := range expected {
		got := cfg.ModelTaskRoutes[task]
		if got.Provider != want.Provider || got.Model != want.Model {
			t.Fatalf("%s route = %+v, want provider=%s model=%s", task, got, want.Provider, want.Model)
		}
	}
	if cfg.ModelProviders[ModelProviderKimi].DefaultModel != "kimi-k2.7-code" {
		t.Fatalf("kimi default model = %q", cfg.ModelProviders[ModelProviderKimi].DefaultModel)
	}
	if cfg.ModelProviders[ModelProviderGLM].DefaultModel != "glm-5.2" {
		t.Fatalf("glm default model = %q", cfg.ModelProviders[ModelProviderGLM].DefaultModel)
	}
	if cfg.ModelProviders[ModelProviderMinimax].DefaultModel != "minimax-m3" {
		t.Fatalf("minimax default model = %q", cfg.ModelProviders[ModelProviderMinimax].DefaultModel)
	}
	if cfg.ModelProviders[ModelProviderSeedance].DefaultModel != "doubao-seedance-2-0-260128" {
		t.Fatalf("seedance default model = %q", cfg.ModelProviders[ModelProviderSeedance].DefaultModel)
	}
	if cfg.ModelProviders[ModelProviderSeedream].DefaultModel != "doubao-seedream-5-0-pro-260628" {
		t.Fatalf("seedream default model = %q", cfg.ModelProviders[ModelProviderSeedream].DefaultModel)
	}
}

func TestRuntimeConfigDefaultsOfficialModelProviderBaseURLs(t *testing.T) {
	clearModelProviderEnv(t)
	cfg, err := RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	expected := map[ModelProvider]string{
		ModelProviderGLM:      "https://open.bigmodel.cn/api/paas/v4",
		ModelProviderKimi:     "https://api.moonshot.cn/v1",
		ModelProviderMinimax:  "https://api.minimaxi.com/v1",
		ModelProviderSeedance: "https://ark.cn-beijing.volces.com/api/v3",
		ModelProviderSeedream: "https://ark.cn-beijing.volces.com/api/v3",
		ModelProviderDoubao:   "https://ark.cn-beijing.volces.com/api/v3",
		ModelProviderDeepSeek: "https://api.deepseek.com",
	}
	for provider, want := range expected {
		if got := cfg.ModelProviders[provider].BaseURL; got != want {
			t.Fatalf("%s base url = %q, want %q", provider, got, want)
		}
	}
}

func TestRuntimeConfigAllowsModelTaskRouteOverrides(t *testing.T) {
	clearModelProviderEnv(t)
	t.Setenv("CASCADE_PLANNING_PROVIDER", "deepseek")
	t.Setenv("CASCADE_PLANNING_MODEL", "deepseek-reasoner")

	cfg, err := RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	route := cfg.ModelTaskRoutes[ModelTaskPlanning]
	if route.Provider != ModelProviderDeepSeek || route.Model != "deepseek-reasoner" {
		t.Fatalf("planning override route = %+v", route)
	}
}

func TestRuntimeConfigAlwaysReservesDomesticProviderSlots(t *testing.T) {
	clearModelProviderEnv(t)

	cfg, err := RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	expected := map[ModelProvider]string{
		ModelProviderGLM:      "GLM_API_KEY",
		ModelProviderKimi:     "KIMI_API_KEY",
		ModelProviderMinimax:  "MINIMAX_API_KEY",
		ModelProviderSeedance: "SEEDANCE_API_KEY",
		ModelProviderSeedream: "SEEDREAM_API_KEY",
		ModelProviderDoubao:   "DOUBAO_API_KEY",
		ModelProviderDeepSeek: "DEEPSEEK_API_KEY",
	}
	for provider, envName := range expected {
		credential, ok := cfg.ModelProviders[provider]
		if !ok {
			t.Fatalf("missing provider slot %s", provider)
		}
		if credential.APIKeyEnv != envName || credential.Enabled || credential.APIKey != "" {
			t.Fatalf("unexpected provider placeholder for %s: %+v", provider, credential)
		}
	}
	if cfg.ModelProviders[ModelProviderDeepSeek].DefaultModel != "deepseek-v4-flash" {
		t.Fatalf("deepseek default model = %q", cfg.ModelProviders[ModelProviderDeepSeek].DefaultModel)
	}
}

func TestRuntimeConfigSeedanceCanUseDoubaoOrArkKeyFallback(t *testing.T) {
	clearModelProviderEnv(t)
	t.Setenv("SEEDANCE_API_KEY", "")
	t.Setenv("DOUBAO_API_KEY", "doubao-key")
	t.Setenv("ARK_API_KEY", "ark-key")

	cfg, err := RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	seedance := cfg.ModelProviders[ModelProviderSeedance]
	if !seedance.Enabled || seedance.APIKey != "doubao-key" || seedance.APIKeySourceEnv != "DOUBAO_API_KEY" {
		t.Fatalf("seedance should use doubao fallback key: %+v", seedance)
	}
	doubao := cfg.ModelProviders[ModelProviderDoubao]
	if !doubao.Enabled || doubao.APIKey != "doubao-key" || doubao.APIKeySourceEnv != "DOUBAO_API_KEY" {
		t.Fatalf("doubao should use primary doubao key: %+v", doubao)
	}

	t.Setenv("DOUBAO_API_KEY", "")
	cfg, err = RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ModelProviders[ModelProviderSeedance].APIKeySourceEnv; got != "ARK_API_KEY" {
		t.Fatalf("seedance fallback source = %q, want ARK_API_KEY", got)
	}
	if got := cfg.ModelProviders[ModelProviderDoubao].APIKeySourceEnv; got != "ARK_API_KEY" {
		t.Fatalf("doubao fallback source = %q, want ARK_API_KEY", got)
	}
}

func TestRuntimeConfigSeedreamCanUseDoubaoOrArkKeyFallback(t *testing.T) {
	clearModelProviderEnv(t)
	t.Setenv("SEEDREAM_API_KEY", "")
	t.Setenv("DOUBAO_API_KEY", "doubao-key")
	t.Setenv("ARK_API_KEY", "ark-key")

	cfg, err := RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	seedream := cfg.ModelProviders[ModelProviderSeedream]
	if !seedream.Enabled || seedream.APIKey != "doubao-key" || seedream.APIKeySourceEnv != "DOUBAO_API_KEY" {
		t.Fatalf("seedream should use doubao fallback key: %+v", seedream)
	}

	t.Setenv("DOUBAO_API_KEY", "")
	cfg, err = RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ModelProviders[ModelProviderSeedream].APIKeySourceEnv; got != "ARK_API_KEY" {
		t.Fatalf("seedream fallback source = %q, want ARK_API_KEY", got)
	}
}

func TestRuntimeConfigKimiCanUseMoonshotKeyFallback(t *testing.T) {
	clearModelProviderEnv(t)
	t.Setenv("KIMI_API_KEY", "")
	t.Setenv("MOONSHOT_API_KEY", "moonshot-key")

	cfg, err := RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	kimi := cfg.ModelProviders[ModelProviderKimi]
	if !kimi.Enabled || kimi.APIKey != "moonshot-key" || kimi.APIKeySourceEnv != "MOONSHOT_API_KEY" {
		t.Fatalf("kimi should use moonshot fallback key: %+v", kimi)
	}
}

func TestRuntimeConfigLLMModeOverride(t *testing.T) {
	clearModelProviderEnv(t)
	t.Setenv("CASCADE_LLM_MODE", "real")

	cfg, err := RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLMMode != LLMModeReal {
		t.Fatalf("llm mode = %s", cfg.LLMMode)
	}
}

func TestRuntimeConfigRejectsUnsupportedArkMediaMode(t *testing.T) {
	clearModelProviderEnv(t)
	t.Setenv("CASCADE_ARK_MEDIA_MODE", "surprise")

	if _, err := RuntimeConfigFromEnvWithRoot(filepath.Join("repo")); err == nil {
		t.Fatal("expected unsupported CASCADE_ARK_MEDIA_MODE error")
	}
}

func TestRuntimeConfigLoadsDesktopResourceManifest(t *testing.T) {
	resourceRoot := t.TempDir()
	manifest := `{
		"app": "Cascade DemoOps",
		"resource_contract_version": 1,
		"sidecars": {
			"video-worker": "sidecars/video-worker/dist/index.js"
		},
		"runtimes": {
			"node": "runtimes/node/node.exe"
		},
		"web": "web"
	}`
	if err := os.WriteFile(filepath.Join(resourceRoot, "desktop-runtime.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CASCADE_PROFILE", "desktop")
	t.Setenv("CASCADE_RESOURCE_ROOT", resourceRoot)
	t.Setenv("NODE_WORKER_PATH", "")
	t.Setenv("NODE_BINARY_PATH", "")

	cfg, err := RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	wantWorker := filepath.Join(resourceRoot, "sidecars", "video-worker", "dist", "index.js")
	if cfg.SidecarPaths["video-worker"] != wantWorker {
		t.Fatalf("worker path = %q, want %q", cfg.SidecarPaths["video-worker"], wantWorker)
	}
	wantNode := filepath.Join(resourceRoot, "runtimes", "node", "node.exe")
	if cfg.NodeBinaryPath != wantNode {
		t.Fatalf("node path = %q, want %q", cfg.NodeBinaryPath, wantNode)
	}
	if cfg.ResourceManifestPath == "" {
		t.Fatal("expected manifest path")
	}
}

func TestRuntimeConfigKeepsEnvSidecarOverrideAheadOfManifest(t *testing.T) {
	resourceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(resourceRoot, "desktop-runtime.json"), []byte(`{"sidecars":{"video-worker":"sidecars/video-worker/dist/index.js"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join("custom", "worker", "index.js")
	t.Setenv("CASCADE_PROFILE", "desktop")
	t.Setenv("CASCADE_RESOURCE_ROOT", resourceRoot)
	t.Setenv("NODE_WORKER_PATH", override)

	cfg, err := RuntimeConfigFromEnvWithRoot(filepath.Join("repo"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SidecarPaths["video-worker"] != override {
		t.Fatalf("worker override = %q, want %q", cfg.SidecarPaths["video-worker"], override)
	}
}

func TestResolveDesktopResourceRootFromExeDirFindsPackageResources(t *testing.T) {
	root := t.TempDir()
	exeDir := filepath.Join(root, "dist", "desktop", "windows")
	resourceRoot := filepath.Join(root, "dist", "package", "resources")
	if err := os.MkdirAll(resourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resourceRoot, "desktop-runtime.json"), []byte(`{"resource_contract_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := resolveDesktopResourceRootFromExeDir(exeDir, filepath.Join(root, "repo"))
	if got != filepath.Clean(resourceRoot) {
		t.Fatalf("resource root = %q, want %q", got, filepath.Clean(resourceRoot))
	}
}

func TestDiscoverDevRepoRootWalksUpFromBackend(t *testing.T) {
	root := t.TempDir()
	backendDir := filepath.Join(root, "backend")
	start := filepath.Join(backendDir, "cmd", "desktop")
	if err := os.MkdirAll(start, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pnpm-workspace.yaml"), []byte("packages: []"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backendDir, "go.mod"), []byte("module test"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := DiscoverDevRepoRoot(start); got != filepath.Clean(root) {
		t.Fatalf("repo root = %q, want %q", got, filepath.Clean(root))
	}
}

func TestDefaultUserDataRootForDesktopPlatforms(t *testing.T) {
	home := filepath.Join("Users", "demo")
	tests := map[string]string{
		"windows": filepath.Join(home, "AppData", "Roaming", AppName),
		"darwin":  filepath.Join(home, "Library", "Application Support", AppName),
		"linux":   filepath.Join(home, ".config", AppName),
	}
	for goos, want := range tests {
		if got := DefaultUserDataRootFor(goos, home, AppName); got != want {
			t.Fatalf("%s root = %q, want %q", goos, got, want)
		}
	}
}

func clearModelProviderEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"GLM_API_KEY",
		"GLM_BASE_URL",
		"GLM_MODEL",
		"KIMI_API_KEY",
		"MOONSHOT_API_KEY",
		"KIMI_BASE_URL",
		"KIMI_MODEL",
		"MINIMAX_API_KEY",
		"MINIMAX_BASE_URL",
		"MINIMAX_MODEL",
		"SEEDANCE_API_KEY",
		"SEEDANCE_BASE_URL",
		"SEEDANCE_MODEL",
		"SEEDREAM_API_KEY",
		"SEEDREAM_BASE_URL",
		"SEEDREAM_MODEL",
		"DOUBAO_API_KEY",
		"DOUBAO_BASE_URL",
		"DOUBAO_MODEL",
		"ARK_API_KEY",
		"DEEPSEEK_API_KEY",
		"DEEPSEEK_BASE_URL",
		"DEEPSEEK_MODEL",
		"CASCADE_PLANNING_PROVIDER",
		"CASCADE_PLANNING_MODEL",
		"CASCADE_CODE_READING_PROVIDER",
		"CASCADE_CODE_READING_MODEL",
		"CASCADE_MULTIMODAL_PROVIDER",
		"CASCADE_MULTIMODAL_MODEL",
		"CASCADE_VIDEO_PROVIDER",
		"CASCADE_VIDEO_MODEL",
		"CASCADE_LLM_MODE",
		"CASCADE_ARK_MEDIA_MODE",
	} {
		t.Setenv(name, "")
	}
}
