package executor

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"cascade-demoops/backend/internal/model"
)

func TestNormalizeArkVideoReferencesCreatesAuditableMP4Derivative(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "evidence.webm")
	if err := os.WriteFile(sourcePath, []byte("immutable evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	requirement := arkVideoReferenceRequirement(sourcePath)
	requirement.Ref.SHA256 = model.SHA256Hex([]byte("immutable evidence"))

	updated, findings := normalizeArkVideoReferences(t.Context(), []model.ArkMediaSourceAssetRequirement{requirement}, root, scriptedArkMediaNormalizer{normalizedBytes: []byte("bounded mp4 reference")})

	if len(findings) != 0 || len(updated) != 1 {
		t.Fatalf("unexpected result: updated=%+v findings=%+v", updated, findings)
	}
	ref := updated[0].Ref
	if ref.URI == sourcePath || ref.MimeType != "video/mp4" || ref.Kind != "model_reference_video" || ref.SHA256 == "" || ref.SizeBytes <= 0 {
		t.Fatalf("expected a distinct integrity-addressed MP4 derivative: %+v", ref)
	}
	if ref.Metadata["source_artifact_id"] != requirement.Ref.ID || ref.Metadata["source_sha256"] != requirement.Ref.SHA256 || ref.Metadata["max_duration_sec"] != seedanceReferenceDurationSec || ref.Metadata["model_reference_only"] != true {
		t.Fatalf("reference provenance metadata mismatch: %+v", ref.Metadata)
	}
	if updated[0].Status != "needs_public_uri" || updated[0].CurrentURIIsPublic {
		t.Fatalf("normalized reference should proceed to TOS publication: %+v", updated[0])
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil || string(data) != "immutable evidence" {
		t.Fatalf("source evidence must remain unchanged: data=%q err=%v", data, err)
	}
}

func TestNormalizeArkVideoReferencesFailureKeepsOriginalAndReportsFinding(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "evidence.webm")
	if err := os.WriteFile(sourcePath, []byte("immutable evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	requirement := arkVideoReferenceRequirement(sourcePath)

	updated, findings := normalizeArkVideoReferences(t.Context(), []model.ArkMediaSourceAssetRequirement{requirement}, root, scriptedArkMediaNormalizer{err: errors.New("ffmpeg unavailable")})

	if len(updated) != 1 || updated[0].Ref.URI != requirement.Ref.URI || updated[0].Ref.MimeType != "video/webm" {
		t.Fatalf("failed normalization must preserve the original requirement: %+v", updated)
	}
	if len(findings) != 1 || findings[0].Code != "seedance_reference_normalization_failed" || findings[0].RefID != requirement.Ref.ID {
		t.Fatalf("failed normalization must be traceable: %+v", findings)
	}
}

func arkVideoReferenceRequirement(sourcePath string) model.ArkMediaSourceAssetRequirement {
	fileURI := (&url.URL{Scheme: "file", Path: filepath.ToSlash(sourcePath)}).String()
	return model.ArkMediaSourceAssetRequirement{
		Ref:    model.DirectorMaterialRef{ID: "raw_recording_1", Kind: "raw_recording", URI: fileURI, MimeType: "video/webm", Sensitive: true},
		TaskID: "seedance_reference_director_preview", Usage: "reference_video_or_image", Required: true,
		AcceptedMimeTypes: []string{"video/mp4", "video/quicktime"}, RequiresPublicURI: true, Status: "unsupported_mime_type",
	}
}
