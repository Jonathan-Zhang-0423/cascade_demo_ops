# Backend Implementation Next Steps

This file tracks the concrete next engineering steps after the initial project skeleton.

## Current State

Done:

```text
monorepo workspace
core package boundaries
schema package
model adapter interfaces
security interfaces
repository interfaces
API route skeletons
database schema init SQL
legacy AIGC adapter reminder
```

Not done yet:

```text
real database client
real repository implementations
request validation middleware
job queue implementation
GitHub integration
SSH integration
browser execution
asset rendering
```

## Next Build Milestone: Backend Foundation 1

Goal:

```text
API can create a project, save project context, and persist audit logs.
```

Tasks:

```text
1. choose DB client: drizzle / kysely / prisma / node-postgres
2. add database connection factory in backend/packages/database
3. implement ProjectRepository
4. implement ProjectContextRepository
5. add API request validation with Zod schemas
6. implement POST /v1/projects
7. implement POST /v1/projects/:projectId/context
8. implement GET /v1/projects/:projectId
9. add basic audit log writer
10. add unit tests for schema validation and repository contracts
```

## Next Build Milestone: Access Foundation

Goal:

```text
Project can store full input access configuration without executing risky actions.
```

Tasks:

```text
1. GitHub access config endpoint stores read-only repo metadata
2. SSH access config endpoint stores secret_ref, allowed_paths, allowed_commands
3. SecretProvider interface gets a local-dev implementation
4. SSH command policy tests for denied commands
5. AccessHealthReport stub returns configured/not_configured states
```

## Next Build Milestone: Evidence Snapshot Stubs

Goal:

```text
Orchestrator can enqueue snapshot jobs and workers can create placeholder evidence records.
```

Tasks:

```text
1. choose queue: BullMQ first, Temporal later if needed
2. define queue names and job payloads
3. repo-worker writes RepoEvidence placeholder
4. ssh-worker writes ServerEvidence placeholder
5. browser-worker writes BrowserEvidence placeholder
6. API exposes evidence list endpoint
```