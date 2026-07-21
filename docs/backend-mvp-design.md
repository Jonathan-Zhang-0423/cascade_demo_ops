# Backend MVP Design

> [!IMPORTANT]
> **Historical MVP architecture.** Keep this document for code archaeology only. The active Server-side architecture is [server-browser-agent-execution-editor-architecture-v2.md](./server-browser-agent-execution-editor-architecture-v2.md).

## Scope

The backend MVP must support full input access and long-term extensibility, while implementing only the core four-layer product flow.

```text
Access & Context -> Evidence -> Workflow Graph -> Execution/Rehearsal -> Asset Generation
```

## Backend Services

### API Service

Responsibilities:

- Project creation and lifecycle APIs.
- Access configuration APIs for frontend, GitHub, SSH, docs, brand, and security constraints.
- Graph review and asset review APIs.
- Execution and asset status APIs.

Initial route groups:

```text
GET    /health
POST   /v1/projects
GET    /v1/projects/:projectId
POST   /v1/projects/:projectId/context
POST   /v1/projects/:projectId/access/github
POST   /v1/projects/:projectId/access/ssh
POST   /v1/projects/:projectId/evidence/snapshot
POST   /v1/projects/:projectId/workflow-graphs/draft
POST   /v1/workflow-graphs/:workflowGraphId/approve
POST   /v1/workflow-graphs/:workflowGraphId/rehearsals
GET    /v1/execution-runs/:executionRunId
POST   /v1/workflow-graphs/:workflowGraphId/assets
GET    /v1/assets/:assetId
POST   /v1/assets/:assetId/approve
```

### Orchestrator Worker

Owns job state transitions and retries. It should not contain product intelligence logic directly.

Primary job types:

```text
build_project_context
snapshot_repo
snapshot_server
snapshot_browser
build_product_map
draft_workflow_graph
run_rehearsal
diagnose_failure
generate_assets
```

### Repo Worker

Read-only GitHub access. Produces Repo Evidence.

MVP extraction targets:

```text
routes
components
data-testid / aria labels
API endpoints
schema / migrations
seed scripts
tests / storybook
README / docs
```

### SSH Worker

Read-only server diagnostics. Produces Server Evidence.

MVP restrictions:

```text
secret_ref only
command allowlist
path allowlist
no write commands
full audit log
stdout/stderr stored as artifacts
```

### Browser Worker

Runs real browser sessions. Produces Browser Evidence and Execution Trace.

Responsibilities:

```text
open URL
login with secret_ref
explore pages
execute Workflow Graph
validate steps
capture screenshots / DOM / accessibility tree / console / network logs
record raw video for recording runs
```

### Render Worker

Generates final assets from validated graph and execution traces.

MVP outputs:

```text
60s demo video
step-by-step docs
screenshot pack
```

## Data Model Strategy

MVP stores stable aggregates in relational tables and keeps evolving details in JSONB:

```text
project_contexts.context_json
workflow_graphs.graph_json
execution_runs.run_json
step_results.result_json
assets.provenance_json
evidence_records.data_json
```

This keeps the schema flexible while preserving auditability and query boundaries.

## Model Layer Rule

Models never execute side effects directly.

```text
Model input: evidence + current task state
Model output: structured JSON suggestion, graph patch, diagnosis, or asset plan
Side effects: executed only by workers and deterministic services
```

## Legacy AIGC Integration Reminder

`D:\test-2026-7-5\aigc-standalone` should be integrated later through an adapter into `backend/packages/model-adapters` and `backend/packages/asset-generation`.

Do not migrate old architecture wholesale.
