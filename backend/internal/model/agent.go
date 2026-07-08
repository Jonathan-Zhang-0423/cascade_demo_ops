package model

import "time"

type AgentStage string

const (
	AgentStageInputContext         AgentStage = "input_context"
	AgentStageProductUnderstanding AgentStage = "product_understanding"
	AgentStageWorkflowGraph        AgentStage = "workflow_graph"
	AgentStageExecutionQA          AgentStage = "execution_qa"
	AgentStageAssetGeneration      AgentStage = "asset_generation"
	AgentStageMaintenance          AgentStage = "maintenance"
)

type AgentTaskKind string

const (
	AgentTaskBuildProjectContext AgentTaskKind = "build_project_context"
	AgentTaskSnapshotRepo        AgentTaskKind = "snapshot_repo"
	AgentTaskSnapshotBrowser     AgentTaskKind = "snapshot_browser"
	AgentTaskBuildProductMap     AgentTaskKind = "build_product_map"
	AgentTaskDraftWorkflowGraph  AgentTaskKind = "draft_workflow_graph"
	AgentTaskDiagnoseFailure     AgentTaskKind = "diagnose_failure"
	AgentTaskPatchWorkflowGraph  AgentTaskKind = "patch_workflow_graph"
	AgentTaskPlanAssets          AgentTaskKind = "plan_assets"
	AgentTaskGenerateNarrative   AgentTaskKind = "generate_narrative"
)

type AgentRunStatus string

const (
	AgentRunStatusCreated          AgentRunStatus = "created"
	AgentRunStatusRunning          AgentRunStatus = "running"
	AgentRunStatusCompleted        AgentRunStatus = "completed"
	AgentRunStatusNeedsHuman       AgentRunStatus = "needs_human"
	AgentRunStatusRejectedByPolicy AgentRunStatus = "rejected_by_policy"
	AgentRunStatusFailed           AgentRunStatus = "failed"
)

type FindingSeverity string

const (
	FindingSeverityInfo     FindingSeverity = "info"
	FindingSeverityWarning  FindingSeverity = "warning"
	FindingSeverityBlocking FindingSeverity = "blocking"
)

type AgentRunEnvelope struct {
	ID            string              `json:"id"`
	ProjectID     string              `json:"project_id"`
	Stage         AgentStage          `json:"stage"`
	TaskKind      AgentTaskKind       `json:"task_kind"`
	Status        AgentRunStatus      `json:"status"`
	SchemaVersion string              `json:"schema_version,omitempty"`
	StartedAt     time.Time           `json:"started_at,omitempty"`
	CompletedAt   time.Time           `json:"completed_at,omitempty"`
	Input         *AgentInputPackage  `json:"input,omitempty"`
	Output        *AgentOutputPackage `json:"output,omitempty"`
	ModelTrace    *ModelTrace         `json:"model_trace,omitempty"`
	Error         *AgentError         `json:"error,omitempty"`
}

type AgentInputPackage struct {
	ProjectContext *ProjectContext    `json:"project_context,omitempty"`
	ProductMap     *ProductMap        `json:"product_map,omitempty"`
	WorkflowGraph  *DemoWorkflowGraph `json:"workflow_graph,omitempty"`
	Evidence       []EvidenceRef      `json:"evidence,omitempty"`
	ExecutionTrace *ExecutionTrace    `json:"execution_trace,omitempty"`
	Task           string             `json:"task,omitempty"`
	Constraints    []DemoRequirement  `json:"constraints,omitempty"`
	Metadata       map[string]any     `json:"metadata,omitempty"`
}

type AgentOutputPackage struct {
	Findings         []AgentFinding       `json:"findings,omitempty"`
	ProductMap       *ProductMap          `json:"product_map,omitempty"`
	WorkflowGraph    *DemoWorkflowGraph   `json:"workflow_graph,omitempty"`
	GraphPatch       *GraphPatch          `json:"graph_patch,omitempty"`
	FailureDiagnosis *FailureDiagnosis    `json:"failure_diagnosis,omitempty"`
	AssetPlan        *AssetPlan           `json:"asset_plan,omitempty"`
	HumanRequests    []HumanActionRequest `json:"human_requests,omitempty"`
	SafetyReport     *SafetyReport        `json:"safety_report,omitempty"`
	Confidence       float64              `json:"confidence,omitempty"`
}

type AgentFinding struct {
	ID              string          `json:"id"`
	Kind            string          `json:"kind"`
	Severity        FindingSeverity `json:"severity"`
	Title           string          `json:"title,omitempty"`
	Summary         string          `json:"summary"`
	Rationale       string          `json:"rationale,omitempty"`
	SuggestedAction string          `json:"suggested_action,omitempty"`
	EvidenceRefs    []EvidenceRef   `json:"evidence_refs,omitempty"`
	Confidence      float64         `json:"confidence,omitempty"`
}

