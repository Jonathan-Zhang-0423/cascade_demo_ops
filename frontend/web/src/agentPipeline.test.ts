import { describe, expect, it } from "vitest";
import { agentPipelineItems, codeInvestigationQuestionsFromWorkspace, codeSummaryFromWorkspace, updateWorkspaceInputs } from "./agentPipeline";
import { createWorkspace } from "./mockWorkspace";

describe("agent pipeline helpers", () => {
  it("updates local repo path, prompt, url, audience, and safety inputs", () => {
    const workspace = createWorkspace("product_demo");

    const next = updateWorkspaceInputs(workspace, {
      productURL: "https://real.example.com",
      localRepoPath: "C:\\Users\\demo\\project",
      rawUserPrompt: "展示团队协作功能",
      targetAudience: "中国运营团队",
      forbiddenPagesText: "/billing\n/settings/api-keys",
      forbiddenDataText: "客户邮箱\nAPI Key",
    });

    expect(next.productURL).toBe("https://real.example.com");
    expect(next.targetAudience).toBe("中国运营团队");
    expect(next.inputBundle.repositories?.[0]?.local_path).toBe("C:\\Users\\demo\\project");
    expect(next.inputBundle.raw_user_prompt).toBe("展示团队协作功能");
    expect(next.planReview.forbiddenPages).toEqual(["/billing", "/settings/api-keys"]);
    expect(next.scriptDocument).toBeUndefined();
    expect(next.sourceConnections.find((source) => source.kind === "local_repo")?.status).toBe("ready");
  });

  it("summarizes code snapshots and marks CodeReader completed", () => {
    const workspace = {
      ...createWorkspace("product_demo"),
      understandingReport: {
        id: "understanding_1",
        project_id: "project_product_demo",
        schema_version: "demoops.multimodal_understanding_report.v1" as const,
        code_snapshots: [
          {
            id: "code_1",
            project_id: "project_product_demo",
            schema_version: "demoops.multimodal_understanding_report.v1",
            file_count: 4,
            frameworks: ["react", "vite"],
            routes: [{ id: "route_dashboard", path: "/dashboard" }],
            components: [{ id: "component_invite", name: "InvitePanel", kind: "ui_component" }],
            selectors: [{ kind: "css", value: "[data-testid='invite']", stability_score: 0.9 }],
            source_digest_sha256: "sha256:source",
          },
        ],
      },
    };

    const summary = codeSummaryFromWorkspace(workspace);
    const codeReader = agentPipelineItems(workspace).find((item) => item.id === "code_reader");

    expect(summary.fileCount).toBe(4);
    expect(summary.frameworks).toEqual(["react", "vite"]);
    expect(summary.routes).toBe(1);
    expect(summary.components).toBe(1);
    expect(summary.selectors).toBe(1);
    expect(summary.sourceDigest).toBe("sha256:source");
    expect(codeReader?.status).toBe("completed");
  });

  it("marks CodeReader attention when repo path exists but scan degraded", () => {
    const workspace = updateWorkspaceInputs(createWorkspace("product_demo"), { localRepoPath: "C:\\missing\\project" });
    const degraded = {
      ...workspace,
      understandingReport: {
        id: "understanding_1",
        project_id: workspace.id,
        schema_version: "demoops.multimodal_understanding_report.v1" as const,
        code_snapshots: [{ id: "code_1", project_id: workspace.id, schema_version: "demoops.multimodal_understanding_report.v1", file_count: 0 }],
      },
    };

    const codeReader = agentPipelineItems(degraded).find((item) => item.id === "code_reader");

    expect(codeSummaryFromWorkspace(degraded).degraded).toBe(true);
    expect(codeReader?.status).toBe("attention");
  });

  it("surfaces investigation questions and bounded tool chains without raw inputs", () => {
    const workspace = {
      ...createWorkspace("product_demo"),
      understandingReport: {
        id: "understanding_tools",
        project_id: "project_product_demo",
        schema_version: "demoops.multimodal_understanding_report.v1" as const,
        code_snapshots: [
          {
            id: "code_tools",
            project_id: "project_product_demo",
            schema_version: "demoops.multimodal_understanding_report.v1",
            file_count: 3,
            investigation_trace: {
              id: "trace_tools",
              mode: "tool_driven_intent_drilldown",
              questions: [
                {
                  id: "question_project_creation",
                  question: "新建项目流程在哪里实现？",
                  intent_label: "新建项目",
                  status: "answered",
                  evidence_summary: "确认创建流程入口和父级 route。",
                  tool_call_ids: ["tool_grep", "tool_find_references"],
                  confidence: 0.82,
                },
              ],
              tool_calls: [
                {
                  id: "tool_grep",
                  tool: "grep_text",
                  input_summary: "terms=create-tetris-project",
                  output_summary: "searched=12 matched=1 selected=1",
                  matched_file_count: 1,
                  selected_file_count: 1,
                  path_hashes: ["sha256:path"],
                },
                {
                  id: "tool_find_references",
                  tool: "find_references",
                  input_summary: "reference_terms=create-tetris-project",
                  output_summary: "searched=8 matched=1 selected=1",
                  matched_file_count: 1,
                  selected_file_count: 1,
                  path_hashes: ["sha256:parent"],
                  metadata: { question_id: "question_project_creation", reference_term_hashes: ["sha256:term"] },
                },
              ],
            },
          },
        ],
      },
    };

    const summary = codeSummaryFromWorkspace(workspace);
    const questions = codeInvestigationQuestionsFromWorkspace(workspace);

    expect(summary.toolBreakdown.grep_text).toBe(1);
    expect(summary.toolBreakdown.find_references).toBe(1);
    expect(questions).toHaveLength(1);
    expect(questions[0]?.toolCalls.map((call) => call.tool)).toEqual(["grep_text", "find_references"]);
    expect(questions[0]?.toolCalls.map((call) => call.summary).join("\n")).not.toContain("create-tetris-project");
  });

  it("surfaces ProjectIntelligenceGraph status and uses intelligence metrics", () => {
    const workspace = {
      ...createWorkspace("product_demo"),
      projectIntelligence: {
        id: "pi_1",
        project_id: "project_product_demo",
        schema_version: "demoops.project_intelligence_pack.v1" as const,
        source_digest_sha256: "sha256:intelligence",
        architecture: {
          id: "arch_1",
          project_id: "project_product_demo",
          frameworks: ["react"],
          route_tree: [{ id: "route_1", path: "/dashboard" }],
          modules: [{ id: "module_1", name: "web", component_refs: ["component_a", "component_b"] }],
        },
        feature_capabilities: [{ id: "cap_1", name: "团队协作", key_actions: ["inspect"] }],
        interaction_surfaces: [{ id: "surface_1", title: "工作台", stable_selectors: [{ kind: "css", value: "main" }] }],
        data_models: [{ id: "model_1", name: "Team" }],
        confidence: 0.8,
      },
      scriptReadiness: {
        id: "ready_1",
        project_id: "project_product_demo",
        schema_version: "demoops.script_readiness_report.v1" as const,
        can_proceed: true,
        warnings: [],
        blockers: [],
      },
      agentGraphTrace: {
        id: "trace_1",
        project_id: "project_product_demo",
        schema_version: "demoops.agent_graph_trace.v1" as const,
        steps: [{ id: "step_1", node_id: "repo_index", tool: "RepoIndexTool", status: "completed" }],
      },
    };

    const summary = codeSummaryFromWorkspace(workspace);
    const intelligenceItem = agentPipelineItems(workspace).find((item) => item.id === "project_intelligence");

    expect(summary.sourceDigest).toBe("sha256:intelligence");
    expect(summary.routes).toBe(1);
    expect(summary.components).toBe(2);
    expect(intelligenceItem?.status).toBe("completed");
    expect(intelligenceItem?.detail).toContain("1 个能力");
  });
});
