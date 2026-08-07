package media

import "testing"

func TestCompileGeneratedShotEditPlanPatchProposalUsesUnifiedAssetRef(t *testing.T) {
	intent, candidate, set, selection := generatedShotApprovalFixture(t)
	approval, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, validGeneratedShotEditorApprovalDecision())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := CompileGeneratedShotEditorAssetRef(intent, candidate, set, selection, approval)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := CompileGeneratedShotEditPlanPatchProposal(intent, candidate, set, selection, approval, ref)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Status != GeneratedShotEditPlanPatchProposed || proposal.ApplicationMode != GeneratedShotEditPlanPatchApplicationMode || proposal.AssetRefID != ref.AssetRefID || proposal.DurationMS != 5000 {
		t.Fatalf("proposal = %+v", proposal)
	}
	if proposal.AutoApply || proposal.ApprovedForDemo || proposal.IncludeInDemo || !proposal.RequiresExplicitOptIn || !proposal.RequiresRendererReview || !proposal.MustNotBindSourceStep {
		t.Fatalf("proposal safety = %+v", proposal)
	}
	if err := ValidateGeneratedShotEditPlanPatchProposal(proposal); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedShotEditPlanPatchProposalRejectsMismatchedPlanOrAsset(t *testing.T) {
	intent, candidate, set, selection := generatedShotApprovalFixture(t)
	approval, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, validGeneratedShotEditorApprovalDecision())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := CompileGeneratedShotEditorAssetRef(intent, candidate, set, selection, approval)
	if err != nil {
		t.Fatal(err)
	}
	ref.TargetPlanID = "other_plan"
	if _, err := CompileGeneratedShotEditPlanPatchProposal(intent, candidate, set, selection, approval, ref); err == nil {
		t.Fatal("expected target plan mismatch to be rejected")
	}
	ref, err = CompileGeneratedShotEditorAssetRef(intent, candidate, set, selection, approval)
	if err != nil {
		t.Fatal(err)
	}
	ref.AssetRefID = "https://provider.example.test/candidate.mp4"
	if _, err := CompileGeneratedShotEditPlanPatchProposal(intent, candidate, set, selection, approval, ref); err == nil {
		t.Fatal("expected provider URL asset ref to be rejected")
	}
}

func TestGeneratedShotEditPlanPatchProposalRejectsApplyAuthority(t *testing.T) {
	intent, candidate, set, selection := generatedShotApprovalFixture(t)
	approval, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, validGeneratedShotEditorApprovalDecision())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := CompileGeneratedShotEditorAssetRef(intent, candidate, set, selection, approval)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := CompileGeneratedShotEditPlanPatchProposal(intent, candidate, set, selection, approval, ref)
	if err != nil {
		t.Fatal(err)
	}
	proposal.AutoApply = true
	if err := ValidateGeneratedShotEditPlanPatchProposal(proposal); err == nil {
		t.Fatal("expected auto-apply authority to be rejected")
	}
}
