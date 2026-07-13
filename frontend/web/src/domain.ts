import type {
  AssetKind,
  DemoUseCase,
  DemoWorkflowGraph,
  EvidenceRef,
  ExecutableRecordingScriptBundle,
  ExecutionScriptDocument,
  MultimodalUnderstandingReport,
  ProjectInputBundle,
  RecordingResultPackage,
  SandboxExecutionMetadata,
  ScriptFailureDiagnostic,
  ScriptRepairRequest,
} from "../../src/types/workflowGraph";

export type ScenarioID = "product_demo" | "ai_customer_service_demo" | "internal_onboarding_tutorial";

export type WorkspaceStage =
  | "setup"
  | "inputs"
  | "understanding"
  | "plan_review"
  | "package_approval"
  | "cloud_run"
  | "script_repair"
  | "result_review";

export type NavSection = "projects" | "new_demo" | "execution_packages" | "assets" | "settings";

export type ScenarioTemplate = {
  id: ScenarioID;
  name: string;
  useCases: DemoUseCase[];
  objective: string;
  targetAudience: string;
  targetDurationSec: number;
  requiredAssets: AssetKind[];
  defaultChecklist: string[];
};

export type SourceConnectionView = {
  id: string;
  kind: "product_url" | "local_repo" | "requirement_doc" | "screenshot" | "release_note" | "credential";
  label: string;
  status: "ready" | "needs_attention" | "processing" | "blocked";
  detail: string;
  secretStored?: boolean;
};

export type UnderstandingSummaryView = {
  productMapID: string;
  routesDetected: number;
  featuresDetected: number;
  componentsSummarized: number;
  dataModelsSummarized: number;
  evidenceRefs: EvidenceRef[];
  sensitiveWarnings: string[];
};

export type WorkflowPlanReviewView = {
  graph: DemoWorkflowGraph;
  targetDurationSec: number;
  allowedDomains: string[];
  forbiddenPages: string[];
  redactionSelectors: string[];
  outputRequests: AssetKind[];
};

export type ExecutionPackagePreview = {
  packageID: string;
  packageDigest: string;
  graphDigest: string;
  sourceSummaryOnly: boolean;
  encrypted: boolean;
  humanApprovalRequired: boolean;
  ipAllowlistAcknowledged: boolean;
  credentialGrants: CredentialGrantPreview[];
  blockedReasons: string[];
};

export type CredentialGrantPreview = {
  grantID: string;
  kind: string;
  purpose: string;
  expiresAt: string;
  allowedDomains: string[];
  rawSecretVisible: false;
};

export type CloudRunStatus = "not_uploaded" | "queued" | "running" | "succeeded" | "failed";

export type ServerLifecycleStageID =
  | "local_generated"
  | "human_approved"
  | "upload_initialized"
  | "package_uploaded"
  | "server_intake"
  | "script_validation"
  | "sandbox_preparing"
  | "browser_execution"
  | "video_rendering"
  | "result_returned";

export type ServerLifecycleStageStatus = "pending" | "active" | "completed" | "failed" | "blocked";

export type ServerLifecycleStageView = {
  id: ServerLifecycleStageID;
  label: string;
  status: ServerLifecycleStageStatus;
  time?: string;
  progress: number;
  summary: string;
  errorCode?: string;
  artifactCount: number;
};

export type CloudArtifactSummary = {
  encrypted: number;
  sensitive: number;
  total: number;
};

export type ExecutionPackageUploadInitView = {
  uploadID: string;
  serverPublicKeyID: string;
  supportedCryptoSuites: string[];
  cascadeExecutionIPs: string[];
  maxEnvelopeBytes?: number;
  expiresAt?: string;
};

export type ExecutionPackageUploadView = {
  exchangePackageID: string;
  cloudJobID: string;
  status: CloudRunStatus;
};

export type CloudRunStatusView = {
  packageID: string;
  uploadID?: string;
  exchangePackageID?: string;
  cloudJobID?: string;
  resultPackageID?: string;
  status: CloudRunStatus;
  stage?: string;
  message?: string;
  stageHistory?: ServerLifecycleStageView[];
  failureSummary?: string;
  sandboxMetadata?: SandboxExecutionMetadata;
  artifactSummary?: CloudArtifactSummary;
  currentStep: string;
  progress: number;
  retryCount: number;
  lastError?: string;
  resultPackage?: RecordingResultPackage;
  failureDiagnostic?: ScriptFailureDiagnostic;
  repairRequest?: ScriptRepairRequest;
};

export type AssetReviewView = {
  assetID: string;
  kind: "video" | "step_docs" | "screenshot_pack";
  title: string;
  status: "generated" | "approved" | "changes_requested";
  uri: string;
  checksum: string;
  provenance: string;
};

export type RuntimeLogEntry = {
  id: string;
  time: string;
  level: "info" | "success" | "warning" | "error";
  message: string;
  detail?: string;
  node?: string;
  elapsedMS?: number;
};

export type ProjectWorkspaceView = {
  id: string;
  name: string;
  scenarioID: ScenarioID;
  stage: WorkspaceStage;
  productURL: string;
  targetAudience: string;
  status: "draft" | "understanding_ready" | "awaiting_approval" | "cloud_running" | "script_repair_required" | "asset_ready";
  inputBundle: ProjectInputBundle;
  sourceConnections: SourceConnectionView[];
  understanding: UnderstandingSummaryView;
  understandingReport?: MultimodalUnderstandingReport;
  planReview: WorkflowPlanReviewView;
  scriptDocument?: ExecutionScriptDocument;
  scriptMarkdown?: string;
  executableScriptBundle?: ExecutableRecordingScriptBundle;
  modelProvenance?: string[];
  runtimeLogs?: RuntimeLogEntry[];
  packagePreview: ExecutionPackagePreview;
  cloudRun: CloudRunStatusView;
  assets: AssetReviewView[];
};

export type RuntimeHealthView = {
  profile: string;
  databaseConfigured: boolean;
  localDataConfigured: boolean;
  resourceManifestLoaded: boolean;
  llmMode?: string;
  modelAdapterVersion?: string;
  sidecars: Record<string, boolean>;
  modelProviders: Record<string, ProviderCredentialStatus>;
  modelTaskRoutes: Record<string, ModelTaskRouteStatus>;
};

export type ModelDiagnosticResult = {
  provider: string;
  task?: string;
  model: string;
  adapterVersion: string;
  mode: string;
  baseURLHost: string;
  baseURLPath: string;
  configured: boolean;
  ok: boolean;
  httpStatus?: number;
  errorClass?: string;
  error?: string;
  latencyMS?: number;
  checkedAt: string;
};

export type ProviderCredentialStatus = {
  apiKeyEnv: string;
  apiKeySourceEnv?: string;
  apiKeyFallbackEnvs?: string[];
  configured: boolean;
  baseURLConfigured: boolean;
  defaultModelConfigured: boolean;
};

export type ModelTaskRouteStatus = {
  provider: string;
  model: string;
  providerOverride: string;
  modelOverride: string;
};

export type ApprovalChecklistState = {
  userApprovedPlan: boolean;
  ipAllowlistAcknowledged: boolean;
  sourceSummaryOnlyAcknowledged: boolean;
  credentialGrantAcknowledged: boolean;
  redactionsReviewed: boolean;
};
