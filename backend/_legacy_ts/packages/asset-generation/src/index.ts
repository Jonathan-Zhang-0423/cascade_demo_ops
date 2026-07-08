import type { Asset, DemoWorkflowGraph, ExecutionRun } from "@cascade/schemas";

export interface AssetGenerationRequest {
  projectId: string;
  workflowGraph: DemoWorkflowGraph;
  executionRun: ExecutionRun;
  requestedAssets: Asset["type"][];
}

export interface AssetGenerationPlan {
  script?: string;
  narration?: string;
  screenshotStepIds: string[];
  renderPlan: Record<string, unknown>;
}

export interface AssetGenerator {
  plan(request: AssetGenerationRequest): Promise<AssetGenerationPlan>;
  generate(request: AssetGenerationRequest, plan: AssetGenerationPlan): Promise<Asset[]>;
}
