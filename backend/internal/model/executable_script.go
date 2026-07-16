package model

import "time"

const ExecutableRecordingScriptBundleSchemaVersion = "demoops.executable_recording_script_bundle.v1"
const StageApprovalPlanSchemaVersion = "demoops.stage_approval_plan.v1"
const BrowserAgentScriptOutlineSchemaVersion = "demoops.browser_agent_script_outline.v1"
const BrowserAgentPromptPolicySchemaVersion = "demoops.browser_agent_prompt_policy.v1"
const ProjectUnderstandingDossierSchemaVersion = "demoops.project_understanding_dossier.v1"

const (
	ExecutableScriptRuntimePlaywrightRestrictedSandbox = "playwright-restricted-sandbox"
	ExecutableScriptRuntimeBrowserAgentOutlineV1       = "browser-agent-outline-v1"
)

type ExecutableScriptBundleStatus string

const (
	ExecutableScriptBundleStatusDraft       ExecutableScriptBundleStatus = "draft"
	ExecutableScriptBundleStatusReviewReady ExecutableScriptBundleStatus = "review_ready"
	ExecutableScriptBundleStatusValidated   ExecutableScriptBundleStatus = "validated"
	ExecutableScriptBundleStatusRejected    ExecutableScriptBundleStatus = "rejected"
)

type ExecutableRecordingScriptBundle struct {
	ID                      string                          `json:"id"`
	ProjectID               string                          `json:"project_id"`
	WorkflowGraphID         string                          `json:"workflow_graph_id"`
	SchemaVersion           string                          `json:"schema_version"`
	Status                  ExecutableScriptBundleStatus    `json:"status,omitempty"`
	ScriptManifest          ExecutableScriptManifest        `json:"script_manifest"`
	PlanJSON                *ExecutionScriptDocument        `json:"plan_json"`
	PlaywrightScript        ExecutableScriptSource          `json:"playwright_script"`
	StageApprovalPlan       *StageApprovalPlan              `json:"stage_approval_plan,omitempty"`
	ScriptOutline           *BrowserAgentScriptOutline      `json:"script_outline,omitempty"`
	AgentPromptPolicy       *BrowserAgentPromptPolicy       `json:"agent_prompt_policy,omitempty"`
	UnderstandingDossierRef *ArtifactRef                    `json:"understanding_dossier_ref,omitempty"`
	UnderstandingDossier    *ProjectUnderstandingDossier    `json:"project_understanding_dossier,omitempty"`
	ApprovalMarkdown        ApprovalMarkdownDocument        `json:"approval_markdown"`
	SecurityPolicy          ExecutableScriptSecurityPolicy  `json:"security_policy"`
	Reproducibility         ExecutableScriptReproducibility `json:"reproducibility"`
	Validation              *ExecutableScriptValidation     `json:"validation,omitempty"`
	RepairLineage           *ScriptRepairLineage            `json:"repair_lineage,omitempty"`
	CreatedAt               time.Time                       `json:"created_at,omitempty"`
	UpdatedAt               time.Time                       `json:"updated_at,omitempty"`
}

type ScriptRepairLineage struct {
	BaseBundleID         string        `json:"base_bundle_id"`
	BaseBundleHashSHA256 string        `json:"base_bundle_hash_sha256"`
	SourceResultID       string        `json:"source_result_id"`
	SourceCloudJobID     string        `json:"source_cloud_job_id"`
	RepairAttempt        int           `json:"repair_attempt"`
	ChangeSummary        string        `json:"change_summary,omitempty"`
	DiagnosticRefs       []EvidenceRef `json:"diagnostic_refs,omitempty"`
	CreatedAt            time.Time     `json:"created_at,omitempty"`
}

type ExecutableScriptManifest struct {
	ScriptID            string   `json:"script_id"`
	Version             int      `json:"version"`
	Language            string   `json:"language"`
	Runtime             string   `json:"runtime"`
	EntryFunction       string   `json:"entry_function"`
	Generator           string   `json:"generator"`
	GeneratorVersion    string   `json:"generator_version"`
	DependencyAllowlist []string `json:"dependency_allowlist,omitempty"`
	ContextAPIs         []string `json:"context_apis,omitempty"`
	StepNodeIDs         []string `json:"step_node_ids"`
}

type ExecutableScriptSource struct {
	InlineSource string       `json:"inline_source,omitempty"`
	Artifact     *ArtifactRef `json:"artifact,omitempty"`
	MimeType     string       `json:"mime_type,omitempty"`
	SHA256       string       `json:"sha256"`
	SizeBytes    int64        `json:"size_bytes,omitempty"`
	Encrypted    bool         `json:"encrypted,omitempty"`
}

