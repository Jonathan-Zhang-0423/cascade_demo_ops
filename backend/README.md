# Cascade DemoOps Go Backend

The backend has been rewritten in Go.

## Current implemented scope

```text
GET  /health
POST /v1/projects
GET  /v1/projects/{projectId}
POST /v1/projects/{projectId}/context
```

## Main structure

```text
cmd/api/                  API process entrypoint
internal/config/          env config
internal/database/        PostgreSQL pool
internal/domain/          core domain structs
internal/repository/      repository interfaces
internal/repository/postgres/ PostgreSQL implementations
internal/service/         application services
internal/httpapi/         HTTP router and handlers
infra/db/schema/          database schema init SQL
_legacy_ts/               previous TypeScript backend skeleton, kept for reference only
```

## Run locally

Install Go first, then:

```text
cd backend
go mod download
go run ./cmd/api
```

The API expects PostgreSQL tables from:

```text
backend/infra/db/schema/001_create_mvp_tables.sql
```

## Architecture rule

Models and agents never execute side effects directly. They produce structured output. Go services/workers perform deterministic side effects.