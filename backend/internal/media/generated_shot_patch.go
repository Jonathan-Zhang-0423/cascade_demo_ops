package media

import (
	"errors"
	"math"
	"strings"
)

const (
	GeneratedShotEditPlanPatchSchemaVersion   = "demoops.generated_shot_edit_plan_patch_proposal.v1"
	GeneratedShotEditPlanPatchProposed        = "proposed_pending_explicit_apply"
	GeneratedShotEditPlanPatchApplicationMode = "manual_or_explicit_opt_in_required"
)

// GeneratedShotEditPlanPatchProposal is a provider-neutral proposal that can
// later be translated into the existing CandidateAssetEditPlanPatch schema.
// It intentionally has no apply method, EditorSession handle, or source step.
type GeneratedShotEditPlanPatchProposal struct {
	SchemaVersion          string   `json:"schema_version"`
	PatchID                string   `json:"patch_id"`
	TargetPlanID           string   `json:"target_plan_id"`
	ExpectedPlanRevision   int      `json:"expected_plan_revision"`
	IntentID               string   `json:"intent_id"`
	CandidateID            string   `json:"candidate_id"`
	Provider               string   `json:"provider"`
	Status                 string   `json:"status"`
	ApplicationMode        string   `json:"application_mode"`
	AssetRefID             string   `json:"asset_ref_id"`
	Placement              string   `json:"placement"`
	AnchorAfterStepID      string   `json:"anchor_after_step_id,omitempty"`
	Purpose                string   `json:"purpose"`
	DurationMS             int      `json:"duration_ms"`
	RequiresExplicitOptIn  bool     `json:"requires_explicit_opt_in"`
	RequiresRendererReview bool     `json:"requires_renderer_validation"`
	PresentationOnly       bool     `json:"presentation_only"`
	NonAuthoritativeOnly   bool     `json:"non_authoritative_only"`
	MustNotBindSourceStep  bool     `json:"must_not_bind_source_step"`
	AutoApply              bool     `json:"auto_apply"`
	ApprovedForDemo        bool     `json:"approved_for_demo"`
	IncludeInDemo          bool     `json:"include_in_demo"`
	EditorApprovalID       string   `json:"editor_approval_id"`
	Warnings               []string `json:"warnings,omitempty"`
}

type GeneratedShotEditPlanPatchValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *GeneratedShotEditPlanPatchValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Field + ": " + e.Message
}

// CompileGeneratedShotEditPlanPatchProposal creates only a patch proposal.
// It does not mutate a plan, compare-and-swap a revision, import an asset, or
// invoke the editor. The caller must later perform those actions explicitly.
func CompileGeneratedShotEditPlanPatchProposal(intent GeneratedShotIntent, candidate GeneratedShotCandidate, set GeneratedShotCandidateSet, selection GeneratedShotSelection, approval GeneratedShotEditorApproval, editorRef GeneratedShotEditorAssetRef) (GeneratedShotEditPlanPatchProposal, error) {
	fail := func(field string, code string, message string) (GeneratedShotEditPlanPatchProposal, error) {
		return GeneratedShotEditPlanPatchProposal{}, &GeneratedShotEditPlanPatchValidationError{Field: field, Code: code, Message: message}
	}
	if err := ValidateGeneratedShotIntent(intent); err != nil {
		return fail("intent", "generated_patch_intent_invalid", err.Error())
	}
	if err := ValidateGeneratedShotCandidate(candidate); err != nil {
		return fail("candidate", "generated_patch_candidate_invalid", err.Error())
	}
	if err := ValidateGeneratedShotEditorApproval(set, selection, approval); err != nil {
		return fail("editor_approval", "generated_patch_approval_invalid", err.Error())
	}
	if err := ValidateGeneratedShotEditorAssetRef(editorRef); err != nil {
		return fail("editor_ref", "generated_patch_editor_ref_invalid", err.Error())
	}
	if intent.IntentID != candidate.IntentID || intent.IntentID != editorRef.IntentID || intent.IntentID != approval.IntentID {
		return fail("intent_id", "generated_patch_binding_mismatch", "intent, candidate, approval, and editor reference must match")
	}
	if candidate.CandidateID != selection.SelectedCandidateID || candidate.CandidateID != editorRef.CandidateID || candidate.CandidateID != approval.CandidateID {
		return fail("candidate_id", "generated_patch_candidate_mismatch", "candidate, selection, approval, and editor reference must match")
	}
	if editorRef.AssetRefID == "" || editorRef.AssetRefID != "generated_candidate_"+candidate.CandidateID+"_normalized" {
		return fail("asset_ref_id", "generated_patch_asset_ref_mismatch", "editor reference identity does not match the normalized candidate")
	}
	if editorRef.TargetPlanID != approval.TargetPlanID || editorRef.ExpectedPlanRevision != approval.ExpectedPlanRevision || editorRef.Placement != approval.Placement || editorRef.AnchorAfterStepID != approval.AnchorAfterStepID {
		return fail("target_plan_id", "generated_patch_plan_binding_mismatch", "editor reference and approval plan bindings must match")
	}
	durationMS := int(math.Round(candidate.NormalizedArtifact.Probe.DurationSec * 1000))
	if durationMS <= 0 {
		return fail("duration_ms", "generated_patch_duration_invalid", "candidate duration must produce a positive millisecond duration")
	}
	proposal := GeneratedShotEditPlanPatchProposal{
		SchemaVersion: GeneratedShotEditPlanPatchSchemaVersion,
		PatchID:       "generated_shot_patch_" + candidate.CandidateID + "_" + approval.ApprovalID,
		TargetPlanID:  approval.TargetPlanID, ExpectedPlanRevision: approval.ExpectedPlanRevision,
		IntentID: intent.IntentID, CandidateID: candidate.CandidateID, Provider: candidate.Provider,
		Status: GeneratedShotEditPlanPatchProposed, ApplicationMode: GeneratedShotEditPlanPatchApplicationMode,
		AssetRefID: editorRef.AssetRefID, Placement: editorRef.Placement, AnchorAfterStepID: editorRef.AnchorAfterStepID, Purpose: intent.Purpose,
		DurationMS: durationMS, RequiresExplicitOptIn: true, RequiresRendererReview: true,
		PresentationOnly: true, NonAuthoritativeOnly: true, MustNotBindSourceStep: true,
		AutoApply: false, ApprovedForDemo: false, IncludeInDemo: false, EditorApprovalID: approval.ApprovalID,
		Warnings: []string{"proposal only; no EditorSession or DemoEditPlan mutation has occurred", "source_step_id is intentionally absent"},
	}
	if err := ValidateGeneratedShotEditPlanPatchProposal(proposal); err != nil {
		return GeneratedShotEditPlanPatchProposal{}, err
	}
	return proposal, nil
}

