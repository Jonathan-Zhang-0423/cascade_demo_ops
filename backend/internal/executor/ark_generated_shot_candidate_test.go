package executor

import (
	"testing"
	"time"

	"cascade-demoops/backend/internal/media"
	"cascade-demoops/backend/internal/model"
)

func TestNewSeedanceGeneratedShotCandidateRequiresExplicitReview(t *testing.T) {
	original := model.ArtifactRef{ID: "original-1", URI: `C:\artifacts\original.mp4`, MimeType: "video/mp4", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 100, Metadata: map[string]any{"download_status": "downloaded"}}
	normalized := model.ArtifactRef{ID: "normalized-1", URI: `C:\artifacts\normalized.mp4`, MimeType: "video/mp4", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SizeBytes: 80, Metadata: map[string]any{
		"provider_original_artifact_id": "original-1", "artifact_variant": "normalized", "normalization_status": "ok", "normalization_profile": media.GeneratedShotNormalizationProfile,
		"presentation_only": true, "non_authoritative": true, "approved_for_demo": false, "include_in_demo": false,
		"normalized_media_probe": media.MiniMaxH3MediaProbe{Format: "mov,mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPS: 30, CFR: true, DurationSec: 5},
	}}
	result := model.ArkMediaGenerationResult{ResultID: "result-1", SourcePackageID: "package-1", SuggestionID: "suggestion-1", Provider: "seedance", TaskID: "task-1", Status: "candidate_artifacts_normalized", NonAuthoritative: true, DownloadedArtifacts: []model.ArtifactRef{original, normalized}, CreatedAt: time.Now()}
	candidate, err := NewSeedanceGeneratedShotCandidate(result, normalized)
	if err != nil {
		t.Fatal(err)
	}
	if err := media.ValidateGeneratedShotCandidate(candidate); err != nil {
		t.Fatal(err)
	}
	if candidate.ApprovedForDemo || candidate.IncludeInDemo || !candidate.RequiresExplicitReview {
		t.Fatalf("unsafe candidate authority: %+v", candidate)
	}
	if candidate.Provider != media.GeneratedShotProviderSeedance20 || candidate.ProviderTaskID != "task-1" {
		t.Fatalf("candidate identity: %+v", candidate)
	}
}

func TestNewSeedanceGeneratedShotCandidateRejectsMissingOriginal(t *testing.T) {
	normalized := model.ArtifactRef{ID: "normalized-1", URI: `C:\artifacts\normalized.mp4`, MimeType: "video/mp4", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SizeBytes: 80, Metadata: map[string]any{
		"provider_original_artifact_id": "missing", "artifact_variant": "normalized", "normalization_status": "ok", "normalization_profile": media.GeneratedShotNormalizationProfile,
		"presentation_only": true, "non_authoritative": true, "normalized_media_probe": media.MiniMaxH3MediaProbe{Format: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPS: 30, CFR: true, DurationSec: 5},
	}}
	_, err := NewSeedanceGeneratedShotCandidate(model.ArkMediaGenerationResult{Provider: "seedance", TaskID: "task-1", Status: "candidate_artifacts_normalized", NonAuthoritative: true, DownloadedArtifacts: []model.ArtifactRef{normalized}}, normalized)
	if err == nil {
		t.Fatal("expected missing original artifact to be rejected")
	}
}
