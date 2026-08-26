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
	StageExecutionEventStageStarted             StageExecutionEventType = "stage_started"
	StageExecutionEventObservationCollected     StageExecutionEventType = "observation_collected"
	StageExecutionEventTargetResolved           StageExecutionEventType = "target_resolved"
	StageExecutionEventActionStarted            StageExecutionEventType = "action_started"
	StageExecutionEventActionCompleted          StageExecutionEventType = "action_completed"
	StageExecutionEventOutcomeObserved          StageExecutionEventType = "outcome_observed"
	StageExecutionEventRepairProposed           StageExecutionEventType = "repair_proposed"
	StageExecutionEventRepairApplied            StageExecutionEventType = "repair_applied"
	StageExecutionEventStageResumed             StageExecutionEventType = "stage_resumed"
	StageExecutionEventBusinessStateObserved    StageExecutionEventType = "business_state_observed"
	StageExecutionEventStepSatisfied            StageExecutionEventType = "step_satisfied_by_observation"
	StageExecutionEventActionEffectCommitted    StageExecutionEventType = "action_effect_committed"
	StageExecutionEventActionEffectReclassified StageExecutionEventType = "action_effect_reclassified"
	StageExecutionEventTransitionAbsorbed       StageExecutionEventType = "transition_absorbed"
	StageExecutionEventConfidenceDeferred       StageExecutionEventType = "confidence_deferred"
	StageExecutionEventCapabilityScored         StageExecutionEventType = "capability_scored"
	StageExecutionEventStageCompleted           StageExecutionEventType = "stage_completed"
	StageExecutionEventStageFailed              StageExecutionEventType = "stage_failed"
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
	HarnessDecision        *HarnessDecision        `json:"harness_decision,omitempty"`
	ActionEffect           *ActionEffectCheckpoint `json:"action_effect,omitempty"`
	CapabilityScore        *CapabilityScore        `json:"capability_score,omitempty"`
	EvidenceRefs           []EvidenceRef           `json:"evidence_refs,omitempty"`
}

type RuntimeAction struct {
	Kind             string `json:"kind"`
	TargetSemanticID string `json:"target_semantic_id,omitempty"`
}

type RuntimeObservation struct {
	Source                   RuntimeObservationSource         `json:"source"`
	URL                      string                           `json:"url,omitempty"`
	Title                    string                           `json:"title,omitempty"`
	Assertions               []RuntimeAssertion               `json:"assertions,omitempty"`
	TargetGeometry           *BrowserTargetGeometry           `json:"target_geometry,omitempty"`
	TargetResolutionAttempts []BrowserTargetResolutionAttempt `json:"target_resolution_attempts,omitempty"`
	StateFingerprint         *BrowserStateFingerprint         `json:"state_fingerprint,omitempty"`
	BusinessState            *BusinessStateSnapshot           `json:"business_state,omitempty"`
}

type BrowserStateFingerprint struct {
	Origin             string            `json:"origin,omitempty"`
	RouteTemplate      string            `json:"route_template,omitempty"`
	DocumentDigest     string            `json:"document_digest,omitempty"`
	ARIADigest         string            `json:"aria_digest,omitempty"`
	VisualRegionDigest string            `json:"visual_region_digest,omitempty"`
	FrameDigests       map[string]string `json:"frame_digests,omitempty"`
	ObservedAt         time.Time         `json:"observed_at"`
}

// BrowserTargetResolutionAttempt is a redacted locator diagnostic. It records
// only counts and policy outcomes, never selector values, page text, DOM, or
// form values.
type BrowserTargetResolutionAttempt struct {
	Strategy       string `json:"strategy"`
	CandidateCount int    `json:"candidate_count"`
	Unique         bool   `json:"unique"`
	Visible        bool   `json:"visible"`
	EvidenceBound  bool   `json:"evidence_bound"`
	RoleAllowed    *bool  `json:"role_allowed,omitempty"`
	NameAllowed    *bool  `json:"name_allowed,omitempty"`
	Outcome        string `json:"outcome"`
}

