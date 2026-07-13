package model

import "time"

const ProjectIntelligencePackSchemaVersion = "demoops.project_intelligence_pack.v1"
const ScriptReadinessReportSchemaVersion = "demoops.script_readiness_report.v1"
const AgentGraphTraceSchemaVersion = "demoops.agent_graph_trace.v1"

// ProjectIntelligencePack is the summary-only project understanding contract
// produced by the local AgentGraph. It intentionally stores hashes, evidence
// refs, selectors, and compact schema signals instead of full source files.
type ProjectIntelligencePack struct {
	ID                    string                    `json:"id"`
	ProjectID             string                    `json:"project_id"`
	SchemaVersion         string                    `json:"schema_version"`
	Architecture          *ProjectArchitectureMap   `json:"architecture,omitempty"`
	FeatureCapabilities   []FeatureCapability       `json:"feature_capabilities,omitempty"`
	InteractionSurfaces   []InteractionSurface      `json:"interaction_surfaces,omitempty"`
	APIContracts          []APIContractSummary      `json:"api_contracts,omitempty"`
	DataModels            []ProjectDataModelSummary `json:"data_models,omitempty"`
	DemoScenarioPlans     []DemoScenarioPlan        `json:"demo_scenario_plans,omitempty"`
	ScriptReadinessReport *ScriptReadinessReport    `json:"script_readiness_report,omitempty"`
	SafetyReport          *SafetyReport             `json:"safety_report,omitempty"`
	InputFingerprints     map[string]string         `json:"input_fingerprints,omitempty"`
	SourceDigestSHA256    string                    `json:"source_digest_sha256,omitempty"`
	EvidenceRefs          []EvidenceRef             `json:"evidence_refs,omitempty"`
	Confidence            float64                   `json:"confidence,omitempty"`
	CreatedAt             time.Time                 `json:"created_at,omitempty"`
}

type ProjectArchitectureMap struct {
	ID                string                  `json:"id"`
	ProjectID         string                  `json:"project_id"`
	SchemaVersion     string                  `json:"schema_version,omitempty"`
	RepositoryCount   int                     `json:"repository_count,omitempty"`
	RepositoryRefIDs  []string                `json:"repository_ref_ids,omitempty"`
	WorkspaceRootHash string                  `json:"workspace_root_hash_sha256,omitempty"`
	PackageManagers   []string                `json:"package_managers,omitempty"`
	Frameworks        []string                `json:"frameworks,omitempty"`
	Languages         []string                `json:"languages,omitempty"`
	RuntimeTargets    []string                `json:"runtime_targets,omitempty"`
	EntryPointHashes  []string                `json:"entrypoint_hashes,omitempty"`
	Modules           []ProjectModule         `json:"modules,omitempty"`
	RouteTree         []ArchitectureRouteNode `json:"route_tree,omitempty"`
	Summary           string                  `json:"summary,omitempty"`
	EvidenceRefs      []EvidenceRef           `json:"evidence_refs,omitempty"`
	Confidence        float64                 `json:"confidence,omitempty"`
}

type ProjectModule struct {
	ID               string        `json:"id"`
	Name             string        `json:"name"`
	Kind             string        `json:"kind,omitempty"`
	Responsibility   string        `json:"responsibility,omitempty"`
	RepositoryRefID  string        `json:"repository_ref_id,omitempty"`
	FileCount        int           `json:"file_count,omitempty"`
	SourcePathHashes []string      `json:"source_path_hashes,omitempty"`
	EntryPointHashes []string      `json:"entrypoint_hashes,omitempty"`
	RouteRefs        []string      `json:"route_refs,omitempty"`
	ComponentRefs    []string      `json:"component_refs,omitempty"`
	APIRefs          []string      `json:"api_refs,omitempty"`
	DataModelRefs    []string      `json:"data_model_refs,omitempty"`
	EvidenceRefs     []EvidenceRef `json:"evidence_refs,omitempty"`
	Confidence       float64       `json:"confidence,omitempty"`
}

