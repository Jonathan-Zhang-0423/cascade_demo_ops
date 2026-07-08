import { z } from "zod";
import { ArtifactRefSchema, IdSchema, IsoDateTimeSchema, JsonObjectSchema } from "./common.js";

export const EvidenceKindSchema = z.enum([
  "user_input",
  "browser",
  "repo",
  "server",
  "docs",
  "execution_trace",
  "human_review"
]);

export const EvidenceRecordSchema = z.object({
  evidenceId: IdSchema,
  projectId: IdSchema,
  kind: EvidenceKindSchema,
  sourceRef: z.object({
    type: z.string().min(1),
    id: z.string().min(1),
    version: z.string().optional()
  }),
  summary: z.string().optional(),
  data: JsonObjectSchema.default({}),
  artifacts: z.array(ArtifactRefSchema).default([]),
  createdAt: IsoDateTimeSchema
});

export const BrowserEvidenceDataSchema = z.object({
  url: z.string(),
  title: z.string().optional(),
  visibleText: z.array(z.string()).default([]),
  clickableElements: z.array(z.object({
    role: z.string().optional(),
    name: z.string().optional(),
    selectorHint: z.string().optional()
  })).default([]),
  screenshots: z.array(ArtifactRefSchema).default([])
});

export const RepoEvidenceDataSchema = z.object({
  commitSha: z.string().optional(),
  routes: z.array(z.string()).default([]),
  selectorHints: z.array(z.string()).default([]),
  apiEndpoints: z.array(z.string()).default([]),
  seedHints: z.array(z.string()).default([])
});

export const ServerEvidenceDataSchema = z.object({
  runtimeSummary: z.string().optional(),
  featureFlags: z.record(z.string()).default({}),
  dbSchemaSummary: z.string().optional(),
  logSummary: z.string().optional()
});

export type EvidenceRecord = z.infer<typeof EvidenceRecordSchema>;