// BrowserTargetGeometry is captured from the live DOM immediately before the
// approved action. It contains no page text or input values.
type BrowserTargetGeometry struct {
	SchemaVersion        string                   `json:"schema_version"`
	TargetSemanticID     string                   `json:"target_semantic_id"`
	ResolutionStrategy   string                   `json:"resolution_strategy"`
	SelectorDigestSHA256 string                   `json:"selector_digest_sha256"`
	CapturedAt           time.Time                `json:"captured_at"`
	RecordingOffsetMS    int64                    `json:"recording_offset_ms"`
	Viewport             BrowserGeometryViewport  `json:"viewport"`
	ElementBoxCSSPX      BrowserGeometryRectangle `json:"element_box_css_px"`
	ElementBoxNormalized BrowserGeometryRectangle `json:"element_box_normalized"`
	ScreenshotArtifactID string                   `json:"screenshot_artifact_id,omitempty"`
	Confidence           float64                  `json:"confidence"`
}

type BrowserGeometryViewport struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	DPR    float64 `json:"dpr"`
}

type BrowserGeometryRectangle struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
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
	NodeID       string          `json:"node_id,omitempty"`
	StageID      string          `json:"stage_id,omitempty"`
	Severity     FindingSeverity `json:"severity,omitempty"`
	Passed       bool            `json:"passed"`
	Required     bool            `json:"required"`
	Summary      string          `json:"summary,omitempty"`
	EvidenceRefs []EvidenceRef   `json:"evidence_refs,omitempty"`
	// P1.2: structured feedback fields populated by AnnotateValidationChecks
	Impact     string `json:"impact,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
	NextStep   string `json:"next_step,omitempty"`
	// P1.3: responsibility domain for cross-team triage
	ResponsibilityDomain ValidationCheckDomain `json:"responsibility_domain,omitempty"`
	// Artifact reference when check targets a specific artifact
	ArtifactID string `json:"artifact_id,omitempty"`
}

// ValidationCheckDomain identifies which side of the system is responsible
// for resolving the issue reported in a ValidationCheck.
type ValidationCheckDomain string

const (
	ValidationCheckDomainApp               ValidationCheckDomain = "app"                // App产包、审批计划或执行包字段问题
	ValidationCheckDomainServer            ValidationCheckDomain = "server"             // Server 执行引擎或 Browser Agent 运行时问题
	ValidationCheckDomainValidation        ValidationCheckDomain = "validation"         // Validation Agent 自身配置或逻辑问题
	ValidationCheckDomainEnvironment       ValidationCheckDomain = "environment"        // Node、FFmpeg、Chromium 等环境问题
	ValidationCheckDomainMediaDelivery     ValidationCheckDomain = "media_delivery"     // 视频编辑、MP4生成、artifact上传等媒体交付问题
	ValidationCheckDomainBrowserRuntime    ValidationCheckDomain = "browser_runtime"    // 页面导航、会话、可见性或超时问题
	ValidationCheckDomainProviderCandidate ValidationCheckDomain = "provider_candidate" // 仅候选模型任务，不代表页面业务成功
)

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
		if e.Observation.TargetGeometry != nil {
			if err := validateBrowserTargetGeometry(*e.Observation.TargetGeometry); err != nil {
				return err
			}
		}
		if len(e.Observation.TargetResolutionAttempts) > 128 {
			return errors.New("runtime observation contains too many target resolution attempts")
		}
		for _, attempt := range e.Observation.TargetResolutionAttempts {
			if strings.TrimSpace(attempt.Strategy) == "" || !validTargetResolutionOutcome(attempt.Outcome) {
				return errors.New("target resolution attempt requires a strategy and supported outcome")
			}
			if attempt.CandidateCount < 0 || attempt.CandidateCount > 10_000 {
				return errors.New("target resolution attempt candidate_count is out of range")
			}
			if err := validateRuntimeContractText(attempt.Strategy, attempt.Outcome); err != nil {
				return err
			}
		}
		if e.Observation.BusinessState != nil {
			if err := ValidateBusinessStateSnapshot(*e.Observation.BusinessState); err != nil {
				return err
			}
		}
	}
	return validateRuntimeEvidenceRefs(e.EvidenceRefs)
}

func validTargetResolutionOutcome(value string) bool {
	switch value {
	case "resolved", "no_candidates", "ambiguous", "not_visible", "role_mismatch", "name_mismatch", "forbidden_name", "unsupported_selector":
		return true
	default:
		return false
	}
}

func validateBrowserTargetGeometry(value BrowserTargetGeometry) error {
	if value.SchemaVersion != "demoops.browser_target_geometry.v1" {
		return errors.New("unsupported browser target geometry schema version")
	}
	if anyBlank(value.TargetSemanticID, value.ResolutionStrategy, value.SelectorDigestSHA256) || value.CapturedAt.IsZero() {
		return errors.New("browser target geometry is missing identity fields or captured_at")
	}
	if value.RecordingOffsetMS < 0 || value.Viewport.Width <= 0 || value.Viewport.Height <= 0 || value.Viewport.DPR <= 0 {
		return errors.New("browser target geometry requires a valid recording offset and viewport")
	}
	if value.ElementBoxCSSPX.Width <= 0 || value.ElementBoxCSSPX.Height <= 0 {
		return errors.New("browser target geometry requires a positive CSS pixel box")
	}
	normalized := value.ElementBoxNormalized
	if normalized.X < 0 || normalized.Y < 0 || normalized.Width <= 0 || normalized.Height <= 0 || normalized.X+normalized.Width > 1.000001 || normalized.Y+normalized.Height > 1.000001 {
		return errors.New("browser target geometry normalized box must remain within the viewport")
	}
	if !unitInterval(value.Confidence) {
		return errors.New("browser target geometry confidence must be between 0 and 1")
	}
	return validateRuntimeContractText(value.TargetSemanticID, value.ResolutionStrategy, value.SelectorDigestSHA256, value.ScreenshotArtifactID)
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
		StageExecutionEventRepairProposed, StageExecutionEventRepairApplied, StageExecutionEventStageResumed,
		StageExecutionEventBusinessStateObserved, StageExecutionEventStepSatisfied, StageExecutionEventActionEffectCommitted,
		StageExecutionEventActionEffectReclassified, StageExecutionEventTransitionAbsorbed, StageExecutionEventConfidenceDeferred,
		StageExecutionEventCapabilityScored, StageExecutionEventStageCompleted,
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

// RuntimeObservationRecordsOptionalCapability identifies the explicit
// terminal evidence used when an enhancement is absent but the core workflow
// remains valid. It intentionally requires an actual browser observation; a
// plan-derived assertion cannot soft-pass an optional business capability.
func RuntimeObservationRecordsOptionalCapability(observation *RuntimeObservation) bool {
	if observation == nil || observation.Source != RuntimeObservationActualBrowser {
		return false
	}
	for _, assertion := range observation.Assertions {
		if assertion.Kind == "optional_capability_recorded" && assertion.Passed {
			return true
		}
	}
	return false
}

func validValidationPhase(value ValidationPhase) bool {
	return value == ValidationPhasePreExecution || value == ValidationPhaseRuntimeStage || value == ValidationPhasePostExecution
}

func validValidationDecision(value ValidationDecision) bool {
	return value == ValidationDecisionContinue || value == ValidationDecisionRepairAllowed ||
		value == ValidationDecisionStopAndReport || value == ValidationDecisionReunderstandingRequired
}

// RuntimeObservationIsRealEvidence reports whether an observation came from
// the running browser or a runtime artifact. Plan-derived observations never
// satisfy execution or delivery verification.
func RuntimeObservationIsRealEvidence(value RuntimeObservationSource) bool {
	return value == RuntimeObservationActualBrowser || value == RuntimeObservationAssertion || value == RuntimeObservationArtifact
}

func realSuccessEvidence(value RuntimeObservationSource) bool {
	return RuntimeObservationIsRealEvidence(value)
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
	ForbiddenActions          []string
	// Readiness validation must see the same package-owned business summary and
	// credential grant metadata that Intake approved. Omitting these fields can
	// turn a valid secret reference into a false credential_grant_missing
	// blocker before Chromium starts.
	ProjectContextSummary ProjectContextSummary
	ProductMapSummary     ProductMapSummary
	CredentialGrants      []CredentialGrant
	WorkflowGraph         *DemoWorkflowGraph
	Plan                  *ExecutionScriptDocument
	StageApprovalPlan     *StageApprovalPlan
	ScriptOutline         *BrowserAgentScriptOutline
	BrowserAgentContract  *BrowserAgentContract
}
