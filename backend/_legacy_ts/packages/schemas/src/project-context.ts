import { z } from "zod";
import { IdSchema, SecretRefSchema, UrlSchema } from "./common.js";

export const AssetTargetSchema = z.enum([
  "demo_video_60s",
  "step_by_step_docs",
  "screenshot_pack",
  "gif",
  "launch_video",
  "sales_material",
  "support_snippet"
]);

export const ProductEnvironmentSchema = z.enum(["staging", "production", "local", "demo"]);
export const AudienceSchema = z.enum(["buyer", "end_user", "support", "sales", "investor", "internal_cs"]);

export const FrontendAccessSchema = z.object({
  url: UrlSchema,
  environment: ProductEnvironmentSchema,
  loginMethod: z.enum(["username_password", "sso_manual", "magic_link", "pre_auth_state"]),
  credentialSecretRef: SecretRefSchema.optional(),
  startPath: z.string().optional()
});

export const GitHubAccessSchema = z.object({
  installationId: z.string().optional(),
  owner: z.string().min(1),
  repo: z.string().min(1),
  branch: z.string().default("main"),
  commitSha: z.string().optional(),
  permission: z.literal("read-only")
});

export const SshAccessSchema = z.object({
  host: z.string().min(1),
  port: z.number().int().min(1).max(65535).default(22),
  username: z.string().min(1),
  authSecretRef: SecretRefSchema,
  allowedPaths: z.array(z.string()).default([]),
  allowedCommands: z.array(z.string()).default([]),
  permission: z.literal("read-only")
});

export const ProjectContextSchema = z.object({
  projectId: IdSchema,
  tenantId: IdSchema,
  product: z.object({
    name: z.string().min(1),
    description: z.string().optional(),
    frontend: FrontendAccessSchema
  }),
  github: GitHubAccessSchema.optional(),
  ssh: SshAccessSchema.optional(),
  docs: z.array(z.object({ title: z.string(), uri: z.string() })).default([]),
  audience: z.object({
    primary: AudienceSchema,
    secondary: z.array(AudienceSchema).default([]),
    intent: z.enum(["launch", "sales", "support", "education", "investor_update"])
  }),
  assetsRequested: z.array(AssetTargetSchema).min(1),
  requirements: z.object({
    mustShow: z.array(z.string()).default([]),
    mustNotShow: z.array(z.string()).default([]),
    forbiddenPages: z.array(z.string()).default([]),
    forbiddenData: z.array(z.string()).default([])
  }),
  brand: z.object({
    tone: z.string().optional(),
    visualStyle: z.string().optional(),
    preferredTerms: z.array(z.string()).default([]),
    forbiddenTerms: z.array(z.string()).default([])
  }).default({ preferredTerms: [], forbiddenTerms: [] })
});

export type ProjectContext = z.infer<typeof ProjectContextSchema>;
