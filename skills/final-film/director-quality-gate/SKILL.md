---
name: director-quality-gate
description: Evaluate H3 or Seedance presentation candidates, produce CandidateQualityReport, permit one finding-driven retry, and fall back to factual media. Use for decode/profile/duration/black/freeze/loudness checks, visual consistency review, retry decisions, and final film acceptance gates.
---

# Director Quality Gate

Convert technical and visual evidence into a bounded decision.

Skill version: `1.1.0`.

## Candidate Gate

1. Read `references/quality-thresholds.md` and `references/interactive-proof-gate.md`.
2. Require successful decode and normalized MP4/H.264/yuv420p/1920x1080/CFR30 evidence.
3. Check duration drift, empty/black/frozen output, first/last frames, motion, adjacent-shot continuity, fake UI, bad readable text, and unsupported claims. A generated moving shot fails if aggregate black exceeds 0.5 seconds or an unintended freeze exceeds 1.0 second.
4. Write concrete findings, score, provider, intent, candidate, and attempt into `CandidateQualityReport`.
5. Attempt one: accept or return `retry` with findings. Attempt two: accept or `fallback_fact_track`.
6. Never mark a generated candidate authoritative and never fabricate a human content approval.

## Full Film Gate

Require decodable 90-120 seconds, 1920x1080 CFR30 H.264/yuv420p/AAC, synchronized timeline, no unplanned skipped FFmpeg operation, aggregate unexpected black no more than 0.5 seconds, and no unintended freeze over 3.0 seconds outside declared result holds. If audio is present, require -16 LUFS ±2 and true peak no higher than -1 dBTP; a verified silent track is allowed. Stop at `awaiting_final_review`; a human accepts or rejects the complete package once per revision/package.