func ValidateGeneratedShotEditPlanPatchProposal(proposal GeneratedShotEditPlanPatchProposal) error {
	fail := func(field string, code string, message string) error {
		return &GeneratedShotEditPlanPatchValidationError{Field: field, Code: code, Message: message}
	}
	if proposal.SchemaVersion != GeneratedShotEditPlanPatchSchemaVersion || strings.TrimSpace(proposal.PatchID) == "" {
		return fail("schema_version", "generated_patch_schema_unsupported", "patch proposal schema and patch_id are required")
	}
	if strings.TrimSpace(proposal.TargetPlanID) == "" || proposal.ExpectedPlanRevision < 1 || strings.TrimSpace(proposal.IntentID) == "" || strings.TrimSpace(proposal.CandidateID) == "" || strings.TrimSpace(proposal.Provider) == "" {
		return fail("target_plan_id", "generated_patch_identity_missing", "target plan, revision, intent, candidate, and provider are required")
	}
	if proposal.Status != GeneratedShotEditPlanPatchProposed || proposal.ApplicationMode != GeneratedShotEditPlanPatchApplicationMode {
		return fail("status", "generated_patch_status_invalid", "patch proposal must remain pending explicit application")
	}
	if strings.TrimSpace(proposal.AssetRefID) == "" || strings.TrimSpace(proposal.Placement) == "" || strings.TrimSpace(proposal.Purpose) == "" || proposal.DurationMS <= 0 {
		return fail("asset_ref_id", "generated_patch_payload_missing", "asset reference, placement, purpose, and positive duration are required")
	}
	if !generatedShotPlacementAnchorAllowed(proposal.Placement, proposal.AnchorAfterStepID) {
		return fail("anchor_after_step_id", "generated_patch_anchor_invalid", "patch placement anchor is invalid")
	}
	if !proposal.RequiresExplicitOptIn || !proposal.RequiresRendererReview || !proposal.PresentationOnly || !proposal.NonAuthoritativeOnly || !proposal.MustNotBindSourceStep {
		return fail("policy", "generated_patch_policy_unsafe", "patch proposal must require explicit opt-in and renderer validation and remain presentation-only")
	}
	if proposal.AutoApply || proposal.ApprovedForDemo || proposal.IncludeInDemo {
		return fail("auto_apply", "generated_patch_auto_apply_forbidden", "patch proposal cannot auto-apply, approve, or include a candidate")
	}
	if strings.TrimSpace(proposal.EditorApprovalID) == "" {
		return fail("editor_approval_id", "generated_patch_approval_missing", "editor approval identity is required")
	}
	if strings.Contains(strings.ToLower(proposal.AssetRefID), "http://") || strings.Contains(strings.ToLower(proposal.AssetRefID), "https://") {
		return fail("asset_ref_id", "generated_patch_provider_reference_forbidden", "patch proposal must reference a Server asset ref, not a provider URL")
	}
	return nil
}

func asGeneratedShotEditPlanPatchValidationError(err error) *GeneratedShotEditPlanPatchValidationError {
	var target *GeneratedShotEditPlanPatchValidationError
	if errors.As(err, &target) {
		return target
	}
	return nil
}
