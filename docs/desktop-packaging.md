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
      ffmpeg/       # planned
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
pnpm build:desktop:mac
pnpm package:desktop
pnpm smoke:desktop-package
pnpm package:desktop:installer
pnpm smoke:desktop-installer
```

`backend/cmd/desktop` is the desktop runtime root. It currently initializes the
shared desktop bridge and is intentionally Wails-ready without importing Wails
yet. The next packaging step is to wire that bridge into a Wails app and embed
the `frontend/web` build output.

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

FFmpeg/ffprobe are still planned runtime assets. Until those are bundled, video
rendering flows that require FFmpeg may need explicit `CASCADE_FFMPEG_PATH` and
`CASCADE_FFPROBE_PATH` configuration or will use existing fallback behavior.

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

`pnpm package:desktop:installer` emits:

```text
dist/release/
  CascadeDemoOps-<version>-windows-x64-installer.exe
  CascadeDemoOps-<version>-windows-x64-installer.exe.manifest
  CascadeDemoOps-<version>-windows-x64-installer.exe.sha256
  CascadeDemoOps-<version>-windows-x64-installer.manifest.json
```

The setup executable supports:

```text
CascadeDemoOps-...-installer.exe --check
CascadeDemoOps-...-installer.exe --quiet --launch=false
CascadeDemoOps-...-installer.exe --install-dir C:\Tools\CascadeDemoOps
```

The current installer is unsigned and per-user. It ships with a sidecar Windows
application manifest declaring `requestedExecutionLevel=asInvoker` so the
non-admin installer can use a normal `installer.exe` filename without triggering
UAC elevation heuristics. It is suitable for internal download/install smoke
testing, while production release still needs an embedded Windows application
manifest, code signing, installer UI polish, bundled FFmpeg strategy, and
auto-update policy.

`pnpm smoke:desktop-installer` installs into an isolated smoke directory with a
mock `%APPDATA%`, verifies the generated Start Menu launcher, starts the app,
then runs `Uninstall-CascadeDemoOps.ps1`. The uninstall script removes the
install directory and launcher, but intentionally leaves user data roots intact.
