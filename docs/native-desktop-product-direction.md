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
- Local repository path and GitHub repository URL are both optional parallel sources; providing one must not replace or disable the other.
- Run local demand understanding and project intelligence through the Go `Service` layer directly.
- Display the three approval artifacts in native preview panes:
  - `approval_markdown.md`
  - `stage_approval_plan.json`
  - `script_outline.json`
- Save the three-in-one package and full bundle into the local artifact directory.
- Use native desktop affordances for local paths and artifacts, including a folder picker for local repositories and an action to open the generated output directory.
- Provide a deliberate native visual hierarchy with desktop fonts, readable preview surfaces, and direct access to diagnostic logs.

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
  - native error dialog for blocking failures

## Implementation Direction

v1 uses dependency-free Go + Win32 APIs because the current machine has no `.NET` SDK and introducing a webview framework would violate the product boundary.

Future options can still be evaluated if the toolchain is added:

- WinUI 3 / WPF if `.NET` and Windows App SDK are available.
- C++/Qt only if a supported build/distribution toolchain is adopted.

Until then, `cmd/desktop` owns the native desktop surface.
