# Ark Media Integration

This document tracks the cloud-side preparation for Seedance/Seedream media
capabilities. The current implementation is intentionally default-off for real
provider calls.

## Runtime Switch

`CASCADE_ARK_MEDIA_MODE` controls Ark media behavior:

| Value | Behavior |
| --- | --- |
| `dry_run` | Default. Build safe request previews and artifacts, but do not call Ark. |
| `real` | Allow the Ark HTTP client to call configured Seedance/Seedream endpoints. |
| `disabled` | Reject Ark media calls explicitly. |

Runtime health exposes only the mode and configured provider status. It never
returns API keys, Authorization headers, prompts, or local provider secrets.

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
  future director model.
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
publication. This prevents simulated URLs from being treated as valid Seedance
or Seedream inputs.

`backend/internal/media` adds a small Ark HTTP client for:

- `POST /contents/generations/tasks`
- `GET /contents/generations/tasks/{id}`
- `POST /images/generations`

The client is covered by local `httptest` tests and does not call the network
unless `CASCADE_ARK_MEDIA_MODE=real` and the caller invokes it.

## Hard Boundary

Ark media output is non-authoritative. It may help with presentation design,
title cards, transitions, pacing previews, or style exploration, but it must not
replace real captured product UI.

The primary storyline remains:

```text
workflow_graph + executable_script_bundle + recorded artifacts
```

Models may not create new browser actions, selectors, test data, or fake
product screenshots.
