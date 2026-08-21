# Interactive Product Story Reference

Use this reference when the verified result is an interactive surface such as a dashboard, editor, canvas, embedded application, or game-like experience.

## Evidence-first beat order

1. Establish the user goal from the frozen product specification.
2. Show the exact submitted input and the once-effect submission evidence.
3. Compress only observed waiting ranges; use temporal keyframes to show genuine progress without implying events that were not captured.
4. Reveal the verified result using the first frame that passed two independent terminal evidence channels.
5. Demonstrate at least two distinct real interactions and their before/after region evidence.
6. Include recovery, reversal, persistence, responsive behavior, or boundary-state proof only when each has its own evidence slot.
7. End with a capability summary derived from passed criteria, never from the requested specification alone.

## Timing guidance

- Keep submission, first reveal, state-changing interactions, reversal, and boundary-state proof at 1x.
- A repeated interaction can be trimmed, but its before and after states must remain readable.
- Represent long waits with a deterministic keyframe montage whose dates and order come from the observation event sequence.
- A restart boundary is a factual chapter boundary only if checkpoint and segment references prove continuity.

## Attribution

The decision log must distinguish system actions, allowed human gates, provider calls, and out-of-band browser actions. If an out-of-band action occurred, preserve the evidence but mark the run invalid rather than hiding it.
