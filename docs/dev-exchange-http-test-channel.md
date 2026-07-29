# Dev Exchange HTTP Test Channel

This is a temporary dev-only channel for testing the client execution package
producer to cloud-side AIGC executor handoff over HTTP.

It exposes the existing client/cloud exchange service from
`backend/internal/app/exchange_intake.go`. It is not a production ingress.

## Enable

```powershell
$env:CASCADE_DEV_EXCHANGE_HTTP = "1"
$env:CASCADE_DEV_EXCHANGE_TOKEN = "replace-with-shared-test-token"

# Only when another machine must call this dev server directly:
$env:CASCADE_DEV_ALLOW_REMOTE_BIND = "1"

# Optional: start execution automatically after a valid plaintext upload:
$env:CASCADE_EXCHANGE_AUTO_RUN = "1"
```

Start the dev server:

```powershell
cd D:\Engine-7-8\backend
go run .\cmd\devserver --addr 127.0.0.1:4317
```

For server-based remote testing, run this on the cloud host and bind to all
interfaces only with the remote bind switch enabled:

```powershell
go run .\cmd\devserver --addr 0.0.0.0:4317
```

All exchange requests must include:

```text
Authorization: Bearer <CASCADE_DEV_EXCHANGE_TOKEN>
Content-Type: application/json
```

Requests after upload also need one of:

```text
X-Cascade-Org-ID: <org_id>
```

or:

```text
?org_id=<org_id>
```

## Endpoints

### Init

```http
POST /v1/execution-packages/init
```

Body:

```json
{
  "org_id": "org_1",
  "project_id": "project_1",
  "package_kind": "client_execution",
  "producer": {
    "app_version": "customer-agent-dev",
    "runtime_profile": "server-agent"
  }
}
```

Response includes `upload_id`.

### Upload

```http
POST /v1/execution-packages
```

Body:

```json
{
  "upload_id": "<upload_id from init>",
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

Production uploads use `payload_ref` only. The ref must match
`envelope.payload_ref`; the service stores only envelope and artifact metadata.
The dev channel also accepts a plaintext `payload` field for local end-to-end
tests, where `payload` must be a `demoops.client_execution_package.v1` payload.
When `CASCADE_EXCHANGE_AUTO_RUN=1`, a valid plaintext upload is immediately
started with the same cloud-side runner used by the dev `/run` endpoint. The
upload response status becomes `running` when the start succeeds. Encrypted
`payload_ref`-only uploads remain `accepted` until an isolated decryption worker
is available; they are not auto-started by this dev bridge.

If upload validation fails, the response uses a stable error code plus safe
field-level details. The server does not echo the script source, HTML, or
secret values.

```json
{
  "error": {
    "code": "client_execution_package_invalid",
    "message": "recording_run_spec.allowed_domains is required",
    "details": [
      {
        "field": "payload.recording_run_spec.allowed_domains",
        "reason": "required",
        "message": "recording_run_spec.allowed_domains is required",
        "hint": "Include the product domain in recording_run_spec.allowed_domains and keep script domains within that list."
      }
    ]
  }
}
```

### Run

```http
POST /v1/dev/execution-packages/{exchange_package_id}/run
X-Cascade-Org-ID: <org_id>
```

This starts the uploaded package in the local Playwright recording/render
pipeline and returns quickly with the current execution status. Poll `status`
or the dev list endpoints until the package reaches `completed` or `failed`.
This endpoint is still available when `CASCADE_EXCHANGE_AUTO_RUN=1`; duplicate
starts return the current package status instead of launching a second run.
The background execution window is taken from
`envelope.policy.max_execution_window_sec`; when the field is absent or not
positive, the dev server uses a 30 minute default. If the window expires, the
package status becomes `failed` with error code `execution_timeout`.

### Cancel

```http
POST /v1/dev/execution-packages/{exchange_package_id}/cancel
X-Cascade-Org-ID: <org_id>
```

This dev-only control endpoint cancels a cloud-side run that is still in
progress and returns the current status. Canceled packages remain queryable via
`status`, `debug`, and the dev list endpoints with status `canceled` and error
code `canceled_by_dev_request`. Completed, failed, expired, or already canceled
packages are terminal and are returned without being overwritten.

### Debug

```http
GET /v1/dev/execution-packages/{exchange_package_id}/debug
X-Cascade-Org-ID: <org_id>
```

This dev-only view returns a redacted package summary, runtime readiness, stage
history, result summary, and failure summary. It does not return the full
payload or executable script source.

Useful fields:

```json
{
  "runtime": {
    "video_worker_ready": true,
    "node_ready": true,
    "ffmpeg_ready": false
  },
  "readiness": {
    "can_run": false,
    "blockers": [
      {
        "code": "video_worker_missing",
        "message": "Video-worker build artifact is not configured or not present."
      }
    ]
  },
  "package": {
    "package_id": "pkg_...",
    "base_url": "https://product.example.com",
    "allowed_domains": ["product.example.com"],
    "workflow_node_count": 3,
    "script_step_count": 3
  },
  "failure": {
    "code": "video_worker_missing",
    "failed_stage": "preparing_worker"
  }
}
```

### List Executions

```http
GET /v1/dev/execution-packages
X-Cascade-Org-ID: <org_id>
```

Returns a lightweight index of uploaded execution packages for the org. This is
for dev联调 and restart recovery: use it to find `exchange_package_id`,
`cloud_job_id`, current status, stage, result id, and summary fields. It does
not return the full package payload or executable script source.

Example:

```bash
curl \
  -H "Authorization: Bearer cascade-dev-20260710" \
  -H "X-Cascade-Org-ID: org_devsmoke" \
  http://127.0.0.1:4317/v1/dev/execution-packages
