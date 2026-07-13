import type { CodeUnderstandingSnapshot } from "../../src/types/workflowGraph";
import type { ProjectWorkspaceView } from "./domain";

export type AgentPipelineStatus = "pending" | "completed" | "attention";

export type AgentPipelineItem = {
  id: string;
  name: string;
  role: string;
  output: string;
  status: AgentPipelineStatus;
  detail: string;
};

export type CodeSummaryView = {
  fileCount: number;
  frameworks: string[];
  routes: number;
  components: number;
  selectors: number;
  sourceDigest: string;
  degraded: boolean;
};

export function updateWorkspaceInputs(
  workspace: ProjectWorkspaceView,
  patch: {
    productURL?: string;
    localRepoPath?: string;
    rawUserPrompt?: string;
    targetAudience?: string;
    forbiddenPagesText?: string;
    forbiddenDataText?: string;
  },
): ProjectWorkspaceView {
  const productURL = patch.productURL ?? workspace.productURL;
  const targetAudience = patch.targetAudience ?? workspace.targetAudience;
  const localRepoPath = patch.localRepoPath ?? workspace.inputBundle.repositories?.[0]?.local_path ?? "";
  const rawUserPrompt = patch.rawUserPrompt ?? workspace.inputBundle.raw_user_prompt ?? "";
  const forbiddenPages = patch.forbiddenPagesText !== undefined ? splitLines(patch.forbiddenPagesText) : workspace.planReview.forbiddenPages;
  const existingForbiddenData = Array.isArray(workspace.inputBundle.metadata?.forbidden_data) ? workspace.inputBundle.metadata.forbidden_data.filter(isString) : undefined;
  const forbiddenData = patch.forbiddenDataText !== undefined ? splitLines(patch.forbiddenDataText) : existingForbiddenData ?? workspace.scriptDocument?.safety_policy.forbidden_data ?? ["客户邮箱", "API Key", "访问令牌"];
  const repositories = localRepoPath
    ? [{ ...(workspace.inputBundle.repositories?.[0] ?? { provider: "local", read_only: true, primary: true }), local_path: localRepoPath, provider: "local", read_only: true, primary: true }]
    : [];

  const next: ProjectWorkspaceView = {
    ...workspace,
    productURL,
    targetAudience,
    inputBundle: {
      ...workspace.inputBundle,
      product_urls: productURL ? [{ ...(workspace.inputBundle.product_urls?.[0] ?? {}), url: productURL, kind: "staging", environment: "desktop" }] : [],
      repositories,
      raw_user_prompt: rawUserPrompt,
      metadata: { ...(workspace.inputBundle.metadata ?? {}), forbidden_data: forbiddenData },
    },
    sourceConnections: workspace.sourceConnections.map((source) => {
      if (source.kind === "product_url") {
        return { ...source, status: productURL ? "ready" : "needs_attention", detail: productURL ? "产品地址已配置，将用于页面理解和 allowed domain 推导。" : "待填写产品 URL。" };
      }
      if (source.kind === "local_repo") {
        return { ...source, status: localRepoPath ? "ready" : "needs_attention", detail: localRepoPath ? `本地项目目录：${localRepoPath}` : "未提供本地项目目录，可使用需求和页面材料降级生成。" };
      }
      return source;
    }),
    planReview: {
      ...workspace.planReview,
      allowedDomains: domainsFromURL(productURL, workspace.planReview.allowedDomains),
      forbiddenPages,
    },
  };
  if (workspace.scriptDocument) {
    next.scriptDocument = {
      ...workspace.scriptDocument,
      safety_policy: { ...workspace.scriptDocument.safety_policy, forbidden_pages: forbiddenPages, forbidden_data: forbiddenData },
    };
  }
  return next;
}

export function codeSummaryFromWorkspace(workspace: ProjectWorkspaceView): CodeSummaryView {
  const snapshots = workspace.understandingReport?.code_snapshots ?? [];
  const architecture = workspace.projectIntelligence?.architecture;
  const fileCount = snapshots.reduce((total, snapshot) => total + (snapshot.file_count ?? 0), 0);
  return {
    fileCount,
    frameworks: unique([...(architecture?.frameworks ?? []), ...snapshots.flatMap((snapshot) => snapshot.frameworks ?? [])]),
    routes: architecture?.route_tree?.length ?? snapshots.reduce((total, snapshot) => total + (snapshot.routes?.length ?? 0), 0),
    components: architecture?.modules?.reduce((total, module) => total + (module.component_refs?.length ?? 0), 0) ?? snapshots.reduce((total, snapshot) => total + (snapshot.components?.length ?? 0), 0),
    selectors: snapshots.reduce((total, snapshot) => total + (snapshot.selectors?.length ?? 0), 0),
    sourceDigest: workspace.projectIntelligence?.source_digest_sha256 ?? firstSourceDigest(snapshots, workspace.understandingReport?.source_digest_sha256),
    degraded: hasRepoInput(workspace) && snapshots.length > 0 && fileCount === 0,
  };
}

