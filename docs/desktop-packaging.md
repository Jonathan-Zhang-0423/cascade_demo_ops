# Desktop Packaging Architecture

Cascade DemoOps desktop packaging is prepared around a Wails shell with a shared
Go application service, React web assets, a Node video-worker sidecar, local
SQLite persistence, and file-backed artifacts.

## Runtime Profiles

```text
dev      local repo paths, sqlite by default, sidecar from video-worker/dist
desktop  packaged resources, sqlite, user data directory
cloud    postgres, object/file storage configured by service environment
```

Environment and CLI overrides should feed `internal/config.AppRuntimeConfig`.
The important variables are:

```text
CASCADE_PROFILE=dev|desktop|cloud
APP_MODE=desktop|web
DATABASE_DIALECT=sqlite|postgres
DATABASE_URL=postgres://...
SQLITE_PATH=/path/to/cascade_demoops.db
CASCADE_DATA_ROOT=/path/to/user-data
CASCADE_RESOURCE_ROOT=/path/to/packaged/resources
NODE_WORKER_PATH=/path/to/video-worker/dist/index.js
CASCADE_FFMPEG_PATH=/path/to/ffmpeg
CASCADE_FFPROBE_PATH=/path/to/ffprobe
```

## Desktop Resource Layout

Packaged resources should use this shape:

```text
dist/package/
  cascade-demoops-desktop(.exe)
  resources/
    desktop-runtime.json
    web/
    sidecars/
      video-worker/
        dist/
          index.js
    runtimes/
      node/
        node(.exe)
      ffmpeg/
        ffmpeg(.exe)
        ffprobe(.exe)
```

Runtime data should live outside app resources:

```text
CascadeDemoOps/
  cascade_demoops.db
  artifacts/
  cache/
  logs/
```

On Windows the default root is:

```text
%USERPROFILE%/AppData/Roaming/CascadeDemoOps
```

On macOS the default root is:

```text
~/Library/Application Support/CascadeDemoOps
```

## Build Commands

```text
pnpm build:web
pnpm build:worker
pnpm build:desktop:win
pnpm build:desktop:wails
pnpm build:desktop:mac
pnpm package:desktop
pnpm smoke:desktop-package
pnpm package:desktop:installer
pnpm smoke:desktop-installer
```

`backend/cmd/wails-desktop` is the Windows-first Wails v2/WebView2 entry. It
serves packaged React assets and the existing Go HTTP bridge in-process, without
a dev server or listening port. The old `backend/cmd/desktop` native Exchange
surface is disabled; it cannot upload, query, or download through the retired
DemoOps Exchange path.

`backend/cmd/desktop-installer` is the current Windows setup baseline. It is a
self-extracting installer that embeds the portable zip payload, validates the
payload SHA-256, installs files into a per-user directory, writes
`install-manifest.json`, creates an uninstall PowerShell script, and can create a
Start Menu launcher. It keeps server connectivity optional and only reserves the
exchange/cloud lifecycle interfaces for later production wiring.

`desktop-runtime.json` is the resource manifest. In desktop profile, Go resolves
resources in this order:

```text
env override
exe_dir/resources
exe_dir/../resources
exe_dir/../../package/resources
exe_dir
```

## Sidecar Contract

The video worker remains a JSON-RPC 2.0 stdio process. The worker exposes a
`health` method for process checks. In dev, Go resolves it from
`NODE_WORKER_PATH` or `video-worker/dist/index.js`. In packaged desktop builds,
Go resolves it from the resource manifest:

```text
resources/sidecars/video-worker/dist/index.js
```

The current Windows package copies the build-time Node executable into
`resources/runtimes/node/node.exe`, records it in `desktop-runtime.json`, and the
Go runtime resolves it before falling back to system `node`. Users should not
need to install Node manually for the packaged video-worker sidecar.
Both portable and installer smoke tests start that bundled Node executable
against `resources/sidecars/video-worker/dist/index.js` and require a JSON-RPC
`health` response before they pass.

The packaged App does not perform production demo recording locally. Real demo
browser execution, adaptive recording, and failure diagnosis belong to the
server-side Browser Agent. Desktop packaging keeps only the workbench that
collects inputs, produces/uploads the approved package, downloads final videos
or error reports, and opens the local editor. The packaged `video-worker`
sidecar is therefore an editor/media helper and compatibility runtime, not the
primary demo-recording execution path.

