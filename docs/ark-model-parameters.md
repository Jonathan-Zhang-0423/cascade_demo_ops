# Ark Model Parameters Reference

This document is the project-local working summary of the Ark Seedance and
Seedream technical docs collected from the local model-parameter documents on
2026-07-15. It is intentionally concise and implementation-oriented.

The raw vendor docs reviewed were:

- Seedance video generation tutorial
- Create video generation task API
- Seedream 4.0-5.0 tutorial
- Image generation API

Raw local source folder:

```text
C:\Users\15193\Desktop\模型参数
```

When vendor docs are refreshed, update this file first and keep runtime code
pointing to the project-local rules instead of relying on desktop-only files.

Seedance 2.5 has a separate target-migration baseline at
[Seedance 2.5 与 Ark 媒体能力迁移基线](seedance-2-5-migration-baseline.md).
The 2026-08-19 provider-account capability snapshot is incorporated in that
baseline without recording account IDs, balances, keys, temporary URLs, or
other operational secrets.
It records the 2026-08-19 vendor refresh and its stricter task-routing rules.
Runtime defaults remain unchanged until the new Provider path completes real
preflight and candidate-only acceptance.

## Project Boundary

Ark media models are optional presentation helpers. They must not replace or
invent captured product UI.

Authoritative source of product behavior remains:

```text
ClientExecutionPackage
-> workflow_graph
-> executable_script_bundle
-> Playwright recording/screenshots
-> RecordingResultPackage
```

Allowed model usage:

- propose pacing, captions, transitions, camera-motion hints, color treatment,
  music direction, and non-authoritative preview candidates
- generate non-product visuals such as title cards or section dividers
- create short source-derived preview clips only from captured source material

Forbidden model usage:

- generate fake product UI screenshots
- replace captured browser footage as proof of product behavior
- invent browser actions, selectors, test data, or workflow steps
- modify required script order or source artifact references

## Runtime Configuration

Current environment variables:

| Purpose | Env |
| --- | --- |
| Ark mode gate | `CASCADE_ARK_MEDIA_MODE=dry_run|real|disabled` |
| Ark task polling attempts | `CASCADE_ARK_MEDIA_POLL_ATTEMPTS` |
| Ark task polling interval | `CASCADE_ARK_MEDIA_POLL_INTERVAL_MS` |
| Ark generated output directory | `CASCADE_ARK_MEDIA_OUTPUT_DIR` |
| Ark generated output download cap | `CASCADE_ARK_MEDIA_DOWNLOAD_MAX_BYTES` |
| Seedance key | `SEEDANCE_API_KEY` |
| Seedance fallback keys | `DOUBAO_API_KEY`, `ARK_API_KEY` |
| Seedance base URL | `SEEDANCE_BASE_URL` |
| Seedance model | `SEEDANCE_MODEL` |
| Seedream key | `SEEDREAM_API_KEY` |
| Seedream base URL | `SEEDREAM_BASE_URL` |
| Seedream model | `SEEDREAM_MODEL` |
| Static Ark input directory | `CASCADE_ARK_ASSET_PUBLIC_DIR` |
| Static Ark input base URL | `CASCADE_ARK_ASSET_PUBLIC_BASE_URL` |

Default base URL:

```text
https://ark.cn-beijing.volces.com/api/v3
```

Default models used by our dry-run plans:

```text
Seedance: doubao-seedance-2-0-260128
Seedream: doubao-seedream-5-0-pro-260628
```

Real calls must stay default-off until the source assets are published as
public HTTPS URLs or provider asset references and cost/rate controls are
explicitly enabled.

## Seedance Video Generation

本节保留 Seedance 2.0 的历史厂商参数基线，不能据此推导 2.5 参数。Server 当前开放边界与官方 2.5 请求合同以 [Seedance 2.5 FinalFilm 接入说明](seedance-2-5-video-generation.md) 为准。MiniMax-H3 的参数不得写入或推导自本节。

Endpoint:

```text
POST /contents/generations/tasks
GET  /contents/generations/tasks/{id}
```

