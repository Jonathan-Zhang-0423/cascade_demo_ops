package media

import (
	"path/filepath"
	"strings"
	"testing"
)

func validMiniMaxH3CandidatePipelineResult(t *testing.T) MiniMaxH3PipelineResult {
	t.Helper()
	root := t.TempDir()
	return MiniMaxH3PipelineResult{
		TaskID: "task_h3_candidate", Status: GeneratedShotCandidateReadyForReview,
		FailurePolicy: GeneratedShotFailureContinue,
		OriginalArtifact: &MiniMaxH3Artifact{
			Role: "original", Path: filepath.Join(root, "original.mp4"), MimeType: "video/mp4",
			SHA256: strings.Repeat("a", 64), SizeBytes: 100,
			Probe:        &MiniMaxH3MediaProbe{Format: "mov,mp4", VideoCodec: "hevc", Width: 2048, Height: 1152, FPS: 24, CFR: true, DurationSec: 5},
			Presentation: true, Authoritative: false, SourceTaskID: "task_h3_candidate",
		},
		Normalized: &MiniMaxH3Artifact{
			Role: "normalized", Path: filepath.Join(root, "normalized.mp4"), MimeType: "video/mp4",
			SHA256: strings.Repeat("b", 64), SizeBytes: 90,
			Probe:        &MiniMaxH3MediaProbe{Format: "mov,mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPS: 30, CFR: true, DurationSec: 5},
			Presentation: true, Authoritative: false, SourceTaskID: "task_h3_candidate",
		},
	}
}

func TestMiniMaxH3PipelineConvertsToProviderNeutralReviewCandidate(t *testing.T) {
	candidate, err := NewGeneratedShotCandidateFromMiniMaxH3("candidate_001", "shot_001", validMiniMaxH3CandidatePipelineResult(t))
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Provider != GeneratedShotProviderMiniMaxH3 || candidate.ApprovedForDemo || candidate.IncludeInDemo || !candidate.RequiresExplicitReview {
		t.Fatalf("candidate safety flags = %+v", candidate)
	}
	if candidate.NormalizedArtifact.NormalizationProfile != GeneratedShotNormalizationProfile {
		t.Fatalf("normalized artifact = %+v", candidate.NormalizedArtifact)
	}
	if err := ValidateGeneratedShotCandidate(candidate); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedShotCandidateCannotBeApprovedByNormalization(t *testing.T) {
	candidate, err := NewGeneratedShotCandidateFromMiniMaxH3("candidate_001", "shot_001", validMiniMaxH3CandidatePipelineResult(t))
	if err != nil {
		t.Fatal(err)
	}
	candidate.ApprovedForDemo = true
	if err := ValidateGeneratedShotCandidate(candidate); err == nil {
		t.Fatal("expected normalization-stage approval to be rejected")
	}
	candidate.ApprovedForDemo = false
	candidate.IncludeInDemo = true
	if err := ValidateGeneratedShotCandidate(candidate); err == nil {
		t.Fatal("expected automatic timeline inclusion to be rejected")
	}
}

func TestGeneratedShotCandidateRejectsTemporaryURLAndNonNormalizedMedia(t *testing.T) {
	candidate, err := NewGeneratedShotCandidateFromMiniMaxH3("candidate_001", "shot_001", validMiniMaxH3CandidatePipelineResult(t))
	if err != nil {
		t.Fatal(err)
	}
	candidate.NormalizedArtifact.Path = "https://provider.example.test/temporary.mp4"
	if err := ValidateGeneratedShotCandidate(candidate); err == nil {
		t.Fatal("expected provider URL to be rejected")
	}
	candidate, err = NewGeneratedShotCandidateFromMiniMaxH3("candidate_002", "shot_001", validMiniMaxH3CandidatePipelineResult(t))
	if err != nil {
		t.Fatal(err)
	}
	candidate.NormalizedArtifact.Probe.FPS = 24
	if err := ValidateGeneratedShotCandidate(candidate); err == nil {
		t.Fatal("expected non-CFR30 candidate to be rejected")
	}
}

func TestGeneratedShotCandidateRejectsSameOriginalAndNormalizedArtifact(t *testing.T) {
	candidate, err := NewGeneratedShotCandidateFromMiniMaxH3("candidate_001", "shot_001", validMiniMaxH3CandidatePipelineResult(t))
	if err != nil {
		t.Fatal(err)
	}
	candidate.NormalizedArtifact.SHA256 = candidate.OriginalArtifact.SHA256
	if err := ValidateGeneratedShotCandidate(candidate); err == nil {
		t.Fatal("expected identical original and normalized digests to be rejected")
	}
}

func TestGeneratedShotCandidateRejectsIncompleteH3Pipeline(t *testing.T) {
	result := validMiniMaxH3CandidatePipelineResult(t)
	result.Status = "failed"
	result.ErrorClass = "media_normalization_failed"
	if _, err := NewGeneratedShotCandidateFromMiniMaxH3("candidate_001", "shot_001", result); err == nil {
		t.Fatal("expected incomplete H3 pipeline to be rejected")
	}
}

func TestGeneratedShotCandidateRejectsUnsafeH3PipelineAuthority(t *testing.T) {
	result := validMiniMaxH3CandidatePipelineResult(t)
	result.Normalized.Authoritative = true
	if _, err := NewGeneratedShotCandidateFromMiniMaxH3("candidate_001", "shot_001", result); err == nil {
		t.Fatal("expected authoritative H3 artifact to be rejected")
	}
	result = validMiniMaxH3CandidatePipelineResult(t)
	result.OriginalArtifact.SourceTaskID = "other_task"
	if _, err := NewGeneratedShotCandidateFromMiniMaxH3("candidate_001", "shot_001", result); err == nil {
		t.Fatal("expected task audit mismatch to be rejected")
	}
}
