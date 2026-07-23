# Cascade DemoOps Native Desktop Direction

## Product Boundary

The desktop product must be a native application surface, not a browser shell around the existing web app.

Primary desktop UI:

- Windows: Win32 native window and controls, built into `cascade-demoops-desktop.exe`.
- The UI calls the Go `app.Service` directly for local understanding and package generation.
- The default launch path must not open a browser, WebView, Edge app mode, or local web URL.

Compatibility UI:

- The packaged React web app may remain as a fallback and smoke/debug surface.
- It must be launched only by explicit flags such as `--native=false`.
- Manifests must identify the native UI as the primary surface and the web host as compatibility-only.

## Native App Workflows

The first native workflow is the three-in-one package workbench:

- Collect product URL, requirement text, transient demo credentials, and any available code source.
- Show native input readiness before generation, covering URL, requirement text, optional code sources, and transient credential completeness.
- Show a native input preflight detail pane before generation, making required inputs, optional parallel code sources, transient credential boundaries, and the current generate gate visible without reading logs.
- Enable generation only after the required URL and requirement inputs are ready; optional source and credential inputs must not block local package generation.
- Persist non-sensitive input drafts locally so URL, code sources, and requirement text survive app restarts; demo credentials must remain transient and must not be saved.
- Provide a native reset action that clears non-sensitive input drafts and transient credential fields without touching generated artifacts.
- Import requirement Markdown/Text documents through the native file picker as a first-class input path.
- Local repository path and GitHub repository URL are both optional parallel sources; providing one must not replace or disable the other.
- Run local demand understanding and project intelligence through the Go `Service` layer directly.
- Display the three approval artifacts in native preview panes:
  - `approval_markdown.md`
  - `stage_approval_plan.json`
  - `script_outline.json`
- Provide a native approval review summary that condenses stages, browser-agent boundaries, bundle hash, and validation findings before users inspect full Markdown or JSON.
- Let users copy the currently previewed approval artifact through a native clipboard action for review handoff.
- Let users open the currently previewed approval artifact with the system default editor/viewer after ensuring the package files exist on disk.
- Let users record a native local approval decision after review; the app writes a non-sensitive `approval_record.json` beside the package so later upload flows can require an explicit approval artifact.
- Show a native server handoff readiness summary, making package file completeness and local approval record status visible before any upload/recording action.
- Show native server connection status in the workbench without exposing bearer tokens or session secrets, so users can distinguish local generation from server upload/recording readiness.
- Let users start server handoff for an already approved package from the native workbench, covering exchange init and package upload.
- Write a non-sensitive `server_handoff_record.json` beside the approved package after upload so users can resume server-stage tracking after app restart or package import.
- Let users manually query server execution status from the native workbench, showing status, stage, progress, result package ID, recent stage history, and failure summary without exposing bearer tokens or credentials.
- Let users fetch the server `RecordingResultPackage` into `server_result_package.json` and a compact `server_result_record.json` once the server exposes a result package ID.
- Let users download server deliverables into `server_deliverables/`, write a local `manifest.json`, and verify SHA-256 checksums before acknowledging delivery.
- Let users open the downloaded deliverables folder and the primary downloaded artifact directly from the native workbench.
- Surface server delivery health as native summary indicators for upload/status, result package, artifact checksum verification, and ACK state.
- Let users explicitly acknowledge result delivery from the native workbench, writing `server_result_ack.json`; ACK prefers locally downloaded and checksum-verified artifact IDs, with metadata-only confirmation retained for compatibility.
- Save the three-in-one package and full bundle into the local artifact directory.
- Import an existing three-in-one package folder back into the native workbench so users can resume review, copy artifacts, open folders, and record approval after an app restart.
- Export the generated three-in-one package to a user-selected folder through native folder selection.
- Keep a native recent-package list that stores only non-sensitive package metadata and output paths, so users can reopen recent approval artifacts without searching logs or folders.
- Show a native summary for the selected recent package, including runtime, stage count, update time, output path, and whether the expected approval files are present.
- Use native desktop affordances for local paths and artifacts, including a folder picker for local repositories and an action to open the generated output directory.
- Provide a deliberate native visual hierarchy with desktop fonts, readable preview surfaces, and direct access to diagnostic logs.
- Expose primary workbench commands through a native menu bar, not only through in-window buttons.
- Support native keyboard accelerators for frequent review and package actions, including generation, save/export, preview switching, copy, folder selection, requirement import, and diagnostics.
- Use native confirmation dialogs for actions that clear drafts or overwrite exported approval artifacts.
- Show the local generation lifecycle as native phase indicators so users can understand progress without reading raw logs.
- Surface approval package health as native summary indicators for runtime, stage coverage, bundle size, and validation state before the user opens detailed JSON.
- Surface a concise package gate summary after generation, including stage/outline alignment, bundle size class, local approval requirement, and secret boundary.

## Design Principles

- Dense, professional desktop console layout, not a marketing page.
- Native controls are acceptable, but the shell must provide clear hierarchy:
  - local engine status
  - input workspace
  - generation lifecycle
  - approval previews
  - save/export actions
- The app must remain diagnosable without a terminal:
  - launcher log
  - visible status feed
  - native environment status bar for data, log, and output locations
  - native error dialog for blocking failures

## Implementation Direction

v1 uses dependency-free Go + Win32 APIs because the current machine has no `.NET` SDK and introducing a webview framework would violate the product boundary.

Future options can still be evaluated if the toolchain is added:

- WinUI 3 / WPF if `.NET` and Windows App SDK are available.
- C++/Qt only if a supported build/distribution toolchain is adopted.

Until then, `cmd/desktop` owns the native desktop surface.
