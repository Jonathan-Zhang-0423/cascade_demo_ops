import { describe, expect, it } from "vitest";
import type { ApprovalChecklistState } from "./domain";
import { createWorkspace } from "./mockWorkspace";
import { canUploadExecutionPackage, mapCloudStatus, packageApprovalBlockedReasons, updateGraphNode } from "./workflow";

describe("workflow helpers", () => {
  it("edits workflow graph nodes without mutating the original graph", () => {
    const workspace = createWorkspace("product_demo");
    const original = workspace.planReview.graph;
    const next = updateGraphNode(original, "node_primary_action", { has_zoom: false, is_screenshot: false });

    expect(original.nodes.find((node) => node.id === "node_primary_action")?.has_zoom).toBe(true);
    expect(next.nodes.find((node) => node.id === "node_primary_action")?.has_zoom).toBe(false);
    expect(next.nodes.find((node) => node.id === "node_primary_action")?.is_screenshot).toBe(false);
  });

  it("blocks package upload until approval, allowlist, grants, and redactions are reviewed", () => {
    const workspace = createWorkspace("product_demo");
    const checklist: ApprovalChecklistState = {
      userApprovedPlan: false,
      ipAllowlistAcknowledged: false,
      sourceSummaryOnlyAcknowledged: true,
      credentialGrantAcknowledged: false,
      redactionsReviewed: false,
    };

    const reasons = packageApprovalBlockedReasons(workspace.packagePreview, checklist, workspace.sourceConnections);
    expect(reasons).toContain("上传前必须完成人工审批。");
    expect(reasons).toContain("需要确认 Cascade 云端执行 IP 已加入白名单。");
    expect(canUploadExecutionPackage(workspace.packagePreview, checklist, workspace.sourceConnections)).toBe(false);
  });

  it("allows package upload after every security checklist item is complete", () => {
    const workspace = createWorkspace("product_demo");
    const checklist: ApprovalChecklistState = {
      userApprovedPlan: true,
      ipAllowlistAcknowledged: true,
      sourceSummaryOnlyAcknowledged: true,
      credentialGrantAcknowledged: true,
      redactionsReviewed: true,
    };
    const preview = { ...workspace.packagePreview, ipAllowlistAcknowledged: true };
    const readySources = workspace.sourceConnections.map((source) => ({ ...source, status: "ready" as const }));

    expect(packageApprovalBlockedReasons(preview, checklist, readySources)).toEqual([]);
    expect(canUploadExecutionPackage(preview, checklist, readySources)).toBe(true);
  });

  it("maps cloud package and result states to app statuses", () => {
    expect(mapCloudStatus("uploaded")).toBe("queued");
    expect(mapCloudStatus("running")).toBe("running");
    expect(mapCloudStatus("completed")).toBe("succeeded");
    expect(mapCloudStatus("expired")).toBe("failed");
    expect(mapCloudStatus("unknown")).toBe("not_uploaded");
  });
});
