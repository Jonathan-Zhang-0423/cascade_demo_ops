import { z } from "zod";
import { ArtifactRefSchema, IdSchema } from "./common.js";

export const AssetSchema = z.object({
  assetId: IdSchema,
  projectId: IdSchema,
  workflowGraphId: IdSchema,
  executionRunId: IdSchema.optional(),
  type: z.enum(["demo_video_60s", "step_by_step_docs", "screenshot_pack", "gif", "launch_video"]),
  status: z.enum(["draft", "generating", "ready_for_review", "approved", "rejected", "published", "stale"]),
  version: z.number().int().min(1),
  artifact: ArtifactRefSchema.optional(),
  freshnessStatus: z.enum(["fresh", "at_risk", "stale", "broken"]).default("fresh"),
  provenance: z.object({
    repoSnapshotId: IdSchema.optional(),
    serverSnapshotId: IdSchema.optional(),
    evidenceIds: z.array(IdSchema).default([])
  }).default({ evidenceIds: [] })
});

export type Asset = z.infer<typeof AssetSchema>;
