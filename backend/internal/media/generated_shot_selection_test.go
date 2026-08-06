package media

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func reviewedH3CandidateForSelection(t *testing.T) GeneratedShotReviewedCandidate {
	t.Helper()
	candidate := validGeneratedShotCandidateForReview(t)
	structural := ReviewGeneratedShotCandidateStructure("structure_001", validGeneratedShotIntent(), candidate)
	content, err := RecordGeneratedShotContentReview(structural, validGeneratedShotContentDecision())
	if err != nil {
		t.Fatal(err)
	}
	return GeneratedShotReviewedCandidate{Candidate: candidate, ContentReview: content}
}

func reviewedSeedanceCandidateForSelection(t *testing.T) GeneratedShotReviewedCandidate {
	t.Helper()
	item := reviewedH3CandidateForSelection(t)
	item.Candidate.CandidateID = "candidate_seedance_001"
	item.Candidate.Provider = GeneratedShotProviderSeedance20
	item.Candidate.ProviderTaskID = "task_seedance_001"
	item.Candidate.OriginalArtifact.Path = filepath.Join(t.TempDir(), "seedance-original.mp4")
	item.Candidate.NormalizedArtifact.Path = filepath.Join(t.TempDir(), "seedance-normalized.mp4")
	item.Candidate.OriginalArtifact.SHA256 = strings.Repeat("c", 64)
	item.Candidate.NormalizedArtifact.SHA256 = strings.Repeat("d", 64)
	item.ContentReview.ReviewID = "content_seedance_001"
	item.ContentReview.CandidateID = item.Candidate.CandidateID
	return item
}

func TestGeneratedShotNormalSetRequiresOneReviewedCandidate(t *testing.T) {
	set, err := NewGeneratedShotCandidateSet("set_001", validGeneratedShotIntent(), GeneratedShotCandidateSetModeNormal, []GeneratedShotReviewedCandidate{reviewedH3CandidateForSelection(t)})
	if err != nil {
		t.Fatal(err)
	}
	if set.Status != GeneratedShotCandidateSetReadyForSelection || !set.SelectionRequired || set.Executable || set.ApprovedForDemo || set.IncludeInDemo || set.AutoApply {
		t.Fatalf("candidate set safety = %+v", set)
	}
	if _, err := NewGeneratedShotCandidateSet("set_002", validGeneratedShotIntent(), GeneratedShotCandidateSetModeNormal, nil); err == nil {
		t.Fatal("expected empty normal set to be rejected")
	}
}

