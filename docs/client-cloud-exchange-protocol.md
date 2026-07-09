# Client to Cloud Exchange Protocol

This protocol moves an approved desktop execution plan from the customer app to
Cascade cloud, then returns encrypted recording results and generated assets.

## Lifecycle

```text
App local understanding
  -> user approves execution plan
  -> App builds ClientExecutionPackage
  -> App canonicalizes, compresses, encrypts, signs
  -> Cloud validates ExchangeEnvelope
  -> Cloud records and renders from DemoWorkflowGraph + RecordingRunSpec
  -> Cloud returns RecordingResultPackage
  -> App acknowledges result delivery
```

Failure repair loop:

```text
Cloud execute_script fails
  -> Cloud captures redacted screenshot, trace, console/network summaries, DOM/a11y refs
  -> Cloud returns failed RecordingResultPackage with ScriptFailureDiagnostic + ScriptRepairRequest
  -> App agent repairs ExecutionScriptDocument + restricted TS script using local code context
  -> user reviews repair approval markdown and approves
  -> App uploads a new ClientExecutionPackage with RepairContext + ScriptRepairLineage
```

## API Contract

```text
POST /v1/execution-packages/init
POST /v1/execution-packages
GET  /v1/execution-packages/:id/status
GET  /v1/result-packages/:id
POST /v1/result-packages/:id/ack
```

The Go DTOs live in `backend/internal/model/exchange.go`.

## Security Boundary

- App uploads only structure summaries, hashes, redacted evidence, and encrypted
  artifact descriptors by default.
- Raw secrets are only accepted inside encrypted credential grants and must be
  converted to cloud vault references before persistence.
- The database stores metadata, checksums, URIs, vault refs, and JSON policies;
  it does not store decrypted execution packages, source archives, or binary
  blobs.
- Cloud execution must obey `allowed_domains`, IP allowlist requirements,
  forbidden pages, redactions, retention, and user approval metadata.

## Reproducibility Boundary

Every execution package carries:

- approved `DemoWorkflowGraph`
- `RecordingRunSpec` with browser, viewport, timing, output, and failure policy
- package, graph, input, source, and browser runtime hashes
- deterministic seed
- human approval digest

Cloud result packages return execution traces, step results, generated artifact
checksums, runtime versions, and optional graph patch suggestions.

## Failure Diagnostics

When `RecordingResultPackage.status` is `failed`, the payload must include:

- `failure_diagnostic`: failed node, machine-readable error, current URL/title,
  encrypted sensitive screenshot/trace/DOM/a11y artifact refs, redacted
  console/network summaries, redaction report, and repair hints.
- `repair_request`: source result/package/job ids, failed plan and bundle hashes,
  repair attempt, max repair attempts, and `approval_required=true`.

Cloud must redact diagnostics before returning them to the App. Diagnostics must
not include raw secrets, cookies, authorization headers, local/session storage
tokens, full HTML, or customer source content. Failure artifacts use
`package_artifacts` roles such as `failure_screenshot`, `failure_trace`,
`failure_dom_snapshot`, and `failure_accessibility_snapshot`, and default to
`sensitive=true` and encrypted transport.

Repair packages reuse the normal upload API. The repaired
`ClientExecutionPackage` carries `repair_context`, and the repaired
`ExecutableRecordingScriptBundle` carries `repair_lineage`. The repaired script
must pass the same hash binding, AST/security validation, allowed-domain checks,
and human approval flow as a first-run package.
