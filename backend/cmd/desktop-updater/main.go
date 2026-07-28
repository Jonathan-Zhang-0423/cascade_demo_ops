package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cascade-demoops/backend/internal/desktopupdate"
)

func main() {
	check := flag.Bool("check", false, "report updater readiness without network access")
	manifestURL := flag.String("manifest-url", "", "HTTPS URL of the signed update manifest")
	publicKeyPath := flag.String("public-key", "", "Ed25519 release public key PEM")
	channel := flag.String("channel", "stable", "release channel")
	stagingDir := flag.String("staging-dir", "", "directory for resumable update downloads")
	apply := flag.Bool("apply", false, "run the verified installer after download")
	jsonOutput := flag.Bool("json", false, "emit a machine-readable verified update result")
	previousInstaller := flag.String("previous-installer", "", "verified previous installer used for rollback")
	appCheck := flag.String("app-check", "", "installed app executable checked after upgrade")
	flag.Parse()
	if *check {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"app": "Cascade DemoOps Updater", "ready": true, "verifies": []string{"https", "ed25519", "sha256", "authenticode"}, "rollback": true,
		})
		return
	}
	if err := run(*manifestURL, *publicKeyPath, *channel, *stagingDir, *apply, *previousInstaller, *appCheck, *jsonOutput); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(manifestURL, publicKeyPath, channel, stagingDir string, apply bool, previousInstaller, appCheck string, jsonOutput bool) error {
	if !strings.HasPrefix(manifestURL, "https://") {
		return errors.New("manifest URL must use HTTPS")
	}
	publicKey, err := readPublicKey(publicKeyPath)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != "https" {
				return errors.New("update redirect must remain HTTPS")
			}
			return nil
		},
	}
	manifestBytes, err := downloadSmall(client, manifestURL, 2<<20)
	if err != nil {
		return err
	}
	signatureBytes, err := downloadSmall(client, manifestURL+".sig", 64<<10)
	if err != nil {
		return err
	}
	manifest, err := desktopupdate.VerifyManifest(manifestBytes, signatureBytes, publicKey, channel)
	if err != nil {
		return err
	}
	if stagingDir == "" {
		stagingDir = filepath.Join(os.TempDir(), "CascadeDemoOps", "updates", channel)
	}
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return err
	}
	artifactPath := filepath.Join(stagingDir, manifest.Artifact.FileName)
	if desktopupdate.VerifyArtifactFile(artifactPath, manifest.Artifact) != nil {
		partialPath := artifactPath + ".part"
		if err := downloadResumable(client, manifest.Artifact.URL, partialPath, manifest.Artifact.SizeBytes); err != nil {
			return err
		}
		if err := desktopupdate.VerifyArtifactFile(partialPath, manifest.Artifact); err != nil {
			return err
		}
		if err := replaceStagedArtifact(partialPath, artifactPath); err != nil {
			return err
		}
	}
	if runtime.GOOS == "windows" {
		if err := verifyAuthenticode(artifactPath); err != nil {
			return err
		}
	}
	if !apply {
		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"verified": true, "version": manifest.Version, "channel": manifest.Channel,
				"release_notes": manifest.ReleaseNotes, "artifact_file_name": manifest.Artifact.FileName,
				"size_bytes": manifest.Artifact.SizeBytes,
			})
		}
		fmt.Printf("verified update %s at %s\n", manifest.Version, artifactPath)
		return nil
	}
	if previousInstaller == "" || appCheck == "" {
		return errors.New("--apply requires --previous-installer and --app-check for rollback")
	}
	if runtime.GOOS == "windows" {
		if err := verifyAuthenticode(previousInstaller); err != nil {
			return fmt.Errorf("previous installer is not trusted: %w", err)
		}
	}
	rollbackInstaller := filepath.Join(stagingDir, "rollback-"+filepath.Base(previousInstaller))
	if err := copyFile(previousInstaller, rollbackInstaller); err != nil {
		return fmt.Errorf("stage rollback installer: %w", err)
	}
	if runtime.GOOS == "windows" {
		if err := verifyAuthenticode(rollbackInstaller); err != nil {
			return fmt.Errorf("staged rollback installer is not trusted: %w", err)
		}
	}
	if err := runInstaller(artifactPath); err != nil {
		_ = runInstaller(rollbackInstaller)
		return fmt.Errorf("upgrade failed and rollback was attempted: %w", err)
	}
	if err := runAppCheck(appCheck); err != nil {
		_ = runInstaller(rollbackInstaller)
		return fmt.Errorf("post-upgrade health check failed and rollback was attempted: %w", err)
	}
	return nil
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func readPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("update public key is not PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	publicKey, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("update public key is not Ed25519")
	}
	return publicKey, nil
}

func downloadSmall(client *http.Client, endpoint string, limit int64) ([]byte, error) {
	response, err := client.Get(endpoint)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("GET update metadata returned %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, limit))
}

func downloadResumable(client *http.Client, endpoint, destination string, expectedSize int64) error {
	offset := int64(0)
	if info, err := os.Stat(destination); err == nil {
		offset = info.Size()
	}
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("GET update artifact returned %d", response.StatusCode)
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if response.StatusCode == http.StatusOK {
		offset = 0
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	file, err := os.OpenFile(destination, flags, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, response.Body)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if offset+written != expectedSize {
		return fmt.Errorf("incomplete update download: got %d want %d", offset+written, expectedSize)
	}
	return nil
}

func replaceStagedArtifact(partialPath, artifactPath string) error {
	if err := os.Remove(artifactPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(partialPath, artifactPath)
}

func verifyAuthenticode(path string) error {
	command := fmt.Sprintf("$s=Get-AuthenticodeSignature -LiteralPath '%s'; if ($s.Status -ne 'Valid') { throw ('invalid Authenticode status: ' + $s.Status) }", strings.ReplaceAll(path, "'", "''"))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("verify Authenticode: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func runInstaller(path string) error {
	cmd := exec.Command(path, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/CLOSEAPPLICATIONS")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func runAppCheck(path string) error {
	cmd := exec.Command(path, "--check")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return errors.New("app health check timed out")
	}
}
