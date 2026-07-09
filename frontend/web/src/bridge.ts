import type { ModelDiagnosticResult, ProjectWorkspaceView, RuntimeHealthView, RuntimeLogEntry, ScenarioID } from "./domain";
import type {
  AssetKind,
  DemoWorkflowGraph,
  ExecutableRecordingScriptBundle,
  ExecutionScriptDocument,
  MultimodalUnderstandingReport,
  ProjectInputBundle,
  RecordingResultPackage,
  ScriptFailureDiagnostic,
} from "../../src/types/workflowGraph";
import { createWorkspace } from "./mockWorkspace";
import { getScenarioTemplate } from "./scenarios";
import { mapCloudStatus } from "./workflow";

export type BridgeResult<T> = {
  ok: boolean;
  data?: T;
  error?: string;
};

export type DesktopBridgeClient = {
  mode: "mock" | "local";
  runtimeHealth(): Promise<BridgeResult<RuntimeHealthView>>;
  modelDiagnostics(): Promise<BridgeResult<ModelDiagnosticResult[]>>;
  executionEvents(projectID: string, afterID?: string): Promise<BridgeResult<RuntimeLogEntry[]>>;
  createProject(scenarioID: ScenarioID): Promise<BridgeResult<ProjectWorkspaceView>>;
  listProjects(): Promise<BridgeResult<ProjectWorkspaceView[]>>;
  loadProject(projectID: string): Promise<BridgeResult<ProjectWorkspaceView>>;
  saveWorkspace(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  getUnderstandingReport(projectID: string): Promise<BridgeResult<MultimodalUnderstandingReport>>;
  getExecutionScriptDocument(projectID: string): Promise<BridgeResult<ExecutionScriptDocument>>;
  getExecutionScriptMarkdown(projectID: string): Promise<BridgeResult<{ markdown: string }>>;
  getExecutableScriptBundle(projectID: string): Promise<BridgeResult<ExecutableRecordingScriptBundle>>;
  buildExecutionPackagePreview(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  approveAndUploadPackage(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  pollCloudRun(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  simulateCloudFailure(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  repairFailedScript(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  acknowledgeResult(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
};

type LocalBridgeResponse<T> = {
  ok: boolean;
  data?: T;
  error?: string;
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
  product_map?: LocalProductMap;
  workflow_graph?: DemoWorkflowGraph;
  script_document?: ExecutionScriptDocument;
  script_markdown?: string;
  executable_script_bundle?: ExecutableRecordingScriptBundle;
  error_message?: string;
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
};

type LocalProductMap = {
  id?: string;
  summary?: string;
  pages?: Array<unknown>;
  features?: Array<unknown>;
  components?: Array<unknown>;
  data_models?: Array<unknown>;
};

type LocalUserInput = {
  mode: "desktop";
  product_url: string;
  local_repo_path?: string;
  product_description: string;
  requirement_documents?: ProjectInputBundle["requirement_documents"];
  webpage_screenshots?: ProjectInputBundle["webpage_screenshots"];
  target_audience: string;
  brand_tone?: string;
  must_show?: string[];
  must_not_show?: string[];
  forbidden_pages?: string[];
  forbidden_data?: string[];
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
  return {
    mode: "local",
    async runtimeHealth() {
      const result = await requestLocal<LocalRuntimeHealth>(baseURL, "/v1/desktop/runtime-health");
      if (!result.ok || !result.data) {
        return { ok: false, error: result.error ?? "本地运行时状态不可用" };
      }
      return ok(runtimeHealthFromLocal(result.data));
    },
    async modelDiagnostics() {
      const result = await requestLocal<LocalModelDiagnostic[]>(baseURL, "/v1/desktop/model-diagnostics", { method: "POST" });
      if (!result.ok || !result.data) {
        return { ok: false, error: result.error ?? "模型诊断不可用" };
      }
      return ok(result.data.map(modelDiagnosticFromLocal));
    },
    async executionEvents(projectID, afterID) {
      const query = afterID ? `?after=${encodeURIComponent(afterID)}` : "";
      const result = await requestLocal<LocalExecutionEvent[]>(baseURL, `/v1/desktop/projects/${encodeURIComponent(projectID)}/execution-events${query}`);
      if (!result.ok || !result.data) {
        return { ok: false, error: result.error ?? "运行日志不可用" };
      }
      return ok(result.data.map(runtimeLogFromLocalEvent));
    },
    async createProject(scenarioID) {
      const workspace = createWorkspace(scenarioID);
      projects.set(workspace.id, workspace);
      return ok(workspace);
    },
    async listProjects() {
      return ok([...projects.values()]);
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
    async buildExecutionPackagePreview(workspace) {
      const userInput = userInputFromWorkspace(workspace);
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
    async approveAndUploadPackage(workspace) {
      return ok(localFutureWorkspace(workspace, "云端上传将在下一阶段接入，本地模式仅生成执行包。"));
    },
    async pollCloudRun(workspace) {
      return ok(localFutureWorkspace(workspace, "云端录制将在下一阶段接入。"));
    },
    async simulateCloudFailure(workspace) {
      return ok(localFutureWorkspace(workspace, "失败诊断将在云端录制接入后启用。"));
    },
    async repairFailedScript(workspace) {
      return ok(localFutureWorkspace(workspace, "脚本修复将在失败诊断接入后启用。"));
    },
    async acknowledgeResult(workspace) {
      return ok(workspace);
    },
  };
}

export function createMockBridgeClient(): DesktopBridgeClient {
  const projects = new Map<string, ProjectWorkspaceView>();
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
      });
    },
    async modelDiagnostics() {
      return ok([
        mockDiagnostic("kimi", "planning", "kimi-k2.7-code"),
        mockDiagnostic("glm", "code_reading", "glm-5.2"),
        mockDiagnostic("minimax", "multimodal_understanding", "minimax-m3"),
        mockDiagnostic("seedance", "video_operation", "seedance-2.0"),
      ]);
    },
    async executionEvents() {
      return ok([]);
    },
    async createProject(scenarioID) {
      const project = createWorkspace(scenarioID);
      projects.set(project.id, project);
      return ok(project);
    },
    async listProjects() {
      return ok([...projects.values()]);
    },
    async loadProject(projectID) {
      const project = projects.get(projectID);
      return project ? ok(project) : { ok: false, error: "未找到项目" };
    },
    async saveWorkspace(workspace) {
      projects.set(workspace.id, workspace);
      return ok(workspace);
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
      });
    },
    async approveAndUploadPackage(workspace) {
      return ok({
        ...workspace,
        stage: "cloud_run",
        status: "cloud_running",
        cloudRun: {
          ...workspace.cloudRun,
          status: mapCloudStatus("running"),
          currentStep: "云端执行器正在打开产品地址",
          progress: 38,
        },
      });
    },
    async pollCloudRun(workspace) {
      return ok({
        ...workspace,
        stage: "result_review",
        status: "asset_ready",
        cloudRun: {
          ...workspace.cloudRun,
          status: "succeeded",
          currentStep: "演示视频和步骤文档已生成",
          progress: 100,
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
          currentStep: "云端执行失败，已返回脱敏截图和错误诊断",
          progress: 62,
          resultPackage: failedResult,
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
      });
    },
    async acknowledgeResult(workspace) {
      return ok({
        ...workspace,
        assets: workspace.assets.map((asset) => ({ ...asset, status: "approved" })),
      });
    },
  };
}

function ok<T>(data: T): BridgeResult<T> {
  return { ok: true, data };
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
    const payload = (await response.json()) as LocalBridgeResponse<T>;
    if (!response.ok || !payload.ok) {
      console.error("[Cascade Dev Bridge] error", init?.method ?? "GET", path, response.status, payload.error);
      return { ok: false, error: payload.error ?? `本地 Dev Bridge 请求失败: ${response.status}` };
    }
    if (payload.data === undefined) {
      console.error("[Cascade Dev Bridge] missing data", init?.method ?? "GET", path);
      return { ok: false, error: "本地 Dev Bridge 响应缺少 data" };
    }
    console.info("[Cascade Dev Bridge] done", init?.method ?? "GET", path, `${Date.now() - startedAt}ms`);
    return { ok: true, data: payload.data };
  } catch (error) {
    console.error("[Cascade Dev Bridge] unavailable", init?.method ?? "GET", path, error);
    return { ok: false, error: error instanceof Error ? error.message : "本地 Dev Bridge 不可用" };
  }
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

export function userInputFromWorkspace(workspace: ProjectWorkspaceView): LocalUserInput {
  const template = getScenarioTemplate(workspace.scenarioID);
  const repository = workspace.inputBundle.repositories?.find((repo) => repo.local_path);
  const requirementDocuments = workspace.inputBundle.requirement_documents;
  const screenshots = workspace.inputBundle.webpage_screenshots;
  const forbiddenData = Array.isArray(workspace.inputBundle.metadata?.forbidden_data)
    ? workspace.inputBundle.metadata.forbidden_data.filter((item): item is string => typeof item === "string")
    : ["客户邮箱", "API Key", "访问令牌"];
  return {
    mode: "desktop",
    product_url: workspace.productURL,
    ...(repository?.local_path ? { local_repo_path: repository.local_path } : {}),
    product_description: workspace.inputBundle.raw_user_prompt || template.objective,
    ...(requirementDocuments?.length ? { requirement_documents: requirementDocuments } : {}),
    ...(screenshots?.length ? { webpage_screenshots: screenshots } : {}),
    target_audience: workspace.targetAudience || template.targetAudience,
    brand_tone: "专业、清晰、适合中国客户",
    must_show: template.defaultChecklist,
    must_not_show: ["原始密码", "API Key 明文", "客户隐私数据"],
    forbidden_pages: workspace.planReview.forbiddenPages,
    forbidden_data: forbiddenData,
  };
}

function workspaceFromCascadeState(state: LocalCascadeState, fallback: ProjectWorkspaceView): ProjectWorkspaceView {
  const project = state.project_context;
  const graph = state.workflow_graph ?? fallback.planReview.graph;
  const report = state.understanding_report;
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
    understanding: {
      productMapID: state.product_map?.id || fallback.understanding.productMapID,
      routesDetected: report?.code_snapshots?.reduce((count, snapshot) => count + (snapshot.routes?.length ?? 0), 0) ?? fallback.understanding.routesDetected,
      featuresDetected: report?.feature_hypotheses?.length ?? fallback.understanding.featuresDetected,
      componentsSummarized: report?.code_snapshots?.reduce((count, snapshot) => count + (snapshot.components?.length ?? 0), 0) ?? fallback.understanding.componentsSummarized,
      dataModelsSummarized: report?.code_snapshots?.reduce((count, snapshot) => count + (snapshot.data_models?.length ?? 0), 0) ?? fallback.understanding.dataModelsSummarized,
      evidenceRefs: report?.evidence_refs ?? fallback.understanding.evidenceRefs,
      sensitiveWarnings: report?.safety_report?.policy_findings?.map((finding) => finding.summary) ?? fallback.understanding.sensitiveWarnings,
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

function modelProvenanceFromState(report: MultimodalUnderstandingReport | undefined, scriptDocument: ExecutionScriptDocument | undefined): string[] {
  const refs = [...(report?.evidence_refs ?? []), ...(scriptDocument?.evidence_refs ?? [])];
  const values = refs
    .map((ref) => ref.summary)
    .filter((summary): summary is string => Boolean(summary?.includes("模型路由")));
  return [...new Set(values)];
}

function sourceConnectionsFromState(project: LocalProjectContext | undefined, report: MultimodalUnderstandingReport | undefined, fallback: ProjectWorkspaceView) {
  const sources = [...fallback.sourceConnections];
  const hasRepo = Boolean(project?.local_repo_path || project?.inputs?.repositories?.some((repo) => repo.local_path || repo.url));
  const hasRequirement = Boolean(project?.product_description || project?.inputs?.requirement_documents?.length || project?.inputs?.raw_user_prompt);
  const hasScreenshots = Boolean(project?.inputs?.webpage_screenshots?.length);
  return sources.map((source) => {
    if (source.kind === "product_url") {
      return { ...source, status: project?.product_url ? "ready" as const : source.status, detail: project?.product_url ? "已进入本地理解链路并生成执行包。" : source.detail };
    }
    if (source.kind === "local_repo") {
      return { ...source, status: hasRepo ? "ready" as const : "needs_attention" as const, detail: hasRepo ? "已生成代码结构摘要和 source digest，不上传完整源码。" : "未提供本地代码目录，使用需求和页面材料生成脚本。" };
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
    kind: "local_repo" as const,
    label: "本地理解摘要",
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
  return `# ${workspace.planReview.graph.name}\n\n## 演示目标\n\n${workspace.planReview.graph.summary}\n\n## 生成依据\n\n- 需求、代码结构摘要和页面证据已融合。\n- 本地代码只用于生成结构摘要，不上传完整源码。\n\n## 安全策略\n\n- 允许域名：${workspace.planReview.allowedDomains.join(", ")}\n- 禁止页面：${workspace.planReview.forbiddenPages.join(", ")}\n- 打码选择器：${workspace.planReview.redactionSelectors.join(", ")}\n\n## 录制策略\n\n- 目标时长：${workspace.planReview.targetDurationSec} 秒\n- 输出资产：演示视频 / 步骤文档\n\n## 凭据与云端边界\n\n- 凭据只通过 secret_ref 使用，不展示明文。\n- 云端执行前必须校验 TS 脚本、JSON plan、hash 和 allowed domains。\n\n## 执行步骤\n\n${workspace.planReview.graph.nodes
    .map((node, index) => `${index + 1}. ${node.title ?? node.action}: ${node.expected_outcome}`)
    .join("\n")}\n\n## 审批清单\n\n- [ ] 上传前必须完成人工审批。`;
}

function mockExecutableScriptBundle(
  workspace: ProjectWorkspaceView,
  repair?: { diagnostic: ScriptFailureDiagnostic; sourceResultID: string; sourceCloudJobID: string; repairAttempt: number },
): ExecutableRecordingScriptBundle {
  const plan = workspace.scriptDocument ?? mockScriptDocument(workspace);
  const markdown = workspace.scriptMarkdown ?? mockScriptMarkdown(workspace);
  const planHash = mockHash(JSON.stringify(plan));
  const source = mockExecutableScriptSource(plan, planHash);
  const scriptHash = mockHash(source);
  const markdownHash = mockHash(markdown);
  const bundleSeed = `${planHash}|${scriptHash}|${markdownHash}|${workspace.id}`;
  return {
    id: `bundle_${plan.id}`,
    project_id: workspace.id,
    workflow_graph_id: plan.workflow_graph_id,
    schema_version: "demoops.executable_recording_script_bundle.v1",
    status: "review_ready",
    script_manifest: {
      script_id: `recording_${plan.workflow_graph_id}`,
      version: 1,
      language: "typescript",
      runtime: "playwright-restricted-sandbox",
      entry_function: "runCascadeRecording",
      generator: "cascade_deterministic_script_code_generator",
      generator_version: "0.1.0",
      dependency_allowlist: [],
      context_apis: ["ctx.page", "ctx.secrets", "ctx.capture", "ctx.assert", "ctx.log"],
      step_node_ids: plan.steps.map((step) => step.node_id),
    },
    plan_json: plan,
    playwright_script: {
      inline_source: source,
      mime_type: "text/typescript",
      sha256: scriptHash,
      size_bytes: source.length,
      encrypted: false,
    },
    approval_markdown: {
      inline_markdown: markdown,
      mime_type: "text/markdown",
      sha256: markdownHash,
      size_bytes: markdown.length,
    },
    security_policy: {
      redactions: plan.safety_policy.redactions,
      secret_refs: plan.steps.map((step) => step.action.secret_ref).filter((ref): ref is string => Boolean(ref)),
      allowed_context_apis: ["ctx.page", "ctx.secrets", "ctx.capture", "ctx.assert", "ctx.log"],
      allowed_page_methods: ["goto", "click", "fill", "selectOption", "setInputFiles", "waitForTimeout", "waitForLoadState", "locator"],
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
      script_hash_sha256: scriptHash,
      markdown_hash_sha256: markdownHash,
      bundle_hash_sha256: mockHash(bundleSeed),
      graph_hash_sha256: workspace.packagePreview.graphDigest,
      source_snapshot_digest: workspace.packagePreview.packageDigest,
      generator_version: "0.1.0",
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
        duration_ms: 10000,
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
- 审批要求：修复后的执行计划和 TS 脚本必须重新人工审批后才能上传。`;
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

function mockHash(value: string): string {
  let hash = 0x811c9dc5;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 0x01000193);
  }
  return `sha256:${(hash >>> 0).toString(16).padStart(8, "0")}`;
}
