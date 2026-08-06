package media

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	GeneratedShotCandidateSetSchemaVersion = "demoops.generated_shot_candidate_set.v1"
	GeneratedShotSelectionSchemaVersion    = "demoops.generated_shot_selection.v1"

	GeneratedShotCandidateSetModeNormal     = "normal"
	GeneratedShotCandidateSetModeComparison = "comparison"

	GeneratedShotCandidateSetReadyForSelection = "ready_for_explicit_selection"
	GeneratedShotSelectedPendingEditorApproval = "selected_pending_editor_approval"
)

type GeneratedShotReviewedCandidate struct {
	Candidate     GeneratedShotCandidate     `json:"candidate"`
	ContentReview GeneratedShotContentReview `json:"content_review"`
}

type GeneratedShotCandidateSetEntry struct {
	CandidateID      string `json:"candidate_id"`
	Provider         string `json:"provider"`
	ProviderTaskID   string `json:"provider_task_id"`
	ContentReviewID  string `json:"content_review_id"`
	NormalizedSHA256 string `json:"normalized_sha256"`
}

// GeneratedShotCandidateSet records comparable, content-approved candidates.
// It carries no provider request and cannot invoke generation or editing.
type GeneratedShotCandidateSet struct {
	SchemaVersion     string                           `json:"schema_version"`
	SetID             string                           `json:"set_id"`
	IntentID          string                           `json:"intent_id"`
	Mode              string                           `json:"mode"`
	Status            string                           `json:"status"`
	Candidates        []GeneratedShotCandidateSetEntry `json:"candidates"`
	SelectionRequired bool                             `json:"selection_required"`
	Executable        bool                             `json:"executable"`
	ApprovedForDemo   bool                             `json:"approved_for_demo"`
	IncludeInDemo     bool                             `json:"include_in_demo"`
	AutoApply         bool                             `json:"auto_apply"`
}

type GeneratedShotSelectionDecision struct {
	SelectionID  string    `json:"selection_id"`
	SetID        string    `json:"set_id"`
	CandidateID  string    `json:"candidate_id"`
	SelectorID   string    `json:"selector_id"`
	SelectorKind string    `json:"selector_kind"`
	SelectedAt   time.Time `json:"selected_at"`
	EvidenceRefs []string  `json:"evidence_refs,omitempty"`
	Reason       string    `json:"reason"`
}

// GeneratedShotSelection is an audited preference, not editor approval.
type GeneratedShotSelection struct {
	SchemaVersion          string    `json:"schema_version"`
	SelectionID            string    `json:"selection_id"`
	SetID                  string    `json:"set_id"`
	IntentID               string    `json:"intent_id"`
	Mode                   string    `json:"mode"`
	SelectedCandidateID    string    `json:"selected_candidate_id"`
	SelectedProvider       string    `json:"selected_provider"`
	SelectorID             string    `json:"selector_id"`
	SelectorKind           string    `json:"selector_kind"`
	SelectedAt             time.Time `json:"selected_at"`
	EvidenceRefs           []string  `json:"evidence_refs,omitempty"`
	Reason                 string    `json:"reason"`
	Status                 string    `json:"status"`
	EditorApprovalRequired bool      `json:"editor_approval_required"`
	ApprovedForDemo        bool      `json:"approved_for_demo"`
	IncludeInDemo          bool      `json:"include_in_demo"`
	AutoApply              bool      `json:"auto_apply"`
}

type GeneratedShotSelectionValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *GeneratedShotSelectionValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Field + ": " + e.Message
}