export function agentPipelineItems(workspace: ProjectWorkspaceView): AgentPipelineItem[] {
  const codeSummary = codeSummaryFromWorkspace(workspace);
  return [
    {
      id: "input_context",
      name: "InputContextAgent",
      role: "整理产品 URL、项目目录、需求和权限策略",
      output: "ProjectContext",
      status: workspace.productURL || hasRepoInput(workspace) || workspace.inputBundle.raw_user_prompt ? "completed" : "pending",
      detail: workspace.productURL ? "输入上下文已就绪" : "等待产品 URL 或需求材料",
    },
    {
      id: "requirement_reader",
      name: "RequirementReaderAgent",
      role: "读取需求和演示目标",
      output: "RequirementBrief",
      status: workspace.understandingReport?.requirement_brief || workspace.inputBundle.raw_user_prompt ? "completed" : "pending",
      detail: workspace.understandingReport?.requirement_brief?.objective ?? workspace.inputBundle.raw_user_prompt ?? "待读取需求",
    },
    {
      id: "code_reader",
      name: "CodeReaderAgent",
      role: "只读扫描项目根目录，生成代码结构摘要",
      output: "CodeUnderstandingSnapshot",
      status: codeReaderStatus(workspace, codeSummary),
      detail: codeSummary.degraded
        ? "路径不可读或无可扫描文件，已降级使用其他材料"
        : codeSummary.fileCount > 0
          ? `${codeSummary.fileCount} 个文件 / ${codeSummary.frameworks.join("、") || "框架待识别"}`
          : "未提供项目根目录",
    },
    {
      id: "page_reader",
      name: "PageReaderAgent",
      role: "读取页面 URL、截图和视觉/OCR线索",
      output: "PageUnderstandingSnapshot",
      status: workspace.understandingReport?.page_snapshots?.length ? "completed" : workspace.productURL ? "completed" : "pending",
      detail: `${workspace.understandingReport?.page_snapshots?.length ?? 0} 个页面快照`,
    },
    {
      id: "project_intelligence",
      name: "ProjectIntelligenceGraph",
      role: "用工具图谱协作生成架构、功能、交互面、API、数据模型和可演示路径",
      output: "ProjectIntelligencePack",
      status: workspace.projectIntelligence ? readinessStatus(workspace) : workspace.understandingReport ? "pending" : "pending",
      detail: workspace.projectIntelligence
        ? `${workspace.projectIntelligence.feature_capabilities?.length ?? 0} 个能力 / ${workspace.projectIntelligence.interaction_surfaces?.length ?? 0} 个交互面 / ${workspace.agentGraphTrace?.steps?.length ?? 0} 个图节点`
        : "等待代码和页面材料读取完成",
    },
    {
      id: "understanding",
      name: "MultimodalUnderstandingAgent",
      role: "融合需求、代码、页面证据",
      output: "MultimodalUnderstandingReport",
      status: workspace.understandingReport ? "completed" : "pending",
      detail: workspace.understandingReport?.summary ?? workspace.projectIntelligence?.architecture?.summary ?? "等待执行包生成",
    },
    {
      id: "product_map",
      name: "ProductMapAgent",
      role: "生成产品地图和功能候选",
      output: "ProductMap",
      status: workspace.understanding.productMapID && workspace.understanding.productMapID !== "map_1" ? "completed" : workspace.understandingReport ? "completed" : "pending",
      detail: workspace.understanding.productMapID || "待生成",
    },
    {
      id: "graph_builder",
      name: "GraphBuilderAgent",
      role: "生成可执行 Demo Workflow Graph",
      output: "DemoWorkflowGraph",
      status: workspace.planReview.graph.status === "review_ready" ? "completed" : "pending",
      detail: `${workspace.planReview.graph.nodes.length} 个执行节点`,
    },
    {
      id: "script_packager",
      name: "ScriptPackagerAgent",
      role: "生成 JSON 执行计划、TS 脚本和中文审批文档",
      output: "ExecutableRecordingScriptBundle",
      status: workspace.executableScriptBundle ? "completed" : "pending",
      detail: workspace.executableScriptBundle?.reproducibility.bundle_hash_sha256 ?? "待生成执行包",
    },
  ];
}

function readinessStatus(workspace: ProjectWorkspaceView): AgentPipelineStatus {
  if (workspace.scriptReadiness?.blockers?.length) {
    return "attention";
  }
  if (workspace.scriptReadiness?.warnings?.length) {
    return "attention";
  }
  return "completed";
}

function codeReaderStatus(workspace: ProjectWorkspaceView, summary: CodeSummaryView): AgentPipelineStatus {
  if (!hasRepoInput(workspace)) {
    return "attention";
  }
  if (summary.degraded) {
    return "attention";
  }
  return summary.fileCount > 0 || workspace.understandingReport?.code_snapshots?.length ? "completed" : "pending";
}

function hasRepoInput(workspace: ProjectWorkspaceView): boolean {
  return Boolean(workspace.inputBundle.repositories?.some((repo) => repo.local_path || repo.url));
}

function firstSourceDigest(snapshots: CodeUnderstandingSnapshot[], fallback?: string): string {
  return snapshots.find((snapshot) => snapshot.source_digest_sha256)?.source_digest_sha256 ?? fallback ?? "";
}

function splitLines(value: string): string[] {
  return value
    .split(/\r?\n|,/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function domainsFromURL(productURL: string, fallback: string[]): string[] {
  try {
    const host = new URL(productURL).host;
    return unique(host ? [host, ...fallback] : fallback);
  } catch {
    return fallback;
  }
}

function unique(values: string[]): string[] {
  return [...new Set(values.filter(Boolean))];
}

function isString(value: unknown): value is string {
  return typeof value === "string";
}
