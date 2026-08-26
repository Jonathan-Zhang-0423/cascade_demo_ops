---
name: director-quality-gate
description: Evaluate generated candidates and the assembled factual demo using separate technical, temporal, text, and content evidence. Use for batch contact-sheet review, flicker or shake measurement, fake-UI and internal-text rejection, one bounded regeneration, repair routing, and final-film acceptance.
---

# Director Quality Gate

Convert technical and visual evidence into a bounded decision.

Skill version: `2.0.0`.

## Candidate Gate

1. Read `references/quality-thresholds.md` and `references/interactive-proof-gate.md`.
2. Require successful decode and normalized MP4/H.264/yuv420p/1920x1080/CFR30 evidence.
3. Compute four independent fields: `TechnicalPass` for decode/profile/black/freeze/audio, `TemporalPass` for signalstats flash measurement plus vidstabdetect-only shake evidence, `TextPass` for no readable text/UI/HTML/internal fields, and `ContentPass` for intent match with no fake UI or garble.
4. Review all first-attempt contact sheets in one visual call and all regenerated contact sheets in at most one second call. Write concrete findings, score, provider, intent, candidate, and attempt into `CandidateQualityReport`.
5. Accept only when all four fields pass. Attempt one may return `retry`; attempt two returns `provider_revision_required` and blocks the required slot.
6. Never mark a generated candidate authoritative and never fabricate a human content approval.

## Full Film Gate

Require decodable 90-120 seconds, 1920x1080 CFR30 H.264/yuv420p/AAC, synchronized timeline, all seven factual chapters in order, exact original prompt visibility, stable factual UI, no non-boundary full-screen flash, no internal text, three accepted generated slots, and no unplanned skipped FFmpeg operation. Review one ordered final-film contact sheet. If audio is present, require -16 LUFS ±2 and true peak no higher than -1 dBTP; verified silence is allowed. Failures create a targeted `RepairDirective`; only a fully passing film reaches `awaiting_final_review`.
