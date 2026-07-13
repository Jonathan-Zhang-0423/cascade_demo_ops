import type { DemoWorkflowGraph, GraphNode, SandboxPolicy } from "../../src/types/workflowGraph";
import type {
  ApprovalChecklistState,
  CloudRunStatus,
  ExecutionPackagePreview,
  ProjectWorkspaceView,
  ServerLifecycleStageID,
  ServerLifecycleStageStatus,
  ServerLifecycleStageView,
  SourceConnectionView,
  WorkspaceStage,
} from "./domain";

export const workflowStageLabels: Record<WorkspaceStage, string> = {
  setup: "基础设置",
  inputs: "输入材料",
  understanding: "产品理解",
  plan_review: "方案审批",
  package_approval: "执行包审批",
  cloud_run: "云端录制",
  script_repair: "脚本修复",
  result_review: "成品验收",
};

export const projectStatusLabels: Record<ProjectWorkspaceView["status"], string> = {
  draft: "草稿",
  understanding_ready: "理解完成",
  awaiting_approval: "等待审批",
  cloud_running: "云端录制中",
  script_repair_required: "脚本待修复",
  asset_ready: "成品就绪",
};

export const serverLifecycleStageLabels: Record<ServerLifecycleStageID, string> = {
  local_generated: "本地生成",
  human_approved: "人工审批",
  upload_initialized: "初始化上传",
  package_uploaded: "上传执行包",
  server_intake: "服务器接收",
  script_validation: "脚本校验",
  sandbox_preparing: "沙箱准备",
  browser_execution: "浏览器执行",
  video_rendering: "视频渲染",
  result_returned: "结果返回",
};

export const serverLifecycleStageOrder = Object.keys(serverLifecycleStageLabels) as ServerLifecycleStageID[];

export function updateGraphNode(graph: DemoWorkflowGraph, nodeID: string, patch: Partial<GraphNode>): DemoWorkflowGraph {
  return {
    ...graph,
    nodes: graph.nodes.map((node) => (node.id === nodeID ? { ...node, ...patch } : node)),
  };
}

export function mapCloudStatus(status: string): CloudRunStatus {
  if (status === "queued" || status === "accepted" || status === "uploaded") {
    return "queued";
  }
  if (status === "running") {
    return "running";
  }
  if (status === "completed" || status === "succeeded" || status === "generated" || status === "delivered") {
    return "succeeded";
  }
  if (status === "failed" || status === "canceled" || status === "expired") {
    return "failed";
  }
  return "not_uploaded";
}

export function lifecycleStatusTone(status: ServerLifecycleStageStatus): "blue" | "green" | "yellow" | "neutral" {
  if (status === "completed") {
    return "green";
  }
  if (status === "active") {
    return "blue";
  }
  if (status === "failed" || status === "blocked") {
    return "yellow";
  }
  return "neutral";
}

export function lifecycleStatusLabel(status: ServerLifecycleStageStatus): string {
  if (status === "completed") {
    return "完成";
  }
  if (status === "active") {
    return "进行中";
  }
  if (status === "failed") {
    return "失败";
  }
  if (status === "blocked") {
    return "阻塞";
  }
  return "等待";
}

export function lifecycleStagesFromWorkspace(workspace: ProjectWorkspaceView): ServerLifecycleStageView[] {
  if (workspace.cloudRun.stageHistory?.length) {
    return workspace.cloudRun.stageHistory;
  }
  const completedUntil = workspace.stage === "result_review"
    ? "result_returned"
    : workspace.stage === "cloud_run"
      ? "browser_execution"
      : workspace.stage === "script_repair"
        ? "browser_execution"
        : workspace.stage === "package_approval" && workspace.executableScriptBundle
          ? "local_generated"
          : undefined;
  return serverLifecycleStageOrder.map((id) => {
    const order = serverLifecycleStageOrder.indexOf(id);
    const completedOrder = completedUntil ? serverLifecycleStageOrder.indexOf(completedUntil) : -1;
    const status: ServerLifecycleStageStatus = order <= completedOrder ? "completed" : order === completedOrder + 1 && workspace.cloudRun.status === "running" ? "active" : "pending";
    return {
      id,
      label: serverLifecycleStageLabels[id],
      status,
      progress: status === "completed" ? 100 : status === "active" ? workspace.cloudRun.progress : 0,
      summary: lifecycleFallbackSummary(id, workspace),
      artifactCount: id === "result_returned" ? workspace.cloudRun.artifactSummary?.total ?? 0 : 0,
    };
  });
}

export function sandboxProfileLabel(profile?: string): string {
  if (profile === "mvp_cloud") {
    return "MVP 云端生产推荐";
  }
  if (profile === "enterprise") {
    return "企业隔离模式";
  }
  if (profile === "dev") {
    return "本地联调模式";
  }
  return profile || "未声明";
}

