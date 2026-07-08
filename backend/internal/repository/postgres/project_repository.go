package postgres

import (
	"context"
	"errors"
	"fmt"

	"cascade-demoops/backend/internal/domain"
	"cascade-demoops/backend/internal/idgen"
	"cascade-demoops/backend/internal/repository"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ProjectRepository struct {
	pool *pgxpool.Pool
}

func NewProjectRepository(pool *pgxpool.Pool) *ProjectRepository {
	return &ProjectRepository{pool: pool}
}

func (r *ProjectRepository) Create(ctx context.Context, input repository.CreateProjectInput) (domain.Project, error) {
	projectID := idgen.New("proj")
	_, err := r.pool.Exec(ctx, `insert into tenants (id, name) values ($1, $1) on conflict (id) do nothing`, input.TenantID)
	if err != nil {
		return domain.Project{}, fmt.Errorf("ensure tenant: %w", err)
	}

	row := r.pool.QueryRow(ctx, `
		insert into projects (id, tenant_id, product_name, status, created_by)
		values ($1, $2, $3, 'created', $4)
		returning id, tenant_id, product_name, status, created_by, created_at, updated_at
	`, projectID, input.TenantID, input.ProductName, input.CreatedBy)
	return scanProject(row)
}

func (r *ProjectRepository) Get(ctx context.Context, projectID string) (domain.Project, error) {
	row := r.pool.QueryRow(ctx, `
		select id, tenant_id, product_name, status, created_by, created_at, updated_at
		from projects where id = $1
	`, projectID)
	project, err := scanProject(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, repository.ErrNotFound
	}
	return project, err
}

func (r *ProjectRepository) UpdateStatus(ctx context.Context, projectID string, status domain.ProjectStatus) (domain.Project, error) {
	row := r.pool.QueryRow(ctx, `
		update projects set status = $2, updated_at = now() where id = $1
		returning id, tenant_id, product_name, status, created_by, created_at, updated_at
	`, projectID, status)
	project, err := scanProject(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, repository.ErrNotFound
	}
	return project, err
}

func scanProject(row pgx.Row) (domain.Project, error) {
	var project domain.Project
	if err := row.Scan(
		&project.ID,
		&project.TenantID,
		&project.ProductName,
		&project.Status,
		&project.CreatedBy,
		&project.CreatedAt,
		&project.UpdatedAt,
	); err != nil {
		return domain.Project{}, err
	}
	return project, nil
}