import type { DemoWorkflowGraph } from "../../src/types/workflowGraph";
import type { ApprovalChecklistState, ProjectWorkspaceView, ScenarioID } from "./domain";
import { getScenarioTemplate } from "./scenarios";

export const initialChecklist: ApprovalChecklistState = {
  userApprovedPlan: false,
  ipAllowlistAcknowledged: false,
  sourceSummaryOnlyAcknowledged: true,
  credentialGrantAcknowledged: false,
  redactionsReviewed: false,
};

export function createWorkspace(scenarioID: ScenarioID = "product_demo"): ProjectWorkspaceView {
  const template = getScenarioTemplate(scenarioID);
  const graph = createDemoGraph(scenarioID);
  const targetAudience = template.targetAudience;

  return {
    id: `project_${scenarioID}`,
    name: template.name,
    scenarioID,
    stage: "plan_review",
    productURL: scenarioID === "ai_customer_service_demo" ? "https://support.example.com" : "https://app.example.com",
    targetAudience,
    status: "awaiting_approval",
    inputBundle: {
      product_urls: [{ url: "https://app.example.com", kind: "staging", environment: "staging" }],
      repositories: [{ local_path: "本地项目目录", provider: "local", read_only: true, primary: true }],
      credentials: [
        {
          id: "cred_demo_user",
          kind: "demo_account",
          secret_ref: "vault://local/demo-account",
          scope: "recording_session",
          required_for: ["cloud_recording_login"],
          session_policy: "expire_after_run",
        },
      ],
      raw_user_prompt: template.objective,
    },
    sourceConnections: [
      {
        id: "source_product_url",
        kind: "product_url",
        label: "产品地址",
        status: "ready",
        detail: "测试环境可访问，等待确认云端执行 IP 白名单。",
      },
      {
        id: "source_repo",
        kind: "local_repo",
        label: "本地代码结构摘要",
        status: "ready",
        detail: "已提取路由、组件、数据模型摘要，不上传完整源码。",
      },
      {
        id: "source_github_repo",
        kind: "github_repo",
        label: "GitHub 仓库结构摘要",
        status: "needs_attention",
        detail: "可选填写 GitHub 仓库 URL；与本地项目目录并列，不互相替代。",
      },
      {
        id: "source_credentials",
        kind: "credential",
        label: "演示账号授权",
        status: "needs_attention",
        detail: "授权将在云端录制结束后自动过期。",
        secretStored: true,
      },
    ],
    understanding: {
      productMapID: "map_1",
      routesDetected: scenarioID === "ai_customer_service_demo" ? 7 : 11,
      featuresDetected: scenarioID === "internal_onboarding_tutorial" ? 5 : 8,
      componentsSummarized: 18,
      dataModelsSummarized: 6,
      evidenceRefs: [
        { id: "ev_routes", kind: "source_code", summary: "路由与组件结构摘要", confidence: 0.91 },
        { id: "ev_requirement", kind: "requirement_doc", summary: "演示目标与受众要求", confidence: 0.88 },
      ],
      sensitiveWarnings: ["客户邮箱选择器会自动打码", "账单页和 API Key 页面禁止访问"],
    },
    planReview: {
      graph,
      targetDurationSec: template.targetDurationSec,
      allowedDomains: ["app.example.com", "support.example.com"],
      forbiddenPages: ["/billing", "/settings/api-keys"],
      redactionSelectors: ["[data-sensitive]", "[data-testid='customer-email']"],
      outputRequests: template.requiredAssets,
    },
    packagePreview: {
      packageID: "pkg_preview_1",
      packageDigest: "sha256:71b5f3d0a5f4b2a6",
      graphDigest: "sha256:19e83593df04c1aa",
      sourceSummaryOnly: true,
      encrypted: true,
      humanApprovalRequired: true,
      ipAllowlistAcknowledged: false,
      credentialGrants: [
        {
          grantID: "grant_demo_login",
          kind: "demo_account",
          purpose: "云端录制登录",
          expiresAt: "2026-07-08T12:00:00Z",
          allowedDomains: ["app.example.com", "support.example.com"],
          rawSecretVisible: false,
        },
      ],
      blockedReasons: [],
    },
    cloudRun: {
      packageID: "pkg_preview_1",
      cloudJobID: "job_mock_1",
      status: "not_uploaded",
      currentStep: "等待审批执行包",
      progress: 0,
      retryCount: 0,
    },
    assets: [
      {
        assetID: "asset_video",
        kind: "video",
        title: "最终演示视频",
        status: "generated",
        uri: "cascade://assets/demo-video.mp4",
        checksum: "sha256:1f28a9",
        provenance: "由 graph_1 v1 和 pkg_preview_1 生成",
      },
      {
        assetID: "asset_docs",
        kind: "step_docs",
        title: "步骤说明文档",
        status: "generated",
        uri: "cascade://assets/step-by-step.md",
        checksum: "sha256:d391aa",
        provenance: "由执行轨迹 trace_1 生成",
      },
    ],
  };
}

