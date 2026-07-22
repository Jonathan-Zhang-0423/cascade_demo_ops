package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var payloadMarker = []byte("CASCADE_DEMOOPS_INSTALLER_PAYLOAD_V1")

type payloadTrailer struct {
	SchemaVersion string `json:"schema_version"`
	App           string `json:"app"`
	Version       string `json:"version"`
	TargetOS      string `json:"target_os"`
	TargetArch    string `json:"target_arch"`
	PackageKind   string `json:"package_kind"`
	PayloadName   string `json:"payload_name"`
	PayloadSize   int64  `json:"payload_size"`
	PayloadSHA256 string `json:"payload_sha256"`
}

type installManifest struct {
	SchemaVersion string              `json:"schema_version"`
	App           string              `json:"app"`
	Version       string              `json:"version"`
	InstalledAt   string              `json:"installed_at"`
	InstallDir    string              `json:"install_dir"`
	Entrypoint    string              `json:"entrypoint"`
	Payload       payloadTrailer      `json:"payload"`
	Shortcuts     []string            `json:"shortcuts"`
	Server        serverConnectivity  `json:"server_connectivity"`
	Files         []installedFileInfo `json:"files"`
}

type serverConnectivity struct {
	RequiredForLocalGeneration bool     `json:"required_for_local_generation"`
	ReservedInterfaces         []string `json:"reserved_interfaces"`
}

type installedFileInfo struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

func main() {
	installDir := flag.String("install-dir", defaultInstallDir(), "directory where Cascade DemoOps Desktop will be installed")
	check := flag.Bool("check", false, "validate that this setup executable contains an installable payload and exit")
	quiet := flag.Bool("quiet", false, "print machine-readable JSON and avoid interactive prompts")
	launch := flag.Bool("launch", true, "launch Cascade DemoOps Desktop after installation")
	noShortcut := flag.Bool("no-shortcut", false, "skip Start Menu launcher creation")
	flag.Parse()

	payload, trailer, err := readEmbeddedPayload()
	must(err)
	if *check {
		writeJSON(os.Stdout, map[string]any{
			"installer":        "Cascade DemoOps Desktop Setup",
			"ready":            true,
			"payload_embedded": true,
			"version":          trailer.Version,
			"target_os":        trailer.TargetOS,
			"target_arch":      trailer.TargetArch,
			"payload_name":     trailer.PayloadName,
			"payload_sha256":   trailer.PayloadSHA256,
			"payload_size":     trailer.PayloadSize,
		})
		return
	}

	result, err := installPayload(payload, trailer, *installDir, !*noShortcut)
	must(err)
	if *quiet {
		writeJSON(os.Stdout, map[string]any{
			"installed":   true,
			"install_dir": result.InstallDir,
			"entrypoint":  result.Entrypoint,
			"manifest":    filepath.Join(result.InstallDir, "install-manifest.json"),
		})
	} else {
		fmt.Printf("Cascade DemoOps Desktop installed to %s\n", result.InstallDir)
		fmt.Printf("Entrypoint: %s\n", result.Entrypoint)
	}
	if *launch {
		_ = openURL(result.Entrypoint)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func writeJSON(w io.Writer, value any) {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	must(encoder.Encode(value))
}

func defaultInstallDir() string {
	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, "Programs", "CascadeDemoOps")
		}
	}
	if configDir, err := os.UserConfigDir(); err == nil && configDir != "" {
		return filepath.Join(configDir, "CascadeDemoOps")
	}
	return filepath.Join(".", "CascadeDemoOps")
}

func readEmbeddedPayload() ([]byte, payloadTrailer, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, payloadTrailer{}, err
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		return nil, payloadTrailer{}, err
	}
	if len(data) < len(payloadMarker)+8 || !bytes.HasSuffix(data, payloadMarker) {
		return nil, payloadTrailer{}, errors.New("setup executable does not contain a Cascade DemoOps payload")
	}
	lengthOffset := len(data) - len(payloadMarker) - 8
	trailerLen := int(binary.BigEndian.Uint64(data[lengthOffset : lengthOffset+8]))
	trailerOffset := lengthOffset - trailerLen
	if trailerLen <= 0 || trailerOffset < 0 {
		return nil, payloadTrailer{}, errors.New("setup payload trailer is malformed")
	}
	var trailer payloadTrailer
	if err := json.Unmarshal(data[trailerOffset:lengthOffset], &trailer); err != nil {
		return nil, payloadTrailer{}, fmt.Errorf("decode setup payload trailer: %w", err)
	}
	payloadOffset := trailerOffset - int(trailer.PayloadSize)
	if trailer.PayloadSize <= 0 || payloadOffset < 0 {
		return nil, payloadTrailer{}, errors.New("setup payload size is malformed")
	}
	payload := data[payloadOffset:trailerOffset]
	sum := sha256.Sum256(payload)
	actual := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actual, trailer.PayloadSHA256) {
		return nil, payloadTrailer{}, fmt.Errorf("setup payload checksum mismatch: %s != %s", actual, trailer.PayloadSHA256)
	}
	if trailer.SchemaVersion != "demoops.desktop_installer_payload.v1" {
		return nil, payloadTrailer{}, fmt.Errorf("unsupported setup payload schema: %s", trailer.SchemaVersion)
	}
	return payload, trailer, nil
}

