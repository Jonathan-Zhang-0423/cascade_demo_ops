# Browser Agent Acceptance Gate v1

## Purpose

Before adding Browser Agent capabilities, the Server must show repeatable,
visible evidence that the controlled runtime respects the approved contract.
The gate starts with the checked-in `browser-agent-outline-v1` fixture, replaces
only its test origin with a Server-owned local HTML page, then runs the same
`ClientExecutionPackage` through Intake, Runtime Router, Outline Runner, result
packaging, status/result retrieval, and delivery acknowledgement. It never uses
an App user's package, asset, credential, or production URL.

## Fixed scenarios

| Scenario | Expected result | Evidence required |
| --- | --- | --- |
| `success_navigation_click` | Navigate, resolve the approved button, click, render the requested final video, and pass required checks. | Intake acceptance, Stage events, redacted screenshots, final video, result package, and delivery acknowledgement. |
| `semantic_target_contract_conflict` | The visible but differently named button is blocked before click. | Failed result, redacted screenshot/trace refs, repair request. |
| `locator_missing` | A target absent from the page is blocked before click. | Failed result, redacted screenshot/trace refs, repair request. |
| `approved_selector_alternative_repair` | The primary selector fails; exactly one App-approved alternative resolves and executes. | Before/after screenshots, verified candidate identity, and patch ledger entry. |
| `busy_page_wait_repair` | The target is missing while the page is busy; a single bounded wait makes it available. | Before/after screenshots, busy-state observation, and patch ledger entry. |
| `required_validation_failure` | An action may finish, but a failed required result stops progression. | No later Stage, failed validation report, failure result. |
| `recording_and_trace_delivery` | Session close preserves replay evidence. | Existing WebM recording, trace ZIP, and JSONL event audit. |

## How to run

From the local product UI, open **执行包** and use **运行固定验收包**. The
same report is presented as expected-versus-actual rows with evidence links.

For code-level execution, run the app acceptance test. The report and evidence
are written under:

```text
artifacts/browser-agent-acceptance/latest/
```

## Gate decision

The gate is passed only when every fixed scenario reports `verdict=passed` and
`strict_gate=passed`. A failed run blocks Browser Agent feature expansion until
the cause is fixed and the entire fixed set is rerun.

This gate verifies the current controlled Browser Agent protocol path. It is
not a claim of production isolation, production credentials, or arbitrary
customer-site coverage; those require separate environment and customer-package
acceptance gates.