// NewGeneratedShotCandidateSet creates a non-executable selection set. Normal
// requires one candidate; comparison requires exactly one Seedance 2.0 and one
// H3 candidate for the same intent. Fallback execution is intentionally absent.
func NewGeneratedShotCandidateSet(setID string, intent GeneratedShotIntent, mode string, reviewed []GeneratedShotReviewedCandidate) (GeneratedShotCandidateSet, error) {
	fail := func(field string, code string, message string) (GeneratedShotCandidateSet, error) {
		return GeneratedShotCandidateSet{}, &GeneratedShotSelectionValidationError{Field: field, Code: code, Message: message}
	}
	if strings.TrimSpace(setID) == "" {
		return fail("set_id", "generated_candidate_set_identity_missing", "set_id is required")
	}
	if err := ValidateGeneratedShotIntent(intent); err != nil {
		return fail("intent", "generated_candidate_set_intent_invalid", err.Error())
	}
	switch mode {
	case GeneratedShotCandidateSetModeNormal:
		if len(reviewed) != 1 {
			return fail("candidates", "generated_candidate_set_normal_count_invalid", "normal mode requires exactly one candidate")
		}
	case GeneratedShotCandidateSetModeComparison:
		if len(reviewed) != 2 {
			return fail("candidates", "generated_candidate_set_comparison_count_invalid", "comparison mode requires exactly two candidates")
		}
	default:
		return fail("mode", "generated_candidate_set_mode_unsupported", "only normal and comparison selection sets are supported; fallback execution is not enabled")
	}

	set := GeneratedShotCandidateSet{
		SchemaVersion: GeneratedShotCandidateSetSchemaVersion, SetID: strings.TrimSpace(setID),
		IntentID: intent.IntentID, Mode: mode, Status: GeneratedShotCandidateSetReadyForSelection,
		SelectionRequired: true, Executable: false, ApprovedForDemo: false, IncludeInDemo: false, AutoApply: false,
	}
	candidateIDs := map[string]struct{}{}
	providerTaskIDs := map[string]struct{}{}
	normalizedDigests := map[string]struct{}{}
	providers := map[string]struct{}{}
	for index, item := range reviewed {
		field := fmt.Sprintf("candidates[%d]", index)
		if err := ValidateGeneratedShotCandidate(item.Candidate); err != nil {
			return fail(field+".candidate", "generated_candidate_set_candidate_invalid", err.Error())
		}
		if item.Candidate.IntentID != intent.IntentID {
			return fail(field+".candidate.intent_id", "generated_candidate_set_intent_mismatch", "every candidate must belong to the same reviewed intent")
		}
		if err := validateGeneratedShotContentReviewForSelection(item.Candidate, item.ContentReview); err != nil {
			return fail(field+".content_review", "generated_candidate_set_review_invalid", err.Error())
		}
		if _, exists := candidateIDs[item.Candidate.CandidateID]; exists {
			return fail(field+".candidate.candidate_id", "generated_candidate_set_duplicate_candidate", "candidate IDs must be unique")
		}
		candidateIDs[item.Candidate.CandidateID] = struct{}{}
		if _, exists := providerTaskIDs[item.Candidate.Provider+"\x00"+item.Candidate.ProviderTaskID]; exists {
			return fail(field+".candidate.provider_task_id", "generated_candidate_set_duplicate_task", "provider task identities must be unique")
		}
		providerTaskIDs[item.Candidate.Provider+"\x00"+item.Candidate.ProviderTaskID] = struct{}{}
		digest := strings.ToLower(item.Candidate.NormalizedArtifact.SHA256)
		if _, exists := normalizedDigests[digest]; exists {
			return fail(field+".candidate.normalized_artifact.sha256", "generated_candidate_set_duplicate_output", "A/B candidates must not reference the same normalized output")
		}
		normalizedDigests[digest] = struct{}{}
		if _, exists := providers[item.Candidate.Provider]; exists && mode == GeneratedShotCandidateSetModeComparison {
			return fail(field+".candidate.provider", "generated_candidate_set_duplicate_provider", "comparison candidates must come from different providers")
		}
		providers[item.Candidate.Provider] = struct{}{}
		set.Candidates = append(set.Candidates, GeneratedShotCandidateSetEntry{
			CandidateID: item.Candidate.CandidateID, Provider: item.Candidate.Provider,
			ProviderTaskID: item.Candidate.ProviderTaskID, ContentReviewID: item.ContentReview.ReviewID,
			NormalizedSHA256: digest,
		})
	}
	if mode == GeneratedShotCandidateSetModeComparison {
		if _, ok := providers[GeneratedShotProviderSeedance20]; !ok {
			return fail("candidates", "generated_candidate_set_provider_pair_invalid", "comparison requires one Seedance 2.0 candidate")
		}
		if _, ok := providers[GeneratedShotProviderMiniMaxH3]; !ok {
			return fail("candidates", "generated_candidate_set_provider_pair_invalid", "comparison requires one MiniMax-H3 candidate")
		}
	}
	return set, nil
}