This boundary is also exposed by `/v1/desktop/runtime-health` as
`app_capabilities.server_recording_required=true` and
`app_capabilities.local_recording_execution=false`, and it is repeated in the
portable, installer, and release-channel manifests.

`CASCADE_PACKAGE_FFMPEG_PATH` and `CASCADE_PACKAGE_FFPROBE_PATH` supply licensed
build inputs. Their paths and hashes enter the resource/package manifests.
`beta` and `stable` builds fail closed when either runtime is absent; `internal`
builds may omit them for UI-only testing.

## Packaged App Surfaces

The desktop package manifest declares the user-facing surfaces that must remain
available in packaged builds:

- `demo_asset_generation_console`: product URL, local repo path, requirement and
  transient credential input, local package generation, stage JSON review,
  Browser Agent outline review, and execution package approval.
- `video_editor`: result package or local media import, timeline editing,
  caption/callout editing, preview, and MP4 export entry points.

Both portable and installer smoke tests inspect the packaged `resources/web`
bundle for these visible workspace labels. This catches release builds that
start successfully but accidentally omit the demo asset generation console or
the colleague-provided editor workspace.

`pnpm smoke:desktop-ui` starts the packaged desktop host and drives a real
Chromium-compatible browser against it. It verifies that the demo console loads,
the execution package approval view is reachable, and the video editor route
shows import, preview, export, and timeline controls. Set
`CASCADE_DESKTOP_UI_SMOKE_BROWSER` when Chrome or Edge is not in a standard
Windows location.

## Data Boundary

Desktop mode is local-first:

- SQLite stores app metadata and graph state.
- Artifact bytes are files under the artifact root.
- The database stores artifact URIs and metadata only.
- Secret values are never stored in the app database; only secret references
  and scope metadata may be persisted.

Until the SQLite adapter is implemented, the desktop entry uses a file-backed
orchestrator state store under the user data directory. This keeps desktop
bootstrap runs durable without changing the final repository boundary.

## Installer Layout

`pnpm package:desktop:installer` invokes Inno Setup and emits a per-user x64
installer. `pnpm package:desktop:installer:legacy` keeps the previous Go
self-extracting installer available only as a migration fallback.

```text
dist/release/
  CascadeDemoOps-desktop-latest.json
  CascadeDemoOps-<version>-windows-x64-installer.exe
  CascadeDemoOps-<version>-windows-x64-installer.exe.manifest
  CascadeDemoOps-<version>-windows-x64-installer.exe.sha256
  CascadeDemoOps-<version>-windows-x64-installer.manifest.json
```

`CascadeDemoOps-desktop-latest.json` is the stable release-channel manifest for
download pages or server-side release feeds. It points to the recommended
installer, the portable zip fallback, their manifests, SHA-256 checksums,
server-connectivity reservation, install behavior, and required App surfaces.

The setup executable supports:

```text
CascadeDemoOps-...-installer.exe --check
CascadeDemoOps-...-installer.exe --quiet --launch=false
CascadeDemoOps-...-installer.exe --install-dir C:\Tools\CascadeDemoOps
```

`beta` and `stable` release jobs must set the Authenticode certificate thumbprint
and RFC3161 timestamp URL. App and installer signatures are verified immediately
after signing. Signed Ed25519 update manifests use `internal`, `beta`, or
`stable`; private keys and certificates stay outside the repository. Release
packages also include the isolated `cascade-demoops-updater.exe` and an Ed25519
public key supplied through `DEMOOPS_UPDATE_PUBLIC_KEY_PATH`. The updater rejects
non-HTTPS feeds, verifies the detached manifest signature, resumes downloads,
checks artifact SHA-256 and Authenticode, and requires a trusted previous
installer before `--apply` so failed install or health-check paths can roll back.
The App settings page exposes the same fail-closed workflow: it only shows
version/channel/release notes, asks for explicit user confirmation, and enables
installation after the signed manifest and artifact are verified. `beta` and
`stable` builds must also set `DEMOOPS_UPDATE_MANIFEST_URL` to a DemoOps-owned
HTTPS release origin; the package never derives an update endpoint from a
customer `product_url`.

`pnpm smoke:desktop-installer` installs into an isolated smoke directory with a
mock `%APPDATA%`, verifies the generated Start Menu launcher, starts the app,
then runs `Uninstall-CascadeDemoOps.ps1`. The uninstall script removes the
install directory and launcher, but intentionally leaves user data roots intact.
