# Server E2E Tests (TEST ONLY)

This directory contains local and controlled end-to-end test orchestration for
the Server-side Browser Agent flow. It is not part of the production runtime
and must not be deployed as a service.

Production builds use only:

- `backend/cmd/browser-agent-gateway`
- `backend/cmd/browser-agent-direct-worker`
- `video-worker`'s compiled runtime

The scripts here may use loopback products, fixtures, test waivers, Dev HTTP,
or test credentials. Their output belongs under `artifacts/dev-test-only/`.

## Test entry points

```text
tests/server-e2e/scripts/run-app-server-e2e-unattended.ps1
tests/server-e2e/scripts/audit-app-browser-agent-package.mjs
tests/server-e2e/scripts/run-local-browser-agent-package.mjs
tests/server-e2e/scripts/repair-local-browser-agent-package.mjs
tests/server-e2e/scripts/validate-server-render.mjs
tests/server-e2e/scripts/smoke-validate-server-render.mjs
```

Do not use these scripts to claim a production App-to-Server run unless the run
uses the production Direct deployment and an App-generated package.
