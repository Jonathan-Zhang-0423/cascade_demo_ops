package media

import (
	"errors"
	"strings"
	"time"
)

const (
	GeneratedShotOrchestrationSchemaVersion = "demoops.generated_shot_orchestration.v1"

	GeneratedShotOrchestrationPreflightComplete          = "preflight_complete"
	GeneratedShotOrchestrationCandidatesReadyForReview   = "candidates_ready_for_review"
	GeneratedShotOrchestrationSelectedPendingEditor      = "selected_pending_editor_approval"
	GeneratedShotOrchestrationEditorApprovedPendingPatch = "editor_approved_pending_patch"
	GeneratedShotOrchestrationStoppedWithoutCandidate    = "stopped_without_generated_candidate"
)

type GeneratedShotOrchestrationRecord struct {
	SchemaVersion          string    `json:"schema_version"`
	OrchestrationID        string    `json:"orchestration_id"`
	IntentID               string    `json:"intent_id"`
	Mode                   string    `json:"mode"`
	State                  string    `json:"state"`
	Revision               int       `json:"revision"`
	UpdatedAt              time.Time `json:"updated_at"`
	PreflightSchemaVersion string    `json:"preflight_schema_version,omitempty"`
	CandidateSetID         string    `json:"candidate_set_id,omitempty"`
	SelectionID            string    `json:"selection_id,omitempty"`
	EditorApprovalID       string    `json:"editor_approval_id,omitempty"`
	FailurePolicy          string    `json:"failure_policy"`
	RuntimeRouteRegistered bool      `json:"runtime_route_registered"`
	ProviderCallsAllowed   bool      `json:"provider_calls_allowed"`
	EditorWritesAllowed    bool      `json:"editor_writes_allowed"`
	RendererAllowed        bool      `json:"renderer_allowed"`
	AutoApply              bool      `json:"auto_apply"`
	StoppedReason          string    `json:"stopped_reason,omitempty"`
}

type GeneratedShotOrchestrationValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *GeneratedShotOrchestrationValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Field + ": " + e.Message
}

func NewGeneratedShotOrchestration(orchestrationID string, intent GeneratedShotIntent, mode string, preflight GeneratedShotPreflightReport, now time.Time) (GeneratedShotOrchestrationRecord, error) {
	if strings.TrimSpace(orchestrationID) == "" {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("orchestration_id", "generated_orchestration_identity_missing", "orchestration_id is required")
	}
	if err := ValidateGeneratedShotIntent(intent); err != nil {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("intent", "generated_orchestration_intent_invalid", err.Error())
	}
	if mode != GeneratedShotCandidateSetModeNormal && mode != GeneratedShotCandidateSetModeComparison {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("mode", "generated_orchestration_mode_unsupported", "only normal and comparison dry-run orchestration records are supported")
	}
	if err := ValidateGeneratedShotPreflightReport(preflight); err != nil {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("preflight", "generated_orchestration_preflight_invalid", err.Error())
	}
	if preflight.IntentID != intent.IntentID {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("preflight", "generated_orchestration_preflight_invalid", "preflight must bind the intent and remain non-executable")
	}
	if mode == GeneratedShotCandidateSetModeComparison && (!generatedShotPreflightHasSeedance(preflight) || !generatedShotPreflightHasProviders(preflight, GeneratedShotProviderMiniMaxH3)) {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("preflight.providers", "generated_orchestration_comparison_preflight_incomplete", "comparison preflight must evaluate Seedance and MiniMax-H3 independently")
	}
	if now.IsZero() {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("updated_at", "generated_orchestration_time_missing", "updated_at is required")
	}
	record := GeneratedShotOrchestrationRecord{
		SchemaVersion:   GeneratedShotOrchestrationSchemaVersion,
		OrchestrationID: strings.TrimSpace(orchestrationID), IntentID: intent.IntentID, Mode: mode,
		State: GeneratedShotOrchestrationPreflightComplete, Revision: 1, UpdatedAt: now.UTC(),
		PreflightSchemaVersion: preflight.SchemaVersion, FailurePolicy: GeneratedShotFailureContinue,
	}
	if err := ValidateGeneratedShotOrchestration(record); err != nil {
		return GeneratedShotOrchestrationRecord{}, err
	}
	return record, nil
}

