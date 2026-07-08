package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"cascade-demoops/backend/internal/idgen"
	"cascade-demoops/backend/internal/repository"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AuditLogWriter struct {
	pool *pgxpool.Pool
}

func NewAuditLogWriter(pool *pgxpool.Pool) *AuditLogWriter {
	return &AuditLogWriter{pool: pool}
}

func (w *AuditLogWriter) Write(ctx context.Context, event repository.AuditEvent) error {
	payload, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("marshal audit metadata: %w", err)
	}
	_, err = w.pool.Exec(ctx, `
		insert into audit_logs (id, project_id, actor, action, target, result, metadata_json)
		values ($1, $2, $3, $4, $5, $6, $7)
	`, idgen.New("audit"), nullableText(event.ProjectID), event.Actor, event.Action, event.Target, event.Result, payload)
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}