The create API is asynchronous. A successful create response returns a task ID.
The caller must poll the get API, or use `callback_url`, until the task
succeeds. The final output is a downloadable MP4 URL.

### Recommended Project Usage

For the first real-call phase:

```json
{
  "model": "doubao-seedance-2-0-260128",
  "content": [
    {
      "type": "text",
      "text": "Use existing captured product material only. Produce a short non-authoritative director preview. Do not invent product UI."
    },
    {
      "type": "video_url",
      "video_url": {
        "url": "https://<asset-host>/ark-inputs/source_reference.mp4"
      },
      "role": "reference_video"
    }
  ],
  "resolution": "1080p",
  "ratio": "16:9",
  "duration": 5,
  "generate_audio": false,
  "return_last_frame": true,
  "watermark": false
}
```

Recommended defaults:

| Parameter | Initial value | Reason |
| --- | --- | --- |
| `model` | `doubao-seedance-2-0-260128` | Highest priority video model currently selected for our video-operation route. |
| `resolution` | `1080p` | Matches our current normalized reference target and avoids 4K/H.265 compatibility risk. |
| `ratio` | `16:9` | Matches enterprise demo output and current renderer target. |
| `duration` | `5` | Cheap, bounded preview segment. Longer final demos remain compositor-driven. |
| `generate_audio` | `false` | Keep audio deterministic until we implement music/voice review. |
| `return_last_frame` | `true` | Useful for chaining or section-transition planning. |
| `watermark` | `false` | Required for clean demo outputs. |
| `service_tier` | omit initially | Use default online mode first; evaluate `flex` later for cheaper offline batch jobs. |

### Seedance Control Parameters

The provider now recommends putting generation controls directly in the request
body. Avoid hiding these controls in prompt suffixes because body fields receive
stronger validation.

| Parameter | Project default | Notes |
| --- | --- | --- |
| `resolution` | `1080p` | Seedance 2.0 supports 480p, 720p, 1080p, and 4K. Fast/mini do not support 1080p. |
| `ratio` | `16:9` | Supported values include 16:9, 4:3, 1:1, 3:4, 9:16, 21:9, and adaptive. |
| `duration` | `5` for preview | Integer seconds. Seedance 2.0 supports 4-15s or `-1`. |
| `frames` | omit | Mutually overlaps with `duration`; provider gives `frames` higher priority. Prefer `duration` for now. |
| `seed` | omit | Add later only when deterministic comparison is required. |
| `camera_fixed` | omit | Add later for model-generated preview clips, not for captured UI footage. |
| `watermark` | `false` | Required for clean demo outputs. |
| `callback_url` | omit initially | Poll task status first; add callback only after we have a stable server endpoint. |
| `execution_expires_after` | provider default | Default is 48h. Use explicit timeout only for production queue control. |
| `priority` | `0` | Seedance 2.0 queue priority; 0-9. Keep default unless product queueing needs it. |
| `safety_identifier` | required for real calls | Use a stable hashed end-user or org identifier, max 64 chars; never send raw PII. |
| `tools.web_search` | disabled | Do not let the media model fetch external facts for product UI generation. |

Audio and draft behavior:

- `generate_audio=true` lets supported models generate mono synchronized audio.
  Keep it `false` until we implement user review for music, voice, and sound.
- `draft=true` is only for Seedance 1.5 Pro. It creates a lower-cost 480p draft
  and is not available for our initial Seedance 2.0 route.
- `return_last_frame=true` is useful when chaining model-generated candidate
  clips, but returned frames are still non-authoritative artifacts.

### Seedance Model IDs

| Model | Model ID | Duration | Resolution | Output |
| --- | --- | --- | --- | --- |
| Seedance 2.0 | `doubao-seedance-2-0-260128` | 4-15s, or `-1` | 480p, 720p, 1080p, 4K | MP4, 24fps |
| Seedance 2.0 fast | `doubao-seedance-2-0-fast-260128` | 4-15s, or `-1` | 480p, 720p | MP4, 24fps |
| Seedance 2.0 mini | `doubao-seedance-2-0-mini-260615` | 4-15s, or `-1` | 480p, 720p | MP4, 24fps |
| Seedance 1.5 pro | `doubao-seedance-1-5-pro-251215` | 4-12s, or `-1` | 480p, 720p, 1080p | MP4, 24fps |
| Seedance 1.0 pro | `doubao-seedance-1-0-pro-250528` | 2-12s | 480p, 720p, 1080p | MP4, 24fps |
| Seedance 1.0 pro fast | `doubao-seedance-1-0-pro-fast-251015` | 2-12s | 480p, 720p, 1080p | MP4, 24fps |

