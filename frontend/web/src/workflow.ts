import type { DemoWorkflowGraph, GraphNode } from "../../src/types/workflowGraph";
import type { ApprovalChecklistState, CloudRunStatus, ExecutionPackagePreview, SourceConnectionView } from "./domain";

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
  if (!checklist.ipAllowlistAcknowledged || !preview.ipAllowlistAcknowledged) {
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