func installPayload(payload []byte, trailer payloadTrailer, installDir string, createShortcut bool) (installManifest, error) {
	absInstallDir, err := filepath.Abs(filepath.Clean(installDir))
	if err != nil {
		return installManifest{}, err
	}
	if err := os.MkdirAll(absInstallDir, 0o755); err != nil {
		return installManifest{}, err
	}
	files, err := extractZip(payload, absInstallDir)
	if err != nil {
		return installManifest{}, err
	}
	entrypoint := filepath.Join(absInstallDir, "cascade-demoops-desktop.exe")
	if runtime.GOOS != "windows" {
		entrypoint = filepath.Join(absInstallDir, "cascade-demoops-desktop")
	}
	if _, err := os.Stat(entrypoint); err != nil {
		return installManifest{}, fmt.Errorf("installed desktop entrypoint is missing: %w", err)
	}
	shortcuts := []string{}
	if createShortcut {
		if shortcut, err := writeStartMenuLauncher(entrypoint); err == nil && shortcut != "" {
			shortcuts = append(shortcuts, shortcut)
		}
	}
	uninstallScript, err := writeUninstallScript(absInstallDir, shortcuts)
	if err != nil {
		return installManifest{}, err
	}
	files = append(files, fileInfo(absInstallDir, uninstallScript))
	manifest := installManifest{
		SchemaVersion: "demoops.desktop_install_manifest.v1",
		App:           "Cascade DemoOps",
		Version:       trailer.Version,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		InstallDir:    absInstallDir,
		Entrypoint:    entrypoint,
		Payload:       trailer,
		Shortcuts:     shortcuts,
		Server: serverConnectivity{
			RequiredForLocalGeneration: false,
			ReservedInterfaces: []string{
				"ExchangeCapabilityResolver",
				"ExchangeIdentityStore",
				"ExchangeSessionManager",
				"CloudLifecycleClient",
			},
		},
		Files: files,
	}
	manifestPath := filepath.Join(absInstallDir, "install-manifest.json")
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return installManifest{}, err
	}
	if err := os.WriteFile(manifestPath, append(manifestBytes, '\n'), 0o644); err != nil {
		return installManifest{}, err
	}
	manifest.Files = append(manifest.Files, fileInfo(absInstallDir, manifestPath))
	return manifest, nil
}

func extractZip(payload []byte, destination string) ([]installedFileInfo, error) {
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		return nil, err
	}
	files := []installedFileInfo{}
	for _, entry := range reader.File {
		entryPath := strings.ReplaceAll(entry.Name, "\\", "/")
		target := filepath.Join(destination, filepath.FromSlash(entryPath))
		if !pathWithin(target, destination) {
			return nil, fmt.Errorf("zip entry escapes install dir: %s", entry.Name)
		}
		if isZipDirectory(entry) {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		src, err := entry.Open()
		if err != nil {
			return nil, err
		}
		err = writeFileFromReader(target, src, entry.FileInfo().Mode())
		closeErr := src.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		files = append(files, fileInfo(destination, target))
	}
	return files, nil
}

func isZipDirectory(entry *zip.File) bool {
	return entry.FileInfo().IsDir() || strings.HasSuffix(entry.Name, "/") || strings.HasSuffix(entry.Name, "\\")
}

func writeFileFromReader(path string, reader io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, reader)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func fileInfo(root string, path string) installedFileInfo {
	info, err := os.Stat(path)
	if err != nil {
		return installedFileInfo{Path: slash(relativePath(root, path))}
	}
	return installedFileInfo{
		Path:      slash(relativePath(root, path)),
		SizeBytes: info.Size(),
		SHA256:    sha256File(path),
	}
}

func relativePath(root string, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

func sha256File(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func pathWithin(path string, root string) bool {
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func writeUninstallScript(installDir string, shortcuts []string) (string, error) {
	path := filepath.Join(installDir, "Uninstall-CascadeDemoOps.ps1")
	shortcutJSON, err := json.Marshal(shortcuts)
	if err != nil {
		return "", err
	}
	content := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$InstallDir = %q
$KnownShortcuts = ConvertFrom-Json @'
%s
'@
$ManifestPath = Join-Path $InstallDir 'install-manifest.json'
if (-not (Test-Path -LiteralPath $ManifestPath)) {
  throw "Cascade DemoOps install manifest is missing: $ManifestPath"
}
$Manifest = Get-Content -Raw -LiteralPath $ManifestPath | ConvertFrom-Json
if ($Manifest.schema_version -ne 'demoops.desktop_install_manifest.v1') {
  throw "Refusing to uninstall unknown install schema: $($Manifest.schema_version)"
}
if ($Manifest.app -ne 'Cascade DemoOps') {
  throw "Refusing to uninstall unknown app: $($Manifest.app)"
}
foreach ($Shortcut in @($KnownShortcuts)) {
  if ($Shortcut -and (Test-Path -LiteralPath $Shortcut)) {
    Remove-Item -LiteralPath $Shortcut -Force
  }
}
if ($Manifest.shortcuts) {
  foreach ($Shortcut in @($Manifest.shortcuts)) {
    if ($Shortcut -and (Test-Path -LiteralPath $Shortcut)) {
      Remove-Item -LiteralPath $Shortcut -Force
    }
  }
}
Remove-Item -LiteralPath $InstallDir -Recurse -Force
Write-Host "Cascade DemoOps Desktop uninstalled from $InstallDir"
`, installDir, string(shortcutJSON))
	return path, os.WriteFile(path, []byte(content), 0o644)
}

func writeStartMenuLauncher(entrypoint string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", nil
	}
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", errors.New("APPDATA is not set")
	}
	dir := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Cascade DemoOps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	launcher := filepath.Join(dir, "Cascade DemoOps.cmd")
	content := fmt.Sprintf("@echo off\r\nstart \"\" %q\r\n", entrypoint)
	return launcher, os.WriteFile(launcher, []byte(content), 0o644)
}

func openURL(path string) error {
	if runtime.GOOS == "windows" {
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", path).Start()
	}
	return errors.New("launch is only supported by the Windows installer")
}

func slash(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}
