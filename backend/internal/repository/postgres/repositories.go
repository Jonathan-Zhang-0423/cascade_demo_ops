package postgres

import (
	"cascade-demoops/backend/internal/repository"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewRepositories(pool *pgxpool.Pool) repository.Repositories {
	return repository.Repositories{
		Projects: NewProjectRepository(pool),
		Contexts: NewProjectContextRepository(pool),
		Audit:    NewAuditLogWriter(pool),
	}
}