package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"cascade-demoops/backend/internal/domain"
	"cascade-demoops/backend/internal/repository"
)

type ProjectService struct {
	projects repository.ProjectRepository
	contexts repository.ProjectContextRepository
	audit    repository.AuditLogWriter
}

func NewProjectService(projects repository.ProjectRepository, contexts repository.ProjectContextRepository, audit repository.AuditLogWriter) *ProjectService {
	return &ProjectService{projects: projects, contexts: contexts, audit: audit}
}

type CreateProjectInput struct {
	TenantID    string `json:"tenantId"`
	ProductName string `json:"productName"`
	RequestedBy string `json:"requestedBy"`
}

func (s *ProjectService) CreateProject(ctx context.Context, input CreateProjectInput) (domain.Project, error) {
	if input.TenantID == "" {
		return domain.Project{}, ValidationError{Field: "tenantId", Message: "tenantId is required"}
	}
	if input.ProductName == "" {
		return domain.Project{}, ValidationError{Field: "productName", Message: "productName is required"}
	}
	if input.RequestedBy == "" {
		return domain.Project{}, ValidationError{Field: "requestedBy", Message: "requestedBy is required"}
	}
	project, err := s.projects.Create(ctx, repository.CreateProjectInput{
		TenantID:    input.TenantID,
		ProductName: input.ProductName,
		CreatedBy:   input.RequestedBy,
	})
	if err != nil {
		return domain.Project{}, err
	}
	_ = s.audit.Write(ctx, repository.AuditEvent{
		ProjectID: project.ID,
		Actor:     input.RequestedBy,
		Action:    "project.create",
		Target:    project.ID,
		Result:    "success",
		Metadata:  map[string]any{"tenantId": input.TenantID, "productName": input.ProductName},
	})
	return project, nil
}

func (s *ProjectService) GetProject(ctx context.Context, projectID string) (domain.Project, error) {
	if projectID == "" {
		return domain.Project{}, ValidationError{Field: "projectId", Message: "projectId is required"}
	}
	return s.projects.Get(ctx, projectID)
}

type SaveProjectContextInput struct {
	Actor   string                `json:"actor"`
	Context domain.ProjectContext `json:"context"`
}

type SaveProjectContextResult struct {
	ContextID string `json:"contextId"`
	Version   int    `json:"version"`
}

func (s *ProjectService) SaveProjectContext(ctx context.Context, projectID string, input SaveProjectContextInput) (SaveProjectContextResult, error) {
	if projectID == "" {
		return SaveProjectContextResult{}, ValidationError{Field: "projectId", Message: "projectId is required"}
	}
	if input.Actor == "" {
		return SaveProjectContextResult{}, ValidationError{Field: "actor", Message: "actor is required"}
	}
	if input.Context.ProjectID != projectID {
		return SaveProjectContextResult{}, ValidationError{Field: "context.projectId", Message: "context.projectId must match route projectId"}
	}
	if err := validateProjectContext(input.Context); err != nil {
		return SaveProjectContextResult{}, err
	}
	version, err := s.contexts.CreateVersion(ctx, repository.CreateProjectContextInput{
		ProjectID: projectID,
		Context:   input.Context,
		CreatedBy: input.Actor,
	})
	if err != nil {
		return SaveProjectContextResult{}, err
	}
	_ = s.audit.Write(ctx, repository.AuditEvent{
		ProjectID: projectID,
		Actor:     input.Actor,
		Action:    "project_context.create_version",
		Target:    version.ID,
		Result:    "success",
		Metadata:  map[string]any{"version": version.Version},
	})
	return SaveProjectContextResult{ContextID: version.ID, Version: version.Version}, nil
}

func validateProjectContext(value domain.ProjectContext) error {
	if value.ProjectID == "" {
		return ValidationError{Field: "projectId", Message: "projectId is required"}
	}
	if value.TenantID == "" {
		return ValidationError{Field: "tenantId", Message: "tenantId is required"}
	}
	if value.Product.Name == "" {
		return ValidationError{Field: "product.name", Message: "product.name is required"}
	}
	if value.Product.Frontend.URL == "" {
		return ValidationError{Field: "product.frontend.url", Message: "frontend URL is required"}
	}
	if _, err := url.ParseRequestURI(value.Product.Frontend.URL); err != nil {
		return ValidationError{Field: "product.frontend.url", Message: fmt.Sprintf("invalid URL: %v", err)}
	}
	if value.Product.Frontend.Environment == "" {
		return ValidationError{Field: "product.frontend.environment", Message: "environment is required"}
	}
	if value.Product.Frontend.LoginMethod == "" {
		return ValidationError{Field: "product.frontend.loginMethod", Message: "loginMethod is required"}
	}
	if value.Audience.Primary == "" {
		return ValidationError{Field: "audience.primary", Message: "primary audience is required"}
	}
	if value.Audience.Intent == "" {
		return ValidationError{Field: "audience.intent", Message: "audience intent is required"}
	}
	if len(value.AssetsRequested) == 0 {
		return ValidationError{Field: "assetsRequested", Message: "at least one asset target is required"}
	}
	if value.GitHub != nil && value.GitHub.Permission != "read-only" {
		return ValidationError{Field: "github.permission", Message: "MVP only allows read-only GitHub access"}
	}
	if value.SSH != nil && value.SSH.Permission != "read-only" {
		return ValidationError{Field: "ssh.permission", Message: "MVP only allows read-only SSH access"}
	}
	return nil
}

type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

func IsValidationError(err error) bool {
	var validationErr ValidationError
	return errors.As(err, &validationErr)
}