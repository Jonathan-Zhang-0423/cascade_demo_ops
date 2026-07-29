# Real Page Visible Acceptance (Local Dev/Test Only)

This tool is **dev/test only**. It is not part of the production Browser Agent
runtime, Exchange API, App-to-Server protocol, or cloud execution path.

Its purpose is to prove that a human can manually authenticate an isolated,
visible browser on a real local product page before an approved package is run.
It never imports the user's existing browser profile, Cookie, local storage,
password, Token, API key, email, or phone number.

## Safety boundary

- Available only when Server runs with `ProfileDev` and a non-production environment.
- Requires an explicit `dev_test_ack: true` request field.
- Accepts only `http://127.0.0.1:<port>/...`, without query, fragment, or URL credentials.
- Opens Chromium with a new isolated context; it does not reuse the user's normal browser.
- Disables video recording and Playwright Trace during manual login. The status endpoint returns only sanitized URL and title.
- The requested target origin is checked again after manual login. A mismatch returns `source_mismatch`; no business action is performed. A same-origin post-login redirect is allowed because real products often route from `/login` to a workspace page.
- A session expires after 20 minutes and can be explicitly aborted. It is never a production session.

## Local workflow

Start the real local product first, for example `http://127.0.0.1:5000/app`,
then start the Engine dev bridge with its normal local-only configuration.

Open the isolated visible browser:

```powershell
$body = @{ target_url = "http://127.0.0.1:5000/app"; dev_test_ack = $true } | ConvertTo-Json
Invoke-RestMethod -Method Post `
  -Uri "http://127.0.0.1:4317/v1/desktop/dev-visible-browser-agent/prepare" `
  -ContentType "application/json" -Body $body
```

If the response is `awaiting_manual_login`, log in **only in the newly opened
isolated Chromium window**, then verify the handoff:

```powershell
Invoke-RestMethod -Method Post `
  -Uri "http://127.0.0.1:4317/v1/desktop/dev-visible-browser-agent/<session_id>/continue"
```

For a read-only status refresh (for example, a local developer dashboard), use:

```powershell
Invoke-RestMethod `
  -Uri "http://127.0.0.1:4317/v1/desktop/dev-visible-browser-agent/<session_id>"
```

Only `ready_for_approved_package` proves the browser is on the requested real
local page. It does not claim that an App execution package was generated,
approved, run, recorded, or rendered. Close it afterwards:

```powershell
Invoke-RestMethod -Method Post `
  -Uri "http://127.0.0.1:4317/v1/desktop/dev-visible-browser-agent/<session_id>/abort"
```

## Approved-package preflight

Before a future visible execution handoff, the following local endpoint may
check an existing App package without storing it or performing a browser
action. The package must already pass the normal protocol validation, have App
human approval metadata, use `browser-agent-outline-v1`, and declare exactly
the visible browser's origin in `base_url`, `product_url`, and
`allowed_origins`.

```powershell
$body = @{ dev_test_ack = $true; package = $approvedPackage } | ConvertTo-Json -Depth 100
Invoke-RestMethod -Method Post `
  -Uri "http://127.0.0.1:4317/v1/desktop/dev-visible-browser-agent/<session_id>/bind-approved-package" `
  -ContentType "application/json" -Body $body
```

This responds with a package ID, runtime, and stage count only. It is a
compatibility preflight, **not execution**. The response cannot be used to
bypass Exchange Intake, package approval, the Runtime Router, or the Outcome
Verifier.

### Fixed real-product test package

If the normal App package-generation path is under diagnosis, local developers
may build one **fixed dev/test-only** package for the explicitly approved
Cascade local-product acceptance. It contains only: open New Project, fill
`贪吃蛇游戏`, and submit Create Project. It is marked
`not_for_exchange_upload`; it is not App-generated and must never be uploaded
to Exchange or used in production.

```powershell
$body = @{ target_url = "http://127.0.0.1:5000/app"; dev_test_ack = $true } | ConvertTo-Json
$approvedPackage = (Invoke-RestMethod -Method Post `
  -Uri "http://127.0.0.1:4317/v1/desktop/dev-visible-browser-agent/approved-package" `
  -ContentType "application/json" -Body $body).data.package
```

Use the returned object only with the visible-session `bind-approved-package`
and `execute-approved-package` endpoints in this document.

## Execute and render

Only after the preflight passes, execute the same approved package in the same
visible, manually authenticated browser session:

```powershell
$body = @{ dev_test_ack = $true; package = $approvedPackage } | ConvertTo-Json -Depth 100
Invoke-RestMethod -Method Post `
  -Uri "http://127.0.0.1:4317/v1/desktop/dev-visible-browser-agent/<session_id>/execute-approved-package" `
  -ContentType "application/json" -Body $body
```

This endpoint repeats protocol and origin validation, applies the approved
policy, executes stages with the existing Outcome Verifier, and closes the
authenticated browser even if execution fails. The resulting MP4, when the
package requests `final_video`, is composed from post-action masked screenshots
only. It never includes the manual login journey or a raw login recording.

The next phase may reuse this design only through a separately implemented,
protocol-validated and human-approved package execution handoff. It must not
be merged into `localBrowserAgentOutlineRunner.Run`.
