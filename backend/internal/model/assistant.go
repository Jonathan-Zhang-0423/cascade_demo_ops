package model

import "time"

type AssistantSurface string

const (
	AssistantSurfaceProjects     AssistantSurface = "projects"
	AssistantSurfaceRepositories AssistantSurface = "repositories"
)

type AssistantWorkstation string

const (
	AssistantWorkstationOverview  AssistantWorkstation = "overview"
	AssistantWorkstationEvidence  AssistantWorkstation = "evidence"
	AssistantWorkstationPlan      AssistantWorkstation = "plan"
	AssistantWorkstationApproval  AssistantWorkstation = "approval"
	AssistantWorkstationExecution AssistantWorkstation = "execution"
	AssistantWorkstationRepair    AssistantWorkstation = "repair"
	AssistantWorkstationAssets    AssistantWorkstation = "assets"
	AssistantWorkstationEditor    AssistantWorkstation = "editor"
)

func IsAssistantWorkstation(value AssistantWorkstation) bool {
	switch value {
	case AssistantWorkstationOverview, AssistantWorkstationEvidence, AssistantWorkstationPlan,
		AssistantWorkstationApproval, AssistantWorkstationExecution, AssistantWorkstationRepair,
		AssistantWorkstationAssets, AssistantWorkstationEditor:
		return true
	default:
		return false
	}
}

type AssistantProposalKind string

const (
	AssistantProposalConfigurationPatch          AssistantProposalKind = "configuration_patch"
	AssistantProposalSelectProjectSource         AssistantProposalKind = "select_project_source"
	AssistantProposalSelectLocalProject          AssistantProposalKind = "select_local_project"
	AssistantProposalConnectGitHub               AssistantProposalKind = "connect_github"
	AssistantProposalAttachRequirementDocument   AssistantProposalKind = "attach_requirement_document"
	AssistantProposalAttachBrandAsset            AssistantProposalKind = "attach_brand_asset"
	AssistantProposalStoreDemoCredential         AssistantProposalKind = "store_demo_credential"
	AssistantProposalConfirmConfiguration        AssistantProposalKind = "confirm_configuration"
	AssistantProposalStartLocalAnalysis          AssistantProposalKind = "start_local_analysis"
	AssistantProposalOpenWorkstation             AssistantProposalKind = "open_workstation"
	AssistantProposalContinueWithWebpageEvidence AssistantProposalKind = "continue_with_webpage_evidence"
	AssistantProposalRetryPageScan               AssistantProposalKind = "retry_page_scan"
	AssistantProposalRegenerateExecutionPackage  AssistantProposalKind = "regenerate_execution_package"
)

func IsAssistantProposalKind(value AssistantProposalKind) bool {
	switch value {
	case AssistantProposalConfigurationPatch, AssistantProposalSelectProjectSource, AssistantProposalSelectLocalProject,
		AssistantProposalConnectGitHub, AssistantProposalAttachRequirementDocument,
		AssistantProposalAttachBrandAsset, AssistantProposalStoreDemoCredential,
		AssistantProposalConfirmConfiguration, AssistantProposalStartLocalAnalysis,
		AssistantProposalOpenWorkstation, AssistantProposalContinueWithWebpageEvidence:
		return true
	case AssistantProposalRetryPageScan, AssistantProposalRegenerateExecutionPackage:
		return true
	default:
		return false
	}
}

type AssistantContext struct {
	Surface                  AssistantSurface `json:"surface"`
	ScopeKey                 string           `json:"scopeKey"`
	ProjectID                string           `json:"projectID,omitempty"`
	ProjectName              string           `json:"projectName,omitempty"`
	CreateProjectOnFirstTurn bool             `json:"createProjectOnFirstTurn,omitempty"`
	RepositoryID             string           `json:"repositoryID,omitempty"`
	RepositoryLabel          string           `json:"repositoryLabel,omitempty"`
	Locale                   string           `json:"locale,omitempty"`
}

