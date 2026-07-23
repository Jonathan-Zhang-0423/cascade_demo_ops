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
- Let users copy the currently previewed approval artifact through a native clipboard action for review handoff.
- Save the three-in-one package and full bundle into the local artifact directory.
- Export the generated three-in-one package to a user-selected folder through native folder selection.
- Use native desktop affordances for local paths and artifacts, including a folder picker for local repositories and an action to open the generated output directory.
- Provide a deliberate native visual hierarchy with desktop fonts, readable preview surfaces, and direct access to diagnostic logs.
- Expose primary workbench commands through a native menu bar, not only through in-window buttons.
- Support native keyboard accelerators for frequent review and package actions, including generation, save/export, preview switching, copy, folder selection, requirement import, and diagnostics.
- Use native confirmation dialogs for actions that clear drafts or overwrite exported approval artifacts.
- Show the local generation lifecycle as native phase indicators so users can understand progress without reading raw logs.
- Surface approval package health as native summary indicators for runtime, stage coverage, bundle size, and validation state before the user opens detailed JSON.

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
