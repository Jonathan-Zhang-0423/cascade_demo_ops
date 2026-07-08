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
