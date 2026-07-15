# Ark Media Integration

This document tracks the cloud-side preparation for Seedance/Seedream media
capabilities. The current implementation is intentionally default-off for real
provider calls.

For model IDs, parameter ranges, input/output formats, and project-specific
preflight rules, see [ark-model-parameters.md](ark-model-parameters.md).

## Runtime Switch

`CASCADE_ARK_MEDIA_MODE` controls Ark media behavior:

| Value | Behavior |
| --- | --- |
| `dry_run` | Default. Build safe request previews and artifacts, but do not call Ark. |
| `real` | Allow the Ark HTTP client to call configured Seedance/Seedream endpoints. |
| `disabled` | Reject Ark media calls explicitly. |

Runtime health exposes only the mode and configured provider status. It never
returns API keys, Authorization headers, prompts, or local provider secrets.

`CASCADE_ARK_ASSET_PUBLIC_DIR` and `CASCADE_ARK_ASSET_PUBLIC_BASE_URL` enable
the first real asset publisher:

```text
CASCADE_ARK_ASSET_PUBLIC_DIR=/srv/cascade/ark-inputs
CASCADE_ARK_ASSET_PUBLIC_BASE_URL=https://assets.example.com/ark-inputs
```

When both are set, captured MP4/PNG/JPEG assets that are otherwise local-only
are copied into the public directory and represented as HTTPS URLs in
`ark_asset_publication_result.json`. Without these variables the publisher
stays dry-run.

Seedance task polling and output persistence are controlled by:

| Env | Default | Meaning |
| --- | --- | --- |
| `CASCADE_ARK_MEDIA_POLL_ATTEMPTS` | `1` in `real`, `0` otherwise | Number of `GET /contents/generations/tasks/{id}` checks after a task is submitted. |
| `CASCADE_ARK_MEDIA_POLL_INTERVAL_MS` | `0` | Delay between polling attempts. Use a larger value only when waiting for async generation. |
| `CASCADE_ARK_MEDIA_OUTPUT_DIR` | render output dir | Directory that receives downloaded generated candidate media. |
| `CASCADE_ARK_MEDIA_DOWNLOAD_MAX_BYTES` | `536870912` | Per-file download cap for provider output URLs. |

## Providers

Seedance is used for optional video-operation experiments:

```text
SEEDANCE_API_KEY
SEEDANCE_BASE_URL=https://ark.cn-beijing.volces.com/api/v3
SEEDANCE_MODEL=doubao-seedance-2-0-260128
```

Seedream is reserved for non-product visual assets such as title cards or
section dividers:

```text
SEEDREAM_API_KEY
SEEDREAM_BASE_URL=https://ark.cn-beijing.volces.com/api/v3
SEEDREAM_MODEL=doubao-seedream-5-0-pro-260628
```

Both providers can fall back to `DOUBAO_API_KEY` or `ARK_API_KEY` for local
development. Real keys must stay in environment variables or a vault, never in
execution packages or persisted artifacts.

## Current Implementation

The renderer still produces the final demo from captured product materials:

```text
ClientExecutionPackage -> Playwright recording/screenshots -> existing-asset render
```

After render, the backend writes:

- `director_input.json`: source-only material and narrative context for a
  future director model, including the decision priority
  `UserIntent > ClientScript > ModelSuggestion`.
- `director_edit_suggestion.json`: adapter output for narrative, shot,
  caption, style, and music-direction suggestions. It includes
  `provider_gate`, which records whether Ark media is `dry_run`, `disabled`,
  `blocked_before_provider_call`, `ready_for_real_call`,
  `provider_call_submitted`, or `provider_call_failed`. When the gate is ready
  and a real Ark client is configured, it also includes `provider_call` with the
  redacted task id, provider status, request summary, and HTTP trace.
- `director_edit_suggestion_validation.json`: validation result proving the
  suggestion preserved source-only policy, decision priority, source traces,
  and required script-step boundaries.
- `ark_media_generation_result.json`: normalized provider-call result. If Ark
  returns generated media URLs, they are registered as non-authoritative
  `candidate_artifacts`; when downloads succeed, durable local
  `downloaded_artifacts` are written under `ark-media/`. If Ark only returns an
  async task id, the file records the pending task without inventing output
  artifacts.
- `candidate_asset_review.json`: conservative review of downloaded generated
  candidates. It records approved/rejected candidates and writes
  `approved_for_demo=true` only for local, non-authoritative, presentation-only
  video candidates.
- `candidate_asset_edit_plan_patch.json`: optional edit-plan patch proposal
  derived from approved candidates. It is `auto_apply=false`, requires explicit
  opt-in plus renderer validation, and may only propose presentation-only
  intro/divider/outro shots that do not bind to a customer-side script step.
- `ark_media_dry_run_plan.json`: Seedance/Seedream request previews and
  prerequisites for safe real calls.
