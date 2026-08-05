package executor

import (
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestReviewArkMediaCandidateAssetsApprovesSafeDownloadedVideoCandidate(t *testing.T) {
	source := sampleClientExecutionPackageForExecutorTest(t)
	candidatePath := filepath.Join(t.TempDir(), "candidate.mp4")
	generationResult := model.ArkMediaGenerationResult{
		ResultID:        "ark_media_generation_result_pkg",
		SourcePackageID: source.PackageID,
		TaskID:          "task_1",
		DownloadedArtifacts: []model.ArtifactRef{{
			ID:        "artifact_candidate_001",
			Kind:      "generated_video_candidate",
			URI:       candidatePath,
			MimeType:  "video/mp4",
			SHA256:    "sha_candidate",
			SizeBytes: 128,
			Metadata: map[string]any{
				"asset_role":             "director_preview_candidate",
				"include_in_demo":        false,
				"source_material_policy": "non_authoritative_generated_candidate",
				"non_authoritative":      true,
				"artifact_variant":       "normalized",
				"normalization_status":   "ok",
				"media_probe_status":     "ok",
				"normalization_profile":  "editor_mp4_h264_yuv420p_1920x1080_cfr30_v1",
			},
		}},
	}

	review := ReviewArkMediaCandidateAssets(&source, &generationResult, time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC))

	if review.SchemaVersion != model.CandidateAssetReviewSchemaVersion || review.Status != "approved" || len(review.ApprovedArtifacts) != 1 {
		t.Fatalf("expected approved candidate review, got %+v", review)
	}
	approved := generationResult.DownloadedArtifacts[0]
	if approved.Metadata["approved_for_demo"] != true || approved.Metadata["presentation_only"] != true || approved.Metadata["include_in_demo"] != false {
		t.Fatalf("approved candidate metadata mismatch: %+v", approved.Metadata)
	}
	if review.Items[0].ApprovedForDemo != true || review.Items[0].PresentationOnly != true {
		t.Fatalf("review item did not capture approval boundary: %+v", review.Items[0])
	}
}

func TestReviewArkMediaCandidateAssetsRejectsDownloadedButUnnormalizedVideo(t *testing.T) {
	generationResult := model.ArkMediaGenerationResult{
		ResultID:        "ark_media_generation_result_pkg",
		SourcePackageID: "pkg_1",
		DownloadedArtifacts: []model.ArtifactRef{{
			ID:        "artifact_raw_provider_video",
			Kind:      "generated_video_candidate",
			URI:       filepath.Join(t.TempDir(), "provider-original.mp4"),
			MimeType:  "video/mp4",
			SHA256:    "sha_raw_provider_video",
			SizeBytes: 128,
			Metadata: map[string]any{
				"source_material_policy": "non_authoritative_generated_candidate",
				"non_authoritative":      true,
			},
		}},
	}

	review := ReviewArkMediaCandidateAssets(nil, &generationResult, time.Date(2026, 7, 15, 21, 3, 0, 0, time.UTC))

	if review.Status != "rejected" || len(review.ApprovedArtifacts) != 0 || len(review.RejectedArtifacts) != 1 {
		t.Fatalf("raw provider candidate must be rejected: %+v", review)
	}
	if len(review.Items[0].Findings) != 1 || review.Items[0].Findings[0].Code != "candidate_media_not_normalized" {
		t.Fatalf("expected normalization finding: %+v", review.Items[0].Findings)
	}
}

func TestReviewArkMediaCandidateAssetsRejectsUnsafeCandidates(t *testing.T) {
	generationResult := model.ArkMediaGenerationResult{
		ResultID:        "ark_media_generation_result_pkg",
		SourcePackageID: "pkg_1",
		DownloadedArtifacts: []model.ArtifactRef{{
			ID:        "artifact_remote_video",
			Kind:      "generated_video_candidate",
			URI:       "https://assets.example.com/generated.mp4",
			MimeType:  "video/mp4",
			SHA256:    "sha_remote",
			SizeBytes: 64,
			Metadata: map[string]any{
				"source_material_policy": "non_authoritative_generated_candidate",
				"non_authoritative":      true,
			},
		}, {
			ID:        "artifact_image",
			Kind:      "generated_image_candidate",
			URI:       filepath.Join(t.TempDir(), "candidate.png"),
			MimeType:  "image/png",
			SHA256:    "sha_image",
			SizeBytes: 64,
			Metadata: map[string]any{
				"source_material_policy": "non_authoritative_generated_candidate",
				"non_authoritative":      true,
			},
		}},
	}

	review := ReviewArkMediaCandidateAssets(nil, &generationResult, time.Date(2026, 7, 15, 21, 5, 0, 0, time.UTC))

	if review.Status != "rejected" || len(review.ApprovedArtifacts) != 0 || len(review.RejectedArtifacts) != 2 {
		t.Fatalf("unsafe candidates should be rejected: %+v", review)
	}
	for _, artifact := range generationResult.DownloadedArtifacts {
		if artifact.Metadata["approved_for_demo"] != false {
			t.Fatalf("rejected candidate should be explicitly unapproved: %+v", artifact.Metadata)
		}
	}
	if len(review.Items[0].Findings) == 0 || len(review.Items[1].Findings) == 0 {
		t.Fatalf("rejected candidates should include findings: %+v", review.Items)
	}
}
