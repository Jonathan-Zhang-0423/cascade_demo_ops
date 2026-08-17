package media

import (
	"errors"
	"strings"
	"time"
)

const (
	GeneratedShotEditorApprovalSchemaVersion = "demoops.generated_shot_editor_approval.v1"
	GeneratedShotEditorApprovedPendingPatch  = "editor_approved_pending_patch"
)

type GeneratedShotEditorApprovalDecision struct {
	ApprovalID           string    `json:"approval_id"`
	SelectionID          string    `json:"selection_id"`
	CandidateID          string    `json:"candidate_id"`
	ApproverID           string    `json:"approver_id"`
	ApproverKind         string    `json:"approver_kind"`
	ApprovedAt           time.Time `json:"approved_at"`
	TargetPlanID         string    `json:"target_plan_id"`
	ExpectedPlanRevision int       `json:"expected_plan_revision"`
	Placement            string    `json:"placement"`
	AnchorAfterStepID    string    `json:"anchor_after_step_id,omitempty"`
	EvidenceRefs         []string  `json:"evidence_refs,omitempty"`
	Reason               string    `json:"reason"`
}

// GeneratedShotEditorApproval authorizes only future patch construction. It
// does not construct, apply, or render a patch and has no source-step field.
type GeneratedShotEditorApproval struct {
	SchemaVersion           string    `json:"schema_version"`
	ApprovalID              string    `json:"approval_id"`
	SelectionID             string    `json:"selection_id"`
	SetID                   string    `json:"set_id"`
	IntentID                string    `json:"intent_id"`
	CandidateID             string    `json:"candidate_id"`
	Provider                string    `json:"provider"`
	NormalizedSHA256        string    `json:"normalized_sha256"`
	ApproverID              string    `json:"approver_id"`
	ApproverKind            string    `json:"approver_kind"`
	ApprovedAt              time.Time `json:"approved_at"`
	TargetPlanID            string    `json:"target_plan_id"`
	ExpectedPlanRevision    int       `json:"expected_plan_revision"`
	Placement               string    `json:"placement"`
	AnchorAfterStepID       string    `json:"anchor_after_step_id,omitempty"`
	EvidenceRefs            []string  `json:"evidence_refs,omitempty"`
	Reason                  string    `json:"reason"`
	Status                  string    `json:"status"`
	PatchCreationAuthorized bool      `json:"patch_creation_authorized"`
	PatchApplyAuthorized    bool      `json:"patch_apply_authorized"`
	RendererAuthorized      bool      `json:"renderer_authorized"`
	MustNotBindSourceStep   bool      `json:"must_not_bind_source_step"`
	ApprovedForDemo         bool      `json:"approved_for_demo"`
	IncludeInDemo           bool      `json:"include_in_demo"`
	AutoApply               bool      `json:"auto_apply"`
}

type GeneratedShotEditorApprovalValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *GeneratedShotEditorApprovalValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Field + ": " + e.Message
}

