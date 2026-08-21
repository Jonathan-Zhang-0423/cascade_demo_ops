# Interactive Evidence EDL Reference

- Bind every real interaction clip to a before state, action interval, and after-state evidence reference.
- Preserve enough pre-roll and post-roll to make the visual change legible.
- Do not synthesize pointer, keyboard, touch, score, chart, canvas, or editor changes.
- Keep recovery-run segments in checkpoint order. Concatenate already verified segments before new segments and never duplicate a once-effect interval.
- A heartbeat montage may use crop, scale, dissolve, and timestamp captions, but its source order is immutable.
- Captions describe observed outcomes. Requested features that did not pass evidence gates must not appear as completed capabilities.
- Background music must not mask real interaction audio; duck music around meaningful source sounds.
