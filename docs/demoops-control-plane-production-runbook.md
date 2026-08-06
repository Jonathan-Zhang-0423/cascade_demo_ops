# DemoOps control plane deployment runbook

> [!WARNING]
> Legacy compatibility runbook. Do not use this control plane for the App's primary Browser Agent execution path. Use [Browser Agent direct deployment](browser-agent-direct-deployment.md).

This runbook deploys the App execution upload path on a dedicated DemoOps API
hostname. A customer `product_url` is never used to discover or configure this
service.

## Required inputs

- A dedicated hostname whose A/AAAA record points at the execution server:
  `<DEMOOPS_API_HOST>`.
- TCP 80/443 open for Caddy certificate issuance and App traffic.
- A non-root `demoops` service account.
- Node.js, FFmpeg/ffprobe, Caddy, and the Playwright Chromium runtime.
- Model provider credentials stored only in `/etc/demoops/control-plane.env`.

Do not put the server IP, SSH password, model keys, test credentials, or a
customer test-site hostname in Git, desktop resources, update manifests, or
installer command lines.

## Build and install

From a clean checkout of the release commit:

```bash
corepack enable
pnpm install --frozen-lockfile
pnpm --filter @cascade/video-worker build
cd backend
go test ./...
go build -trimpath -o ../dist/server/demoops-control-plane ./cmd/controlplane
```

Install the immutable release tree under `/opt/demoops/releases/<commit>` and
point `/opt/demoops/current` at it. Copy the binary to
`/opt/demoops/current/bin/demoops-control-plane`; retain
`video-worker/dist/index.js` and its production dependencies in the same
release tree.

Install Chromium into the service-owned path:

```bash
sudo -u demoops env PLAYWRIGHT_BROWSERS_PATH=/var/lib/demoops/playwright \
  pnpm --dir /opt/demoops/current/video-worker exec playwright install chromium
```

Create `/etc/demoops/control-plane.env` from
`deploy/control-plane.env.example`, set mode `0600`, and add only the model
credentials required by the server. The current App transport sends the
approved, minimized package inline over TLS, so this release must remain
`APP_ENV=staging`. `APP_ENV=production` deliberately rejects inline payloads;
enable it only after encrypted artifact upload and the isolated decrypt worker
are implemented and verified.

Install `deploy/systemd/demoops-control-plane.service`, then install the Caddy
site from `deploy/caddy/Caddyfile.example` after replacing
`<DEMOOPS_API_HOST>`. The Go service always binds to loopback. Only Caddy owns
the public sockets.

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now demoops-control-plane
sudo systemctl reload caddy
```

## Health and isolation checks

The public service intentionally has no desktop or dev diagnostics route:

```bash
curl -fsS https://<DEMOOPS_API_HOST>/.well-known/cascade-exchange
test "$(curl -sS -o /dev/null -w '%{http_code}' https://<DEMOOPS_API_HOST>/v1/desktop/runtime-health)" = 404
test "$(curl -sS -o /dev/null -w '%{http_code}' https://<DEMOOPS_API_HOST>/v1/dev/execution-packages)" = 404
test "$(curl -sS -o /dev/null -w '%{http_code}' -X POST https://<DEMOOPS_API_HOST>/v1/execution-packages/init)" = 401
```

The App obtains a challenge, registers its Ed25519 installation key, and then
uses `Authorization: Cascade-Session <opaque-session>` for init, upload,
status, SSE, result download, ack, review, and revision. A legacy shared bearer
token is not accepted by the public handler.

## Desktop release configuration

Package every beta/stable client with:

```text
DEMOOPS_CONTROL_PLANE_BASE_URL=https://<DEMOOPS_API_HOST>
```

`scripts/package-desktop.mjs` writes that value into
`resources/desktop-runtime.json`. Beta/stable packaging rejects an empty URL,
plaintext remote HTTP, credentials/query fragments, paths, and IP-literal
hosts. An explicit `CASCADE_CLOUD_EXCHANGE_BASE_URL` environment variable can
still override the packaged value for managed deployments.

## Operational limits before stable release

- Self-service installation registration proves possession of a per-install
  key, but it is not an account entitlement or billing check.
- Inline package transport is protected by TLS and package signatures, but is
  not yet the production encrypted object-storage flow.
- Exchange metadata currently uses a restart-safe local snapshot. Move shared
  metadata to the dedicated database and artifacts to object storage before
  horizontal scaling.
- Add account authorization, quotas, admission control, retention jobs,
  encrypted artifact ingestion, and a decrypt worker before enabling an
  unrestricted stable channel.