func RecordGeneratedShotEditorApproval(intent GeneratedShotIntent, candidate GeneratedShotCandidate, set GeneratedShotCandidateSet, selection GeneratedShotSelection, decision GeneratedShotEditorApprovalDecision) (GeneratedShotEditorApproval, error) {
	fail := func(field string, code string, message string) (GeneratedShotEditorApproval, error) {
		return GeneratedShotEditorApproval{}, &GeneratedShotEditorApprovalValidationError{Field: field, Code: code, Message: message}
	}
	if err := ValidateGeneratedShotIntent(intent); err != nil {
		return fail("intent", "generated_editor_approval_intent_invalid", err.Error())
	}
	if err := ValidateGeneratedShotCandidate(candidate); err != nil {
		return fail("candidate", "generated_editor_approval_candidate_invalid", err.Error())
	}
	if err := ValidateGeneratedShotSelection(set, selection); err != nil {
		return fail("selection", "generated_editor_approval_selection_invalid", err.Error())
	}
	if intent.IntentID != set.IntentID || candidate.IntentID != intent.IntentID || selection.SelectedCandidateID != candidate.CandidateID || selection.SelectedProvider != candidate.Provider {
		return fail("candidate_id", "generated_editor_approval_binding_mismatch", "intent, set, selection, candidate, and provider must match")
	}
	var selected *GeneratedShotCandidateSetEntry
	for index := range set.Candidates {
		if set.Candidates[index].CandidateID == candidate.CandidateID {
			selected = &set.Candidates[index]
			break
		}
	}
	if selected == nil || selected.NormalizedSHA256 != strings.ToLower(candidate.NormalizedArtifact.SHA256) || selected.ProviderTaskID != candidate.ProviderTaskID {
		return fail("candidate", "generated_editor_approval_candidate_summary_mismatch", "selected candidate digest and provider task must match the reviewed set")
	}
	if strings.TrimSpace(decision.ApprovalID) == "" || decision.SelectionID != selection.SelectionID || decision.CandidateID != candidate.CandidateID {
		return fail("approval_id", "generated_editor_approval_identity_invalid", "approval identity must bind the selection and candidate")
	}
	if strings.TrimSpace(decision.ApproverID) == "" || decision.ApproverKind != "human" || decision.ApprovedAt.IsZero() {
		return fail("approver_kind", "generated_editor_approval_human_required", "current profile requires a human editor approver and timestamp")
	}
	if strings.TrimSpace(decision.TargetPlanID) == "" || decision.ExpectedPlanRevision < 1 {
		return fail("target_plan_id", "generated_editor_approval_plan_binding_missing", "target plan and positive expected revision are required")
	}
	if !generatedShotPlacementAllowed(intent.Purpose, decision.Placement) {
		return fail("placement", "generated_editor_approval_placement_invalid", "placement is not allowed for the reviewed intent purpose")
	}
	if !generatedShotPlacementAnchorAllowed(decision.Placement, decision.AnchorAfterStepID) {
		return fail("anchor_after_step_id", "generated_editor_approval_anchor_invalid", "section and presentation-gap placements require an explicit step anchor; intro/outro placements forbid one")
	}
	if strings.TrimSpace(decision.Reason) == "" || len(nonEmptyUniqueStrings(decision.EvidenceRefs)) == 0 {
		return fail("evidence_refs", "generated_editor_approval_evidence_missing", "editor approval requires a reason and evidence")
	}
	approval := GeneratedShotEditorApproval{
		SchemaVersion: GeneratedShotEditorApprovalSchemaVersion,
		ApprovalID:    strings.TrimSpace(decision.ApprovalID), SelectionID: selection.SelectionID, SetID: set.SetID,
		IntentID: intent.IntentID, CandidateID: candidate.CandidateID, Provider: candidate.Provider,
		NormalizedSHA256: strings.ToLower(candidate.NormalizedArtifact.SHA256),
		ApproverID:       strings.TrimSpace(decision.ApproverID), ApproverKind: decision.ApproverKind, ApprovedAt: decision.ApprovedAt.UTC(),
		TargetPlanID: strings.TrimSpace(decision.TargetPlanID), ExpectedPlanRevision: decision.ExpectedPlanRevision,
		Placement: decision.Placement, AnchorAfterStepID: strings.TrimSpace(decision.AnchorAfterStepID), EvidenceRefs: nonEmptyUniqueStrings(decision.EvidenceRefs), Reason: strings.TrimSpace(decision.Reason),
		Status: GeneratedShotEditorApprovedPendingPatch, PatchCreationAuthorized: true,
		PatchApplyAuthorized: false, RendererAuthorized: false, MustNotBindSourceStep: true,
		ApprovedForDemo: true, IncludeInDemo: false, AutoApply: false,
	}
	if err := ValidateGeneratedShotEditorApproval(set, selection, approval); err != nil {
		return GeneratedShotEditorApproval{}, err
	}
	return approval, nil
}

