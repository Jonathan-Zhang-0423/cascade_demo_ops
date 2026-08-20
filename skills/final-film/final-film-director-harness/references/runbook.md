# Guided harness runbook

Flow: factual baseline -> evidence digest -> Director story -> H3 intro/outro and Seedance 2.5 dividers -> quality/retry -> deterministic EDL -> FFmpeg -> review package -> one human final review.

Recovery invariant: completed facts and paid attempts are identified by job revision, persisted admission, intent, provider, attempt number, and idempotency key. `non_destructive` does not mean replayable.

Review package roles include final video, fact baseline, raw recording, provider originals/normalized files, Director plan, evidence digest, EDL, quality reports, provider admission ledger, render manifest, and manifest.

A client or polling timeout does not create attempt N+1. Resume the existing non-terminal admission with its stored idempotency key. Only a distinct persisted admission that reaches terminal failure consumes an attempt. Failed generated slots are omitted; verified factual material may fill duration only within its captured source bounds.
