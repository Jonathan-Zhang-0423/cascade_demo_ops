package desktopupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestVerifyManifestAndArtifact(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	artifact := []byte("signed installer")
	digest := sha256.Sum256(artifact)
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion, App: "Cascade DemoOps", Channel: "beta", Version: "1.2.3", MinimumVersion: "1.0.0", PublishedAt: "2026-07-27T00:00:00Z",
		Artifact:     ManifestArtifact{URL: "https://updates.demoops.example/beta/setup.exe", FileName: "setup.exe", SizeBytes: int64(len(artifact)), SHA256: hex.EncodeToString(digest[:])},
		ReleaseNotes: "test", Signature: ManifestSignature{Algorithm: "Ed25519", KeyID: "release-1"},
	}
	canonical, _ := canonicalManifest(manifest)
	signature := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonical)))
	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")

	verified, err := VerifyManifest(manifestBytes, signature, publicKey, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if verified.Version != "1.2.3" {
		t.Fatalf("unexpected version %q", verified.Version)
	}
	if err := VerifyArtifact(artifact, verified.Artifact); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyManifestRejectsHTTPAndTampering(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion, App: "Cascade DemoOps", Channel: "stable", Version: "1.0.0", MinimumVersion: "1.0.0",
		Artifact:  ManifestArtifact{URL: "http://updates.example/setup.exe", FileName: "setup.exe", SizeBytes: 1, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Signature: ManifestSignature{Algorithm: "Ed25519", KeyID: "release-1"},
	}
	canonical, _ := canonicalManifest(manifest)
	signature := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonical)))
	manifestBytes, _ := json.Marshal(manifest)
	if _, err := VerifyManifest(manifestBytes, signature, publicKey, "stable"); err == nil {
		t.Fatal("HTTP update origin must be rejected")
	}
	manifest.Artifact.URL = "https://updates.example/setup.exe"
	manifestBytes, _ = json.Marshal(manifest)
	if _, err := VerifyManifest(manifestBytes, signature, publicKey, "stable"); err == nil {
		t.Fatal("tampered manifest must be rejected")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"1.2.0", "1.1.9", 1},
		{"1.2.0-beta.1", "1.2.0", -1},
		{"1.2.0", "1.2.0+build.2", 0},
	}
	for _, test := range cases {
		got, err := CompareVersions(test.left, test.right)
		if err != nil || got != test.want {
			t.Fatalf("CompareVersions(%q, %q) = %d, %v; want %d", test.left, test.right, got, err, test.want)
		}
	}
}

func TestVerifyArtifactFile(t *testing.T) {
	artifact := []byte("streamed installer")
	digest := sha256.Sum256(artifact)
	path := t.TempDir() + "/setup.exe"
	if err := os.WriteFile(path, artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyArtifactFile(path, ManifestArtifact{SizeBytes: int64(len(artifact)), SHA256: hex.EncodeToString(digest[:])}); err != nil {
		t.Fatal(err)
	}
}
