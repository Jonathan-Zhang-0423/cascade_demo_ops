# Client to Cloud Exchange Protocol

> [!IMPORTANT]
> **当前交换协议。** `browser-agent-outline-v1` 是最新执行包主路径，受限 TypeScript 是 Legacy 兼容子路径。Server 本地视频编辑器不改变本协议，只消费执行后形成的录屏、素材目录和编辑计划。

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
  -> App agent repairs StageApprovalPlan + BrowserAgentScriptOutline using local code context
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

Production deployments may mount these routes under a gateway prefix, for
example `/aigc/v1/...`. The route semantics stay the same.

The Go DTOs live in `backend/internal/model/exchange.go`.

Cloud intake validation lives in
`backend/internal/model/exchange_validation.go`. It validates the envelope,
payload schema, identity binding, upload approval, executable script bundle,
recording run spec, sandbox policy, graph/script hash binding, and renderable
recording result packages before downstream execution or rendering.

The current service boundary lives in `backend/internal/app/exchange_intake.go`
and maps the API contract to callable methods:

- `Init` -> `POST /v1/execution-packages/init`
- `Upload` -> `POST /v1/execution-packages`
- `Status` -> `GET /v1/execution-packages/:id/status`
- `GetResultPackage` -> `GET /v1/result-packages/:id`
- `AckResultPackage` -> `POST /v1/result-packages/:id/ack`

This layer currently uses in-memory state; production storage should implement
the existing `repository.ExchangeRepository` contract.

## Executable Script Bundle

The App uploads one approved `ExecutableRecordingScriptBundle` inside
`ClientExecutionPackage.executable_script_bundle`. The new product path uses
`runtime=browser-agent-outline-v1`:

- `plan_json`: the machine-auditable `ExecutionScriptDocument`.
- `stage_approval_plan`: user-approved stage JSON with route/component/API
  evidence, input semantics, success state, risks, confidence, and timing.
- `script_outline`: the server browser agent outline with product routes,
  interaction targets, candidate selector/role/name hints, waits, capture
  points, and bounded exploration scope.
- `agent_prompt_policy`: immutable vs server-editable fields and the browser
  agent system policy.
- `project_understanding_dossier` or `understanding_dossier_ref`: requirement
  related project understanding, stored as summaries, hashes, evidence refs, and
  redacted snippets rather than full source.
- `approval_markdown`: the Chinese reasoning and approval document shown to the
  user.

These fields are hash-bound through `plan_hash_sha256`,
`stage_plan_hash_sha256`, `outline_hash_sha256`,
`prompt_policy_hash_sha256`, `understanding_dossier_hash_sha256`,
`markdown_hash_sha256`, and `bundle_hash_sha256`. The server validates these
hashes before execution.

The legacy `runtime=playwright-restricted-sandbox` remains supported for old
servers and CI fixtures. In that mode, `playwright_script.inline_source` is a
deterministic restricted TypeScript artifact and must export
`runCascadeRecording`. In outline mode, `playwright_script` may be empty and the
manifest entry function is `runBrowserAgentOutline`; the cloud browser agent is
responsible for final adaptive exploration and executable script repair inside
the approved boundaries.

## Upload Shape

Production upload bodies use an encrypted payload reference:

```json
{
  "upload_id": "upload_...",
  "envelope": {},
  "payload_ref": {
    "kind": "artifact",
    "artifact_id": "payload_artifact_1",
    "uri": "s3://cascade-exchange/payload.enc",
    "sha256": "<ciphertext sha256>",
    "size_bytes": 2048,
    "encrypted": true,
    "sensitive": true,
    "compression_alg": "gzip"
  }
}
```

`payload_ref` must match `envelope.payload_ref`. The dev channel may also
accept plaintext `payload` for local tests only; production should not persist
or log that field.

## Status and Ack

Status polling is the stable App UI driver. The server should use these stages:

```text
accepted -> validating -> preparing_worker -> browser_agent_planning -> script_ready -> recording -> material_validation -> directing -> rendering -> quality_validation -> completed
```

Failed jobs use `status=failed` and return a `failure_summary` on the status
endpoint. Detailed screenshots, traces, console/network summaries, and repair
materials live in the encrypted failed `RecordingResultPackage`.

After the App downloads artifacts and verifies checksums, it acknowledges
delivery with:

```json
{
  "acked_by_install_id": "install_...",
  "received_asset_ids": ["artifact_pkg_1_demo_video_001"],
  "verified_checksums": true
}
```

`verified_checksums=true` is required. If checksum mismatches exist, the App
sends `checksum_mismatch_ids` and the server rejects the ack.

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
- Cloud execution also resolves a `SandboxPolicy` from
  `RecordingRunSpec.sandbox_policy` and the executable bundle security policy.
  The default production profile is `mvp_cloud` with per-job container
  isolation, allowed-domain-only egress through policy proxy, read-only root
  filesystem, vault-only secret injection, encrypted sensitive artifacts, and
  redacted encrypted diagnostics.
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
`ExecutionTrace.sandbox` and `CloudExecutionAuditTrail.sandbox` return the
resolved sandbox policy hash, profile, isolation mode, network mode, worker id,
and runtime versions for audit and reproducibility.
Final videos are delivered as encrypted artifact descriptors with
`role=final_demo_video`, `kind=video`, `encrypted=true`, `sensitive=true`, and
the App recipient key id.

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
`ExecutableRecordingScriptBundle` carries `repair_lineage`. In the outline
runtime, repairs update the approved stage plan, script outline, prompt policy,
and evidence chain; in the legacy TS runtime, repairs update the restricted
script. Both paths must pass the same hash binding, allowed-domain checks,
safety validation, and human approval flow as a first-run package.

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