Seedance 2.0 16:9 output reference sizes:

| Resolution | Size |
| --- | --- |
| 480p | 864x496 |
| 720p | 1280x720 |
| 1080p | 1920x1080 |
| 4K | 3840x2160 |

Use 1080p first. 4K output may use H.265/10-bit encoding and can create
compatibility issues in players and downstream tooling.

### Seedance Input Content

Supported `content` item types:

| Type | Notes |
| --- | --- |
| `text` | Prompt. Chinese recommendation <= 500 chars; English <= 1000 words. |
| `image_url` | Image URL, Base64 data URL, or `asset://<ASSET_ID>`. |
| `video_url` | Seedance 2.0 only. Video URL or `asset://<ASSET_ID>`. |
| `audio_url` | Seedance 2.0 only. Must not be the only reference input. |
| `draft_task` | Draft task ID reuse. Mainly useful for Seedance 1.5 pro draft workflow. |

Image reference constraints:

- URL, Base64, or provider asset ID are supported.
- Image formats include jpeg, png, webp, bmp, tiff, gif; Seedance 1.5 Pro and
  Seedance 2.0 also support heic/heif.
- Single image should be less than 30 MB.
- Request body should be less than 64 MB.
- Avoid Base64 for large files.

Video reference constraints:

- Only Seedance 2.0 supports direct video reference input.
- Formats: mp4 or mov.
- MIME: `video/mp4` or `video/quicktime`.
- Video codec: H.264/AVC or H.265/HEVC.
- Audio codec: AAC or MP3.
- Each reference video: 2-15 seconds.
- Max reference videos: 3.
- Total reference video duration: <= 15 seconds.

Multimodal reference constraints:

- Reference images: 0-9.
- Reference videos: 0-3.
- Reference audio: 0-3.
- At least one image or video is required; audio cannot be the only input.
- First-frame, first-last-frame, and multimodal-reference modes are mutually
  exclusive. Do not mix these roles in one request.
- For exact first/last-frame control, prefer first-last-frame mode with
  `role=first_frame` and `role=last_frame`.

Output and persistence:

- Output video format is MP4.
- Video URL is valid for 24 hours and must be downloaded or transferred.
- Task records are kept for 7 days.
- Returned generated media is non-authoritative in our system and must be
  stored as a candidate artifact, not as product evidence.

### Parameters To Validate Before Real Calls

The code should block real Seedance calls unless all are true:

- `CASCADE_ARK_MEDIA_MODE=real`
- provider key is configured
- all selected source assets use `cn-beijing` private-TOS `ivolces.com` HTTPS
  GET presigned URLs, provider asset IDs, or small Base64 payloads within
  provider limits; source buckets must not be made public for model access
- source video is mp4/mov with accepted codecs
- source video references satisfy 2-15 seconds each and <= 15 seconds total
- requested `duration` is valid for the chosen model
- `resolution` is supported by the chosen model
- generated output will be downloaded within 24 hours
- output is marked non-authoritative and cannot replace captured UI evidence

## Seedream Image Generation

Endpoint:

```text
POST /images/generations
```

Seedream must be reserved for non-product visuals only in this project:

- title cards
- section dividers
- abstract brand visuals
- background plates that do not recreate product UI

It must not generate app screens, fake dashboards, fake text, fake forms, or
anything that can be confused with captured product behavior.

### Recommended Project Usage

For future non-product visual candidates:

```json
{
  "model": "doubao-seedream-5-0-pro-260628",
  "prompt": "Create a clean 16:9 enterprise demo title card inspired by the captured product's brand mood. Do not recreate product UI screens, do not add fake interface text.",
  "size": "2K",
  "response_format": "url",
  "output_format": "png",
  "watermark": false
}
```

