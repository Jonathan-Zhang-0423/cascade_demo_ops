# Legacy Playwright Runtime Freeze v1

## Decision

`playwright-restricted-sandbox` is frozen as of 2026-07-23. It remains
executable only to replay or deliver packages that were already produced on the
legacy route. The product execution path is `browser-agent-outline-v1`.

## What remains supported

- Reading, validating, running, and returning results for historical legacy
  packages.
- Regression fixtures that prove the legacy router branch has not been broken.
- Emergency rollback for a package whose approved runtime is explicitly
  `playwright-restricted-sandbox`.

## What is frozen

- No new Browser Agent capability, policy rule, locator repair, validation
  behaviour, input resolver, secret broker, or UI feature may be added to the
  legacy TypeScript runner.
- New acceptance fixtures and all new App-to-Server integration work must use
  `browser-agent-outline-v1`.
- An Outline package must never be converted to inline TypeScript merely to use
  the legacy runner.

## Operational guardrail

`Runtime Router` continues to route solely by the approved
`script_manifest.runtime`. It does not silently fall back between runtimes:

```text
playwright-restricted-sandbox -> legacy runner (historical compatibility only)
browser-agent-outline-v1      -> Policy Guard -> Outline Runner
```

This is a product-development freeze, not a destructive removal. Removing the
legacy runner is a later migration decision and requires a separate rollback
and historical-package retention plan.
