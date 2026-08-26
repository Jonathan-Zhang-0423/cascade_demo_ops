# Runtime contract

Input: the public projection of `DirectorEvidenceDigest` schema `demoops.director_evidence_digest.v2`: `PublicNarrativeFact`, Artifact refs, source ranges, wait ranges, palette, and audio availability. Never expose internal step/node IDs, DOM, selectors, expected/observed fields, paths, or request JSON to the Director model.

Required output: `DirectorStoryPlan` schema `demoops.director_story_plan.v1`, target 90000-120000 ms, ordered beats, timeline segments, audio plan, skill versions, and decision log.

The Director returns public fact IDs. The deterministic compiler restores `source_artifact_id`, internal step binding, and `source_range_ms`. Non-wait facts stay at 1x; build-wait facts may use 4-12x. Generated segments carry only an intent ID, placement, and optional public factual anchor.
