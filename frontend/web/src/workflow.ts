import type { DemoWorkflowGraph, GraphNode } from "../../src/types/workflowGraph";
import type { ApprovalChecklistState, CloudRunStatus, ExecutionPackagePreview, ProjectWorkspaceView, SourceConnectionView, WorkspaceStage } from "./domain";

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
  if (!checklist.credentialGrantAcknowledged || preview.credentialGrants.length === 0) {
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
