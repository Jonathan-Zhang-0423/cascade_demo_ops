package model

import "time"

// Validation-related data structures for the Server-Side Stage Validation Agent
// legacy compatibility path. Browser-agent runtime validation uses the distinct
// types in browser_agent_runtime.go.

const (
	// ValidationFeedbackContinue - All validations passed, continue execution
	ValidationFeedbackContinue = "continue"
	// ValidationFeedbackFineTune - Minor issues detected, apply runtime repair patch
	ValidationFeedbackFineTune = "fine_tune"
	// ValidationFeedbackReunderstandingRequired - Critical issues detected, rollback to app for reunderstanding
	ValidationFeedbackReunderstandingRequired = "reunderstanding_required"
)

// ValidationResultType - Type of validation result
type ValidationResultType string

const (
	ValidationResultTypePassed      ValidationResultType = "passed"
	ValidationResultTypeFailed      ValidationResultType = "failed"
	ValidationResultTypeWarning     ValidationResultType = "warning"
	ValidationResultTypeUnresolved  ValidationResultType = "unresolved"
	ValidationResultTypeUncertainty ValidationResultType = "uncertainty"
)

// LegacyValidationPhase - Phase of legacy validation
type LegacyValidationPhase string

const (
	LegacyValidationPhasePreExecution       LegacyValidationPhase = "pre_execution"
	LegacyValidationPhaseRealTimeBatch      LegacyValidationPhase = "real_time_batch"
	LegacyValidationPhasePostExecutionBatch LegacyValidationPhase = "post_execution_batch"
	LegacyValidationPhasePlayback           LegacyValidationPhase = "playback"
)

// ValidationContext - Complete context for validation across all phases
type ValidationContext struct {
	PackageID         string             `json:"package_id"`
	WorkflowGraph     *DemoWorkflowGraph `json:"workflow_graph"`
	RecordingRunSpec  RecordingRunSpec   `json:"recording_run_spec"`
	StageApprovalPlan *StageApprovalPlan `json:"stage_approval_plan"`
	ExecutionTrace    *ExecutionTrace    `json:"execution_trace,omitempty"`
	StepResults       []StepResult       `json:"step_results,omitempty"`
	CloudJobID        string             `json:"cloud_job_id,omitempty"`
	CreatedAt         time.Time          `json:"created_at,omitempty"`
}

// ValidationResult - Result of a single validation check
type ValidationResult struct {
	ID             string                 `json:"id"`
	NodeID         string                 `json:"node_id"`
	Phase          LegacyValidationPhase  `json:"phase"`
	Type           ValidationResultType   `json:"type"`
	Title          string                 `json:"title,omitempty"`
	Description    string                 `json:"description,omitempty"`
	ExpectedValue  string                 `json:"expected_value,omitempty"`
	ObservedValue  string                 `json:"observed_value,omitempty"`
	Confidence     float64                `json:"confidence,omitempty"`
	Critical       bool                   `json:"critical"`
	Blocker        bool                   `json:"blocker"`
	ValidationRule string                 `json:"validation_rule,omitempty"`
	RuleParameters map[string]interface{} `json:"rule_parameters,omitempty"`
	EvidenceRefs   []EvidenceRef          `json:"evidence_refs,omitempty"`
	Repaired       bool                   `json:"repaired,omitempty"`
	RepairDetails  *ValidationRepair      `json:"repair_details,omitempty"`
	Timestamp      time.Time              `json:"timestamp,omitempty"`
}

