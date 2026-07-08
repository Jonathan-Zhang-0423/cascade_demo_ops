package config

import (
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
