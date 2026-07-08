# Legacy AIGC Adapter Integration Plan

## Source

```text
D:\test-2026-7-5\aigc-standalone
```

This directory contains the only legacy AIGC functionality currently considered useful for the new MVP project.

## Decision

Use adapter-based integration later. Do not copy the full legacy architecture into the new project.

```text
Adapter first. Capability migration only. No old architecture migration.
```

## Target Layer

Legacy AIGC belongs to:

```text
Layer 4: Asset Generation Layer
```

It may provide:

```text
video script generation
narration generation
subtitle generation
docs draft generation
asset planning
render prompt generation
```

It must not own:

```text
Access & Context
GitHub integration
SSH integration
Browser rehearsal
Workflow Graph execution
Permission system
Audit logging
```

## Target Interfaces

```text
backend/packages/model-adapters/src/index.ts
backend/packages/asset-generation/src/index.ts
```

Planned adapter:

```ts
LegacyAigcAdapter
```

Expected input:

```text
workflowGraphId
executionRunId
assetType
project context summary
validated execution trace
selected screenshots
brand and audience constraints
```

Expected output:

```text
script
narration
docsDraft
renderPlan
```

## Migration Timing

Do not integrate during the first backend foundation step.

Recommended timing:

```text
After Workflow Graph execution and Asset Generation interfaces are stable.
Before production-quality video generation.
```

## Evaluation Checklist

Before integration, inspect:

```text
runtime dependencies
model dependencies
input/output format
file/artifact assumptions
stateful side effects
security assumptions
rendering assumptions
```

Then wrap useful functions behind adapter methods.