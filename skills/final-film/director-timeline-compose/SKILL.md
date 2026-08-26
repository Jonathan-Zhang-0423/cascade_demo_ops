---
name: director-timeline-compose
description: Compile a factual story plan and accepted presentation candidates into a deterministic versioned EDL for FFmpeg. Use for 90-120 second pacing, wait compression, captions, generated-shot placement, audio/loudness planning, or diagnosing speed and output-time drift.
---

# Director Timeline Compose

Create a deterministic edit plan whose factual source bindings remain auditable.

Skill version: `2.0.0`.

## Workflow

1. Read `references/edl-contract.md` and `references/interactive-evidence-edl.md`.
2. Copy the factual plan. Never change fact source artifact IDs, source ranges, or required order. Bind internal step IDs only after the Director returns public fact IDs.
3. Apply 4-12x speed only to evidenced wait ranges. Use legal FFmpeg `atempo` factors; 8x becomes `2,2,2`.
4. Insert accepted generated candidates only at their declared before-first, after-last, or after-step anchor.
5. Recompute every output duration after speed, then align captions, audio segments, and global time ranges to the output timeline.
6. Normalize every segment to 1920x1080, CFR30, H.264/yuv420p/AAC. Keep factual UI at a fixed crop with no pan, zoom, stabilization, or synthetic motion. Use hard cuts or one real xfade of about eight frames; never fade every segment independently.
7. Target -16 LUFS and -1 dBTP when an audio mastering pass is available. Preserve source audio unless explicitly muted.
8. Emit the EDL, render profile, subtitles, FFmpeg plan, and decision log. Refuse output outside 90-120 seconds.

Caption text must exactly equal one approved public variant. Remove an unbound caption; never derive copy from action, expected outcome, observed state, DOM, or logs.

## Recovery

Rendering the same job revision must use the same output directory and EDL. Do not create new model attempts during composition recovery.