```

### List Results

```http
GET /v1/dev/result-packages
X-Cascade-Org-ID: <org_id>
```

Returns a lightweight index of generated result packages for the org. Result
items include `result_package_id`, source execution identity, status,
`result_summary`, `failure_summary` when present, and dev `download_url` values
for local deliverables. Result items also expose `delivery_status`,
`delivered_at`, `acked_at`, and `acked_by_install_id` when the result package
has been downloaded or acknowledged.

Example:

```bash
curl \
  -H "Authorization: Bearer cascade-dev-20260710" \
  -H "X-Cascade-Org-ID: org_devsmoke" \
  http://127.0.0.1:4317/v1/dev/result-packages
```

### Status

```http
GET /v1/execution-packages/{exchange_package_id}/status
X-Cascade-Org-ID: <org_id>
```

Typical response after upload:

```json
{
  "exchange_package_id": "xpkg_...",
  "cloud_job_id": "job_...",
  "status": "accepted",
  "stage": "accepted",
  "message": "Execution package accepted. Call the dev run endpoint to start recording and rendering.",
  "progress_percent": 10,
  "stage_history": [
    {
      "stage": "accepted",
      "status": "accepted",
      "progress_percent": 10
    }
  ],
  "updated_at": "2026-07-09T10:00:00Z"
}
```

Run stages are:

```text
accepted -> validating -> preparing_worker -> browser_agent_planning -> script_ready -> recording -> material_validation -> directing -> rendering -> quality_validation -> completed
```

On failure the status becomes `failed`, and `failure_summary.failed_stage`
shows the stage that failed. When the dev cancel endpoint is used before
completion, the status becomes `canceled`.

Typical response after a successful dev run:

```json
{
  "exchange_package_id": "xpkg_...",
  "cloud_job_id": "job_...",
  "status": "completed",
  "stage": "completed",
  "message": "Recording and rendering completed. Result package is ready.",
  "progress_percent": 100,
  "result_package_id": "result_pkg_...",
  "result_summary": {
    "result_id": "result_pkg_1",
    "result_status": "generated",
    "delivery_status": "ready",
    "pass_rate": 1,
    "step_count": 3,
    "passed_step_count": 3,
    "generated_asset_count": 8,
    "demo_video_count": 1,
    "screenshot_count": 3,
    "raw_recording_count": 1,
    "trace_count": 1,
    "primary_demo_video_uri": "D:\\Engine-7-8\\artifacts\\exchange\\xpkg_...\\render\\demo_30s.webm",
    "raw_recording_uri": "D:\\Engine-7-8\\artifacts\\exchange\\xpkg_...\\recording\\recording.webm",
    "ack_required": true,
    "expires_at": "2026-07-10T10:01:00Z",
    "deliverables": [
      {
        "kind": "demo_video",
        "role": "final_demo",
        "uri": "D:\\Engine-7-8\\artifacts\\exchange\\xpkg_...\\render\\demo_30s.webm",
        "download_url": "/v1/dev/result-packages/result_pkg_.../deliverables/artifact_pkg_demo_video_001",
        "sha256": "<file sha256>",
        "size_bytes": 73648,
        "include_in_demo": true
      },
      {
        "kind": "raw_recording",
        "role": "raw_recording",
        "uri": "file:///D:/Engine-7-8/artifacts/exchange/xpkg_.../recording/page.webm",
        "download_url": "/v1/dev/result-packages/result_pkg_.../deliverables/artifact_raw_recording",
        "sha256": "<file sha256>",
        "size_bytes": 1048576,
        "include_in_demo": true
      },
      {
        "kind": "browser_trace",
        "role": "debug_trace",
        "uri": "file:///D:/Engine-7-8/artifacts/exchange/xpkg_.../recording/trace.zip",
        "download_url": "/v1/dev/result-packages/result_pkg_.../deliverables/artifact_browser_trace"
      }
    ]
  },
  "updated_at": "2026-07-09T10:01:00Z"
}
```

### Result

```http
GET /v1/result-packages/{result_package_id}
X-Cascade-Org-ID: <org_id>
```

Calling this endpoint marks the result package as delivered. Later status and
list responses should show `result_summary.delivery_status="delivered"` and a
non-empty `delivered_at`.

### Ack

```http
POST /v1/result-packages/{result_package_id}/ack
X-Cascade-Org-ID: <org_id>
```

Body:

```json
{
  "acked_by_install_id": "customer-agent-dev",
  "received_asset_ids": ["artifact_pkg_1_demo_video_001"],
  "verified_checksums": true
}
```

`verified_checksums=true` is required. If the App detects a mismatch it must
send `checksum_mismatch_ids` and the server will reject the ack.

Successful ack stores `acked_by_install_id`, `received_asset_ids`,
`verified_checksums`, and `acked_at`. Later status and list responses should
show `result_summary.result_status="acked"` and
`result_summary.delivery_status="acked"`.

## Server Smoke

After the server pulls this branch and starts the dev bridge, use `cmd/devsmoke`
to verify the full cloud-side handoff without manually crafting protocol JSON.

Start the server:

```bash
cd /path/to/cascade_demo_ops/backend
export CASCADE_DEV_EXCHANGE_HTTP=1
export CASCADE_DEV_EXCHANGE_TOKEN=cascade-dev-20260710
export CASCADE_DEV_ALLOW_REMOTE_BIND=1
export CASCADE_EXCHANGE_AUTO_RUN=1
export CASCADE_LLM_MODE=deterministic
go run ./cmd/devserver --addr 0.0.0.0:4317
```

In another shell on the same server:

```bash
cd /path/to/cascade_demo_ops/backend
go run ./cmd/devsmoke \
  --base-url http://127.0.0.1:4317 \
  --token cascade-dev-20260710 \
  --mode success