func ValidateGeneratedShotEditorApproval(set GeneratedShotCandidateSet, selection GeneratedShotSelection, approval GeneratedShotEditorApproval) error {
	fail := func(field string, code string, message string) error {
		return &GeneratedShotEditorApprovalValidationError{Field: field, Code: code, Message: message}
	}
	if err := ValidateGeneratedShotSelection(set, selection); err != nil {
		return fail("selection", "generated_editor_approval_selection_invalid", err.Error())
	}
	if approval.SchemaVersion != GeneratedShotEditorApprovalSchemaVersion || strings.TrimSpace(approval.ApprovalID) == "" {
		return fail("schema_version", "generated_editor_approval_schema_unsupported", "approval schema and identity are required")
	}
	if approval.SelectionID != selection.SelectionID || approval.SetID != set.SetID || approval.IntentID != set.IntentID || approval.CandidateID != selection.SelectedCandidateID || approval.Provider != selection.SelectedProvider {
		return fail("selection_id", "generated_editor_approval_binding_mismatch", "approval must bind the selection, set, intent, candidate, and provider")
	}
	if !generatedShotSHA256Pattern.MatchString(approval.NormalizedSHA256) {
		return fail("normalized_sha256", "generated_editor_approval_digest_invalid", "approval must carry the selected normalized SHA-256")
	}
	if strings.TrimSpace(approval.ApproverID) == "" || approval.ApproverKind != "human" || approval.ApprovedAt.IsZero() {
		return fail("approver_kind", "generated_editor_approval_human_required", "approval requires a human identity and timestamp")
	}
	if strings.TrimSpace(approval.TargetPlanID) == "" || approval.ExpectedPlanRevision < 1 || strings.TrimSpace(approval.Placement) == "" {
		return fail("target_plan_id", "generated_editor_approval_plan_binding_missing", "plan binding, revision, and placement are required")
	}
	if !generatedShotPlacementAnchorAllowed(approval.Placement, approval.AnchorAfterStepID) {
		return fail("anchor_after_step_id", "generated_editor_approval_anchor_invalid", "serialized approval placement anchor is invalid")
	}
	if strings.TrimSpace(approval.Reason) == "" || len(nonEmptyUniqueStrings(approval.EvidenceRefs)) == 0 {
		return fail("evidence_refs", "generated_editor_approval_evidence_missing", "approval requires a reason and evidence")
	}
	if approval.Status != GeneratedShotEditorApprovedPendingPatch || !approval.PatchCreationAuthorized || approval.PatchApplyAuthorized || approval.RendererAuthorized || !approval.MustNotBindSourceStep || !approval.ApprovedForDemo || approval.IncludeInDemo || approval.AutoApply {
		return fail("status", "generated_editor_approval_safety_envelope_invalid", "approval may authorize patch creation only and must not authorize apply, render, source-step binding, inclusion, or auto-apply")
	}
	return nil
}

func generatedShotPlacementAllowed(purpose string, placement string) bool {
	switch purpose {
	case GeneratedShotPurposeIntro:
		return placement == "before_first_required_step"
	case GeneratedShotPurposeOutro:
		return placement == "after_last_required_step"
	case GeneratedShotPurposeSectionDivider, GeneratedShotPurposeTransition:
		return placement == "between_sections"
	case GeneratedShotPurposeAbstractBRoll, GeneratedShotPurposeBrandAtmosphere:
		return placement == "presentation_gap"
	default:
		return false
	}
}

func generatedShotPlacementAnchorAllowed(placement, anchor string) bool {
	hasAnchor := strings.TrimSpace(anchor) != ""
	switch placement {
	case "before_first_required_step", "after_last_required_step":
		return !hasAnchor
	case "between_sections", "presentation_gap":
		return hasAnchor
	default:
		return false
	}
}

func asGeneratedShotEditorApprovalValidationError(err error) *GeneratedShotEditorApprovalValidationError {
	var target *GeneratedShotEditorApprovalValidationError
	if errors.As(err, &target) {
		return target
	}
	return nil
}
