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
resources/
  desktop-runtime.json
  web/
  sidecars/
    video-worker/
      dist/
        index.js
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
```

`backend/cmd/desktop` is the desktop runtime root. It currently initializes the
shared desktop bridge and is intentionally Wails-ready without importing Wails
yet. The next packaging step is to wire that bridge into a Wails app and embed
the `frontend/web` build output.

## Sidecar Contract

The video worker remains a JSON-RPC 2.0 stdio process. In dev, Go resolves it
from `NODE_WORKER_PATH` or `video-worker/dist/index.js`. In packaged desktop
builds, Go resolves it from:

```text
resources/sidecars/video-worker/dist/index.js
```

Production installers must provide the worker runtime or a bundled
Node-compatible executable so users do not need to install Node manually.

## Data Boundary

Desktop mode is local-first:

- SQLite stores app metadata and graph state.
- Artifact bytes are files under the artifact root.
- The database stores artifact URIs and metadata only.
- Secret values are never stored in the app database; only secret references
  and scope metadata may be persisted.
