# MVP Project Structure

> [!IMPORTANT]
> **Historical project-structure proposal.** Existing package names may still reflect this design, but it must not be used as the roadmap for new modules. Follow [server-browser-agent-execution-editor-architecture-v2.md](./server-browser-agent-execution-editor-architecture-v2.md) for Server-side development.

## Design Goal

MVP uses four implementation layers, while preserving future upgrade space for richer model adapters, world models, software-interaction RL models, and legacy AIGC integration.

```text
1. Access & Context Layer
2. Product Intelligence / Workflow Graph Layer
3. Execution & Rehearsal Layer
4. Asset Generation Layer
```

The MVP should be narrow in feature scope but not narrow in backend foundations. It must support full input access, auditable permissions, versioned evidence, execution traces, and model adapter boundaries from day one.

## Directory Layout

```text
backend/
  api/                       Backend API service
  backend/workers/                   Backend async workers
  backend/packages/                  Backend domain packages
  infra/                     Backend infrastructure
  tests/                     Backend tests

frontend/
  web/                       Operator/customer review UI

backend/workers/
  orchestrator/              Job orchestration, state transitions, async workflows
  browser-worker/            Playwright browser sandbox and workflow execution
  repo-worker/               GitHub read-only repo snapshot and source evidence extraction
  ssh-worker/                Read-only SSH diagnostics and server evidence extraction
  render-worker/             Video/docs/screenshot rendering jobs

backend/packages/
  schemas/                   Shared JSON/Zod schemas and types
  access-context/            Project input, credentials, permissions, context builder
  evidence/                  Browser/Repo/Server/Docs/UserInput evidence model
  product-intelligence/      Product map, route map, feature discovery, workflow planning
  workflow-graph/            Demo Workflow Graph domain model and versioning
  graph-executor/            Graph actions, validations, fallback, trace collector
  model-adapters/            LLM, vision, RL, world model, AIGC adapter interfaces
  asset-generation/          Video script, narration, docs, screenshot pack, render plan
  storage/                   Artifact storage abstraction
  security/                  Secret refs, audit log, command/file allowlists
  shared/                    Common errors, logging, ids, config helpers

infra/
  db/migrations/             Database schema init scripts
  docker/                    Local infrastructure definitions

docs/                        Architecture and implementation notes
tests/                       Cross-package and integration tests
```

## Layer 1: Access & Context

Responsibilities:

- Create project.
- Collect full MVP inputs.
- Manage GitHub read-only integration.
- Manage SSH read-only integration.
- Store credentials as secret references.
- Build Project Context.
- Produce access health reports and audit logs.

MVP inputs:

```text
Frontend URL
Demo account
GitHub repo read-only authorization
SSH read-only server access
Docs / release notes
Product description
Target audience
Brand constraints
Security constraints
Must-show / must-not-show requirements
```

Main backend modules:

```text
backend/packages/access-context
backend/packages/security
backend/workers/repo-worker
backend/workers/ssh-worker
backend/api
```

## Layer 2: Product Intelligence / Workflow Graph

Responsibilities:

- Convert raw inputs into structured evidence.
- Build Product Map.
- Create Candidate Workflows.
- Generate Demo Workflow Graph draft.
- Preserve source evidence for every graph step.

Important rule:

```text
All planning output must be structured and schema-validated.
The durable output is Demo Workflow Graph, not free-form agent text.
```

Main packages:

```text
backend/packages/evidence
backend/packages/product-intelligence
backend/packages/workflow-graph
backend/packages/model-adapters
```

## Layer 3: Execution & Rehearsal

Responsibilities:

- Execute approved Demo Workflow Graph in browser sandbox.
- Validate every step.
- Collect trace, screenshot, DOM, console logs, network logs.
- Diagnose failures with repo/server/browser evidence.
- Produce graph patches or human-action requests.

Important rule:

```text
Agents do not directly control browser or SSH.
Agents propose structured plans and patches.
Deterministic workers execute allowed actions.
```

Main backend modules:

```text
backend/packages/graph-executor
backend/workers/browser-worker
backend/workers/orchestrator
backend/packages/model-adapters
```

## Layer 4: Asset Generation

Responsibilities:

- Generate 60s demo video.
- Generate step-by-step docs.
- Generate screenshot pack.
- Produce asset review artifacts.
- Preserve provenance to workflow graph, execution run, repo snapshot, and server snapshot.

Main backend modules:

```text
backend/packages/asset-generation
backend/workers/render-worker
backend/packages/storage
```

## Future Adapter Integration

Legacy AIGC branch:

```text
D:\test-2026-7-5\aigc-standalone
```

Integration principle:

```text
Adapter first. Do not migrate old architecture wholesale.
```

Target location:

```text
backend/packages/model-adapters/src/legacy-aigc-adapter.ts
backend/packages/asset-generation/src/aigc-backed-generators.ts
```

Future model adapters:

```text
LLMPlanningModelAdapter
VisionUIUnderstandingAdapter
SoftwareInteractionRLAdapter
WorldModelAdapter
FailureRepairModelAdapter
AssetGenerationAdapter
LegacyAigcAdapter
```

## Immediate Build Order

```text
1. Shared schemas and DB schema
2. Project creation and access context APIs
3. Secret refs, permission scopes, audit logs
4. GitHub repo snapshot worker
5. SSH server snapshot worker
6. Evidence model
7. Hand-written Workflow Graph execution
8. Browser worker and trace collection
9. Graph review UI
10. Model adapter interfaces
11. Product map and graph draft generation
12. Rehearsal failure diagnosis
13. Asset generation and review
```
