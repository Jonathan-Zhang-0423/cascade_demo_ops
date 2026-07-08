import type { DemoWorkflowGraph, ExecutionRun, WorkflowStep } from "@cascade/schemas";

export interface BrowserExecutionContext {
  projectId: string;
  workflowGraphId: string;
  mode: "exploration" | "rehearsal" | "recording";
  viewport: { width: number; height: number };
}

export interface StepExecutor {
  executeStep(step: WorkflowStep, context: BrowserExecutionContext): Promise<void>;
}

export interface GraphExecutor {
  execute(graph: DemoWorkflowGraph, context: BrowserExecutionContext): Promise<ExecutionRun>;
}
