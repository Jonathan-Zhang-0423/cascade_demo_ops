package repository

import (
	"context"
	"encoding/json"
	"time"

	"cascade-demoops/backend/internal/model"
)

type Organization struct {
	ID        string
	Name      string
	Slug      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type User struct {
	ID        string
	Email     string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type OrganizationMember struct {
	OrgID     string
	UserID    string
	Role      string
	Status    string
	CreatedAt time.Time
}

type Project struct {
	ID              string
	OrgID           string
	CreatedByUserID string
	Name            string
	Mode            model.AppMode
	Status          string
	ProductURL      string
	TargetAudience  string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ArchivedAt      *time.Time
}

type ProjectContextRecord struct {
	ID                 string
	ProjectID          string
	Version            int
	SchemaVersion      string
	Context            *model.ProjectContext
	InputJSON          json.RawMessage
	AccessPolicyJSON   json.RawMessage
	SecurityPolicyJSON json.RawMessage
	IsCurrent          bool
	CreatedByUserID    string
	CreatedAt          time.Time
}

type ProjectInputRecord struct {
	ID                string
	ProjectID         string
	Kind              string
	Title             string
	SourceURI         string
	InputJSON         json.RawMessage
	ArtifactID        string
	FingerprintSHA256 string
	Status            string
	CreatedAt         time.Time
}

type SecretRefRecord struct {
	ID        string
	OrgID     string
	ProjectID string
	Kind      string
	Provider  string
	SecretRef string
	Scope     string
	ExpiresAt *time.Time
	Status    string
	CreatedAt time.Time
}

type ArtifactRecord struct {
	ID           string
	OrgID        string
	ProjectID    string
	Kind         string
	URI          string
	MimeType     string
	Label        string
	SHA256       string
	SizeBytes    int64
	Sensitive    bool
	MetadataJSON json.RawMessage
	CreatedAt    time.Time
	ExpiresAt    *time.Time
}

type EvidenceRecord struct {
	ID        string
	ProjectID string
	Kind      model.EvidenceKind
	Evidence  *model.EvidenceRecord
	CreatedAt time.Time
}

type EvidenceArtifactLink struct {
	EvidenceID string
	ArtifactID string
	Role       string
	CreatedAt  time.Time
}

type KnowledgeChunkRecord struct {
	ID            string
	ProjectID     string
	EvidenceID    string
	SourceKind    string
	Title         string
	ContentText   string
	ContentSHA256 string
	ChunkIndex    int
	MetadataJSON  json.RawMessage
	CreatedAt     time.Time
}

type ProductMapRecord struct {
	ID                    string
	ProjectID             string
	Version               int
	Status                string
	Summary               string
	Map                   *model.ProductMap
	GeneratedByAgentRunID string
	CreatedAt             time.Time
}

type WorkflowGraphRecord struct {
	ID                  string
	ProjectID           string
	GraphKey            string
	Version             int
	Status              model.GraphStatus
	Name                string
	Summary             string
	EntryPoint          string
	SchemaVersion       string
	Graph               *model.DemoWorkflowGraph
	CreatedFromGraphID  string
	CreatedByAgentRunID string
	ApprovedByUserID    string
	ApprovedAt          *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type WorkflowGraphPatchRecord struct {
	ID                  string
	ProjectID           string
	BaseGraphID         string
	Patch               *model.GraphPatch
	Status              string
	Rationale           string
	CreatedByAgentRunID string
	ReviewedByUserID    string
	AppliedGraphID      string
	CreatedAt           time.Time
}

type OrchestratorRunRecord struct {
	ID          string
	ProjectID   string
	Status      string
	CurrentNode string
	StateJSON   json.RawMessage
	StartedAt   *time.Time
	CompletedAt *time.Time
	ErrorJSON   json.RawMessage
	CreatedAt   time.Time
}

type AgentRunRecord struct {
	ID          string
	ProjectID   string
	Stage       model.AgentStage
	TaskKind    model.AgentTaskKind
	Status      model.AgentRunStatus
	Envelope    *model.AgentRunEnvelope
	StartedAt   *time.Time
	CompletedAt *time.Time
	CreatedAt   time.Time
}

type JobRecord struct {
	ID          string
	ProjectID   string
	JobType     string
	Status      string
	PayloadJSON json.RawMessage
	Attempts    int
	MaxAttempts int
	RunAfter    time.Time
	LockedBy    string
	LockedAt    *time.Time
	ErrorJSON   json.RawMessage
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ExecutionRunRecord struct {
	ID              string
	ProjectID       string
	WorkflowGraphID string
	RunType         string
	Status          string
	PassRate        *float64
	Trace           *model.ExecutionTrace
	StartedAt       *time.Time
	CompletedAt     *time.Time
	ErrorJSON       json.RawMessage
	CreatedAt       time.Time
}

type StepResultRecord struct {
	ID             string
	ExecutionRunID string
	NodeID         string
	Status         string
	DurationMS     *int
	ResultJSON     json.RawMessage
	StartedAt      *time.Time
	CompletedAt    *time.Time
	CreatedAt      time.Time
}

type AssetRecord struct {
	ID               string
	ProjectID        string
	WorkflowGraphID  string
	ExecutionRunID   string
	Kind             model.AssetKind
	Status           string
	Title            string
	URI              string
	MimeType         string
	ProvenanceJSON   json.RawMessage
	ReviewStatus     string
	ApprovedByUserID string
	ApprovedAt       *time.Time
	CreatedAt        time.Time
}

type AssetReviewRecord struct {
	ID             string
	AssetID        string
	ReviewerUserID string
	Decision       string
	Notes          string
	CreatedAt      time.Time
}

type AuditLogRecord struct {
	ID           string
	OrgID        string
	ProjectID    string
	ActorUserID  string
	Action       string
	ResourceType string
	ResourceID   string
	MetadataJSON json.RawMessage
	CreatedAt    time.Time
}

type TenancyRepository interface {
	CreateOrganization(ctx context.Context, org Organization) error
	GetOrganization(ctx context.Context, orgID string) (Organization, error)
	CreateUser(ctx context.Context, user User) error
	GetUser(ctx context.Context, userID string) (User, error)
	AddOrganizationMember(ctx context.Context, member OrganizationMember) error
	ListOrganizationMembers(ctx context.Context, orgID string) ([]OrganizationMember, error)
}

type ProjectRepository interface {
	CreateProject(ctx context.Context, project Project) error
	GetProject(ctx context.Context, orgID string, projectID string) (Project, error)
	ListProjects(ctx context.Context, orgID string) ([]Project, error)
	ArchiveProject(ctx context.Context, orgID string, projectID string, archivedAt time.Time) error

	SaveProjectContext(ctx context.Context, record ProjectContextRecord) error
	GetCurrentProjectContext(ctx context.Context, projectID string) (ProjectContextRecord, error)
	GetProjectContextVersion(ctx context.Context, projectID string, version int) (ProjectContextRecord, error)

	CreateProjectInput(ctx context.Context, input ProjectInputRecord) error
	ListProjectInputs(ctx context.Context, projectID string) ([]ProjectInputRecord, error)
	CreateSecretRef(ctx context.Context, secret SecretRefRecord) error
	ListSecretRefs(ctx context.Context, projectID string) ([]SecretRefRecord, error)
}

type EvidenceRepository interface {
	CreateArtifact(ctx context.Context, artifact ArtifactRecord) error
	GetArtifact(ctx context.Context, projectID string, artifactID string) (ArtifactRecord, error)
	CreateEvidenceRecord(ctx context.Context, evidence EvidenceRecord) error
	ListEvidenceRecords(ctx context.Context, projectID string) ([]EvidenceRecord, error)
	LinkEvidenceArtifact(ctx context.Context, link EvidenceArtifactLink) error
	CreateKnowledgeChunk(ctx context.Context, chunk KnowledgeChunkRecord) error
	ListKnowledgeChunks(ctx context.Context, projectID string, evidenceID string) ([]KnowledgeChunkRecord, error)
}

type ProductIntelligenceRepository interface {
	SaveProductMap(ctx context.Context, productMap ProductMapRecord) error
	GetProductMapVersion(ctx context.Context, projectID string, version int) (ProductMapRecord, error)
	ListProductMaps(ctx context.Context, projectID string) ([]ProductMapRecord, error)
}

type WorkflowGraphRepository interface {
	SaveWorkflowGraph(ctx context.Context, graph WorkflowGraphRecord) error
	GetWorkflowGraph(ctx context.Context, projectID string, graphID string) (WorkflowGraphRecord, error)
	GetWorkflowGraphVersion(ctx context.Context, projectID string, graphKey string, version int) (WorkflowGraphRecord, error)
	ListWorkflowGraphs(ctx context.Context, projectID string) ([]WorkflowGraphRecord, error)
	ApproveWorkflowGraph(ctx context.Context, projectID string, graphID string, userID string, approvedAt time.Time) error
	CreateWorkflowGraphPatch(ctx context.Context, patch WorkflowGraphPatchRecord) error
	ListWorkflowGraphPatches(ctx context.Context, projectID string, baseGraphID string) ([]WorkflowGraphPatchRecord, error)
}

type OrchestrationRepository interface {
	CreateOrchestratorRun(ctx context.Context, run OrchestratorRunRecord) error
	UpdateOrchestratorRun(ctx context.Context, run OrchestratorRunRecord) error
	GetOrchestratorRun(ctx context.Context, projectID string, runID string) (OrchestratorRunRecord, error)
	CreateAgentRun(ctx context.Context, run AgentRunRecord) error
	UpdateAgentRun(ctx context.Context, run AgentRunRecord) error
	CreateJob(ctx context.Context, job JobRecord) error
	ClaimNextJob(ctx context.Context, workerID string, now time.Time) (JobRecord, error)
	CompleteJob(ctx context.Context, jobID string, completedAt time.Time) error
	FailJob(ctx context.Context, jobID string, errJSON json.RawMessage, failedAt time.Time) error
}

type ExecutionRepository interface {
	CreateExecutionRun(ctx context.Context, run ExecutionRunRecord) error
	UpdateExecutionRun(ctx context.Context, run ExecutionRunRecord) error
	GetExecutionRun(ctx context.Context, projectID string, runID string) (ExecutionRunRecord, error)
	CreateStepResult(ctx context.Context, result StepResultRecord) error
	ListStepResults(ctx context.Context, executionRunID string) ([]StepResultRecord, error)
}

type AssetRepository interface {
	CreateAsset(ctx context.Context, asset AssetRecord) error
	GetAsset(ctx context.Context, projectID string, assetID string) (AssetRecord, error)
	ListAssets(ctx context.Context, projectID string) ([]AssetRecord, error)
	CreateAssetReview(ctx context.Context, review AssetReviewRecord) error
	ApproveAsset(ctx context.Context, projectID string, assetID string, userID string, approvedAt time.Time) error
}

type AuditRepository interface {
	WriteAuditLog(ctx context.Context, log AuditLogRecord) error
	ListAuditLogs(ctx context.Context, orgID string, projectID string) ([]AuditLogRecord, error)
}
