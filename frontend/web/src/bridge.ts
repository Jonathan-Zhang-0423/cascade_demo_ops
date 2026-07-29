import type {
  CloudArtifactSummary,
  ExecutionPackageUploadInitView,
  ExecutionPackageUploadView,
  ModelDiagnosticResult,
  AssistantContextView,
  AssistantEventView,
  AssistantSessionView,
  ConfigurationSourceRefView,
  ProjectSummaryView,
  ProjectWorkspaceView,
  ProductSourceBindingView,
  RuntimeHealthView,
  RuntimeLogEntry,
  ScenarioID,
  ServerLifecycleStageID,
  ServerLifecycleStageStatus,
  ServerLifecycleStageView,
} from "./domain";
import type {
  AssetKind,
  DemoWorkflowGraph,
  ExecutableRecordingScriptBundle,
  ExecutionScriptDocument,
  MultimodalUnderstandingReport,
  ProjectInputBundle,
  RecordingResultPackage,
  SandboxExecutionMetadata,
  SandboxPolicy,
  AgentGraphTrace,
  ScriptFailureDiagnostic,
  ScriptStep,
  ProjectIntelligencePack,
  ScriptReadinessReport,
  ClientExecutionPackage,
  ExchangeEnvelope,
  EncryptedPayloadRef,
} from "../../src/types/workflowGraph";
import { createWorkspace } from "./mockWorkspace";
import { getScenarioTemplate } from "./scenarios";
import { serverLifecycleStageLabels, serverLifecycleStageOrder } from "./workflow";

export type BridgeResult<T> = {
  ok: boolean;
  data?: T;
  error?: string;
  errorInfo?: LocalBridgeErrorInfo | undefined;
};

export type ProjectCreationInput = {
  userInput: LocalUserInput;
  scenarioID?: ScenarioID;
};

