# Cascade DemoOps Engine

Server-side engine for browser exploration, deterministic execution, recording, lightweight editing, and result delivery.

Authoritative Server-side v2 flow:

```text
Approved Stage JSON + Browser Agent outline + understanding dossier
  -> intake validation and hash binding
  -> bounded Server Browser Agent exploration and execution planning
  -> Playwright execution + Outcome Verifier
  -> policy-constrained Runtime Repair Patch when allowed
  -> recording, local lightweight editing, FFmpeg rendering, and delivery
```

Architecture rules:

```text
Business semantics come from external business evidence.
Runtime page facts come from the Server Browser Agent.
The original authorized draft remains immutable; permitted repairs are auditable overlays.
Models may resolve or suggest targets, while deterministic services enforce execution and safety boundaries.
```

The shipped Desktop execution path is the direct Browser Agent transport. The retired DemoOps Exchange contracts remain only for explicitly marked test/fixture compatibility and are not an upload or execution capability in production Desktop builds. The `browser-agent-outline-v1` package contract remains authoritative for the payload format.

Documentation:

- [Engineering and Agent Design Principles](./docs/engineering-principles.md)
- [Server-side v2 authoritative architecture](./docs/server-browser-agent-execution-editor-architecture-v2.md)
- [Documentation index and v1 migration status](./docs/README.md)
- [Local Demo Editor MVP](./docs/local-demo-editor-mvp.md)
- [Enterprise film pipeline v2 development plan](./docs/enterprise-film-development-plan-v2.md)
- [Enterprise film pipeline v2 implementation record](./docs/enterprise-film-implementation-v2.md)

Legacy AIGC capabilities may only return through adapter-based integration; the old architecture is not part of the Server-side v2 core. See [Legacy AIGC Integration Plan](./docs/legacy-aigc-integration-plan.md).
