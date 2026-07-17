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

The v2 architecture is the target direction and is still being implemented. The versioned exchange and `browser-agent-outline-v1` contracts remain authoritative for implemented code paths until an explicit schema migration lands.

Documentation:

- [Server-side v2 authoritative architecture](./docs/server-browser-agent-execution-editor-architecture-v2.md)
- [Documentation index and v1 migration status](./docs/README.md)
- [Local Demo Editor MVP](./docs/local-demo-editor-mvp.md)

Legacy AIGC capabilities may only return through adapter-based integration; the old architecture is not part of the Server-side v2 core. See [Legacy AIGC Integration Plan](./docs/legacy-aigc-integration-plan.md).