function createDemoGraph(scenarioID: ScenarioID): DemoWorkflowGraph {
  const isSupport = scenarioID === "ai_customer_service_demo";
  const isOnboarding = scenarioID === "internal_onboarding_tutorial";
  return {
    id: "graph_1",
    project_id: `project_${scenarioID}`,
    schema_version: "demoops.workflow_graph.v1",
    version: 1,
    status: "review_ready",
    name: isSupport ? "AI 客服问题解决流程" : isOnboarding ? "新员工业务教程" : "产品发布演示流程",
    summary: isSupport
      ? "展示 AI 客服如何回答问题、检索知识，并在必要时转人工。"
      : isOnboarding
        ? "引导新员工完成第一次核心业务操作。"
        : "展示从工作台到团队协作的产品价值路径。",
    entry_point: isSupport ? "https://support.example.com/chat" : "https://app.example.com/dashboard",
    nodes: [
      {
        id: "node_open",
        action: "navigate",
        selector: "",
        input_data: "",
        expected_outcome: "入口页面以正确账号状态加载",
        is_screenshot: true,
        has_zoom: false,
        retry_policy: 2,
        type: "action",
        title: isSupport ? "打开客服对话" : "打开产品工作台",
        action_spec: {
          type: "navigate",
          target: { url: isSupport ? "https://support.example.com/chat" : "https://app.example.com/dashboard" },
        },
        capture: { screenshot: true, video: true, mask_selectors: ["[data-sensitive]"] },
        duration_hint_ms: 2500,
      },
      {
        id: "node_primary_action",
        action: isSupport ? "发送客服问题" : isOnboarding ? "完成新手任务" : "邀请团队成员",
        selector: isSupport ? "[data-testid='chat-input']" : "[data-testid='primary-action']",
        input_data: isSupport ? "如何重置工作区邀请链接？" : "",
        expected_outcome: isSupport ? "AI 回答引用知识来源" : "核心流程进入下一步",
        is_screenshot: true,
        has_zoom: true,
        retry_policy: 2,
        type: "action",
        title: isSupport ? "向 AI 客服提问" : isOnboarding ? "完成引导任务" : "邀请团队成员",
        duration_hint_ms: 4500,
      },
      {
        id: "node_proof",
        action: isSupport ? "验证解决结果" : "校验成功状态",
        selector: "[data-testid='success-state']",
        input_data: "",
        expected_outcome: isSupport ? "解决方案和转人工边界清晰可见" : "成功状态清晰可见",
        is_screenshot: true,
        has_zoom: false,
        retry_policy: 1,
        type: "validation",
        title: "录制结果证明",
        duration_hint_ms: 3000,
      },
    ],
    edges: [
      { id: "edge_open_primary", from_node: "node_open", to_node: "node_primary_action" },
      { id: "edge_primary_proof", from_node: "node_primary_action", to_node: "node_proof" },
    ],
    assets: {
      demo_video_60s: true,
      screenshot_pack: false,
      step_by_step_docs: true,
      target_duration_sec: isOnboarding ? 120 : isSupport ? 90 : 60,
      requested_assets: [
        { id: "demo_video", kind: "demo_video", format: "mp4", required: true, status: "requested" },
        { id: "step_docs", kind: "step_by_step_docs", format: "markdown", required: true, status: "requested" },
      ],
    },
    execution: {
      required_pass_rate: 0.9,
      max_attempts: 2,
      timeout_ms: 120000,
      browser: "chromium",
      headless: true,
      viewports: [{ name: "desktop", width: 1440, height: 900, device: "desktop" }],
      trace_level: "screenshots_dom_console_network",
    },
  };
}
