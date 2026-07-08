# Cascade DemoOps Engine

MVP backend-first project for Cascade DemoOps.

Core MVP flow:

```text
Access & Context
  -> Evidence
  -> Product Intelligence / Workflow Graph
  -> Execution & Rehearsal
  -> Asset Generation
```

Important architecture rule:

```text
Models produce structured suggestions and artifacts.
Deterministic services perform browser, repo, SSH, storage, rendering, and execution work.
Demo Workflow Graph is the durable intermediate representation.
```

Legacy AIGC note:

```text
D:\test-2026-7-5\aigc-standalone is reserved for later adapter-based integration into Asset Generation.
Do not import the old architecture wholesale.
```
