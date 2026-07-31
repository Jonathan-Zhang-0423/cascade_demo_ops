package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	StageExecutionEventSchemaVersion     = "demoops.stage_execution_event.v1"
	ValidationReportSchemaVersion        = "demoops.validation_report.v1"
	RuntimeRepairProposalSchemaVersion   = "demoops.runtime_repair_proposal.v1"
	RuntimePatchLedgerEntrySchemaVersion = "demoops.patch_ledger_entry.v1"
)

type StageExecutionEventType string

const (
	StageExecutionEventStageStarted         StageExecutionEventType = "stage_started"
	StageExecutionEventObservationCollected StageExecutionEventType = "observation_collected"
	StageExecutionEventTargetResolved       StageExecutionEventType = "target_resolved"
	StageExecutionEventActionStarted        StageExecutionEventType = "action_started"
	StageExecutionEventActionCompleted      StageExecutionEventType = "action_completed"
	StageExecutionEventOutcomeObserved      StageExecutionEventType = "outcome_observed"
	StageExecutionEventRepairProposed       StageExecutionEventType = "repair_proposed"
	StageExecutionEventRepairApplied        StageExecutionEventType = "repair_applied"
	StageExecutionEventStageCompleted       StageExecutionEventType = "stage_completed"
	StageExecutionEventStageFailed          StageExecutionEventType = "stage_failed"
)

type RuntimeObservationSource string

const (
	RuntimeObservationActualBrowser RuntimeObservationSource = "actual_browser_observation"
	RuntimeObservationAssertion     RuntimeObservationSource = "browser_assertion"
	RuntimeObservationArtifact      RuntimeObservationSource = "artifact_observation"
	RuntimeObservationDerivedPlan   RuntimeObservationSource = "derived_from_plan"
	RuntimeObservationInsufficient  RuntimeObservationSource = "insufficient_evidence"
)

type ValidationPhase string

const (
	ValidationPhasePreExecution  ValidationPhase = "pre_execution"
	ValidationPhaseRuntimeStage  ValidationPhase = "runtime_stage"
	ValidationPhasePostExecution ValidationPhase = "post_execution"
)

type ValidationDecision string

const (
	ValidationDecisionContinue                ValidationDecision = "continue"
	ValidationDecisionRepairAllowed           ValidationDecision = "repair_allowed"
	ValidationDecisionStopAndReport           ValidationDecision = "stop_and_report"
	ValidationDecisionReunderstandingRequired ValidationDecision = "reunderstanding_required"
)

type StageExecutionEvent struct {
	SchemaVersion          string                  `json:"schema_version"`
	EventID                string                  `json:"event_id"`
	RunID                  string                  `json:"run_id"`
	SourcePackageID        string                  `json:"source_package_id"`
	SourceBundleHashSHA256 string                  `json:"source_bundle_hash_sha256"`
	PolicyHashSHA256       string                  `json:"policy_hash_sha256"`
	NodeID                 string                  `json:"node_id"`
	StageID                string                  `json:"stage_id"`
	Attempt                int                     `json:"attempt"`
	Sequence               int64                   `json:"sequence"`
	EventType              StageExecutionEventType `json:"event_type"`
	OccurredAt             time.Time               `json:"occurred_at"`
	Action                 *RuntimeAction          `json:"action,omitempty"`
	Observation            *RuntimeObservation     `json:"observation,omitempty"`
	EvidenceRefs           []EvidenceRef           `json:"evidence_refs,omitempty"`
}

type RuntimeAction struct {
	Kind             string `json:"kind"`
	TargetSemanticID string `json:"target_semantic_id,omitempty"`
}

type RuntimeObservation struct {
	Source     RuntimeObservationSource `json:"source"`
	URL        string                   `json:"url,omitempty"`
	Title      string                   `json:"title,omitempty"`
	Assertions []RuntimeAssertion       `json:"assertions,omitempty"`
}