func AdvanceGeneratedShotOrchestrationToCandidates(record GeneratedShotOrchestrationRecord, set GeneratedShotCandidateSet, now time.Time) (GeneratedShotOrchestrationRecord, error) {
	if err := ValidateGeneratedShotOrchestration(record); err != nil {
		return GeneratedShotOrchestrationRecord{}, err
	}
	if record.State != GeneratedShotOrchestrationPreflightComplete {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("state", "generated_orchestration_transition_invalid", "candidate set requires preflight_complete state")
	}
	if err := ValidateGeneratedShotCandidateSet(set); err != nil {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("candidate_set", "generated_orchestration_candidate_set_invalid", err.Error())
	}
	if set.IntentID != record.IntentID || set.Mode != record.Mode || !now.After(record.UpdatedAt) {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("candidate_set", "generated_orchestration_binding_mismatch", "candidate set, mode, and timestamp must match the orchestration")
	}
	record.State = GeneratedShotOrchestrationCandidatesReadyForReview
	record.CandidateSetID = set.SetID
	record.Revision++
	record.UpdatedAt = now.UTC()
	return record, ValidateGeneratedShotOrchestration(record)
}

func AdvanceGeneratedShotOrchestrationToSelection(record GeneratedShotOrchestrationRecord, set GeneratedShotCandidateSet, selection GeneratedShotSelection, now time.Time) (GeneratedShotOrchestrationRecord, error) {
	if err := ValidateGeneratedShotOrchestration(record); err != nil {
		return GeneratedShotOrchestrationRecord{}, err
	}
	if record.State != GeneratedShotOrchestrationCandidatesReadyForReview || record.CandidateSetID != set.SetID {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("state", "generated_orchestration_transition_invalid", "selection requires the bound candidates_ready_for_review state")
	}
	if err := ValidateGeneratedShotSelection(set, selection); err != nil {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("selection", "generated_orchestration_selection_invalid", err.Error())
	}
	if selection.IntentID != record.IntentID || selection.Mode != record.Mode || !now.After(record.UpdatedAt) {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("selection", "generated_orchestration_binding_mismatch", "selection, mode, and timestamp must match the orchestration")
	}
	record.State = GeneratedShotOrchestrationSelectedPendingEditor
	record.SelectionID = selection.SelectionID
	record.Revision++
	record.UpdatedAt = now.UTC()
	return record, ValidateGeneratedShotOrchestration(record)
}

func AdvanceGeneratedShotOrchestrationToEditorApproval(record GeneratedShotOrchestrationRecord, set GeneratedShotCandidateSet, selection GeneratedShotSelection, approval GeneratedShotEditorApproval, now time.Time) (GeneratedShotOrchestrationRecord, error) {
	if err := ValidateGeneratedShotOrchestration(record); err != nil {
		return GeneratedShotOrchestrationRecord{}, err
	}
	if record.State != GeneratedShotOrchestrationSelectedPendingEditor || record.CandidateSetID != set.SetID || record.SelectionID != selection.SelectionID {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("state", "generated_orchestration_transition_invalid", "editor approval requires the bound selected_pending_editor_approval state")
	}
	if err := ValidateGeneratedShotEditorApproval(set, selection, approval); err != nil {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("editor_approval", "generated_orchestration_editor_approval_invalid", err.Error())
	}
	if approval.IntentID != record.IntentID || !now.After(record.UpdatedAt) {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("editor_approval", "generated_orchestration_binding_mismatch", "approval and timestamp must match the orchestration")
	}
	record.State = GeneratedShotOrchestrationEditorApprovedPendingPatch
	record.EditorApprovalID = approval.ApprovalID
	record.Revision++
	record.UpdatedAt = now.UTC()
	return record, ValidateGeneratedShotOrchestration(record)
}

func StopGeneratedShotOrchestration(record GeneratedShotOrchestrationRecord, reason string, now time.Time) (GeneratedShotOrchestrationRecord, error) {
	if err := ValidateGeneratedShotOrchestration(record); err != nil {
		return GeneratedShotOrchestrationRecord{}, err
	}
	if record.State == GeneratedShotOrchestrationEditorApprovedPendingPatch || record.State == GeneratedShotOrchestrationStoppedWithoutCandidate {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("state", "generated_orchestration_transition_invalid", "terminal orchestration state cannot be stopped again")
	}
	if strings.TrimSpace(reason) == "" || !now.After(record.UpdatedAt) {
		return GeneratedShotOrchestrationRecord{}, generatedShotOrchestrationError("stopped_reason", "generated_orchestration_stop_reason_missing", "stop reason and timestamp are required")
	}
	record.State = GeneratedShotOrchestrationStoppedWithoutCandidate
	record.StoppedReason = strings.TrimSpace(reason)
	record.Revision++
	record.UpdatedAt = now.UTC()
	return record, ValidateGeneratedShotOrchestration(record)
}

