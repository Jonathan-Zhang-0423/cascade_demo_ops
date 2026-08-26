---
name: director-evidence-story
description: Convert approved public narrative facts and verified cross-site media evidence into factual chapters and a 90-120 second demo story. Use when a Director must select source ranges, compress real waits, place generated packaging, request missing material, or preserve a complete login-to-product-use narrative without receiving internal browser fields.
---

# Director Evidence Story

Turn verified evidence into a concise story while keeping the captured UI authoritative.

Skill version: `2.0.0`.

## Workflow

1. Read `references/runtime-contract.md`, `references/product-archetypes.md`, and `references/interactive-product-story.md` before producing structured output.
2. Accept narrative text only through `PublicNarrativeFact.approved_caption_variants`. Reject `observed_state`, `expected_outcome`, selectors, HTML, schema fields, request JSON, source paths, or execution node IDs.
3. Require covered chapters in this order: login, creation, prompt input, submission, build wait, result reveal, interaction. Emit a `material_repair_request` instead of inventing a missing chapter.
4. Preserve factual order exactly. Keep login, creation, original prompt, submit, result, and real interaction at 1x. Mark only genuinely idle build ranges for 4-12x compression; prefer 8x.
5. Give result/proof beats enough time to understand the new state. Generated media may introduce or bridge chapters but cannot replace a fact beat.
6. Treat digest ranges as source time and all story-plan durations as output time. Allocate a 90-120 second output target including 4-second intro/outro and up to two 4-second dividers. If the evidence cannot support that duration without fabrication, report the constraint.
7. Return a `DirectorStoryPlan` with source choices, ranges, speeds, generated-slot placement, audio strategy, material repair requests, and concise reasons. Refer to public fact IDs and Artifact refs at the model boundary; let the compiler bind internal step IDs afterward.

## Generalization Rules

- Infer structure from semantic roles, actions, outcomes, timing, and observed surfaces.
- Never branch on hostname, fixed routes, product names, business copy, or selectors remembered from another run.
- Treat canvas, iframe, DOM, and native-looking surfaces as evidence regions, not business categories.
- Do not invent narration, product claims, numbers, or success states.
