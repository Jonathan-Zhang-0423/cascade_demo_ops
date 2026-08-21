# 2048 v2 real-run report: pre-submit package gate

- Run ID: `experiment_c510179ec91266f851fbf9fc`
- Date: 2026-08-21 (Asia/Shanghai)
- Final state: `canceled`
- Final phase: `canceled`
- Report validity: `false`
- Target submissions: `0`
- H3/Seedance provider calls: `0`
- Visual observation calls: `0`
- Out-of-band browser actions: `0`

## Outcome

The first authorized real run stopped before any target-site side effect. The
compatibility adapter completed requirement reading, product intelligence,
multimodal understanding, product exploration, page interaction verification,
and graph generation. Script packaging then rejected the interactive keyboard
stage because the semantic outcome validator accepted only a page-wide change
while the selected interactive archetype correctly emitted a bounded
`frame_surface_changed` assertion.

The observed error was:

`deterministic_validation_missing: stage "business_stage_interactive_surface_change" requires a concrete result assertion that proves the business outcome rather than rechecking the action target`

No project was created or submitted, no Direct external task ID was admitted,
and no H3 or Seedance budget was consumed. The run was canceled with revision 5
instead of resumed, so it cannot be picked up after an App or Worker restart.

## Harness correction

The semantic validator now recognizes `page_changed`,
`visual_region_changed`, and `frame_surface_changed` as independent before/after
outcome evidence for an approved bounded keyboard action. It still rejects
`element_visible` and rejects unbounded key input.

A revision-bound experiment cancellation operation was also added. Cancellation
terminates both legs, creates a structured audit event and invalid report, and
does not replay or create external effects.

Relevant commits:

- `a5e0da4 fix(browser): accept evidence-bound surface changes`
- `d048a7f feat(experiment): add revision-bound cancellation`

## Rerun rule

This run is evidence only and must not be resumed. A new real run requires a
new startup/cost authorization and a new experiment run ID. It must use a new
natural project name and proceed exclusively through DemoOps.
