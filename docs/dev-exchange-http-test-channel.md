# Dev Exchange HTTP Test Channel

This is a temporary dev-only channel for testing the customer-side agent to
cloud-side agent handoff over HTTP.

It exposes the existing client/cloud exchange service from
`backend/internal/app/exchange_intake.go`. It is not a production ingress.

## Enable

```powershell
$env:CASCADE_DEV_EXCHANGE_HTTP = "1"
$env:CASCADE_DEV_EXCHANGE_TOKEN = "replace-with-shared-test-token"

# Only when another machine must call this dev server directly:
$env:CASCADE_DEV_ALLOW_REMOTE_BIND = "1"
```

Start the dev server:

```powershell
cd D:\Engine-7-8\backend
go run .\cmd\devserver --addr 127.0.0.1:4317
```

For same-LAN testing, use an explicit host address only with the remote bind
switch enabled:

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
  "payload": {}
}
```

`payload` must be a `demoops.client_execution_package.v1` payload.

### Run

```http
POST /v1/dev/execution-packages/{exchange_package_id}/run
X-Cascade-Org-ID: <org_id>
```

This runs the uploaded package through the local Playwright recording/render
pipeline and stores the result in the in-memory exchange service.

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
  "updated_at": "2026-07-09T10:00:00Z"
}
```

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
    "pass_rate": 1,
    "step_count": 3,
    "passed_step_count": 3,
    "generated_asset_count": 8,
    "demo_video_count": 1,
    "screenshot_count": 3,
    "raw_recording_count": 1,
    "trace_count": 1,
    "primary_demo_video_uri": "D:\\Engine-7-8\\artifacts\\exchange\\xpkg_...\\render\\demo_30s.webm",
    "raw_recording_uri": "D:\\Engine-7-8\\artifacts\\exchange\\xpkg_...\\recording\\recording.webm"
  },
  "updated_at": "2026-07-09T10:01:00Z"
}
```

### Result

```http
GET /v1/result-packages/{result_package_id}
X-Cascade-Org-ID: <org_id>
```

### Ack

```http
POST /v1/result-packages/{result_package_id}/ack
X-Cascade-Org-ID: <org_id>
```

Body:

```json
{
  "acked_by_install_id": "customer-agent-dev"
}
```

## Notes

- The channel is in-memory. Restarting the dev server clears upload sessions,
  package status, and result packages.
- The run endpoint uses `NODE_WORKER_PATH` when configured, otherwise it falls
  back to `video-worker/dist/index.js` under the repo root.
- The renderer only uses existing recorded assets. It does not generate new
  images or video from prompts.