export function sandboxRiskLevel(policy?: SandboxPolicy): "ok" | "warning" {
  if (!policy) {
    return "warning";
  }
  if (policy.profile === "dev" || policy.isolation_mode === "local_sidecar") {
    return "warning";
  }
  if (!policy.network_policy.proxy_required || policy.network_policy.mode !== "allowed_domains_only") {
    return "warning";
  }
  if (!policy.artifact_policy.encrypt_sensitive_artifacts || !policy.artifact_policy.sensitive_by_default) {
    return "warning";
  }
  if (!policy.diagnostic_policy.redaction_required || !policy.diagnostic_policy.encrypt_diagnostics) {
    return "warning";
  }
  return "ok";
}

export function sandboxRiskMessage(policy?: SandboxPolicy): string {
  if (!policy) {
    return "缺少 sandbox_policy，服务器应在执行前阻断或补齐默认策略。";
  }
  if (policy.profile === "dev" || policy.isolation_mode === "local_sidecar") {
    return "当前是本地联调沙箱，不适合真实客户数据。";
  }
  if (sandboxRiskLevel(policy) === "warning") {
    return "沙箱策略存在缺项，请复核网络出口、artifact 加密和诊断脱敏。";
  }
  return "策略满足 mvp cloud 执行边界。";
}

export function packageApprovalBlockedReasons(
  preview: ExecutionPackagePreview,
  checklist: ApprovalChecklistState,
  sources: SourceConnectionView[],
): string[] {
  const blocked = new Set<string>(preview.blockedReasons);
  if (!checklist.userApprovedPlan) {
    blocked.add("上传前必须完成人工审批。");
  }
  if (!checklist.ipAllowlistAcknowledged && !preview.ipAllowlistAcknowledged) {
    blocked.add("需要确认 Cascade 云端执行 IP 已加入白名单。");
  }
  if (!checklist.sourceSummaryOnlyAcknowledged || !preview.sourceSummaryOnly) {
    blocked.add("需要确认仅上传代码结构摘要，不上传完整源码。");
  }
  if (preview.credentialGrants.length > 0 && !checklist.credentialGrantAcknowledged) {
    blocked.add("需要复核凭据授权范围和过期时间。");
  }
  if (!checklist.redactionsReviewed) {
    blocked.add("需要复核打码选择器和禁止访问数据。");
  }
  for (const source of sources) {
    if (source.status === "blocked") {
      blocked.add(`${source.label} 处于阻塞状态。`);
    }
  }
  return [...blocked];
}

export function canUploadExecutionPackage(
  preview: ExecutionPackagePreview,
  checklist: ApprovalChecklistState,
  sources: SourceConnectionView[],
): boolean {
  return packageApprovalBlockedReasons(preview, checklist, sources).length === 0;
}

export function resetApprovalChecklistForRepair(current: ApprovalChecklistState): ApprovalChecklistState {
  return {
    userApprovedPlan: false,
    ipAllowlistAcknowledged: false,
    sourceSummaryOnlyAcknowledged: current.sourceSummaryOnlyAcknowledged,
    credentialGrantAcknowledged: false,
    redactionsReviewed: false,
  };
}

function lifecycleFallbackSummary(id: ServerLifecycleStageID, workspace: ProjectWorkspaceView): string {
  if (id === "local_generated") {
    return workspace.executableScriptBundle ? "三合一方案包已生成" : "等待本地生成执行包";
  }
  if (id === "human_approved") {
    return workspace.status === "awaiting_approval" ? "等待人工审批" : "审批记录将随包上传";
  }
  if (id === "upload_initialized") {
    return workspace.cloudRun.uploadID ? `upload id: ${workspace.cloudRun.uploadID}` : "等待 /execution-packages/init";
  }
  if (id === "package_uploaded") {
    return workspace.cloudRun.exchangePackageID ? `exchange id: ${workspace.cloudRun.exchangePackageID}` : "等待上传 ExchangeEnvelope";
  }
  if (id === "server_intake") {
    return workspace.cloudRun.exchangePackageID ? "服务器已接收 metadata 和 artifact descriptor" : "等待服务器接收";
  }
  if (id === "script_validation") {
    return "校验 TS AST、hash、allowed domains 和安全策略";
  }
  if (id === "sandbox_preparing") {
    return "准备一次性容器、vault 凭据和 artifact workspace";
  }
  if (id === "browser_execution") {
    return workspace.cloudRun.currentStep;
  }
  if (id === "video_rendering") {
    return "执行成功后进入无凭据渲染沙箱";
  }
  return workspace.cloudRun.resultPackageID ? `result id: ${workspace.cloudRun.resultPackageID}` : "等待返回结果包";
}