type RuntimeAssertion struct {
	Kind         string        `json:"kind"`
	Passed       bool          `json:"passed"`
	Actual       string        `json:"actual,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type ValidationReport struct {
	SchemaVersion          string                   `json:"schema_version"`
	ReportID               string                   `json:"report_id"`
	RunID                  string                   `json:"run_id"`
	SourcePackageID        string                   `json:"source_package_id"`
	SourceBundleHashSHA256 string                   `json:"source_bundle_hash_sha256"`
	PolicyHashSHA256       string                   `json:"policy_hash_sha256"`
	Phase                  ValidationPhase          `json:"phase"`
	NodeID                 string                   `json:"node_id,omitempty"`
	StageID                string                   `json:"stage_id,omitempty"`
	Decision               ValidationDecision       `json:"decision"`
	PassRate               float64                  `json:"pass_rate"`
	OverallConfidence      float64                  `json:"overall_confidence"`
	EvidenceQuality        RuntimeObservationSource `json:"evidence_quality"`
	Checks                 []ValidationCheck        `json:"checks,omitempty"`
	EvidenceRefs           []EvidenceRef            `json:"evidence_refs,omitempty"`
	RepairProposalRefs     []string                 `json:"repair_proposal_refs,omitempty"`
	CreatedAt              time.Time                `json:"created_at"`
}

type ValidationCheck struct {
	ID           string          `json:"id"`
	Kind         string          `json:"kind"`
	Code         string          `json:"code,omitempty"`
	Severity     FindingSeverity `json:"severity,omitempty"`
	Passed       bool            `json:"passed"`
	Required     bool            `json:"required"`
	Summary      string          `json:"summary,omitempty"`
	EvidenceRefs []EvidenceRef   `json:"evidence_refs,omitempty"`
}

type RuntimeRepairProposal struct {
	SchemaVersion        string        `json:"schema_version"`
	ProposalID           string        `json:"proposal_id"`
	RunID                string        `json:"run_id"`
	NodeID               string        `json:"node_id"`
	StageID              string        `json:"stage_id"`
	BaseBundleHashSHA256 string        `json:"base_bundle_hash_sha256"`
	PolicyHashSHA256     string        `json:"policy_hash_sha256"`
	RepairKind           string        `json:"repair_kind"`
	Field                string        `json:"field"`
	Before               string        `json:"before,omitempty"`
	After                string        `json:"after"`
	Confidence           float64       `json:"confidence"`
	EvidenceRefs         []EvidenceRef `json:"evidence_refs,omitempty"`
	RequiresApproval     bool          `json:"requires_approval"`
	CreatedAt            time.Time     `json:"created_at"`
}

type RuntimePatchLedgerEntry struct {
	SchemaVersion          string             `json:"schema_version"`
	EntryID                string             `json:"entry_id"`
	ProposalID             string             `json:"proposal_id"`
	RunID                  string             `json:"run_id"`
	NodeID                 string             `json:"node_id"`
	StageID                string             `json:"stage_id"`
	Attempt                int                `json:"attempt"`
	SourceBundleHashSHA256 string             `json:"source_bundle_hash_sha256"`
	PolicyHashSHA256       string             `json:"policy_hash_sha256"`
	Field                  string             `json:"field"`
	Before                 string             `json:"before,omitempty"`
	After                  string             `json:"after"`
	PolicyDecision         ValidationDecision `json:"policy_decision"`
	Reason                 string             `json:"reason,omitempty"`
	Applied                bool               `json:"applied"`
	RolledBack             bool               `json:"rolled_back,omitempty"`
	EvidenceRefs           []EvidenceRef      `json:"evidence_refs,omitempty"`
	AppliedAt              time.Time          `json:"applied_at,omitempty"`
}

func (e StageExecutionEvent) Validate() error {
	if e.SchemaVersion != StageExecutionEventSchemaVersion {
		return errors.New("unsupported stage execution event schema version")
	}
	if anyBlank(e.EventID, e.RunID, e.SourcePackageID, e.SourceBundleHashSHA256, e.PolicyHashSHA256, e.NodeID, e.StageID) {
		return errors.New("stage execution event is missing required identity fields")
	}
	if e.Attempt < 1 || e.Sequence < 1 || e.OccurredAt.IsZero() {
		return errors.New("stage execution event requires positive attempt and sequence and occurred_at")
	}
	if !validStageExecutionEventType(e.EventType) {
		return fmt.Errorf("unsupported stage execution event type %q", e.EventType)
	}
	if e.Observation != nil {
		if !validObservationSource(e.Observation.Source) {
			return fmt.Errorf("unsupported runtime observation source %q", e.Observation.Source)
		}
		if err := validateRuntimeContractText(e.Observation.URL, e.Observation.Title); err != nil {
			return err
		}
		for _, assertion := range e.Observation.Assertions {
			if strings.TrimSpace(assertion.Kind) == "" {
				return errors.New("runtime assertion kind is required")
			}
			if err := validateRuntimeContractText(assertion.Actual); err != nil {
				return err
			}
			if err := validateRuntimeEvidenceRefs(assertion.EvidenceRefs); err != nil {
				return err
			}
		}
	}
	return validateRuntimeEvidenceRefs(e.EvidenceRefs)
}

func (r ValidationReport) Validate() error {
	if r.SchemaVersion != ValidationReportSchemaVersion {
		return errors.New("unsupported validation report schema version")
	}
	if anyBlank(r.ReportID, r.RunID, r.SourcePackageID, r.SourceBundleHashSHA256, r.PolicyHashSHA256) || r.CreatedAt.IsZero() {
		return errors.New("validation report is missing required identity fields or created_at")
	}
	if !validValidationPhase(r.Phase) || !validValidationDecision(r.Decision) || !validObservationSource(r.EvidenceQuality) {
		return errors.New("validation report contains unsupported phase, decision, or evidence quality")
	}
	if r.Phase == ValidationPhaseRuntimeStage && anyBlank(r.NodeID, r.StageID) {
		return errors.New("runtime-stage validation report requires node_id and stage_id")
	}
	if !unitInterval(r.PassRate) || !unitInterval(r.OverallConfidence) {
		return errors.New("validation report pass_rate and overall_confidence must be between 0 and 1")
	}
	// Pre-execution validation can only rely on the approved, hash-bound
	// package. Runtime and post-execution success still require browser or
	// artifact evidence.
	if r.Decision == ValidationDecisionContinue && r.Phase != ValidationPhasePreExecution && !realSuccessEvidence(r.EvidenceQuality) {
		return errors.New("continue decision requires real observed evidence")
	}
	if r.Decision == ValidationDecisionContinue && len(r.EvidenceRefs) == 0 {
		return errors.New("continue decision requires evidence_refs")
	}
	for _, check := range r.Checks {
		if anyBlank(check.ID, check.Kind) {
			return errors.New("validation check requires id and kind")
		}
		if check.Severity != "" && check.Severity != FindingSeverityInfo && check.Severity != FindingSeverityWarning && check.Severity != FindingSeverityBlocking {
			return errors.New("validation check severity must be info, warning, or blocking")
		}
		if err := validateRuntimeContractText(check.Summary); err != nil {
			return err
		}
		if err := validateRuntimeEvidenceRefs(check.EvidenceRefs); err != nil {
			return err
		}
	}
	return validateRuntimeEvidenceRefs(r.EvidenceRefs)
}

func (p RuntimeRepairProposal) Validate() error {
	if p.SchemaVersion != RuntimeRepairProposalSchemaVersion {
		return errors.New("unsupported runtime repair proposal schema version")
	}
	if anyBlank(p.ProposalID, p.RunID, p.NodeID, p.StageID, p.BaseBundleHashSHA256, p.PolicyHashSHA256, p.RepairKind, p.Field, p.After) || p.CreatedAt.IsZero() {
		return errors.New("runtime repair proposal is missing required fields or created_at")
	}
	if !unitInterval(p.Confidence) {
		return errors.New("runtime repair proposal confidence must be between 0 and 1")
	}
	if err := validateRuntimeContractText(p.Field, p.Before, p.After); err != nil {
		return err
	}
	return validateRuntimeEvidenceRefs(p.EvidenceRefs)
}

func (e RuntimePatchLedgerEntry) Validate() error {
	if e.SchemaVersion != RuntimePatchLedgerEntrySchemaVersion {
		return errors.New("unsupported runtime patch ledger entry schema version")
	}
	if anyBlank(e.EntryID, e.ProposalID, e.RunID, e.NodeID, e.StageID, e.SourceBundleHashSHA256, e.PolicyHashSHA256, e.Field, e.After) {
		return errors.New("runtime patch ledger entry is missing required fields")
	}
	if e.Attempt < 1 {
		return errors.New("runtime patch ledger entry requires a positive attempt")
	}
	if e.PolicyDecision != ValidationDecisionRepairAllowed && e.PolicyDecision != ValidationDecisionStopAndReport {
		return errors.New("runtime patch ledger entry policy_decision must be repair_allowed or stop_and_report")
	}
	if e.Applied && e.AppliedAt.IsZero() {
		return errors.New("applied runtime patch ledger entry requires applied_at")
	}
	if e.RolledBack && !e.Applied {
		return errors.New("runtime patch cannot be rolled back before it was applied")
	}
	if err := validateRuntimeContractText(e.Field, e.Before, e.After, e.Reason); err != nil {
		return err
	}
	return validateRuntimeEvidenceRefs(e.EvidenceRefs)
}

func validStageExecutionEventType(value StageExecutionEventType) bool {
	switch value {
	case StageExecutionEventStageStarted, StageExecutionEventObservationCollected, StageExecutionEventTargetResolved,
		StageExecutionEventActionStarted, StageExecutionEventActionCompleted, StageExecutionEventOutcomeObserved,
		StageExecutionEventRepairProposed, StageExecutionEventRepairApplied, StageExecutionEventStageCompleted,
		StageExecutionEventStageFailed:
		return true
	default:
		return false
	}
}

func validObservationSource(value RuntimeObservationSource) bool {
	switch value {
	case RuntimeObservationActualBrowser, RuntimeObservationAssertion, RuntimeObservationArtifact,
		RuntimeObservationDerivedPlan, RuntimeObservationInsufficient:
		return true
	default:
		return false
	}
}

func validValidationPhase(value ValidationPhase) bool {
	return value == ValidationPhasePreExecution || value == ValidationPhaseRuntimeStage || value == ValidationPhasePostExecution
}

func validValidationDecision(value ValidationDecision) bool {
	return value == ValidationDecisionContinue || value == ValidationDecisionRepairAllowed ||
		value == ValidationDecisionStopAndReport || value == ValidationDecisionReunderstandingRequired
}

func realSuccessEvidence(value RuntimeObservationSource) bool {
	return value == RuntimeObservationActualBrowser || value == RuntimeObservationAssertion || value == RuntimeObservationArtifact
}

func unitInterval(value float64) bool {
	return value >= 0 && value <= 1
}

func anyBlank(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}

func validateRuntimeEvidenceRefs(refs []EvidenceRef) error {
	for _, ref := range refs {
		if strings.TrimSpace(ref.ID) == "" {
			return errors.New("runtime evidence ref id is required")
		}
		if err := validateRuntimeContractText(ref.ID, ref.Summary, ref.FieldPath, ref.ArtifactID); err != nil {
			return err
		}
	}
	return nil
}

func validateRuntimeContractText(values ...string) error {
	for _, value := range values {
		lower := strings.ToLower(value)
		if containsUnsafeDiagnosticToken(value) || strings.Contains(lower, "<!doctype html") || strings.Contains(lower, "<html") {
			return errors.New("browser-agent runtime contract contains sensitive or full-page content")
		}
	}
	return nil
}

// BrowserAgentValidationContext is an immutable view of the App-approved
// package passed to OutcomeVerifier methods. Lives in model to avoid circular
// imports between app and orchestrator.
type BrowserAgentValidationContext struct {
	RunID                     string
	SourcePackageID           string
	SourceBundleHashSHA256    string
	EffectivePolicyHashSHA256 string
	AllowedDomains            []string
	WorkflowGraph             *DemoWorkflowGraph
	Plan                      *ExecutionScriptDocument
	StageApprovalPlan         *StageApprovalPlan
	ScriptOutline             *BrowserAgentScriptOutline
	BrowserAgentContract      *BrowserAgentContract
}
