---
name: director-generated-shots
description: Design provider-neutral presentation shots for a factual demo and compile them to MiniMax H3 intro/outro or Seedance 2.5 chapter transitions. Use when creating FinalFilmGeneratedSpec prompts, choosing adjacent-frame continuity, or turning a failed CandidateQualityReport into one bounded regeneration attempt.
---

# Director Generated Shots

Design moving chapter packaging that complements, but never impersonates, the product.

Skill version: `1.0.0`.

## Workflow

1. Read `references/provider-policy.md`.
2. Derive a provider-neutral visual intent from the evidence palette, product objective, chapter meaning, and neighboring factual frames.
3. Compile intro/outro to MiniMax H3 and section dividers to Seedance 2.5. Provider choice is server-owned.
4. Request 4 seconds, 16:9, continuous motion. Avoid static cards, readable text, logos, product UI, business facts, people operating software, and fake results.
5. Bind each spec to one pre-created presentation intent and preserve the intent duration, aspect ratio, and references.
6. On the first terminal failed admission, append only the concrete findings and regenerate with the same provider. A polling/client timeout resumes the same admission and idempotency key; it is not a new attempt. Never grant a third admitted attempt.
7. If attempt two terminally fails, emit `fallback_fact_track` and omit that generated slot. Do not replace it with another provider or fabricated media. If omission makes the output shorter than 90 seconds and verified factual material cannot fill it, fail the duration gate explicitly.

## Output

Return provider-neutral intent, compiled prompt, safety constraints, expected transition boundary, and retry feedback when applicable.
