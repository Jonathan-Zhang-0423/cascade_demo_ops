import type { EvidenceRecord } from "@cascade/schemas";

export interface EvidenceWriter {
  write(record: EvidenceRecord): Promise<void>;
}

export interface EvidenceReader {
  listByProject(projectId: string): Promise<EvidenceRecord[]>;
  get(evidenceId: string): Promise<EvidenceRecord | undefined>;
}

export interface EvidenceSnapshotJob {
  projectId: string;
  source: "browser" | "repo" | "server" | "docs" | "user_input";
  requestedBy: string;
}
