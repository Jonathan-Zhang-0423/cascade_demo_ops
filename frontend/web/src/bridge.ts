import type { ProjectWorkspaceView, RuntimeHealthView, ScenarioID } from "./domain";
import type { ExecutableRecordingScriptBundle, ExecutionScriptDocument, MultimodalUnderstandingReport } from "../../src/types/workflowGraph";
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
  getExecutableScriptBundle(projectID: string): Promise<BridgeResult<ExecutableRecordingScriptBundle>>;
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
  return `# ${workspace.planReview.graph.name}\n\n## 演示目标\n\n${workspace.planReview.graph.summary}\n\n## 生成依据\n\n- 需求、代码结构摘要和页面证据已融合。\n- 本地代码只用于生成结构摘要，不上传完整源码。\n\n## 安全策略\n\n- 允许域名：${workspace.planReview.allowedDomains.join(", ")}\n- 禁止页面：${workspace.planReview.forbiddenPages.join(", ")}\n- 打码选择器：${workspace.planReview.redactionSelectors.join(", ")}\n\n## 录制策略\n\n- 目标时长：${workspace.planReview.targetDurationSec} 秒\n- 输出资产：演示视频 / 步骤文档\n\n## 凭据与云端边界\n\n- 凭据只通过 secret_ref 使用，不展示明文。\n- 云端执行前必须校验 TS 脚本、JSON plan、hash 和 allowed domains。\n\n## 执行步骤\n\n${workspace.planReview.graph.nodes
    .map((node, index) => `${index + 1}. ${node.title ?? node.action}: ${node.expected_outcome}`)
    .join("\n")}\n\n## 审批清单\n\n- [ ] 上传前必须完成人工审批。`;
}

function mockExecutableScriptBundle(workspace: ProjectWorkspaceView): ExecutableRecordingScriptBundle {
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
  };
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
