# DemoOps Execution Port v1 Contracts

This directory is the machine-readable companion to `docs/protocols/universal-execution-port-v1.md`.

## Schemas

| Schema | Used by |
| --- | --- |
| `module-manifest.schema.json` | `DescribeCapabilities` |
| `workflow-template.schema.json` | Lifecycle Kernel admission and Task Pack compilation |
| `execution-request.schema.json` | `ValidateRequest` and `StartRun` |
| `module-run.schema.json` | `StartRun`, `GetRun`, `ProvideInput`, `ResumeRun`, `CancelRun` |
| `execution-event.schema.json` | `WatchEvents` |
| `gate-decision.schema.json` | Gate evaluation and UI read model |
| `artifact-descriptor.schema.json` | `ListArtifacts` and module handoff |
| `common.schema.json` | Shared identifiers, budgets, errors, checkpoints and refs |

Transport adapters may wrap these objects with authentication or encryption metadata, but must not change their lifecycle, Gate, replay or error semantics.

Run `pnpm validate:execution-port` from the repository root. The validator compiles every Draft 2020-12 schema, checks all positive and negative fixtures, validates the sample DAG, rejects site-specific router fields, enforces state transitions/event sequencing, and checks the 64 KiB canonical event limit.