type ApprovalMarkdownDocument struct {
	InlineMarkdown string       `json:"inline_markdown,omitempty"`
	Artifact       *ArtifactRef `json:"artifact,omitempty"`
	MimeType       string       `json:"mime_type,omitempty"`
	SHA256         string       `json:"sha256"`
	SizeBytes      int64        `json:"size_bytes,omitempty"`
}

type ExecutableScriptSecurityPolicy struct {
	AllowedDomains       []string        `json:"allowed_domains,omitempty"`
	ForbiddenPages       []string        `json:"forbidden_pages,omitempty"`
	ForbiddenData        []string        `json:"forbidden_data,omitempty"`
	Redactions           RedactionPolicy `json:"redactions"`
	SecretRefs           []string        `json:"secret_refs,omitempty"`
	AllowedContextAPIs   []string        `json:"allowed_context_apis,omitempty"`
	AllowedPageMethods   []string        `json:"allowed_page_methods,omitempty"`
	ForbiddenImports     []string        `json:"forbidden_imports,omitempty"`
	ForbiddenIdentifiers []string        `json:"forbidden_identifiers,omitempty"`
	NetworkPolicy        string          `json:"network_policy,omitempty"`
	FileSystemPolicy     string          `json:"file_system_policy,omitempty"`
}

type ExecutableScriptReproducibility struct {
	PlanHashSHA256                 string            `json:"plan_hash_sha256"`
	ScriptHashSHA256               string            `json:"script_hash_sha256,omitempty"`
	StagePlanHashSHA256            string            `json:"stage_plan_hash_sha256,omitempty"`
	OutlineHashSHA256              string            `json:"outline_hash_sha256,omitempty"`
	PromptPolicyHashSHA256         string            `json:"prompt_policy_hash_sha256,omitempty"`
	UnderstandingDossierHashSHA256 string            `json:"understanding_dossier_hash_sha256,omitempty"`
	MarkdownHashSHA256             string            `json:"markdown_hash_sha256"`
	BundleHashSHA256               string            `json:"bundle_hash_sha256,omitempty"`
	GraphHashSHA256                string            `json:"graph_hash_sha256,omitempty"`
	SourceSnapshotDigest           string            `json:"source_snapshot_digest,omitempty"`
	GeneratorVersion               string            `json:"generator_version,omitempty"`
	DeterministicSeed              string            `json:"deterministic_seed,omitempty"`
	InputFingerprints              map[string]string `json:"input_fingerprints,omitempty"`
}

type ExecutableScriptValidation struct {
	Valid       bool           `json:"valid"`
	Findings    []AgentFinding `json:"findings,omitempty"`
	ValidatedAt time.Time      `json:"validated_at,omitempty"`
}

func (b *ExecutableRecordingScriptBundle) ComputeBundleHash() (string, error) {
	if b == nil {
		return "", nil
	}
	copy := *b
	copy.Reproducibility.BundleHashSHA256 = ""
	copy.Validation = nil
	copy.RepairLineage = nil
	return DigestCanonicalJSON(copy)
}

type StageApprovalPlan struct {
	ID                string               `json:"id"`
	ProjectID         string               `json:"project_id"`
	WorkflowGraphID   string               `json:"workflow_graph_id"`
	SchemaVersion     string               `json:"schema_version"`
	Title             string               `json:"title,omitempty"`
	Summary           string               `json:"summary,omitempty"`
	Runtime           string               `json:"runtime,omitempty"`
	Language          string               `json:"language,omitempty"`
	Stages            []StageApprovalStage `json:"stages"`
	SafetyPolicy      ScriptSafetyPolicy   `json:"safety_policy"`
	UncertaintyReport []StageUncertainty   `json:"uncertainty_report,omitempty"`
	EvidenceRefs      []EvidenceRef        `json:"evidence_refs,omitempty"`
	Confidence        float64              `json:"confidence,omitempty"`
	CreatedAt         time.Time            `json:"created_at,omitempty"`
	UpdatedAt         time.Time            `json:"updated_at,omitempty"`
}

