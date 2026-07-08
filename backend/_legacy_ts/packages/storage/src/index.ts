import type { ArtifactRef } from "@cascade/schemas";

export interface PutArtifactInput {
  projectId: string;
  kind: string;
  filename: string;
  mimeType?: string;
  bytes: Uint8Array;
}

export interface ArtifactStorage {
  put(input: PutArtifactInput): Promise<ArtifactRef>;
  get(artifactId: string): Promise<Uint8Array>;
  getSignedUrl(artifactId: string, expiresInSeconds: number): Promise<string>;
}
