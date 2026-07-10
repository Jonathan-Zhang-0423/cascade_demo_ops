package driver

import (
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestNewSandboxRunnerSelectsRuntimeProfiles(t *testing.T) {
	dev, err := NewSandboxRunner(SandboxRunnerConfig{Profile: model.SandboxProfileDev, WorkerPath: "video-worker/dist/index.js"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := dev.(*LocalDriver); !ok {
		t.Fatalf("dev profile should use local sidecar, got %T", dev)
	}

	cloud, err := NewSandboxRunner(SandboxRunnerConfig{Profile: model.SandboxProfileMVPCloud})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cloud.(*CloudDriver); !ok {
		t.Fatalf("mvp cloud profile should use cloud container driver, got %T", cloud)
	}

	enterprise, err := NewSandboxRunner(SandboxRunnerConfig{Profile: model.SandboxProfileEnterprise})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := enterprise.(*EnterpriseDriver); !ok {
		t.Fatalf("enterprise profile should use microVM driver, got %T", enterprise)
	}
}

func TestNewSandboxRunnerRejectsUnknownProfile(t *testing.T) {
	if _, err := NewSandboxRunner(SandboxRunnerConfig{Profile: "unknown"}); err == nil {
		t.Fatal("expected unsupported profile to be rejected")
	}
}
