package model

import "time"

const ExecutionScriptDocumentSchemaVersion = "demoops.execution_script_document.v1"

type ScriptDocumentStatus string

const (
	ScriptDocumentStatusDraft       ScriptDocumentStatus = "draft"
	ScriptDocumentStatusReviewReady ScriptDocumentStatus = "review_ready"
	ScriptDocumentStatusApproved    ScriptDocumentStatus = "approved"
)

type ExecutionScriptDocument struct {
	ID                string                  `json:"id"`
	ProjectID         string                  `json:"project_id"`
	WorkflowGraphID   string                  `json:"workflow_graph_id"`
	GraphVersion      int                     `json:"graph_version"`
	SchemaVersion     string                  `json:"schema_version"`
	Status            ScriptDocumentStatus    `json:"status,omitempty"`
	Title             string                  `json:"title,omitempty"`
	Summary           string                  `json:"summary,omitempty"`
	Language          string                  `json:"language,omitempty"`
	WorkflowGraph     *DemoWorkflowGraph      `json:"workflow_graph,omitempty"`
	RecordingRunSpec  RecordingRunSpec        `json:"recording_run_spec"`
	Steps             []ScriptStep            `json:"steps"`
	SafetyPolicy      ScriptSafetyPolicy      `json:"safety_policy"`
	Reproducibility   ReproducibilitySpec     `json:"reproducibility"`
	ApprovalChecklist ScriptApprovalChecklist `json:"approval_checklist"`
	EvidenceRefs      []EvidenceRef           `json:"evidence_refs,omitempty"`
	MarkdownArtifact  *ArtifactRef            `json:"markdown_artifact,omitempty"`
	CreatedAt         time.Time               `json:"created_at,omitempty"`
	UpdatedAt         time.Time               `json:"updated_at,omitempty"`
}

type ScriptStep struct {
	ID                  string                      `json:"id"`
	Order               int                         `json:"order"`
	NodeID              string                      `json:"node_id"`
	StageKind           BusinessStageKind           `json:"stage_kind,omitempty"`
	RouteState          BusinessRouteState          `json:"route_state,omitempty"`
	NonDestructive      bool                        `json:"non_destructive,omitempty"`
	RuntimeAdaptive     bool                        `json:"runtime_adaptive,omitempty"`
	Title               string                      `json:"title,omitempty"`
	BusinessValue       string                      `json:"business_value,omitempty"`
	PageTarget          ScriptPageTarget            `json:"page_target"`
	Action              ScriptActionInstruction     `json:"action"`
	TargetContract      *BrowserAgentTargetContract `json:"target_contract,omitempty"`
	InteractionContract *InteractionContract        `json:"interaction_contract,omitempty"`
	ExpectedOutcome     string                      `json:"expected_outcome"`
	Validations         []ValidationSpec            `json:"validations"`
	Capture             CaptureSpec                 `json:"capture"`
	Timing              NodeTimingHint              `json:"timing"`
	Narrative           NarrativeCue                `json:"narrative"`
	EvidenceRefs        []EvidenceRef               `json:"evidence_refs,omitempty"`
	Blocking            bool                        `json:"blocking"`
}

type ScriptPageTarget struct {
	URL                  string              `json:"url,omitempty"`
	Selector             string              `json:"selector,omitempty"`
	SelectorAlternatives []SelectorCandidate `json:"selector_alternatives,omitempty"`
	PageRef              string              `json:"page_ref,omitempty"`
}

type ScriptActionInstruction struct {
	Type          GraphActionType  `json:"type"`
	Target        ActionTarget     `json:"target"`
	Value         string           `json:"value,omitempty"`
	InputRef      string           `json:"input_ref,omitempty"`
	SecretRef     string           `json:"secret_ref,omitempty"`
	Parameters    map[string]any   `json:"parameters,omitempty"`
	TimeoutMS     int              `json:"timeout_ms,omitempty"`
	WaitUntil     string           `json:"wait_until,omitempty"`
	Preconditions []StateAssertion `json:"preconditions,omitempty"`
}

type ScriptSafetyPolicy struct {
	AllowedDomains []string        `json:"allowed_domains,omitempty"`
	ForbiddenPages []string        `json:"forbidden_pages,omitempty"`
	ForbiddenData  []string        `json:"forbidden_data,omitempty"`
	Redactions     RedactionPolicy `json:"redactions"`
	PIIHandling    string          `json:"pii_handling,omitempty"`
}

type ScriptApprovalChecklist struct {
	HumanApprovalRequired              bool     `json:"human_approval_required"`
	SourceSummaryOnly                  bool     `json:"source_summary_only"`
	CredentialScopeReviewRequired      bool     `json:"credential_scope_review_required"`
	RedactionsReviewRequired           bool     `json:"redactions_review_required"`
	IPAllowlistAcknowledgementRequired bool     `json:"ip_allowlist_acknowledgement_required"`
	BlockingReasons                    []string `json:"blocking_reasons,omitempty"`
}

type ScriptDocumentPackage struct {
	Document         *ExecutionScriptDocument         `json:"document"`
	Markdown         string                           `json:"markdown,omitempty"`
	MarkdownArtifact *ArtifactRef                     `json:"markdown_artifact,omitempty"`
	ExecutableBundle *ExecutableRecordingScriptBundle `json:"executable_bundle,omitempty"`
}

func (d *ExecutionScriptDocument) ComputeScriptHash() (string, error) {
	if d == nil {
		return "", nil
	}
	copy := *d
	copy.Reproducibility.ScriptHashSHA256 = ""
	copy.MarkdownArtifact = nil
	return DigestCanonicalJSON(copy)
}
