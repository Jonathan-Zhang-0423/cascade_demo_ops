import { describe, expect, it } from "vitest";
import type { ApprovalChecklistState } from "./domain";
import { createProjectDraftWorkspace, createWorkspace } from "./mockWorkspace";
import {
  canUploadExecutionPackage,
	executionServerBlockedReason,
  lifecycleStagesFromWorkspace,
  mapCloudStatus,
  packageApprovalBlockedReasons,
  projectJourney,
  projectNextAction,
  projectStatusLabels,
  recommendedWorkstation,
  resetApprovalChecklistForRepair,
  sandboxRiskLevel,
  sandboxRiskMessage,
  shouldResumeCloudRun,
  updateGraphNode,
  workflowStageLabels,
} from "./workflow";

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
    expect(reasons).toContain("需要确认 DemoOps 执行服务器出口 IP 已加入客户环境白名单。");
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

  it("blocks confidence-gated package even when approval checklist is complete", () => {
	const workspace = createWorkspace("product_demo");
	const checklist: ApprovalChecklistState = { userApprovedPlan: true, ipAllowlistAcknowledged: true, sourceSummaryOnlyAcknowledged: true, credentialGrantAcknowledged: true, redactionsReviewed: true };
	const preview = { ...workspace.packagePreview, readiness: "blocked" as const, blockedReasons: ["node_checkout: 缺少必填确定性结果验证"] };
	const sources = workspace.sourceConnections.map((source) => ({ ...source, status: "ready" as const }));
	expect(canUploadExecutionPackage(preview, checklist, sources)).toBe(false);
	expect(packageApprovalBlockedReasons(preview, checklist, sources)).toContain("执行包确信度门禁未通过，请先修复阻断项。");
  });

  it("treats the UI allowlist acknowledgement as enough for mock package upload", () => {
    const workspace = createWorkspace("product_demo");
    const checklist: ApprovalChecklistState = {
      userApprovedPlan: true,
      ipAllowlistAcknowledged: true,
      sourceSummaryOnlyAcknowledged: true,
      credentialGrantAcknowledged: true,
      redactionsReviewed: true,
    };
    const readySources = workspace.sourceConnections.map((source) => ({ ...source, status: "ready" as const }));

    expect(workspace.packagePreview.ipAllowlistAcknowledged).toBe(false);
    expect(packageApprovalBlockedReasons(workspace.packagePreview, checklist, readySources)).toEqual([]);
    expect(canUploadExecutionPackage(workspace.packagePreview, checklist, readySources)).toBe(true);
  });

  it("maps cloud package and result states to app statuses", () => {
    expect(mapCloudStatus("uploaded")).toBe("queued");
    expect(mapCloudStatus("running")).toBe("running");
    expect(mapCloudStatus("completed")).toBe("succeeded");
    expect(mapCloudStatus("expired")).toBe("failed");
    expect(mapCloudStatus("unknown")).toBe("not_uploaded");
  });

  it("keeps the desktop workflow stage labels complete and ordered by app model", () => {
    expect(Object.keys(workflowStageLabels)).toEqual([
      "setup",
      "inputs",
      "understanding",
      "plan_review",
      "package_approval",
      "cloud_run",
      "script_repair",
      "result_review",
    ]);
    expect(workflowStageLabels.script_repair).toBe("脚本修复");
    expect(projectStatusLabels.script_repair_required).toBe("脚本待修复");
  });

  it("resets approval checklist when a repaired script package is generated", () => {
    const checklist: ApprovalChecklistState = {
      userApprovedPlan: true,
      ipAllowlistAcknowledged: true,
      sourceSummaryOnlyAcknowledged: true,
      credentialGrantAcknowledged: true,
      redactionsReviewed: true,
    };

    expect(resetApprovalChecklistForRepair(checklist)).toEqual({
      userApprovedPlan: false,
      ipAllowlistAcknowledged: false,
      sourceSummaryOnlyAcknowledged: true,
      credentialGrantAcknowledged: false,
      redactionsReviewed: false,
    });
  });

  it("derives lifecycle stages from the workspace when server history is absent", () => {
    const workspace = {
      ...createWorkspace("product_demo"),
      stage: "package_approval" as const,
      executableScriptBundle: {} as never,
    };

    const stages = lifecycleStagesFromWorkspace(workspace);

    expect(stages).toHaveLength(10);
    expect(stages[0]).toMatchObject({ id: "local_generated", status: "completed", progress: 100 });
    expect(stages[1]).toMatchObject({ id: "human_approved", status: "pending" });
  });

  it("starts real project drafts without demo URLs, repositories, credentials, or generated assets", () => {
    const draft = createProjectDraftWorkspace("product_demo");

    expect(draft.stage).toBe("setup");
    expect(draft.targetAudience).toBe("");
    expect(draft.productURL).toBe("");
    expect(draft.inputBundle.product_urls).toEqual([]);
    expect(draft.inputBundle.repositories).toEqual([]);
    expect(draft.inputBundle.credentials).toEqual([]);
    expect(draft.planReview.graph.nodes).toEqual([]);
    expect(draft.assets).toEqual([]);
    expect(JSON.stringify(draft)).not.toContain("app.example.com");
  });

  it("derives one recommended workstation and next action from authoritative project state", () => {
    const draft = createProjectDraftWorkspace("product_demo");
    expect(recommendedWorkstation(draft)).toBe("overview");
    expect(projectNextAction(draft).kind).toBe("configure");

    const configured = { ...draft, productURL: "https://product.example", inputBundle: { ...draft.inputBundle, raw_user_prompt: "展示核心流程" } };
    expect(recommendedWorkstation(configured)).toBe("evidence");
    expect(projectNextAction(configured).kind).toBe("analyze");

    const planned = { ...configured, projectIntelligence: {} as never };
    expect(recommendedWorkstation(planned)).toBe("plan");
    expect(projectNextAction(planned).kind).toBe("review_plan");

    const packaged = { ...planned, executableScriptBundle: {} as never, packagePreview: { ...planned.packagePreview, buildStatus: "draft" as const } };
    expect(recommendedWorkstation(packaged)).toBe("approval");
    expect(projectNextAction(packaged).kind).toBe("approve_upload");

    const running = { ...packaged, cloudRun: { ...packaged.cloudRun, exchangePackageID: "xpkg_1", status: "running" as const } };
    expect(recommendedWorkstation(running)).toBe("execution");
    expect(projectNextAction(running).kind).toBe("monitor");
	expect(shouldResumeCloudRun(running, running.id)).toBe(true);
	expect(shouldResumeCloudRun(running, "another-project")).toBe(false);

    const completed = { ...running, stage: "result_review" as const, status: "asset_ready" as const, cloudRun: { ...running.cloudRun, status: "succeeded" as const, resultPackageID: "result_1" } };
    expect(recommendedWorkstation(completed)).toBe("assets");
    expect(projectNextAction(completed).kind).toBe("review_result");
    expect(projectJourney(completed).map((step) => step.status)).toEqual(["completed", "completed", "completed", "completed", "current"]);
  });

	it("keeps upload blocked until execution-server health is resolved and configured", () => {
		expect(executionServerBlockedReason(false, undefined)).toBe("正在检查执行服务器连接。");
		expect(executionServerBlockedReason(true, false)).toBe("执行服务器尚未完成连接与安装身份验证。");
		expect(executionServerBlockedReason(true, true)).toBeUndefined();
	});

  it("recognizes production and dev sandbox policy risk levels", () => {
    const workspace = createWorkspace("product_demo");
    const productionPolicy = {
      profile: "mvp_cloud",
      isolation_mode: "per_job_container",
      network_policy: { mode: "allowed_domains_only", proxy_required: true },
      filesystem_policy: { no_host_mount: true, no_docker_socket: true, delete_temp_after_run: true },
      resource_limits: {},
      browser_policy: { fresh_context_per_run: true, disable_extensions: true, disable_downloads: true, trace_sources: false },
      secret_policy: { vault_only: true, inject_via_context_only: true, forbid_env_injection: true, revoke_after_run: true, rotation_required_after_run: false },
      artifact_policy: { encrypt_sensitive_artifacts: true, sensitive_by_default: true, require_checksum: true },
      diagnostic_policy: { redaction_required: true, forbid_full_html: true, encrypt_diagnostics: true, return_repair_hints: true },
    };
    const devPolicy = { ...productionPolicy, profile: "dev", isolation_mode: "local_sidecar" };

    expect(sandboxRiskLevel(productionPolicy)).toBe("ok");
    expect(sandboxRiskLevel(devPolicy)).toBe("warning");
    expect(sandboxRiskMessage(undefined)).toContain("缺少 sandbox_policy");
    expect(workspace.planReview.allowedDomains).toContain("app.example.com");
  });
});
