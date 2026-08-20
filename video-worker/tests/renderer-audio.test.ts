import { spawnSync } from "node:child_process";
import { mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { describe, expect, it } from "vitest";

import { audioVolumeExpression, globalCaptionCuesForOutputWindow, render, validateEditPlan, type AssetTimelineCatalog, type DemoEditPlan } from "../src/renderer.js";

const ffmpegPath = process.env.CASCADE_FFMPEG_PATH || "ffmpeg";
const ffmpegAvailable = spawnSync(ffmpegPath, ["-version"], { stdio: "ignore", windowsHide: true }).status === 0;
const renderWithFFmpeg = ffmpegAvailable ? it : it.skip;

function runFFmpeg(args: string[]): void {
  const result = spawnSync(ffmpegPath, args, { encoding: "utf8", windowsHide: true });
  if (result.status !== 0) throw new Error(`ffmpeg fixture failed: ${result.stderr || result.stdout || result.error?.message || "unknown error"}`);
}

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
  }, {
    id: "step_screenshot",
    kind: "step_screenshot",
    uri: "file:///tmp/step.png",
    mime_type: "image/png",
    asset_role: "presentation_reference",
    include_in_demo: true,
    metadata: { presentation_only: true },
  }, {
    id: "narration_1",
    kind: "narration_audio",
    uri: "file:///tmp/narration.wav",
    mime_type: "audio/wav",
    asset_role: "narration_audio",
    include_in_demo: true,
    duration_ms: 1800,
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

  it("accepts only a timed presentation-only step screenshot as a still", () => {
    const valid = plan();
    valid.shots.push({ id: "still", source_artifact_id: "step_screenshot", presentation_kind: "still", output_duration_ms: 1500, purpose: "Show saved state" });
    expect(validateEditPlan({ catalog, edit_plan: valid }).valid).toBe(true);

    const invalid = structuredClone(valid);
    invalid.shots[1]!.output_duration_ms = 100;
    invalid.shots[1]!.source_time_range_ms = [0, 100];
    expect(validateEditPlan({ catalog, edit_plan: invalid }).errors.map((item) => item.code)).toEqual(expect.arrayContaining(["invalid_still_output_duration", "still_source_time_range_not_allowed"]));
  });

  it("rejects a screenshot explicitly mislabeled as video", () => {
    const invalid = plan();
    invalid.shots.push({
      id: "mislabelled_screenshot",
      source_artifact_id: "step_screenshot",
      presentation_kind: "video",
      source_time_range_ms: [7_500, 10_600],
      purpose: "Screenshot must not be treated as a video source",
    });
    const report = validateEditPlan({ catalog, edit_plan: invalid });
    expect(report.valid).toBe(false);
    expect(report.errors.map((item) => item.code)).toContain("time_range_outside_source");
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

  it("clips global caption cues into each output segment before the only video encode", () => {
    const editPlan = plan();
    editPlan.caption_cues = [
      { id: "cross_segment", output_range_ms: [750, 1250], text: "Build the project", source: "user_configured" },
      { id: "second_segment", output_range_ms: [1500, 1900], text: "Review the result", source: "model_confirmed" },
    ];
    expect(globalCaptionCuesForOutputWindow(editPlan, 0, 1000)).toEqual([
      { start_ms: 750, end_ms: 1000, text: "Build the project" },
    ]);
    expect(globalCaptionCuesForOutputWindow(editPlan, 1000, 1000)).toEqual([
      { start_ms: 0, end_ms: 250, text: "Build the project" },
      { start_ms: 500, end_ms: 900, text: "Review the result" },
    ]);
  });

  it("accepts confirmed narration and caption cues but rejects invalid candidate data", () => {
    const valid = plan();
    valid.narrations = [{
      id: "intro", source_artifact_id: "narration_1", source_time_range_ms: [0, 1500], output_time_range_ms: [200, 1700],
      volume_percent: 100, duck_source_audio: true, duck_source_to_percent: 30, source: "user_recorded",
    }];
    valid.caption_cues = [{ id: "intro", output_range_ms: [200, 1700], text: "打开产品工作台。", source: "user_configured" }];
    expect(validateEditPlan({ catalog, edit_plan: valid }).valid).toBe(true);

    const invalid = plan();
    invalid.narrations = [{
      id: "", source_artifact_id: "asset_audio", output_time_range_ms: [0, 2100], volume_percent: 250, duck_source_to_percent: 101, source: "model_candidate" as "user_recorded",
    }];
    invalid.caption_cues = [{ id: "", output_range_ms: [1000, 1000], text: "", source: "asr_candidate" as "user_configured" }];
    expect(validateEditPlan({ catalog, edit_plan: invalid }).errors.map((item) => item.code)).toEqual(expect.arrayContaining([
      "invalid_narration_id", "invalid_narration_artifact", "invalid_narration_range", "narration_source_too_short", "invalid_narration_volume", "invalid_narration_duck", "invalid_narration_source",
      "invalid_caption_cue_id", "invalid_caption_cue_range", "empty_caption_cue", "invalid_caption_cue_source",
    ]));
  });

  it("does not mark an unprocessed source recording as rendered when FFmpeg is unavailable", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "cascade-audio-post-process-"));
    const previousFFmpeg = process.env.CASCADE_FFMPEG_PATH;
    try {
      const videoPath = path.join(root, "recording.mp4");
      const narrationPath = path.join(root, "narration.wav");
      await writeFile(videoPath, "not parsed because FFmpeg is intentionally unavailable");
      await writeFile(narrationPath, "not parsed because FFmpeg is intentionally unavailable");
      process.env.CASCADE_FFMPEG_PATH = "definitely-not-installed-ffmpeg-for-audio-post-process";
      const renderCatalog: AssetTimelineCatalog = structuredClone(catalog);
      renderCatalog.artifacts[0] = { ...renderCatalog.artifacts[0], uri: pathToFileURL(videoPath).href };
      renderCatalog.artifacts[1] = { ...renderCatalog.artifacts[1], uri: pathToFileURL(narrationPath).href };
      const editPlan = plan();
      editPlan.narrations = [{
        id: "intro", source_artifact_id: "narration_1", source_time_range_ms: [0, 1500], output_time_range_ms: [200, 1700],
        volume_percent: 100, duck_source_audio: true, duck_source_to_percent: 30, source: "user_recorded",
      }];
      editPlan.caption_cues = [{ id: "intro", output_range_ms: [200, 1700], text: "Open the product workspace.", source: "user_configured" }];
      const result = await render({ output_dir: path.join(root, "render"), asset_timeline_catalog: renderCatalog, edit_plan: editPlan });
      const manifest = JSON.parse(await readFile(result.render_manifest_path, "utf8"));
      expect(manifest.status).toBe("planned");
      expect(manifest.compositor.fallback_reason).toBe("ffmpeg_not_available_for_audio_caption_post_process");
      expect(manifest.compositor.applied_operations).toEqual([]);
      expect(manifest.compositor.planned_operations).toEqual(expect.arrayContaining(["narration", "caption"]));
    } finally {
      if (previousFFmpeg === undefined) delete process.env.CASCADE_FFMPEG_PATH;
      else process.env.CASCADE_FFMPEG_PATH = previousFFmpeg;
      await rm(root, { recursive: true, force: true });
    }
  });

  renderWithFFmpeg("mixes confirmed narration into both final delivery profiles", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "cascade-dual-narration-"));
    try {
      const recordingPath = path.join(root, "recording.mp4");
      const narrationPath = path.join(root, "narration.wav");
      runFFmpeg(["-y", "-f", "lavfi", "-i", "color=c=navy:s=320x180:r=30", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "2", "-c:v", "mpeg4", "-c:a", "aac", recordingPath]);
      runFFmpeg(["-y", "-f", "lavfi", "-i", "sine=frequency=880:sample_rate=24000", "-t", "1.5", "-c:a", "pcm_s16le", narrationPath]);

      const renderCatalog: AssetTimelineCatalog = structuredClone(catalog);
      renderCatalog.artifacts[0] = { ...renderCatalog.artifacts[0], uri: pathToFileURL(recordingPath).href, local_path: recordingPath };
      renderCatalog.artifacts[2] = { ...renderCatalog.artifacts[2], uri: pathToFileURL(narrationPath).href, local_path: narrationPath, duration_ms: 1500 };
      const editPlan = plan();
      editPlan.narrations = [{
        id: "intro", source_artifact_id: "narration_1", source_time_range_ms: [0, 1500], output_time_range_ms: [200, 1700],
        volume_percent: 100, duck_source_audio: true, duck_source_to_percent: 30, source: "tts_confirmed",
      }];

      const result = await render({
        output_dir: path.join(root, "render"),
        asset_timeline_catalog: renderCatalog,
        edit_plan: editPlan,
        delivery_profiles: [
          { id: "final_master_2k", mode: "final", width: 2560, height: 1440, fps: 30, format: "mp4" },
          { id: "final_delivery_1080p", mode: "final", width: 1920, height: 1080, fps: 30, format: "mp4" },
        ],
      });

      expect(result.delivery_status).toBe("complete");
      expect(result.deliverables).toHaveLength(2);
      for (const deliverable of result.deliverables || []) {
        expect(deliverable.status).toBe("complete");
        expect((await stat(deliverable.video_path)).size).toBeGreaterThan(0);
        const probe = spawnSync(ffmpegPath, ["-hide_banner", "-i", deliverable.video_path], { encoding: "utf8", windowsHide: true });
        expect(`${probe.stdout}\n${probe.stderr}`).toMatch(/Audio:/);
        const manifest = JSON.parse(await readFile(deliverable.render_manifest_path, "utf8"));
        expect(manifest.compositor.applied_operations).toContain("narration");
        expect(manifest.compositor.encoding_audit.post_process_video_mode).toBe("stream_copy");
      }
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }, 120_000);
});
