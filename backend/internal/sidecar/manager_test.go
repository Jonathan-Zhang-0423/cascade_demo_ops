package sidecar

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/model"
)

func TestVideoWorkerSpecUsesEnvOverride(t *testing.T) {
	override := filepath.Join("custom", "worker", "index.js")
	nodePath := filepath.Join("runtime", "node")
	manager := NewManager(config.AppRuntimeConfig{
		Profile:        config.ProfileDesktop,
		Mode:           model.AppModeDesktop,
		ResourceRoot:   filepath.Join("app", "resources"),
		DevRepoRoot:    filepath.Join("repo"),
		SidecarPaths:   map[string]string{VideoWorkerName: override},
		NodeBinaryPath: nodePath,
	})
	spec := manager.VideoWorkerSpec()
	if spec.Command != nodePath {
		t.Fatalf("command = %q", spec.Command)
	}
	if len(spec.Args) != 1 || spec.Args[0] != override {
		t.Fatalf("args = %#v", spec.Args)
	}
	if spec.WorkingDir != filepath.Dir(override) {
		t.Fatalf("working dir = %q", spec.WorkingDir)
	}
	if spec.Health == nil || spec.Health.Method != "health" {
		t.Fatalf("health check = %#v", spec.Health)
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

func TestManagerUsesBundledNodeRuntimeWhenPresent(t *testing.T) {
	resourceRoot := t.TempDir()
	nodePath := filepath.Join(resourceRoot, "runtimes", "node", nodeExecutableName())
	if err := os.MkdirAll(filepath.Dir(nodePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nodePath, []byte("node"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(nodePath, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	manager := NewManager(config.AppRuntimeConfig{
		Profile:      config.ProfileDesktop,
		Mode:         model.AppModeDesktop,
		ResourceRoot: resourceRoot,
		DevRepoRoot:  filepath.Join("repo"),
		SidecarPaths: map[string]string{},
	})
	spec := manager.VideoWorkerSpec()
	if spec.Command != nodePath {
		t.Fatalf("node binary = %q, want %q", spec.Command, nodePath)
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
