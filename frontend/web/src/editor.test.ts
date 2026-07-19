import { describe, expect, it } from "vitest";
import { createEditorClient, decodeEditorBridgeResponse } from "./editor";

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

    const analysis = await client.analyzeAudio(session.session_id, imported.data!.asset_catalog.artifacts[0]!.id, { bucketMS: 100 });
    expect(analysis.data).toMatchObject({ schema_version: "demoops.audio_analysis.v1", bucket_ms: 100, has_audio: true });

    const plan = structuredClone(imported.data!.edit_plan);
    plan.shots[0]!.source_time_range_ms = [1000, 5000];
    const saved = await client.savePlan(session.session_id, imported.data!.revision, plan);
    expect(saved.data?.revision).toBe(3);

    const conflict = await client.savePlan(session.session_id, 2, plan);
    expect(conflict.ok).toBe(false);
    expect(conflict.error).toContain("版本冲突");

    const fromResult = await client.createSessionFromResultPackage("D:\\results\\recording-result.json", "D:\\results\\raw.mp4", "结果包项目");
    expect(fromResult.data?.name).toBe("结果包项目");

    const cancelled = await client.cancelRender(session.session_id, "preview");
    expect(cancelled.data?.preview.status).toBe("cancelled");

    const uploaded = await client.uploadAsset(session.session_id, new File(["fixture"], "recording.mp4", { type: "video/mp4" }));
    expect(uploaded.data?.asset_catalog.artifacts.at(-1)?.label).toBe("recording.mp4");
  });

  it("reports an old Bridge plain-text 404 without hiding the HTTP status", async () => {
    const result = await decodeEditorBridgeResponse(new Response("404 page not found\n", { status: 404 }));
    expect(result).toEqual({ ok: false, error: "HTTP 404: 404 page not found" });
  });
});
