import type { DemoWorkflowGraph } from "@cascade/schemas";

export interface GraphVersion {
  workflowGraphId: string;
  version: number;
  status: DemoWorkflowGraph["status"];
  createdBy: string;
  createdAt: string;
}

export interface WorkflowGraphRepository {
  createDraft(graph: DemoWorkflowGraph, actor: string): Promise<GraphVersion>;
  approve(workflowGraphId: string, actor: string): Promise<GraphVersion>;
  markStale(workflowGraphId: string, reason: string): Promise<GraphVersion>;
}
