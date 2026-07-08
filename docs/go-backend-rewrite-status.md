# Go Backend Rewrite Status

## Decision

Backend mainline is now Go. Previous TypeScript backend skeleton has been moved to:

```text
backend/_legacy_ts
```

It is retained for reference only. Do not continue backend implementation there.

## Current Go Backend Scope

Implemented source structure:

```text
backend/go.mod
backend/cmd/api/main.go
backend/internal/config
backend/internal/database
backend/internal/domain
backend/internal/repository
backend/internal/repository/postgres
backend/internal/service
backend/internal/httpapi
backend/infra/db/schema/001_create_mvp_tables.sql
```

Implemented API routes:

```text
GET  /health
POST /v1/projects
GET  /v1/projects/{projectId}
POST /v1/projects/{projectId}/context
```

## First Backend Foundation Goal

The first goal is still:

```text
Project + Project Context + Audit Log persistence
```

This establishes the durable backend base for full MVP inputs.

## Go Implementation Notes

- HTTP uses Go stdlib `net/http` and Go 1.22+ route patterns.
- PostgreSQL uses `pgxpool`.
- Domain structs live in `internal/domain`.
- Business orchestration lives in `internal/service`.
- Repositories are defined in `internal/repository` and implemented in `internal/repository/postgres`.
- HTTP handlers are in `internal/httpapi`.

## Required Local Tooling

This machine currently does not have `go` available in PATH. After installing Go:

```text
cd backend
go mod download
go fmt ./...
go test ./...
go run ./cmd/api
```

## Next Go Backend Steps

```text
1. Install Go toolchain.
2. Run go mod download.
3. Run gofmt and go test.
4. Fix compile issues if any.
5. Apply backend/infra/db/schema/001_create_mvp_tables.sql to local Postgres.
6. Smoke test POST /v1/projects.
7. Smoke test POST /v1/projects/{projectId}/context.
8. Add GitHub access config repository/API.
9. Add SSH access config repository/API.
10. Add evidence snapshot job model and worker skeletons in Go.
```