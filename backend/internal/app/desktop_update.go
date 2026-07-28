package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cascade-demoops/backend/internal/config"
	"cascade-demoops/backend/internal/desktopupdate"
)

type DesktopUpdateStatus struct {
	Configured       bool   `json:"configured"`
	CurrentVersion   string `json:"current_version,omitempty"`
	AvailableVersion string `json:"available_version,omitempty"`
	Channel          string `json:"channel,omitempty"`
	ReleaseNotes     string `json:"release_notes,omitempty"`
	ArtifactFileName string `json:"artifact_file_name,omitempty"`
	SizeBytes        int64  `json:"size_bytes,omitempty"`
	UpdateAvailable  bool   `json:"update_available"`
	InstallReady     bool   `json:"install_ready"`
}

type updaterResult struct {
	Verified         bool   `json:"verified"`
	Version          string `json:"version"`
	Channel          string `json:"channel"`
	ReleaseNotes     string `json:"release_notes"`
	ArtifactFileName string `json:"artifact_file_name"`
	SizeBytes        int64  `json:"size_bytes"`
}

var runDesktopUpdater = runUpdater

func CheckDesktopUpdate(ctx context.Context, runtime config.AppRuntimeConfig) (DesktopUpdateStatus, error) {
	status, err := desktopUpdateConfiguration(runtime)
	if err != nil || !status.Configured {
		return status, err
	}
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	output, err := runDesktopUpdater(commandCtx, runtime, false)
	if err != nil {
		return status, err
	}
	var result updaterResult
	if err := json.Unmarshal(output, &result); err != nil || !result.Verified {
		return status, errors.New("updater returned an invalid verification result")
	}
	comparison, err := desktopupdate.CompareVersions(result.Version, status.CurrentVersion)
	if err != nil {
		return status, err
	}
	status.AvailableVersion = result.Version
	status.Channel = result.Channel
	status.ReleaseNotes = result.ReleaseNotes
	status.ArtifactFileName = result.ArtifactFileName
	status.SizeBytes = result.SizeBytes
	status.UpdateAvailable = comparison > 0
	status.InstallReady = status.UpdateAvailable && updateRollbackConfigured(runtime)
	return status, nil
}

func ApplyDesktopUpdate(ctx context.Context, runtime config.AppRuntimeConfig) error {
	status, err := CheckDesktopUpdate(ctx, runtime)
	if err != nil {
		return err
	}
	if !status.UpdateAvailable {
		return errors.New("no newer verified update is available")
	}
	if !status.InstallReady {
		return errors.New("update rollback paths are not configured")
	}
	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	_, err = runDesktopUpdater(commandCtx, runtime, true)
	return err
}

func desktopUpdateConfiguration(runtime config.AppRuntimeConfig) (DesktopUpdateStatus, error) {
	status := DesktopUpdateStatus{
		CurrentVersion: strings.TrimSpace(runtime.AppVersion),
		Channel:        strings.TrimSpace(runtime.UpdateChannel),
	}
	values := []string{runtime.UpdateManifestURL, runtime.UpdatePublicKeyPath, runtime.UpdateExecutablePath, status.CurrentVersion, status.Channel}
	configured := true
	for _, value := range values {
		configured = configured && strings.TrimSpace(value) != ""
	}
	status.Configured = configured
	if !configured {
		return status, nil
	}
	if !strings.HasPrefix(runtime.UpdateManifestURL, "https://") {
		return status, errors.New("update manifest URL must use HTTPS")
	}
	if _, err := os.Stat(runtime.UpdateExecutablePath); err != nil {
		return status, errors.New("desktop updater is unavailable")
	}
	if _, err := os.Stat(runtime.UpdatePublicKeyPath); err != nil {
		return status, errors.New("update public key is unavailable")
	}
	return status, nil
}

func updateRollbackConfigured(runtime config.AppRuntimeConfig) bool {
	for _, path := range []string{runtime.PreviousInstallerPath, runtime.DesktopExecutablePath} {
		if strings.TrimSpace(path) == "" {
			return false
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func runUpdater(ctx context.Context, runtimeConfig config.AppRuntimeConfig, apply bool) ([]byte, error) {
	stagingDir := filepath.Join(runtimeConfig.CacheRoot, "updates", runtimeConfig.UpdateChannel)
	updaterPath := runtimeConfig.UpdateExecutablePath
	if runtime.GOOS == "windows" && runtimeConfig.UpdateChannel != "internal" {
		if err := verifyDesktopUpdateAuthenticode(updaterPath); err != nil {
			return nil, err
		}
	}
	if apply {
		var err error
		updaterPath, err = stageDesktopUpdater(runtimeConfig.UpdateExecutablePath, stagingDir)
		if err != nil {
			return nil, err
		}
		if runtime.GOOS == "windows" && runtimeConfig.UpdateChannel != "internal" {
			if err := verifyDesktopUpdateAuthenticode(updaterPath); err != nil {
				return nil, err
			}
		}
	}
	args := []string{
		"--manifest-url", runtimeConfig.UpdateManifestURL,
		"--public-key", runtimeConfig.UpdatePublicKeyPath,
		"--channel", runtimeConfig.UpdateChannel,
		"--staging-dir", stagingDir,
	}
	if apply {
		args = append(args, "--apply", "--previous-installer", runtimeConfig.PreviousInstallerPath, "--app-check", runtimeConfig.DesktopExecutablePath)
	} else {
		args = append(args, "--json")
	}
	command := exec.CommandContext(ctx, updaterPath, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("desktop update failed: %s", redactBridgeError(message))
	}
	return stdout.Bytes(), nil
}

func stageDesktopUpdater(source, stagingDir string) (string, error) {
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return "", err
	}
	destination := filepath.Join(stagingDir, "cascade-demoops-updater"+filepath.Ext(source))
	temporary := destination + ".new"
	input, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer input.Close()
	output, err := os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(destination)
		if err := os.Rename(temporary, destination); err != nil {
			return "", err
		}
	}
	return destination, nil
}

func verifyDesktopUpdateAuthenticode(path string) error {
	command := fmt.Sprintf("$s=Get-AuthenticodeSignature -LiteralPath '%s'; if ($s.Status -ne 'Valid') { throw 'invalid updater signature' }", strings.ReplaceAll(path, "'", "''"))
	result := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
	if output, err := result.CombinedOutput(); err != nil {
		return fmt.Errorf("desktop updater signature verification failed: %s", redactBridgeError(strings.TrimSpace(string(output))))
	}
	return nil
}