```

Expected success result:

```json
{
  "ok": true,
  "mode": "success",
  "status": "completed",
  "stage": "completed",
  "list_check": {
    "execution_found": true,
    "result_found": true
  },
  "result": {
    "pass_rate": 1,
    "demo_video_count": 1,
    "raw_recording_count": 1,
    "trace_count": 1,
    "deliverables": [
      {
        "kind": "demo_video",
        "role": "final_demo",
        "uri": "...",
        "download_url": "/v1/dev/result-packages/result_pkg_.../deliverables/artifact_pkg_demo_video_001"
      }
    ]
  }
}
```

Download a deliverable:

```bash
curl -L \
  -H "Authorization: Bearer cascade-dev-20260710" \
  -H "X-Cascade-Org-ID: org_devsmoke" \
  -o demo.webm \
  http://127.0.0.1:4317/v1/dev/result-packages/result_pkg_.../deliverables/artifact_pkg_demo_video_001
```

The dev download endpoint only serves files resolved under the configured
`CASCADE_ARTIFACT_ROOT`. It rejects non-local URIs and paths outside the
artifact root.

Restart recovery smoke:

```bash
# 1. Run a normal success smoke and copy exchange_package_id from the output.
go run ./cmd/devsmoke \
  --base-url http://127.0.0.1:4317 \
  --token cascade-dev-20260710 \
  --mode success