Recommended defaults:

| Parameter | Initial value | Reason |
| --- | --- | --- |
| `model` | `doubao-seedream-5-0-pro-260628` | Strongest listed Seedream model for controlled high-quality image generation. |
| `size` | `2K` | Enough for 1080p video compositing without huge cost. |
| `response_format` | `url` | Easy download flow; must download within 24h. |
| `output_format` | `png` | Clean compositing format; supported by Seedream 5.0 models. |
| `watermark` | `false` | Required for clean demo outputs. |
| `sequential_image_generation` | `disabled` initially | We do not need group images for the first integration. |

### Seedream Model IDs

| Model | Model ID | Size options | Output formats | Notes |
| --- | --- | --- | --- | --- |
| Seedream 5.0 Pro | `doubao-seedream-5-0-pro-260628` | 1K, 1.5K, 2K | png, jpeg | Max 10 reference images; supports interaction editing and `layer_decomposition`; does not support sequential group images. |
| Seedream 5.0 Lite | `doubao-seedream-5-0-260128` or `doubao-seedream-5-0-lite-260128` | 2K, 3K, 4K | png, jpeg | Group images supported; input refs + generated images <= 15. |
| Seedream 4.5 | `doubao-seedream-4-5-251128` | 2K, 4K | jpeg | Max 14 reference images. |
| Seedream 4.0 | `doubao-seedream-4-0-250828` | 1K, 2K, 4K | jpeg | Supports fast prompt optimization mode. |

Image API request notes:

- `prompt` is required.
- Recommended prompt length: <= 300 Chinese chars or <= 600 English words.
- `image` can be a single URL/Base64 or an array of URLs/Base64 values.
- Supported input image formats include jpeg, png, webp, bmp, tiff, gif, heic,
  and heif.
- `response_format=url` links expire within 24 hours.
- `response_format=b64_json` returns image bytes inline.
- Seedream 5.0 Pro supports up to 10 reference images.
- Seedream 5.0 Lite, 4.5, and 4.0 support up to 14 reference images.
- For group generation, input references plus generated images must be <= 15.
- `layer_decomposition=true` is only available to 5.0 Pro: one PNG/JPEG input
  of at least 512x512, `size=auto`, up to 16 foreground layers plus a base
  image. It is for non-product candidate art only.
- `sequential_image_generation=auto` and
  `sequential_image_generation_options.max_images=1..15` are only available
  to 5.0 Lite, 4.5, and 4.0; never send them to 5.0 Pro.
- `tools=[{"type":"web_search"}]` is only available to 5.0 Lite. Streaming
  (`stream=true`) is supported by 5.0 Lite, 4.5, and 4.0, not by 5.0 Pro.
- Current account capability snapshot: the listed Seedream models are Public
  and support direct Model-ID calls. This confirms availability, not a
  completed project real-call acceptance.
- Private `cn-beijing` TOS inputs should use a one-hour HTTPS GET presigned
  `*.tos-cn-beijing.ivolces.com` URL. It lets Ark retrieve the image without
  making the source bucket public. The full policy is in the Seedance 2.5
  baseline and applies equally to the Seedream `image` field.

### Seedream Validation Details

Input image constraints:

- Accepted formats: jpeg, png, webp, bmp, tiff, gif, heic, heif.
- Each input image must be <= 30 MB.
- Aspect ratio must be within `[1/16, 16]`.
- Total pixels must be within 196 through 36,000,000.

Output size controls:

| Model | Named sizes | Explicit pixel constraints |
| --- | --- | --- |
| Seedream 5.0 Pro | 1K, 2K | Pixel product roughly 1280x720 through 2048x2048x1.1025; aspect ratio `[1/16, 16]`. |
| Seedream 5.0 Lite | 2K, 3K, 4K | Pixel product roughly 2560x1440 through 4096x4096; aspect ratio `[1/16, 16]`. |
| Seedream 4.5 | 2K, 4K | Pixel product roughly 2560x1440 through 4096x4096; aspect ratio `[1/16, 16]`. |
| Seedream 4.0 | 1K, 2K, 4K | Pixel product roughly 1280x720 through 4096x4096; aspect ratio `[1/16, 16]`. |

