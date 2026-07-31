package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cascade-demoops/backend/internal/config"
)

func TestDesktopUpdateConfigurationIsExplicitAndRedacted(t *testing.T) {
	status, err := desktopUpdateConfiguration(config.AppRuntimeConfig{AppVersion: "1.0.0", UpdateChannel: "stable"})
	if err != nil || status.Configured {
		t.Fatalf("unconfigured update status = %+v, %v", status, err)
	}
	payload, _ := json.Marshal(status)
	if string(payload) == "" || containsLocalUpdatePath(string(payload)) {
		t.Fatalf("update status exposed local configuration: %s", payload)
	}
}

func TestCheckDesktopUpdateUsesVerifiedMachineReadableResult(t *testing.T) {
	root := t.TempDir()
	updater := filepath.Join(root, "updater.exe")
	if err := os.WriteFile(updater, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	publicKey := filepath.Join(root, "release.pem")
	if err := os.WriteFile(publicKey, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	previousRunner := runDesktopUpdater
	runDesktopUpdater = func(context.Context, config.AppRuntimeConfig, bool) ([]byte, error) {
		return []byte(`{"verified":true,"version":"1.1.0","channel":"stable","release_notes":"verified","artifact_file_name":"setup.exe","size_bytes":42}`), nil
	}
	t.Cleanup(func() { runDesktopUpdater = previousRunner })
	status, err := CheckDesktopUpdate(context.Background(), config.AppRuntimeConfig{
		AppVersion: "1.0.0", UpdateChannel: "stable", UpdateManifestURL: "https://updates.demoops.example/stable.json",
		UpdatePublicKeyPath: publicKey, UpdateExecutablePath: updater, CacheRoot: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !status.UpdateAvailable || status.AvailableVersion != "1.1.0" || status.InstallReady {
		t.Fatalf("unexpected verified update status: %+v", status)
	}
}

func containsLocalUpdatePath(payload string) bool {
	return filepath.IsAbs(payload) || len(payload) >= 3 && payload[1:3] == `:\`
}
