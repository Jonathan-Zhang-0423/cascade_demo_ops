import type {
  AssetKind,
  DemoUseCase,
  DemoWorkflowGraph,
  EvidenceRef,
  ExecutableRecordingScriptBundle,
  ExecutionScriptDocument,
  AgentGraphTrace,
  MultimodalUnderstandingReport,
  ProjectInputBundle,
  ProjectIntelligencePack,
  RecordingResultPackage,
  SandboxExecutionMetadata,
  ScriptReadinessReport,
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

export type NavSection = "projects" | "project_library" | "repositories" | "new_demo" | "execution_packages" | "assets" | "editor" | "settings";

export type ProjectWorkstationView = "overview" | "evidence" | "plan" | "approval" | "execution" | "repair" | "assets" | "editor";
export type AssistantSurface = "projects" | "repositories";

export type ConfigurationSourceRefView = {
  ref: string;
  kind: "local_repository" | "github_repository" | "requirement_document" | "brand_asset" | string;
  label: string;
  url?: string;
};

export type ProjectConfigurationDraftView = {
  projectName?: string;
  productURL?: string;
  sources?: ConfigurationSourceRefView[];
  objective?: string;
  targetAudience?: string;
  targetDurationSec?: number;
  mustShow?: string[];
  mustNotShow?: string[];
  forbiddenPages?: string[];
  forbiddenData?: string[];
  brandTone?: string;
  credentialRefs?: string[];
  allowedDomains?: string[];
  version: number;
  hash: string;
  readiness: "incomplete" | "ready";
  missingFields?: string[];
  confirmed: boolean;
  confirmedAt?: string;
  analysisProjectID?: string;
};

export type ProjectConfigurationPatchView = Partial<Omit<ProjectConfigurationDraftView, "version" | "hash" | "readiness" | "missingFields" | "confirmed" | "confirmedAt" | "analysisProjectID">>;
export type AssistantContextView = { surface: AssistantSurface; scopeKey: string; projectID?: string; projectName?: string; repositoryID?: string; repositoryLabel?: string };
export type AssistantProposalKind = "configuration_patch" | "select_project_source" | "select_local_project" | "connect_github" | "attach_requirement_document" | "attach_brand_asset" | "store_demo_credential" | "confirm_configuration" | "start_local_analysis" | "open_workstation" | "continue_with_webpage_evidence" | "open_project" | "inspect_project" | "attach_repository" | "detach_repository" | "open_repository_form" | "prepare_understanding" | "prepare_execution";

export type ProductSourceBindingView = {
  schema_version: string;
  status: "matched" | "mismatched" | "unverified" | "not_applicable";
  effective_mode: "mixed" | "page_only" | "blocked";
  assessment_hash: string;
  decision?: string;
  sources?: Array<{ source_ref_id: string; status: string; matched_kinds?: string[]; conflicting_kinds?: string[] }>;
};
export type AssistantEvidenceView = { id: string; label: string; source: string; summary: string; confidence?: number };
export type AssistantProposalView = { id: string; kind: AssistantProposalKind; title: string; description: string; targetID?: string; targetWorkstation?: ProjectWorkstationView; patch?: ProjectConfigurationPatchView; baseVersion: number; idempotencyKey: string; requiresConfirmation: boolean; status: "available" | "confirmed" | "dismissed"; executionResult?: Record<string, unknown> };
export type AssistantMessageView = { id: string; role: "agent" | "user" | "system"; kind: "answer" | "evidence" | "proposal" | "status" | "error"; text: string; generationSource?: "llm" | "deterministic_fallback" | "manual"; modelProvider?: string; modelName?: string; fallbackReason?: string; createdAt: string; targetWorkstation?: ProjectWorkstationView; evidence?: AssistantEvidenceView[]; proposals?: AssistantProposalView[] };
export type AssistantEventView = { id: string; sessionID: string; type: string; text: string; createdAt: string };
export type AssistantNextActionView = { kind: string; title: string; description: string; primaryLabel?: string; proposalID?: string; targetWorkstation?: ProjectWorkstationView; requiresUserAction: boolean; blocked: boolean; missingFields?: string[] };
export type AssistantSessionView = { id: string; context: AssistantContextView; status: "idle" | "thinking" | "waiting_for_user" | "awaiting_confirmation" | "error"; activeWorkstation?: ProjectWorkstationView; workstationTitle?: string; workstationStatus?: string; nextAction: AssistantNextActionView; configuration: ProjectConfigurationDraftView; messages: AssistantMessageView[]; lastEventID?: string };
export type ProjectSummaryView = { id: string; name: string; productURL: string; stage: WorkspaceStage; status: ProjectWorkspaceView["status"]; assetCount: number; generatedAssetCount: number; createdAt?: string; updatedAt?: string };

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
  kind: "product_url" | "local_repo" | "github_repo" | "requirement_doc" | "screenshot" | "release_note" | "credential";
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
  buildStatus?: "draft" | "approved";
  approvalSubjectDigest?: string;
  confidenceAssessmentHash?: string;
  readiness?: "blocked" | "review_required" | "ready";
  confidenceScore?: number;
  confidenceWarnings?: string[];
  totalBytes?: number;
  sectionBytes?: Record<string, number>;
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
	lastEventID?: string;
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
	resultReview?: ResultReviewState;
	resultDownloaded?: boolean;
	editorSessionID?: string;
	editorMaterializationMessage?: string;
};

export type ResultReviewDecision = "approved" | "reedit_requested" | "rerecord_requested";

export type ResultReviewState = {
	decision: ResultReviewDecision;
	reviewID?: string;
	revisionID?: string;
	revisionAction?: "reedit" | "rerecord";
	summary?: string;
	updatedAt: string;
};

export type AssetReviewView = {
  assetID: string;
  kind: "video" | "step_docs" | "screenshot_pack";
  title: string;
  status: "generated" | "approved" | "changes_requested";
  uri: string;
  mediaURL?: string;
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
  projectIntelligence?: ProjectIntelligencePack;
  scriptReadiness?: ScriptReadinessReport;
  agentGraphTrace?: AgentGraphTrace;
  sourceBinding?: ProductSourceBindingView;
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
  llmProxyConfigured?: boolean;
  llmProxyHost?: string;
  modelAdapterVersion?: string;
  sidecars: Record<string, boolean>;
  modelProviders: Record<string, ProviderCredentialStatus>;
  modelTaskRoutes: Record<string, ModelTaskRouteStatus>;
  cloudExchange?: CloudExchangeStatus;
  appCapabilities?: AppCapabilitiesStatus;
};

export type AppCapabilitiesStatus = {
  developerUI: boolean;
  demoAssetGenerationConsole: boolean;
  videoEditor: boolean;
  localPackageGeneration: boolean;
  stagePlanReview: boolean;
  executionPackageApproval: boolean;
  approvedPackageUpload: boolean;
  resultVideoDownload: boolean;
  errorReportDownload: boolean;
  serverRecordingRequired: boolean;
  localRecordingExecution: boolean;
  localRecordingScope: string;
  videoWorkerRole: string;
};

export type CloudExchangeStatus = {
  configured: boolean;
  exchangeDiscovered: boolean;
  installationPaired: boolean;
  sessionValid: boolean;
  baseURLHost?: string;
  baseURLPath?: string;
  serverKeyID?: string;
  installIDSuffix?: string;
  authMode: "installation_session" | "dev_token" | "unpaired" | string;
  environment?: string;
  devPlaintext?: boolean;
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