// ValidationRepair - Runtime repair patch details
type ValidationRepair struct {
	Kind              string                 `json:"kind,omitempty"`
	FieldName         string                 `json:"field_name,omitempty"`
	OriginalValue     interface{}            `json:"original_value,omitempty"`
	RepairedValue     interface{}            `json:"repaired_value,omitempty"`
	SelectorPatch     *SelectorPatch         `json:"selector_patch,omitempty"`
	WaitStrategyPatch *WaitStrategyPatch     `json:"wait_strategy_patch,omitempty"`
	TimingPatch       *TimingPatch           `json:"timing_patch,omitempty"`
	AppliedAt         time.Time              `json:"applied_at,omitempty"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

// SelectorPatch - Runtime selector repair
type SelectorPatch struct {
	NodeID           string  `json:"node_id,omitempty"`
	OriginalSelector string  `json:"original_selector,omitempty"`
	PatchedSelector  string  `json:"patched_selector,omitempty"`
	SelectorType     string  `json:"selector_type,omitempty"`
	Confidence       float64 `json:"confidence,omitempty"`
}

// WaitStrategyPatch - Runtime wait strategy repair
type WaitStrategyPatch struct {
	NodeID          string `json:"node_id,omitempty"`
	OriginalWait    string `json:"original_wait,omitempty"`
	PatchedWait     string `json:"patched_wait,omitempty"`
	TimeoutMS       int    `json:"timeout_ms,omitempty"`
	PollingInterval int    `json:"polling_interval_ms,omitempty"`
}

// TimingPatch - Runtime timing repair
type TimingPatch struct {
	NodeID          string `json:"node_id,omitempty"`
	CapturePoint    string `json:"capture_point,omitempty"`
	OriginalDelayMS int    `json:"original_delay_ms,omitempty"`
	PatchedDelayMS  int    `json:"patched_delay_ms,omitempty"`
	StabilizationMS int    `json:"stabilization_ms,omitempty"`
}

// StageFeedback - Feedback for a single stage
type StageFeedback struct {
	NodeID            string             `json:"node_id"`
	StageOrder        int                `json:"stage_order"`
	BusinessStageID   string             `json:"business_stage_id,omitempty"`
	FeedbackType      string             `json:"feedback_type"`
	ValidationResults []ValidationResult `json:"validation_results"`
	Summary           string             `json:"summary,omitempty"`
	RiskLevel         string             `json:"risk_level,omitempty"`
	Confidence        float64            `json:"confidence,omitempty"`
	RequiresRepair    bool               `json:"requires_repair,omitempty"`
	RepairsApplied    []ValidationRepair `json:"repairs_applied,omitempty"`
	Blocked           bool               `json:"blocked"`
	BlockReason       string             `json:"block_reason,omitempty"`
	EvidenceRefs      []EvidenceRef      `json:"evidence_refs,omitempty"`
	GeneratedAt       time.Time          `json:"generated_at,omitempty"`
}

// LegacyValidationReport - Complete validation report for all phases
// (legacy compatibility path: pre/post execution hooks around the old runtime).
type LegacyValidationReport struct {
	ID                  string                 `json:"id"`
	ValidationContextID string                 `json:"validation_context_id"`
	Phase               LegacyValidationPhase  `json:"phase"`
	StageFeedbacks      []StageFeedback        `json:"stage_feedbacks"`
	GlobalFeedbackType  string                 `json:"global_feedback_type"`
	Summary             string                 `json:"summary,omitempty"`
	OverallConfidence   float64                `json:"overall_confidence,omitempty"`
	PassRate            float64                `json:"pass_rate,omitempty"`
	TotalStages         int                    `json:"total_stages"`
	PassedStages        int                    `json:"passed_stages"`
	FailedStages        int                    `json:"failed_stages"`
	WarningStages       int                    `json:"warning_stages"`
	CriticalIssues      int                    `json:"critical_issues"`
	BlockerIssues       int                    `json:"blocker_issues"`
	RepairsApplied      []ValidationRepair     `json:"repairs_applied,omitempty"`
	Recommendation      string                 `json:"recommendation,omitempty"`
	EvidenceRefs        []EvidenceRef          `json:"evidence_refs,omitempty"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt           time.Time              `json:"created_at,omitempty"`
}

// PreValidationCheck - Pre-execution validation check
type PreValidationCheck struct {
	CheckID       string        `json:"check_id"`
	CheckCategory string        `json:"check_category"`
	CheckName     string        `json:"check_name"`
	Description   string        `json:"description,omitempty"`
	CheckType     string        `json:"check_type"` // "domain", "selector", "evidence", "blocking_uncertainty"
	NodeID        string        `json:"node_id,omitempty"`
	Target        string        `json:"target,omitempty"`
	ExpectedValue interface{}   `json:"expected_value,omitempty"`
	Threshold     float64       `json:"threshold,omitempty"`
	Required      bool          `json:"required"`
	Executed      bool          `json:"executed"`
	Passed        bool          `json:"passed"`
	Details       string        `json:"details,omitempty"`
	Error         string        `json:"error,omitempty"`
	Confidence    float64       `json:"confidence,omitempty"`
	RiskLevel     string        `json:"risk_level,omitempty"`
	EvidenceRefs  []EvidenceRef `json:"evidence_refs,omitempty"`
}

