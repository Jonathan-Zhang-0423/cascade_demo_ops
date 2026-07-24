import { describe, expect, it } from "vitest";
import type { EditorPlan, EditorSession } from "./editor";
import { compilePresentationComposition, editorCapabilities, finalRendererSupportsOverlay, frameToMilliseconds, millisecondsToFrame, overlayExportStatus } from "./presentationComposition";

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
    artifacts: [
      { id: "raw", kind: "raw_recording", uri: "file:///raw.mp4", duration_ms: 4000 },
      { id: "screenshot", kind: "step_screenshot", uri: "file:///step.png", mime_type: "image/png", metadata: { presentation_only: true } },
      { id: "narration", kind: "narration_audio", uri: "file:///narration.wav", duration_ms: 2500 },
    ],
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

  it("allows flat supported shapes but blocks preview-only 3D transforms", () => {
    expect(finalRendererSupportsOverlay({ type: "highlight_box", shape: "rectangle", scale_x: 135, scale_y: 80 })).toBe(true);
    expect(finalRendererSupportsOverlay({ type: "highlight_box", shape: "polygon" })).toBe(true);
    expect(overlayExportStatus({ type: "highlight_box", shape: "star" })).toEqual({ exportable: true });
    expect(overlayExportStatus({ type: "highlight_box", shape: "circle", tilt_x: 12 })).toEqual({ exportable: false, reason: "3D 倾斜标注" });
    expect(overlayExportStatus({ type: "highlight_box", shape: "diamond" })).toEqual({ exportable: false, reason: "highlight_box:diamond" });
  });

  it("keeps a step screenshot as a timed still without inventing source audio", () => {
    const withStill = structuredClone(plan);
    withStill.shots.push({
      id: "still", source_artifact_id: "screenshot", source_step_id: "open", presentation_kind: "still", output_duration_ms: 1500,
      purpose: "Confirm the saved state", overlays: [{ type: "caption", text: "Saved", start_ms: 0, end_ms: 1500 }],
    });
    const composition = compilePresentationComposition(session, withStill);
    expect(composition.durationInFrames).toBe(120);
    expect(composition.sequences.filter((sequence) => sequence.kind === "still")[0]).toMatchObject({
      fromFrame: 75, durationInFrames: 45, sourceArtifactId: "screenshot", sourceStepId: "open",
    });
    expect(composition.sequences.filter((sequence) => sequence.kind === "audio")).toHaveLength(1);
  });

  it("compiles confirmed narration and global captions on output time", () => {
    const narratedPlan = structuredClone(plan);
    narratedPlan.narrations = [{
      id: "intro", source_artifact_id: "narration", source_time_range_ms: [100, 1600], output_time_range_ms: [500, 2000],
      volume_percent: 100, duck_source_audio: true, duck_source_to_percent: 30, source: "user_recorded",
    }];
    narratedPlan.caption_cues = [{ id: "intro", output_range_ms: [500, 2000], text: "打开产品工作台。", source: "user_configured" }];
    const sequences = compilePresentationComposition(session, narratedPlan).sequences;
    expect(sequences.filter((sequence) => sequence.kind === "narration")[0]).toMatchObject({
      fromFrame: 15, durationInFrames: 45, sourceArtifactId: "narration", sourceStartFrame: 3, sourceEndFrame: 48,
      props: { volumePercent: 100, duckSourceAudio: true, duckSourceToPercent: 30 },
    });
    expect(sequences.filter((sequence) => sequence.id === "caption_cue_intro")[0]).toMatchObject({
      kind: "caption", fromFrame: 15, durationInFrames: 45, props: { text: "打开产品工作台。", source: "user_configured" },
    });
  });
});
