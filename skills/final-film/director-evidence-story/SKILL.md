---
name: director-evidence-story
description: Convert a verified cross-site browser recording evidence digest into factual story beats and a 90-120 second demo structure. Use when planning a product demo from DirectorEvidenceDigest, timeline steps, observed outcomes, active/wait ranges, or evidence-bound artifacts without relying on a site hostname or remembered selectors.
---

# Director Evidence Story

Turn verified evidence into a concise story while keeping the captured UI authoritative.

Skill version: `1.0.0`.

## Workflow

1. Read `references/runtime-contract.md` and `references/product-archetypes.md` before producing structured output.
2. Confirm every required step has a positive source range, observed outcome, and artifact reference. Stop if a factual step is unsupported.
3. Preserve required-step order exactly. Group adjacent steps into setup, action, result, and proof beats only when that grouping does not reorder them.
4. Keep input, submit, result, and real interaction at 1x. Mark genuinely idle ranges for 4-12x compression; prefer 8x.
5. Give result/proof beats enough time to understand the new state. Generated media may introduce or bridge chapters but cannot replace a fact beat.
6. Treat digest ranges as source time and all story-plan durations as output time. Allocate a 90-120 second output target including 4-second intro/outro and up to two 4-second dividers. If the evidence cannot support that duration without fabrication, report the constraint.
7. Return a `DirectorStoryPlan` plus short decision log citing step IDs and evidence refs.

## Generalization Rules

- Infer structure from semantic roles, actions, outcomes, timing, and observed surfaces.
- Never branch on hostname, fixed routes, product names, business copy, or selectors remembered from another run.
- Treat canvas, iframe, DOM, and native-looking surfaces as evidence regions, not business categories.
- Do not invent narration, product claims, numbers, or success states.
