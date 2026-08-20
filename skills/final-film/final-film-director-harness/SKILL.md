---
name: final-film-director-harness
description: Run, inspect, recover, and forward-test the guided H3/Seedance/FFmpeg final-film workflow in this repository. Use for real provider smoke tests from .env, async final-film API jobs, checkpoint/idempotency diagnosis, review-package inspection, or validating that the Director workflow generalizes beyond one website.
---

# Final Film Director Harness

Operate the complete guided final-film flow without bypassing its factual evidence boundary.

Skill version: `1.0.0`.

## Procedure

1. Read `references/runbook.md` and the four Director skills alongside this harness.
2. Confirm the job uses `guided-demo-v1`, a 90-120 second target, and server-owned H3/Seedance provider policy.
3. Load provider credentials through the application's existing `.env` configuration. Never print secrets or copy a key into logs, prompts, job JSON, or Skill files.
4. Start with `POST /v1/final-film/jobs/{id}/run`, one authorization reference, and a call budget no larger than the server policy.
5. Observe durable phases. On restart, resume the current phase; do not repeat completed baseline, Director, provider, or composition work.
6. Inspect CandidateQualityReports and persisted provider admissions. A missing result may resume the same idempotency key, not consume an undeclared attempt.
7. Verify final media and the ZIP review package. End at `awaiting_final_review`.
8. Accept or reject through `/final-review`; there is one human final review per immutable revision/package. Rejection clears consumption authorization and requires a new user authorization after revised direction.

## Generalization Test

Use semantic interaction contracts and runtime-observed surfaces. Mutate hostname, route, DOM depth, copy, and iframe/canvas layout. Any behavior chosen from hostname, fixed selector, or remembered product wording is a failure.
