import type { DemoWorkflowGraph, GraphNode, SandboxPolicy } from "../../src/types/workflowGraph";
import type {
  ApprovalChecklistState,
  AssistantSessionView,
  CloudRunStatus,
  ExecutionPackagePreview,
  ProjectWorkspaceView,
  ProjectWorkstationView,
  ServerLifecycleStageID,
  ServerLifecycleStageStatus,
  ServerLifecycleStageView,
  SourceConnectionView,
  WorkspaceStage,
} from "./domain";

export type ProjectJourneyStepID = "evidence" | "plan" | "approval" | "execution" | "assets";

export type ProjectJourneyStep = {
  id: ProjectJourneyStepID;
  label: string;
  status: "completed" | "current" | "upcoming" | "blocked";
  workstation: ProjectWorkstationView;
};

export type ProjectNextAction = {
  kind: "configure" | "analyze" | "review_plan" | "approve_upload" | "monitor" | "repair" | "review_result" | "complete";
  workstation: ProjectWorkstationView;
  title: string;
  description: string;
};

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

const journeyOrder: ProjectJourneyStepID[] = ["evidence", "plan", "approval", "execution", "assets"];
const journeyLabels: Record<ProjectJourneyStepID, string> = {
  evidence: "理解产品",
  plan: "确认方案",
  approval: "审批上传",
  execution: "生成成片",
  assets: "人工审核",
};

export function recommendedWorkstation(workspace: ProjectWorkspaceView): ProjectWorkstationView {
  if (workspace.status === "script_repair_required" || workspace.cloudRun.status === "failed") return "repair";
  if (workspace.cloudRun.resultPackage || workspace.cloudRun.status === "succeeded" || workspace.stage === "result_review") return "assets";
  if (workspace.cloudRun.exchangePackageID || workspace.cloudRun.status === "queued" || workspace.cloudRun.status === "running") return "execution";
  if (workspace.executableScriptBundle || workspace.packagePreview.buildStatus === "draft" || workspace.stage === "package_approval") return "approval";
  if (workspace.projectIntelligence || workspace.understandingReport) return "plan";
  return workspace.productURL && workspace.inputBundle.raw_user_prompt ? "evidence" : "overview";
}

const lifecycleOwnedWorkstations = new Set<ProjectWorkstationView>(["approval", "execution", "repair", "assets"]);

// Planning is conversational, while approval and live execution remain owned
// by deterministic project state. This prevents a stale assistant session from
// hiding a sensitive or currently running lifecycle surface.
export function resolveProjectTaskWorkstation(workspace: ProjectWorkspaceView, session?: AssistantSessionView): ProjectWorkstationView {
  const lifecycleWorkstation = recommendedWorkstation(workspace);
  if (lifecycleOwnedWorkstations.has(lifecycleWorkstation)) return lifecycleWorkstation;

  const assistantWorkstation = session?.activeWorkstation;
  if (!assistantWorkstation) return lifecycleWorkstation;

  if (assistantWorkstation === "overview") {
    return session.configuration.confirmed ? lifecycleWorkstation : "overview";
  }
  if (assistantWorkstation === "evidence" && (lifecycleWorkstation === "evidence" || lifecycleWorkstation === "plan")) {
    return "evidence";
  }
  if (assistantWorkstation === "plan" && lifecycleWorkstation === "plan") {
    return "plan";
  }
  return lifecycleWorkstation;
}

export function displayedProjectWorkstation(taskWorkstation: ProjectWorkstationView, inspectionWorkstation?: ProjectWorkstationView): ProjectWorkstationView {
  return inspectionWorkstation ?? taskWorkstation;
}

export function shouldResumeCloudRun(workspace: ProjectWorkspaceView, selectedProjectID?: string): boolean {
  return selectedProjectID === workspace.id
    && (workspace.cloudRun.status === "queued" || workspace.cloudRun.status === "running")
    && Boolean(workspace.cloudRun.exchangePackageID);
}

export function projectNextAction(workspace: ProjectWorkspaceView): ProjectNextAction {
  const workstation = recommendedWorkstation(workspace);
  if (workstation === "overview") {
    return { kind: "configure", workstation, title: "补全项目配置", description: "在左侧告诉 Cascade 产品地址、演示目标和源码来源。配置确认只会启动本地分析。" };
  }
  if (workstation === "evidence") {
    return { kind: "analyze", workstation, title: "分析并生成执行方案", description: "读取已批准的本地材料，生成证据、阶段计划和 BrowserAgent 大纲；不会上传服务器。" };
  }
  if (workstation === "plan") {
    return { kind: "review_plan", workstation, title: "检查录制方案", description: "确认业务阶段、必须展示内容和安全边界，再进入独立上传审批。" };
  }
  if (workstation === "approval") {
    return { kind: "approve_upload", workstation, title: "审批并上传执行包", description: "逐项确认数据范围与风险后，才会把当前 digest 对应的执行包上传服务器。" };
  }
  if (workstation === "execution") {
    return { kind: "monitor", workstation, title: "服务器正在生成成片", description: "状态会自动恢复并更新；你可以离开当前页面，无需手动刷新。" };
  }
  if (workstation === "repair") {
    return { kind: "repair", workstation, title: "修复失败步骤", description: "根据脱敏诊断重新生成脚本；修复后的执行包仍需再次人工审批。" };
  }
  if (workspace.cloudRun.resultReview?.decision === "approved") {
    return { kind: "complete", workstation: "assets", title: "成品已通过", description: "最终审核已记录，可以在 Editor 中继续处理或导出成品。" };
  }
  if (!workspace.cloudRun.resultDownloaded) {
    return { kind: "review_result", workstation: "assets", title: "下载并校验成品", description: "完整下载结果包并校验 SHA-256 后，才开放人工审核。" };
  }
  return { kind: "review_result", workstation: "assets", title: "人工审核成品", description: "播放成品并选择通过、重新剪辑或缺少素材需要重新录制。" };
}

export function executionServerBlockedReason(resolved: boolean, sessionValid: boolean | undefined): string | undefined {
	if (!resolved) return "正在检查执行服务器连接。";
	if (sessionValid !== true) return "执行服务器尚未完成连接与安装身份验证。";
	return undefined;
}

export function projectJourney(workspace: ProjectWorkspaceView): ProjectJourneyStep[] {
  const currentWorkstation = recommendedWorkstation(workspace);
  const currentID: ProjectJourneyStepID = currentWorkstation === "overview" || currentWorkstation === "evidence"
    ? "evidence"
    : currentWorkstation === "plan"
      ? "plan"
      : currentWorkstation === "approval" || currentWorkstation === "repair"
        ? "approval"
        : currentWorkstation === "execution"
          ? "execution"
          : "assets";
  const currentIndex = journeyOrder.indexOf(currentID);
  return journeyOrder.map((id, index) => ({
    id,
    label: journeyLabels[id],
    workstation: id,
    status: id === currentID
      ? (currentWorkstation === "repair" ? "blocked" : "current")
      : index < currentIndex
        ? "completed"
        : "upcoming",
  }));
}

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
    : workspace.cloudRun.exchangePackageID
      ? "package_uploaded"
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
  if (preview.readiness === "blocked") {
    blocked.add("执行包确信度门禁未通过，请先修复阻断项。");
  }
  if (!checklist.userApprovedPlan) {
    blocked.add("上传前必须完成人工审批。");
  }
  if (!checklist.ipAllowlistAcknowledged && !preview.ipAllowlistAcknowledged) {
    blocked.add("需要确认 DemoOps 执行服务器出口 IP 已加入客户环境白名单。");
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