type ArchitectureRouteNode struct {
	ID            string        `json:"id"`
	Path          string        `json:"path"`
	Name          string        `json:"name,omitempty"`
	ParentPath    string        `json:"parent_path,omitempty"`
	ComponentRefs []string      `json:"component_refs,omitempty"`
	AuthRequired  bool          `json:"auth_required,omitempty"`
	EvidenceRefs  []EvidenceRef `json:"evidence_refs,omitempty"`
	Confidence    float64       `json:"confidence,omitempty"`
}

type FeatureCapability struct {
	ID                   string        `json:"id"`
	Name                 string        `json:"name"`
	Kind                 string        `json:"kind,omitempty"`
	UserValue            string        `json:"user_value,omitempty"`
	BusinessValue        string        `json:"business_value,omitempty"`
	Priority             string        `json:"priority,omitempty"`
	SupportingRouteRefs  []string      `json:"supporting_route_refs,omitempty"`
	SupportingPageRefs   []string      `json:"supporting_page_refs,omitempty"`
	SupportingComponents []string      `json:"supporting_components,omitempty"`
	SupportingAPIs       []string      `json:"supporting_apis,omitempty"`
	SupportingDataModels []string      `json:"supporting_data_models,omitempty"`
	KeyActions           []string      `json:"key_actions,omitempty"`
	Risks                []string      `json:"risks,omitempty"`
	EvidenceRefs         []EvidenceRef `json:"evidence_refs,omitempty"`
	DemoValueScore       float64       `json:"demo_value_score,omitempty"`
	Confidence           float64       `json:"confidence,omitempty"`
}

type InteractionSurface struct {
	ID              string              `json:"id"`
	PageID          string              `json:"page_id,omitempty"`
	URL             string              `json:"url,omitempty"`
	Title           string              `json:"title,omitempty"`
	PageRole        string              `json:"page_role,omitempty"`
	Actions         []UIActionRef       `json:"actions,omitempty"`
	StableSelectors []SelectorCandidate `json:"stable_selectors,omitempty"`
	States          []string            `json:"states,omitempty"`
	WaitHints       []string            `json:"wait_hints,omitempty"`
	FeatureRefs     []string            `json:"feature_refs,omitempty"`
	RiskFindings    []AgentFinding      `json:"risk_findings,omitempty"`
	EvidenceRefs    []EvidenceRef       `json:"evidence_refs,omitempty"`
	Confidence      float64             `json:"confidence,omitempty"`
}

type APIContractSummary struct {
	ID                 string        `json:"id"`
	Method             string        `json:"method,omitempty"`
	Path               string        `json:"path"`
	Purpose            string        `json:"purpose,omitempty"`
	AuthRequired       bool          `json:"auth_required,omitempty"`
	RequestFields      []string      `json:"request_fields,omitempty"`
	ResponseFields     []string      `json:"response_fields,omitempty"`
	SensitiveFields    []string      `json:"sensitive_fields,omitempty"`
	FilePathHashSHA256 string        `json:"file_path_hash_sha256,omitempty"`
	EvidenceRefs       []EvidenceRef `json:"evidence_refs,omitempty"`
	Confidence         float64       `json:"confidence,omitempty"`
}

type ProjectDataModelSummary struct {
	ID                   string        `json:"id"`
	Name                 string        `json:"name"`
	Kind                 string        `json:"kind,omitempty"`
	Fields               []DataField   `json:"fields,omitempty"`
	SensitiveFields      []string      `json:"sensitive_fields,omitempty"`
	SourcePathHashSHA256 string        `json:"source_path_hash_sha256,omitempty"`
	EvidenceRefs         []EvidenceRef `json:"evidence_refs,omitempty"`
	Confidence           float64       `json:"confidence,omitempty"`
}

