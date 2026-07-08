# Video Worker Packaging Contract

The desktop app treats this package as an external sidecar.

Build output:

```text
video-worker/dist/index.js
```

Packaged resource location:

```text
resources/sidecars/video-worker/dist/index.js
```

Runtime protocol:

```text
JSON-RPC 2.0 over stdio
```

The Go desktop runtime discovers the worker through `NODE_WORKER_PATH` in dev
or through the packaged resource path in desktop builds. The user should not be
required to install Node manually for production packaging; the final installer
must provide the worker runtime or a bundled Node-compatible executable.
