# App <-> Browser Agent Direct Protocol v1.1

Status: App implementation contract. Transport remains `cascade.browser_agent_direct.v1`; v1.1 adds App-side package gates and failure reunderstanding without restoring legacy Exchange or Playwright packages.

## Ownership boundary

The App owns user requirements, the workflow graph, stage intent, secret references, selector provenance gates, package digests, human approval, and repair lineage. Browser Agent owns bounded runtime navigation, selector and wait adaptation inside the approved product scope, redacted observations, and execution evidence. It must not change business intent, input values, credentials, allowed origins, or safety policy.

Natural-language runtime errors are diagnostic only. Lifecycle decisions come from typed validation reports or a deterministic App audit of the exact source package. Fixtures and Mock Bridge runs are never release evidence.

## Route and selector provenance

For every executable stage, these values identify the action execution route and must match:

- `plan_json.steps[].page_target.url`
- `stage_approval_plan.stages[].entry_route`
- `script_outline.stages[].route`
- the primary selector's `observed_url` or `observed_route_template`

`expected_route_after_action` is a separate post-action assertion and must not replace the execution route.

Route matching uses these fixed rules:

1. Scheme, host, and effective port match exactly.
2. Paths are slash-normalized and fragments are ignored.
3. A template parameter is allowed only as a complete path segment such as `/projects/{id}`.
4. Query is ignored unless the contract explicitly declares query matching.
5. Source-only selector candidates cannot outrank a verified page-scan candidate.

Formal page-scan selector provenance contains `observed_url`, `observed_route_template`, `observed_page_role`, `observed_form_role`, `evidence_digest_sha256`, `evidence_id`, source digest, observation time, and evidence references. These fields are included in the typed approval component digests; any change invalidates the approval subject digest.

Authentication entry evidence and authentication form evidence are separate. A login email field is valid only when the evidence binds it to an authentication route and a password-bearing form with `observed_form_role=authentication`. Newsletter, waitlist, and marketing email fields are excluded. Login success requires both leaving the authentication route and observing an authenticated workspace marker.

Blocking gate codes are:

| Code | Meaning | Owner |
| --- | --- | --- |
| `selector_route_provenance_mismatch` | Primary selector was not observed on the execution route | App package generation |
| `authentication_context_unverified` | Credential control lacks deterministic authentication context | App package generation |
| `login_entry_evidence_missing` | Authentication entry evidence is absent or mixed with form evidence | App package generation |
| `login_success_validation_missing` | Post-login route exit and workspace assertion are incomplete | App package generation |

## Failure reunderstanding API

Both adapters call the same Service method:

```go
ReunderstandDirectBrowserAgentFailure(
    context.Context,
    projectID string,
    request DirectFailureReunderstandingRequest,
) (DirectFailureReunderstandingResult, error)
```

HTTP:

```text
POST /v1/desktop/projects/{project_id}/browser-agent-direct/reunderstand
```

Wails:

```text
ReunderstandDirectBrowserAgentFailure(projectID, request)
```

The request is fixed and fail-closed:

```json
{
  "schema_version": "demoops.direct_failure_reunderstanding.v1",
  "source_job_id": "job_...",
  "source_result_id": "result_...",
  "source_package_id": "package_...",
  "repair_request_id": "repair_...",
  "base_package_digest_sha256": "...",
  "base_graph_digest_sha256": "...",
  "failed_bundle_hash_sha256": "...",
  "failed_plan_hash_sha256": "...",
  "diagnostic_digest_sha256": "...",
  "selected_issue_ids": ["issue_..."],
  "idempotency_key": "...",
  "user_confirmed": true
}
```

Selectors, URLs, package JSON, credentials, and client patches are not accepted. Every required server-issued issue ID must be selected.

A successful response contains persisted `state`, `build`, `new_package_id`, `package_digest_sha256`, `graph_digest_sha256`, `approval_subject_digest_sha256`, `confidence_assessment_hash`, `repair_lineage`, `issues`, and `requires_reapproval=true`. The build remains a draft and is never automatically approved, uploaded, or started.

## Deterministic processing

The Service performs these operations in order:

1. Load the persisted failed result and repair history.
2. Resolve idempotency before requiring the current failed result, so a completed retry remains stable.
3. Bind source Job, Result, Package, repair request, and all supplied digests to persisted state.
4. Read authoritative `ValidationReports[].Decision`. A generic `browser_agent_action_failed` remains an ordinary failure.
5. Accept only `reunderstanding_required` or a deterministic audit reproducing one of the four App gate codes.
6. Reconstruct input from the original project context and local secret refs.
7. Run a real page rescan and regenerate Graph, ScriptDocument, StageApprovalPlan, Outline, bundle, and confidence assessment.
8. Increment generation once per `source_result_id`, persist repair history, and invalidate old approval/upload/ACK/review/editor state.
9. Stop at a new human approval gate.

Failed results and downloaded diagnostic assets remain append-only audit records in repair history. They are not copied into the new uploadable package.

## Digest and idempotency rules

The canonical request JSON is hashed and stored with the idempotency key. The same key and same request return the same persisted draft. The same key with a different request returns `idempotency_conflict`.

The package digest covers the formal package. The graph digest covers the authoritative graph. The approval subject digest is computed from typed plan, stage approval plan, outline, Browser Agent contract, prompt policy, approval Markdown, and selector provenance components. The confidence assessment hash binds package readiness. Upload requires all four current values and a fresh human approval.

Any source identity, lineage, or digest drift returns `package_preview_stale`; clients must reload persisted state and must not synthesize replacement values.

## State machine

```text
local_generated
  -> human_approved
  -> upload_initialized
  -> accepted
  -> running
  -> completed | failed | canceled | expired

failed + authoritative App issue
  -> reunderstanding_required
  -> rescanning
  -> local_generated (new package, requires reapproval)
```

When `reunderstanding_required` is persisted, the UI immediately clears approval data and disables upload. Requesting, failed, incomplete, draft-ready, and reapproval states must survive reload. A stale response never creates a local success state.

## Errors and responsibility

| Code | HTTP class | Retry | Responsibility / action |
| --- | --- | --- | --- |
| `bad_request` | 400 | No | Client omitted schema, confirmation, or required issue selection |
| `repair_lineage_mismatch` | 409 | Reload | App state does not match source identities; reload the failed result |
| `package_preview_stale` | 409 | Reload | Digest or lineage changed; discard the client preview |
| `idempotency_conflict` | 409 | No | Generate a new key only for a deliberately new request |
| `reunderstanding_incomplete` | 422 | After remediation | Real rescan or formal package gate is still blocked; no approvable package exists |
| the four selector/auth codes above | 422 | After rescan | App must regenerate from observed evidence; Browser Agent must not patch App intent |
| `browser_agent_action_failed` | runtime failure | Policy-dependent | Ordinary runtime failure; does not imply reunderstanding |

All messages and issue summaries are redacted. Responses, logs, state, packages, reports, and artifacts must not contain plaintext credentials, raw authorization headers, browser storage, or secret values.

## Capability negotiation and implementation guidance

- Advertise the Direct transport, package schema, runtime, Worker protocol, outcome verifier rules, and reunderstanding capability independently.
- Model selector provenance as typed data rather than free-form metadata.
- Keep result and repair history append-only; derive the current UI state from persisted authoritative state.
- Preserve explicit responsibility domains in validation checks and issues.
- Apply the same Service and structured errors through HTTP and Wails.
- Keep Mock Bridge behavior fail-closed and state-compatible, but label it non-formal.
- Never hardcode product routes or selectors and never use fixtures as merge or release acceptance.