type DemoScenarioPlan struct {
	ID                   string        `json:"id"`
	Name                 string        `json:"name"`
	UseCase              DemoUseCase   `json:"use_case,omitempty"`
	AudienceID           string        `json:"audience_id,omitempty"`
	Objective            string        `json:"objective,omitempty"`
	ValueProposition     string        `json:"value_proposition,omitempty"`
	FeatureRefs          []string      `json:"feature_refs,omitempty"`
	PageRefs             []string      `json:"page_refs,omitempty"`
	RouteRefs            []string      `json:"route_refs,omitempty"`
	EstimatedSteps       int           `json:"estimated_steps,omitempty"`
	EstimatedDurationSec int           `json:"estimated_duration_sec,omitempty"`
	NarrativeBeats       []string      `json:"narrative_beats,omitempty"`
	RiskNotes            []string      `json:"risk_notes,omitempty"`
	EvidenceRefs         []EvidenceRef `json:"evidence_refs,omitempty"`
	Feasibility          float64       `json:"feasibility,omitempty"`
	ValueScore           float64       `json:"value_score,omitempty"`
	Confidence           float64       `json:"confidence,omitempty"`
}

type ScriptReadinessReport struct {
	ID                         string         `json:"id"`
	ProjectID                  string         `json:"project_id"`
	SchemaVersion              string         `json:"schema_version"`
	CanProceed                 bool           `json:"can_proceed"`
	Summary                    string         `json:"summary,omitempty"`
	Blockers                   []AgentFinding `json:"blockers,omitempty"`
	Warnings                   []AgentFinding `json:"warnings,omitempty"`
	MissingInputs              []string       `json:"missing_inputs,omitempty"`
	RepairSuggestions          []string       `json:"repair_suggestions,omitempty"`
	RecommendedScenarioID      string         `json:"recommended_scenario_id,omitempty"`
	RecommendedScenarioName    string         `json:"recommended_scenario_name,omitempty"`
	SuggestedStageCount        int            `json:"suggested_stage_count,omitempty"`
	SuggestedTargetDurationSec int            `json:"suggested_target_duration_sec,omitempty"`
	SelectorCoverage           float64        `json:"selector_coverage,omitempty"`
	CredentialCoverage         bool           `json:"credential_coverage"`
	EvidenceRefs               []EvidenceRef  `json:"evidence_refs,omitempty"`
	Confidence                 float64        `json:"confidence,omitempty"`
	CreatedAt                  time.Time      `json:"created_at,omitempty"`
}

type AgentGraphTrace struct {
	ID             string                `json:"id"`
	ProjectID      string                `json:"project_id"`
	SchemaVersion  string                `json:"schema_version"`
	GraphName      string                `json:"graph_name,omitempty"`
	Steps          []AgentGraphTraceStep `json:"steps,omitempty"`
	Summary        string                `json:"summary,omitempty"`
	FallbackReason string                `json:"fallback_reason,omitempty"`
	StartedAt      time.Time             `json:"started_at,omitempty"`
	CompletedAt    time.Time             `json:"completed_at,omitempty"`
}

type AgentGraphTraceStep struct {
	ID             string        `json:"id"`
	NodeID         string        `json:"node_id"`
	Agent          string        `json:"agent,omitempty"`
	Tool           string        `json:"tool,omitempty"`
	Status         string        `json:"status"`
	InputSummary   string        `json:"input_summary,omitempty"`
	OutputSummary  string        `json:"output_summary,omitempty"`
	ElapsedMS      int64         `json:"elapsed_ms,omitempty"`
	Confidence     float64       `json:"confidence,omitempty"`
	FallbackReason string        `json:"fallback_reason,omitempty"`
	EvidenceRefs   []EvidenceRef `json:"evidence_refs,omitempty"`
	StartedAt      time.Time     `json:"started_at,omitempty"`
	CompletedAt    time.Time     `json:"completed_at,omitempty"`
}
