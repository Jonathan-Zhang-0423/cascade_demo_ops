import { z } from "zod";
import { IdSchema } from "./common.js";

export const WorkflowActionSchema = z.discriminatedUnion("type", [
  z.object({ type: z.literal("navigate"), url: z.string() }),
  z.object({ type: z.literal("click"), selector: z.string(), label: z.string().optional() }),
  z.object({ type: z.literal("fill"), selector: z.string(), value: z.string(), label: z.string().optional() }),
  z.object({ type: z.literal("select"), selector: z.string(), value: z.string() }),
  z.object({ type: z.literal("upload"), selector: z.string(), artifactId: IdSchema }),
  z.object({ type: z.literal("wait"), ms: z.number().int().min(0).max(60000) }),
  z.object({ type: z.literal("assert"), validationId: z.string() }),
  z.object({ type: z.literal("screenshot"), name: z.string() })
]);

export const StepValidationSchema = z.discriminatedUnion("type", [
  z.object({ type: z.literal("url_contains"), value: z.string() }),
  z.object({ type: z.literal("text_visible"), value: z.string() }),
  z.object({ type: z.literal("selector_visible"), selector: z.string() }),
  z.object({ type: z.literal("element_enabled"), selector: z.string() }),
  z.object({ type: z.literal("network_idle"), timeoutMs: z.number().int().default(5000) })
]);

export const WorkflowStepSchema = z.object({
  id: IdSchema,
  intent: z.string().min(1),
  action: WorkflowActionSchema,
  validations: z.array(StepValidationSchema).default([]),
  fallback: z.array(WorkflowActionSchema).default([]),
  capture: z.object({
    record: z.boolean().default(true),
    screenshot: z.boolean().default(false),
    zoom: z.string().optional(),
    callout: z.string().optional()
  }).default({ record: true, screenshot: false }),
  narration: z.string().optional(),
  privacy: z.object({
    maskSelectors: z.array(z.string()).default([]),
    forbiddenRegions: z.array(z.string()).default([])
  }).default({ maskSelectors: [], forbiddenRegions: [] }),
  sourceEvidenceIds: z.array(IdSchema).default([])
});

export const DemoWorkflowGraphSchema = z.object({
  workflowGraphId: IdSchema,
  projectId: IdSchema,
  version: z.number().int().min(1),
  status: z.enum(["draft", "awaiting_review", "approved", "rehearsing", "passed", "failed", "stale"]),
  name: z.string().min(1),
  audience: z.string().min(1),
  assetTargets: z.array(z.string()).default([]),
  preconditions: z.record(z.unknown()).default({}),
  steps: z.array(WorkflowStepSchema).min(1)
});

export type DemoWorkflowGraph = z.infer<typeof DemoWorkflowGraphSchema>;
export type WorkflowStep = z.infer<typeof WorkflowStepSchema>;
