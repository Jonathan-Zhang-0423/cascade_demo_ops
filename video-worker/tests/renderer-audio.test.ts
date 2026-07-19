import { describe, expect, it } from "vitest";

import { audioVolumeExpression, validateEditPlan, type AssetTimelineCatalog, type DemoEditPlan } from "../src/renderer.js";

const catalog: AssetTimelineCatalog = {
  schema_version: "demoops.asset_timeline_catalog.v1",
  catalog_id: "catalog_audio",
  workflow_graph_id: "graph_audio",
  graph_version: 1,
  run_id: "run_audio",
  source: { generated_at: "2026-07-18T00:00:00Z" },
  constraints: {
    source_material_only: true,
    prohibit_new_image_or_video_generation: true,
    script_is_primary_storyline: true,
    allowed_edit_operations: ["trim"],
    prohibited_plan_keys: [],
  },
  timeline: { duration_ms: 2000, recording_artifact_id: "asset_audio" },
  steps: [],
  artifacts: [{
    id: "asset_audio",
    kind: "raw_recording",
    uri: "file:///tmp/audio.mp4",
    asset_role: "raw_recording",
    include_in_demo: true,
    duration_ms: 2000,
  }],
};

function plan(): DemoEditPlan {
  return {
    plan_id: "plan_audio",
    source_authority: "server_local_editor",
    model_role: "presentation_optimizer_only",
    source_material_policy: "existing_assets_only",
    script_order_policy: "preserve_required_step_order",
    locked_fields: ["source_authority", "model_role", "source_material_policy", "script_order_policy", "source_artifact_id", "source_step_id", "source_time_range_ms", "required_step_order"],
    model_editable_fields: [],
    shots: [{ id: "shot_audio", source_artifact_id: "asset_audio", source_time_range_ms: [0, 2000], purpose: "Audio test" }],
  };
}

describe("editor audio policy", () => {
  it("keeps legacy plans valid with source audio defaults", () => {
    expect(validateEditPlan({ catalog, edit_plan: plan() }).valid).toBe(true);
  });

  it("rejects invalid modes and out-of-range volume", () => {
    const invalid = plan();
    invalid.audio = { mode: "invalid" as "source", volume_percent: 250 };
    const report = validateEditPlan({ catalog, edit_plan: invalid });
    expect(report.valid).toBe(false);
    expect(report.errors.map((item) => item.code)).toEqual(expect.arrayContaining(["invalid_audio_mode", "invalid_audio_volume"]));
  });

  it("accepts ordered audio split points and rejects invalid boundaries", () => {
    const valid = plan();
    valid.audio = { mode: "source", volume_percent: 100, split_points_ms: [500, 1500], segment_settings: [{ start_ms: 500, end_ms: 1500, mode: "mute", volume_percent: 100 }] };
    expect(validateEditPlan({ catalog, edit_plan: valid }).valid).toBe(true);

    const invalid = plan();
    invalid.audio = { mode: "source", volume_percent: 100, split_points_ms: [1500, 1500, 2000] };
    expect(validateEditPlan({ catalog, edit_plan: invalid }).errors.map((item) => item.code)).toEqual(expect.arrayContaining(["audio_split_points_not_ordered", "audio_split_point_outside_timeline"]));
  });

  it("rejects unaligned, overlapping, or invalid audio segment settings", () => {
    const invalid = plan();
    invalid.audio = {
      mode: "source",
      volume_percent: 100,
      split_points_ms: [500, 1500],
      segment_settings: [
        { start_ms: 400, end_ms: 1500, mode: "invalid" as "source", volume_percent: 250 },
        { start_ms: 500, end_ms: 1500, mode: "mute", volume_percent: 100 },
      ],
    };
    expect(validateEditPlan({ catalog, edit_plan: invalid }).errors.map((item) => item.code)).toEqual(expect.arrayContaining([
      "audio_segment_not_aligned",
      "audio_segments_overlap",
      "invalid_audio_segment_mode",
      "invalid_audio_segment_volume",
    ]));
  });

  it("maps output-time audio overrides into each FFmpeg source segment", () => {
    const audio = {
      mode: "source" as const,
      volume_percent: 100,
      split_points_ms: [1000, 3000],
      segment_settings: [
        { start_ms: 1000, end_ms: 3000, mode: "mute" as const, volume_percent: 100 },
        { start_ms: 3000, end_ms: 4000, mode: "source" as const, volume_percent: 60 },
      ],
    };
    expect(audioVolumeExpression(audio, 0, 2000)).toEqual({ expression: "if(between(t,1.000,2.000),0.00,1.00)", usesSource: true });
    expect(audioVolumeExpression(audio, 2000, 2000)).toEqual({ expression: "if(between(t,0.000,1.000),0.00,if(between(t,1.000,2.000),0.60,1.00))", usesSource: true });
    expect(audioVolumeExpression(audio, 0, 2000, 500)).toEqual({ expression: "if(between(t,1.500,2.500),0.00,1.00)", usesSource: true });
    expect(audioVolumeExpression({ mode: "mute", volume_percent: 100 }, 0, 2000)).toEqual({ expression: "0.00", usesSource: false });
  });
});
