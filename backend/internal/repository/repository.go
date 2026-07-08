package repository

import (
	"context"

	"cascade-demoops/backend/internal/domain"
)

type ProjectRepository interface {
	Create(ctx context.Context, input CreateProjectInput) (domain.Project, error)
	Get(ctx context.Context, projectID string) (domain.Project, error)
	UpdateStatus(ctx context.Context, projectID string, status domain.ProjectStatus) (domain.Project, error)
}

type CreateProjectInput struct {
	TenantID    string
	ProductName string
	CreatedBy   string
}

type ProjectContextRepository interface {
	CreateVersion(ctx context.Context, input CreateProjectContextInput) (ContextVersion, error)
	GetLatest(ctx context.Context, projectID string) (domain.ProjectContext, error)
}

type CreateProjectContextInput struct {
	ProjectID string
	Context   domain.ProjectContext
	CreatedBy string
}

type ContextVersion struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type AuditLogWriter interface {
	Write(ctx context.Context, event AuditEvent) error
}

type AuditEvent struct {
	ProjectID string
	Actor     string
	Action    string
	Target    string
	Result    string
	Metadata  map[string]any
}

type Repositories struct {
	Projects ProjectRepository
	Contexts ProjectContextRepository
	Audit    AuditLogWriter
}