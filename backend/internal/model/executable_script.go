package model

import "time"

const ExecutableRecordingScriptBundleSchemaVersion = "demoops.executable_recording_script_bundle.v1"

type ExecutableScriptBundleStatus string

const (
	ExecutableScriptBundleStatusDraft       ExecutableScriptBundleStatus = "draft"
	ExecutableScriptBundleStatusReviewReady ExecutableScriptBundleStatus = "review_ready"
	ExecutableScriptBundleStatusValidated   ExecutableScriptBundleStatus = "validated"
	ExecutableScriptBundleStatusRejected    ExecutableScriptBundleStatus = "rejected"
)

type ExecutableRecordingScriptBundle struct {
	ID               string                          `json:"id"`
	ProjectID        string                          `json:"project_id"`
	WorkflowGraphID  string                          `json:"workflow_graph_id"`
	SchemaVersion    string                          `json:"schema_version"`
	Status           ExecutableScriptBundleStatus    `json:"status,omitempty"`
	ScriptManifest   ExecutableScriptManifest        `json:"script_manifest"`
	PlanJSON         *ExecutionScriptDocument        `json:"plan_json"`
	PlaywrightScript ExecutableScriptSource          `json:"playwright_script"`
	ApprovalMarkdown ApprovalMarkdownDocument        `json:"approval_markdown"`
	SecurityPolicy   ExecutableScriptSecurityPolicy  `json:"security_policy"`
	Reproducibility  ExecutableScriptReproducibility `json:"reproducibility"`
	Validation       *ExecutableScriptValidation     `json:"validation,omitempty"`
	RepairLineage    *ScriptRepairLineage            `json:"repair_lineage,omitempty"`
	CreatedAt        time.Time                       `json:"created_at,omitempty"`
	UpdatedAt        time.Time                       `json:"updated_at,omitempty"`
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
	PlanHashSHA256       string            `json:"plan_hash_sha256"`
	ScriptHashSHA256     string            `json:"script_hash_sha256"`
	MarkdownHashSHA256   string            `json:"markdown_hash_sha256"`
	BundleHashSHA256     string            `json:"bundle_hash_sha256,omitempty"`
	GraphHashSHA256      string            `json:"graph_hash_sha256,omitempty"`
	SourceSnapshotDigest string            `json:"source_snapshot_digest,omitempty"`
	GeneratorVersion     string            `json:"generator_version,omitempty"`
	DeterministicSeed    string            `json:"deterministic_seed,omitempty"`
	InputFingerprints    map[string]string `json:"input_fingerprints,omitempty"`
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