For enterprise demo compositing, prefer `size=2K`, `response_format=url`, and
`output_format=png` when available. Use `b64_json` only for small controlled
tests because it increases request/response payload pressure.

Generated image URLs expire after about 24 hours. On a successful candidate
result, download once to controlled storage, record a URL digest rather than
the full signed URL, probe the dimensions/format, compute SHA-256, and retain
the provider task/result metadata for audit. Image URLs have no documented
download-count cap; that must not be confused with Seedance video URLs.

### Seedream Request-Type Guards

The Server must validate model-specific fields before an API call:

| Use case | Required model/fields | Reject before provider call when |
| --- | --- | --- |
| Controlled single image / image edit | 5.0 Pro; up to 10 references; `size=1K|1.5K|2K` or a valid pixel size | group, stream, or web search fields are supplied |
| Sequential non-product concept images | 5.0 Lite, 4.5, or 4.0; `sequential_image_generation=auto`; `max_images=1..15` | reference-image count plus requested output exceeds 15 |
| Layer extraction | 5.0 Pro; exactly one PNG/JPEG reference at least 512x512; `layer_decomposition=true`; `size=auto` | input is not eligible, more than one reference is supplied, or the output is intended to recreate product UI |
| External-search illustration | 5.0 Lite only; `tools=[{"type":"web_search"}]` | any other Seedream model is selected |

Only successful images are billable. For group output, use
`usage.generated_images` rather than the requested count as the actual
success/charge/audit quantity. 5.0 Pro layer extraction is charged per
returned layer; all project cost estimates remain advisory until the provider
response is recorded.

## Mapping To Our Pipeline

Current implemented artifacts:

| Artifact | Current role |
| --- | --- |
| `source_reference.mp4` | Normalized local MP4/H.264/AAC reference video for future Seedance input. |
| `director_input.json` | Source-only planning input for director/model adapters. |
| `director_edit_suggestion.json` | DirectorAdapter output for narrative, shots, captions, style, and music direction, including `provider_gate` and optional `provider_call` task trace. |
| `director_edit_suggestion_validation.json` | Validation that model/director suggestions preserve user priority, source traces, and source-only policy. |
| `ark_media_generation_result.json` | Provider task/result normalization. Returned video/image URLs become non-authoritative candidate artifacts; successful downloads become durable local `downloaded_artifacts`; pending task ids remain pending. |
| `candidate_asset_review.json` | Conservative review artifact. Local non-authoritative video candidates can be marked `approved_for_demo=true`; generated candidates still remain presentation-only and opt-in. |
| `ark_media_dry_run_plan.json` | Machine-readable Seedance/Seedream request preview and constraints. |
| `ark_asset_publication_plan.json` | Dry-run plan for turning local files into model-accessible URLs. |
| `ark_asset_publication_result.json` | Asset publication result. Dry-run by default; static publisher can mark captured assets as real-call ready when a public HTTPS directory/base URL is configured. |
| `requirement_satisfaction_report.json` | Checks whether protocol requirements and captured assets were satisfied. |

Next implementation order:

1. Keep the default DirectorAdapter dry-run until real provider calls are
   explicitly enabled.
2. Configure and verify a server-side HTTPS static asset path or provider asset
   upload path for captured source materials.
3. Convert approved provider outputs into validated `DemoEditPlan` updates without
   overriding captured product UI.

Real-call blockers to resolve before enabling production use:

- Confirm whether the deployed server will call model IDs directly or Endpoint
  IDs managed in Ark.
- Provide a public HTTPS asset publication path, provider asset upload, or
  another model-accessible reference mechanism.
- Define where callback notifications should land if we move from polling to
  callback-based task completion.
- Add quota, timeout, retry, and cost guards around asynchronous video tasks.
- Persist generated URLs immediately because generated image/video URLs expire
  after 24 hours.

Important design decision:

Seedance/Seedream outputs should produce candidate artifacts or presentation
suggestions. They should not directly overwrite the deterministic
`DemoEditPlan` or final captured-product render without validation.
