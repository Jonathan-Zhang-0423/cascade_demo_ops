package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestCandidateExtensionPrefersResponseContentType(t *testing.T) {
	for _, test := range []struct {
		contentType string
		url         string
		want        string
	}{
		{"video/mp4", "https://asset.example/no-extension", ".mp4"},
		{"image/jpeg; charset=binary", "https://asset.example/generated.mp4", ".jpg"},
		{"", "https://asset.example/generated.webp?token=redacted", ".webp"},
		{"application/octet-stream", "https://asset.example/no-extension", ".bin"},
	} {
		if got := candidateExtension(test.contentType, test.url); got != test.want {
			t.Fatalf("candidateExtension(%q, %q)=%q want %q", test.contentType, test.url, got, test.want)
		}
	}
}

func TestCandidateArtifactManifestCarriesLocalIntegrityEvidence(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "candidate-01.mp4")
	normalized := filepath.Join(dir, "candidate-01.normalized.mp4")
	if err := os.WriteFile(original, []byte("provider-video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(normalized, []byte("normalized-video"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := candidateArtifactManifest([]string{filepath.Base(original)}, []string{filepath.Base(normalized)}, dir)
	if len(manifest) != 2 {
		t.Fatalf("manifest count=%d want 2", len(manifest))
	}
	if manifest[0]["normalized"] != false || manifest[0]["sha256"] == "" || manifest[0]["size_bytes"] != int64(len("provider-video")) {
		t.Fatalf("original evidence=%+v", manifest[0])
	}
	if manifest[1]["normalized"] != true || manifest[1]["sha256"] == "" || manifest[1]["sha256"] == manifest[0]["sha256"] {
		t.Fatalf("normalized evidence=%+v", manifest[1])
	}
	if !candidateArtifactManifestVerified(manifest) {
		t.Fatal("complete manifest should be integrity verified")
	}
	if candidateArtifactManifestVerified(append(manifest, map[string]any{"integrity_error": "missing"})) {
		t.Fatal("manifest with integrity error must fail closed")
	}
}

func TestSeedancePreflightSourcePackageIDIsTimeScoped(t *testing.T) {
	created := time.Date(2026, time.August, 17, 10, 0, 0, 123, time.UTC)
	if got, want := seedancePreflightSourcePackageID(created), "seedance_preflight_1786960800000000123"; got != want {
		t.Fatalf("seedancePreflightSourcePackageID()=%q want %q", got, want)
	}
}

func TestNormalizedCandidatePathUsesIndependentMP4Artifact(t *testing.T) {
	got := normalizedCandidatePath(`D:\artifacts\candidate-01.mp4`)
	if want := `D:\artifacts\candidate-01.normalized.mp4`; got != want {
		t.Fatalf("normalizedCandidatePath()=%q want %q", got, want)
	}
}

func TestReadExistingAuditPreservesPriorSubmissionEvidence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seedance_audit.json")
	content := `{"schema_version":"cascade.seedance_preflight.v1","created_at":"2026-08-17T10:00:00Z","publication":{"can_use_for_real_call":true},"request_trace":{"http_status":200}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	audit, err := readExistingAudit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if audit["publication"] == nil || audit["request_trace"] == nil || audit["created_at"] == nil {
		t.Fatalf("prior audit evidence was not preserved: %+v", audit)
	}
}

func TestProbeSeedanceReferenceRejectsVideoOutsideProviderDurationLimit(t *testing.T) {
	dir := t.TempDir()
	probe := filepath.Join(dir, "ffprobe.cmd")
	if err := os.WriteFile(probe, []byte("@echo {\"format\":{\"duration\":\"60.209\"},\"streams\":[{\"codec_type\":\"video\",\"codec_name\":\"h264\"}]}\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := probeSeedanceReference(context.Background(), probe, filepath.Join(dir, "source.mp4")); err == nil {
		t.Fatal("expected long reference to be rejected")
	}
}

func TestPublicationRequiresRetentionAck(t *testing.T) {
	result := model.ArkAssetPublicationResult{Blockers: []model.ArkMediaReadinessFinding{{Code: "tos_retention_client_ack_required"}}}
	if !publicationRequiresRetentionAck(result) {
		t.Fatal("expected retention acknowledgement blocker to be detected")
	}
	result.Blockers = []model.ArkMediaReadinessFinding{{Code: "tos_upload_failed"}}
	if publicationRequiresRetentionAck(result) {
		t.Fatal("unrelated publication failure must not be reported as retention acknowledgement")
	}
}
