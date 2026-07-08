import { z } from "zod";
import { IdSchema } from "./common.js";

export const JobTypeSchema = z.enum([
  "build_project_context",
  "snapshot_repo",
  "snapshot_server",
  "snapshot_browser",
  "build_product_map",
  "draft_workflow_graph",
  "run_rehearsal",
  "diagnose_failure",
  "run_recording",
  "generate_assets"
]);

export const JobStatusSchema = z.enum(["queued", "running", "succeeded", "failed", "blocked", "cancelled"]);

export const OrchestratorJobSchema = z.object({
  jobId: IdSchema,
  projectId: IdSchema,
  type: JobTypeSchema,
  status: JobStatusSchema,
  priority: z.number().int().min(0).max(100).default(50),
  input: z.record(z.unknown()).default({}),
  output: z.record(z.unknown()).optional(),
  error: z.object({ code: z.string(), message: z.string(), details: z.record(z.unknown()).optional() }).optional(),
  attempts: z.number().int().min(0).default(0),
  createdAt: z.string().datetime(),
  updatedAt: z.string().datetime()
});

export type JobType = z.infer<typeof JobTypeSchema>;
export type JobStatus = z.infer<typeof JobStatusSchema>;
export type OrchestratorJob = z.infer<typeof OrchestratorJobSchema>;