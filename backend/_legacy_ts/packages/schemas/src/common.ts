import { z } from "zod";

export const IdSchema = z.string().min(3);
export const IsoDateTimeSchema = z.string().datetime();
export const UrlSchema = z.string().url();

export const SecretRefSchema = z.object({
  provider: z.enum(["local-dev", "vault", "aws-secrets-manager", "doppler"]),
  ref: z.string().min(1),
  scope: z.string().optional()
});

export const ArtifactRefSchema = z.object({
  artifactId: IdSchema,
  uri: z.string().min(1),
  mimeType: z.string().optional(),
  sha256: z.string().optional()
});

export const JsonObjectSchema: z.ZodType<Record<string, unknown>> = z.record(z.unknown());

export type SecretRef = z.infer<typeof SecretRefSchema>;
export type ArtifactRef = z.infer<typeof ArtifactRefSchema>;
