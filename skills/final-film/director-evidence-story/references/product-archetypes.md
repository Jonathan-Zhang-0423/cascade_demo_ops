# Product Archetype References

Route by observed interaction structure, never by hostname, route shape,
selector, product name, or remembered copy. If two profiles tie, use the
generic goal-driven flow and do not guess.

## Async builder

- Signals: a committed action, bounded progress observation, and a terminal
  result observation.
- Evidence: network completion, DOM/ARIA delta, observed route transition, or
  an independently bound result surface.

## CRUD or form

- Signals: one or more value mutations, a commit action, and observation of
  the new/updated state.
- Evidence: exact preserved input value followed by DOM/ARIA or result-surface
  change. Seeing the submit control again is not an outcome.

## Dashboard or analytics

- Signals: observation-dominant flow, optional filters, and data-region
  refresh.
- Evidence: network completion plus DOM/ARIA or visual-region change. Do not
  infer success from labels or chart titles alone.

## Canvas or editor

- Signals: repeated direct manipulation followed by a visible composition
  result.
- Evidence: a before/after digest of the approved canvas/editor region. Keep
  source time and output time distinct.

## Interactive application

- Signals: bounded keyboard or pointer input directed at an approved runtime
  region.
- Evidence: iframe/canvas/region before-and-after change. Do not search for
  game-specific scores, controls, text, or DOM structure.

These profiles affect evidence interpretation only. Factual step order,
generated-shot provider policy, retry budget, and FFmpeg output gates remain
unchanged.