type StageApprovalStage struct {
	ID             string                  `json:"id"`
	Order          int                     `json:"order"`
	NodeID         string                  `json:"node_id"`
	Title          string                  `json:"title,omitempty"`
	Objective      string                  `json:"objective,omitempty"`
	BusinessIntent string                  `json:"business_intent,omitempty"`
	DurationMS     int                     `json:"duration_ms,omitempty"`
	TargetRoute    string                  `json:"target_route,omitempty"`
	TargetURL      string                  `json:"target_url,omitempty"`
	ComponentRefs  []string                `json:"component_refs,omitempty"`
	APIRefs        []string                `json:"api_refs,omitempty"`
	StyleRefs      []string                `json:"style_refs,omitempty"`
	DataModelRefs  []string                `json:"data_model_refs,omitempty"`
	InputContent   []StageInputContent     `json:"input_content,omitempty"`
	Interaction    BrowserAgentInteraction `json:"interaction"`
	SuccessState   string                  `json:"success_state,omitempty"`
	WaitConditions []string                `json:"wait_conditions,omitempty"`
	CapturePoints  []string                `json:"capture_points,omitempty"`
	RiskNotes      []string                `json:"risk_notes,omitempty"`
	EvidenceRefs   []EvidenceRef           `json:"evidence_refs,omitempty"`
	Confidence     float64                 `json:"confidence,omitempty"`
}

