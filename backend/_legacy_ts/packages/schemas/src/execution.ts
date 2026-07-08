import { z } from "zod";
import { ArtifactRefSchema, IdSchema, IsoDateTimeSchema } from "./common.js";

export const FailureTypeSchema = z.enum([
  "selector_not_found",
  "auth_failed",
  "navigation_timeout",
  "validation_failed",
  "unexpected_modal",
  "dirty_demo_data",
  "sensitive_data_exposed",
  "feature_flag_disabled",
  "backend_api_error",
  "server_runtime_error",
  "unknown_error"
]);

export const StepResultSchema = z.object({
  stepResultId: IdSchema,
  executionRunId: IdSchema,
  stepId: IdSchema,
  status: z.enum(["passed", "failed", "skipped"]),
  attempts: z.number().int().min(1),
  durationMs: z.number().int().min(0),
  failureType: FailureTypeSchema.optional(),
  message: z.string().optional(),
  artifacts: z.array(ArtifactRefSchema).default([])
});

export const ExecutionRunSchema = z.object({
  executionRunId: IdSchema,
  workflowGraphId: IdSchema,
  projectId: IdSchema,
  mode: z.enum(["exploration", "rehearsal", "recording"]),
  status: z.enum(["queued", "running", "passed", "failed", "blocked", "cancelled"]),
  startedAt: IsoDateTimeSchema.optional(),
  completedAt: IsoDateTimeSchema.optional(),
  stepResults: z.array(StepResultSchema).default([]),
  qaFindings: z.array(z.object({ severity: z.enum(["info", "warning", "error"]), message: z.string() })).default([])
});

export type ExecutionRun = z.infer<typeof ExecutionRunSchema>;
export type StepResult = z.infer<typeof StepResultSchema>;
