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
}

func TestRuntimeConfigHonorsEnvOverrides(t *testing.T) {
	t.Setenv("CASCADE_PROFILE", "cloud")
	t.Setenv("APP_MODE", "web")
	t.Setenv("APP_ENV", "staging")
	t.Setenv("DATABASE_DIALECT", "postgres")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("CASCADE_DATA_ROOT", filepath.Join("tmp", "data"))
	t.Setenv("CASCADE_ARTIFACT_ROOT", filepath.Join("tmp", "artifacts"))
	t.Setenv("NODE_WORKER_PATH", filepath.Join("sidecars", "video-worker", "dist", "index.js"))

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