# 2. Restart cmd/devserver.

# 3. Verify the persisted status/result/deliverable mapping.
go run ./cmd/devsmoke \
  --base-url http://127.0.0.1:4317 \
  --token cascade-dev-20260710 \
  --mode success \
  --reuse-package-id xpkg_...
```

Run the diagnostic path:

```bash
go run ./cmd/devsmoke \
  --base-url http://127.0.0.1:4317 \
  --token cascade-dev-20260710 \
  --mode failure
```

Expected failure result:

```json
{
  "ok": true,
  "mode": "failure",
  "status": "failed",
  "failure": {
    "failed_node_id": "node_missing_selector",
    "failure_screenshot_uri": "file:///...",
    "failure_trace_uri": "file:///..."
  }
}
```

Run a one-command package-file replay smoke:

```bash
go run ./cmd/devsmoke \
  --base-url http://127.0.0.1:4317 \
  --token cascade-dev-20260710 \
  --package-file-fixture \
  --sample-output-dir "../.cascade-dev/samples/package-replay-{id}" \
  --timeout 240s
```

This command first writes a real plaintext `execution-package.fixture.json` into
the sample directory, then replays that file through the same `--package-file`
upload path used for upstream handoff tests. It exercises package JSON loading,
HTTP intake, script execution, screenshot/recording capture, render output,
result package lookup, deliverable download, and result ack in one run.

Replay a real upstream package file:

```bash
go run ./cmd/devsmoke \
  --base-url http://127.0.0.1:4317 \
  --token cascade-dev-20260710 \
  --package-file ./execution-package.json \
  --timeout 240s
```

`--package-file` accepts either a full upload body:

```json
{
  "envelope": {},
  "payload_ref": {},
  "payload": {}
}
```

or a raw `demoops.client_execution_package.v1` payload. When the file is a raw
payload, `cmd/devsmoke` creates a dev envelope for replay. If the file only
contains encrypted `payload_ref` metadata and no plaintext `payload`, the server
will accept the upload but `cmd/devsmoke` will not wait for execution because
the dev bridge has no plaintext script to run.

For raw payload files that do not include enough identity metadata, pass:

```bash
go run ./cmd/devsmoke \
  --base-url http://127.0.0.1:4317 \
  --token cascade-dev-20260710 \
  --package-file ./execution-package.json \
  --org-id org_1 \
  --project-id project_1
```

## Notes

- The dev server persists exchange package status, result packages, and
  deliverable mappings under `.cascade-dev/data/exchange_state`. Restarting the
  dev server should preserve completed results and downloadable local artifacts.
- Restarting the dev server marks persisted `running` or `queued` packages as
  `failed` with error code `interrupted_by_restart`, preserving the last known
  execution stage in `failure_summary.failed_stage`.
- Restarting the dev server clears unfinished upload sessions only when they
  were not saved before a valid package upload.
- The run endpoint uses `NODE_WORKER_PATH` when configured, otherwise it falls
  back to `video-worker/dist/index.js` under the repo root.
- The renderer only uses existing recorded assets. It does not generate new
  images or video from prompts.