func validateGeneratedShotContentReviewForSelection(candidate GeneratedShotCandidate, review GeneratedShotContentReview) error {
	if review.SchemaVersion != GeneratedShotContentReviewSchemaVersion || review.Status != GeneratedShotContentApprovedPendingSelection || review.Decision != GeneratedShotContentDecisionApprove || !review.ContentApproved || !review.SelectionRequired {
		return errors.New("content review must be approved and pending selection")
	}
	if strings.TrimSpace(review.ReviewID) == "" || strings.TrimSpace(review.StructuralReviewID) == "" {
		return errors.New("content and structural review identities are required")
	}
	if review.CandidateID != candidate.CandidateID || review.IntentID != candidate.IntentID {
		return errors.New("content review must bind the candidate and intent")
	}
	if review.ReviewerKind != "human" || strings.TrimSpace(review.ReviewerID) == "" || review.ReviewedAt.IsZero() {
		return errors.New("content review must contain a human reviewer identity and timestamp")
	}
	if !generatedShotContentAssertionsSafe(review.Assertions) || len(nonEmptyUniqueStrings(review.EvidenceRefs)) == 0 {
		return errors.New("content review safety assertions and evidence are incomplete")
	}
	if review.ApprovedForDemo || review.IncludeInDemo {
		return errors.New("content review must not pre-approve or include the candidate")
	}
	return nil
}