type ConfigurationSourceRef struct {
	Ref   string `json:"ref"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
}

type ProjectConfigurationDraft struct {
	ProjectName       string                   `json:"projectName,omitempty"`
	ProductURL        string                   `json:"productURL,omitempty"`
	Sources           []ConfigurationSourceRef `json:"sources,omitempty"`
	Objective         string                   `json:"objective,omitempty"`
	TargetAudience    string                   `json:"targetAudience,omitempty"`
	TargetDurationSec int                      `json:"targetDurationSec,omitempty"`
	MustShow          []string                 `json:"mustShow,omitempty"`
	MustNotShow       []string                 `json:"mustNotShow,omitempty"`
	ForbiddenPages    []string                 `json:"forbiddenPages,omitempty"`
	ForbiddenData     []string                 `json:"forbiddenData,omitempty"`
	BrandTone         string                   `json:"brandTone,omitempty"`
	CredentialRefs    []string                 `json:"credentialRefs,omitempty"`
	AllowedDomains    []string                 `json:"allowedDomains,omitempty"`
	Version           int64                    `json:"version"`
	Hash              string                   `json:"hash"`
	Readiness         string                   `json:"readiness"`
	MissingFields     []string                 `json:"missingFields,omitempty"`
	Confirmed         bool                     `json:"confirmed"`
	ConfirmedAt       *time.Time               `json:"confirmedAt,omitempty"`
	AnalysisProjectID string                   `json:"analysisProjectID,omitempty"`
}

const AgentIntentPlanSchemaVersion = "demoops.agent_intent_plan.v1"

type AgentActionRisk string

const (
	AgentActionRiskReadOnly      AgentActionRisk = "read_only"
	AgentActionRiskSessionWrite  AgentActionRisk = "session_write"
	AgentActionRiskExternalWrite AgentActionRisk = "external_write"
	AgentActionRiskDestructive   AgentActionRisk = "destructive"
)

type AgentIntentStep struct {
	ID                  string          `json:"id"`
	Order               int             `json:"order"`
	Action              string          `json:"action"`
	Target              string          `json:"target,omitempty"`
	Risk                AgentActionRisk `json:"risk"`
	ExpectedOutcome     string          `json:"expectedOutcome,omitempty"`
	EvidenceRequirement string          `json:"evidenceRequirement,omitempty"`
}

type AgentIntentPlan struct {
	SchemaVersion        string            `json:"schemaVersion"`
	Objective            string            `json:"objective,omitempty"`
	Entities             map[string]string `json:"entities,omitempty"`
	Steps                []AgentIntentStep `json:"steps,omitempty"`
	Constraints          []string          `json:"constraints,omitempty"`
	RequiredCapabilities []string          `json:"requiredCapabilities,omitempty"`
	Readiness            string            `json:"readiness"`
	MissingRequirements  []string          `json:"missingRequirements,omitempty"`
	Version              int64             `json:"version"`
	Digest               string            `json:"digest"`
}

type AgentActionSpecification struct {
	ID                  string          `json:"id"`
	Title               string          `json:"title"`
	InputSchema         map[string]any  `json:"inputSchema,omitempty"`
	OutputKinds         []string        `json:"outputKinds,omitempty"`
	AllowedPhases       []string        `json:"allowedPhases"`
	Risk                AgentActionRisk `json:"risk"`
	AutoPolicy          string          `json:"autoPolicy"`
	Idempotent          bool            `json:"idempotent"`
	RequiresUserGesture bool            `json:"requiresUserGesture"`
}

type AgentAction struct {
	ID                  string               `json:"id"`
	SpecID              string               `json:"specID"`
	Title               string               `json:"title"`
	Description         string               `json:"description,omitempty"`
	Risk                AgentActionRisk      `json:"risk"`
	Status              string               `json:"status"`
	AutoExecutable      bool                 `json:"autoExecutable"`
	RequiresUserGesture bool                 `json:"requiresUserGesture"`
	DependsOn           []string             `json:"dependsOn,omitempty"`
	DependencyDigest    string               `json:"dependencyDigest"`
	IdempotencyKey      string               `json:"idempotencyKey"`
	ProposalID          string               `json:"proposalID,omitempty"`
	BatchID             string               `json:"batchID,omitempty"`
	TargetWorkstation   AssistantWorkstation `json:"targetWorkstation,omitempty"`
	ExecutionResult     map[string]any       `json:"executionResult,omitempty"`
}

type AgentActionBatch struct {
	ID                    string   `json:"id"`
	ActionIDs             []string `json:"actionIDs"`
	BaseIntentDigest      string   `json:"baseIntentDigest"`
	ApprovalSubjectDigest string   `json:"approvalSubjectDigest,omitempty"`
	Status                string   `json:"status"`
	RequiresConfirmation  bool     `json:"requiresConfirmation"`
	IdempotencyKey        string   `json:"idempotencyKey"`
}

// ProjectConfigurationPatch uses pointers so omission and clearing remain distinct.
type ProjectConfigurationPatch struct {
	ProjectName       *string                   `json:"projectName,omitempty"`
	ProductURL        *string                   `json:"productURL,omitempty"`
	Sources           *[]ConfigurationSourceRef `json:"sources,omitempty"`
	Objective         *string                   `json:"objective,omitempty"`
	TargetAudience    *string                   `json:"targetAudience,omitempty"`
	TargetDurationSec *int                      `json:"targetDurationSec,omitempty"`
	MustShow          *[]string                 `json:"mustShow,omitempty"`
	MustNotShow       *[]string                 `json:"mustNotShow,omitempty"`
	ForbiddenPages    *[]string                 `json:"forbiddenPages,omitempty"`
	ForbiddenData     *[]string                 `json:"forbiddenData,omitempty"`
	BrandTone         *string                   `json:"brandTone,omitempty"`
	CredentialRefs    *[]string                 `json:"credentialRefs,omitempty"`
	AllowedDomains    *[]string                 `json:"allowedDomains,omitempty"`
}

type AssistantEvidence struct {
	ID         string  `json:"id"`
	Label      string  `json:"label"`
	Source     string  `json:"source"`
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence,omitempty"`
}

