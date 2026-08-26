# Guided harness runbook

Flow: factual baseline -> evidence digest -> Director story -> H3 intro/outro and Seedance 2.5 dividers -> quality/retry -> deterministic EDL -> FFmpeg -> review package -> one human final review.

Recovery invariant: completed facts and paid attempts are identified by job revision, persisted admission, intent, provider, attempt number, and idempotency key. `non_destructive` does not mean replayable.

Review package roles include final video, fact baseline, raw recording, provider originals/normalized files, Director plan, evidence digest, EDL, quality reports, provider admission ledger, render manifest, and manifest.

A client or polling timeout does not create attempt N+1. Resume the existing non-terminal admission with its stored idempotency key. Only a distinct persisted admission that reaches terminal failure consumes an attempt. A required slot that fails twice moves the job to `provider_revision_required`; never omit it and deliver a degraded film.

Reject any job whose final contact sheet does not prove login, creation, original prompt input, submission, real waiting, result reveal, and interaction in order. Editing defects route to Director replanning/recomposition without Provider consumption. Material defects route to chapter rerecording. Product defects route to the same bound entity, at most three repair rounds.