// ValidateGeneratedShotCandidateSet revalidates serialized or replayed sets
// before a selection is recorded. It does not prove content quality; it
// protects the immutable selection envelope from structural tampering.
func ValidateGeneratedShotCandidateSet(set GeneratedShotCandidateSet) error {
	fail := func(field string, code string, message string) error {
		return &GeneratedShotSelectionValidationError{Field: field, Code: code, Message: message}
	}
	if set.SchemaVersion != GeneratedShotCandidateSetSchemaVersion {
		return fail("schema_version", "generated_candidate_set_schema_unsupported", "candidate set schema version is unsupported")
	}
	if strings.TrimSpace(set.SetID) == "" || strings.TrimSpace(set.IntentID) == "" {
		return fail("set_id", "generated_candidate_set_identity_missing", "set_id and intent_id are required")
	}
	if set.Status != GeneratedShotCandidateSetReadyForSelection || !set.SelectionRequired || set.Executable || set.ApprovedForDemo || set.IncludeInDemo || set.AutoApply {
		return fail("status", "generated_candidate_set_safety_envelope_invalid", "candidate set must be ready for selection and remain non-executable, unapproved, excluded, and non-auto-apply")
	}
	switch set.Mode {
	case GeneratedShotCandidateSetModeNormal:
		if len(set.Candidates) != 1 {
			return fail("candidates", "generated_candidate_set_normal_count_invalid", "normal mode requires exactly one candidate")
		}
	case GeneratedShotCandidateSetModeComparison:
		if len(set.Candidates) != 2 {
			return fail("candidates", "generated_candidate_set_comparison_count_invalid", "comparison mode requires exactly two candidates")
		}
	default:
		return fail("mode", "generated_candidate_set_mode_unsupported", "candidate set mode is unsupported")
	}
	candidateIDs := map[string]struct{}{}
	taskIDs := map[string]struct{}{}
	reviewIDs := map[string]struct{}{}
	digests := map[string]struct{}{}
	providers := map[string]struct{}{}
	for index, entry := range set.Candidates {
		field := fmt.Sprintf("candidates[%d]", index)
		if strings.TrimSpace(entry.CandidateID) == "" || strings.TrimSpace(entry.ProviderTaskID) == "" || strings.TrimSpace(entry.ContentReviewID) == "" {
			return fail(field, "generated_candidate_set_entry_identity_missing", "candidate, provider task, and content review identities are required")
		}
		if entry.Provider != GeneratedShotProviderSeedance20 && entry.Provider != GeneratedShotProviderMiniMaxH3 {
			return fail(field+".provider", "generated_candidate_set_provider_unsupported", "candidate provider is unsupported")
		}
		digest := strings.ToLower(strings.TrimSpace(entry.NormalizedSHA256))
		if !generatedShotSHA256Pattern.MatchString(digest) {
			return fail(field+".normalized_sha256", "generated_candidate_set_digest_invalid", "normalized candidate digest must be a SHA-256")
		}
		if _, exists := candidateIDs[entry.CandidateID]; exists {
			return fail(field+".candidate_id", "generated_candidate_set_duplicate_candidate", "candidate IDs must be unique")
		}
		candidateIDs[entry.CandidateID] = struct{}{}
		taskKey := entry.Provider + "\x00" + entry.ProviderTaskID
		if _, exists := taskIDs[taskKey]; exists {
			return fail(field+".provider_task_id", "generated_candidate_set_duplicate_task", "provider task identities must be unique")
		}
		taskIDs[taskKey] = struct{}{}
		if _, exists := reviewIDs[entry.ContentReviewID]; exists {
			return fail(field+".content_review_id", "generated_candidate_set_duplicate_review", "content review identities must be unique")
		}
		reviewIDs[entry.ContentReviewID] = struct{}{}
		if _, exists := digests[digest]; exists {
			return fail(field+".normalized_sha256", "generated_candidate_set_duplicate_output", "normalized candidate digests must be unique")
		}
		digests[digest] = struct{}{}
		providers[entry.Provider] = struct{}{}
	}
	if set.Mode == GeneratedShotCandidateSetModeComparison {
		if len(providers) != 2 {
			return fail("candidates", "generated_candidate_set_provider_pair_invalid", "comparison requires different providers")
		}
		if _, ok := providers[GeneratedShotProviderSeedance20]; !ok {
			return fail("candidates", "generated_candidate_set_provider_pair_invalid", "comparison requires Seedance 2.0")
		}
		if _, ok := providers[GeneratedShotProviderMiniMaxH3]; !ok {
			return fail("candidates", "generated_candidate_set_provider_pair_invalid", "comparison requires MiniMax-H3")
		}
	}
	return nil
}