type AssistantQuestionOption struct {
	Label       string `json:"label"`
	Value       string `json:"value"`
	Description string `json:"description"`
	Recommended bool   `json:"recommended,omitempty"`
}

type AssistantQuestion struct {
	Field       string                    `json:"field"`
	Prompt      string                    `json:"prompt"`
	Suggestions []string                  `json:"suggestions,omitempty"`
	Options     []AssistantQuestionOption `json:"options,omitempty"`
}

type AssistantProposal struct {
	ID                   string                     `json:"id"`
	Kind                 AssistantProposalKind      `json:"kind"`
	Title                string                     `json:"title"`
	Description          string                     `json:"description"`
	TargetWorkstation    AssistantWorkstation       `json:"targetWorkstation,omitempty"`
	Patch                *ProjectConfigurationPatch `json:"patch,omitempty"`
	BaseVersion          int64                      `json:"baseVersion"`
	IdempotencyKey       string                     `json:"idempotencyKey"`
	RequiresConfirmation bool                       `json:"requiresConfirmation"`
	Status               string                     `json:"status"`
	ExecutionResult      map[string]any             `json:"executionResult,omitempty"`
}

type AssistantMessage struct {
	ID                string               `json:"id"`
	Role              string               `json:"role"`
	Kind              string               `json:"kind"`
	Text              string               `json:"text"`
	GenerationSource  string               `json:"generationSource,omitempty"`
	ModelProvider     string               `json:"modelProvider,omitempty"`
	ModelName         string               `json:"modelName,omitempty"`
	FallbackReason    string               `json:"fallbackReason,omitempty"`
	CreatedAt         time.Time            `json:"createdAt"`
	TargetWorkstation AssistantWorkstation `json:"targetWorkstation,omitempty"`
	Evidence          []AssistantEvidence  `json:"evidence,omitempty"`
	Proposals         []AssistantProposal  `json:"proposals,omitempty"`
	Question          *AssistantQuestion   `json:"question,omitempty"`
}

