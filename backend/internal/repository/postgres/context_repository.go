package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"cascade-demoops/backend/internal/domain"
	"cascade-demoops/backend/internal/idgen"
	"cascade-demoops/backend/internal/repository"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ProjectContextRepository struct {
	pool *pgxpool.Pool
}

func NewProjectContextRepository(pool *pgxpool.Pool) *ProjectContextRepository {
	return &ProjectContextRepository{pool: pool}
}

func (r *ProjectContextRepository) CreateVersion(ctx context.Context, input repository.CreateProjectContextInput) (repository.ContextVersion, error) {
	payload, err := json.Marshal(input.Context)
	if err != nil {
		return repository.ContextVersion{}, fmt.Errorf("marshal context: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return repository.ContextVersion{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var version int
	if err := tx.QueryRow(ctx, `select coalesce(max(version), 0) + 1 from project_contexts where project_id = $1`, input.ProjectID).Scan(&version); err != nil {
		return repository.ContextVersion{}, fmt.Errorf("next context version: %w", err)
	}

	id := idgen.New("ctx")
	_, err = tx.Exec(ctx, `
		insert into project_contexts (id, project_id, version, context_json, created_by)
		values ($1, $2, $3, $4, $5)
	`, id, input.ProjectID, version, payload, input.CreatedBy)
	if err != nil {
		return repository.ContextVersion{}, fmt.Errorf("insert context: %w", err)
	}

	_, err = tx.Exec(ctx, `update projects set status = 'context_ready', updated_at = now() where id = $1`, input.ProjectID)
	if err != nil {
		return repository.ContextVersion{}, fmt.Errorf("update project status: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return repository.ContextVersion{}, fmt.Errorf("commit context: %w", err)
	}
	return repository.ContextVersion{ID: id, Version: version}, nil
}

func (r *ProjectContextRepository) GetLatest(ctx context.Context, projectID string) (domain.ProjectContext, error) {
	var payload []byte
	err := r.pool.QueryRow(ctx, `
		select context_json from project_contexts
		where project_id = $1 order by version desc limit 1
	`, projectID).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectContext{}, repository.ErrNotFound
	}
	if err != nil {
		return domain.ProjectContext{}, err
	}
	var value domain.ProjectContext
	if err := json.Unmarshal(payload, &value); err != nil {
		return domain.ProjectContext{}, fmt.Errorf("unmarshal context: %w", err)
	}
	return value, nil
}