package media

import (
	"testing"
	"time"
)

func generatedShotApprovalFixture(t *testing.T) (GeneratedShotIntent, GeneratedShotCandidate, GeneratedShotCandidateSet, GeneratedShotSelection) {
	t.Helper()
	intent := validGeneratedShotIntent()
	reviewed := reviewedH3CandidateForSelection(t)
	set, err := NewGeneratedShotCandidateSet("set_001", intent, GeneratedShotCandidateSetModeNormal, []GeneratedShotReviewedCandidate{reviewed})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := RecordGeneratedShotSelection(set, validGeneratedShotSelectionDecision())
	if err != nil {
		t.Fatal(err)
	}
	return intent, reviewed.Candidate, set, selection
}

func TestGeneratedShotEditorApprovalAuthorizesPatchCreationOnly(t *testing.T) {
	intent, candidate, set, selection := generatedShotApprovalFixture(t)
	approval, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, validGeneratedShotEditorApprovalDecision())
	if err != nil {
		t.Fatal(err)
	}
	if approval.Status != GeneratedShotEditorApprovedPendingPatch || !approval.PatchCreationAuthorized || !approval.ApprovedForDemo {
		t.Fatalf("approval = %+v", approval)
	}
	if approval.PatchApplyAuthorized || approval.RendererAuthorized || approval.IncludeInDemo || approval.AutoApply || !approval.MustNotBindSourceStep {
		t.Fatalf("approval authority widened unexpectedly: %+v", approval)
	}
}

func TestGeneratedShotEditorApprovalRequiresHumanPlanBindingAndEvidence(t *testing.T) {
	intent, candidate, set, selection := generatedShotApprovalFixture(t)
	decision := validGeneratedShotEditorApprovalDecision()
	decision.ApproverKind = "model"
	if _, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, decision); err == nil {
		t.Fatal("expected model editor approval to be rejected")
	}
	decision = validGeneratedShotEditorApprovalDecision()
	decision.ExpectedPlanRevision = 0
	if _, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, decision); err == nil {
		t.Fatal("expected missing plan revision to be rejected")
	}
	decision = validGeneratedShotEditorApprovalDecision()
	decision.EvidenceRefs = nil
	if _, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, decision); err == nil {
		t.Fatal("expected missing approval evidence to be rejected")
	}
}

func TestGeneratedShotEditorApprovalEnforcesPurposePlacement(t *testing.T) {
	intent, candidate, set, selection := generatedShotApprovalFixture(t)
	decision := validGeneratedShotEditorApprovalDecision()
	decision.Placement = "after_last_required_step"
	if _, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, decision); err == nil {
		t.Fatal("expected intro candidate at outro placement to be rejected")
	}
}

func TestGeneratedShotEditorApprovalRejectsCandidateSummaryMismatch(t *testing.T) {
	intent, candidate, set, selection := generatedShotApprovalFixture(t)
	candidate.NormalizedArtifact.SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, validGeneratedShotEditorApprovalDecision()); err == nil {
		t.Fatal("expected selected candidate digest mismatch to be rejected")
	}
}

func TestGeneratedShotEditorApprovalRevalidatesSerializedSelection(t *testing.T) {
	intent, candidate, set, selection := generatedShotApprovalFixture(t)
	selection.AutoApply = true
	if _, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, validGeneratedShotEditorApprovalDecision()); err == nil {
		t.Fatal("expected tampered selection to be rejected")
	}
}

func validGeneratedShotEditorApprovalDecision() GeneratedShotEditorApprovalDecision {
	return GeneratedShotEditorApprovalDecision{
		ApprovalID: "editor_approval_001", SelectionID: "selection_001", CandidateID: "candidate_001",
		ApproverID: "editor_reviewer_001", ApproverKind: "human", ApprovedAt: time.Date(2026, 8, 5, 18, 0, 0, 0, time.UTC),
		TargetPlanID: "demo_edit_plan_001", ExpectedPlanRevision: 3, Placement: "before_first_required_step",
		EvidenceRefs: []string{"editor-preview-001"}, Reason: "approved as an optional presentation-only intro candidate",
	}
}
