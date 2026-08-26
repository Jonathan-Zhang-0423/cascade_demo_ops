# EDL contract

Fact identity fields are immutable. Factual UI may use only trim, a constant crop, wait-only speed, approved captions, audio mix, and deterministic boundary transition. Time-varying crop, pan, zoom, stabilization, `ambient_motion`, and per-segment fade-in are prohibited.

Output profile: MP4, H.264, yuv420p, AAC, 1920x1080, CFR30. Output time advances by `source_duration / speed`, not source duration.

Every caption must exactly match a `PublicNarrativeFact.approved_caption_variants` value. The output uses hard cuts or a true xfade of about eight frames.
