export interface ModelRunContext {
  projectId: string;
  runId?: string;
  promptVersion?: string;
  modelVersion?: string;
  trace?: Record<string, unknown>;
}

export interface ModelAdapter<Input, Output> {
  name: string;
  version: string;
  run(input: Input, context: ModelRunContext): Promise<Output>;
}

export interface FailureRepairInput {
  failedStepId: string;
  executionTrace: Record<string, unknown>;
  evidenceIds: string[];
}

export interface FailureRepairOutput {
  failureType: string;
  rootCause: string;
  graphPatch?: Record<string, unknown>;
  retryPlan?: Record<string, unknown>;
  humanActionRequired?: string;
  confidence: number;
}

export type FailureRepairModelAdapter = ModelAdapter<FailureRepairInput, FailureRepairOutput>;

export interface LegacyAigcAdapterInput {
  workflowGraphId: string;
  executionRunId?: string;
  assetType: string;
  context: Record<string, unknown>;
}

export interface LegacyAigcAdapterOutput {
  script?: string;
  narration?: string;
  docsDraft?: string;
  renderPlan?: Record<string, unknown>;
}

export type LegacyAigcAdapter = ModelAdapter<LegacyAigcAdapterInput, LegacyAigcAdapterOutput>;
