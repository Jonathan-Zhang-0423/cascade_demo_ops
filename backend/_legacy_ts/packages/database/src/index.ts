import type {
  Asset,
  DemoWorkflowGraph,
  EvidenceRecord,
  ExecutionRun,
  ProjectContext,
  StepResult
} from "@cascade/schemas";

export interface ProjectRecord {
  id: string;
  tenantId: string;
  productName: string;
  status: "created" | "context_ready" | "graph_ready" | "rehearsing" | "assets_ready" | "blocked";
  createdBy: string;
  createdAt: string;
  updatedAt: string;
}

export interface ProjectRepository {
  create(input: { tenantId: string; productName: string; createdBy: string }): Promise<ProjectRecord>;
  get(projectId: string): Promise<ProjectRecord | undefined>;
  updateStatus(projectId: string, status: ProjectRecord["status"]): Promise<ProjectRecord>;
}

export interface ProjectContextRepository {
  createVersion(input: { projectId: string; context: ProjectContext; createdBy: string }): Promise<{ id: string; version: number }>;
  getLatest(projectId: string): Promise<ProjectContext | undefined>;
}

export interface EvidenceRepository {
  write(record: EvidenceRecord): Promise<void>;
  listByProject(projectId: string): Promise<EvidenceRecord[]>;
  listByKind(projectId: string, kind: EvidenceRecord["kind"]): Promise<EvidenceRecord[]>;
}

export interface WorkflowGraphRepository {
  createVersion(input: { projectId: string; graph: DemoWorkflowGraph; createdBy: string }): Promise<{ id: string; version: number }>;
  getLatest(projectId: string): Promise<DemoWorkflowGraph | undefined>;
  get(workflowGraphId: string): Promise<DemoWorkflowGraph | undefined>;
  updateStatus(workflowGraphId: string, status: DemoWorkflowGraph["status"]): Promise<void>;
}

export interface ExecutionRepository {
  createRun(run: ExecutionRun): Promise<void>;
  updateRun(run: ExecutionRun): Promise<void>;
  appendStepResult(result: StepResult): Promise<void>;
  getRun(executionRunId: string): Promise<ExecutionRun | undefined>;
}

export interface AssetRepository {
  create(asset: Asset): Promise<void>;
  update(asset: Asset): Promise<void>;
  listByProject(projectId: string): Promise<Asset[]>;
  get(assetId: string): Promise<Asset | undefined>;
}

export interface UnitOfWork {
  projects: ProjectRepository;
  contexts: ProjectContextRepository;
  evidence: EvidenceRepository;
  workflowGraphs: WorkflowGraphRepository;
  executions: ExecutionRepository;
  assets: AssetRepository;
}

export * from "./connection.js";
export * from "./postgres.js";