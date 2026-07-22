# Clueso UI Capture Procedure

This procedure captures user-visible UI design information from a user-owned,
already authenticated Clueso browser session. It is for design research only.
It does not capture network traffic, cookies, tokens, form values, transcript
text, media URLs, browser storage, or proprietary source bundles.

## Prerequisites

- Node.js 22 or newer. The repository's current Node.js installation satisfies
  this requirement.
- Google Chrome on the local machine.
- A Clueso account session that the user is authorized to access.
- The repository at `D:\Engine-7-8`.

## Start A Local Capture Browser

Open **PowerShell**, not the browser console, and run:

```powershell
$chrome = "${env:ProgramFiles}\Google\Chrome\Application\chrome.exe"
& $chrome --remote-debugging-port=9222 --user-data-dir="${env:LOCALAPPDATA}\CascadeCluesoCapture" --new-window "https://web.clueso.io/"
```

The dedicated user-data directory avoids attaching to the normal daily-use
Chrome profile. Log in manually in this new browser window. Do not expose port
`9222` outside the local machine.

Use the desktop editor at a normal desktop width. Do not enable device emulation
in DevTools; Clueso presents a narrow-viewport fallback instead of its editor.

## Capture A State

1. In the capture browser, navigate to the desired editor state.
2. Do not enter sensitive transcript text or upload sensitive material.
3. Open a second PowerShell window.
4. Run the following from the repository root:

```powershell
cd D:\Engine-7-8
node .\scripts\capture-clueso-ui.mjs --name default
```

The output is written to the gitignored path:

```text
D:\Engine-7-8\artifacts\clueso-ui\default\
  manifest.json
  ui-capture.json
  viewport.png
```

Capture each manually opened state separately. Suggested names:

```powershell
node .\scripts\capture-clueso-ui.mjs --name tools-shapes-open
node .\scripts\capture-clueso-ui.mjs --name selected-video
node .\scripts\capture-clueso-ui.mjs --name selected-caption
node .\scripts\capture-clueso-ui.mjs --name selected-effect
node .\scripts\capture-clueso-ui.mjs --name timeline-selected-clip
```

The script attaches only to a page at `https://web.clueso.io/`. It will fail
instead of selecting an unrelated browser tab.

## Captured Fields

`ui-capture.json` contains only:

- viewport dimensions and device pixel ratio;
- section geometry and selected computed styles;
- visible control tag, role, accessible label, UI state attributes, class name,
  geometry, and selected computed styles;
- design token values whose names begin with `--color-`, `--spacing-`,
  `--radius-`, `--text-`, `--shadow-`, or `--font-`.

It deliberately excludes arbitrary text content, input values, network data,
cookies, storage, and URLs containing project-specific data.

## Stop

When capture is finished, close the dedicated Chrome window. The local CDP port
stops with the browser process.