// RecordGeneratedShotSelection records a human selection. It deliberately
// remains pending a separate editor approval and cannot auto-apply a patch.
func RecordGeneratedShotSelection(set GeneratedShotCandidateSet, decision GeneratedShotSelectionDecision) (GeneratedShotSelection, error) {
	fail := func(field string, code string, message string) (GeneratedShotSelection, error) {
		return GeneratedShotSelection{}, &GeneratedShotSelectionValidationError{Field: field, Code: code, Message: message}
	}
	if err := ValidateGeneratedShotCandidateSet(set); err != nil {
		return fail("candidate_set", "generated_selection_set_ineligible", err.Error())
	}
	if strings.TrimSpace(decision.SelectionID) == "" || decision.SetID != set.SetID {
		return fail("selection_id", "generated_selection_identity_invalid", "selection_id is required and set_id must match")
	}
	if strings.TrimSpace(decision.SelectorID) == "" || decision.SelectorKind != "human" {
		return fail("selector_kind", "generated_selection_human_required", "the current project profile requires a human selector")
	}
	if decision.SelectedAt.IsZero() {
		return fail("selected_at", "generated_selection_time_missing", "selected_at is required")
	}
	if strings.TrimSpace(decision.Reason) == "" || len(nonEmptyUniqueStrings(decision.EvidenceRefs)) == 0 {
		return fail("reason", "generated_selection_evidence_missing", "selection requires a reason and at least one evidence reference")
	}
	var selected *GeneratedShotCandidateSetEntry
	for index := range set.Candidates {
		if set.Candidates[index].CandidateID == decision.CandidateID {
			selected = &set.Candidates[index]
			break
		}
	}
	if selected == nil {
		return fail("candidate_id", "generated_selection_candidate_not_in_set", "selected candidate is not in the reviewed candidate set")
	}
	selection := GeneratedShotSelection{
		SchemaVersion: GeneratedShotSelectionSchemaVersion,
		SelectionID:   strings.TrimSpace(decision.SelectionID), SetID: set.SetID, IntentID: set.IntentID, Mode: set.Mode,
		SelectedCandidateID: selected.CandidateID, SelectedProvider: selected.Provider,
		SelectorID: strings.TrimSpace(decision.SelectorID), SelectorKind: decision.SelectorKind,
		SelectedAt: decision.SelectedAt.UTC(), EvidenceRefs: nonEmptyUniqueStrings(decision.EvidenceRefs), Reason: strings.TrimSpace(decision.Reason),
		Status: GeneratedShotSelectedPendingEditorApproval, EditorApprovalRequired: true,
		ApprovedForDemo: false, IncludeInDemo: false, AutoApply: false,
	}
	if err := ValidateGeneratedShotSelection(set, selection); err != nil {
		return GeneratedShotSelection{}, err
	}
	return selection, nil
}

// ValidateGeneratedShotSelection revalidates a serialized selection against
// its immutable candidate-set summary.
func ValidateGeneratedShotSelection(set GeneratedShotCandidateSet, selection GeneratedShotSelection) error {
	fail := func(field string, code string, message string) error {
		return &GeneratedShotSelectionValidationError{Field: field, Code: code, Message: message}
	}
	if err := ValidateGeneratedShotCandidateSet(set); err != nil {
		return fail("candidate_set", "generated_selection_set_ineligible", err.Error())
	}
	if selection.SchemaVersion != GeneratedShotSelectionSchemaVersion || strings.TrimSpace(selection.SelectionID) == "" {
		return fail("schema_version", "generated_selection_schema_unsupported", "selection schema and identity are required")
	}
	if selection.SetID != set.SetID || selection.IntentID != set.IntentID || selection.Mode != set.Mode {
		return fail("set_id", "generated_selection_binding_mismatch", "selection must bind the candidate set, intent, and mode")
	}
	if selection.Status != GeneratedShotSelectedPendingEditorApproval || !selection.EditorApprovalRequired || selection.ApprovedForDemo || selection.IncludeInDemo || selection.AutoApply {
		return fail("status", "generated_selection_safety_envelope_invalid", "selection must remain pending editor approval and cannot approve, include, or auto-apply")
	}
	if strings.TrimSpace(selection.SelectorID) == "" || selection.SelectorKind != "human" || selection.SelectedAt.IsZero() {
		return fail("selector_kind", "generated_selection_human_required", "selection requires a human identity and timestamp")
	}
	if strings.TrimSpace(selection.Reason) == "" || len(nonEmptyUniqueStrings(selection.EvidenceRefs)) == 0 {
		return fail("evidence_refs", "generated_selection_evidence_missing", "selection requires a reason and evidence")
	}
	for _, entry := range set.Candidates {
		if entry.CandidateID == selection.SelectedCandidateID {
			if entry.Provider != selection.SelectedProvider {
				return fail("selected_provider", "generated_selection_provider_mismatch", "selected provider must match the candidate set")
			}
			return nil
		}
	}
	return fail("selected_candidate_id", "generated_selection_candidate_not_in_set", "selected candidate is not in the candidate set")
}

func asGeneratedShotSelectionValidationError(err error) *GeneratedShotSelectionValidationError {
	var target *GeneratedShotSelectionValidationError
	if errors.As(err, &target) {
		return target
	}
	return nil
}