func TestGeneratedShotComparisonRequiresSeedanceAndH3ForSameIntent(t *testing.T) {
	set, err := NewGeneratedShotCandidateSet("set_ab", validGeneratedShotIntent(), GeneratedShotCandidateSetModeComparison, []GeneratedShotReviewedCandidate{
		reviewedSeedanceCandidateForSelection(t), reviewedH3CandidateForSelection(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Candidates) != 2 || set.Mode != GeneratedShotCandidateSetModeComparison {
		t.Fatalf("comparison set = %+v", set)
	}
	twoH3 := []GeneratedShotReviewedCandidate{reviewedH3CandidateForSelection(t), reviewedH3CandidateForSelection(t)}
	twoH3[1].Candidate.CandidateID = "candidate_h3_002"
	twoH3[1].Candidate.ProviderTaskID = "task_h3_002"
	twoH3[1].Candidate.OriginalArtifact.Path = filepath.Join(t.TempDir(), "h3-2-original.mp4")
	twoH3[1].Candidate.NormalizedArtifact.Path = filepath.Join(t.TempDir(), "h3-2-normalized.mp4")
	twoH3[1].Candidate.OriginalArtifact.SHA256 = strings.Repeat("e", 64)
	twoH3[1].Candidate.NormalizedArtifact.SHA256 = strings.Repeat("f", 64)
	twoH3[1].ContentReview.CandidateID = twoH3[1].Candidate.CandidateID
	twoH3[1].ContentReview.ReviewID = "content_h3_002"
	if _, err := NewGeneratedShotCandidateSet("set_invalid", validGeneratedShotIntent(), GeneratedShotCandidateSetModeComparison, twoH3); err == nil {
		t.Fatal("expected same-provider A/B set to be rejected")
	}
}

func TestGeneratedShotCandidateSetRejectsDuplicateOutputAndUnapprovedReview(t *testing.T) {
	seedance := reviewedSeedanceCandidateForSelection(t)
	h3 := reviewedH3CandidateForSelection(t)
	seedance.Candidate.NormalizedArtifact.SHA256 = h3.Candidate.NormalizedArtifact.SHA256
	if _, err := NewGeneratedShotCandidateSet("set_duplicate", validGeneratedShotIntent(), GeneratedShotCandidateSetModeComparison, []GeneratedShotReviewedCandidate{seedance, h3}); err == nil {
		t.Fatal("expected duplicate normalized output to be rejected")
	}
	h3 = reviewedH3CandidateForSelection(t)
	h3.ContentReview.Status = GeneratedShotContentRejected
	h3.ContentReview.ContentApproved = false
	if _, err := NewGeneratedShotCandidateSet("set_unreviewed", validGeneratedShotIntent(), GeneratedShotCandidateSetModeNormal, []GeneratedShotReviewedCandidate{h3}); err == nil {
		t.Fatal("expected unapproved content review to be rejected")
	}
}

func TestGeneratedShotSelectionRequiresHumanEvidenceAndSetMembership(t *testing.T) {
	set, err := NewGeneratedShotCandidateSet("set_001", validGeneratedShotIntent(), GeneratedShotCandidateSetModeNormal, []GeneratedShotReviewedCandidate{reviewedH3CandidateForSelection(t)})
	if err != nil {
		t.Fatal(err)
	}
	decision := validGeneratedShotSelectionDecision()
	decision.SelectorKind = "model"
	if _, err := RecordGeneratedShotSelection(set, decision); err == nil {
		t.Fatal("expected model selection to be rejected")
	}
	decision = validGeneratedShotSelectionDecision()
	decision.CandidateID = "candidate_outside_set"
	if _, err := RecordGeneratedShotSelection(set, decision); err == nil {
		t.Fatal("expected candidate outside set to be rejected")
	}
	decision = validGeneratedShotSelectionDecision()
	decision.EvidenceRefs = nil
	if _, err := RecordGeneratedShotSelection(set, decision); err == nil {
		t.Fatal("expected evidence-free selection to be rejected")
	}
}

func TestGeneratedShotSelectionRemainsPendingEditorApproval(t *testing.T) {
	set, err := NewGeneratedShotCandidateSet("set_001", validGeneratedShotIntent(), GeneratedShotCandidateSetModeNormal, []GeneratedShotReviewedCandidate{reviewedH3CandidateForSelection(t)})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := RecordGeneratedShotSelection(set, validGeneratedShotSelectionDecision())
	if err != nil {
		t.Fatal(err)
	}
	if selection.Status != GeneratedShotSelectedPendingEditorApproval || !selection.EditorApprovalRequired {
		t.Fatalf("selection = %+v", selection)
	}
	if selection.ApprovedForDemo || selection.IncludeInDemo || selection.AutoApply {
		t.Fatalf("selection cannot approve or apply candidate: %+v", selection)
	}
}

func TestGeneratedShotSelectionRevalidatesSerializedCandidateSet(t *testing.T) {
	set, err := NewGeneratedShotCandidateSet("set_001", validGeneratedShotIntent(), GeneratedShotCandidateSetModeNormal, []GeneratedShotReviewedCandidate{reviewedH3CandidateForSelection(t)})
	if err != nil {
		t.Fatal(err)
	}
	set.Executable = true
	if _, err := RecordGeneratedShotSelection(set, validGeneratedShotSelectionDecision()); err == nil {
		t.Fatal("expected executable set to be rejected")
	}
	set.Executable = false
	set.Candidates[0].NormalizedSHA256 = "not-a-sha256"
	if _, err := RecordGeneratedShotSelection(set, validGeneratedShotSelectionDecision()); err == nil {
		t.Fatal("expected tampered digest to be rejected")
	}
}

func TestGeneratedShotCandidateSetDoesNotEnableFallbackExecution(t *testing.T) {
	if _, err := NewGeneratedShotCandidateSet("set_fallback", validGeneratedShotIntent(), "fallback", []GeneratedShotReviewedCandidate{reviewedH3CandidateForSelection(t)}); err == nil {
		t.Fatal("expected fallback execution mode to remain unsupported")
	}
}

func validGeneratedShotSelectionDecision() GeneratedShotSelectionDecision {
	return GeneratedShotSelectionDecision{
		SelectionID: "selection_001", SetID: "set_001", CandidateID: "candidate_001",
		SelectorID: "selector_001", SelectorKind: "human", SelectedAt: time.Date(2026, 8, 5, 17, 30, 0, 0, time.UTC),
		EvidenceRefs: []string{"comparison-contact-sheet-001"}, Reason: "candidate best matches the approved presentation-only intent",
	}
}