// PostExecutionAnalysis - Post-execution batch analysis result
type PostExecutionAnalysis struct {
	NodeID            string            `json:"node_id"`
	StepResult        *StepResult       `json:"step_result,omitempty"`
	ExpectedOutcome   *GraphNode        `json:"expected_outcome,omitempty"`
	StatusMatch       bool              `json:"status_match"`
	RouteMatch        bool              `json:"route_match,omitempty"`
	StateMatch        bool              `json:"state_match"`
	ArtifactMatch     bool              `json:"artifact_match"`
	DurationMatch     bool              `json:"duration_match"`
	ObservedIssues    []ValidationIssue `json:"observed_issues"`
	OverallAssessment string            `json:"overall_assessment"`
	Recommendation    string            `json:"recommendation"`
	Confidence        float64           `json:"confidence,omitempty"`
}

// ValidationIssue - Specific validation issue detected
type ValidationIssue struct {
	IssueID      string        `json:"issue_id"`
	Category     string        `json:"category"`
	Severity     string        `json:"severity"` // "critical", "major", "minor"
	Title        string        `json:"title"`
	Description  string        `json:"description"`
	Context      string        `json:"context,omitempty"`
	Expected     string        `json:"expected,omitempty"`
	Observed     string        `json:"observed,omitempty"`
	Recoverable  bool          `json:"recoverable"`
	Blocker      bool          `json:"blocker"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

// PlaybackValidationResult - Result of playback validation
type PlaybackValidationResult struct {
	NodeID             string            `json:"node_id"`
	PlaybackSessionID  string            `json:"playback_session_id"`
	SuccessStateMatch  bool              `json:"success_state_match"`
	PageRouteMatch     bool              `json:"page_route_match,omitempty"`
	ElementExists      bool              `json:"element_exists"`
	ScreenshotValid    bool              `json:"screenshot_valid"`
	StabilityVerified  bool              `json:"stability_verified"`
	PlaybackError      string            `json:"playback_error,omitempty"`
	ValidationIssues   []ValidationIssue `json:"validation_issues"`
	OverallResult      string            `json:"overall_result"`
	Confidence         float64           `json:"confidence,omitempty"`
	PlaybackDurationMS int               `json:"playback_duration_ms,omitempty"`
	EvidenceRefs       []EvidenceRef     `json:"evidence_refs,omitempty"`
	CreatedAt          time.Time         `json:"created_at,omitempty"`
}

// BlockReason - Reason for blocking execution
type BlockReason struct {
	ReasonCode string `json:"reason_code"`
	Message    string `json:"message"`
	NodeID     string `json:"node_id,omitempty"`
	Severity   string `json:"severity,omitempty"`
}

// BlockingReasons - Collection of blocking reasons
type BlockingReasons []BlockReason

// HasBlockingReasons - Check if there are any blocking reasons
func (br BlockingReasons) HasBlockingReasons() bool {
	for _, reason := range br {
		if reason.Severity == "critical" || reason.Severity == "blocking" {
			return true
		}
	}
	return false
}

// ValidationConfig - Configuration for validation behavior
type ValidationConfig struct {
	PreExecutionEnabled       bool    `json:"pre_execution_enabled"`
	RealTimeBatchEnabled      bool    `json:"real_time_batch_enabled"`
	PostExecutionBatchEnabled bool    `json:"post_execution_batch_enabled"`
	PlaybackValidationEnabled bool    `json:"playback_validation_enabled"`
	PassRateThreshold         float64 `json:"pass_rate_threshold"`
	ConfidenceThreshold       float64 `json:"confidence_threshold"`
	CriticalIssueThreshold    int     `json:"critical_issue_threshold"`
	EnableRuntimeRepair       bool    `json:"enable_runtime_repair"`
	AutoApplyMinorRepairs     bool    `json:"auto_apply_minor_repairs"`
	MaxRepairAttemptsPerStage int     `json:"max_repair_attempts_per_stage"`
	ParallelValidationEnabled bool    `json:"parallel_validation_enabled"`
	ValidationTimeoutSeconds  int     `json:"validation_timeout_seconds"`
}
