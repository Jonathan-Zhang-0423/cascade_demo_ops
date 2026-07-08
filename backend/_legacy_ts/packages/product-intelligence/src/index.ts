import type { DemoWorkflowGraph, EvidenceRecord, ProjectContext } from "@cascade/schemas";

export interface ProductMap {
  productMapId: string;
  projectId: string;
  version: number;
  pages: Array<{ url: string; title?: string; demoValueScore?: number }>;
  features: Array<{ name: string; userValue: string; evidenceIds: string[] }>;
  candidateWorkflows: Array<{ name: string; valueProp: string; confidence: number }>;
}

export interface ProductIntelligenceEngine {
  buildProductMap(context: ProjectContext, evidence: EvidenceRecord[]): Promise<ProductMap>;
  draftWorkflowGraph(context: ProjectContext, productMap: ProductMap): Promise<DemoWorkflowGraph>;
}
