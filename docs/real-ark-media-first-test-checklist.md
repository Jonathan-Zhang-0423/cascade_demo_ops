# Seedance Real Media First-Test Checklist

Status: preparation only. This checklist does not authorize a real provider
call. Do not put keys, passwords, cookies, tokens, or full source code in this
file, an execution package, or a commit.

## Goal and Boundary

The first test verifies this constrained path:

```text
App execution package -> Server recording -> selected captured assets
-> private TOS upload -> short-lived signed HTTPS references -> Seedance candidate -> review
-> validated edit-plan patch -> FFmpeg MP4 render
```

The captured product recording remains the authoritative proof of business
actions. A generated candidate may only be used as a reviewed presentation
element such as a title card, divider, transition, or outro. It cannot replace
an application recording or satisfy a required execution step.

## Manual Items Required From the Operator

1. In Volcengine Ark, confirm that the account has billing/quota and access to
   `doubao-seedance-2-0-260128`. Use the exact model ID enabled for the account
   if it differs.
2. Create a dedicated, least-privilege Ark API key for this test. Keep it in a
   local environment variable or secret manager only. Do not send it in chat.
3. Configure the private TOS bucket credentials and `ark-media/` prefix used
   for only the selected captured test artifacts. The Server uploads those
   files and generates short-lived signed HTTPS URLs; no public bucket, CORS,
   custom domain, or IP allowlist is required.
4. Confirm the test material contains no secrets, personal data, or pages that
   the product policy forbids exporting to the model provider.
5. Review the generated candidate before it is allowed into the edit plan. The
   first test must retain `AutoInclude=false`.

## Local Configuration Template

Set these in the PowerShell window that starts the Server. Substitute values
locally; do not save a real key into `.env.example` or Git.

```powershell
$env:CASCADE_LLM_MODE = "deterministic" # Keep App planning local for this test.
$env:CASCADE_ARK_MEDIA_MODE = "real"
$env:SEEDANCE_API_KEY = "<set-locally-not-in-chat>"
$env:SEEDANCE_BASE_URL = "https://ark.cn-beijing.volces.com/api/v3"
$env:SEEDANCE_MODEL = "doubao-seedance-2-0-260128"

# Private TOS. This local Server runs outside the Volcengine VPC, so it must
# use the public endpoint. The bucket itself remains private and the provider
# receives only a short-lived signed GET URL.
$env:VOLC_TOS_ACCESS_KEY = "<set-locally-not-in-chat>"
$env:VOLC_TOS_SECRET_KEY = "<set-locally-not-in-chat>"
$env:VOLC_TOS_ENDPOINT = "tos-cn-beijing.volces.com"
$env:VOLC_TOS_REGION = "cn-beijing"
$env:VOLC_TOS_BUCKET = "<your-private-bucket>"
$env:VOLC_TOS_PREFIX = "ark-media/"
$env:VOLC_TOS_SIGNED_URL_TTL_SEC = "3600"
$env:CASCADE_ARK_MEDIA_POLL_ATTEMPTS = "20"
$env:CASCADE_ARK_MEDIA_POLL_INTERVAL_MS = "3000"
```

`CASCADE_LLM_MODE` and `CASCADE_ARK_MEDIA_MODE` are independent. The first
controls planning/understanding LLM calls; the second alone enables Seedance
media calls after all source-asset gates pass. Keeping the first deterministic
limits the first external test to the intended media call.

## TOS IAM Policy For This Test Bucket

Bind this custom policy to the dedicated uploader user only. It limits object
operations to the `ark-media/` prefix in `cascade-ark-media-test`; do not bind
an administrator or all-buckets policy.

```json
{
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "tos:PutObject",
        "tos:GetObject",
        "tos:DeleteObject",
        "tos:AbortMultipartUpload",
        "tos:ListMultipartUploadParts"
      ],
      "Resource": [
        "trn:tos:::cascade-ark-media-test",
        "trn:tos:::cascade-ark-media-test/ark-media/*"
      ]
    },
    {
      "Effect": "Allow",
      "Action": ["tos:ListBucket"],
      "Resource": ["trn:tos:::cascade-ark-media-test"],
      "Condition": {"StringLike": {"tos:prefix": ["ark-media/*"]}}
    }
  ]
}
```

In the TOS console, create a lifecycle rule named `delete-ark-media-1d` for
prefix `ark-media/`, with deletion after one day. This rule must not target the
entire bucket.

## Preconditions the Server Verifies

Before sending a request, the Server must report all of the following:

- `CASCADE_ARK_MEDIA_MODE=real`;
- Seedance key is configured, without exposing its value;
- captured references are supported media and have provider-reachable signed
  TOS HTTPS URLs, not `D:\...` paths or simulated URLs;
- publication result says `can_use_for_real_call=true`;
- the input material passed the project redaction and policy checks.

The observable gate is `director_edit_suggestion.json.provider_gate`. A valid
first submission changes it to `provider_call_submitted`; a failed provider
request changes it to `provider_call_failed` with a redacted error class.

## Acceptance Evidence

Keep the following non-sensitive artifacts for review:

- `director_input.json` and `director_edit_suggestion.json`;
- `ark_media_dry_run_plan.json` and `ark_asset_publication_result.json`;
- `ark_media_generation_result.json` with task/status summaries;
- `candidate_asset_review.json` and edit-plan patch validation result;
- final `demo_edit_plan.json`, MP4 checksum, and Stage/StepResult traces.

Do not retain provider Authorization headers, raw API responses containing
secrets, Cookies, passwords, or public asset URLs beyond their intended expiry.

## Recommended First Test Scope

Use one completed, non-sensitive recorded flow and request one 5-second,
16:9, 1080p presentation-only candidate. Do not enable automatic inclusion.
Validate that the source-only MP4 is still rendered successfully when the
provider is slow, rejects the request, or returns no downloadable candidate.

For the initial Seedance reference, keep each MP4/MOV to 2-15 seconds,
480P/720P/1080P, H.264/H.265, no more than 50 MB, and no more than three
videos with a combined duration of 15 seconds. Configure a TOS lifecycle rule
to delete only `ark-media/` objects after one day.
