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

  it("exposes the Browser Agent fixed acceptance gate separately from App packages", async () => {
    const bridge = createMockBridgeClient();
    const result = await bridge.browserAgentAcceptance();

    expect(result.ok).toBe(true);
    expect(result.data?.report?.runtime).toBe("browser-agent-outline-v1");
    expect(result.data?.report?.scenarios).toHaveLength(5);
    expect(result.data?.report?.scenarios.find((item) => item.id === "required_validation_failure")?.action_executed).toBe(true);
    expect(result.data?.report?.scenarios.find((item) => item.id === "locator_missing")?.action_executed).toBe(false);
  });

  it("builds execution package preview without full source or raw secrets", async () => {
    const bridge = createMockBridgeClient();
    const workspace = createWorkspace("product_demo");
    const result = await bridge.buildExecutionPackagePreview(workspace);
    const payload = JSON.stringify(result.data);

    expect(result.ok).toBe(true);
    expect(result.data?.executableScriptBundle?.script_manifest.runtime).toBe("browser-agent-outline-v1");
    expect(result.data?.executableScriptBundle?.playwright_script.inline_source).toBeUndefined();
    expect(result.data?.executableScriptBundle?.stage_approval_plan?.stages.length).toBeGreaterThan(0);
    expect(result.data?.executableScriptBundle?.script_outline?.stages.length).toBeGreaterThan(0);
    expect(result.data?.executableScriptBundle?.agent_prompt_policy?.immutable_fields.length).toBeGreaterThan(0);
    expect(payload).not.toContain("function submitPayment");
    expect(payload).not.toContain("runCascadeRecording");
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
    expect(repaired.data?.executableScriptBundle?.script_manifest.runtime).toBe("browser-agent-outline-v1");
    expect(repaired.data?.scriptMarkdown).toContain("本次修复说明");
  });

  it("keeps checksum ack separate from the mock human review", async () => {
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
	const reviewed = await bridge.reviewResult(acked.data ?? completed.data ?? packageResult.data, "approved", "成品通过");

    expect(init.data?.supportedCryptoSuites).toContain("aes-256-gcm");
    expect(upload.data?.status).toBe("queued");
    expect(running.data?.cloudRun.stageHistory?.some((stage) => stage.id === "script_validation" && stage.status === "completed")).toBe(true);
    expect(completed.data?.stage).toBe("result_review");
    expect(completed.data?.cloudRun.stageHistory?.find((stage) => stage.id === "result_returned")?.status).toBe("completed");
    expect(resultPackage.data?.delivery?.asset_refs?.[0]?.role).toBe("final_demo_video");
    expect(resultPackage.data?.delivery?.asset_refs?.every((artifact) => artifact.encrypted && artifact.sensitive)).toBe(true);
	expect(acked.data?.assets.every((asset) => asset.status !== "approved")).toBe(true);
	expect(reviewed.data?.assets.every((asset) => asset.status === "approved")).toBe(true);
  });

  it("runs local split cloud lifecycle through real bridge methods", async () => {
    const workspace = createWorkspace("product_demo");
    const build = {
      org_id: "org_desktop",
      project_id: workspace.id,
      package: { package_id: "pkg_split_real" },
      envelope: { crypto: { payload_digest_sha256: "sha256:payload-split" } },
      payload_ref: { kind: "inline", sha256: "sha256:payload-split", encrypted: true, sensitive: true },
    };
    const resultPackage = {
      result_id: "result_split_real",
      source_package_id: "pkg_split_real",
      cloud_job_id: "job_split_real",
      schema_version: "demoops.recording_result_package.v1",
      status: "generated",
      verification_report: { reproducibility_match: true, pass_rate: 1 },
      delivery: {
        result_package_ref: {
          id: "artifact_result_split",
          kind: "result_package",
          uri: "artifact://result.json",
          sha256: "sha256:result",
          encrypted: true,
          sensitive: true,
        },
        asset_refs: [{
          id: "artifact_video_split",
          role: "final_demo_video",
          kind: "video",
          uri: "artifact://video.webm",
          mime_type: "video/webm",
          sha256: "sha256:video",
          encrypted: true,
          sensitive: true,
        }],
        ack_required: true,
      },
      created_at: "2026-07-14T00:00:00Z",
    };
    const fetchMock = vi.fn(async (url: string) => {
      if (url.includes("/cloud/init")) {
        return bridgeJSON({
          build,
          init: {
            upload_id: "upload_split_real",
            server_public_key_id: "server-key",
            supported_crypto_suites: ["aes-256-gcm"],
            cascade_execution_ips: ["203.0.113.10"],
          },
        });
      }
      if (url.includes("/cloud/upload")) {
        return bridgeJSON({
          build,
          upload: {
            exchange_package_id: "xpkg_split_real",
            cloud_job_id: "job_split_real",
            status: "running",
          },
        });
      }
      if (url.includes("/cloud/status")) {
        return bridgeJSON({
          exchange_package_id: "xpkg_split_real",
          cloud_job_id: "job_split_real",
          status: "completed",
          stage: "completed",
          message: "录制完成",
          progress_percent: 100,
          result_package_id: "result_split_real",
          stage_history: [
            { stage: "accepted", status: "completed", message: "已接收", progress_percent: 20, updated_at: "2026-07-14T00:00:01Z" },
			{ stage: "validating_pre_execution", status: "completed", message: "执行前校验通过", progress_percent: 36, updated_at: "2026-07-14T00:00:03Z" },
			{ stage: "running_browser_agent", status: "completed", message: "浏览器智能执行完成", progress_percent: 68, updated_at: "2026-07-14T00:00:10Z" },
			{ stage: "validating_runtime_stage", status: "completed", message: "步骤结果校验完成", progress_percent: 82, updated_at: "2026-07-14T00:00:12Z" },
			{ stage: "validating_post_execution", status: "completed", message: "执行后复核完成", progress_percent: 95, updated_at: "2026-07-14T00:00:16Z" },
            { stage: "completed", status: "completed", message: "结果返回", progress_percent: 100, updated_at: "2026-07-14T00:00:20Z" },
          ],
        });
      }
	  if (url.includes("/cloud/result")) {
		return bridgeJSON(resultPackage);
	  }
	  if (url.includes("/cloud/deliverable/download")) {
		return bridgeJSON({ artifact_id: "artifact_video_split", local_path: "C:\\DemoOps\\video.webm", sha256: "sha256:video", expected_sha256: "sha256:video", checksum_verified: true });
	  }
	  if (url.includes("/cloud/ack")) {
		return bridgeJSON({ result_package_id: "result_split_real", status: "acked", delivery_status: "acked" });
	  }
	  if (url.includes("/cloud/review")) {
		return bridgeJSON({ review_id: "review_split_real", decision: "approved", summary: "成品通过", reviewed_at: "2026-07-14T00:01:00Z" });
	  }
      throw new Error(`unexpected URL ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock);

    const bridge = createLocalBridgeClient("http://127.0.0.1:4317");
    const uploaded = await bridge.approveAndUploadPackage(workspace);
    const completed = await bridge.pollCloudRun(uploaded.data ?? workspace);
	const acked = await bridge.ackResultPackage(completed.data ?? workspace);
	const reviewed = await bridge.reviewResult(acked.data ?? completed.data ?? workspace, "approved", "成品通过");

    expect(uploaded.ok).toBe(true);
    expect(uploaded.data?.cloudRun.exchangePackageID).toBe("xpkg_split_real");
    expect(completed.data?.stage).toBe("result_review");
    expect(completed.data?.cloudRun.stageHistory?.find((stage) => stage.id === "script_validation")?.status).toBe("completed");
	expect(completed.data?.cloudRun.stageHistory?.find((stage) => stage.id === "browser_execution")?.summary).toBe("步骤结果校验完成");
	expect(completed.data?.cloudRun.stageHistory?.find((stage) => stage.id === "video_rendering")?.summary).toBe("执行后复核完成");
    expect(completed.data?.assets[0]?.assetID).toBe("artifact_video_split");
	expect(acked.data?.assets[0]?.status).not.toBe("approved");
	expect(reviewed.data?.assets[0]?.status).toBe("approved");
    expect(fetchMock.mock.calls.map((call) => String(call[0]))).toEqual([
      "http://127.0.0.1:4317/v1/desktop/projects/project_product_demo/cloud/init",
      "http://127.0.0.1:4317/v1/desktop/projects/project_product_demo/cloud/upload",
      "http://127.0.0.1:4317/v1/desktop/projects/project_product_demo/cloud/status?org_id=org_desktop&exchange_package_id=xpkg_split_real",
	  "http://127.0.0.1:4317/v1/desktop/projects/project_product_demo/cloud/result?org_id=org_desktop&result_package_id=result_split_real",
	  "http://127.0.0.1:4317/v1/desktop/projects/project_product_demo/cloud/deliverable/download",
	  "http://127.0.0.1:4317/v1/desktop/projects/project_product_demo/cloud/ack",
	  "http://127.0.0.1:4317/v1/desktop/projects/project_product_demo/cloud/review",
	]);
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

  it("passes GitHub repository URL alongside local path to the local bridge", async () => {
    const workspace = updateWorkspaceInputs(createWorkspace("product_demo"), {
      productURL: "https://real.example.com",
      localRepoPath: "C:\\Users\\demo\\project",
      gitRepoURL: "https://github.com/acme/demo-app",
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
            git_repo_url: "https://github.com/acme/demo-app",
            local_repo_path: "C:\\Users\\demo\\project",
            inputs: workspace.inputBundle,
          },
        },
      }),
    }));
    vi.stubGlobal("fetch", fetchMock);

    const bridge = createLocalBridgeClient("http://127.0.0.1:4317");
    const result = await bridge.buildExecutionPackagePreview(workspace);

    expect(result.ok).toBe(true);
    const firstCall = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const body = JSON.parse(String(firstCall[1].body));
    expect(body.user_input.local_repo_path).toBe("C:\\Users\\demo\\project");
    expect(body.user_input.git_repo_url).toBe("https://github.com/acme/demo-app");
    expect(result.data?.sourceConnections.find((source) => source.kind === "github_repo")?.status).toBe("ready");
    expect(result.data?.sourceConnections.find((source) => source.kind === "local_repo")?.status).toBe("ready");
  });

  it("runs local product preparation and stops for independent human approval", async () => {
    const workspace = updateWorkspaceInputs(createWorkspace("product_demo"), {
      productURL: "https://cascadeai.cn",
      localRepoPath: "C:\\Users\\CascadeAI\\Desktop\\CascadeAI\\Cascade",
      rawUserPrompt: "展示 Cascade 从项目理解到自动录制的完整流程",
      targetAudience: "中国产品运营团队",
    });
    const preparedState = {
      project_id: "project_cloud_real",
      current_node: "HumanApprove",
      status: "awaiting_human_approval",
      project_context: {
        id: "project_cloud_real",
        product_url: "https://cascadeai.cn",
        product_description: "展示 Cascade 从项目理解到自动录制的完整流程",
        target_audience: "中国产品运营团队",
        inputs: workspace.inputBundle,
      },
      workflow_graph: workspace.planReview.graph,
      script_markdown: "# 产品实战思路文档\n\n自动生成并上传服务器录制。",
      executable_script_bundle: {
        ...workspace.executableScriptBundle,
        id: "bundle_cloud_real",
        project_id: "project_cloud_real",
        reproducibility: {
          ...workspace.executableScriptBundle?.reproducibility,
          graph_hash_sha256: "sha256:graph-cloud",
          plan_hash_sha256: "sha256:plan-cloud",
          script_hash_sha256: "sha256:script-cloud",
          markdown_hash_sha256: "sha256:markdown-cloud",
          bundle_hash_sha256: "sha256:bundle-cloud",
        },
      },
    };
    const build = {
      org_id: "org_desktop",
      project_id: "project_cloud_real",
      package: { package_id: "pkg_bundle_cloud_real" },
      envelope: { crypto: { payload_digest_sha256: "sha256:payload-cloud" } },
      payload_ref: { kind: "inline", sha256: "sha256:payload-cloud", encrypted: true, sensitive: true },
    };
    const init = {
      upload_id: "upload_cloud_real",
      server_public_key_id: "mock-kms-202607",
      supported_crypto_suites: ["aes-256-gcm"],
      cascade_execution_ips: ["203.0.113.10"],
    };
    const upload = {
      exchange_package_id: "xpkg_cloud_real",
      cloud_job_id: "job_cloud_real",
      status: "running",
    };
    const status = {
      exchange_package_id: "xpkg_cloud_real",
      cloud_job_id: "job_cloud_real",
      status: "completed",
      stage: "completed",
      message: "录制完成，结果包已生成。",
      progress_percent: 100,
      result_package_id: "result_cloud_real",
      stage_history: [
        { stage: "accepted", status: "completed", message: "执行包已接收", progress_percent: 20, updated_at: "2026-07-13T10:00:00Z" },
        { stage: "validating", status: "completed", message: "脚本校验通过", progress_percent: 45, updated_at: "2026-07-13T10:00:04Z" },
        { stage: "running_script", status: "completed", message: "浏览器脚本执行完成", progress_percent: 80, updated_at: "2026-07-13T10:00:20Z" },
        { stage: "completed", status: "completed", message: "结果包返回", progress_percent: 100, updated_at: "2026-07-13T10:00:30Z" },
      ],
      result_summary: {
        result_id: "result_cloud_real",
        demo_video_count: 1,
        deliverables: [{ id: "artifact_video_real", kind: "video", role: "final_demo_video", uri: "artifact://video.webm", sensitive: true }],
      },
    };
    const resultPackage = {
      result_id: "result_cloud_real",
      source_package_id: "pkg_bundle_cloud_real",
      cloud_job_id: "job_cloud_real",
      schema_version: "demoops.recording_result_package.v1",
      status: "generated",
      verification_report: { reproducibility_match: true, pass_rate: 1 },
      delivery: {
        result_package_ref: {
          id: "artifact_result_real",
          kind: "result_package",
          uri: "artifact://result.json",
          sha256: "sha256:result",
          encrypted: true,
          sensitive: true,
        },
        asset_refs: [{
          id: "artifact_video_real",
          role: "final_demo_video",
          kind: "video",
          uri: "artifact://video.webm",
          mime_type: "video/webm",
          sha256: "sha256:video",
          encrypted: true,
          sensitive: true,
        }],
        ack_required: true,
      },
      created_at: "2026-07-13T10:00:31Z",
    };
    const fetchMock = vi.fn(async (input: unknown) => {
      const url = String(input);
      let data: unknown = {};
      if (url.includes("/product-run/prepare")) {
        data = { state: preparedState, build };
      } else if (url.includes("/cloud/init")) {
        data = { build, init };
      } else if (url.includes("/cloud/upload")) {
        data = { build, upload };
      } else if (url.includes("/cloud/status")) {
        data = status;
      } else if (url.includes("/cloud/result")) {
        data = resultPackage;
      } else if (url.includes("/cloud/ack")) {
        data = { result_package_id: "result_cloud_real", status: "acked", delivery_status: "acked" };
      }
      return {
        ok: true,
        status: 200,
        json: async () => ({ ok: true, data }),
      };
    });
    vi.stubGlobal("fetch", fetchMock);

    const bridge = createLocalBridgeClient("http://127.0.0.1:4317");
    const result = await bridge.runProductLifecycle(workspace);

    expect(result.ok).toBe(true);
	  expect(result.data?.stage).toBe("package_approval");
	  expect(result.data?.status).toBe("awaiting_approval");
	  expect(result.data?.cloudRun.status).toBe("not_uploaded");
    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:4317/v1/desktop/projects/project_product_demo/product-run/prepare",
      expect.objectContaining({ method: "POST" }),
    );
	  expect(fetchMock.mock.calls.some((call) => String(call[0]).includes("/cloud/init"))).toBe(false);
    expect(fetchMock.mock.calls.map((call) => String(call[0])).some((url) => url.includes("/cloud-lifecycle"))).toBe(false);
    const firstCall = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    const body = JSON.parse(String(firstCall[1].body));
    expect(body).toMatchObject({
      org_id: "org_desktop",
    });
    expect(body.user_input).toMatchObject({
      product_url: "https://cascadeai.cn",
      local_repo_path: "C:\\Users\\CascadeAI\\Desktop\\CascadeAI\\Cascade",
      product_description: "展示 Cascade 从项目理解到自动录制的完整流程",
      target_audience: "中国产品运营团队",
    });
    expect(JSON.stringify(body)).not.toMatch(/0{6}|Authorization|sk-/i);
  });

  it("keeps the private GitHub token inside the local credential route", async () => {
    const token = "github_pat_transient_test_value";
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      expect(url).toBe("http://127.0.0.1:4317/v1/desktop/github-credential");
      if (method === "POST") {
        expect(init?.body).toBe(JSON.stringify({ token }));
      } else {
        expect(String(init?.body ?? "")).not.toContain(token);
      }
      return bridgeJSON({ configured: method !== "DELETE" });
    });
    vi.stubGlobal("fetch", fetchMock);

    const bridge = createLocalBridgeClient("http://127.0.0.1:4317");
    const stored = await bridge.storeGitHubToken(token);
    const status = await bridge.githubCredentialStatus();
    const deleted = await bridge.deleteGitHubToken();

    expect(stored.data).toEqual({ configured: true });
    expect(status.data).toEqual({ configured: true });
    expect(deleted.data).toEqual({ configured: false });
    expect(JSON.stringify([stored, status, deleted])).not.toContain(token);
    expect(fetchMock.mock.calls.map((call) => (call[1] as RequestInit | undefined)?.method ?? "GET")).toEqual(["POST", "GET", "DELETE"]);
  });

  it("never includes a stored GitHub token in workspace or package JSON", async () => {
    const token = "github_pat_workspace_leak_test";
    const bridge = createMockBridgeClient();
    await bridge.storeGitHubToken(token);
    const workspace = createWorkspace("product_demo");
    const result = await bridge.buildExecutionPackagePreview(workspace);

    expect(result.ok).toBe(true);
    expect(JSON.stringify(result.data)).not.toContain(token);
    expect(await bridge.githubCredentialStatus()).toEqual({ ok: true, data: { configured: true } });
    expect(await bridge.deleteGitHubToken()).toEqual({ ok: true, data: { configured: false } });
  });

  it("maps verified desktop update status without exposing local paths", async () => {
    const fetchMock = vi.fn(async () => bridgeJSON({
      configured: true,
      current_version: "1.0.0",
      available_version: "1.1.0",
      channel: "stable",
      release_notes: "安全与录制稳定性更新",
      artifact_file_name: "CascadeDemoOps-1.1.0-setup.exe",
      size_bytes: 123456,
      update_available: true,
      install_ready: true,
    }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await createLocalBridgeClient().checkDesktopUpdate();

    expect(result.data).toMatchObject({ currentVersion: "1.0.0", availableVersion: "1.1.0", updateAvailable: true, installReady: true });
    expect(JSON.stringify(result.data)).not.toMatch(/manifest_url|public_key|C:\\/i);
    expect(fetchMock).toHaveBeenCalledWith(
      "/v1/desktop/update/check",
      expect.objectContaining({ method: "POST", headers: expect.objectContaining({ "Content-Type": "application/json" }) }),
    );
  });

  it("does not start resumable cloud SSE before approval", async () => {
    const workspace = createWorkspace("product_demo");
    const build = {
      org_id: "org_desktop",
      project_id: workspace.id,
      package: { package_id: "pkg_stream" },
      envelope: { crypto: { payload_digest_sha256: "sha256:stream" } },
      payload_ref: { kind: "inline", sha256: "sha256:stream", encrypted: true, sensitive: true },
    };
    const completeStatus = {
      exchange_package_id: "xpkg_stream",
      cloud_job_id: "job_stream",
      status: "completed",
      stage: "completed",
      progress_percent: 100,
      result_package_id: "result_stream",
      stage_history: [
        { event_id: "xpkg_stream:1", stage: "recording", status: "running", message: "录制中", progress_percent: 65 },
        { event_id: "xpkg_stream:2", stage: "completed", status: "completed", message: "完成", progress_percent: 100 },
      ],
    };
    const resultPackage = {
      result_id: "result_stream",
      source_package_id: "pkg_stream",
      cloud_job_id: "job_stream",
      schema_version: "demoops.recording_result_package.v1",
      status: "generated",
      verification_report: { reproducibility_match: true, pass_rate: 1 },
      delivery: { result_package_ref: { id: "result_manifest", kind: "result_package", uri: "artifact://result.json", sha256: "sha256:result", encrypted: true, sensitive: true }, asset_refs: [] },
      created_at: "2026-07-27T00:00:00Z",
    };
    const progress: string[] = [];
    let streamAttempt = 0;
    const fetchMock = vi.fn(async (input: unknown, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/product-run/prepare")) return bridgeJSON({ state: { project_id: workspace.id }, build });
      if (url.includes("/cloud/init")) return bridgeJSON({ build, init: { upload_id: "upload_stream", server_public_key_id: "server-key", cascade_execution_ips: [] } });
      if (url.includes("/cloud/upload")) return bridgeJSON({ build, upload: { exchange_package_id: "xpkg_stream", cloud_job_id: "job_stream", status: "running" } });
      if (url.includes("/cloud/events")) {
        streamAttempt += 1;
        if (streamAttempt === 2) expect(new Headers(init?.headers).get("Last-Event-ID")).toBe("xpkg_stream:1");
        const body = streamAttempt === 1
          ? 'id: xpkg_stream:1\nevent: stage\ndata: {"event_id":"xpkg_stream:1","stage":"recording","status":"running","message":"录制中","progress_percent":65}\n\n'
          : `event: complete\ndata: ${JSON.stringify(completeStatus)}\n\n`;
        return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } });
      }
      if (url.includes("/cloud/status")) return bridgeJSON(completeStatus);
      if (url.includes("/cloud/result")) return bridgeJSON(resultPackage);
      throw new Error(`unexpected URL ${url}`);
    });
    vi.stubGlobal("fetch", fetchMock);

    const result = await createLocalBridgeClient("http://127.0.0.1:4317").runProductLifecycle(workspace, {
      onCloudStatus: (next) => progress.push(next.cloudRun.stage ?? ""),
    });

    expect(result.ok).toBe(true);
	  expect(progress).toEqual([]);
	  expect(result.data?.stage).toBe("package_approval");
	  expect(fetchMock.mock.calls.filter((call) => String(call[0]).includes("/cloud/events"))).toHaveLength(0);
	  expect(fetchMock.mock.calls.filter((call) => String(call[0]).includes("/cloud/status"))).toHaveLength(0);
  });

  it("keeps prepared script package without contacting cloud auth before approval", async () => {
    const workspace = updateWorkspaceInputs(createWorkspace("product_demo"), {
      productURL: "https://cascadeai.cn",
      localRepoPath: "C:\\Users\\CascadeAI\\Desktop\\CascadeAI\\Cascade",
      rawUserPrompt: "演示创建项目和查看工作台",
      targetAudience: "中国产品运营团队",
    });
    const build = {
      org_id: "org_desktop",
      project_id: "project_cloud_auth",
      package: { package_id: "pkg_ready_local" },
      envelope: { crypto: { payload_digest_sha256: "sha256:payload-ready" } },
      payload_ref: { kind: "inline", sha256: "sha256:payload-ready", encrypted: true, sensitive: true },
    };
    const fetchMock = vi.fn(async (input: unknown) => {
      const url = String(input);
      if (url.includes("/cloud/init")) {
        return {
          ok: false,
          status: 400,
          json: async () => ({
            ok: false,
            error: "服务器尚未部署 App installation 自动配对接口，无法在无手动 token 的情况下上传执行包。",
            error_info: {
              code: "cloud_auth_unavailable",
              message: "服务器尚未部署 App installation 自动配对接口，无法在无手动 token 的情况下上传执行包。",
              correlation_id: "bridge_test",
              retryable: false,
            },
          }),
        };
      }
      return {
        ok: true,
        status: 200,
        json: async () => ({
          ok: true,
          data: {
            state: {
              project_id: "project_cloud_auth",
              current_node: "HumanApprove",
              status: "awaiting_human_approval",
              project_context: {
                id: "project_cloud_auth",
                product_url: "https://cascadeai.cn",
                product_description: "演示创建项目和查看工作台",
                target_audience: "中国产品运营团队",
                inputs: workspace.inputBundle,
              },
              workflow_graph: workspace.planReview.graph,
              script_markdown: "# 本地脚本已就绪",
              executable_script_bundle: workspace.executableScriptBundle,
            },
            build,
          },
        }),
      };
    });
    vi.stubGlobal("fetch", fetchMock);

    const bridge = createLocalBridgeClient("http://127.0.0.1:4317");
    const result = await bridge.runProductLifecycle(workspace);

    expect(result.ok).toBe(true);
    expect(result.data?.stage).toBe("package_approval");
	  expect(result.data?.cloudRun.status).toBe("not_uploaded");
	  expect(result.data?.cloudRun.stage).toBe("local_generated");
    expect(result.data?.packagePreview.packageID).toBe("pkg_ready_local");
    const calledURLs = fetchMock.mock.calls.map((call) => String(call[0]));
	  expect(calledURLs.some((url) => url.includes("/product-run/prepare"))).toBe(true);
	  expect(calledURLs.some((url) => url.includes("/cloud/init"))).toBe(false);
    expect(calledURLs.some((url) => url.includes("/cloud/upload"))).toBe(false);
    expect(calledURLs.some((url) => url.includes("/cloud-lifecycle"))).toBe(false);
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

  it("adds demo credentials only to transient bridge input", () => {
    const workspace = updateWorkspaceInputs(createWorkspace("product_demo"), {
      productURL: "https://demo.example.cn",
      localRepoPath: "D:\\apps\\demo",
      rawUserPrompt: "展示登录后的核心工作流",
    });
    const input = userInputFromWorkspace(workspace, {
      demoCredentials: {
        username: "demo-user@example.cn",
        password: "demo-password-123",
      },
    });

    expect(input).toMatchObject({
      demo_username: "demo-user@example.cn",
      demo_password: "demo-password-123",
    });
    const workspaceJSON = JSON.stringify(workspace);
    expect(workspaceJSON).not.toContain("demo-user@example.cn");
    expect(workspaceJSON).not.toContain("demo-password-123");
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
          app_capabilities: {
            demo_asset_generation_console: true,
            video_editor: true,
            local_package_generation: true,
            stage_plan_review: true,
            execution_package_approval: true,
            approved_package_upload: true,
            result_video_download: true,
            error_report_download: true,
            server_recording_required: true,
            local_recording_execution: false,
            local_recording_scope: "dev_and_test_compatibility_only",
            video_worker_role: "editor_media_helper_and_dev_compatibility_runtime",
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
    expect(result.data?.appCapabilities?.demoAssetGenerationConsole).toBe(true);
    expect(result.data?.appCapabilities?.videoEditor).toBe(true);
    expect(result.data?.appCapabilities?.serverRecordingRequired).toBe(true);
    expect(result.data?.appCapabilities?.localRecordingExecution).toBe(false);
    expect(result.data?.appCapabilities?.videoWorkerRole).toBe("editor_media_helper_and_dev_compatibility_runtime");
  });

  it("reports non-json local bridge responses with status and snippet", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ({
      ok: false,
      status: 404,
      statusText: "Not Found",
      text: async () => "404 page not found",
    })));

    const bridge = createLocalBridgeClient();
    const result = await bridge.runtimeHealth();

    expect(result.ok).toBe(false);
    expect(result.error).toContain("非 JSON 响应");
    expect(result.error).toContain("HTTP 404 Not Found");
    expect(result.error).toContain("404 page not found");
  });

  it("sanitizes local bridge LLM JSON parse errors for runtime UI", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ({
      ok: false,
      status: 400,
      statusText: "Bad Request",
      text: async () => JSON.stringify({
        ok: false,
        error: "llm JSON parse failed: json: cannot unmarshal array into Go value of type agents.requirementLLMOutput",
      }),
    })));

    const bridge = createLocalBridgeClient();
    const result = await bridge.runtimeHealth();

    expect(result.ok).toBe(false);
    expect(result.error).toContain("模型返回的 JSON 结构不稳定");
    expect(result.error).not.toContain("cannot unmarshal");
    expect(result.error).not.toContain("agents.requirementLLMOutput");
  });

  it("formats structured bridge error details for field-level fixes", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => ({
      ok: false,
      status: 400,
      statusText: "Bad Request",
      text: async () => JSON.stringify({
        ok: false,
        error: "execution package has no real business action",
        error_info: {
          code: "preflight_failed",
          message: "execution package has no real business action",
          correlation_id: "bridge_test",
          retryable: false,
          details: [{
            field: "payload.executable_script_bundle.plan_json.steps",
            reason: "blocking",
            message: "business action missing",
            hint: "Add click/fill/select/upload/api_call.",
          }],
        },
      }),
    })));

    const bridge = createLocalBridgeClient();
    const result = await bridge.initExecutionPackageUpload(createWorkspace("product_demo"));

    expect(result.ok).toBe(false);
    expect(result.error).toContain("错误码: preflight_failed");
    expect(result.error).toContain("字段: payload.executable_script_bundle.plan_json.steps");
    expect(result.error).toContain("建议: Add click/fill/select/upload/api_call.");
    expect(result.errorInfo?.code).toBe("preflight_failed");
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

function bridgeJSON(data: unknown) {
  return {
    ok: true,
    status: 200,
    text: async () => JSON.stringify({ ok: true, data }),
  };
}
