# Runtime contract

Input: `DirectorEvidenceDigest` schema `demoops.director_evidence_digest.v1`.

Required output: `DirectorStoryPlan` schema `demoops.director_story_plan.v1`, target 90000-120000 ms, ordered beats, timeline segments, audio plan, skill versions, and decision log.

Fact segments must retain `source_artifact_id`, `source_step_id`, and `source_range_ms`. Speed is 0.5-12. Generated segments carry only an intent ID, placement, and optional factual anchor.
