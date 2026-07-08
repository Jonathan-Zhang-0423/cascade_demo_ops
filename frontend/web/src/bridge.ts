import type { ProjectWorkspaceView, RuntimeHealthView, ScenarioID } from "./domain";
import type { ExecutionScriptDocument, MultimodalUnderstandingReport } from "../../src/types/workflowGraph";
import { createWorkspace } from "./mockWorkspace";
import { mapCloudStatus } from "./workflow";

export type BridgeResult<T> = {
  ok: boolean;
  data?: T;
  error?: string;
};

export type DesktopBridgeClient = {
  runtimeHealth(): Promise<BridgeResult<RuntimeHealthView>>;
  createProject(scenarioID: ScenarioID): Promise<BridgeResult<ProjectWorkspaceView>>;
  listProjects(): Promise<BridgeResult<ProjectWorkspaceView[]>>;
  loadProject(projectID: string): Promise<BridgeResult<ProjectWorkspaceView>>;
  saveWorkspace(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  getUnderstandingReport(projectID: string): Promise<BridgeResult<MultimodalUnderstandingReport>>;
  getExecutionScriptDocument(projectID: string): Promise<BridgeResult<ExecutionScriptDocument>>;
  getExecutionScriptMarkdown(projectID: string): Promise<BridgeResult<{ markdown: string }>>;
  buildExecutionPackagePreview(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  approveAndUploadPackage(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  pollCloudRun(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
  acknowledgeResult(workspace: ProjectWorkspaceView): Promise<BridgeResult<ProjectWorkspaceView>>;
};

export function createMockBridgeClient(): DesktopBridgeClient {
  const projects = new Map<string, ProjectWorkspaceView>();
  const initial = createWorkspace("product_demo");
  projects.set(initial.id, initial);

  return {
    async runtimeHealth() {
      return ok({
        profile: "desktop",
        databaseConfigured: true,
        localDataConfigured: true,
        resourceManifestLoaded: true,
        sidecars: { "video-worker": true },
      });
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
    async buildExecutionPackagePreview(workspace) {
      return ok({
        ...workspace,
        stage: "package_approval",
        status: "awaiting_approval",
        packagePreview: {
          ...workspace.packagePreview,
          graphDigest: `sha256:${workspace.planReview.graph.nodes.length}nodes-${workspace.planReview.targetDurationSec}s`,
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
  return `# ${workspace.planReview.graph.name}\n\n## 执行步骤\n\n${workspace.planReview.graph.nodes
    .map((node, index) => `${index + 1}. ${node.title ?? node.action}: ${node.expected_outcome}`)
    .join("\n")}\n\n## 审批清单\n\n- [ ] 上传前必须完成人工审批。`;
}
