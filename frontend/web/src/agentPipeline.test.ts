import { describe, expect, it } from "vitest";
import { agentPipelineItems, codeSummaryFromWorkspace, updateWorkspaceInputs } from "./agentPipeline";
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
});
