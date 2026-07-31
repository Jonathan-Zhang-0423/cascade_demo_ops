package desktopupdate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const ManifestSchemaVersion = "demoops.desktop_update.v1"

type Manifest struct {
	SchemaVersion  string            `json:"schema_version"`
	App            string            `json:"app"`
	Channel        string            `json:"channel"`
	Version        string            `json:"version"`
	MinimumVersion string            `json:"minimum_version"`
	PublishedAt    string            `json:"published_at"`
	Artifact       ManifestArtifact  `json:"artifact"`
	ReleaseNotes   string            `json:"release_notes"`
	Signature      ManifestSignature `json:"signature"`
}

type ManifestArtifact struct {
	URL       string `json:"url"`
	FileName  string `json:"file_name"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

type ManifestSignature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
}

func VerifyManifest(manifestBytes, signatureBytes []byte, publicKey ed25519.PublicKey, expectedChannel string) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode update manifest: %w", err)
	}
	if err := validateManifest(manifest, expectedChannel); err != nil {
		return Manifest{}, err
	}
	canonical, err := canonicalManifest(manifest)
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signatureBytes)))
	if err != nil {
		return Manifest{}, fmt.Errorf("decode update signature: %w", err)
	}
	if len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(publicKey, canonical, signature) {
		return Manifest{}, errors.New("update manifest signature is invalid")
	}
	return manifest, nil
}

func canonicalManifest(manifest Manifest) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(manifest); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func VerifyArtifact(data []byte, artifact ManifestArtifact) error {
	if int64(len(data)) != artifact.SizeBytes {
		return fmt.Errorf("update artifact size mismatch: got %d want %d", len(data), artifact.SizeBytes)
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), artifact.SHA256) {
		return errors.New("update artifact SHA-256 mismatch")
	}
	return nil
}

func VerifyArtifactFile(path string, artifact ManifestArtifact) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() != artifact.SizeBytes {
		return fmt.Errorf("update artifact size mismatch: got %d want %d", info.Size(), artifact.SizeBytes)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), artifact.SHA256) {
		return errors.New("update artifact SHA-256 mismatch")
	}
	return nil
}

func validateManifest(manifest Manifest, expectedChannel string) error {
	if manifest.SchemaVersion != ManifestSchemaVersion || manifest.App != "Cascade DemoOps" {
		return errors.New("unsupported update manifest")
	}
	if manifest.Channel != expectedChannel || !validChannel(manifest.Channel) {
		return fmt.Errorf("update channel mismatch: got %q want %q", manifest.Channel, expectedChannel)
	}
	if manifest.Signature.Algorithm != "Ed25519" || strings.TrimSpace(manifest.Signature.KeyID) == "" {
		return errors.New("update manifest must declare an Ed25519 key id")
	}
	if !validVersion(manifest.Version) || !validVersion(manifest.MinimumVersion) {
		return errors.New("update manifest contains an invalid version")
	}
	parsed, err := url.Parse(manifest.Artifact.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("update artifact URL must use HTTPS without embedded credentials")
	}
	if manifest.Artifact.SizeBytes <= 0 || !regexp.MustCompile(`^[a-fA-F0-9]{64}$`).MatchString(manifest.Artifact.SHA256) {
		return errors.New("update artifact size and SHA-256 are required")
	}
	if manifest.Artifact.FileName == "" || strings.ContainsAny(manifest.Artifact.FileName, `/\\`) {
		return errors.New("update artifact file name is invalid")
	}
	return nil
}

func validChannel(channel string) bool {
	return channel == "internal" || channel == "beta" || channel == "stable"
}

func validVersion(version string) bool {
	return regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`).MatchString(strings.TrimSpace(version))
}

// CompareVersions compares the numeric core first and treats a prerelease as older than its release.
func CompareVersions(left, right string) (int, error) {
	if !validVersion(left) || !validVersion(right) {
		return 0, errors.New("invalid desktop version")
	}
	leftCore, leftPre := splitVersion(left)
	rightCore, rightPre := splitVersion(right)
	for index := 0; index < 3; index++ {
		leftPart, _ := strconv.Atoi(leftCore[index])
		rightPart, _ := strconv.Atoi(rightCore[index])
		if leftPart < rightPart {
			return -1, nil
		}
		if leftPart > rightPart {
			return 1, nil
		}
	}
	if leftPre == rightPre {
		return 0, nil
	}
	if leftPre == "" {
		return 1, nil
	}
	if rightPre == "" {
		return -1, nil
	}
	return strings.Compare(leftPre, rightPre), nil
}

func splitVersion(version string) ([]string, string) {
	withoutBuild := strings.SplitN(strings.TrimSpace(version), "+", 2)[0]
	parts := strings.SplitN(withoutBuild, "-", 2)
	pre := ""
	if len(parts) == 2 {
		pre = parts[1]
	}
	return strings.Split(parts[0], "."), pre
}
