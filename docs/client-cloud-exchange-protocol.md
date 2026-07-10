# Client to Cloud Exchange Protocol

This protocol moves an approved desktop execution plan from the customer app to
Cascade cloud, then returns encrypted recording results and generated assets.

## Lifecycle

```text
App local understanding
  -> user approves execution plan
  -> App builds ClientExecutionPackage plaintext source locally
  -> App canonicalizes, digests, compresses, encrypts, signs
  -> Cloud validates ExchangeEnvelope
  -> Cloud decrypts only inside an isolated worker memory boundary
  -> Cloud records and renders from DemoWorkflowGraph + RecordingRunSpec
  -> Cloud returns encrypted RecordingResultPackage/artifact refs
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

Cloud intake validation lives in
`backend/internal/model/exchange_validation.go`. It validates the envelope,
payload schema, identity binding, upload approval, executable script bundle,
recording run spec, graph/script hash binding, and renderable recording result
packages before downstream execution or rendering.

The current service boundary lives in `backend/internal/app/exchange_intake.go`
and maps the API contract to callable methods:

- `Init` -> `POST /v1/execution-packages/init`
- `Upload` -> `POST /v1/execution-packages`
- `Status` -> `GET /v1/execution-packages/:id/status`
- `GetResultPackage` -> `GET /v1/result-packages/:id`
- `AckResultPackage` -> `POST /v1/result-packages/:id/ack`

This layer currently uses in-memory state; production storage should implement
the existing `repository.ExchangeRepository` contract.

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
- `exchange_packages` stores routing metadata only: org/project ids, status,
  schema versions, payload/ciphertext digests, crypto suite, key wrapping mode,
  server key id, policy, producer metadata, and errors. It must not store
  plaintext `ClientExecutionPackage`, workflow graph JSON, TS script, approval
  markdown, source summaries, screenshots, trace files, or result package body.
- `payload_ref` and all package/result artifact descriptors point to encrypted
  bytes. Inline payloads are for small dev/test packages; production should use
  encrypted artifact refs with checksum and size metadata.

## Crypto Suite v1

- Default key management is hybrid: Cascade server KMS/public key for MVP, with
  `key_wrapping_mode=customer_kms` reserved for enterprise deployments.
- `ExchangeEnvelope.crypto.crypto_suite` and
  `content_encryption_alg` identify the content encryption suite. MVP supports
  `aes-256-gcm` in the local contract helper and reserves
  `xchacha20-poly1305` as the preferred modern suite when the crypto backend is
  linked.
- Each package or attachment uses a fresh random content key. The content key is
  wrapped by `server_kms`, `server_public_key`, or `customer_kms`; databases
  store only `encrypted_content_key`, `content_key_ref`, or `kms_key_ref`.
- App installation signing keys sign the canonical payload digest plus envelope
  metadata. Cloud rejects digest mismatch, bad signature, expiry, replayed nonce,
  org/project mismatch, unsupported crypto suite, missing key-wrapping metadata,
  and unencrypted payload refs.
- Result packages and failure diagnostics are encrypted for the App recipient by
  default: `delivery.recipient_kind=app_installation`,
  `delivery.recipient_key_id=<install key id>`, and
  `delivery.encryption_alg=<suite>`.

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

## Render Handoff

The AIGC render stage must not consume an ad hoc payload. It receives either a
validated `RecordingResultPackage` or a `RenderRequest` created from one via
`executor.NewRenderRequestFromRecordingResult`.

```text
ClientExecutionPackage
  -> validated executable script bundle
  -> cloud recording run
  -> RecordingResultPackage
  -> RenderRequest
  -> AssetTimelineCatalog
  -> DemoEditPlan
```

This keeps the cloud-side video planning layer aligned with the customer-side
agent protocol instead of inventing a second UI interaction format.