type AssistantEvent struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionID"`
	Type      string    `json:"type"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

type AssistantActiveTurn struct {
	ID             string     `json:"id"`
	UserMessageID  string     `json:"userMessageID"`
	Status         string     `json:"status"`
	IdempotencyKey string     `json:"idempotencyKey,omitempty"`
	StartedAt      time.Time  `json:"startedAt"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
	FailureReason  string     `json:"failureReason,omitempty"`
}

type AssistantNextAction struct {
	Kind               string               `json:"kind"`
	Title              string               `json:"title"`
	Description        string               `json:"description"`
	PrimaryLabel       string               `json:"primaryLabel,omitempty"`
	ProposalID         string               `json:"proposalID,omitempty"`
	TargetWorkstation  AssistantWorkstation `json:"targetWorkstation,omitempty"`
	RequiresUserAction bool                 `json:"requiresUserAction"`
	Blocked            bool                 `json:"blocked"`
	MissingFields      []string             `json:"missingFields,omitempty"`
}

type AssistantSession struct {
	ID                   string                     `json:"id"`
	Context              AssistantContext           `json:"context"`
	Status               string                     `json:"status"`
	ActiveWorkstation    AssistantWorkstation       `json:"activeWorkstation,omitempty"`
	WorkstationTitle     string                     `json:"workstationTitle,omitempty"`
	WorkstationStatus    string                     `json:"workstationStatus,omitempty"`
	NextAction           AssistantNextAction        `json:"nextAction"`
	Configuration        ProjectConfigurationDraft  `json:"configuration"`
	Messages             []AssistantMessage         `json:"messages"`
	PendingQuestion      *AssistantQuestion         `json:"pendingQuestion,omitempty"`
	Events               []AssistantEvent           `json:"events,omitempty"`
	LastEventID          string                     `json:"lastEventID,omitempty"`
	ProcessedIdempotency map[string]string          `json:"processedIdempotency,omitempty"`
	ActionMode           string                     `json:"actionMode,omitempty"`
	IntentPlan           *AgentIntentPlan           `json:"intentPlan,omitempty"`
	ActionCatalog        []AgentActionSpecification `json:"actionCatalog,omitempty"`
	Actions              []AgentAction              `json:"actions,omitempty"`
	ActionBatches        []AgentActionBatch         `json:"actionBatches,omitempty"`
	ActiveTurn           *AssistantActiveTurn       `json:"activeTurn,omitempty"`
}

type AgentActionCompleteRequest struct {
	DependencyDigest string                   `json:"dependencyDigest,omitempty"`
	IdempotencyKey   string                   `json:"idempotencyKey"`
	SelectedSources  []ConfigurationSourceRef `json:"selectedSources,omitempty"`
	CredentialRefs   []string                 `json:"credentialRefs,omitempty"`
}

type AgentActionBatchConfirmRequest struct {
	BaseIntentDigest      string `json:"baseIntentDigest"`
	ApprovalSubjectDigest string `json:"approvalSubjectDigest,omitempty"`
	IdempotencyKey        string `json:"idempotencyKey"`
}

type AssistantTurnRequest struct {
	Message         string                   `json:"message"`
	IdempotencyKey  string                   `json:"idempotencyKey,omitempty"`
	SelectedSources []ConfigurationSourceRef `json:"selectedSources,omitempty"`
	CredentialRefs  []string                 `json:"credentialRefs,omitempty"`
}

type AssistantConfigurationProposalRequest struct {
	Patch          ProjectConfigurationPatch `json:"patch"`
	BaseVersion    int64                     `json:"baseVersion"`
	IdempotencyKey string                    `json:"idempotencyKey"`
}

type AssistantProposalDecisionRequest struct {
	BaseVersion    int64  `json:"baseVersion,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

type AssistantClientActionResultRequest struct {
	BaseVersion     int64                    `json:"baseVersion,omitempty"`
	IdempotencyKey  string                   `json:"idempotencyKey,omitempty"`
	SelectedSources []ConfigurationSourceRef `json:"selectedSources,omitempty"`
	CredentialRefs  []string                 `json:"credentialRefs,omitempty"`
}