export type DesktopBridgeClient = {
  mode: "mock" | "local";
  runtimeHealth(): Promise<BridgeResult<RuntimeHealthView>>;
  githubCredentialStatus(): Promise<BridgeResult<GitHubCredentialStatus>>;
  storeGitHubToken(token: string): Promise<BridgeResult<GitHubCredentialStatus>>;
  deleteGitHubToken(): Promise<BridgeResult<GitHubCredentialStatus>>;
  desktopUpdateStatus(): Promise<BridgeResult<DesktopUpdateStatus>>;
  checkDesktopUpdate(): Promise<BridgeResult<DesktopUpdateStatus>>;
  applyDesktopUpdate(): Promise<BridgeResult<{ started: boolean }>>;
  modelDiagnostics(): Promise<BridgeResult<ModelDiagnosticResult[]>>;
  preflightExecutionPackage(workspace: ProjectWorkspaceView): Promise<BridgeResult<CloudPackagePreflightView>>;
  editorMaterialization(workspace: ProjectWorkspaceView): Promise<BridgeResult<EditorSessionMaterializationView>>;
  browserAgentAcceptance(): Promise<BridgeResult<BrowserAgentAcceptanceView>>;
  runBrowserAgentAcceptance(): Promise<BridgeResult<BrowserAgentAcceptanceView>>;
  browserAgentBusinessAcceptance(): Promise<BridgeResult<BrowserAgentBusinessAcceptanceView>>;
  runBrowserAgentBusinessAcceptance(): Promise<BridgeResult<BrowserAgentBusinessAcceptanceView>>;
  executionEvents(projectID: string, afterID?: string): Promise<BridgeResult<RuntimeLogEntry[]>>;
  createProject(input: ScenarioID | ProjectCreationInput): Promise<BridgeResult<ProjectWorkspaceView>>;
  listProjects(): Promise<BridgeResult<ProjectSummaryView[]>>;

  listProjectSummaries(): Promise<BridgeResult<ProjectSummaryView[]>>;
  loadProject(projectID: string): Promise<BridgeResult<ProjectWorkspaceView>>;
  getSourceBinding(projectID: string): Promise<BridgeResult<ProductSourceBindingView>>;
  continueWithWebpageEvidence(projectID: string, assessmentHash: string, idempotencyKey: string): Promise<BridgeResult<ProjectWorkspaceView>>;
  archiveProject(projectID: string): Promise<BridgeResult<{ archived: boolean }>>;
  deleteProject(projectID: string): Promise<BridgeResult<{ deleted: boolean }>>;
  createAssistantSession(context: AssistantContextView): Promise<BridgeResult<AssistantSessionView>>;
  getAssistantSession(sessionID: string): Promise<BridgeResult<AssistantSessionView>>;
  submitAssistantTurn(sessionID: string, message: string, idempotencyKey?: string, safeSelections?: { selectedSources?: ConfigurationSourceRefView[]; credentialRefs?: string[] }): Promise<BridgeResult<AssistantSessionView>>;
  listAssistantEvents(sessionID: string, afterID?: string): Promise<BridgeResult<AssistantEventView[]>>;
  confirmAssistantProposal(sessionID: string, proposalID: string, baseVersion?: number, idempotencyKey?: string): Promise<BridgeResult<AssistantSessionView>>;
  dismissAssistantProposal(sessionID: string, proposalID: string, baseVersion?: number, idempotencyKey?: string): Promise<BridgeResult<AssistantSessionView>>;
  cancelAssistantSession(sessionID: string): Promise<BridgeResult<AssistantSessionView>>;
  selectLocalProjectDirectory(): Promise<BridgeResult<ConfigurationSourceRefView>>;
  selectRequirementDocuments(): Promise<BridgeResult<ConfigurationSourceRefView[]>>;
  selectBrandAssets(): Promise<BridgeResult<ConfigurationSourceRefView[]>>;
  storeDemoCredential(ref: string, username: string, password: string): Promise<BridgeResult<{ secretRef: string; configured: boolean }>>;
  saveWorkspace(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  saveProjectInputs(projectID: string, inputs: ProjectInputBundle): Promise<BridgeResult<ProjectWorkspaceView>>;
  getUnderstandingReport(projectID: string): Promise<BridgeResult<MultimodalUnderstandingReport>>;
  getExecutionScriptDocument(projectID: string): Promise<BridgeResult<ExecutionScriptDocument>>;
  getExecutionScriptMarkdown(projectID: string): Promise<BridgeResult<{ markdown: string }>>;
  getExecutableScriptBundle(projectID: string): Promise<BridgeResult<ExecutableRecordingScriptBundle>>;
  buildExecutionPackagePreview(workspace: ProjectWorkspaceView, options?: BridgeRunOptions): Promise<BridgeResult<ProjectWorkspaceView>>;
  runProductLifecycle(workspace: ProjectWorkspaceView, options?: BridgeRunOptions): Promise<BridgeResult<ProjectWorkspaceView>>;
  initExecutionPackageUpload(workspace: ProjectWorkspaceView): Promise<BridgeResult<ExecutionPackageUploadInitView>>;
  uploadExecutionPackage(workspace: ProjectWorkspaceView): Promise<BridgeResult<ExecutionPackageUploadView>>;
  pollExecutionPackageStatus(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  getResultPackage(workspace: ProjectWorkspaceView): Promise<BridgeResult<RecordingResultPackage>>;
  approveAndUploadPackage(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  pollCloudRun(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  simulateCloudFailure(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  repairFailedScript(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  ackResultPackage(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  acknowledgeResult(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  reviewResult(workspace: ProjectWorkspaceView, decision: "approved" | "reedit_requested" | "rerecord_requested", summary?: string): Promise<BridgeResult<ProjectWorkspaceView>>;
};

export type BrowserAgentAcceptanceView = {
  ready: boolean;
  can_run: boolean;
  message: string;
  report_path?: string;
  report?: {
    schema_version: string;
    generated_at: string;
    runtime: string;
    strict_gate: string;
    scenarios: BrowserAgentAcceptanceScenarioView[];
  };
};

export type BrowserAgentAcceptanceScenarioView = {
  id: string;
  description: string;
  expected: string;
  actual: string;
  verdict: string;
  action_executed: boolean;
  evidence: Array<{ id: string; kind: string; uri: string; mime_type: string; sha256: string; size_bytes: number }>;
  assertions: Array<{ kind: string; passed: boolean; actual?: string }>;
  stop_reason?: string;
};

export type BrowserAgentBusinessAcceptanceView = {
  ready: boolean;
  can_run: boolean;
  message: string;
  report_path?: string;
  report?: {
    schema_version: string;
    generated_at: string;
    runtime: string;
    strict_gate: string;
    package_id: string;
    business_flow: string;
    stages: BrowserAgentAcceptanceScenarioView[];
    editor_materialization: { ready: boolean; created: boolean; session_id?: string; message: string };
  };
};

export type CloudPackagePreflightView = {
  valid: boolean;
  runtime?: string;
  package_id?: string;
  stage_count?: number;
  required_checks?: number;
  allowed_domains?: string[];
  message: string;
};

export type EditorSessionMaterializationView = {
  ready: boolean;
  created: boolean;
  session_id?: string;
  message: string;
};

export type BridgeRunOptions = {
  demoCredentials?: {
    username?: string;
    password?: string;
  };
  onCloudStatus?: (workspace: ProjectWorkspaceView) => void;
};

export type GitHubCredentialStatus = {
  configured: boolean;
};

export type DesktopUpdateStatus = {
  configured: boolean;
  currentVersion?: string;
  availableVersion?: string;
  channel?: string;
  releaseNotes?: string;
  artifactFileName?: string;
  sizeBytes?: number;
  updateAvailable: boolean;
  installReady: boolean;
};

type LocalDesktopUpdateStatus = {
  configured: boolean;
  current_version?: string;
  available_version?: string;
  channel?: string;
  release_notes?: string;
  artifact_file_name?: string;
  size_bytes?: number;
  update_available: boolean;
  install_ready: boolean;
};

type LocalBridgeResponse<T> = {
  ok: boolean;
  data?: T;
  error?: string;
  error_info?: LocalBridgeErrorInfo;
};

type LocalBridgeErrorInfo = {
  code: string;
  message: string;
  details?: LocalBridgeErrorDetail[];
  correlation_id?: string;
  retryable?: boolean;
};

type LocalBridgeErrorDetail = {
  field?: string;
  reason?: string;
  message: string;
  hint?: string;
};

type LocalResultReviewRecord = {
	review_id: string;
	decision: "approved" | "reedit_requested" | "rerecord_requested";
	summary?: string;
	reviewed_at: string;
};

type LocalResultRevisionRecord = {
	revision_id: string;
	resolved_action: "reedit" | "rerecord";
	status: string;
	requested_at: string;
};

type LocalRuntimeHealth = {
  profile: string;
  database_configured?: boolean;
  local_data_configured?: boolean;
  resource_manifest_loaded?: boolean;
  sidecars?: Record<string, boolean>;
  llm_mode?: string;
  model_adapter_version?: string;
  model_providers?: Record<string, {
    api_key_env: string;
    api_key_source_env?: string;
    api_key_fallback_envs?: string[];
    configured: boolean;
    base_url_configured: boolean;
    default_model_configured: boolean;
  }>;
  model_task_routes?: Record<string, {
    provider: string;
    model: string;
    provider_override: string;
    model_override: string;
  }>;
  cloud_exchange?: {
    configured: boolean;
    exchange_discovered?: boolean;
    installation_paired?: boolean;
    session_valid?: boolean;
    base_url_host?: string;
    base_url_path?: string;
    server_key_id?: string;
    install_id_suffix?: string;
    auth_mode?: string;
    environment?: string;
    dev_plaintext?: boolean;
  };
  app_capabilities?: {
    developer_ui?: boolean;
    demo_asset_generation_console?: boolean;
    video_editor?: boolean;
    local_package_generation?: boolean;
    stage_plan_review?: boolean;
    execution_package_approval?: boolean;
    approved_package_upload?: boolean;
    result_video_download?: boolean;
    error_report_download?: boolean;
    server_recording_required?: boolean;
    local_recording_execution?: boolean;
    local_recording_scope?: string;
    video_worker_role?: string;
  };
};

type LocalModelDiagnostic = {
  provider: string;
  task?: string;
  model: string;
  adapter_version: string;
  mode: string;
  base_url_host: string;
  base_url_path: string;
  configured: boolean;
  ok: boolean;
  http_status?: number;
  error_class?: string;
  error?: string;
  latency_ms?: number;
  checked_at: string;
};

type LocalCascadeState = {
  project_id: string;
  current_node?: string;
  status?: string;
  project_context?: LocalProjectContext;
  understanding_report?: MultimodalUnderstandingReport;
  project_intelligence?: ProjectIntelligencePack;
  script_readiness_report?: ScriptReadinessReport;
  agent_graph_trace?: AgentGraphTrace;
  verified_interaction_plan?: ProjectIntelligencePack["verified_interaction_plan"];
  missing_evidence_report?: ProjectIntelligencePack["missing_evidence_report"];
  product_map?: LocalProductMap;
  workflow_graph?: DemoWorkflowGraph;
  script_document?: ExecutionScriptDocument;
  script_markdown?: string;
  executable_script_bundle?: ExecutableRecordingScriptBundle;
  error_message?: string;
  source_binding?: ProductSourceBindingView;
};

type LocalClientExecutionPackageBuild = {
  org_id: string;
  project_id: string;
  package: ClientExecutionPackage;
  envelope?: ExchangeEnvelope;
  payload_ref?: EncryptedPayloadRef;
  build_status: "draft" | "approved";
  approval_subject_digest_sha256: string;
  package_digest_sha256: string;
  size_report: {
    algorithm_version: string;
    total_bytes: number;
    section_bytes: Record<string, number>;
    stage_count: number;
    evidence_count: number;
    selector_count: number;
  };
};

type LocalCloudLifecycleResult = {
  state?: LocalCascadeState;
  build?: LocalClientExecutionPackageBuild | undefined;
  init: LocalExecutionPackageInitResponse;
  upload: LocalExecutionPackageUploadResponse;
  status: LocalExecutionPackageStatusResponse;
  result?: RecordingResultPackage;
  ack?: LocalResultPackageAckResponse;
  cloud_base_url?: string;
};

type LocalProductRunPrepareResult = {
  state: LocalCascadeState;
  build?: LocalClientExecutionPackageBuild | undefined;
};

type LocalCloudUploadInitResult = {
  build?: LocalClientExecutionPackageBuild | undefined;
  init: LocalExecutionPackageInitResponse;
  cloud_base_url?: string;
};

type LocalCloudUploadPackageResult = {
  build?: LocalClientExecutionPackageBuild | undefined;
  upload: LocalExecutionPackageUploadResponse;
  cloud_base_url?: string;
};

type LocalExecutionPackageInitResponse = {
  upload_id: string;
  server_public_key_id: string;
  supported_crypto_suites?: string[];
  cascade_execution_ips: string[];
  max_envelope_bytes?: number | undefined;
  expires_at?: string | undefined;
};

type LocalExecutionPackageUploadResponse = {
  exchange_package_id: string;
  cloud_job_id?: string | undefined;
  status: string;
};

type LocalExecutionPackageStatusResponse = {
  exchange_package_id: string;
  cloud_job_id?: string | undefined;
  status: string;
  stage?: string;
  message?: string;
  progress_percent?: number;
  stage_history?: LocalExecutionStageEvent[];
  result_package_id?: string;
  result_summary?: LocalExecutionResultSummary;
  failure_summary?: LocalExecutionFailureSummary;
  error?: { code: string; message: string; retryable?: boolean };
  updated_at?: string;
};

type LocalExecutionStageEvent = {
  event_id?: string;
  stage: string;
  status?: string;
  message?: string;
  progress_percent?: number;
  updated_at?: string;
};

type LocalExecutionDeliverable = {
  id?: string;
  kind?: string;
  role?: string;
  uri?: string;
  download_url?: string;
  mime_type?: string;
  sha256?: string;
  size_bytes?: number;
  source_node_id?: string;
  include_in_demo?: boolean;
  sensitive?: boolean;
};

type LocalExecutionResultSummary = {
  result_id?: string;
  result_status?: string;
  delivery_status?: string;
  pass_rate?: number;
  step_count?: number;
  demo_video_count?: number;
  screenshot_count?: number;
  raw_recording_count?: number;
  trace_count?: number;
  primary_demo_video_uri?: string;
  raw_recording_uri?: string;
  deliverables?: LocalExecutionDeliverable[];
  acked_at?: string;
};

type LocalExecutionFailureSummary = {
  code?: string;
  message?: string;
  failed_stage?: string;
  failed_node_id?: string;
  current_url?: string;
  page_title?: string;
  failure_screenshot_uri?: string;
  failure_trace_uri?: string;
  retryable?: boolean;
};

type LocalResultPackageAckResponse = {
  result_package_id: string;
  status: string;
  delivery_status?: string;
};

type LocalCloudDeliverableDownloadResult = {
	artifact_id: string;
	local_path: string;
	size_bytes?: number;
	sha256: string;
	expected_sha256?: string;
	checksum_verified: boolean;
};

type LocalExecutionEvent = {
  id: number;
  project_id: string;
  level: "info" | "success" | "warning" | "error";
  node?: string;
  message: string;
  detail?: string;
  elapsed_ms?: number;
  created_at: string;
};

type LocalProjectContext = {
  id: string;
  name?: string;
  mode?: string;
  product_url?: string;
  git_repo_url?: string;
  local_repo_path?: string;
  product_description?: string;
  target_audience?: string;
  forbidden_pages?: string[];
  forbidden_data?: string[];
  inputs?: ProjectInputBundle;
  source_binding?: ProductSourceBindingView;
};

type LocalProductMap = {
  id?: string;
  summary?: string;
  pages?: Array<unknown>;
  features?: Array<unknown>;
  components?: Array<unknown>;
  data_models?: Array<unknown>;
};

export type LocalUserInput = {
  mode: "desktop";
  product_url: string;
  local_repo_path?: string;
  git_repo_url?: string;
  product_description: string;
  requirement_documents?: ProjectInputBundle["requirement_documents"];
  webpage_screenshots?: ProjectInputBundle["webpage_screenshots"];
  target_audience: string;
  brand_tone?: string;
  must_show?: string[];
  must_not_show?: string[];
  forbidden_pages?: string[];
  forbidden_data?: string[];
  demo_username?: string;
  demo_password?: string;
};

const defaultLocalBridgeURL = "";

export function createBridgeClient(): DesktopBridgeClient {
  if (import.meta.env.VITE_CASCADE_BRIDGE === "local") {
    return createLocalBridgeClient(import.meta.env.VITE_CASCADE_BRIDGE_URL || defaultLocalBridgeURL);
  }
  return createMockBridgeClient();
}

export function createLocalBridgeClient(baseURL: string = defaultLocalBridgeURL): DesktopBridgeClient {
  const projects = new Map<string, ProjectWorkspaceView>();
  const cloudBuilds = new Map<string, LocalClientExecutionPackageBuild>();
  const orgID = import.meta.env.VITE_CASCADE_ORG_ID || "org_desktop";
  return {
    mode: "local",
    async runtimeHealth() {
      const result = await requestLocal<LocalRuntimeHealth>(baseURL, "/v1/desktop/runtime-health");
      if (!result.ok || !result.data) {
        return { ok: false, error: result.error ?? "本地运行时状态不可用" };
      }
      return ok(runtimeHealthFromLocal(result.data));
    },
    async githubCredentialStatus() {
      return requestLocal<GitHubCredentialStatus>(baseURL, "/v1/desktop/github-credential");
    },
    async storeGitHubToken(token) {
      if (!token.trim()) {
        return { ok: false, error: "请输入 GitHub fine-grained token" };
      }
      return requestLocal<GitHubCredentialStatus>(baseURL, "/v1/desktop/github-credential", {
        method: "POST",
        body: JSON.stringify({ token }),
      });
    },
    async deleteGitHubToken() {
      return requestLocal<GitHubCredentialStatus>(baseURL, "/v1/desktop/github-credential", { method: "DELETE" });
    },
    async desktopUpdateStatus() {
      return mapDesktopUpdateResult(await requestLocal<LocalDesktopUpdateStatus>(baseURL, "/v1/desktop/update"));
    },
    async checkDesktopUpdate() {
      return mapDesktopUpdateResult(await requestLocal<LocalDesktopUpdateStatus>(baseURL, "/v1/desktop/update/check", { method: "POST" }));
    },
    async applyDesktopUpdate() {
      return requestLocal<{ started: boolean }>(baseURL, "/v1/desktop/update/apply", { method: "POST" });
    },
    async modelDiagnostics() {
      const result = await requestLocal<LocalModelDiagnostic[]>(baseURL, "/v1/desktop/model-diagnostics", { method: "POST" });
      if (!result.ok || !result.data) {
        return { ok: false, error: result.error ?? "模型诊断不可用" };
      }
      return ok(result.data.map(modelDiagnosticFromLocal));
    },
    async preflightExecutionPackage(workspace) {
	  const preview = workspace.packagePreview;
      return requestLocal<CloudPackagePreflightView>(baseURL, `/v1/desktop/projects/${encodeURIComponent(workspace.id)}/cloud/preflight`, {
        method: "POST",
		body: JSON.stringify({ org_id: orgID, approval_subject_digest_sha256: preview.approvalSubjectDigest ?? preview.packageDigest, confidence_assessment_hash: preview.confidenceAssessmentHash ?? preview.packageDigest, risk_confirmed: true, idempotency_key: `preflight-${workspace.id}-${preview.approvalSubjectDigest ?? preview.packageDigest}` }),
      });
    },
    async editorMaterialization(workspace) {
      const resultPackageID = workspace.cloudRun.resultPackageID ?? workspace.cloudRun.resultPackage?.result_id;
      if (!resultPackageID) return { ok: false, error: "缺少结果包，无法确认待编辑素材" };
      const query = `?org_id=${encodeURIComponent(orgID)}&result_package_id=${encodeURIComponent(resultPackageID)}`;
      return requestLocal<EditorSessionMaterializationView>(baseURL, `/v1/desktop/projects/${encodeURIComponent(workspace.id)}/cloud/editor-materialization${query}`);
    },
    async browserAgentAcceptance() {
      return requestLocal<BrowserAgentAcceptanceView>(baseURL, "/v1/desktop/browser-agent-acceptance");
    },
    async runBrowserAgentAcceptance() {
      return requestLocal<BrowserAgentAcceptanceView>(baseURL, "/v1/desktop/browser-agent-acceptance/run", { method: "POST" });
    },
    async browserAgentBusinessAcceptance() {
      return requestLocal<BrowserAgentBusinessAcceptanceView>(baseURL, "/v1/desktop/browser-agent-business-acceptance");
    },
    async runBrowserAgentBusinessAcceptance() {
      return requestLocal<BrowserAgentBusinessAcceptanceView>(baseURL, "/v1/desktop/browser-agent-business-acceptance/run", { method: "POST" });
    },
    async executionEvents(projectID, afterID) {
      const query = afterID ? `?after=${encodeURIComponent(afterID)}` : "";
      const result = await requestLocal<LocalExecutionEvent[]>(baseURL, `/v1/desktop/projects/${encodeURIComponent(projectID)}/execution-events${query}`);
      if (!result.ok || !result.data) {
        return { ok: false, error: result.error ?? "运行日志不可用" };
      }
      return ok(result.data.map(runtimeLogFromLocalEvent));
    },
    async createProject(input) {
      const scenarioID = typeof input === "string" ? input : input.scenarioID ?? "product_demo";
      if (typeof input === "string") {
        const workspace = createWorkspace(scenarioID);
        projects.set(workspace.id, workspace);
        return ok(workspace);
      }
      const result = await requestLocal<LocalCascadeState>(baseURL, "/v1/desktop/projects", {
        method: "POST",
        body: JSON.stringify(input.userInput),
      });
      if (!result.ok || !result.data) return { ok: false, error: result.error ?? "创建项目失败" };
      const workspace = workspaceFromCascadeState(result.data, createWorkspace(scenarioID));
      projects.set(workspace.id, workspace);
      return ok(workspace);
    },
    async listProjects() {
      return this.listProjectSummaries();
    },
    async listProjectSummaries() {
      const result = await requestLocal<Array<{ id: string; name: string; product_url?: string; stage: ProjectSummaryView["stage"]; status: ProjectSummaryView["status"]; asset_count: number; generated_asset_count: number; created_at?: string; updated_at?: string }>>(baseURL, "/v1/desktop/projects");
      if (!result.ok || !result.data) return { ok: false, error: result.error ?? "项目列表不可用" };
      return ok(result.data.map((item) => ({ id: item.id, name: item.name, productURL: item.product_url ?? "", stage: item.stage, status: item.status, assetCount: item.asset_count, generatedAssetCount: item.generated_asset_count, ...(item.created_at ? { createdAt: item.created_at } : {}), ...(item.updated_at ? { updatedAt: item.updated_at } : {}) })));
    },
    async archiveProject(projectID) {
      return requestLocal<{ archived: boolean }>(baseURL, `/v1/desktop/projects/${encodeURIComponent(projectID)}/archive`, { method: "POST" });
    },
    async deleteProject(projectID) {
      projects.delete(projectID);
      return requestLocal<{ deleted: boolean }>(baseURL, `/v1/desktop/projects/${encodeURIComponent(projectID)}`, { method: "DELETE" });
    },
    async createAssistantSession(context) {
      return requestLocal<AssistantSessionView>(baseURL, "/v1/desktop/assistant/sessions", { method: "POST", body: JSON.stringify({ context }) });
    },
    async getAssistantSession(sessionID) {
      return requestLocal<AssistantSessionView>(baseURL, `/v1/desktop/assistant/sessions/${encodeURIComponent(sessionID)}`);
    },
    async submitAssistantTurn(sessionID, message, idempotencyKey, safeSelections) {
      return requestLocal<AssistantSessionView>(baseURL, `/v1/desktop/assistant/sessions/${encodeURIComponent(sessionID)}/turns`, { method: "POST", body: JSON.stringify({ message, idempotencyKey, ...safeSelections }) });
    },
    async listAssistantEvents(sessionID, afterID) {
      const query = afterID ? `?after=${encodeURIComponent(afterID)}` : "";
      return requestLocal<AssistantEventView[]>(baseURL, `/v1/desktop/assistant/sessions/${encodeURIComponent(sessionID)}/events${query}`);
    },
    async confirmAssistantProposal(sessionID, proposalID, baseVersion, idempotencyKey) {
      return requestLocal<AssistantSessionView>(baseURL, `/v1/desktop/assistant/sessions/${encodeURIComponent(sessionID)}/proposals/${encodeURIComponent(proposalID)}/confirm`, { method: "POST", body: JSON.stringify({ baseVersion, idempotencyKey }) });
    },
    async dismissAssistantProposal(sessionID, proposalID, baseVersion, idempotencyKey) {
      return requestLocal<AssistantSessionView>(baseURL, `/v1/desktop/assistant/sessions/${encodeURIComponent(sessionID)}/proposals/${encodeURIComponent(proposalID)}/dismiss`, { method: "POST", body: JSON.stringify({ baseVersion, idempotencyKey }) });
    },
    async cancelAssistantSession(sessionID) {
      return requestLocal<AssistantSessionView>(baseURL, `/v1/desktop/assistant/sessions/${encodeURIComponent(sessionID)}/cancel`, { method: "POST" });
    },
    async selectLocalProjectDirectory() {
      return callWailsBridge<ConfigurationSourceRefView>("SelectLocalProjectDirectory");
    },
    async selectRequirementDocuments() {
      return callWailsBridge<ConfigurationSourceRefView[]>("SelectRequirementDocuments");
    },
    async selectBrandAssets() {
      return callWailsBridge<ConfigurationSourceRefView[]>("SelectBrandAssets");
    },
    async storeDemoCredential(ref, username, password) {
      return callWailsBridge<{ secretRef: string; configured: boolean }>("StoreDemoCredential", ref, username, password);
    },
    async saveProjectInputs(projectID, inputs) {
      const result = await requestLocal<LocalProjectContext>(baseURL, `/v1/desktop/projects/${encodeURIComponent(projectID)}/inputs`, {
        method: "POST",
        body: JSON.stringify(inputs),
      });
      if (!result.ok) return { ok: false, error: result.error ?? "项目输入保存失败" };
      projects.delete(projectID);
      return this.loadProject(projectID);
    },
    async loadProject(projectID) {
      const cached = projects.get(projectID);
      if (cached) {
        return ok(cached);
      }
      const result = await requestLocal<LocalCascadeState>(baseURL, `/v1/desktop/projects/${encodeURIComponent(projectID)}`);
      if (!result.ok || !result.data) {
        return { ok: false, error: result.error ?? "未找到项目" };
      }
      const workspace = workspaceFromCascadeState(result.data, createWorkspace("product_demo"));
      projects.set(workspace.id, workspace);
      return ok(workspace);
    },
    async getSourceBinding(projectID) {
      return requestLocal<ProductSourceBindingView>(baseURL, `/v1/desktop/projects/${encodeURIComponent(projectID)}/source-binding`);
    },
    async continueWithWebpageEvidence(projectID, assessmentHash, idempotencyKey) {
      const result = await requestLocal<LocalCascadeState>(baseURL, `/v1/desktop/projects/${encodeURIComponent(projectID)}/source-binding/decisions`, {
        method: "POST", body: JSON.stringify({ decision: "continue_page_only", assessment_hash: assessmentHash, idempotency_key: idempotencyKey }),
      });
      if (!result.ok || !result.data) return bridgeFailure(result.error ?? "无法切换到仅网页证据模式", result.errorInfo);
      const workspace = workspaceFromCascadeState(result.data, projects.get(projectID) ?? createWorkspace("product_demo"));
      projects.set(projectID, workspace);
      return ok(workspace);
    },
    async saveWorkspace(workspace) {
      projects.set(workspace.id, workspace);
      return ok(workspace);
    },
    async getUnderstandingReport(projectID) {
      const loaded = await this.loadProject(projectID);
      return loaded.ok && loaded.data?.understandingReport ? ok(loaded.data.understandingReport) : { ok: false, error: loaded.error ?? "理解报告尚未生成" };
    },
    async getExecutionScriptDocument(projectID) {
      const loaded = await this.loadProject(projectID);
      return loaded.ok && loaded.data?.scriptDocument ? ok(loaded.data.scriptDocument) : { ok: false, error: loaded.error ?? "脚本文档尚未生成" };
    },
    async getExecutionScriptMarkdown(projectID) {
      const loaded = await this.loadProject(projectID);
      return loaded.ok && loaded.data?.scriptMarkdown ? ok({ markdown: loaded.data.scriptMarkdown }) : { ok: false, error: loaded.error ?? "审批文档尚未生成" };
    },
    async getExecutableScriptBundle(projectID) {
      const loaded = await this.loadProject(projectID);
      return loaded.ok && loaded.data?.executableScriptBundle ? ok(loaded.data.executableScriptBundle) : { ok: false, error: loaded.error ?? "可执行脚本包尚未生成" };
    },
    async buildExecutionPackagePreview(workspace, options) {
      const userInput = userInputFromWorkspace(workspace, options);
      const result = await requestLocal<LocalCascadeState>(baseURL, `/v1/desktop/projects/${encodeURIComponent(workspace.id)}/execution-package`, {
        method: "POST",
        body: JSON.stringify({ user_input: userInput }),
      });
      if (!result.ok || !result.data) {
        return { ok: false, error: result.error ?? "执行包生成失败" };
      }
      const generated = workspaceFromCascadeState(result.data, workspace);
      projects.set(generated.id, generated);
      return ok(generated);
    },
    async runProductLifecycle(workspace, options) {
      const userInput = userInputFromWorkspace(workspace, options);
      const prepared = await requestLocal<LocalProductRunPrepareResult>(baseURL, `/v1/desktop/projects/${encodeURIComponent(workspace.id)}/product-run/prepare`, {
        method: "POST",
        body: JSON.stringify({
          user_input: userInput,
          org_id: orgID,
        }),
      });
      if (!prepared.ok || !prepared.data) {
        return bridgeFailure(prepared.error ?? "本地产品实战准备失败", prepared.errorInfo);
      }
      let current = workspaceFromCascadeState(prepared.data.state, workspace);
      if (prepared.data.build) {
        cloudBuilds.set(current.id, prepared.data.build);
        current = workspaceWithPreparedBuild(current, prepared.data.build);
      }
      projects.set(current.id, current);
	  return ok(current);
    },
    async initExecutionPackageUpload(workspace) {
	  const preview = workspace.packagePreview;
      const result = await requestLocal<LocalCloudUploadInitResult>(baseURL, `/v1/desktop/projects/${encodeURIComponent(workspace.id)}/cloud/init`, {
        method: "POST",
		body: JSON.stringify({
		  org_id: orgID,
		  approval_subject_digest_sha256: preview.approvalSubjectDigest ?? preview.packageDigest,
		  confidence_assessment_hash: preview.confidenceAssessmentHash ?? preview.packageDigest,
		  risk_confirmed: true,
		  idempotency_key: `approve-${workspace.id}-${preview.approvalSubjectDigest ?? preview.packageDigest}`,
		}),
      });
      if (!result.ok || !result.data) {
        return bridgeFailure(result.error ?? "初始化服务器上传会话失败", result.errorInfo);
      }
      if (result.data.build) {
        cloudBuilds.set(workspace.id, result.data.build);
      }
      const next = workspaceWithCloudInit(workspace, result.data);
      projects.set(next.id, next);
      return ok(uploadInitFromLocal(result.data.init));
    },
    async uploadExecutionPackage(workspace) {
      const build = cloudBuilds.get(workspace.id);
      const result = await requestLocal<LocalCloudUploadPackageResult>(baseURL, `/v1/desktop/projects/${encodeURIComponent(workspace.id)}/cloud/upload`, {
        method: "POST",
        body: JSON.stringify({
          org_id: orgID,
          upload_id: workspace.cloudRun.uploadID,
          ...(build ? { build } : {}),
        }),
      });
      if (!result.ok || !result.data) {
        return bridgeFailure(result.error ?? "上传执行包失败", result.errorInfo);
      }
      if (result.data.build) {
        cloudBuilds.set(workspace.id, result.data.build);
      }
      const next = workspaceWithCloudUpload(workspace, result.data);
      projects.set(next.id, next);
      return ok(uploadViewFromLocal(result.data.upload));
    },
    async pollExecutionPackageStatus(workspace) {
      const exchangePackageID = workspace.cloudRun.exchangePackageID;
      if (!exchangePackageID) {
        return { ok: false, error: "缺少 exchange package id，无法轮询服务器状态" };
      }
      const query = `?org_id=${encodeURIComponent(orgID)}&exchange_package_id=${encodeURIComponent(exchangePackageID)}`;
      const result = await requestLocal<LocalExecutionPackageStatusResponse>(baseURL, `/v1/desktop/projects/${encodeURIComponent(workspace.id)}/cloud/status${query}`);
      if (!result.ok || !result.data) {
        return bridgeFailure(result.error ?? "轮询服务器执行状态失败", result.errorInfo);
      }
      let next = workspaceWithCloudStatus(workspace, result.data);
      if (result.data.result_package_id && isTerminalLocalStatus(result.data.status)) {
        const resultPackage = await this.getResultPackage(next);
        if (resultPackage.ok && resultPackage.data) {
          next = workspaceWithResultPackage(next, resultPackage.data);
        }
      }
      projects.set(next.id, next);
      return ok(next);
    },
    async getResultPackage(workspace) {
      if (workspace.cloudRun.resultPackage) {
        return ok(workspace.cloudRun.resultPackage);
      }
      const resultPackageID = workspace.cloudRun.resultPackageID;
      if (!resultPackageID) {
        return { ok: false, error: "缺少 result package id，无法读取服务器结果包" };
      }
      const query = `?org_id=${encodeURIComponent(orgID)}&result_package_id=${encodeURIComponent(resultPackageID)}`;
      const result = await requestLocal<RecordingResultPackage>(baseURL, `/v1/desktop/projects/${encodeURIComponent(workspace.id)}/cloud/result${query}`);
      if (!result.ok || !result.data) {
        return bridgeFailure(result.error ?? "读取服务器结果包失败", result.errorInfo);
      }
      const next = workspaceWithResultPackage(workspace, result.data);
      projects.set(next.id, next);
      return ok(result.data);
    },
    async approveAndUploadPackage(workspace) {
      const init = await this.initExecutionPackageUpload(workspace);
      if (!init.ok || !init.data) {
        return bridgeFailure(init.error ?? "初始化服务器上传会话失败", init.errorInfo);
      }
      const initialized = workspaceWithCloudInit(workspace, {
        build: cloudBuilds.get(workspace.id),
        init: localInitResponseFromView(init.data),
      });
      const upload = await this.uploadExecutionPackage(initialized);
      if (!upload.ok || !upload.data) {
        return bridgeFailure(upload.error ?? "上传执行包失败", upload.errorInfo);
      }
      const uploaded = workspaceWithCloudUpload(initialized, localUploadPackageResultFromView(upload.data, cloudBuilds.get(workspace.id)));
      projects.set(uploaded.id, uploaded);
      return ok(uploaded);
    },
    async pollCloudRun(workspace) {
      return this.pollExecutionPackageStatus(workspace);
    },
    async simulateCloudFailure(workspace) {
      return ok(localFutureWorkspace(workspace, "失败诊断将在云端录制接入后启用。"));
    },
    async repairFailedScript(workspace) {
      return ok(localFutureWorkspace(workspace, "脚本修复将在失败诊断接入后启用。"));
    },
    async ackResultPackage(workspace) {
	  const resultPackageID = workspace.cloudRun.resultPackageID;
      if (!resultPackageID) {
		return { ok: false, error: "缺少 result package id，无法下载结果包" };
      }
	  const deliverables = workspace.cloudRun.resultPackage?.delivery?.asset_refs ?? [];
	  if (deliverables.length === 0) {
		return { ok: false, error: "结果包没有可下载的成品 artifact" };
	  }
	  const receivedAssetIDs: string[] = [];
	  for (const deliverable of deliverables) {
		const download = await requestLocal<LocalCloudDeliverableDownloadResult>(baseURL, `/v1/desktop/projects/${encodeURIComponent(workspace.id)}/cloud/deliverable/download`, {
		  method: "POST",
		  body: JSON.stringify({
			org_id: orgID,
			result_package_id: resultPackageID,
			exchange_package_id: workspace.cloudRun.exchangePackageID,
			deliverable,
		  }),
		});
		if (!download.ok || !download.data) {
		  return bridgeFailure(download.error ?? `下载成品 ${deliverable.id} 失败`, download.errorInfo);
		}
		if (!download.data.checksum_verified) {
		  return { ok: false, error: `成品 ${deliverable.id} checksum 校验失败，未发送接收确认` };
		}
		receivedAssetIDs.push(deliverable.id);
	  }
      const result = await requestLocal<LocalResultPackageAckResponse>(baseURL, `/v1/desktop/projects/${encodeURIComponent(workspace.id)}/cloud/ack`, {
        method: "POST",
        body: JSON.stringify({
          org_id: orgID,
          result_package_id: resultPackageID,
          exchange_package_id: workspace.cloudRun.exchangePackageID,
          received_asset_ids: receivedAssetIDs,
          verified_checksums: true,
        }),
      });
      if (!result.ok || !result.data) {
        return bridgeFailure(result.error ?? "确认结果包失败", result.errorInfo);
      }
      const next = ackWorkspaceAssets(workspace);
      projects.set(next.id, next);
      return ok(next);
    },
	async acknowledgeResult(workspace) {
	  return this.ackResultPackage(workspace);
	},
	async reviewResult(workspace, decision, summary) {
	  const resultPackageID = workspace.cloudRun.resultPackageID;
	  if (!resultPackageID) {
		return { ok: false, error: "缺少 result package id，无法提交人工审核" };
	  }
	  const idempotencyKey = `${workspace.id}:${resultPackageID}:${decision}:${Date.now()}`;
	  const reviewed = await requestLocal<LocalResultReviewRecord>(baseURL, `/v1/desktop/projects/${encodeURIComponent(workspace.id)}/cloud/review`, {
		method: "POST",
		body: JSON.stringify({
		  org_id: orgID,
		  result_package_id: resultPackageID,
		  review: { idempotency_key: idempotencyKey, decision, summary: summary?.trim() || undefined },
		}),
	  });
	  if (!reviewed.ok || !reviewed.data) {
		return bridgeFailure(reviewed.error ?? "提交人工审核失败", reviewed.errorInfo);
	  }
	  let revision: LocalResultRevisionRecord | undefined;
	  if (decision !== "approved") {
		const revised = await requestLocal<LocalResultRevisionRecord>(baseURL, `/v1/desktop/projects/${encodeURIComponent(workspace.id)}/cloud/revision`, {
		  method: "POST",
		  body: JSON.stringify({
			org_id: orgID,
			result_package_id: resultPackageID,
			revision: {
			  idempotency_key: `${idempotencyKey}:revision`,
			  requested_action: decision === "rerecord_requested" ? "rerecord" : "reedit",
			  summary: summary?.trim() || undefined,
			},
		  }),
		});
		if (!revised.ok || !revised.data) {
		  return bridgeFailure(revised.error ?? "提交返工请求失败", revised.errorInfo);
		}
		revision = revised.data;
	  }
	  const next = workspaceWithReview(workspace, reviewed.data, revision);
	  projects.set(next.id, next);
	  return ok(next);
	},
  };
}

export function createMockBridgeClient(): DesktopBridgeClient {
  const projects = new Map<string, ProjectWorkspaceView>();
  let githubCredentialConfigured = false;
  const initial = createWorkspace("product_demo");
  projects.set(initial.id, initial);

  return {
    mode: "mock",
    async runtimeHealth() {
      return ok({
        profile: "desktop",
        databaseConfigured: true,
        localDataConfigured: true,
        resourceManifestLoaded: true,
        llmMode: "auto",
        modelAdapterVersion: "domestic-llm-adapter-v1",
        sidecars: { "video-worker": true },
        modelProviders: {
          glm: { apiKeyEnv: "GLM_API_KEY", configured: false, baseURLConfigured: true, defaultModelConfigured: true },
          kimi: { apiKeyEnv: "KIMI_API_KEY", configured: false, baseURLConfigured: true, defaultModelConfigured: true },
          minimax: { apiKeyEnv: "MINIMAX_API_KEY", configured: false, baseURLConfigured: true, defaultModelConfigured: true },
          seedance: {
            apiKeyEnv: "SEEDANCE_API_KEY",
            apiKeyFallbackEnvs: ["DOUBAO_API_KEY", "ARK_API_KEY"],
            configured: false,
            baseURLConfigured: true,
            defaultModelConfigured: true,
          },
          doubao: {
            apiKeyEnv: "DOUBAO_API_KEY",
            apiKeyFallbackEnvs: ["ARK_API_KEY"],
            configured: false,
            baseURLConfigured: true,
            defaultModelConfigured: false,
          },
          deepseek: { apiKeyEnv: "DEEPSEEK_API_KEY", configured: false, baseURLConfigured: true, defaultModelConfigured: false },
        },
        modelTaskRoutes: {
          planning: {
            provider: "kimi",
            model: "kimi-k2.7-code",
            providerOverride: "CASCADE_PLANNING_PROVIDER",
            modelOverride: "CASCADE_PLANNING_MODEL",
          },
          code_reading: {
            provider: "glm",
            model: "glm-5.2",
            providerOverride: "CASCADE_CODE_READING_PROVIDER",
            modelOverride: "CASCADE_CODE_READING_MODEL",
          },
          multimodal_understanding: {
            provider: "minimax",
            model: "minimax-m3",
            providerOverride: "CASCADE_MULTIMODAL_PROVIDER",
            modelOverride: "CASCADE_MULTIMODAL_MODEL",
          },
          video_operation: {
            provider: "seedance",
            model: "seedance-2.0",
            providerOverride: "CASCADE_VIDEO_PROVIDER",
            modelOverride: "CASCADE_VIDEO_MODEL",
          },
        },
        cloudExchange: {
          configured: false,
          exchangeDiscovered: false,
          installationPaired: false,
          sessionValid: false,
          authMode: "unpaired",
          environment: "development",
          devPlaintext: false,
        },
      });
    },
    async githubCredentialStatus() {
      return ok({ configured: githubCredentialConfigured });
    },
    async storeGitHubToken(token) {
      if (!token.trim()) {
        return { ok: false, error: "请输入 GitHub fine-grained token" };
      }
      githubCredentialConfigured = true;
      return ok({ configured: true });
    },
    async deleteGitHubToken() {
      githubCredentialConfigured = false;
      return ok({ configured: false });
    },
    async desktopUpdateStatus() {
      return ok(mockDesktopUpdateStatus());
    },
    async checkDesktopUpdate() {
      return ok(mockDesktopUpdateStatus());
    },
    async applyDesktopUpdate() {
      return { ok: false, error: "Mock 模式未配置签名更新源" };
    },
    async modelDiagnostics() {
      return ok([
        mockDiagnostic("kimi", "planning", "kimi-k2.7-code"),
        mockDiagnostic("glm", "code_reading", "glm-5.2"),
        mockDiagnostic("minimax", "multimodal_understanding", "minimax-m3"),
        mockDiagnostic("seedance", "video_operation", "seedance-2.0"),
      ]);
    },
    async preflightExecutionPackage(workspace) {
      const bundle = workspace.executableScriptBundle;
      const plan = bundle?.plan_json;
      return ok({
        valid: Boolean(bundle && plan),
        ...(bundle?.script_manifest.runtime ? { runtime: bundle.script_manifest.runtime } : {}),
        package_id: workspace.packagePreview.packageID,
        stage_count: plan?.steps.length ?? 0,
        required_checks: plan?.steps.reduce((total, step) => total + step.validations.filter((validation) => validation.required).length, 0) ?? 0,
        allowed_domains: workspace.planReview.allowedDomains,
        message: "模拟预检通过：尚未上传、尚未启动浏览器。",
      });
    },
    async editorMaterialization(workspace) {
      const failed = workspace.cloudRun.status === "failed";
      return ok(failed
        ? { ready: false, created: false, message: "模拟失败结果不创建编辑会话。" }
        : { ready: true, created: false, session_id: "edit_mock_result", message: "模拟待编辑素材已登记。" });
    },
    async browserAgentAcceptance() {
      return ok(mockBrowserAgentAcceptance());
    },
    async runBrowserAgentAcceptance() {
      return ok(mockBrowserAgentAcceptance());
    },
    async browserAgentBusinessAcceptance() {
      return ok(mockBrowserAgentBusinessAcceptance());
    },
    async runBrowserAgentBusinessAcceptance() {
      return ok(mockBrowserAgentBusinessAcceptance());
    },
    async executionEvents() {
      return ok([]);
    },
    async createProject(input) {
      const scenarioID = typeof input === "string" ? input : input.scenarioID ?? "product_demo";
      const project = createWorkspace(scenarioID);
      const userInput = typeof input === "string" ? undefined : input.userInput;
      const seeded = userInput ? {
        ...project,
        productURL: userInput.product_url,
        targetAudience: userInput.target_audience,
        inputBundle: { ...project.inputBundle, raw_user_prompt: userInput.product_description },
      } : project;
      projects.set(seeded.id, seeded);
      return ok(seeded);
    },
    async listProjects() {
      return this.listProjectSummaries();
    },
    async listProjectSummaries() {
      return ok([...projects.values()].map((project) => ({ id: project.id, name: project.name, productURL: project.productURL, stage: project.stage, status: project.status, assetCount: project.assets.length, generatedAssetCount: project.assets.length })));
    },
    async archiveProject(projectID) { projects.delete(projectID); return ok({ archived: true }); },
    async deleteProject(projectID) { projects.delete(projectID); return ok({ deleted: true }); },
    async createAssistantSession(context) { return ok(mockAssistantSession(context)); },
    async getAssistantSession() { return { ok: false, error: "Mock Assistant session is not persisted" }; },
    async submitAssistantTurn(_sessionID, message) { return ok(mockAssistantSession({ surface: "projects", scopeKey: "mock" }, message)); },
    async listAssistantEvents() { return ok([]); },
    async confirmAssistantProposal(_sessionID, _proposalID, _baseVersion, _idempotencyKey) { return ok(mockAssistantSession({ surface: "projects", scopeKey: "mock" })); },
    async dismissAssistantProposal(_sessionID, _proposalID, _baseVersion, _idempotencyKey) { return ok(mockAssistantSession({ surface: "projects", scopeKey: "mock" })); },
    async cancelAssistantSession() { return ok(mockAssistantSession({ surface: "projects", scopeKey: "mock" })); },
    async selectLocalProjectDirectory() { return ok({ ref: "source_mock_local", kind: "local_repository", label: "sample-project" }); },
    async selectRequirementDocuments() { return ok([{ ref: "source_mock_requirement", kind: "requirement_document", label: "requirements.md" }]); },
    async selectBrandAssets() { return ok([{ ref: "source_mock_brand", kind: "brand_asset", label: "brand.png" }]); },
    async storeDemoCredential(ref) { return ok({ secretRef: `credential://demo/${ref}`, configured: true }); },
    async loadProject(projectID) {
      const project = projects.get(projectID);
      return project ? ok(project) : { ok: false, error: "未找到项目" };
    },
    async getSourceBinding(projectID) {
      const project = projects.get(projectID);
      return project?.sourceBinding ? ok(project.sourceBinding) : { ok: false, error: "source binding assessment is missing" };
    },
    async continueWithWebpageEvidence(projectID, assessmentHash) {
      const project = projects.get(projectID);
      if (!project || project.sourceBinding?.assessment_hash !== assessmentHash) return { ok: false, error: "source_binding_stale" };
      const next: ProjectWorkspaceView = { ...project, sourceBinding: { ...project.sourceBinding, effective_mode: "page_only", decision: "continue_page_only" } };
      projects.set(projectID, next);
      return ok(next);
    },
    async saveWorkspace(workspace) {
      projects.set(workspace.id, workspace);
      return ok(workspace);
    },
    async saveProjectInputs(projectID, inputs) {
      const project = projects.get(projectID);
      if (!project) return { ok: false, error: "未找到项目" };
      const next = { ...project, productURL: inputs.product_urls?.[0]?.url ?? project.productURL, inputBundle: inputs };
      projects.set(projectID, next);
      return ok(next);
    },
    async getUnderstandingReport(projectID) {
      const project = projects.get(projectID);
      return project ? ok(project.understandingReport ?? mockUnderstandingReport(project)) : { ok: false, error: "未找到项目" };
    },
    async getExecutionScriptDocument(projectID) {
      const project = projects.get(projectID);
      return project ? ok(project.scriptDocument ?? mockScriptDocument(project)) : { ok: false, error: "未找到项目" };
    },
    async getExecutionScriptMarkdown(projectID) {
      const project = projects.get(projectID);
      return project ? ok({ markdown: project.scriptMarkdown ?? mockScriptMarkdown(project) }) : { ok: false, error: "未找到项目" };
    },
    async getExecutableScriptBundle(projectID) {
      const project = projects.get(projectID);
      return project ? ok(project.executableScriptBundle ?? mockExecutableScriptBundle(project)) : { ok: false, error: "未找到项目" };
    },
    async buildExecutionPackagePreview(workspace) {
      const scriptDocument = workspace.scriptDocument ?? mockScriptDocument(workspace);
      const scriptMarkdown = workspace.scriptMarkdown ?? mockScriptMarkdown(workspace);
      const executableScriptBundle = workspace.executableScriptBundle ?? mockExecutableScriptBundle({
        ...workspace,
        scriptDocument,
        scriptMarkdown,
      });
      return ok({
        ...workspace,
        stage: "package_approval",
        status: "awaiting_approval",
        scriptDocument,
        scriptMarkdown,
        executableScriptBundle,
        packagePreview: {
          ...workspace.packagePreview,
          packageDigest: executableScriptBundle.reproducibility.bundle_hash_sha256 ?? workspace.packagePreview.packageDigest,
          graphDigest: executableScriptBundle.reproducibility.plan_hash_sha256,
        },
        cloudRun: {
          ...workspace.cloudRun,
          packageID: executableScriptBundle.id,
          status: "not_uploaded",
          stage: "local_generated",
          message: "三合一方案包已生成，等待人工审批。",
          currentStep: "执行包已在本地生成，等待人工审批",
          progress: 0,
          stageHistory: mockLifecycleStages(workspace, "local_generated"),
          artifactSummary: { total: 0, encrypted: 0, sensitive: 0 },
        },
      });
    },
    async runProductLifecycle(workspace) {
      const preview = await this.buildExecutionPackagePreview(workspace);
      if (!preview.ok || !preview.data) {
        return preview;
      }
      const uploaded = await this.approveAndUploadPackage(preview.data);
      if (!uploaded.ok || !uploaded.data) {
        return uploaded;
      }
      return this.pollCloudRun(uploaded.data);
    },
    async initExecutionPackageUpload(workspace) {
      return ok(mockUploadInit(workspace, "mock"));
    },
    async uploadExecutionPackage(workspace) {
      return ok({
        exchangePackageID: workspace.cloudRun.exchangePackageID ?? `xpkg_${workspace.id}`,
        cloudJobID: workspace.cloudRun.cloudJobID ?? `job_${workspace.id}`,
        status: "queued",
      });
    },
    async pollExecutionPackageStatus(workspace) {
      return ok(mockCloudRunningWorkspace(workspace));
    },
    async getResultPackage(workspace) {
      return ok(workspace.cloudRun.resultPackage ?? mockSuccessfulRecordingResultPackage(workspace));
    },
    async approveAndUploadPackage(workspace) {
      const upload = mockUploadInit(workspace, "mock");
      const uploaded = await this.uploadExecutionPackage({
        ...workspace,
        cloudRun: {
          ...workspace.cloudRun,
          uploadID: upload.uploadID,
          stage: "package_uploaded",
          stageHistory: mockLifecycleStages(workspace, "package_uploaded"),
        },
      });
      return ok(mockCloudRunningWorkspace({
        ...workspace,
        packagePreview: { ...workspace.packagePreview, ipAllowlistAcknowledged: true },
        cloudRun: {
          ...workspace.cloudRun,
          uploadID: upload.uploadID,
          exchangePackageID: uploaded.data?.exchangePackageID ?? `xpkg_${workspace.id}`,
          cloudJobID: uploaded.data?.cloudJobID ?? `job_${workspace.id}`,
        },
      }));
    },
    async pollCloudRun(workspace) {
      const resultPackage = mockSuccessfulRecordingResultPackage(workspace);
      return ok({
        ...workspace,
        stage: "result_review",
        status: "asset_ready",
        cloudRun: {
          ...workspace.cloudRun,
          status: "succeeded",
          stage: "completed",
          message: "服务器已返回加密结果包和成品资产 descriptor。",
          currentStep: "演示视频和步骤文档已生成",
          progress: 100,
          resultPackageID: resultPackage.result_id,
          resultPackage,
          stageHistory: mockLifecycleStages(workspace, "result_returned", { resultPackage }),
          sandboxMetadata: resultPackage.execution_trace?.sandbox ?? resultPackage.audit_trail?.sandbox ?? mockSandboxMetadata(workspace),
          artifactSummary: artifactSummaryFromResult(resultPackage),
        },
      });
    },
    async simulateCloudFailure(workspace) {
      const failedResult = mockFailedRecordingResultPackage(workspace);
      return ok({
        ...workspace,
        stage: "script_repair",
        status: "script_repair_required",
        cloudRun: {
          ...workspace.cloudRun,
          status: "failed",
          stage: "recording_failed",
          message: "服务器在浏览器执行阶段返回失败诊断。",
          currentStep: "云端执行失败，已返回脱敏截图和错误诊断",
          progress: 62,
          resultPackageID: failedResult.result_id,
          resultPackage: failedResult,
          stageHistory: mockLifecycleStages(workspace, "browser_execution", { failed: true, resultPackage: failedResult }),
          sandboxMetadata: failedResult.execution_trace?.sandbox ?? failedResult.audit_trail?.sandbox ?? mockSandboxMetadata(workspace),
          artifactSummary: artifactSummaryFromResult(failedResult),
          ...(failedResult.failure_diagnostic ? { failureDiagnostic: failedResult.failure_diagnostic, lastError: failedResult.failure_diagnostic.error.message } : {}),
          ...(failedResult.repair_request ? { repairRequest: failedResult.repair_request } : {}),
        },
      });
    },
    async repairFailedScript(workspace) {
      const diagnostic = workspace.cloudRun.failureDiagnostic ?? mockFailureDiagnostic(workspace);
      const repairRequest = workspace.cloudRun.repairRequest ?? mockFailedRecordingResultPackage(workspace).repair_request;
      const scriptDocument = workspace.scriptDocument ?? mockScriptDocument(workspace);
      const repairedMarkdown = mockRepairApprovalMarkdown(workspace, diagnostic);
      const repairedBundle = mockExecutableScriptBundle({
        ...workspace,
        scriptDocument,
        scriptMarkdown: repairedMarkdown,
      }, {
        diagnostic,
        sourceCloudJobID: repairRequest?.cloud_job_id ?? "job_failed",
        sourceResultID: repairRequest?.source_result_id ?? "result_failed",
        repairAttempt: repairRequest?.repair_attempt ?? 1,
      });
      return ok({
        ...workspace,
        stage: "package_approval",
        status: "awaiting_approval",
        scriptDocument,
        scriptMarkdown: repairedMarkdown,
        executableScriptBundle: repairedBundle,
        packagePreview: {
          ...workspace.packagePreview,
          packageDigest: repairedBundle.reproducibility.bundle_hash_sha256 ?? workspace.packagePreview.packageDigest,
          graphDigest: repairedBundle.reproducibility.plan_hash_sha256,
          humanApprovalRequired: true,
          blockedReasons: ["修复后的脚本必须重新审批"],
        },
        cloudRun: {
          ...workspace.cloudRun,
          status: "not_uploaded",
          stage: "local_generated",
          message: "修复后的三合一方案包已生成，等待重新审批。",
          currentStep: "修复包已生成，等待人工审批",
          progress: 0,
          stageHistory: mockLifecycleStages(workspace, "local_generated"),
        },
      });
    },
    async ackResultPackage(workspace) {
      return ok(ackWorkspaceAssets(workspace));
    },
	async acknowledgeResult(workspace) {
	  return this.ackResultPackage(workspace);
	},
	async reviewResult(workspace, decision, summary) {
	  return ok(workspaceWithReview(workspace, {
		review_id: `review_mock_${Date.now()}`,
		decision,
		...(summary ? { summary } : {}),
		reviewed_at: new Date().toISOString(),
	  }, decision === "approved" ? undefined : {
		revision_id: `revision_mock_${Date.now()}`,
		resolved_action: decision === "rerecord_requested" ? "rerecord" : "reedit",
		status: "queued",
		requested_at: new Date().toISOString(),
	  }));
	},
  };
}

function ok<T>(data: T): BridgeResult<T> {
  return { ok: true, data };
}

function bridgeFailure<T>(error: string, errorInfo?: LocalBridgeErrorInfo): BridgeResult<T> {
  return {
    ok: false,
    error,
    ...(errorInfo ? { errorInfo } : {}),
  };
}

async function requestLocal<T>(baseURL: string, path: string, init?: RequestInit): Promise<BridgeResult<T>> {
  const startedAt = Date.now();
  console.info("[Cascade Dev Bridge] request", init?.method ?? "GET", path);
  try {
    const response = await fetch(`${baseURL}${path}`, {
      ...init,
      headers: {
        "Content-Type": "application/json",
        ...(init?.headers ?? {}),
      },
    });
    const responseText = await readLocalResponseText(response);
    let payload: LocalBridgeResponse<T>;
    try {
      payload = JSON.parse(responseText) as LocalBridgeResponse<T>;
    } catch {
      const snippet = safeResponseSnippet(responseText);
      console.error("[Cascade Dev Bridge] non-json", init?.method ?? "GET", path, response.status, snippet);
      return {
        ok: false,
        error: `本地 Dev Bridge 返回非 JSON 响应: HTTP ${response.status}${response.statusText ? ` ${response.statusText}` : ""}; ${snippet}`,
      };
    }
    if (!response.ok || !payload.ok) {
      console.error("[Cascade Dev Bridge] error", init?.method ?? "GET", path, response.status, payload.error_info ?? payload.error);
      return {
        ok: false,
        error: formatLocalBridgeError(payload.error_info, payload.error ?? `本地 Dev Bridge 请求失败: ${response.status}`),
        ...(payload.error_info ? { errorInfo: payload.error_info } : {}),
      };
    }
    if (payload.data === undefined) {
      console.error("[Cascade Dev Bridge] missing data", init?.method ?? "GET", path);
      return { ok: false, error: "本地 Dev Bridge 响应缺少 data" };
    }
    console.info("[Cascade Dev Bridge] done", init?.method ?? "GET", path, `${Date.now() - startedAt}ms`);
    return { ok: true, data: payload.data };
  } catch (error) {
    console.error("[Cascade Dev Bridge] unavailable", init?.method ?? "GET", path, error);
    return { ok: false, error: userFacingBridgeError(error instanceof Error ? error.message : "本地 Dev Bridge 不可用") };
  }
}

type WailsDesktopBridge = Record<string, (...args: unknown[]) => Promise<BridgeResult<unknown>> | BridgeResult<unknown>>;

async function callWailsBridge<T>(method: string, ...args: unknown[]): Promise<BridgeResult<T>> {
  const bridge = (window as unknown as { go?: { app?: { DesktopBridge?: WailsDesktopBridge } } }).go?.app?.DesktopBridge;
  const fn = bridge?.[method];
  if (!fn) return { ok: false, error: "此操作需要 Wails 桌面应用" };
  try {
    return await fn(...args) as BridgeResult<T>;
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : String(error) };
  }
}

function mockAssistantSession(context: AssistantContextView, message = ""): AssistantSessionView {
  const configuration = {
    projectName: "示例项目", productURL: "https://example.com", objective: message || "展示核心产品价值",
    targetAudience: "潜在客户", targetDurationSec: 60, sources: [{ ref: "source_mock_local", kind: "local_repository", label: "sample-project" }],
    version: 1, hash: "sha256:mock", readiness: "ready" as const, confirmed: false,
  };
  return {
    id: `assistant_${context.scopeKey}`, context, status: "waiting_for_user", activeWorkstation: "overview",
    workstationTitle: "项目配置", workstationStatus: "等待确认", configuration,
    messages: [{ id: "welcome", role: "agent", kind: "answer", text: message ? "我已整理为 configuration 提案。" : "描述你想制作的产品演示，我会先整理 configuration。", createdAt: new Date().toISOString() }],
  };
}

function formatLocalBridgeError(errorInfo: LocalBridgeErrorInfo | undefined, fallback: string): string {
  if (!errorInfo) {
    return userFacingBridgeError(fallback);
  }
  const base = userFacingBridgeError(errorInfo.message || fallback);
  const firstDetail = errorInfo.details?.[0];
  const detailParts = [
    firstDetail?.field ? `字段: ${firstDetail.field}` : "",
    firstDetail?.reason ? `原因: ${firstDetail.reason}` : "",
    firstDetail?.message && firstDetail.message !== errorInfo.message ? `详情: ${firstDetail.message}` : "",
    firstDetail?.hint ? `建议: ${firstDetail.hint}` : "",
  ].filter(Boolean);
  const code = errorInfo.code ? `错误码: ${errorInfo.code}` : "";
  const correlation = errorInfo.correlation_id ? `追踪: ${errorInfo.correlation_id}` : "";
  return [base, code, ...detailParts, correlation].filter(Boolean).join("；");
}

async function readLocalResponseText(response: Response): Promise<string> {
  const readable = response as Response & {
    text?: () => Promise<string>;
    json?: () => Promise<unknown>;
  };
  if (typeof readable.text === "function") {
    return readable.text();
  }
  if (typeof readable.json === "function") {
    return JSON.stringify(await readable.json());
  }
  return "";
}

function safeResponseSnippet(value: string): string {
  const redacted = value
    .replace(/Bearer\s+[A-Za-z0-9._-]+/gi, "Bearer [redacted]")
    .replace(/sk-[A-Za-z0-9_-]+/g, "sk-[redacted]")
    .replace(/\s+/g, " ")
    .trim();
  if (!redacted) {
    return "(empty response)";
  }
  return redacted.length > 240 ? `${redacted.slice(0, 240)}...` : redacted;
}

function userFacingBridgeError(message: string): string {
  const lower = message.toLowerCase();
  if (
    lower.includes("llm json parse failed") ||
    lower.includes("cannot unmarshal") ||
    lower.includes("invalid character") ||
    lower.includes("unexpected non-whitespace character after json")
  ) {
    return "模型返回的 JSON 结构不稳定，系统已记录诊断。请重试，或把 CASCADE_LLM_MODE 设置为 auto 让产品流程在格式异常时安全降级。";
  }
  if (message.includes("执行包没有真实业务动作")) {
    return message;
  }
  return message;
}

function runtimeLogFromLocalEvent(event: LocalExecutionEvent): RuntimeLogEntry {
  return {
    id: String(event.id),
    time: new Date(event.created_at).toLocaleTimeString("zh-CN", { hour12: false }),
    level: event.level,
    message: event.message,
    ...(event.detail ? { detail: event.detail } : {}),
    ...(event.node ? { node: event.node } : {}),
    ...(typeof event.elapsed_ms === "number" ? { elapsedMS: event.elapsed_ms } : {}),
  };
}

export function userInputFromWorkspace(workspace: ProjectWorkspaceView, options?: BridgeRunOptions): LocalUserInput {
  const template = getScenarioTemplate(workspace.scenarioID);
  const localRepository = workspace.inputBundle.repositories?.find((repo) => repo.local_path);
  const gitRepository = workspace.inputBundle.repositories?.find((repo) => repo.url);
  const requirementDocuments = workspace.inputBundle.requirement_documents;
  const screenshots = workspace.inputBundle.webpage_screenshots;
  const forbiddenData = Array.isArray(workspace.inputBundle.metadata?.forbidden_data)
    ? workspace.inputBundle.metadata.forbidden_data.filter((item): item is string => typeof item === "string")
    : ["客户邮箱", "API Key", "访问令牌"];
  const username = options?.demoCredentials?.username?.trim();
  const password = options?.demoCredentials?.password;
  return {
    mode: "desktop",
    product_url: workspace.productURL,
    ...(localRepository?.local_path ? { local_repo_path: localRepository.local_path } : {}),
    ...(gitRepository?.url ? { git_repo_url: gitRepository.url } : {}),
    product_description: workspace.inputBundle.raw_user_prompt || template.objective,
    ...(requirementDocuments?.length ? { requirement_documents: requirementDocuments } : {}),
    ...(screenshots?.length ? { webpage_screenshots: screenshots } : {}),
    target_audience: workspace.targetAudience || template.targetAudience,
    brand_tone: "专业、清晰、适合中国客户",
    must_show: template.defaultChecklist,
    must_not_show: ["原始密码", "API Key 明文", "客户隐私数据"],
    forbidden_pages: workspace.planReview.forbiddenPages,
    forbidden_data: forbiddenData,
    ...(username ? { demo_username: username } : {}),
    ...(password ? { demo_password: password } : {}),
  };
}

export function workspaceFromCascadeStateForTest(state: LocalCascadeState, fallback: ProjectWorkspaceView): ProjectWorkspaceView {
  return workspaceFromCascadeState(state, fallback);
}

function workspaceFromCascadeState(state: LocalCascadeState, fallback: ProjectWorkspaceView): ProjectWorkspaceView {
  const project = state.project_context;
  const graph = state.workflow_graph ?? fallback.planReview.graph;
  const report = state.understanding_report;
  let intelligence = state.project_intelligence;
  if (intelligence) {
    const merged: ProjectIntelligencePack = { ...intelligence };
    const verified = state.verified_interaction_plan ?? intelligence.verified_interaction_plan;
    const missing = state.missing_evidence_report ?? intelligence.missing_evidence_report;
    if (verified) {
      merged.verified_interaction_plan = verified;
    }
    if (missing) {
      merged.missing_evidence_report = missing;
    }
    intelligence = merged;
  }
  const readiness = state.script_readiness_report ?? intelligence?.script_readiness_report;
  const bundle = state.executable_script_bundle;
  const scriptDocument = state.script_document ?? bundle?.plan_json;
  const runSpec = scriptDocument?.recording_run_spec;
  const inputBundle = project?.inputs ?? fallback.inputBundle;
  const productURL = project?.product_url || runSpec?.base_url || fallback.productURL;
  const allowedDomains = runSpec?.allowed_domains ?? scriptDocument?.safety_policy.allowed_domains ?? fallback.planReview.allowedDomains;
  const redactionSelectors = runSpec?.redactions.mask_selectors ?? scriptDocument?.safety_policy.redactions.mask_selectors ?? fallback.planReview.redactionSelectors;
  const forbiddenPages = scriptDocument?.safety_policy.forbidden_pages ?? project?.forbidden_pages ?? fallback.planReview.forbiddenPages;
  const outputRequests = outputRequestsFromScript(scriptDocument, fallback.planReview.outputRequests);
  const packageID = bundle?.id ?? scriptDocument?.id ?? `pkg_${state.project_id}`;
  const graphDigest = bundle?.reproducibility.graph_hash_sha256 || scriptDocument?.reproducibility.graph_hash_sha256 || fallback.packagePreview.graphDigest;
  const packageDigest = bundle?.reproducibility.bundle_hash_sha256 || bundle?.reproducibility.plan_hash_sha256 || scriptDocument?.reproducibility.script_hash_sha256 || fallback.packagePreview.packageDigest;

  const sourceBinding = state.source_binding ?? project?.source_binding ?? fallback.sourceBinding;
  const workspace: ProjectWorkspaceView = {
    ...fallback,
    id: state.project_id || project?.id || fallback.id,
    name: graph.name || project?.name || fallback.name,
    stage: "package_approval",
    productURL,
    targetAudience: project?.target_audience || fallback.targetAudience,
    status: "awaiting_approval",
    inputBundle,
    sourceConnections: sourceConnectionsFromState(project, report, fallback),
    ...(sourceBinding ? { sourceBinding } : {}),
    understanding: {
      productMapID: state.product_map?.id || fallback.understanding.productMapID,
      routesDetected: intelligence?.architecture?.route_tree?.length ?? report?.code_snapshots?.reduce((count, snapshot) => count + (snapshot.routes?.length ?? 0), 0) ?? fallback.understanding.routesDetected,
      featuresDetected: intelligence?.feature_capabilities?.length ?? report?.feature_hypotheses?.length ?? fallback.understanding.featuresDetected,
      componentsSummarized: intelligence?.interaction_surfaces?.length ?? report?.code_snapshots?.reduce((count, snapshot) => count + (snapshot.components?.length ?? 0), 0) ?? fallback.understanding.componentsSummarized,
      dataModelsSummarized: intelligence?.data_models?.length ?? report?.code_snapshots?.reduce((count, snapshot) => count + (snapshot.data_models?.length ?? 0), 0) ?? fallback.understanding.dataModelsSummarized,
      evidenceRefs: intelligence?.evidence_refs ?? report?.evidence_refs ?? fallback.understanding.evidenceRefs,
      sensitiveWarnings: intelligence?.safety_report?.policy_findings?.map((finding) => finding.summary) ?? report?.safety_report?.policy_findings?.map((finding) => finding.summary) ?? fallback.understanding.sensitiveWarnings,
    },
    planReview: {
      graph,
      targetDurationSec: runSpec?.timeline.target_duration_sec ?? fallback.planReview.targetDurationSec,
      allowedDomains,
      forbiddenPages,
      redactionSelectors,
      outputRequests,
    },
    packagePreview: {
      packageID,
      packageDigest,
      graphDigest,
      sourceSummaryOnly: scriptDocument?.approval_checklist.source_summary_only ?? true,
      encrypted: true,
      humanApprovalRequired: scriptDocument?.approval_checklist.human_approval_required ?? true,
      ipAllowlistAcknowledged: false,
      credentialGrants: credentialGrantsFromInput(inputBundle, allowedDomains),
      blockedReasons: scriptDocument?.approval_checklist.blocking_reasons ?? fallback.packagePreview.blockedReasons,
    },
    cloudRun: {
      packageID,
      status: "not_uploaded",
      currentStep: "执行包已在本地生成，等待人工审批",
      progress: 0,
      retryCount: 0,
    },
  };
  if (report) {
    workspace.understandingReport = report;
  }
  if (intelligence) {
    workspace.projectIntelligence = intelligence;
  }
  if (readiness) {
    workspace.scriptReadiness = readiness;
  }
  if (state.agent_graph_trace) {
    workspace.agentGraphTrace = state.agent_graph_trace;
  }
  if (scriptDocument) {
    workspace.scriptDocument = scriptDocument;
  }
  const scriptMarkdown = state.script_markdown ?? bundle?.approval_markdown.inline_markdown ?? fallback.scriptMarkdown;
  if (scriptMarkdown) {
    workspace.scriptMarkdown = scriptMarkdown;
  }
    if (bundle) {
    workspace.executableScriptBundle = bundle;
  }
  const provenance = modelProvenanceFromState(report, scriptDocument);
  if (provenance.length > 0) {
    workspace.modelProvenance = provenance;
  }
  return workspace;
}

function workspaceFromCloudLifecycleResult(workspace: ProjectWorkspaceView, lifecycle: LocalCloudLifecycleResult): ProjectWorkspaceView {
  const status = lifecycle.status;
  const resultPackage = lifecycle.result;
  const mappedStatus = mapCloudRunStatus(status.status);
  const artifactSummary = resultPackage ? artifactSummaryFromResult(resultPackage) : artifactSummaryFromStatus(status);
  const failureDiagnostic = resultPackage?.failure_diagnostic;
  const repairRequest = resultPackage?.repair_request;
  const resultAssets = resultPackage ? assetsFromResultPackage(resultPackage, workspace) : workspace.assets;
  const cloudJobID = lifecycle.upload.cloud_job_id || status.cloud_job_id;
  const failureSummary = failureSummaryText(status.failure_summary, status.error);
  const sandboxMetadata = resultPackage?.execution_trace?.sandbox ?? resultPackage?.audit_trail?.sandbox ?? workspace.cloudRun.sandboxMetadata;
  return {
    ...workspace,
    stage: mappedStatus === "failed" ? "script_repair" : mappedStatus === "succeeded" ? "result_review" : "cloud_run",
    status: mappedStatus === "failed" ? "script_repair_required" : mappedStatus === "succeeded" ? "asset_ready" : "cloud_running",
    assets: resultAssets,
    packagePreview: {
      ...workspace.packagePreview,
      ipAllowlistAcknowledged: true,
      packageID: lifecycle.build?.package.package_id ?? workspace.packagePreview.packageID,
      packageDigest: lifecycle.build?.package_digest_sha256 ?? workspace.packagePreview.packageDigest,
    },
    cloudRun: {
      ...workspace.cloudRun,
      packageID: lifecycle.build?.package.package_id ?? workspace.cloudRun.packageID,
      uploadID: lifecycle.init.upload_id,
      exchangePackageID: lifecycle.upload.exchange_package_id || status.exchange_package_id,
      status: mappedStatus,
      stageHistory: lifecycleStagesFromStatus(status, resultPackage),
      artifactSummary,
      currentStep: status.message || cloudRunCurrentStep(status),
      progress: status.progress_percent ?? (mappedStatus === "succeeded" || mappedStatus === "failed" ? 100 : workspace.cloudRun.progress),
      retryCount: workspace.cloudRun.retryCount,
      ...(cloudJobID ? { cloudJobID } : {}),
      ...(status.result_package_id ? { resultPackageID: status.result_package_id } : {}),
      ...(status.stage ? { stage: status.stage } : {}),
      ...(status.message ? { message: status.message } : {}),
      ...(failureSummary ? { failureSummary } : {}),
      ...(sandboxMetadata ? { sandboxMetadata } : {}),
      ...(resultPackage ? { resultPackage } : {}),
      ...(failureDiagnostic ? { failureDiagnostic, lastError: failureDiagnostic.error.message } : {}),
      ...(repairRequest ? { repairRequest } : {}),
    },
  };
}

function workspaceWithPreparedBuild(workspace: ProjectWorkspaceView, build: LocalClientExecutionPackageBuild): ProjectWorkspaceView {
  return {
    ...workspace,
    stage: "package_approval",
    status: "awaiting_approval",
    packagePreview: {
      ...workspace.packagePreview,
      ipAllowlistAcknowledged: true,
      packageID: build.package.package_id ?? workspace.packagePreview.packageID,
	  packageDigest: build.package_digest_sha256 || workspace.packagePreview.packageDigest,
	  buildStatus: build.build_status,
	  approvalSubjectDigest: build.approval_subject_digest_sha256,
	  ...(build.package.confidence_summary?.assessment_hash ? { confidenceAssessmentHash: build.package.confidence_summary.assessment_hash } : {}),
	  ...(build.package.confidence_summary?.readiness ? { readiness: build.package.confidence_summary.readiness } : {}),
	  ...(build.package.confidence_summary?.overall_score != null ? { confidenceScore: build.package.confidence_summary.overall_score } : {}),
	  ...(build.package.confidence_summary?.warnings ? { confidenceWarnings: build.package.confidence_summary.warnings } : {}),
	  ...(build.size_report?.total_bytes != null ? { totalBytes: build.size_report.total_bytes } : {}),
	  ...(build.size_report?.section_bytes ? { sectionBytes: build.size_report.section_bytes } : {}),
	  blockedReasons: build.package.confidence_summary?.blocking_reasons ?? workspace.packagePreview.blockedReasons,
    },
    cloudRun: {
      ...workspace.cloudRun,
      packageID: build.package.package_id ?? workspace.cloudRun.packageID,
      status: "not_uploaded",
      stage: "local_generated",
      message: "本地理解与脚本生成已完成；等待服务器连接和上传。",
      currentStep: "三合一方案包已准备完成",
      progress: 18,
      stageHistory: localLifecycleStagesFromPartial("local_generated"),
    },
  };
}

function workspaceWithCloudPhaseFailure(
  workspace: ProjectWorkspaceView,
  stage: string,
  message: string,
  errorInfo?: LocalBridgeErrorInfo,
): ProjectWorkspaceView {
  const failedStage: ServerLifecycleStageView = {
    id: "upload_initialized",
    label: serverLifecycleStageLabels.upload_initialized,
    status: "failed",
    progress: 100,
    summary: message,
    artifactCount: 0,
  };
  if (errorInfo?.code) {
    failedStage.errorCode = errorInfo.code;
  }
  return {
    ...workspace,
    stage: "package_approval",
    status: "awaiting_approval",
    cloudRun: {
      ...workspace.cloudRun,
      status: "failed",
      stage,
      message,
      currentStep: message,
      progress: Math.max(workspace.cloudRun.progress, 18),
      lastError: message,
      failureSummary: errorInfo?.code ? `${errorInfo.code}: ${message}` : message,
      stageHistory: [
        ...localLifecycleStagesFromPartial("local_generated").filter((item) => item.id !== "upload_initialized"),
        failedStage,
      ],
    },
  };
}

function workspaceWithCloudInit(workspace: ProjectWorkspaceView, result: LocalCloudUploadInitResult): ProjectWorkspaceView {
  const build = result.build;
  return {
    ...workspace,
    stage: "package_approval",
    packagePreview: {
      ...workspace.packagePreview,
      ipAllowlistAcknowledged: true,
      packageID: build?.package.package_id ?? workspace.packagePreview.packageID,
	  packageDigest: build?.package_digest_sha256 ?? workspace.packagePreview.packageDigest,
	  ...(build?.build_status ? { buildStatus: build.build_status } : {}),
    },
    cloudRun: {
      ...workspace.cloudRun,
      packageID: build?.package.package_id ?? workspace.cloudRun.packageID,
      uploadID: result.init.upload_id,
      status: "queued",
      stage: "upload_initialized",
      message: "服务器上传会话已初始化；当前 local bridge 使用 dev 明文 payload 联调。",
      currentStep: "上传会话已初始化",
      progress: 10,
      stageHistory: localLifecycleStagesFromPartial("upload_initialized"),
    },
  };
}

function workspaceWithCloudUpload(workspace: ProjectWorkspaceView, result: LocalCloudUploadPackageResult): ProjectWorkspaceView {
  const status: LocalExecutionPackageStatusResponse = {
    exchange_package_id: result.upload.exchange_package_id,
    status: result.upload.status,
    stage: "accepted",
    message: "执行包已上传服务器，等待校验和沙箱执行。",
    progress_percent: result.upload.status === "running" ? 35 : 20,
    ...(result.upload.cloud_job_id ? { cloud_job_id: result.upload.cloud_job_id } : {}),
  };
  const lifecycle: LocalCloudLifecycleResult = {
    init: {
      upload_id: workspace.cloudRun.uploadID ?? "",
      server_public_key_id: "",
      cascade_execution_ips: [],
    },
    upload: result.upload,
    status,
    ...(result.build ? { build: result.build } : {}),
  };
  return workspaceFromCloudLifecycleResult(workspace, lifecycle);
}

function workspaceWithCloudStatus(workspace: ProjectWorkspaceView, status: LocalExecutionPackageStatusResponse): ProjectWorkspaceView {
  return workspaceFromCloudLifecycleResult(workspace, {
    init: {
      upload_id: workspace.cloudRun.uploadID ?? "",
      server_public_key_id: "",
      cascade_execution_ips: [],
    },
    upload: {
      exchange_package_id: status.exchange_package_id || workspace.cloudRun.exchangePackageID || "",
      status: status.status,
      ...(status.cloud_job_id ?? workspace.cloudRun.cloudJobID ? { cloud_job_id: status.cloud_job_id ?? workspace.cloudRun.cloudJobID } : {}),
    },
    status,
  });
}

function workspaceWithResultPackage(workspace: ProjectWorkspaceView, resultPackage: RecordingResultPackage): ProjectWorkspaceView {
  const mappedStatus = resultPackage.status === "failed" ? "failed" : "succeeded";
  const status: LocalExecutionPackageStatusResponse = {
    exchange_package_id: workspace.cloudRun.exchangePackageID ?? "",
    status: mappedStatus === "succeeded" ? "completed" : "failed",
    stage: mappedStatus === "succeeded" ? "completed" : "failed",
    message: mappedStatus === "succeeded" ? "服务器结果包已返回。" : "服务器返回失败诊断。",
    progress_percent: 100,
    result_package_id: resultPackage.result_id,
    ...(workspace.cloudRun.cloudJobID ? { cloud_job_id: workspace.cloudRun.cloudJobID } : {}),
  };
	const next = workspaceFromCloudLifecycleResult(workspace, {
    init: {
      upload_id: workspace.cloudRun.uploadID ?? "",
      server_public_key_id: "",
      cascade_execution_ips: [],
    },
    upload: {
      exchange_package_id: workspace.cloudRun.exchangePackageID ?? "",
      status: status.status,
      ...(workspace.cloudRun.cloudJobID ? { cloud_job_id: workspace.cloudRun.cloudJobID } : {}),
    },
    status,
    result: resultPackage,
  });
	const previousStages = workspace.cloudRun.stageHistory;
	if (!previousStages?.length) return next;
	return {
		...next,
		cloudRun: {
			...next.cloudRun,
			stageHistory: (next.cloudRun.stageHistory ?? previousStages).map((stage) => stage.id === "result_returned" ? stage : previousStages.find((previous) => previous.id === stage.id) ?? stage),
		},
	};
}

function mockBrowserAgentAcceptance(): BrowserAgentAcceptanceView {
  return {
    ready: true,
    can_run: true,
    message: "演示数据：本地受控验收包已通过。真实模式会运行浏览器并生成截图、录屏与 trace。",
    report_path: "artifacts/browser-agent-acceptance/latest/acceptance-report.json",
    report: {
      schema_version: "cascade.browser_agent_acceptance.v1",
      generated_at: new Date().toISOString(),
      runtime: "browser-agent-outline-v1",
      strict_gate: "passed",
      scenarios: [
        { id: "success_navigation_click", description: "导航、识别经批准的按钮、点击并验证页面结果", expected: "通过；含截图证据", actual: "pass", verdict: "passed", action_executed: true, evidence: [], assertions: [{ kind: "required_validation", passed: true, actual: "matched" }] },
        { id: "semantic_target_contract_conflict", description: "页面存在按钮但批准名称不同", expected: "点击前拦截", actual: "pass", verdict: "passed", action_executed: false, evidence: [], assertions: [{ kind: "target_contract", passed: true, actual: "blocked" }] },
        { id: "locator_missing", description: "批准目标不在页面上", expected: "点击前拦截", actual: "pass", verdict: "passed", action_executed: false, evidence: [], assertions: [{ kind: "locator_resolution", passed: true, actual: "blocked" }] },
        { id: "required_validation_failure", description: "动作已完成但要求的业务结果不存在", expected: "停止，不进入后续阶段", actual: "pass", verdict: "passed", action_executed: true, evidence: [], assertions: [{ kind: "required_validation", passed: false, actual: "not_matched" }], stop_reason: "required_validation_failed" },
        { id: "recording_and_trace_delivery", description: "关闭会话后保留可回放证据", expected: "录屏和 trace 均存在", actual: "pass", verdict: "passed", action_executed: false, evidence: [], assertions: [{ kind: "recording_and_trace", passed: true, actual: "retained" }] },
      ],
    },
  };
}

function uploadInitFromLocal(init: LocalExecutionPackageInitResponse): ExecutionPackageUploadInitView {
  return {
    uploadID: init.upload_id,
    serverPublicKeyID: init.server_public_key_id,
    supportedCryptoSuites: init.supported_crypto_suites ?? [],
    cascadeExecutionIPs: init.cascade_execution_ips ?? [],
    ...(typeof init.max_envelope_bytes === "number" ? { maxEnvelopeBytes: init.max_envelope_bytes } : {}),
    ...(init.expires_at ? { expiresAt: init.expires_at } : {}),
  };
}

function uploadViewFromLocal(upload: LocalExecutionPackageUploadResponse): ExecutionPackageUploadView {
  return {
    exchangePackageID: upload.exchange_package_id,
    cloudJobID: upload.cloud_job_id ?? "",
    status: mapCloudRunStatus(upload.status),
  };
}

function localInitResponseFromView(init: ExecutionPackageUploadInitView): LocalExecutionPackageInitResponse {
  return {
    upload_id: init.uploadID,
    server_public_key_id: init.serverPublicKeyID,
    supported_crypto_suites: init.supportedCryptoSuites,
    cascade_execution_ips: init.cascadeExecutionIPs,
    ...(typeof init.maxEnvelopeBytes === "number" ? { max_envelope_bytes: init.maxEnvelopeBytes } : {}),
    ...(init.expiresAt ? { expires_at: init.expiresAt } : {}),
  };
}

function localUploadPackageResultFromView(upload: ExecutionPackageUploadView, build?: LocalClientExecutionPackageBuild): LocalCloudUploadPackageResult {
  return {
    ...(build ? { build } : {}),
    upload: {
      exchange_package_id: upload.exchangePackageID,
      status: upload.status,
      ...(upload.cloudJobID ? { cloud_job_id: upload.cloudJobID } : {}),
    },
  };
}

function localLifecycleStagesFromPartial(currentID: ServerLifecycleStageID): ServerLifecycleStageView[] {
  const activeIndex = serverLifecycleStageOrder.indexOf(currentID);
  return serverLifecycleStageOrder.map((id, index) => ({
    id,
    label: serverLifecycleStageLabels[id],
    status: index < activeIndex ? "completed" : index === activeIndex ? "active" : "pending",
    progress: index < activeIndex ? 100 : index === activeIndex ? 20 : 0,
    summary: id === "upload_initialized" ? "服务器返回 upload id 和上传约束。" : "",
    artifactCount: 0,
  }));
}

function isTerminalLocalStatus(status: string | undefined): boolean {
  return ["completed", "succeeded", "failed", "canceled", "expired"].includes(status ?? "");
}

function isTerminalCloudRun(status: ProjectWorkspaceView["cloudRun"]["status"]): boolean {
  return status === "succeeded" || status === "failed";
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => globalThis.setTimeout(resolve, ms));
}

function mapDesktopUpdateResult(result: BridgeResult<LocalDesktopUpdateStatus>): BridgeResult<DesktopUpdateStatus> {
  if (!result.ok || !result.data) {
    return { ok: false, error: result.error ?? "桌面更新状态不可用", ...(result.errorInfo ? { errorInfo: result.errorInfo } : {}) };
  }
  const data = result.data;
  return ok({
    configured: data.configured,
    ...(data.current_version ? { currentVersion: data.current_version } : {}),
    ...(data.available_version ? { availableVersion: data.available_version } : {}),
    ...(data.channel ? { channel: data.channel } : {}),
    ...(data.release_notes ? { releaseNotes: data.release_notes } : {}),
    ...(data.artifact_file_name ? { artifactFileName: data.artifact_file_name } : {}),
    ...(typeof data.size_bytes === "number" ? { sizeBytes: data.size_bytes } : {}),
    updateAvailable: data.update_available,
    installReady: data.install_ready,
  });
}

function mockDesktopUpdateStatus(): DesktopUpdateStatus {
  return { configured: false, channel: "internal", updateAvailable: false, installReady: false };
}

async function streamCloudExecutionStatus(
  baseURL: string,
  workspace: ProjectWorkspaceView,
  orgID: string,
  deadline: number,
  onStatus: (workspace: ProjectWorkspaceView) => void,
): Promise<ProjectWorkspaceView | undefined> {
  const packageID = workspace.cloudRun.exchangePackageID;
  if (!packageID || typeof ReadableStream === "undefined") return undefined;
  let current = workspace;
  let lastEventID = readCloudEventCursor(packageID);
  let consecutiveFailures = 0;
  let connected = false;
  const stageHistory: LocalExecutionStageEvent[] = [];
  while (!isTerminalCloudRun(current.cloudRun.status) && Date.now() < deadline && consecutiveFailures < 3) {
    const controller = new AbortController();
    const timeout = globalThis.setTimeout(() => controller.abort(), Math.max(1, deadline - Date.now()));
    try {
      const query = `?org_id=${encodeURIComponent(orgID)}&exchange_package_id=${encodeURIComponent(packageID)}`;
      const response = await fetch(`${baseURL}/v1/desktop/projects/${encodeURIComponent(workspace.id)}/cloud/events${query}`, {
        signal: controller.signal,
        headers: {
          Accept: "text/event-stream",
          ...(lastEventID ? { "Last-Event-ID": lastEventID } : {}),
        },
      });
      if (!response.ok || !response.body || !String(response.headers?.get?.("Content-Type") ?? "").includes("text/event-stream")) {
        return undefined;
      }
      connected = true;
      let receivedEvent = false;
      await consumeSSE(response.body, (event) => {
        if (event.id) {
          lastEventID = event.id;
          writeCloudEventCursor(packageID, event.id);
        }
        if (event.type === "stage") {
          const stage = parseSSEJSON<LocalExecutionStageEvent>(event.data);
          if (!stage) return;
          const normalizedStage = event.id && !stage.event_id ? { ...stage, event_id: event.id } : stage;
          const existingIndex = normalizedStage.event_id ? stageHistory.findIndex((item) => item.event_id === normalizedStage.event_id) : -1;
          if (existingIndex >= 0) stageHistory[existingIndex] = normalizedStage;
          else stageHistory.push(normalizedStage);
          current = workspaceWithCloudStatus(current, statusFromStageEvent(current, packageID, normalizedStage, stageHistory));
        } else if (event.type === "complete") {
          const status = parseSSEJSON<LocalExecutionPackageStatusResponse>(event.data);
          if (!status) return;
          current = workspaceWithCloudStatus(current, status);
          clearCloudEventCursor(packageID);
        } else {
          return;
        }
        receivedEvent = true;
        onStatus(current);
      });
      consecutiveFailures = receivedEvent ? 0 : consecutiveFailures + 1;
    } catch {
      if (!connected) return undefined;
      consecutiveFailures += 1;
    } finally {
      globalThis.clearTimeout(timeout);
    }
    if (!isTerminalCloudRun(current.cloudRun.status) && consecutiveFailures < 3) await delay(500);
  }
  return current === workspace ? undefined : current;
}

type SSEMessage = { type: string; id: string; data: string };

async function consumeSSE(stream: ReadableStream<Uint8Array>, onEvent: (event: SSEMessage) => void): Promise<void> {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  try {
    for (;;) {
      const { done, value } = await reader.read();
      buffer += decoder.decode(value, { stream: !done }).replaceAll("\r\n", "\n");
      let boundary = buffer.indexOf("\n\n");
      while (boundary >= 0) {
        const block = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);
        const event = parseSSEBlock(block);
        if (event) onEvent(event);
        boundary = buffer.indexOf("\n\n");
      }
      if (done) break;
    }
  } finally {
    reader.releaseLock();
  }
}

function parseSSEBlock(block: string): SSEMessage | undefined {
  let type = "message";
  let id = "";
  const data: string[] = [];
  for (const line of block.split("\n")) {
    if (line.startsWith("event:")) type = line.slice(6).trimStart();
    else if (line.startsWith("id:")) id = line.slice(3).trimStart();
    else if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
  }
  return data.length ? { type, id, data: data.join("\n") } : undefined;
}

function parseSSEJSON<T>(value: string): T | undefined {
  try {
    return JSON.parse(value) as T;
  } catch {
    return undefined;
  }
}

function statusFromStageEvent(workspace: ProjectWorkspaceView, packageID: string, event: LocalExecutionStageEvent, history: LocalExecutionStageEvent[]): LocalExecutionPackageStatusResponse {
  const terminalStatus = ["failed", "canceled", "expired"].includes(event.status ?? "") ? event.status : undefined;
  return {
    exchange_package_id: packageID,
    ...(workspace.cloudRun.cloudJobID ? { cloud_job_id: workspace.cloudRun.cloudJobID } : {}),
    status: terminalStatus ?? (event.stage === "accepted" ? "accepted" : "running"),
    stage: event.stage,
    ...(event.message ? { message: event.message } : {}),
    ...(typeof event.progress_percent === "number" ? { progress_percent: event.progress_percent } : {}),
    stage_history: [...history],
    ...(event.updated_at ? { updated_at: event.updated_at } : {}),
  };
}

function cloudEventCursorKey(packageID: string): string {
  return `demoops.cloud-event-cursor.${packageID}`;
}

function readCloudEventCursor(packageID: string): string {
  try {
    return globalThis.sessionStorage?.getItem(cloudEventCursorKey(packageID)) ?? "";
  } catch {
    return "";
  }
}

function writeCloudEventCursor(packageID: string, eventID: string): void {
  try {
    globalThis.sessionStorage?.setItem(cloudEventCursorKey(packageID), eventID);
  } catch {
    // Streaming still works when WebView storage is unavailable.
  }
}

function clearCloudEventCursor(packageID: string): void {
  try {
    globalThis.sessionStorage?.removeItem(cloudEventCursorKey(packageID));
  } catch {
    // Nothing else is required for an unavailable storage backend.
  }
}

function mapCloudRunStatus(status: string | undefined): "not_uploaded" | "queued" | "running" | "succeeded" | "failed" {
  if (status === "accepted" || status === "queued" || status === "uploaded") return "queued";
  if (status === "running" || status === "validating") return "running";
  if (status === "completed" || status === "succeeded" || status === "acked") return "succeeded";
  if (status === "failed" || status === "canceled" || status === "expired") return "failed";
  return "not_uploaded";
}

function lifecycleStagesFromStatus(status: LocalExecutionPackageStatusResponse, resultPackage?: RecordingResultPackage): ServerLifecycleStageView[] {
  const currentID = lifecycleStageIDFromCloudStage(status.stage, status.status);
  const activeIndex = serverLifecycleStageOrder.indexOf(currentID);
  const terminal = mapCloudRunStatus(status.status);
  return serverLifecycleStageOrder.map((id, index) => {
    let stageStatus: ServerLifecycleStageStatus = "pending";
    if (index < activeIndex) {
      stageStatus = "completed";
    } else if (index === activeIndex) {
      stageStatus = terminal === "failed" ? "failed" : terminal === "succeeded" ? "completed" : "active";
    }
	const event = status.stage_history?.filter((item) => lifecycleStageIDFromCloudStage(item.stage, item.status) === id).at(-1);
    const time = event?.updated_at
      ? new Date(event.updated_at).toLocaleTimeString("zh-CN", { hour12: false })
      : stageStatus === "pending"
        ? undefined
        : new Date().toLocaleTimeString("zh-CN", { hour12: false });
    return {
      id,
      label: serverLifecycleStageLabels[id],
      status: stageStatus,
      progress: stageStatus === "completed" ? 100 : stageStatus === "active" ? status.progress_percent ?? event?.progress_percent ?? 20 : stageStatus === "failed" ? 100 : 0,
      summary: event?.message ?? lifecycleSummaryFromCloud(id, status, resultPackage),
      artifactCount: id === "result_returned" && resultPackage ? artifactSummaryFromResult(resultPackage).total : id === "browser_execution" && resultPackage?.status === "failed" ? diagnosticArtifactCount(resultPackage.failure_diagnostic) : 0,
      ...(time ? { time } : {}),
      ...(stageStatus === "failed" ? { errorCode: status.failure_summary?.code ?? status.error?.code ?? "recording_failed" } : {}),
    };
  });
}

function lifecycleStageIDFromCloudStage(stage: string | undefined, status?: string): ServerLifecycleStageID {
	// Stage-history items commonly have status=completed; the explicit stage
	// still determines their lifecycle card. Only a top-level status without a
	// stage may fall back to the final result card.
  if (stage === "completed" || (!stage && status === "completed")) return "result_returned";
  if (stage === "accepted") return "package_uploaded";
  if (stage === "validated" || stage === "validating" || stage === "browser_agent_planning" || stage === "script_ready") return "script_validation";
  if (stage === "preparing_worker") return "sandbox_preparing";
  if (stage === "validating_pre_execution") return "script_validation";
  if (stage === "running_script" || stage === "running_browser_agent" || stage === "validating_runtime_stage" || stage === "recording") return "browser_execution";
	if (stage === "packaging_recording" || stage === "material_validation" || stage === "directing" || stage === "rendering" || stage === "quality_validation" || stage === "validating_post_execution") return "video_rendering";
  if (stage === "failed") return "browser_execution";
  return "server_intake";
}

function lifecycleSummaryFromCloud(id: ServerLifecycleStageID, status: LocalExecutionPackageStatusResponse, resultPackage?: RecordingResultPackage): string {
  if (id === "local_generated") return "App 已生成三合一执行包。";
  if (id === "human_approved") return "产品实战模式已自动确认审批清单。";
  if (id === "upload_initialized") return "上传会话已初始化。";
  if (id === "package_uploaded") return status.exchange_package_id ? `exchange id: ${status.exchange_package_id}` : "执行包已上传。";
  if (id === "server_intake") return "服务器接收 envelope、payload_ref 和明文 dev payload。";
  if (id === "script_validation") return "服务器校验 hash、脚本策略和 allowed domains。";
  if (id === "sandbox_preparing") return "服务器准备执行 worker 和 artifact workspace。";
  if (id === "browser_execution") return status.failure_summary?.message ?? status.message ?? "浏览器执行脚本并采集素材。";
  if (id === "video_rendering") return "服务器打包录屏、截图、trace 并渲染视频。";
  return resultPackage ? `result id: ${resultPackage.result_id}` : "等待结果包返回。";
}

function cloudRunCurrentStep(status: LocalExecutionPackageStatusResponse): string {
  if (status.failure_summary?.message) return status.failure_summary.message;
  if (status.message) return status.message;
  if (status.stage) return `服务器阶段：${status.stage}`;
  return "等待服务器状态";
}

function failureSummaryText(failure?: LocalExecutionFailureSummary, error?: { code: string; message: string }): string | undefined {
  if (!failure && !error) return undefined;
  const code = failure?.code ?? error?.code ?? "recording_failed";
  const message = failure?.message ?? error?.message ?? "服务器执行失败";
  return `${code}: ${message}`;
}

function artifactSummaryFromStatus(status: LocalExecutionPackageStatusResponse): CloudArtifactSummary {
  const deliverables = status.result_summary?.deliverables ?? [];
  return { total: deliverables.length, encrypted: deliverables.filter((item) => item.sensitive).length, sensitive: deliverables.filter((item) => item.sensitive).length };
}

function assetsFromResultPackage(result: RecordingResultPackage, workspace: ProjectWorkspaceView) {
  const refs = result.delivery?.asset_refs ?? [];
  const assets = refs
    .filter((ref) => ref.kind === "video" || ref.role === "final_demo_video" || ref.kind === "demo_video" || ref.kind === "step_docs" || ref.kind === "step_by_step_docs" || ref.kind === "screenshot_pack")
    .map((ref) => ({
      assetID: ref.id,
      kind: assetKindFromArtifact(ref.kind, ref.role),
      title: assetTitleFromArtifact(ref.kind, ref.role),
      status: "generated" as const,
      uri: ref.uri,
      checksum: ref.sha256 ?? "",
      provenance: `由 ${result.source_package_id} / ${result.cloud_job_id} 生成`,
    }));
  return assets.length > 0 ? assets : workspace.assets;
}

function assetKindFromArtifact(kind?: string, role?: string): "video" | "step_docs" | "screenshot_pack" {
  if (kind === "step_docs" || kind === "step_by_step_docs" || role === "step_by_step_docs") return "step_docs";
  if (kind === "screenshot_pack" || role === "screenshot_pack") return "screenshot_pack";
  return "video";
}

function assetTitleFromArtifact(kind?: string, role?: string): string {
  if (kind === "step_docs" || kind === "step_by_step_docs" || role === "step_by_step_docs") return "步骤说明文档";
  if (kind === "screenshot_pack" || role === "screenshot_pack") return "截图包";
  return "最终演示视频";
}

function modelProvenanceFromState(report: MultimodalUnderstandingReport | undefined, scriptDocument: ExecutionScriptDocument | undefined): string[] {
  const refs = [...(report?.evidence_refs ?? []), ...(scriptDocument?.evidence_refs ?? [])];
  const values = refs
    .map((ref) => ref.summary)
    .filter((summary): summary is string => Boolean(summary?.includes("模型路由")));
  return [...new Set(values)];
}

function sourceConnectionsFromState(project: LocalProjectContext | undefined, report: MultimodalUnderstandingReport | undefined, fallback: ProjectWorkspaceView) {
  const sources = [...fallback.sourceConnections];
  const hasLocalRepo = Boolean(project?.local_repo_path || project?.inputs?.repositories?.some((repo) => repo.local_path));
  const hasGitRepo = Boolean(project?.git_repo_url || project?.inputs?.repositories?.some((repo) => repo.url));
  const digestSourceKind: "github_repo" | "local_repo" = hasGitRepo && !hasLocalRepo ? "github_repo" : "local_repo";
  const hasRequirement = Boolean(project?.product_description || project?.inputs?.requirement_documents?.length || project?.inputs?.raw_user_prompt);
  const hasScreenshots = Boolean(project?.inputs?.webpage_screenshots?.length);
  return sources.map((source) => {
    if (source.kind === "product_url") {
      return { ...source, status: project?.product_url ? "ready" as const : source.status, detail: project?.product_url ? "已进入本地理解链路并生成执行包。" : source.detail };
    }
    if (source.kind === "local_repo") {
      return { ...source, status: hasLocalRepo ? "ready" as const : "needs_attention" as const, detail: hasLocalRepo ? "已生成本地代码结构摘要和 source digest，不上传完整源码。" : "未提供本地代码目录；可改用 GitHub 仓库或需求/页面材料。" };
    }
    if (source.kind === "github_repo") {
      return { ...source, status: hasGitRepo ? "ready" as const : "needs_attention" as const, detail: hasGitRepo ? "已生成 GitHub 仓库结构摘要和 source digest，不上传完整源码。" : "未提供 GitHub 仓库 URL；可改用本地代码目录或需求/页面材料。" };
    }
    if (source.kind === "requirement_doc") {
      return { ...source, status: hasRequirement ? "ready" as const : "needs_attention" as const };
    }
    if (source.kind === "screenshot") {
      return { ...source, status: hasScreenshots ? "ready" as const : source.status };
    }
    return source;
  }).concat(report?.source_digest_sha256 ? [{
    id: "source_digest",
    kind: digestSourceKind,
    label: hasGitRepo && !hasLocalRepo ? "GitHub 理解摘要" : "代码理解摘要",
    status: "ready" as const,
    detail: `source digest: ${report.source_digest_sha256}`,
  }] : []);
}

function credentialGrantsFromInput(inputBundle: ProjectInputBundle, allowedDomains: string[]) {
  return (inputBundle.credentials ?? []).map((credential) => ({
    grantID: credential.id,
    kind: credential.kind,
    purpose: credential.required_for?.join("、") || "本地执行包生成引用",
    expiresAt: credential.expires_at ?? "人工审批后配置",
    allowedDomains,
    rawSecretVisible: false as const,
  }));
}

function outputRequestsFromScript(scriptDocument: ExecutionScriptDocument | undefined, fallback: AssetKind[]): AssetKind[] {
  if (!scriptDocument) {
    return fallback;
  }
  const outputs: AssetKind[] = [];
  if (scriptDocument.recording_run_spec.outputs.final_video) {
    outputs.push("demo_video");
  }
  if (scriptDocument.recording_run_spec.outputs.step_by_step_docs) {
    outputs.push("step_by_step_docs");
  }
  if (scriptDocument.recording_run_spec.outputs.screenshot_pack) {
    outputs.push("screenshot_pack");
  }
  return outputs.length > 0 ? outputs : fallback;
}

function localFutureWorkspace(workspace: ProjectWorkspaceView, message: string): ProjectWorkspaceView {
  return {
    ...workspace,
    cloudRun: {
      ...workspace.cloudRun,
      currentStep: message,
      status: "not_uploaded",
      progress: 0,
      lastError: message,
      stage: "future_disabled",
      message,
      stageHistory: workspace.cloudRun.stageHistory ?? mockLifecycleStages(workspace, "local_generated"),
    },
  };
}

function mockUploadInit(workspace: ProjectWorkspaceView, prefix: string): ExecutionPackageUploadInitView {
  return {
    uploadID: `upload_${prefix}_${workspace.id}`,
    serverPublicKeyID: "mock-kms-202607",
    supportedCryptoSuites: ["aes-256-gcm"],
    cascadeExecutionIPs: ["203.0.113.42"],
    maxEnvelopeBytes: 8 * 1024 * 1024,
    expiresAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
  };
}

function mockCloudRunningWorkspace(workspace: ProjectWorkspaceView): ProjectWorkspaceView {
  return {
    ...workspace,
    stage: "cloud_run",
    status: "cloud_running",
    cloudRun: {
      ...workspace.cloudRun,
      status: "running",
      stage: "running_script",
      message: "服务器已完成 intake 和脚本校验，正在一次性沙箱中执行浏览器录制。",
      currentStep: "云端执行器正在打开产品地址",
      progress: 38,
      uploadID: workspace.cloudRun.uploadID ?? `upload_mock_${workspace.id}`,
      exchangePackageID: workspace.cloudRun.exchangePackageID ?? `xpkg_${workspace.id}`,
      cloudJobID: workspace.cloudRun.cloudJobID ?? `job_${workspace.id}`,
      stageHistory: mockLifecycleStages(workspace, "browser_execution"),
      sandboxMetadata: mockSandboxMetadata(workspace),
      artifactSummary: { total: 0, encrypted: 0, sensitive: 0 },
    },
  };
}

function mockLifecycleStages(
  workspace: ProjectWorkspaceView,
  activeOrCompleted: ServerLifecycleStageID,
  options?: { failed?: boolean; resultPackage?: RecordingResultPackage },
): ServerLifecycleStageView[] {
  const activeIndex = serverLifecycleStageOrder.indexOf(activeOrCompleted);
  return serverLifecycleStageOrder.map((id, index) => {
    let status: ServerLifecycleStageStatus = "pending";
    if (index < activeIndex) {
      status = "completed";
    } else if (index === activeIndex) {
      status = options?.failed ? "failed" : activeOrCompleted === "result_returned" ? "completed" : "active";
    }
    const stage: ServerLifecycleStageView = {
      id,
      label: serverLifecycleStageLabels[id],
      status,
      progress: status === "completed" ? 100 : status === "active" ? Math.max(10, Math.min(workspace.cloudRun.progress || 38, 95)) : status === "failed" ? Math.max(workspace.cloudRun.progress || 62, 62) : 0,
      summary: mockLifecycleSummary(id, workspace, options),
      artifactCount: lifecycleArtifactCount(id, options?.resultPackage),
    };
    if (status === "completed" || status === "active" || status === "failed") {
      stage.time = new Date(Date.now() - Math.max(0, activeIndex - index) * 5000).toLocaleTimeString("zh-CN", { hour12: false });
    }
    if (status === "failed") {
      stage.errorCode = options?.resultPackage?.failure_diagnostic?.error.code ?? "recording_failed";
    }
    return stage;
  });
}

function mockLifecycleSummary(id: ServerLifecycleStageID, workspace: ProjectWorkspaceView, options?: { resultPackage?: RecordingResultPackage }): string {
  if (id === "local_generated") {
    return "App 已生成执行计划 JSON、受限 TS 脚本和中文审批文档。";
  }
  if (id === "human_approved") {
    return "审批清单确认 hash、域名、凭据范围和打码策略。";
  }
  if (id === "upload_initialized") {
    return `upload id: ${workspace.cloudRun.uploadID ?? `upload_mock_${workspace.id}`}`;
  }
  if (id === "package_uploaded") {
    return `exchange id: ${workspace.cloudRun.exchangePackageID ?? `xpkg_${workspace.id}`}`;
  }
  if (id === "server_intake") {
    return "服务器只落 metadata、digest、policy 和 artifact descriptor。";
  }
  if (id === "script_validation") {
    return "已校验 TS AST、bundle hash、allowed domains 和 redaction policy。";
  }
  if (id === "sandbox_preparing") {
    return "准备 per-job container、vault secret refs 和加密 artifact workspace。";
  }
  if (id === "browser_execution") {
    return options?.resultPackage?.status === "failed" ? "浏览器执行失败，诊断材料已脱敏加密。" : "受限 ctx.page 正在按脚本录制素材。";
  }
  if (id === "video_rendering") {
    return "无凭据渲染沙箱消费录屏、截图和 trace summary。";
  }
  return options?.resultPackage ? `result id: ${options.resultPackage.result_id}` : "等待返回 RecordingResultPackage。";
}

function lifecycleArtifactCount(id: ServerLifecycleStageID, resultPackage?: RecordingResultPackage): number {
  if (!resultPackage) {
    return 0;
  }
  if (id === "browser_execution" && resultPackage.status === "failed") {
    return diagnosticArtifactCount(resultPackage.failure_diagnostic);
  }
  if (id === "result_returned") {
    return artifactSummaryFromResult(resultPackage).total;
  }
  return 0;
}

function diagnosticArtifactCount(diagnostic: ScriptFailureDiagnostic | undefined): number {
  if (!diagnostic) {
    return 0;
  }
  return (diagnostic.screenshot_refs?.length ?? 0)
    + (diagnostic.trace_refs?.length ?? 0)
    + (diagnostic.dom_snapshot_ref ? 1 : 0)
    + (diagnostic.accessibility_snapshot_ref ? 1 : 0);
}

function artifactSummaryFromResult(result: RecordingResultPackage): CloudArtifactSummary {
  const descriptors = [
    ...(result.delivery?.asset_refs ?? []),
    ...(result.delivery?.result_package_ref ? [result.delivery.result_package_ref] : []),
    ...(result.failure_diagnostic?.screenshot_refs ?? []),
    ...(result.failure_diagnostic?.trace_refs ?? []),
    ...(result.failure_diagnostic?.dom_snapshot_ref ? [result.failure_diagnostic.dom_snapshot_ref] : []),
    ...(result.failure_diagnostic?.accessibility_snapshot_ref ? [result.failure_diagnostic.accessibility_snapshot_ref] : []),
  ];
  return {
    total: descriptors.length,
    encrypted: descriptors.filter((artifact) => artifact.encrypted).length,
    sensitive: descriptors.filter((artifact) => artifact.sensitive).length,
  };
}

function mockSandboxPolicy(workspace: ProjectWorkspaceView): SandboxPolicy {
  return {
    profile: "mvp_cloud",
    isolation_mode: "per_job_container",
    network_policy: {
      mode: "allowed_domains_only",
      allowed_domains: workspace.planReview.allowedDomains,
      allowed_cascade_endpoints: [],
      denied_cidrs: ["169.254.169.254/32", "127.0.0.0/8", "10.0.0.0/8"],
      proxy_required: true,
      dns_policy: "policy_proxy",
    },
    filesystem_policy: {
      mode: "tmpfs_workspace",
      writable_paths: ["/cascade/job-workspace"],
      no_host_mount: true,
      no_docker_socket: true,
      delete_temp_after_run: true,
    },
    resource_limits: {
      max_runtime_sec: 900,
      max_memory_mb: 2048,
      max_cpu_count: 2,
      max_disk_mb: 4096,
    },
    browser_policy: {
      fresh_context_per_run: true,
      disable_extensions: true,
      disable_downloads: true,
      trace_sources: false,
      allowed_page_methods: ["goto", "click", "fill", "locator", "waitForLoadState", "waitForTimeout"],
      allowed_context_apis: ["ctx.page", "ctx.secrets", "ctx.capture", "ctx.assert", "ctx.log"],
    },
    secret_policy: {
      vault_only: true,
      allowed_secret_refs: workspace.packagePreview.credentialGrants.map((grant) => grant.grantID),
      inject_via_context_only: true,
      forbid_env_injection: true,
      revoke_after_run: true,
      rotation_required_after_run: false,
    },
    artifact_policy: {
      encrypt_sensitive_artifacts: true,
      sensitive_by_default: true,
      recipient_kind: "app_installation",
      recipient_key_id: "app_installation_key_mock",
      retention: { failed_diagnostic_ttl_hours: 24, result_ttl_days: 7 },
      require_checksum: true,
    },
    diagnostic_policy: {
      redaction_required: true,
      forbid_full_html: true,
      strip_headers: ["authorization", "cookie"],
      strip_storage_keys: ["localStorage", "sessionStorage"],
      encrypt_diagnostics: true,
      return_repair_hints: true,
    },
    policy_hash_sha256: mockHash(`sandbox_${workspace.id}_${workspace.packagePreview.packageDigest}`),
  };
}

function mockSandboxMetadata(workspace: ProjectWorkspaceView): SandboxExecutionMetadata {
  return {
    profile: "mvp_cloud",
    isolation_mode: "per_job_container",
    network_mode: "allowed_domains_only",
    worker_id: "worker_mock_cn_1",
    container_id: `container_${workspace.id}`,
    policy_hash_sha256: workspace.scriptDocument?.recording_run_spec.sandbox_policy?.policy_hash_sha256 ?? mockHash(`sandbox_${workspace.id}_${workspace.packagePreview.packageDigest}`),
    runtime_versions: {
      chromium: "stable-pinned",
      playwright: "1.x",
      video_worker: "mock",
    },
  };
}

function mockSuccessfulRecordingResultPackage(workspace: ProjectWorkspaceView): RecordingResultPackage {
  const videoRef = {
    id: `asset_video_${workspace.id}`,
    role: "final_demo_video",
    kind: "video",
    uri: `artifact://result/${workspace.id}/final-demo.mp4.enc`,
    mime_type: "video/mp4",
    sha256: mockHash(`final_video_${workspace.id}`),
    size_bytes: 12_800_000,
    encrypted: true,
    sensitive: true,
    recipient_key_id: "app_installation_key_mock",
  };
  const docsRef = {
    id: `asset_docs_${workspace.id}`,
    role: "step_docs",
    kind: "step_docs",
    uri: `artifact://result/${workspace.id}/step-docs.md.enc`,
    mime_type: "text/markdown",
    sha256: mockHash(`step_docs_${workspace.id}`),
    size_bytes: 32000,
    encrypted: true,
    sensitive: true,
    recipient_key_id: "app_installation_key_mock",
  };
  const packageRef = {
    id: `result_package_${workspace.id}`,
    role: "recording_result_package",
    kind: "json",
    uri: `artifact://result/${workspace.id}/result-package.json.enc`,
    mime_type: "application/json",
    sha256: mockHash(`result_package_${workspace.id}`),
    size_bytes: 48000,
    encrypted: true,
    sensitive: true,
    recipient_key_id: "app_installation_key_mock",
  };
  const sandbox = mockSandboxMetadata(workspace);
  return {
    result_id: `result_${workspace.id}`,
    source_package_id: workspace.packagePreview.packageID,
    cloud_job_id: workspace.cloudRun.cloudJobID ?? `job_${workspace.id}`,
    schema_version: "demoops.recording_result_package.v1",
    status: "generated",
    execution_trace: {
      id: `trace_${workspace.id}`,
      workflow_graph_id: workspace.planReview.graph.id,
      graph_version: workspace.planReview.graph.version,
      pass_rate: 1,
      step_results: workspace.planReview.graph.nodes.map((node) => ({ node_id: node.id, status: "passed", duration_ms: node.duration_hint_ms ?? 3000 })),
      sandbox,
    },
    step_results: workspace.planReview.graph.nodes.map((node) => ({ node_id: node.id, status: "passed", duration_ms: node.duration_hint_ms ?? 3000 })),
    generated_assets: [
      { id: videoRef.id, kind: "video", uri: videoRef.uri, mime_type: videoRef.mime_type, sha256: videoRef.sha256, size_bytes: videoRef.size_bytes, sensitive: true },
      { id: docsRef.id, kind: "step_docs", uri: docsRef.uri, mime_type: docsRef.mime_type, sha256: docsRef.sha256, size_bytes: docsRef.size_bytes, sensitive: true },
    ],
    verification_report: {
      pass_rate: 1,
      failed_node_ids: [],
      reproducibility_match: true,
      output_checksums: [
        { id: videoRef.id, kind: videoRef.kind, sha256: videoRef.sha256, size_bytes: videoRef.size_bytes },
        { id: docsRef.id, kind: docsRef.kind, sha256: docsRef.sha256, size_bytes: docsRef.size_bytes },
      ],
    },
    audit_trail: {
      sandbox,
      source_package_digest: workspace.packagePreview.packageDigest,
      graph_digest: workspace.packagePreview.graphDigest,
      execution_ip: "203.0.113.42",
      ...(sandbox.worker_id ? { cloud_worker_id: sandbox.worker_id } : {}),
      ...(sandbox.runtime_versions ? { runtime_versions: sandbox.runtime_versions } : {}),
    },
    delivery: {
      result_package_ref: packageRef,
      asset_refs: [videoRef, docsRef],
      recipient_kind: "app_installation",
      recipient_key_id: "app_installation_key_mock",
      encryption_alg: "aes-256-gcm",
      expires_at: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString(),
      ack_required: true,
    },
    created_at: new Date().toISOString(),
  };
}

function ackWorkspaceAssets(workspace: ProjectWorkspaceView): ProjectWorkspaceView {
  return {
    ...workspace,
	assets: workspace.assets,
    cloudRun: {
      ...workspace.cloudRun,
		message: "App 已下载全部成品、校验 checksum 并确认接收结果包。",
    },
  };
}

function workspaceWithReview(workspace: ProjectWorkspaceView, review: LocalResultReviewRecord, revision?: LocalResultRevisionRecord): ProjectWorkspaceView {
	const approved = review.decision === "approved";
	return {
	  ...workspace,
	  status: approved ? "asset_ready" : workspace.status,
	  assets: workspace.assets.map((asset) => ({ ...asset, status: approved ? "approved" : "changes_requested" })),
	  cloudRun: {
		...workspace.cloudRun,
		message: approved ? "用户已人工批准成品。" : revision?.resolved_action === "rerecord" ? "已提交重新录制请求。" : "已提交重新剪辑请求。",
		resultReview: {
		  decision: review.decision,
		  reviewID: review.review_id,
		  ...(revision?.revision_id ? { revisionID: revision.revision_id } : {}),
		  ...(revision?.resolved_action ? { revisionAction: revision.resolved_action } : {}),
		  ...(review.summary ? { summary: review.summary } : {}),
		  updatedAt: review.reviewed_at,
		},
	  },
	};
}

function runtimeHealthFromLocal(local: LocalRuntimeHealth): RuntimeHealthView {
  const providers: RuntimeHealthView["modelProviders"] = {};
  for (const [key, value] of Object.entries(local.model_providers ?? {})) {
    providers[key] = {
      apiKeyEnv: value.api_key_env,
      ...(value.api_key_source_env ? { apiKeySourceEnv: value.api_key_source_env } : {}),
      ...(value.api_key_fallback_envs?.length ? { apiKeyFallbackEnvs: value.api_key_fallback_envs } : {}),
      configured: value.configured,
      baseURLConfigured: value.base_url_configured,
      defaultModelConfigured: value.default_model_configured,
    };
  }
  const routes: RuntimeHealthView["modelTaskRoutes"] = {};
  for (const [key, value] of Object.entries(local.model_task_routes ?? {})) {
    routes[key] = {
      provider: value.provider,
      model: value.model,
      providerOverride: value.provider_override,
      modelOverride: value.model_override,
    };
  }
  const health: RuntimeHealthView = {
    profile: local.profile,
    databaseConfigured: Boolean(local.database_configured),
    localDataConfigured: Boolean(local.local_data_configured),
    resourceManifestLoaded: Boolean(local.resource_manifest_loaded),
    sidecars: local.sidecars ?? {},
    modelProviders: providers,
    modelTaskRoutes: routes,
  };
  if (local.llm_mode !== undefined) {
    health.llmMode = local.llm_mode;
  }
  if (local.model_adapter_version !== undefined) {
    health.modelAdapterVersion = local.model_adapter_version;
  }
  if (local.cloud_exchange) {
    health.cloudExchange = {
      configured: local.cloud_exchange.configured,
      exchangeDiscovered: Boolean(local.cloud_exchange.exchange_discovered),
      installationPaired: Boolean(local.cloud_exchange.installation_paired),
      sessionValid: Boolean(local.cloud_exchange.session_valid),
      ...(local.cloud_exchange.base_url_host ? { baseURLHost: local.cloud_exchange.base_url_host } : {}),
      ...(local.cloud_exchange.base_url_path ? { baseURLPath: local.cloud_exchange.base_url_path } : {}),
      ...(local.cloud_exchange.server_key_id ? { serverKeyID: local.cloud_exchange.server_key_id } : {}),
      ...(local.cloud_exchange.install_id_suffix ? { installIDSuffix: local.cloud_exchange.install_id_suffix } : {}),
      authMode: local.cloud_exchange.auth_mode ?? "unpaired",
      ...(local.cloud_exchange.environment ? { environment: local.cloud_exchange.environment } : {}),
      devPlaintext: Boolean(local.cloud_exchange.dev_plaintext),
    };
  }
  if (local.app_capabilities) {
    health.appCapabilities = {
      developerUI: Boolean(local.app_capabilities.developer_ui),
      demoAssetGenerationConsole: Boolean(local.app_capabilities.demo_asset_generation_console),
      videoEditor: Boolean(local.app_capabilities.video_editor),
      localPackageGeneration: Boolean(local.app_capabilities.local_package_generation),
      stagePlanReview: Boolean(local.app_capabilities.stage_plan_review),
      executionPackageApproval: Boolean(local.app_capabilities.execution_package_approval),
      approvedPackageUpload: Boolean(local.app_capabilities.approved_package_upload),
      resultVideoDownload: Boolean(local.app_capabilities.result_video_download),
      errorReportDownload: Boolean(local.app_capabilities.error_report_download),
      serverRecordingRequired: Boolean(local.app_capabilities.server_recording_required),
      localRecordingExecution: Boolean(local.app_capabilities.local_recording_execution),
      localRecordingScope: local.app_capabilities.local_recording_scope ?? "unknown",
      videoWorkerRole: local.app_capabilities.video_worker_role ?? "unknown",
    };
  }
  return health;
}

function modelDiagnosticFromLocal(local: LocalModelDiagnostic): ModelDiagnosticResult {
  return {
    provider: local.provider,
    ...(local.task ? { task: local.task } : {}),
    model: local.model,
    adapterVersion: local.adapter_version,
    mode: local.mode,
    baseURLHost: local.base_url_host,
    baseURLPath: local.base_url_path,
    configured: local.configured,
    ok: local.ok,
    ...(local.http_status !== undefined ? { httpStatus: local.http_status } : {}),
    ...(local.error_class ? { errorClass: local.error_class } : {}),
    ...(local.error ? { error: local.error } : {}),
    ...(local.latency_ms !== undefined ? { latencyMS: local.latency_ms } : {}),
    checkedAt: local.checked_at,
  };
}

function mockDiagnostic(provider: string, task: string, model: string): ModelDiagnosticResult {
  return {
    provider,
    task,
    model,
    adapterVersion: "domestic-llm-adapter-v1",
    mode: "mock",
    baseURLHost: `${provider}.example`,
    baseURLPath: "/v1",
    configured: false,
    ok: false,
    errorClass: "mock_mode",
    error: "mock bridge 不调用真实模型",
    checkedAt: new Date().toISOString(),
  };
}

function mockUnderstandingReport(workspace: ProjectWorkspaceView): MultimodalUnderstandingReport {
  const objective = workspace.planReview.graph.intent?.objective;
  const useCase = workspace.planReview.graph.intent?.use_case;
  return {
    id: `understanding_${workspace.id}`,
    project_id: workspace.id,
    schema_version: "demoops.multimodal_understanding_report.v1",
    summary: "已融合需求、代码结构摘要和页面截图证据。",
    requirement_brief: {
      id: `requirement_${workspace.id}`,
      project_id: workspace.id,
      target_audience: workspace.targetAudience,
      required_assets: workspace.planReview.outputRequests,
      confidence: 0.82,
      ...(objective ? { objective } : {}),
      ...(useCase ? { use_cases: [useCase] } : {}),
    },
    page_snapshots: workspace.understanding.evidenceRefs
      .filter((ref) => ref.kind === "webpage_screenshot")
      .map((ref) => ({
        id: ref.id,
        project_id: workspace.id,
        ...(ref.summary ? { title: ref.summary } : {}),
        ...(ref.confidence !== undefined ? { confidence: ref.confidence } : {}),
      })),
    evidence_refs: workspace.understanding.evidenceRefs,
    input_fingerprints: { graph: workspace.packagePreview.graphDigest },
    source_digest_sha256: workspace.packagePreview.packageDigest,
    confidence: 0.84,
  };
}

function mockScriptDocument(workspace: ProjectWorkspaceView): ExecutionScriptDocument {
  const graph = workspace.planReview.graph;
  return {
    id: `script_${graph.id}`,
    project_id: workspace.id,
    workflow_graph_id: graph.id,
    graph_version: graph.version,
    schema_version: "demoops.execution_script_document.v1",
    status: "review_ready",
    language: "zh-CN",
    workflow_graph: graph,
    ...(graph.name ? { title: graph.name } : {}),
    ...(graph.summary ? { summary: graph.summary } : {}),
    recording_run_spec: {
      run_id: `run_${graph.id}`,
      base_url: workspace.productURL,
      allowed_domains: workspace.planReview.allowedDomains,
      timezone: "Asia/Shanghai",
      locale: "zh-CN",
      browser: {
        engine: "chromium",
        version_policy: "stable-pinned",
        headless: true,
        ...(graph.execution?.viewports ? { viewports: graph.execution.viewports } : {}),
      },
      timeline: {
        target_duration_sec: workspace.planReview.targetDurationSec,
        node_timing_hints: graph.nodes.map((node) => ({ node_id: node.id, duration_ms: node.duration_hint_ms ?? 3000 })),
      },
      outputs: { raw_recording: true, final_video: true, screenshot_pack: false, step_by_step_docs: true, trace: true },
      redactions: { mask_selectors: workspace.planReview.redactionSelectors },
      failure_policy: { retry_attempts: 2, selector_repair_allowed: true, data_repair_allowed: false, max_repair_attempts: 1 },
      sandbox_policy: mockSandboxPolicy(workspace),
    },
    steps: graph.nodes.map((node, index) => {
      const pageTarget = {
        ...(node.action_spec?.target.url ? { url: node.action_spec.target.url } : {}),
        ...(node.action_spec?.target.selector || node.selector ? { selector: node.action_spec?.target.selector ?? node.selector } : {}),
      };
      const action = {
        type: node.action_spec?.type ?? "inspect",
        target: node.action_spec?.target ?? { selector: node.selector },
        ...(node.action_spec?.timeout_ms !== undefined ? { timeout_ms: node.action_spec.timeout_ms } : {}),
        ...(node.action_spec?.wait_until ? { wait_until: node.action_spec.wait_until } : {}),
      };
      return {
        id: `step_${index + 1}_${node.id}`,
        order: index + 1,
        node_id: node.id,
        ...(node.title ? { title: node.title } : {}),
        ...(node.goal ? { business_value: node.goal } : {}),
        page_target: pageTarget,
        action,
        expected_outcome: node.expected_outcome,
        validations: node.validations ?? [],
        capture: node.capture ?? { screenshot: node.is_screenshot, video: true, zoom: node.has_zoom },
        timing: { node_id: node.id, duration_ms: node.duration_hint_ms ?? 3000 },
        narrative: node.narrative ?? { ...(node.title ? { title: node.title } : {}), caption: node.expected_outcome },
        ...(node.evidence_refs ? { evidence_refs: node.evidence_refs } : {}),
        blocking: true,
      };
    }),
    safety_policy: {
      allowed_domains: workspace.planReview.allowedDomains,
      forbidden_pages: workspace.planReview.forbiddenPages,
      forbidden_data: [],
      redactions: { mask_selectors: workspace.planReview.redactionSelectors },
      pii_handling: "mask_in_artifacts",
    },
    reproducibility: {
      graph_hash_sha256: workspace.packagePreview.graphDigest,
      script_hash_sha256: workspace.packagePreview.packageDigest,
      input_fingerprints: { package: workspace.packagePreview.packageDigest },
      deterministic_seed: `seed_${workspace.id}`,
    },
    approval_checklist: {
      human_approval_required: true,
      source_summary_only: true,
      credential_scope_review_required: true,
      redactions_review_required: true,
      ip_allowlist_acknowledgement_required: true,
      blocking_reasons: workspace.packagePreview.blockedReasons,
    },
    evidence_refs: workspace.understanding.evidenceRefs,
  };
}

function mockScriptMarkdown(workspace: ProjectWorkspaceView): string {
  return `# ${workspace.planReview.graph.name}\n\n## 演示目标\n\n${workspace.planReview.graph.summary}\n\n## 生成依据\n\n- 需求、代码结构摘要和页面证据已融合。\n- 本地代码只用于生成结构摘要，不上传完整源码。\n\n## 安全策略\n\n- 允许域名：${workspace.planReview.allowedDomains.join(", ")}\n- 禁止页面：${workspace.planReview.forbiddenPages.join(", ")}\n- 打码选择器：${workspace.planReview.redactionSelectors.join(", ")}\n\n## 录制策略\n\n- 目标时长：${workspace.planReview.targetDurationSec} 秒\n- 输出资产：演示视频 / 步骤文档\n\n## 凭据与云端边界\n\n- 凭据只通过 secret_ref 使用，不展示明文。\n- 云端执行前必须校验 Stage JSON、Browser Agent 大纲、hash 和 allowed domains。\n\n## 执行步骤\n\n${workspace.planReview.graph.nodes
    .map((node, index) => `${index + 1}. ${node.title ?? node.action}: ${node.expected_outcome}`)
    .join("\n")}\n\n## 审批清单\n\n- [ ] 上传前必须完成人工审批。`;
}

function mockStageApprovalPlan(workspace: ProjectWorkspaceView, plan: ExecutionScriptDocument) {
  return {
    id: `stage_plan_${plan.id}`,
    project_id: workspace.id,
    workflow_graph_id: plan.workflow_graph_id,
    schema_version: "demoops.stage_approval_plan.v1",
    title: plan.title ?? workspace.planReview.graph.name ?? "Browser Agent Outline",
    summary: "App 端确认用户意图、stage 顺序、业务目标和证据链；服务器 browser agent 只在该边界内自适应探索。",
    runtime: "browser-agent-outline-v1",
    language: "zh-CN",
    stages: plan.steps.map((step) => {
      const duration = explicitStepDurationMS(step);
      const route = routeFromStep(workspace, step);
      return {
        id: `stage_${step.node_id}`,
        order: step.order,
        node_id: step.node_id,
        stage_kind: stageKindFromAction(step.action.type),
        route_state: routeStateFromStep(step, route),
        title: step.title ?? step.node_id,
        objective: step.expected_outcome,
        business_intent: step.business_value ?? step.narrative.voiceover ?? step.narrative.caption ?? step.expected_outcome,
        ...(duration > 0 ? { duration_ms: duration } : {}),
        target_route: route,
        target_url: step.page_target.url ?? step.action.target.url ?? workspace.productURL,
        component_refs: [`component:${step.node_id}`],
        api_refs: step.action.type === "api_call" ? [`api:${step.node_id}`] : [],
        style_refs: [`style:${step.node_id}`],
        data_model_refs: [`model:${step.node_id}`],
        input_content: step.action.value
          ? [{ kind: "user_text", label: step.title ?? step.node_id, value: step.action.value, editable: false, evidence_refs: step.evidence_refs ?? [] }]
          : step.action.secret_ref
            ? [{ kind: "secret_ref", label: "演示凭据", secret_ref: step.action.secret_ref, editable: false, evidence_refs: step.evidence_refs ?? [] }]
            : [],
        interaction: {
          kind: step.action.type,
          target: step.action.target,
          ...(step.action.value ? { value: step.action.value } : {}),
          ...(step.action.input_ref ? { input_ref: step.action.input_ref } : {}),
          ...(step.action.secret_ref ? { secret_ref: step.action.secret_ref } : {}),
          wait_until: step.action.wait_until ?? "render_stable",
          wait_conditions: ["wait_after_entry_at_least_1000ms", "wait_for_render_stable_before_capture"],
          non_destructive: !["upload", "api_call"].includes(step.action.type),
          selector_policy: "server_may_adapt_selector_within_evidence_chain",
          evidence_refs: step.evidence_refs ?? [],
        },
        success_state: step.expected_outcome,
        wait_conditions: ["wait_after_entry_at_least_1000ms", "wait_for_network_or_dom_stable", "wait_for_render_stable_before_capture"],
        capture_points: step.capture.screenshot ? ["stage_entry_after_render", "stage_success_state"] : ["stage_success_state"],
        capture_plan: mockCapturePlanForStep(step, duration),
        risk_notes: ["不得改写用户意图、stage 顺序、填充语义或安全边界。"],
        evidence_refs: step.evidence_refs ?? workspace.understanding.evidenceRefs,
        confidence: 0.82,
      };
    }),
    safety_policy: plan.safety_policy,
    evidence_refs: workspace.understanding.evidenceRefs,
    confidence: 0.82,
  };
}

function mockBrowserAgentOutline(workspace: ProjectWorkspaceView, plan: ExecutionScriptDocument) {
  const origin = originFromURL(workspace.productURL);
  return {
    id: `outline_${plan.id}`,
    project_id: workspace.id,
    workflow_graph_id: plan.workflow_graph_id,
    schema_version: "demoops.browser_agent_script_outline.v1",
    runtime: "browser-agent-outline-v1",
    base_url: workspace.productURL,
    product_origin: origin,
    summary: "服务器 browser agent 按 stage JSON 执行：可探索同源产品页面，可调整 selector/等待/截图时机，不可改变用户需求和安全范围。",
    stages: plan.steps.map((step) => {
      const selector = step.action.target.selector ?? step.page_target.selector ?? "";
      const route = routeFromStep(workspace, step);
      const duration = explicitStepDurationMS(step);
      return {
        id: `outline_stage_${step.node_id}`,
        stage_id: `stage_${step.node_id}`,
        order: step.order,
        node_id: step.node_id,
        objective: step.expected_outcome,
        route,
        url: step.page_target.url ?? step.action.target.url ?? workspace.productURL,
        components: [
          {
            component_ref: `component:${step.node_id}`,
            route_ref: route,
            role: roleFromAction(step.action.type),
            name: step.title ?? step.action.type,
            text: step.action.value || step.title || step.action.type,
            selector,
            selector_alternatives: selector ? [{ kind: "css", value: selector, confidence: 0.72, stability_score: 0.68, source: "workflow_graph" }] : [],
            evidence_refs: step.evidence_refs ?? workspace.understanding.evidenceRefs,
            confidence: selector ? 0.78 : 0.64,
          },
        ],
        interactions: [
          {
            kind: step.action.type,
            target: step.action.target,
            ...(step.action.value ? { value: step.action.value } : {}),
            ...(step.action.input_ref ? { input_ref: step.action.input_ref } : {}),
            ...(step.action.secret_ref ? { secret_ref: step.action.secret_ref } : {}),
            wait_until: step.action.wait_until ?? "render_stable",
            wait_conditions: ["wait_after_entry_at_least_1000ms", "wait_for_render_stable_before_capture"],
            non_destructive: step.action.type !== "api_call",
            selector_policy: "prefer_role_name_testid_then_verified_css; never use control-plane urls",
            evidence_refs: step.evidence_refs ?? workspace.understanding.evidenceRefs,
          },
        ],
        wait_conditions: ["wait_after_entry_at_least_1000ms", "wait_for_render_stable_before_capture"],
        capture_points: ["after_entry_render_stable", "after_success_state"],
        success_state: step.expected_outcome,
        ...(duration > 0 ? { duration_ms: duration } : {}),
        capture_plan: mockCapturePlanForStep(step, duration),
        can_modify: ["selector", "selector_alternatives", "wait_conditions", "capture_points", "non_destructive_exploration_path", "capture_plan.pre_capture_wait_ms", "capture_plan.hold_after_ms"],
        must_preserve: ["objective", "business_intent", "input semantics", "stage order", "explicit duration requirement", "allowed domains", "forbidden pages"],
        evidence_refs: step.evidence_refs ?? workspace.understanding.evidenceRefs,
        confidence: 0.8,
      };
    }),
    allowed_exploration_scope: {
      allowed_origins: origin ? [origin] : [],
      allowed_routes: Array.from(new Set(plan.steps.map((step) => routeFromStep(workspace, step)).filter(Boolean))),
      forbidden_path_prefixes: ["/aigc", "/.well-known", "/v1", "/api/debug", ...workspace.planReview.forbiddenPages],
      forbidden_keywords: ["delete", "remove", "billing", "api key", "payment", "logout"],
      max_depth: 3,
      allow_non_destructive: true,
    },
    forbidden_actions: ["delete", "payment", "billing_update", "api_key_create", "logout", "control_plane_exchange_call"],
    server_editable_fields: ["script_outline.stages[].components[].selector", "script_outline.stages[].components[].selector_alternatives", "script_outline.stages[].wait_conditions", "script_outline.stages[].capture_points"],
    immutable_fields: ["stage_approval_plan.stages[].objective", "stage_approval_plan.stages[].business_intent", "stage_approval_plan.stages[].input_content", "security_policy", "recording_run_spec.allowed_domains"],
    evidence_refs: workspace.understanding.evidenceRefs,
    confidence: 0.8,
  };
}

function explicitStepDurationMS(step: ScriptStep): number {
  const duration = step.timing?.duration_ms ?? 0;
  return duration > 0 ? duration : 0;
}

function mockCapturePlanForStep(step: ScriptStep, durationMS: number) {
  const requiredAssets = [
    ...(step.capture.video ? ["stage_video"] : []),
    ...(step.capture.screenshot ? ["viewport_screenshot"] : []),
  ];
  return {
    intent: durationMS > 0
      ? `按用户明确时长要求采集 ${Math.ceil(durationMS / 1000)} 秒素材。`
      : "按 stage 结果采集素材，等待页面稳定后截图或录屏。",
    primary_artifact: requiredAssets[0] ?? "redacted_trace_observation",
    required_assets: requiredAssets.length > 0 ? requiredAssets : ["redacted_trace_observation"],
    ...(durationMS > 0 ? { min_duration_ms: durationMS } : {}),
    pre_capture_wait_ms: 1000,
    clip_suggestion: durationMS > 0 ? "不得压缩低于用户明确时长。" : "由 server browser agent 按页面状态和审批目标控制节奏。",
    notes: ["进入页面后至少等待 1 秒并确认渲染稳定再截图。"],
  };
}

function mockBrowserAgentPromptPolicy(workspace: ProjectWorkspaceView, plan: ExecutionScriptDocument) {
  return {
    id: `prompt_policy_${plan.id}`,
    project_id: workspace.id,
    workflow_graph_id: plan.workflow_graph_id,
    schema_version: "demoops.browser_agent_prompt_policy.v1",
    runtime: "browser-agent-outline-v1",
    system_prompt:
      "你是 Cascade 云端 browser agent。严格执行 StageApprovalPlan 与 BrowserAgentScriptOutline。可以在同源产品页面内自适应探索、替换 selector、调整等待和截图时机；不得改变用户需求、stage 目标、填充语义、凭据引用、安全边界或访问控制面路径。任何不确定项必须回传诊断，不得猜测。",
    immutable_fields: ["user intent", "stage order", "stage objectives", "input semantics", "secret_ref", "allowed_domains", "forbidden_pages", "redaction policy"],
    editable_fields: ["selector", "selector alternatives", "wait conditions", "non destructive exploration path", "capture timing"],
    forbidden_changes: ["不要访问 /aigc、/.well-known、/v1、execution-packages、app-installations 等控制面接口", "不要把 secret_ref 展开为明文", "不要创建破坏性数据或支付/删除类操作"],
    repair_policy: ["selector 不可见时优先用 role/name/text/testid 在同一业务语义内修复", "页面未加载完成时延长等待并重新扫描 DOM/a11y", "无法确认业务动作时返回 missing_product_evidence"],
    evidence_policy: ["每个动作必须绑定 stage、route/component 或页面扫描证据", "服务器可补充 runtime evidence，但不可覆盖 App 审批目标"],
    safety_boundaries: [`allowed_domains=${workspace.planReview.allowedDomains.join(",")}`, `forbidden_pages=${workspace.planReview.forbiddenPages.join(",")}`, "result artifacts sensitive/encrypted"],
    human_review_required: true,
  };
}

function mockUnderstandingDossier(workspace: ProjectWorkspaceView, plan: ExecutionScriptDocument) {
  const routeEvidence = plan.steps.map((step) => ({
    id: `route_ev_${step.node_id}`,
    kind: "route",
    label: step.title ?? step.node_id,
    summary: `需求 stage ${step.node_id} 对应页面：${step.page_target.url ?? step.action.target.url ?? workspace.productURL}`,
    route: routeFromStep(workspace, step),
    component_ref: `component:${step.node_id}`,
    file_path_hash_sha256: mockHash(`route:${workspace.id}:${step.node_id}`),
    evidence_refs: step.evidence_refs ?? workspace.understanding.evidenceRefs,
    confidence: 0.78,
  }));
  const componentEvidence = plan.steps.map((step) => ({
    id: `component_ev_${step.node_id}`,
    kind: "component",
    label: step.title ?? step.action.type,
    summary: `与需求动作 ${step.action.type} 相关的可交互组件摘要；服务器可在运行时用页面扫描确认最终 selector。`,
    route: routeFromStep(workspace, step),
    component_ref: `component:${step.node_id}`,
    source_path_hash_sha256: mockHash(`component:${workspace.id}:${step.node_id}`),
    evidence_refs: step.evidence_refs ?? workspace.understanding.evidenceRefs,
    confidence: 0.76,
  }));
  return {
    id: `dossier_${plan.id}`,
    project_id: workspace.id,
    workflow_graph_id: plan.workflow_graph_id,
    schema_version: "demoops.project_understanding_dossier.v1",
    summary: "需求相关项目理解包：只包含 route/component/style/API/data model 摘要、hash 和证据引用，不上传完整源码。",
    requirement_objective: workspace.inputBundle.raw_user_prompt ?? workspace.planReview.graph.summary ?? "Approved demo objective",
    architecture_summary: `已围绕 ${workspace.planReview.graph.name} 追踪路由、交互组件、样式状态和后端/API 依赖摘要。`,
    route_evidence: routeEvidence,
    component_evidence: componentEvidence,
    style_evidence: plan.steps.map((step) => ({
      id: `style_ev_${step.node_id}`,
      kind: "style",
      label: step.title ?? step.node_id,
      summary: "样式证据以哈希与摘要形式提供，服务器不得据此猜测未证实页面。",
      component_ref: `component:${step.node_id}`,
      file_path_hash_sha256: mockHash(`style:${workspace.id}:${step.node_id}`),
      evidence_refs: step.evidence_refs ?? workspace.understanding.evidenceRefs,
      confidence: 0.7,
    })),
    api_evidence: [],
    data_model_evidence: [],
    interaction_evidence: componentEvidence,
    security_evidence: [
      {
        id: `security_ev_${workspace.id}`,
        kind: "security",
        label: "凭据与打码边界",
        summary: "凭据只以 secret_ref 注入；截图和 trace 需按 sensitive artifact 处理。",
        evidence_refs: workspace.understanding.evidenceRefs,
        confidence: 0.9,
      },
    ],
    source_digest_sha256: workspace.packagePreview.packageDigest,
    input_fingerprints: { package: workspace.packagePreview.packageDigest, graph: workspace.packagePreview.graphDigest },
    evidence_refs: workspace.understanding.evidenceRefs,
    confidence: 0.78,
  };
}

function mockExecutableScriptBundle(
  workspace: ProjectWorkspaceView,
  repair?: { diagnostic: ScriptFailureDiagnostic; sourceResultID: string; sourceCloudJobID: string; repairAttempt: number },
): ExecutableRecordingScriptBundle {
  const plan = workspace.scriptDocument ?? mockScriptDocument(workspace);
  const markdown = workspace.scriptMarkdown ?? mockScriptMarkdown(workspace);
  const planHash = mockHash(JSON.stringify(plan));
  const stagePlan = mockStageApprovalPlan(workspace, plan);
  const outline = mockBrowserAgentOutline(workspace, plan);
  const promptPolicy = mockBrowserAgentPromptPolicy(workspace, plan);
  const dossier = mockUnderstandingDossier(workspace, plan);
  const stagePlanHash = mockHash(JSON.stringify(stagePlan));
  const outlineHash = mockHash(JSON.stringify(outline));
  const promptPolicyHash = mockHash(JSON.stringify(promptPolicy));
  const dossierHash = mockHash(JSON.stringify(dossier));
  const markdownHash = mockHash(markdown);
  const bundleSeed = `${planHash}|${stagePlanHash}|${outlineHash}|${promptPolicyHash}|${dossierHash}|${markdownHash}|${workspace.id}`;
  return {
    id: `bundle_${plan.id}`,
    project_id: workspace.id,
    workflow_graph_id: plan.workflow_graph_id,
    schema_version: "demoops.executable_recording_script_bundle.v1",
    status: "review_ready",
    script_manifest: {
      script_id: `recording_${plan.workflow_graph_id}`,
      version: 1,
      language: "browser-agent-outline",
      runtime: "browser-agent-outline-v1",
      entry_function: "runBrowserAgentOutline",
      generator: "cascade_browser_agent_outline_packager",
      generator_version: "0.1.0",
      dependency_allowlist: [],
      context_apis: ["browser_agent.explore", "browser_agent.act", "browser_agent.capture", "browser_agent.report"],
      step_node_ids: plan.steps.map((step) => step.node_id),
    },
    plan_json: plan,
    playwright_script: {
      mime_type: "application/x.browser-agent-outline+json",
      sha256: "",
      size_bytes: 0,
      encrypted: false,
    },
    stage_approval_plan: stagePlan,
    script_outline: outline,
    agent_prompt_policy: promptPolicy,
    understanding_dossier_ref: {
      id: `artifact_${plan.id}_understanding_dossier`,
      kind: "project_understanding_dossier",
      uri: `inline://project-understanding/${plan.id}`,
      mime_type: "application/json",
      sha256: dossierHash,
      sensitive: true,
    },
    project_understanding_dossier: dossier,
    approval_markdown: {
      inline_markdown: markdown,
      mime_type: "text/markdown",
      sha256: markdownHash,
      size_bytes: markdown.length,
    },
    security_policy: {
      redactions: plan.safety_policy.redactions,
      secret_refs: plan.steps.map((step) => step.action.secret_ref).filter((ref): ref is string => Boolean(ref)),
      allowed_context_apis: ["browser_agent.explore", "browser_agent.act", "browser_agent.capture", "browser_agent.report"],
      allowed_page_methods: ["goto", "click", "fill", "selectOption", "setInputFiles", "waitForLoadState", "locator"],
      forbidden_imports: ["fs", "node:fs", "child_process", "node:child_process", "http", "https", "net", "tls"],
      forbidden_identifiers: ["import", "require", "eval", "Function", "process", "global", "globalThis", "window", "document", "fetch"],
      network_policy: "allowed_domains_only_via_ctx_page",
      file_system_policy: "no_direct_fs_access",
      ...(plan.safety_policy.allowed_domains ? { allowed_domains: plan.safety_policy.allowed_domains } : {}),
      ...(plan.safety_policy.forbidden_pages ? { forbidden_pages: plan.safety_policy.forbidden_pages } : {}),
      ...(plan.safety_policy.forbidden_data ? { forbidden_data: plan.safety_policy.forbidden_data } : {}),
    },
    reproducibility: {
      plan_hash_sha256: planHash,
      stage_plan_hash_sha256: stagePlanHash,
      outline_hash_sha256: outlineHash,
      prompt_policy_hash_sha256: promptPolicyHash,
      understanding_dossier_hash_sha256: dossierHash,
      markdown_hash_sha256: markdownHash,
      bundle_hash_sha256: mockHash(bundleSeed),
      graph_hash_sha256: workspace.packagePreview.graphDigest,
      source_snapshot_digest: workspace.packagePreview.packageDigest,
      generator_version: "browser-agent-outline-v1",
      deterministic_seed: `seed_${workspace.id}`,
      input_fingerprints: { package: workspace.packagePreview.packageDigest },
    },
    validation: { valid: true, findings: [] },
    ...(repair
      ? {
          repair_lineage: {
            base_bundle_id: workspace.executableScriptBundle?.id ?? `bundle_${plan.id}`,
            base_bundle_hash_sha256: workspace.executableScriptBundle?.reproducibility.bundle_hash_sha256 ?? workspace.packagePreview.packageDigest,
            source_result_id: repair.sourceResultID,
            source_cloud_job_id: repair.sourceCloudJobID,
            repair_attempt: repair.repairAttempt,
            change_summary: `根据失败节点 ${repair.diagnostic.failed_node_id} 的脱敏截图和错误信息修复 selector/等待条件。`,
            diagnostic_refs: [{ id: repair.diagnostic.id, kind: "browser_trace", summary: repair.diagnostic.error.message }],
            created_at: new Date().toISOString(),
          },
        }
      : {}),
  };
}

function mockFailedRecordingResultPackage(workspace: ProjectWorkspaceView): RecordingResultPackage {
  const diagnostic = mockFailureDiagnostic(workspace);
  return {
    result_id: `result_failed_${workspace.id}`,
    source_package_id: workspace.packagePreview.packageID,
    cloud_job_id: workspace.cloudRun.cloudJobID ?? `job_${workspace.id}`,
    schema_version: "demoops.recording_result_package.v1",
    status: "failed",
    step_results: [
      { node_id: workspace.planReview.graph.nodes[0]?.id ?? "node_start", status: "passed", duration_ms: 1200 },
      {
        node_id: diagnostic.failed_node_id,
        status: "failed",
        duration_ms: workspace.planReview.graph.nodes.find((node) => node.id === diagnostic.failed_node_id)?.duration_hint_ms ?? 0,
        error: diagnostic.error,
        ...(diagnostic.screenshot_refs
          ? {
              artifacts: diagnostic.screenshot_refs.map((ref) => ({
                id: ref.id,
                kind: ref.kind,
                uri: ref.uri,
                ...(ref.mime_type ? { mime_type: ref.mime_type } : {}),
                sha256: ref.sha256,
                sensitive: true,
                source_node_id: diagnostic.failed_node_id,
              })),
            }
          : {}),
      },
    ],
    verification_report: {
      pass_rate: 0.5,
      failed_node_ids: [diagnostic.failed_node_id],
      reproducibility_match: true,
    },
    failure_diagnostic: diagnostic,
    repair_request: {
      id: `repair_request_${workspace.id}`,
      source_result_id: `result_failed_${workspace.id}`,
      source_package_id: workspace.packagePreview.packageID,
      cloud_job_id: workspace.cloudRun.cloudJobID ?? `job_${workspace.id}`,
      failed_bundle_hash_sha256: workspace.executableScriptBundle?.reproducibility.bundle_hash_sha256 ?? workspace.packagePreview.packageDigest,
      failed_plan_hash_sha256: workspace.executableScriptBundle?.reproducibility.plan_hash_sha256 ?? workspace.packagePreview.graphDigest,
      max_repair_attempts: 2,
      repair_attempt: 1,
      approval_required: true,
      requested_at: new Date().toISOString(),
    },
    created_at: new Date().toISOString(),
  };
}

function mockFailureDiagnostic(workspace: ProjectWorkspaceView): ScriptFailureDiagnostic {
  const failedNode = workspace.planReview.graph.nodes[1] ?? workspace.planReview.graph.nodes[0];
  const failedNodeID = failedNode?.id ?? "node_unknown";
  return {
    id: `diag_${workspace.id}_${failedNodeID}`,
    schema_version: "demoops.script_failure_diagnostic.v1",
    source_package_id: workspace.packagePreview.packageID,
    cloud_job_id: workspace.cloudRun.cloudJobID ?? `job_${workspace.id}`,
    failed_node_id: failedNodeID,
    failed_step_order: 2,
    attempt: 1,
    error: {
      code: "selector_timeout",
      message: `等待节点 ${failedNodeID} 的 selector 超时`,
      retryable: true,
    },
    current_url: workspace.productURL,
    ...(workspace.planReview.graph.name ? { page_title: workspace.planReview.graph.name } : {}),
    screenshot_refs: [
      {
        id: `failure_screenshot_${failedNodeID}`,
        role: "failure_screenshot",
        kind: "webpage_screenshot",
        uri: `artifact://failure/${workspace.id}/${failedNodeID}.png.enc`,
        mime_type: "image/png",
        sha256: mockHash(`failure_screenshot_${workspace.id}_${failedNodeID}`),
        encrypted: true,
        sensitive: true,
      },
    ],
    trace_refs: [
      {
        id: `failure_trace_${workspace.id}`,
        role: "failure_trace",
        kind: "browser_trace",
        uri: `artifact://failure/${workspace.id}/trace.zip.enc`,
        mime_type: "application/zip",
        sha256: mockHash(`failure_trace_${workspace.id}`),
        encrypted: true,
        sensitive: true,
      },
    ],
    console_events: [{ level: "warning", message: "目标按钮在数据加载前不可见。" }],
    network_events: [{ url: `${workspace.productURL}/api/status`, method: "GET", status: 200, resource: "xhr", redacted: true }],
    dom_snapshot_ref: {
      id: `failure_dom_${workspace.id}`,
      role: "failure_dom_snapshot",
      kind: "dom_snapshot",
      uri: `artifact://failure/${workspace.id}/dom.json.enc`,
      mime_type: "application/json",
      sha256: mockHash(`failure_dom_${workspace.id}`),
      encrypted: true,
      sensitive: true,
    },
    accessibility_snapshot_ref: {
      id: `failure_a11y_${workspace.id}`,
      role: "failure_accessibility_snapshot",
      kind: "accessibility_snapshot",
      uri: `artifact://failure/${workspace.id}/a11y.json.enc`,
      mime_type: "application/json",
      sha256: mockHash(`failure_a11y_${workspace.id}`),
      encrypted: true,
      sensitive: true,
    },
    redaction_report: {
      applied: true,
      policy_ref: `${workspace.packagePreview.packageID}.redactions`,
      masked_selectors: workspace.planReview.redactionSelectors,
      stripped_headers: ["authorization", "cookie"],
      stripped_storage_keys: ["localStorage", "sessionStorage"],
      full_html_included: false,
    },
    repair_hints: [
      {
        kind: "selector_repair",
        node_id: failedNodeID,
        summary: "候选 selector 已从失败截图和可访问性摘要中提取。",
        suggested_action: "优先改用 role/text/test id 组合，并延长等待数据加载的时间。",
        selector_candidates: [{ kind: "role", value: "button[name='邀请']", confidence: 0.82, stability_score: 0.76, source: "failure_accessibility_snapshot" }],
        confidence: 0.8,
      },
    ],
    captured_at: new Date().toISOString(),
  };
}

function mockRepairApprovalMarkdown(workspace: ProjectWorkspaceView, diagnostic: ScriptFailureDiagnostic): string {
  return `${mockScriptMarkdown(workspace)}

## 本次修复说明

- 失败节点：${diagnostic.failed_node_id}
- 错误信息：${diagnostic.error.message}
- 修复依据：云端返回的脱敏截图、trace、DOM 摘要和可访问性摘要。
- 修改范围：仅调整 selector/等待条件，不新增域名、不改变凭据范围、不访问禁用页面。
- 审批要求：修复后的 Stage JSON 和 Browser Agent 大纲必须重新人工审批后才能上传。`;
}

function mockExecutableScriptSource(plan: ExecutionScriptDocument, planHash: string): string {
  const lines = [
    "type CascadeRecordingContext = { page: any; secrets: any; capture: any; assert: any; log: any };",
    "type CascadeRecordingResult = { ok: boolean; planHash: string; stepResults: Array<{ nodeId: string; status: string }> };",
    `const cascadePlanHash = ${JSON.stringify(planHash)};`,
    "export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> {",
    "  const stepResults: Array<{ nodeId: string; status: string }> = [];",
    "  await ctx.capture.start({ planHash: cascadePlanHash });",
  ];
  for (const step of plan.steps) {
    const selector = step.action.target.selector ?? step.page_target.selector ?? "";
    const url = step.action.target.url ?? step.page_target.url ?? "";
    lines.push(`  await ctx.log.step(${JSON.stringify(step.node_id)}, ${JSON.stringify(step.title ?? step.node_id)});`);
    if (step.action.type === "navigate" && url) {
      lines.push(`  await ctx.page.goto(${JSON.stringify(url)}, { waitUntil: "networkidle", timeout: ${step.action.timeout_ms ?? 10000} });`);
    } else if (step.action.type === "click" && selector) {
      lines.push(`  await ctx.page.click(${JSON.stringify(selector)}, { timeout: ${step.action.timeout_ms ?? 10000} });`);
    } else if (step.action.type === "fill" && selector) {
      const value = step.action.secret_ref ? `await ctx.secrets.get(${JSON.stringify(step.action.secret_ref)})` : JSON.stringify(step.action.value ?? "");
      lines.push(`  await ctx.page.fill(${JSON.stringify(selector)}, ${value}, { timeout: ${step.action.timeout_ms ?? 10000} });`);
    } else {
      lines.push(`  await ctx.log.info("inspect step", { nodeId: ${JSON.stringify(step.node_id)} });`);
    }
    lines.push(`  await ctx.assert.step(${JSON.stringify(step.node_id)}, ${JSON.stringify(step.expected_outcome)}, { selector: ${JSON.stringify(selector)}, url: ${JSON.stringify(url)} });`);
    if (step.capture.screenshot) {
      lines.push(`  await ctx.capture.screenshot({ nodeId: ${JSON.stringify(step.node_id)}, selector: ${JSON.stringify(selector)}, maskSelectors: ${JSON.stringify(step.capture.mask_selectors ?? [])} });`);
    }
    lines.push(`  stepResults.push({ nodeId: ${JSON.stringify(step.node_id)}, status: "passed" });`);
  }
  lines.push("  await ctx.capture.stop();");
  lines.push("  return { ok: true, planHash: cascadePlanHash, stepResults };");
  lines.push("}");
  return lines.join("\n");
}

function routeFromStep(workspace: ProjectWorkspaceView, step: ExecutionScriptDocument["steps"][number]): string {
  const url = step.page_target.url ?? step.action.target.url ?? workspace.productURL;
  try {
    return new URL(url).pathname || "/";
  } catch {
    return url.startsWith("/") ? url : "/";
  }
}

function originFromURL(url: string): string {
  try {
    return new URL(url).origin;
  } catch {
    return "";
  }
}

function roleFromAction(action: string): string {
  if (action === "fill") {
    return "textbox";
  }
  if (action === "select") {
    return "combobox";
  }
  if (action === "navigate" || action === "inspect" || action === "assert" || action === "wait") {
    return "region";
  }
  return "button";
}

function mockBrowserAgentBusinessAcceptance(): BrowserAgentBusinessAcceptanceView {
  const stageDefinitions: Array<[string, string, string]> = [
    ["node_open_workspace", "打开项目工作台", "Create project page is visible"],
    ["node_fill_project_name", "输入项目名称", "Project name equals Tetris Launch"],
    ["node_select_build_mode", "选择构建模式", "Build mode selected"],
    ["node_submit_build", "提交构建", "Build result route is visible"],
    ["node_verify_build_result", "验证构建结果", "Build in progress is visible"],
  ];
  const stages: BrowserAgentAcceptanceScenarioView[] = stageDefinitions.map(([id, title, expected]) => ({ id, description: `${title}：${expected}`, expected, actual: "pass", verdict: "passed", action_executed: true, evidence: [], assertions: [{ kind: "required_outcome_validation", passed: true, actual: "pass" }] }));
  return {
    ready: true,
    can_run: true,
    message: "演示数据：受控业务验收已通过。真实模式会在 Server 临时业务页面上输入项目名、选择模式、提交并验证结果页。",
    report_path: "artifacts/browser-agent-business-acceptance/latest/acceptance-report.json",
    report: {
      schema_version: "cascade.browser_agent_business_acceptance.v1",
      generated_at: new Date().toISOString(),
      runtime: "browser-agent-outline-v1",
      strict_gate: "passed",
      package_id: "pkg_controlled_business_outline",
      business_flow: "进入工作台 → 输入项目名 → 选择构建模式 → 提交构建 → 验证构建结果",
      stages,
      editor_materialization: { ready: true, created: true, session_id: "editor_controlled_business", message: "待编辑素材已登记，可直接进入视频编辑器。" },
    },
  };
}

function stageKindFromAction(action: string): string {
  if (action === "fill" || action === "upload") return "business_input";
  if (action === "select") return "mode_selection";
  if (action === "wait") return "observe_progress";
  if (action === "assert" || action === "inspect") return "final_observe";
  return "business_action";
}

function routeStateFromStep(step: ScriptStep, route: string): string {
  const value = `${route} ${step.title ?? ""} ${step.expected_outcome ?? ""}`.toLowerCase();
  if (value.includes("login") || value.includes("sign-in") || value.includes("登录")) return "unauthenticated";
  if (value.includes("create") || value.includes("new") || value.includes("创建")) return "creation_flow";
  if (value.includes("build") || value.includes("generating") || value.includes("生成中")) return "build_running";
  if (value.includes("project") || value.includes("detail") || value.includes("项目")) return "project_detail";
  return "workspace";
}

function mockHash(value: string): string {
  let hash = 0x811c9dc5;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 0x01000193);
  }
  return `sha256:${(hash >>> 0).toString(16).padStart(8, "0")}`;
}
