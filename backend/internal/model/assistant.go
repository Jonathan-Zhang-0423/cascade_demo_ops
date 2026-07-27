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
	case AssistantWorkstationOverview, AssistantWorkstationEvidence, AssistantWorkstationPlan, AssistantWorkstationApproval, AssistantWorkstationExecution, AssistantWorkstationRepair, AssistantWorkstationAssets, AssistantWorkstationEditor:
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

type AssistantEvidence struct {
	ID         string  `json:"id"`
	Label      string  `json:"label"`
	Source     string  `json:"source"`
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence,omitempty"`
}

type AssistantProposal struct {
	ID                   string `json:"id"`
	Kind                 string `json:"kind"`
	Title                string `json:"title"`
	Description          string `json:"description"`
	TargetID             string `json:"targetID,omitempty"`
	TargetWorkstation    AssistantWorkstation `json:"targetWorkstation,omitempty"`
	ActionIntent         string `json:"actionIntent,omitempty"`
	RequiresConfirmation bool   `json:"requiresConfirmation"`
	Status               string `json:"status"`
}

type AssistantMessage struct {
	ID         string                 `json:"id"`
	Role       string                 `json:"role"`
	Kind       string                 `json:"kind"`
	Text       string                 `json:"text"`
	CreatedAt  time.Time              `json:"createdAt"`
	TargetWorkstation AssistantWorkstation `json:"targetWorkstation,omitempty"`
	Evidence   []AssistantEvidence    `json:"evidence,omitempty"`
	Proposals  []AssistantProposal    `json:"proposals,omitempty"`
}

type AssistantEvent struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionID"`
	Type      string    `json:"type"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

type AssistantSession struct {
	ID         string             `json:"id"`
	Context    AssistantContext   `json:"context"`
	Status     string             `json:"status"`
	ActiveWorkstation AssistantWorkstation `json:"activeWorkstation,omitempty"`
	WorkstationTitle string `json:"workstationTitle,omitempty"`
	WorkstationStatus string `json:"workstationStatus,omitempty"`
	Messages   []AssistantMessage `json:"messages"`
	Events     []AssistantEvent   `json:"events,omitempty"`
	LastEventID string            `json:"lastEventID,omitempty"`
}