- `ark_asset_publication_plan.json`: dry-run publication/preflight plan for
  turning local captured MP4/PNG assets into short-lived HTTPS URLs or provider
  asset references.
- `ark_asset_publication_result.json`: current publisher result. The default
  publisher is dry-run, so proposed URLs remain non-callable until a real
  object-storage/CDN/provider-asset publisher replaces it.

`ark_media_dry_run_plan.json` is intentionally machine-readable:

- `provider_constraints` records Seedance/Seedream duration, format, public URL,
  and non-authoritative output limits.
- `source_asset_requirements` lists every source asset that must be published or
  converted before real Ark calls are allowed.
- `output_handling` says returned URLs must be downloaded quickly, stored as
  artifacts, and treated as generated candidates rather than proof of product
  behavior.
- `real_call_readiness` reports whether a package is blocked, ready only after
  `CASCADE_ARK_MEDIA_MODE=real`, or disabled by missing source assets.

In dry-run mode, `real_call_readiness.can_call_now` must remain `false`. A
package may only report `can_call_when_enabled=true` after all required source
assets are in accepted formats and exposed through public HTTPS URLs or provider
asset references.

`ark_asset_publication_plan.json` is also machine-readable. It merges repeated
asset requirements across model tasks, reports whether each captured asset is
already public, needs conversion, or only needs publication, and proposes stable
output filenames for a future publisher implementation. The current plan is
dry-run only: it does not upload files, mint URLs, or grant provider access.

`ark_asset_publication_result.json` records the publisher outcome. In the
default dry-run implementation, `contains_dry_run_refs=true` and
`can_use_for_real_call=false` whenever local captured assets still need real
publication. When the static asset publisher is configured, local captured
assets are copied to `CASCADE_ARK_ASSET_PUBLIC_DIR`; the result becomes
`can_use_for_real_call=true` only when every required source asset has a real
HTTPS URL and no blocker remains. This prevents simulated URLs from being
treated as valid Seedance or Seedream inputs.

`director_edit_suggestion.json.provider_gate` is the final preflight checkpoint
before any director model call can be enabled. It requires:

- `CASCADE_ARK_MEDIA_MODE=real`
- a configured Seedance-compatible key from `SEEDANCE_API_KEY`, `DOUBAO_API_KEY`,
  or `ARK_API_KEY`
- `ark_media_dry_run_plan.real_call_readiness.can_call_when_enabled=true`
- `ark_asset_publication_result.can_use_for_real_call=true`

Even when `provider_gate.status=ready_for_real_call`, the current adapter only
reports readiness unless an Ark client is attached. When a real call is made,
`provider_call.real_call_made=true`; generated media still remains
non-authoritative candidate material and cannot replace captured product UI or
the script-driven final demo.

`backend/internal/media` adds a small Ark HTTP client for:

- `POST /contents/generations/tasks`
- `GET /contents/generations/tasks/{id}`
- `POST /images/generations`

The client is covered by local `httptest` tests and does not call the network
unless `CASCADE_ARK_MEDIA_MODE=real` and the caller invokes it.

The cloud-side director adapter now invokes `POST /contents/generations/tasks`
only after all preflight gates pass. A failed provider request is recorded in
`director_edit_suggestion.json.provider_call` and does not fail the stable
recording/rendering pipeline.

When a provider response or poll result includes downloadable video or image
URLs, the backend writes them into
`ark_media_generation_result.json.candidate_artifacts` and attempts to download
them into `downloaded_artifacts`. These artifacts carry `include_in_demo=false`
and `source_material_policy=non_authoritative_generated_candidate`, so
downstream rendering must explicitly validate and opt in before any candidate
is used. If polling still only returns a task id or queued/running status, the
generation result stays in `pending_provider_output`.

The deterministic renderer will reject generated candidates unless
`candidate_asset_review.json` or a later explicit review/planning step sets all
of these metadata fields on the local downloaded artifact:

```json
{
  "approved_for_demo": true,
  "non_authoritative": true,
  "source_material_policy": "non_authoritative_generated_candidate"
}
```

Even after approval, generated candidates are presentation-only. They cannot set
`source_step_id` in a shot and therefore cannot satisfy or replace a required
customer-side product interaction step.

`candidate_asset_edit_plan_patch.json` is the only default bridge from approved
generated candidates toward future edit-plan changes. It records proposed shots
without applying them to `demo_edit_plan.json` or the current final video. A
later user/admin/model workflow must explicitly merge the patch and pass render
validation before any candidate appears in delivery.

## Hard Boundary

Ark media output is non-authoritative. It may help with presentation design,
title cards, transitions, pacing previews, or style exploration, but it must not
replace real captured product UI.

The final delivery priority is:

```text
user_explicit_requirements
-> workflow_graph + executable_script_bundle + recorded artifacts
-> cloud_model_suggestions
```

Models may not create new browser actions, selectors, test data, or fake
product screenshots.
