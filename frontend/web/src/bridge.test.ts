import { describe, expect, it } from "vitest";
import { afterEach, vi } from "vitest";
import { userInputFromWorkspace, createLocalBridgeClient, createMockBridgeClient, workspaceFromCascadeStateForTest } from "./bridge";
import { updateWorkspaceInputs } from "./agentPipeline";
import { createWorkspace } from "./mockWorkspace";

describe("desktop bridge contract", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns redacted runtime health", async () => {
    const bridge = createMockBridgeClient();
    const result = await bridge.runtimeHealth();

    expect(result.ok).toBe(true);
    expect(JSON.stringify(result.data)).not.toMatch(/sk-|secret-|C:\\\\|postgres:\/\//i);
    expect(result.data?.modelProviders.seedance?.apiKeyFallbackEnvs).toEqual(["DOUBAO_API_KEY", "ARK_API_KEY"]);
  });

  it("builds execution package preview without full source or raw secrets", async () => {
    const bridge = createMockBridgeClient();
    const workspace = createWorkspace("product_demo");
    const result = await bridge.buildExecutionPackagePreview(workspace);
    const payload = JSON.stringify(result.data);

    expect(result.ok).toBe(true);
    expect(result.data?.executableScriptBundle?.playwright_script.inline_source).toContain("runCascadeRecording");
    expect(payload).not.toContain("function submitPayment");
    expect(payload).not.toContain("raw-password");
    expect(payload).toContain("sourceSummaryOnly");
  });

  it("returns failed result diagnostics and repair requests", async () => {
    const bridge = createMockBridgeClient();
    const workspace = createWorkspace("product_demo");
    const failure = await bridge.simulateCloudFailure(workspace);

    expect(failure.ok).toBe(true);
    expect(failure.data?.stage).toBe("script_repair");
    expect(failure.data?.cloudRun.failureDiagnostic?.schema_version).toBe("demoops.script_failure_diagnostic.v1");
    expect(failure.data?.cloudRun.failureDiagnostic?.redaction_report.applied).toBe(true);
    expect(failure.data?.cloudRun.failureDiagnostic?.screenshot_refs?.[0]?.sensitive).toBe(true);
    expect(failure.data?.cloudRun.repairRequest?.approval_required).toBe(true);
    expect(failure.data?.cloudRun.stageHistory?.find((stage) => stage.id === "browser_execution")?.status).toBe("failed");
    expect(failure.data?.cloudRun.artifactSummary?.encrypted).toBeGreaterThan(0);
  });

  it("generates repaired bundles with lineage and returns to approval", async () => {
    const bridge = createMockBridgeClient();
    const workspace = createWorkspace("product_demo");
    const failure = await bridge.simulateCloudFailure(workspace);
    if (!failure.data) {
      throw new Error("expected failure workspace");
    }

    const repaired = await bridge.repairFailedScript(failure.data);

    expect(repaired.ok).toBe(true);
    expect(repaired.data?.stage).toBe("package_approval");
    expect(repaired.data?.status).toBe("awaiting_approval");
    expect(repaired.data?.executableScriptBundle?.repair_lineage?.source_result_id).toBe(failure.data.cloudRun.repairRequest?.source_result_id);
    expect(repaired.data?.scriptMarkdown).toContain("本次修复说明");
  });

  it("runs the mock upload lifecycle through result ack", async () => {
    const bridge = createMockBridgeClient();
    const workspace = createWorkspace("product_demo");
    const packageResult = await bridge.buildExecutionPackagePreview(workspace);
    if (!packageResult.data) {
      throw new Error("expected package workspace");
    }

    const init = await bridge.initExecutionPackageUpload(packageResult.data);
    const upload = await bridge.uploadExecutionPackage({
      ...packageResult.data,
      cloudRun: { ...packageResult.data.cloudRun, ...(init.data?.uploadID ? { uploadID: init.data.uploadID } : {}) },
    });
    const running = await bridge.pollExecutionPackageStatus({
      ...packageResult.data,
      cloudRun: {
        ...packageResult.data.cloudRun,
        ...(init.data?.uploadID ? { uploadID: init.data.uploadID } : {}),
        ...(upload.data?.exchangePackageID ? { exchangePackageID: upload.data.exchangePackageID } : {}),
        ...(upload.data?.cloudJobID ? { cloudJobID: upload.data.cloudJobID } : {}),
      },
    });
    const completed = await bridge.pollCloudRun(running.data ?? packageResult.data);
    const resultPackage = await bridge.getResultPackage(completed.data ?? packageResult.data);
    const acked = await bridge.ackResultPackage(completed.data ?? packageResult.data);

    expect(init.data?.supportedCryptoSuites).toContain("aes-256-gcm");
    expect(upload.data?.status).toBe("queued");
    expect(running.data?.cloudRun.stageHistory?.some((stage) => stage.id === "script_validation" && stage.status === "completed")).toBe(true);
    expect(completed.data?.stage).toBe("result_review");
    expect(completed.data?.cloudRun.stageHistory?.find((stage) => stage.id === "result_returned")?.status).toBe("completed");
    expect(resultPackage.data?.delivery?.asset_refs?.[0]?.role).toBe("final_demo_video");
    expect(resultPackage.data?.delivery?.asset_refs?.every((artifact) => artifact.encrypted && artifact.sensitive)).toBe(true);
    expect(acked.data?.assets.every((asset) => asset.status === "approved")).toBe(true);
  });

  it("maps local dev bridge execution packages into the workspace approval view", async () => {
    const workspace = updateWorkspaceInputs(createWorkspace("product_demo"), {
      productURL: "https://real.example.com",
      localRepoPath: "C:\\Users\\demo\\project",
      rawUserPrompt: "真实项目演示需求",
      targetAudience: "中国运营团队",
    });
    const fetchMock = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        ok: true,
        data: {
          project_id: "project_real",
          current_node: "HumanApprove",
          status: "awaiting_human_approval",
          project_context: {
            id: "project_real",
            product_url: "https://real.example.com",
            product_description: "真实执行包生成",
            target_audience: "中国运营团队",
            forbidden_pages: ["/billing"],
            inputs: workspace.inputBundle,
          },
          product_map: { id: "map_real", summary: "真实产品地图" },
          project_intelligence: {
            id: "pi_real",
            project_id: "project_real",
            schema_version: "demoops.project_intelligence_pack.v1",
            source_digest_sha256: "sha256:intelligence",
            architecture: {
              id: "arch_real",
              project_id: "project_real",
              summary: "React + Go 混合项目",
              frameworks: ["react", "go"],
              route_tree: [{ id: "route_dashboard", path: "/dashboard" }],
              modules: [{ id: "module_web", name: "web", component_refs: ["component_a"] }],
            },
            feature_capabilities: [{ id: "cap_real", name: "团队协作", key_actions: ["inspect"] }],
            interaction_surfaces: [{ id: "surface_real", title: "工作台", stable_selectors: [{ kind: "css", value: "main" }] }],
            api_contracts: [{ id: "api_invites", path: "/api/team/invites" }],
            data_models: [{ id: "model_team", name: "Team" }],
            evidence_refs: [{ id: "ev_pi", kind: "code_snapshot", summary: "项目图谱摘要", confidence: 0.86 }],
            safety_report: { allowed_to_proceed: true, policy_findings: [{ id: "finding_pi", kind: "summary_only", severity: "info", summary: "不上传完整源码" }] },
            confidence: 0.86,
          },
          script_readiness_report: {
            id: "ready_real",
            project_id: "project_real",
            schema_version: "demoops.script_readiness_report.v1",
            can_proceed: true,
            recommended_scenario_name: "团队协作主线演示",
            selector_coverage: 0.75,
            warnings: [],
            blockers: [],
          },
          agent_graph_trace: {
            id: "trace_real",
            project_id: "project_real",
            schema_version: "demoops.agent_graph_trace.v1",
            graph_name: "ProjectIntelligenceGraph",
            steps: [{ id: "trace_step_1", node_id: "repo_index", tool: "RepoIndexTool", status: "completed", output_summary: "识别项目结构" }],
          },
          understanding_report: {
            id: "understanding_real",
            project_id: "project_real",
            schema_version: "demoops.multimodal_understanding_report.v1",
            summary: "真实理解报告",
            feature_hypotheses: [{ id: "feature_real", name: "团队协作", value: "提升协作效率" }],
            evidence_refs: [
              { id: "ev_real", kind: "requirement_doc", summary: "需求摘要", confidence: 0.9 },
              { id: "ev_model", kind: "requirement_doc", summary: "RequirementReaderAgent 模型路由：kimi/kimi-k2.7-code/domestic-llm-adapter-v1/adapter=kimi-openai-compatible", confidence: 0.8 },
            ],
            input_fingerprints: { product_url: "sha256:product" },
            source_digest_sha256: "sha256:source",
            confidence: 0.9,
          },
          workflow_graph: workspace.planReview.graph,
          script_document: {
            ...workspace.scriptDocument,
            id: "script_real",
            project_id: "project_real",
            workflow_graph_id: workspace.planReview.graph.id,
            graph_version: workspace.planReview.graph.version,
            schema_version: "demoops.execution_script_document.v1",
            recording_run_spec: {
              run_id: "run_real",
              base_url: "https://real.example.com",
              allowed_domains: ["real.example.com"],
              timezone: "Asia/Shanghai",
              locale: "zh-CN",
              browser: { engine: "chromium", headless: true },
              timeline: { target_duration_sec: 60 },
              outputs: { raw_recording: true, final_video: true, screenshot_pack: false, step_by_step_docs: true, trace: true },
              redactions: { mask_selectors: ["[data-sensitive]"] },
              failure_policy: { selector_repair_allowed: true, data_repair_allowed: false },
            },
            steps: [],
            safety_policy: {
              allowed_domains: ["real.example.com"],
              forbidden_pages: ["/billing"],
              forbidden_data: ["API Key"],
              redactions: { mask_selectors: ["[data-sensitive]"] },
            },
            reproducibility: { graph_hash_sha256: "sha256:graph", script_hash_sha256: "sha256:plan" },
            approval_checklist: {
              human_approval_required: true,
              source_summary_only: true,
              credential_scope_review_required: false,
              redactions_review_required: true,
              ip_allowlist_acknowledgement_required: true,
              blocking_reasons: ["上传前必须完成人工审批。"],
            },
          },
          script_markdown: "# 真实中文思路文档\n\n## 执行步骤",
          executable_script_bundle: {
            id: "bundle_real",
            project_id: "project_real",
            workflow_graph_id: workspace.planReview.graph.id,
            schema_version: "demoops.executable_recording_script_bundle.v1",
            status: "review_ready",
            script_manifest: {
              script_id: "recording_real",
              version: 1,
              language: "typescript",
              runtime: "playwright-restricted-sandbox",
              entry_function: "runCascadeRecording",
              generator: "cascade_deterministic_script_code_generator",
              generator_version: "0.1.0",
              step_node_ids: ["node_open"],
            },
            plan_json: {
              id: "script_real",
              project_id: "project_real",
              workflow_graph_id: workspace.planReview.graph.id,
              graph_version: workspace.planReview.graph.version,
              schema_version: "demoops.execution_script_document.v1",
              recording_run_spec: {
                base_url: "https://real.example.com",
                allowed_domains: ["real.example.com"],
                browser: { engine: "chromium", headless: true },
                timeline: { target_duration_sec: 60 },
                outputs: { raw_recording: true, final_video: true, screenshot_pack: false, step_by_step_docs: true, trace: true },
                redactions: { mask_selectors: ["[data-sensitive]"] },
                failure_policy: { selector_repair_allowed: true, data_repair_allowed: false },
              },
              steps: [],
              safety_policy: { redactions: { mask_selectors: ["[data-sensitive]"] } },
              reproducibility: { graph_hash_sha256: "sha256:graph", script_hash_sha256: "sha256:plan" },
              approval_checklist: { human_approval_required: true, source_summary_only: true, credential_scope_review_required: false, redactions_review_required: true, ip_allowlist_acknowledgement_required: true },
            },
            playwright_script: { inline_source: "export async function runCascadeRecording() { return { ok: true }; }", sha256: "sha256:script" },
            approval_markdown: { inline_markdown: "# 真实中文思路文档", sha256: "sha256:markdown" },
            security_policy: { allowed_domains: ["real.example.com"], redactions: { mask_selectors: ["[data-sensitive]"] } },
            reproducibility: {
              plan_hash_sha256: "sha256:plan",
              script_hash_sha256: "sha256:script",
              markdown_hash_sha256: "sha256:markdown",
              bundle_hash_sha256: "sha256:bundle",
              graph_hash_sha256: "sha256:graph",
            },
            validation: { valid: true },
          },
        },
      }),
    }));
    vi.stubGlobal("fetch", fetchMock);

    const bridge = createLocalBridgeClient("http://127.0.0.1:4317");
    const result = await bridge.buildExecutionPackagePreview(workspace);

    expect(result.ok).toBe(true);
    expect(result.data?.id).toBe("project_real");
    expect(result.data?.stage).toBe("package_approval");
    expect(result.data?.scriptMarkdown).toContain("真实中文思路文档");
    expect(result.data?.executableScriptBundle?.playwright_script.inline_source).toContain("runCascadeRecording");
    expect(result.data?.projectIntelligence?.architecture?.summary).toBe("React + Go 混合项目");
    expect(result.data?.scriptReadiness?.recommended_scenario_name).toBe("团队协作主线演示");
    expect(result.data?.agentGraphTrace?.steps?.[0]?.tool).toBe("RepoIndexTool");
    expect(result.data?.understanding.routesDetected).toBe(1);
    expect(result.data?.packagePreview.packageDigest).toBe("sha256:bundle");
    expect(result.data?.modelProvenance?.[0]).toContain("kimi-openai-compatible");
    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:4317/v1/desktop/projects/project_product_demo/execution-package",
      expect.objectContaining({ method: "POST" }),
    );
    const firstCall = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const requestInit = firstCall[1];
    const body = JSON.parse(String(requestInit.body));
    expect(body.user_input.local_repo_path).toBe("C:\\Users\\demo\\project");
    expect(body.user_input.product_url).toBe("https://real.example.com");
    expect(body.user_input.product_description).toBe("真实项目演示需求");
    expect(body.user_input.target_audience).toBe("中国运营团队");
  });

  it("maps project intelligence fields from local cascade state", () => {
    const workspace = createWorkspace("product_demo");
    const mapped = workspaceFromCascadeStateForTest({
      project_id: "project_pi",
      project_context: {
        id: "project_pi",
        product_url: "https://example.com",
        target_audience: "运营团队",
        inputs: workspace.inputBundle,
      },
      product_map: { id: "map_pi" },
      workflow_graph: workspace.planReview.graph,
      project_intelligence: {
        id: "pi_1",
        project_id: "project_pi",
        schema_version: "demoops.project_intelligence_pack.v1",
        source_digest_sha256: "sha256:pi",
        architecture: {
          id: "arch_1",
          project_id: "project_pi",
          summary: "架构摘要",
          route_tree: [{ id: "route_1", path: "/home" }, { id: "route_2", path: "/team" }],
          modules: [],
        },
        feature_capabilities: [{ id: "cap_1", name: "团队管理" }],
        interaction_surfaces: [{ id: "surface_1", title: "团队页" }],
        data_models: [{ id: "model_1", name: "Team" }],
      },
      script_readiness_report: {
        id: "ready_1",
        project_id: "project_pi",
        schema_version: "demoops.script_readiness_report.v1",
        can_proceed: true,
      },
      agent_graph_trace: {
        id: "trace_1",
        project_id: "project_pi",
        schema_version: "demoops.agent_graph_trace.v1",
        steps: [{ id: "trace_step_1", node_id: "route_map", tool: "RouteMapTool", status: "completed" }],
      },
    }, workspace);

    expect(mapped.projectIntelligence?.source_digest_sha256).toBe("sha256:pi");
    expect(mapped.scriptReadiness?.can_proceed).toBe(true);
    expect(mapped.agentGraphTrace?.steps?.[0]?.tool).toBe("RouteMapTool");
    expect(mapped.understanding.routesDetected).toBe(2);
    expect(mapped.understanding.featuresDetected).toBe(1);
    expect(mapped.understanding.componentsSummarized).toBe(1);
    expect(mapped.understanding.dataModelsSummarized).toBe(1);
  });

  it("builds local user input from editable workspace fields", () => {
    const workspace = updateWorkspaceInputs(createWorkspace("ai_customer_service_demo"), {
      productURL: "https://support.example.cn",
      localRepoPath: "D:\\apps\\support-agent",
      rawUserPrompt: "展示 AI 客服检索知识并转人工",
      targetAudience: "中国客服负责人",
      forbiddenPagesText: "/billing,/admin",
      forbiddenDataText: "手机号\n访问令牌",
    });

    expect(userInputFromWorkspace(workspace)).toMatchObject({
      mode: "desktop",
      product_url: "https://support.example.cn",
      local_repo_path: "D:\\apps\\support-agent",
      product_description: "展示 AI 客服检索知识并转人工",
      target_audience: "中国客服负责人",
      forbidden_pages: ["/billing", "/admin"],
      forbidden_data: ["手机号", "访问令牌"],
    });
  });

  it("maps local dev runtime health into frontend camelCase DTOs", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        ok: true,
        data: {
          profile: "dev",
          database_configured: true,
          local_data_configured: true,
          resource_manifest_loaded: false,
          llm_mode: "real",
          model_adapter_version: "domestic-llm-adapter-v1",
          sidecars: { "video-worker": false },
          model_providers: {
            kimi: {
              api_key_env: "KIMI_API_KEY",
              api_key_source_env: "KIMI_API_KEY",
              configured: true,
              base_url_configured: true,
              default_model_configured: true,
            },
          },
          model_task_routes: {
            planning: {
              provider: "kimi",
              model: "kimi-k2.7-code",
              provider_override: "CASCADE_PLANNING_PROVIDER",
              model_override: "CASCADE_PLANNING_MODEL",
            },
          },
        },
      }),
    })));

    const bridge = createLocalBridgeClient("http://127.0.0.1:4317");
    const result = await bridge.runtimeHealth();

    expect(result.ok).toBe(true);
    expect(result.data?.databaseConfigured).toBe(true);
    expect(result.data?.llmMode).toBe("real");
    expect(result.data?.modelAdapterVersion).toBe("domestic-llm-adapter-v1");
    expect(result.data?.modelProviders.kimi?.apiKeyEnv).toBe("KIMI_API_KEY");
    expect(result.data?.modelTaskRoutes.planning?.modelOverride).toBe("CASCADE_PLANNING_MODEL");
  });

  it("maps local model diagnostics into frontend camelCase DTOs", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        ok: true,
        data: [
          {
            provider: "kimi",
            task: "planning",
            model: "kimi-k2.7-code",
            adapter_version: "domestic-llm-adapter-v1",
            mode: "real",
            base_url_host: "api.moonshot.cn",
            base_url_path: "/v1",
            configured: true,
            ok: false,
            http_status: 401,
            error_class: "http_401",
            error: "provider HTTP 401: [redacted]",
            latency_ms: 120,
            checked_at: "2026-07-09T00:00:00Z",
          },
        ],
      }),
    })));

    const bridge = createLocalBridgeClient();
    const result = await bridge.modelDiagnostics();

    expect(result.ok).toBe(true);
    expect(result.data?.[0]).toMatchObject({
      provider: "kimi",
      task: "planning",
      model: "kimi-k2.7-code",
      baseURLHost: "api.moonshot.cn",
      httpStatus: 401,
      errorClass: "http_401",
      latencyMS: 120,
    });
    expect(JSON.stringify(result.data)).not.toMatch(/sk-|Authorization/i);
  });

  it("maps local execution events into runtime logs", async () => {
    const fetchMock = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        ok: true,
        data: [
          {
            id: 12,
            project_id: "project_real",
            level: "success",
            node: "CodeRead",
            message: "CodeReaderAgent 完成只读代码摘要",
            detail: "snapshots=1 files=119",
            elapsed_ms: 10021,
            created_at: "2026-07-09T10:26:22Z",
          },
        ],
      }),
    }));
    vi.stubGlobal("fetch", fetchMock);

    const bridge = createLocalBridgeClient();
    const result = await bridge.executionEvents("project_real", "10");

    expect(result.ok).toBe(true);
    expect(result.data?.[0]).toMatchObject({
      id: "12",
      level: "success",
      node: "CodeRead",
      message: "CodeReaderAgent 完成只读代码摘要",
      detail: "snapshots=1 files=119",
      elapsedMS: 10021,
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/desktop/projects/project_real/execution-events?after=10",
      expect.objectContaining({ headers: expect.objectContaining({ "Content-Type": "application/json" }) }),
    );
  });

  it("uses same-origin local bridge by default so the demo stays on port 3000", async () => {
    const fetchMock = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({
        ok: true,
        data: {
          profile: "dev",
          database_configured: true,
          local_data_configured: true,
          resource_manifest_loaded: false,
          sidecars: {},
          model_providers: {},
          model_task_routes: {},
        },
      }),
    }));
    vi.stubGlobal("fetch", fetchMock);

    const bridge = createLocalBridgeClient();
    await bridge.runtimeHealth();

    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/desktop/runtime-health",
      expect.objectContaining({ headers: expect.objectContaining({ "Content-Type": "application/json" }) }),
    );
  });
});