type GraphPatch struct {
	ID             string                `json:"id"`
	BaseGraphID    string                `json:"base_graph_id"`
	BaseVersion    int                   `json:"base_version"`
	Operations     []GraphPatchOperation `json:"operations"`
	Rationale      string                `json:"rationale,omitempty"`
	SafetyChecks   []string              `json:"safety_checks,omitempty"`
	EvidenceRefs   []EvidenceRef         `json:"evidence_refs,omitempty"`
	RequiresReview bool                  `json:"requires_review"`
	CreatedAt      time.Time             `json:"created_at,omitempty"`
}

type GraphPatchOperation struct {
	Op           string          `json:"op"`
	Path         string          `json:"path,omitempty"`
	Node         *GraphNode      `json:"node,omitempty"`
	Edge         *GraphEdge      `json:"edge,omitempty"`
	Validation   *ValidationSpec `json:"validation,omitempty"`
	Value        any             `json:"value,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	EvidenceRefs []EvidenceRef   `json:"evidence_refs,omitempty"`
}

type ExecutionTrace struct {
	ID              string            `json:"id"`
	WorkflowGraphID string            `json:"workflow_graph_id"`
	GraphVersion    int               `json:"graph_version"`
	StartedAt       time.Time         `json:"started_at,omitempty"`
	CompletedAt     time.Time         `json:"completed_at,omitempty"`
	PassRate        float64           `json:"pass_rate,omitempty"`
	StepResults     []StepResult      `json:"step_results,omitempty"`
	Artifacts       []ArtifactRef     `json:"artifacts,omitempty"`
	Environment     map[string]string `json:"environment,omitempty"`
}

type StepResult struct {
	NodeID        string        `json:"node_id"`
	Status        string        `json:"status"`
	StartedAt     time.Time     `json:"started_at,omitempty"`
	CompletedAt   time.Time     `json:"completed_at,omitempty"`
	DurationMS    int           `json:"duration_ms,omitempty"`
	ObservedState string        `json:"observed_state,omitempty"`
	ValidationIDs []string      `json:"validation_ids,omitempty"`
	Artifacts     []ArtifactRef `json:"artifacts,omitempty"`
	Error         *AgentError   `json:"error,omitempty"`
}

type FailureDiagnosis struct {
	ID                 string              `json:"id"`
	FailedNodeID       string              `json:"failed_node_id,omitempty"`
	FailureKind        string              `json:"failure_kind,omitempty"`
	RootCause          string              `json:"root_cause,omitempty"`
	RepairConfidence   float64             `json:"repair_confidence,omitempty"`
	SelectorCandidates []SelectorCandidate `json:"selector_candidates,omitempty"`
	ProposedPatch      *GraphPatch         `json:"proposed_patch,omitempty"`
	HumanRequest       *HumanActionRequest `json:"human_request,omitempty"`
	EvidenceRefs       []EvidenceRef       `json:"evidence_refs,omitempty"`
}

type AssetPlan struct {
	ID              string           `json:"id"`
	WorkflowGraphID string           `json:"workflow_graph_id"`
	GraphVersion    int              `json:"graph_version"`
	Requests        []AssetRequest   `json:"requests,omitempty"`
	NarrativeRefs   []string         `json:"narrative_refs,omitempty"`
	BrandKit        *BrandKit        `json:"brand_kit,omitempty"`
	RenderSettings  *RenderSettings  `json:"render_settings,omitempty"`
	Provenance      *AssetProvenance `json:"provenance,omitempty"`
}

type RenderSettings struct {
	DurationSec      int      `json:"duration_sec,omitempty"`
	Transitions      []string `json:"transitions,omitempty"`
	ZoomCount        int      `json:"zoom_count,omitempty"`
	OutputFormats    []string `json:"output_formats,omitempty"`
	ResolutionWidth  int      `json:"resolution_width,omitempty"`
	ResolutionHeight int      `json:"resolution_height,omitempty"`
}

type HumanActionRequest struct {
	ID           string        `json:"id"`
	Kind         string        `json:"kind"`
	Priority     string        `json:"priority,omitempty"`
	Question     string        `json:"question,omitempty"`
	Options      []string      `json:"options,omitempty"`
	Blocking     bool          `json:"blocking"`
	NodeRefs     []string      `json:"node_refs,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type SafetyReport struct {
	AllowedToProceed bool           `json:"allowed_to_proceed"`
	PolicyFindings   []AgentFinding `json:"policy_findings,omitempty"`
	MaskedFields     []string       `json:"masked_fields,omitempty"`
	Notes            []string       `json:"notes,omitempty"`
}

type ModelTrace struct {
	Provider         string        `json:"provider,omitempty"`
	Model            string        `json:"model,omitempty"`
	PromptRef        string        `json:"prompt_ref,omitempty"`
	InputTokens      int           `json:"input_tokens,omitempty"`
	OutputTokens     int           `json:"output_tokens,omitempty"`
	LatencyMS        int           `json:"latency_ms,omitempty"`
	StructuredSchema string        `json:"structured_schema,omitempty"`
	EvidenceRefs     []EvidenceRef `json:"evidence_refs,omitempty"`
}

type AgentError struct {
	Code         string        `json:"code"`
	Message      string        `json:"message"`
	Retryable    bool          `json:"retryable,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}
