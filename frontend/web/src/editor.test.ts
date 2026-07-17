import { describe, expect, it } from "vitest";
import { createEditorClient } from "./editor";

describe("local demo editor client", () => {
  it("keeps mock editor sessions revisioned and Seedance opt-in only", async () => {
    const client = createEditorClient();
    expect(client.mode).toBe("mock");

    const created = await client.createSession("测试剪辑");
    expect(created.ok).toBe(true);
    const session = created.data!;
    expect(session.provider_capabilities[0]).toMatchObject({
      provider: "seedance",
      auto_include: false,
      requires_review: true,
      presentation_only: true,
      can_represent_business_step: false,
    });

    const imported = await client.importAsset(session.session_id, "D:\\recordings\\demo.mp4");
    expect(imported.data?.revision).toBe(2);
    expect(imported.data?.edit_plan.shots).toHaveLength(1);

    const plan = structuredClone(imported.data!.edit_plan);
    plan.shots[0]!.source_time_range_ms = [1000, 5000];
    const saved = await client.savePlan(session.session_id, imported.data!.revision, plan);
    expect(saved.data?.revision).toBe(3);

    const conflict = await client.savePlan(session.session_id, 2, plan);
    expect(conflict.ok).toBe(false);
    expect(conflict.error).toContain("版本冲突");
  });
});
