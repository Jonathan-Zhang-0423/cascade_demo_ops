import { describe, expect, it } from "vitest";
import type { EditorPlan, EditorSession } from "./editor";
import { compilePresentationComposition, editorCapabilities, frameToMilliseconds, millisecondsToFrame } from "./presentationComposition";

const plan: EditorPlan = {
  plan_id: "plan",
  source_authority: "server_local_editor",
  model_role: "presentation_optimizer_only",
  source_material_policy: "existing_assets_only",
  script_order_policy: "preserve_required_step_order",
  locked_fields: [],
  model_editable_fields: [],
  audio: { mode: "source", volume_percent: 80 },
  shots: [{
    id: "shot",
    source_artifact_id: "raw",
    source_step_id: "open",
    source_time_range_ms: [1000, 3500],
    purpose: "Open the product",
    operations: [{ type: "trim" }],
    overlays: [{ type: "caption", text: "Open", start_ms: 500, end_ms: 1500 }],
  }],
};

const session: EditorSession = {
  schema_version: "demoops.editor_session.v1",
  session_id: "edit",
  name: "test",
  mode: "demo_safe",
  status: "editing",
  revision: 1,
  asset_catalog: {
    catalog_id: "catalog",
    timeline: { duration_ms: 4000 },
    artifacts: [{ id: "raw", kind: "raw_recording", uri: "file:///raw.mp4", duration_ms: 4000 }],
  },
  edit_plan: plan,
  preview_profile: { width: 1280, height: 720, fps: 30, format: "mp4" },
  final_profile: { width: 1920, height: 1080, fps: 30, format: "mp4" },
  preview: { status: "not_started" },
  final_render: { status: "not_started" },
  provider_capabilities: [],
};

describe("presentation composition", () => {
  it("compiles plan milliseconds into deterministic frame sequences", () => {
    const composition = compilePresentationComposition(session, plan);
    expect(composition).toMatchObject({ fps: 30, width: 1920, height: 1080, durationInFrames: 75 });
    expect(composition.sequences.map((sequence) => [sequence.kind, sequence.fromFrame, sequence.durationInFrames])).toEqual([
      ["video", 0, 75],
      ["audio", 0, 75],
      ["caption", 15, 30],
    ]);
    expect(composition.sequences[0]).toMatchObject({ sourceStartFrame: 30, sourceEndFrame: 105, sourceStepId: "open" });
  });

  it("uses one frame conversion rule and keeps unfinished features disabled", () => {
    expect(millisecondsToFrame(1500, 30)).toBe(45);
    expect(frameToMilliseconds(45, 30)).toBe(1500);
    expect(editorCapabilities.caption).toMatchObject({ enabled: true, finalRenderer: true });
    expect(editorCapabilities.zoomPan).toMatchObject({ enabled: false, finalRenderer: false });
  });

  it("compiles declared audio cuts into independent preview sequences", () => {
    const splitPlan = structuredClone(plan);
    splitPlan.audio = { mode: "source", volume_percent: 80, split_points_ms: [1000], segment_settings: [{ start_ms: 1000, end_ms: 2500, mode: "mute", volume_percent: 80 }] };
    const audio = compilePresentationComposition(session, splitPlan).sequences.filter((sequence) => sequence.kind === "audio");
    expect(audio.map((sequence) => [sequence.fromFrame, sequence.durationInFrames, sequence.sourceStartFrame, sequence.sourceEndFrame])).toEqual([
      [0, 30, 30, 60],
      [30, 45, 60, 105],
    ]);
    expect(audio.map((sequence) => sequence.props)).toEqual([
      { mode: "source", volumePercent: 80 },
      { mode: "mute", volumePercent: 80 },
    ]);
  });
});
