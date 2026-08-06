package executor

import (
	"path/filepath"
	"testing"
	"time"

	"cascade-demoops/backend/internal/model"
)

func TestReviewArkMediaCandidateAssetsKeepsSafeDownloadedVideoCandidatePendingExplicitReview(t *testing.T) {
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
			},
		}},
	}

	review := ReviewArkMediaCandidateAssets(&source, &generationResult, time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC))

	if review.SchemaVersion != model.CandidateAssetReviewSchemaVersion || review.Status != "media_eligible_awaiting_user_review" || len(review.ApprovedArtifacts) != 0 || len(review.PendingReviewArtifacts) != 1 {
		t.Fatalf("expected media-eligible candidate awaiting user review, got %+v", review)
	}
	pending := generationResult.DownloadedArtifacts[0]
	if pending.Metadata["approved_for_demo"] != false || pending.Metadata["media_eligible"] != true || pending.Metadata["explicit_review_required"] != true || pending.Metadata["presentation_only"] != true || pending.Metadata["include_in_demo"] != false {
		t.Fatalf("pending candidate metadata mismatch: %+v", pending.Metadata)
	}
	if review.Items[0].ApprovedForDemo || !review.Items[0].MediaEligible || !review.Items[0].ExplicitReviewRequired || !review.Items[0].PresentationOnly {
		t.Fatalf("review item did not capture explicit-review boundary: %+v", review.Items[0])
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
