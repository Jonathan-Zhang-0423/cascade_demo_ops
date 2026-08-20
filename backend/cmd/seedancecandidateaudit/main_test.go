package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cascade-demoops/backend/internal/executor"
	"cascade-demoops/backend/internal/media"
)

func TestGenerationResultFromAuditLeavesNormalizedCandidatePendingReview(t *testing.T) {
	audit := validCandidateAudit(t)
	result, err := generationResultFromAudit(audit)
	if err != nil {
		t.Fatal(err)
	}
	review := executor.ReviewArkMediaCandidateAssets(nil, &result, time.Unix(0, 0).UTC())
	if review.Status != "partially_media_eligible_awaiting_user_review" {
		t.Fatalf("review status = %q", review.Status)
	}
	if len(review.PendingReviewArtifacts) != 1 || len(review.ApprovedArtifacts) != 0 || len(review.RejectedArtifacts) != 1 {
		t.Fatalf("review decisions pending=%d approved=%d rejected=%d", len(review.PendingReviewArtifacts), len(review.ApprovedArtifacts), len(review.RejectedArtifacts))
	}
	pending := review.PendingReviewArtifacts[0]
	if pending.Metadata["approved_for_demo"] != false || pending.Metadata["include_in_demo"] != false || pending.Metadata["presentation_only"] != true {
		t.Fatalf("pending candidate safety metadata = %#v", pending.Metadata)
	}
	if pending.Metadata["provider"] != "seedance" || pending.Metadata["model"] != "doubao-seedance-test" || pending.Metadata["task_id"] != "task_001" {
		t.Fatalf("pending candidate must retain provider provenance: %#v", pending.Metadata)
	}
	patch := executor.NewCandidateAssetEditPlanPatch(nil, nil, &review, time.Unix(0, 0).UTC())
	if patch.Status != "no_approved_candidates" || len(patch.ProposedShots) != 0 {
		t.Fatalf("pending review must not create a patch: %#v", patch)
	}
}

func TestGenerationResultFromAuditRejectsChangedArtifactDigest(t *testing.T) {
	audit := validCandidateAudit(t)
	if err := os.WriteFile(audit.Artifacts[1].Path, []byte("tampered candidate!!"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := generationResultFromAudit(audit)
	if err == nil || !strings.Contains(err.Error(), "sha256 does not match") {
		t.Fatalf("expected digest failure, got %v", err)
	}
}

func TestGenerationResultFromAuditRejectsNormalizedCandidateWithoutProbe(t *testing.T) {
	audit := validCandidateAudit(t)
	audit.Probes = nil
	_, err := generationResultFromAudit(audit)
	if err == nil || !strings.Contains(err.Error(), "has no recorded media probe") {
		t.Fatalf("expected missing probe failure, got %v", err)
	}
}

func TestReadAuditRejectsNonSeedanceOrNonNormalizedStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seedance_audit.json")
	if err := os.WriteFile(path, []byte(`{"provider":"other","status":"candidate_downloaded_and_normalized","task_id":"task","source_package_id":"pkg"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAudit(path); err == nil || !strings.Contains(err.Error(), "not Seedance") {
		t.Fatalf("expected provider rejection, got %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"provider":"seedance","status":"pending","task_id":"task","source_package_id":"pkg"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAudit(path); err == nil || !strings.Contains(err.Error(), "not a normalized") {
		t.Fatalf("expected status rejection, got %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"provider":"seedance","status":"candidate_downloaded_and_normalized","provider_status":"running","task_id":"task","source_package_id":"pkg"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAudit(path); err == nil || !strings.Contains(err.Error(), "status is not succeeded") {
		t.Fatalf("expected provider status rejection, got %v", err)
	}
}

func TestReadAuditAcceptsSeedance25CompletedCandidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seedance_audit.json")
	content := `{"provider":"seedance-2.5","model":"doubao-seedance-2-5-260628","status":"candidate_downloaded_and_normalized","provider_status":"succeeded","task_id":"task-25","source_package_id":"pkg-25"}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	audit, err := readAudit(path)
	if err != nil || audit.Provider != "seedance-2.5" {
		t.Fatalf("Seedance 2.5 audit should be accepted: audit=%+v err=%v", audit, err)
	}
}

func validCandidateAudit(t *testing.T) preflightAudit {
	t.Helper()
	dir := t.TempDir()
	original := writeCandidateFile(t, filepath.Join(dir, "candidate.mp4"), []byte("provider candidate"), false)
	normalized := writeCandidateFile(t, filepath.Join(dir, "candidate.normalized.mp4"), []byte("normalized candidate"), true)
	return preflightAudit{
		CreatedAt: time.Unix(0, 0).UTC(), SourcePackageID: "pkg_001", Provider: "seedance", Model: "doubao-seedance-test", TaskID: "task_001", ProviderStatus: "succeeded", Status: "candidate_downloaded_and_normalized",
		Artifacts: []auditArtifact{original, normalized},
		Probes:    []auditProbe{{SourceFile: original.File, NormalizedFile: normalized.File, OriginalProbe: media.MiniMaxH3MediaProbe{Format: "mov,mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPS: 24, CFR: true, DurationSec: 5}, NormalizedProbe: media.MiniMaxH3MediaProbe{Format: "mov,mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPS: 30, CFR: true, DurationSec: 5}}},
	}
}

func writeCandidateFile(t *testing.T, path string, content []byte, normalized bool) auditArtifact {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	return auditArtifact{File: filepath.Base(path), Path: path, SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(content)), Normalized: normalized}
}