func ValidateGeneratedShotOrchestration(record GeneratedShotOrchestrationRecord) error {
	if record.SchemaVersion != GeneratedShotOrchestrationSchemaVersion || strings.TrimSpace(record.OrchestrationID) == "" || strings.TrimSpace(record.IntentID) == "" {
		return generatedShotOrchestrationError("schema_version", "generated_orchestration_identity_invalid", "orchestration schema and identities are required")
	}
	if record.Mode != GeneratedShotCandidateSetModeNormal && record.Mode != GeneratedShotCandidateSetModeComparison {
		return generatedShotOrchestrationError("mode", "generated_orchestration_mode_unsupported", "orchestration mode is unsupported")
	}
	if record.Revision < 1 || record.UpdatedAt.IsZero() || record.FailurePolicy != GeneratedShotFailureContinue {
		return generatedShotOrchestrationError("revision", "generated_orchestration_metadata_invalid", "revision, timestamp, and continue failure policy are required")
	}
	if record.RuntimeRouteRegistered || record.ProviderCallsAllowed || record.EditorWritesAllowed || record.RendererAllowed || record.AutoApply {
		return generatedShotOrchestrationError("runtime_route_registered", "generated_orchestration_runtime_authority_forbidden", "current orchestration record cannot register routes, call providers, write editor state, render, or auto-apply")
	}
	switch record.State {
	case GeneratedShotOrchestrationPreflightComplete:
		if record.PreflightSchemaVersion != GeneratedShotPreflightSchemaVersion || record.CandidateSetID != "" || record.SelectionID != "" || record.EditorApprovalID != "" || record.StoppedReason != "" {
			return generatedShotOrchestrationError("state", "generated_orchestration_state_payload_invalid", "preflight state contains invalid downstream identities")
		}
	case GeneratedShotOrchestrationCandidatesReadyForReview:
		if record.CandidateSetID == "" || record.SelectionID != "" || record.EditorApprovalID != "" || record.StoppedReason != "" {
			return generatedShotOrchestrationError("state", "generated_orchestration_state_payload_invalid", "candidate state identities are invalid")
		}
	case GeneratedShotOrchestrationSelectedPendingEditor:
		if record.CandidateSetID == "" || record.SelectionID == "" || record.EditorApprovalID != "" || record.StoppedReason != "" {
			return generatedShotOrchestrationError("state", "generated_orchestration_state_payload_invalid", "selection state identities are invalid")
		}
	case GeneratedShotOrchestrationEditorApprovedPendingPatch:
		if record.CandidateSetID == "" || record.SelectionID == "" || record.EditorApprovalID == "" || record.StoppedReason != "" {
			return generatedShotOrchestrationError("state", "generated_orchestration_state_payload_invalid", "editor approval state identities are invalid")
		}
	case GeneratedShotOrchestrationStoppedWithoutCandidate:
		if record.StoppedReason == "" {
			return generatedShotOrchestrationError("stopped_reason", "generated_orchestration_stop_reason_missing", "stopped orchestration requires a reason")
		}
	default:
		return generatedShotOrchestrationError("state", "generated_orchestration_state_unsupported", "orchestration state is unsupported")
	}
	return nil
}

func generatedShotOrchestrationError(field string, code string, message string) error {
	return &GeneratedShotOrchestrationValidationError{Field: field, Code: code, Message: message}
}

func generatedShotPreflightHasProviders(report GeneratedShotPreflightReport, providers ...string) bool {
	found := map[string]struct{}{}
	for _, provider := range report.Providers {
		found[provider.Provider] = struct{}{}
	}
	for _, provider := range providers {
		if _, ok := found[provider]; !ok {
			return false
		}
	}
	return true
}

func generatedShotPreflightHasSeedance(report GeneratedShotPreflightReport) bool {
	for _, provider := range report.Providers {
		if isGeneratedShotSeedanceProvider(provider.Provider) {
			return true
		}
	}
	return false
}

func asGeneratedShotOrchestrationValidationError(err error) *GeneratedShotOrchestrationValidationError {
	var target *GeneratedShotOrchestrationValidationError
	if errors.As(err, &target) {
		return target
	}
	return nil
}
