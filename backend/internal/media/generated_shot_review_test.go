package media

import (
	"testing"
	"time"
)

func validGeneratedShotCandidateForReview(t *testing.T) GeneratedShotCandidate {
	t.Helper()
	candidate, err := NewGeneratedShotCandidateFromMiniMaxH3("candidate_001", "shot_001", validMiniMaxH3CandidatePipelineResult(t))
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func TestGeneratedShotStructuralReviewAllowsOnlyContentReview(t *testing.T) {
	review := ReviewGeneratedShotCandidateStructure("structure_001", validGeneratedShotIntent(), validGeneratedShotCandidateForReview(t))
	if !review.StructurallyEligible || review.Status != GeneratedShotStructuralEligible || !review.ContentReviewRequired {
		t.Fatalf("structural review = %+v", review)
	}
	if review.ApprovedForDemo || review.IncludeInDemo {
		t.Fatalf("structural review cannot approve or include: %+v", review)
	}
}

func TestGeneratedShotStructuralReviewRejectsIntentAndDurationMismatch(t *testing.T) {
	intent := validGeneratedShotIntent()
	candidate := validGeneratedShotCandidateForReview(t)
	candidate.IntentID = "other_intent"
	candidate.NormalizedArtifact.Probe.DurationSec = 8
	review := ReviewGeneratedShotCandidateStructure("structure_001", intent, candidate)
	if review.StructurallyEligible || review.Status != GeneratedShotStructuralRejected || len(review.Findings) < 2 {
		t.Fatalf("structural review = %+v", review)
	}
}

func TestGeneratedShotStructuralReviewRejectsNormalizationDurationDrift(t *testing.T) {
	candidate := validGeneratedShotCandidateForReview(t)
	candidate.OriginalArtifact.Probe.DurationSec = 5
	candidate.NormalizedArtifact.Probe.DurationSec = 5.5
	review := ReviewGeneratedShotCandidateStructure("structure_001", validGeneratedShotIntent(), candidate)
	if review.StructurallyEligible || review.Status != GeneratedShotStructuralRejected {
		t.Fatalf("expected duration drift rejection: %+v", review)
	}
}

func TestGeneratedShotContentApprovalRequiresHumanEvidenceAndAllAssertions(t *testing.T) {
	structural := ReviewGeneratedShotCandidateStructure("structure_001", validGeneratedShotIntent(), validGeneratedShotCandidateForReview(t))
	decision := validGeneratedShotContentDecision()
	decision.ReviewerKind = "model"
	if _, err := RecordGeneratedShotContentReview(structural, decision); err == nil {
		t.Fatal("expected model self-approval to be rejected")
	}
	decision = validGeneratedShotContentDecision()
	decision.Assertions.NoCapturedUIReplacement = false
	if _, err := RecordGeneratedShotContentReview(structural, decision); err == nil {
		t.Fatal("expected unsafe content assertion to be rejected")
	}
	decision = validGeneratedShotContentDecision()
	decision.EvidenceRefs = nil
	if _, err := RecordGeneratedShotContentReview(structural, decision); err == nil {
		t.Fatal("expected missing review evidence to be rejected")
	}
}

func TestGeneratedShotContentApprovalRemainsPendingSelection(t *testing.T) {
	structural := ReviewGeneratedShotCandidateStructure("structure_001", validGeneratedShotIntent(), validGeneratedShotCandidateForReview(t))
	review, err := RecordGeneratedShotContentReview(structural, validGeneratedShotContentDecision())
	if err != nil {
		t.Fatal(err)
	}
	if !review.ContentApproved || review.Status != GeneratedShotContentApprovedPendingSelection || !review.SelectionRequired {
		t.Fatalf("content review = %+v", review)
	}
	if review.ApprovedForDemo || review.IncludeInDemo {
		t.Fatalf("content approval cannot select or include candidate: %+v", review)
	}
}

func TestGeneratedShotContentRejectionRequiresReason(t *testing.T) {
	structural := ReviewGeneratedShotCandidateStructure("structure_001", validGeneratedShotIntent(), validGeneratedShotCandidateForReview(t))
	decision := validGeneratedShotContentDecision()
	decision.Decision = GeneratedShotContentDecisionReject
	decision.Reason = ""
	if _, err := RecordGeneratedShotContentReview(structural, decision); err == nil {
		t.Fatal("expected rejection reason to be required")
	}
	decision.Reason = "candidate contains product UI-like controls"
	review, err := RecordGeneratedShotContentReview(structural, decision)
	if err != nil {
		t.Fatal(err)
	}
	if review.ContentApproved || review.Status != GeneratedShotContentRejected || review.IncludeInDemo {
		t.Fatalf("rejected content review = %+v", review)
	}
}

func validGeneratedShotContentDecision() GeneratedShotContentReviewDecision {
	return GeneratedShotContentReviewDecision{
		ReviewID: "content_001", StructuralReviewID: "structure_001", CandidateID: "candidate_001", IntentID: "shot_001",
		ReviewerID: "reviewer_001", ReviewerKind: "human", ReviewedAt: time.Date(2026, 8, 5, 17, 0, 0, 0, time.UTC),
		Decision: GeneratedShotContentDecisionApprove,
		Assertions: GeneratedShotContentSafetyAssertions{
			PurposeMatchesIntent: true, NoCapturedUIReplacement: true, NoBusinessFactClaims: true,
			NoUnverifiedTextOrNumbers: true, NoReferenceFactMutation: true,
		},
		EvidenceRefs: []string{"review-frame-contact-sheet-001"},
	}
}
