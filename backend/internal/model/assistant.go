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
	AssistantProposalSelectLocalProject          AssistantProposalKind = "select_local_project"
	AssistantProposalConnectGitHub               AssistantProposalKind = "connect_github"
	AssistantProposalAttachRequirementDocument   AssistantProposalKind = "attach_requirement_document"
	AssistantProposalAttachBrandAsset            AssistantProposalKind = "attach_brand_asset"
	AssistantProposalStoreDemoCredential         AssistantProposalKind = "store_demo_credential"
	AssistantProposalConfirmConfiguration        AssistantProposalKind = "confirm_configuration"
	AssistantProposalStartLocalAnalysis          AssistantProposalKind = "start_local_analysis"
	AssistantProposalOpenWorkstation             AssistantProposalKind = "open_workstation"
	AssistantProposalContinueWithWebpageEvidence AssistantProposalKind = "continue_with_webpage_evidence"
)

func IsAssistantProposalKind(value AssistantProposalKind) bool {
	switch value {
	case AssistantProposalConfigurationPatch, AssistantProposalSelectLocalProject,
		AssistantProposalConnectGitHub, AssistantProposalAttachRequirementDocument,
		AssistantProposalAttachBrandAsset, AssistantProposalStoreDemoCredential,
		AssistantProposalConfirmConfiguration, AssistantProposalStartLocalAnalysis,
		AssistantProposalOpenWorkstation, AssistantProposalContinueWithWebpageEvidence:
		return true
	default:
		return false
	}
}

type AssistantContext struct {
	Surface         AssistantSurface `json:"surface"`
	ScopeKey        string           `json:"scopeKey"`
	ProjectID       string           `json:"projectID,omitempty"`
	ProjectName     string           `json:"projectName,omitempty"`
	RepositoryID    string           `json:"repositoryID,omitempty"`
	RepositoryLabel string           `json:"repositoryLabel,omitempty"`
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
	CreatedAt         time.Time            `json:"createdAt"`
	TargetWorkstation AssistantWorkstation `json:"targetWorkstation,omitempty"`
	Evidence          []AssistantEvidence  `json:"evidence,omitempty"`
	Proposals         []AssistantProposal  `json:"proposals,omitempty"`
}

type AssistantEvent struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionID"`
	Type      string    `json:"type"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

type AssistantSession struct {
	ID                   string                    `json:"id"`
	Context              AssistantContext          `json:"context"`
	Status               string                    `json:"status"`
	ActiveWorkstation    AssistantWorkstation      `json:"activeWorkstation,omitempty"`
	WorkstationTitle     string                    `json:"workstationTitle,omitempty"`
	WorkstationStatus    string                    `json:"workstationStatus,omitempty"`
	Configuration        ProjectConfigurationDraft `json:"configuration"`
	Messages             []AssistantMessage        `json:"messages"`
	Events               []AssistantEvent          `json:"events,omitempty"`
	LastEventID          string                    `json:"lastEventID,omitempty"`
	ProcessedIdempotency map[string]string         `json:"processedIdempotency,omitempty"`
}

type AssistantTurnRequest struct {
	Message         string                   `json:"message"`
	IdempotencyKey  string                   `json:"idempotencyKey,omitempty"`
	SelectedSources []ConfigurationSourceRef `json:"selectedSources,omitempty"`
	CredentialRefs  []string                 `json:"credentialRefs,omitempty"`
}

type AssistantProposalDecisionRequest struct {
	BaseVersion    int64  `json:"baseVersion,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}
