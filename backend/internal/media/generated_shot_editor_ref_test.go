package media

import "testing"

func TestCompileGeneratedShotEditorAssetRefUnifiesH3CandidateForEditor(t *testing.T) {
	intent, candidate, set, selection := generatedShotApprovalFixture(t)
	approval, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, validGeneratedShotEditorApprovalDecision())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := CompileGeneratedShotEditorAssetRef(intent, candidate, set, selection, approval)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != GeneratedShotEditorAssetKind || ref.Provider != GeneratedShotProviderMiniMaxH3 || ref.MimeType != "video/mp4" || ref.Width != 1920 || ref.Height != 1080 || ref.FPS != 30 || !ref.ApprovedForDemo {
		t.Fatalf("editor ref = %+v", ref)
	}
	if ref.IncludeInDemo || ref.AutoApply || ref.SourceMaterialPolicy != GeneratedShotSourceMaterialPolicy {
		t.Fatalf("editor ref safety = %+v", ref)
	}
	if err := ValidateGeneratedShotEditorAssetRef(ref); err != nil {
		t.Fatal(err)
	}
}

func TestCompileGeneratedShotEditorAssetRefRejectsProviderURLAndDigestMismatch(t *testing.T) {
	intent, candidate, set, selection := generatedShotApprovalFixture(t)
	approval, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, validGeneratedShotEditorApprovalDecision())
	if err != nil {
		t.Fatal(err)
	}
	candidate.NormalizedArtifact.Path = "https://provider.example.test/temporary.mp4"
	if _, err := CompileGeneratedShotEditorAssetRef(intent, candidate, set, selection, approval); err == nil {
		t.Fatal("expected temporary provider URL to be rejected")
	}
	candidate = validGeneratedShotCandidateForReview(t)
	candidate.CandidateID = "candidate_other"
	if _, err := CompileGeneratedShotEditorAssetRef(intent, candidate, set, selection, approval); err == nil {
		t.Fatal("expected candidate digest or identity mismatch to be rejected")
	}
}

func TestGeneratedShotEditorAssetRefRejectsAutoApplyAndNonNormalizedMedia(t *testing.T) {
	intent, candidate, set, selection := generatedShotApprovalFixture(t)
	approval, err := RecordGeneratedShotEditorApproval(intent, candidate, set, selection, validGeneratedShotEditorApprovalDecision())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := CompileGeneratedShotEditorAssetRef(intent, candidate, set, selection, approval)
	if err != nil {
		t.Fatal(err)
	}
	ref.AutoApply = true
	if err := ValidateGeneratedShotEditorAssetRef(ref); err == nil {
		t.Fatal("expected auto-apply reference to be rejected")
	}
	ref.AutoApply = false
	ref.FPS = 24
	if err := ValidateGeneratedShotEditorAssetRef(ref); err == nil {
		t.Fatal("expected non-CFR30 reference to be rejected")
	}
}
