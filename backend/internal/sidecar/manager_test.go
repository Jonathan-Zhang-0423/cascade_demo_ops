package sidecar

import (
	"path/filepath"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
)

func TestVideoWorkerSpecUsesEnvOverride(t *testing.T) {
	override := filepath.Join("custom", "worker", "index.js")
	manager := NewManager(config.AppRuntimeConfig{
		Profile:      config.ProfileDesktop,
		Mode:         model.AppModeDesktop,
		ResourceRoot: filepath.Join("app", "resources"),
		DevRepoRoot:  filepath.Join("repo"),
		SidecarPaths: map[string]string{VideoWorkerName: override},
	})
	spec := manager.VideoWorkerSpec()
	if spec.Command != "node" {
		t.Fatalf("command = %q", spec.Command)
	}
	if len(spec.Args) != 1 || spec.Args[0] != override {
		t.Fatalf("args = %#v", spec.Args)
	}
	if spec.WorkingDir != filepath.Dir(override) {
		t.Fatalf("working dir = %q", spec.WorkingDir)
	}
}

func TestVideoWorkerSpecUsesPackagedResourcePathForDesktop(t *testing.T) {
	manager := NewManager(config.AppRuntimeConfig{
		Profile:      config.ProfileDesktop,
		Mode:         model.AppModeDesktop,
		ResourceRoot: filepath.Join("app", "resources"),
		DevRepoRoot:  filepath.Join("repo"),
		SidecarPaths: map[string]string{},
	})
	spec := manager.VideoWorkerSpec()
	want := filepath.Join("app", "resources", "sidecars", "video-worker", "dist", "index.js")
	if spec.Args[0] != want {
		t.Fatalf("worker path = %q, want %q", spec.Args[0], want)
	}
}

func TestVideoWorkerSpecUsesDevDistPathForDev(t *testing.T) {
	manager := NewManager(config.AppRuntimeConfig{
		Profile:      config.ProfileDev,
		Mode:         model.AppModeDesktop,
		ResourceRoot: filepath.Join("app", "resources"),
		DevRepoRoot:  filepath.Join("repo"),
		SidecarPaths: map[string]string{},
	})
	spec := manager.VideoWorkerSpec()
	want := filepath.Join("repo", "video-worker", "dist", "index.js")
	if spec.Args[0] != want {
		t.Fatalf("worker path = %q, want %q", spec.Args[0], want)
	}
}