type StageInputContent struct {
	Kind         string        `json:"kind,omitempty"`
	Label        string        `json:"label,omitempty"`
	Value        string        `json:"value,omitempty"`
	InputRef     string        `json:"input_ref,omitempty"`
	SecretRef    string        `json:"secret_ref,omitempty"`
	Editable     bool          `json:"editable,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

type BrowserAgentScriptOutline struct {
	ID                      string                       `json:"id"`
	ProjectID               string                       `json:"project_id"`
	WorkflowGraphID         string                       `json:"workflow_graph_id"`
	SchemaVersion           string                       `json:"schema_version"`
	Runtime                 string                       `json:"runtime"`
	BaseURL                 string                       `json:"base_url,omitempty"`
	ProductOrigin           string                       `json:"product_origin,omitempty"`
	Summary                 string                       `json:"summary,omitempty"`
	Stages                  []BrowserAgentOutlineStage   `json:"stages"`
	AllowedExplorationScope BrowserAgentExplorationScope `json:"allowed_exploration_scope"`
	ForbiddenActions        []string                     `json:"forbidden_actions,omitempty"`
	ServerEditableFields    []string                     `json:"server_editable_fields,omitempty"`
	ImmutableFields         []string                     `json:"immutable_fields,omitempty"`
	UncertaintyReport       []StageUncertainty           `json:"uncertainty_report,omitempty"`
	EvidenceRefs            []EvidenceRef                `json:"evidence_refs,omitempty"`
	Confidence              float64                      `json:"confidence,omitempty"`
	CreatedAt               time.Time                    `json:"created_at,omitempty"`
	UpdatedAt               time.Time                    `json:"updated_at,omitempty"`
}

type BrowserAgentExplorationScope struct {
	AllowedOrigins        []string `json:"allowed_origins,omitempty"`
	AllowedRoutes         []string `json:"allowed_routes,omitempty"`
	ForbiddenPathPrefixes []string `json:"forbidden_path_prefixes,omitempty"`
	ForbiddenKeywords     []string `json:"forbidden_keywords,omitempty"`
	MaxDepth              int      `json:"max_depth,omitempty"`
	AllowNonDestructive   bool     `json:"allow_non_destructive"`
}

type BrowserAgentOutlineStage struct {
	ID             string                        `json:"id"`
	StageID        string                        `json:"stage_id"`
	Order          int                           `json:"order"`
	NodeID         string                        `json:"node_id"`
	Objective      string                        `json:"objective,omitempty"`
	Route          string                        `json:"route,omitempty"`
	URL            string                        `json:"url,omitempty"`
	Components     []BrowserAgentComponentTarget `json:"components,omitempty"`
	Interactions   []BrowserAgentInteraction     `json:"interactions,omitempty"`
	WaitConditions []string                      `json:"wait_conditions,omitempty"`
	CapturePoints  []string                      `json:"capture_points,omitempty"`
	SuccessState   string                        `json:"success_state,omitempty"`
	DurationMS     int                           `json:"duration_ms,omitempty"`
	CanModify      []string                      `json:"can_modify,omitempty"`
	MustPreserve   []string                      `json:"must_preserve,omitempty"`
	EvidenceRefs   []EvidenceRef                 `json:"evidence_refs,omitempty"`
	Confidence     float64                       `json:"confidence,omitempty"`
}

type BrowserAgentComponentTarget struct {
	ComponentRef         string              `json:"component_ref,omitempty"`
	RouteRef             string              `json:"route_ref,omitempty"`
	Role                 string              `json:"role,omitempty"`
	Name                 string              `json:"name,omitempty"`
	Text                 string              `json:"text,omitempty"`
	Label                string              `json:"label,omitempty"`
	TestID               string              `json:"test_id,omitempty"`
	Selector             string              `json:"selector,omitempty"`
	SelectorAlternatives []SelectorCandidate `json:"selector_alternatives,omitempty"`
	EvidenceRefs         []EvidenceRef       `json:"evidence_refs,omitempty"`
	Confidence           float64             `json:"confidence,omitempty"`
}

type BrowserAgentInteraction struct {
	Kind           GraphActionType `json:"kind"`
	Target         ActionTarget    `json:"target,omitempty"`
	Value          string          `json:"value,omitempty"`
	InputRef       string          `json:"input_ref,omitempty"`
	SecretRef      string          `json:"secret_ref,omitempty"`
	Parameters     map[string]any  `json:"parameters,omitempty"`
	WaitUntil      string          `json:"wait_until,omitempty"`
	WaitConditions []string        `json:"wait_conditions,omitempty"`
	NonDestructive bool            `json:"non_destructive,omitempty"`
	SelectorPolicy string          `json:"selector_policy,omitempty"`
	EvidenceRefs   []EvidenceRef   `json:"evidence_refs,omitempty"`
}

type BrowserAgentPromptPolicy struct {
	ID                  string    `json:"id"`
	ProjectID           string    `json:"project_id"`
	WorkflowGraphID     string    `json:"workflow_graph_id"`
	SchemaVersion       string    `json:"schema_version"`
	Runtime             string    `json:"runtime"`
	SystemPrompt        string    `json:"system_prompt"`
	ImmutableFields     []string  `json:"immutable_fields"`
	EditableFields      []string  `json:"editable_fields"`
	ForbiddenChanges    []string  `json:"forbidden_changes"`
	RepairPolicy        []string  `json:"repair_policy,omitempty"`
	EvidencePolicy      []string  `json:"evidence_policy,omitempty"`
	SafetyBoundaries    []string  `json:"safety_boundaries,omitempty"`
	HumanReviewRequired bool      `json:"human_review_required"`
	CreatedAt           time.Time `json:"created_at,omitempty"`
	UpdatedAt           time.Time `json:"updated_at,omitempty"`
}

type ProjectUnderstandingDossier struct {
	ID                   string             `json:"id"`
	ProjectID            string             `json:"project_id"`
	WorkflowGraphID      string             `json:"workflow_graph_id,omitempty"`
	SchemaVersion        string             `json:"schema_version"`
	Summary              string             `json:"summary,omitempty"`
	RequirementObjective string             `json:"requirement_objective,omitempty"`
	ArchitectureSummary  string             `json:"architecture_summary,omitempty"`
	RouteEvidence        []DossierEvidence  `json:"route_evidence,omitempty"`
	ComponentEvidence    []DossierEvidence  `json:"component_evidence,omitempty"`
	StyleEvidence        []DossierEvidence  `json:"style_evidence,omitempty"`
	APIEvidence          []DossierEvidence  `json:"api_evidence,omitempty"`
	DataModelEvidence    []DossierEvidence  `json:"data_model_evidence,omitempty"`
	InteractionEvidence  []DossierEvidence  `json:"interaction_evidence,omitempty"`
	SecurityEvidence     []DossierEvidence  `json:"security_evidence,omitempty"`
	UncertaintyReport    []StageUncertainty `json:"uncertainty_report,omitempty"`
	SourceDigestSHA256   string             `json:"source_digest_sha256,omitempty"`
	InputFingerprints    map[string]string  `json:"input_fingerprints,omitempty"`
	EvidenceRefs         []EvidenceRef      `json:"evidence_refs,omitempty"`
	Confidence           float64            `json:"confidence,omitempty"`
	CreatedAt            time.Time          `json:"created_at,omitempty"`
	UpdatedAt            time.Time          `json:"updated_at,omitempty"`
}

type DossierEvidence struct {
	ID                   string        `json:"id"`
	Kind                 string        `json:"kind,omitempty"`
	Label                string        `json:"label,omitempty"`
	Summary              string        `json:"summary,omitempty"`
	Route                string        `json:"route,omitempty"`
	ComponentRef         string        `json:"component_ref,omitempty"`
	FilePathHashSHA256   string        `json:"file_path_hash_sha256,omitempty"`
	SourcePathHashSHA256 string        `json:"source_path_hash_sha256,omitempty"`
	EvidenceRefs         []EvidenceRef `json:"evidence_refs,omitempty"`
	Confidence           float64       `json:"confidence,omitempty"`
}

type StageUncertainty struct {
	ID              string        `json:"id"`
	StageID         string        `json:"stage_id,omitempty"`
	NodeID          string        `json:"node_id,omitempty"`
	Kind            string        `json:"kind,omitempty"`
	Summary         string        `json:"summary,omitempty"`
	Blocking        bool          `json:"blocking,omitempty"`
	SuggestedAction string        `json:"suggested_action,omitempty"`
	EvidenceRefs    []EvidenceRef `json:"evidence_refs,omitempty"`
}
