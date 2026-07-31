import { spawnSync } from "node:child_process";
import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { describe, expect, it } from "vitest";

import { render, type AssetTimelineCatalog, type DemoEditPlan } from "../src/renderer.js";

const ffmpegPath = process.env.CASCADE_FFMPEG_PATH || "ffmpeg";
const ffmpegAvailable = spawnSync(ffmpegPath, ["-version"], { stdio: "ignore", windowsHide: true }).status === 0;
const renderWithFFmpeg = ffmpegAvailable ? it : it.skip;

function runFFmpeg(args: string[]): void {
  const result = spawnSync(ffmpegPath, args, { encoding: "utf8", windowsHide: true });
  if (result.status !== 0) throw new Error(`ffmpeg fixture failed: ${result.stderr || result.stdout || result.error?.message || "unknown error"}`);
}

function generateScreenshot(screenshotPath: string): void {
  runFFmpeg([
    "-y",
    "-f", "lavfi",
    "-i", "color=c=white:s=320x180",
    "-frames:v", "1",
    "-update", "1",
    screenshotPath,
  ]);
}

function catalog(recordingPath: string, screenshotPath: string): AssetTimelineCatalog {
  return {
    schema_version: "demoops.asset_timeline_catalog.v1",
    catalog_id: "catalog_still_e2e",
    workflow_graph_id: "graph_still_e2e",
    graph_version: 1,
    run_id: "run_still_e2e",
    source: { generated_at: "2026-07-21T00:00:00Z" },
    constraints: {
      source_material_only: true,
      prohibit_new_image_or_video_generation: true,
      script_is_primary_storyline: true,
      allowed_edit_operations: ["trim"],
      prohibited_plan_keys: [],
    },
    timeline: { duration_ms: 1000, recording_artifact_id: "recording" },
    steps: [],
    artifacts: [
      {
        id: "recording",
        kind: "raw_recording",
        uri: pathToFileURL(recordingPath).href,
        local_path: recordingPath,
        mime_type: "video/mp4",
        asset_role: "raw_recording",
        include_in_demo: true,
        duration_ms: 1000,
      },
      {
        id: "screenshot",
        kind: "step_screenshot",
        uri: pathToFileURL(screenshotPath).href,
        local_path: screenshotPath,
        mime_type: "image/png",
        asset_role: "presentation_reference",
        include_in_demo: true,
        metadata: { presentation_only: true },
      },
    ],
  };
}

function plan(): DemoEditPlan {
  return {
    plan_id: "plan_still_e2e",
    source_authority: "server_local_editor",
    model_role: "presentation_optimizer_only",
    source_material_policy: "existing_assets_only",
    script_order_policy: "preserve_required_step_order",
    locked_fields: ["source_authority", "model_role", "source_material_policy", "script_order_policy", "source_artifact_id", "source_step_id", "source_time_range_ms", "required_step_order"],
    model_editable_fields: [],
    target_duration_ms: 1500,
    shots: [
      { id: "recording_shot", source_artifact_id: "recording", source_time_range_ms: [0, 1000], purpose: "Synthetic recording" },
      { id: "still_shot", source_artifact_id: "screenshot", presentation_kind: "still", output_duration_ms: 500, purpose: "Synthetic step screenshot" },
    ],
    audio: { mode: "source", volume_percent: 100 },
  };
}

function planWithShape(): DemoEditPlan {
  const result = plan();
  result.shots[0]!.overlays = [{
    type: "highlight_box", shape: "rectangle", x: 0.2, y: 0.2, width: 0.4, height: 0.3,
    color: "#dc58d5", stroke_width: 5, fill_color: "#dc58d5", fill_opacity: 10, rotation: 24, scale_x: 125, scale_y: 75,
    start_ms: 100, end_ms: 900,
  }, {
    type: "highlight_box", shape: "polygon", x: 0.08, y: 0.08, width: 0.18, height: 0.16,
    color: "#1da7ff", stroke_width: 3, fill_color: "#1da7ff", fill_opacity: 20, start_ms: 150, end_ms: 850,
  }, {
    type: "highlight_box", shape: "star", x: 0.7, y: 0.55, width: 0.16, height: 0.22,
    color: "#f0b429", stroke_width: 3, fill_color: "#f0b429", fill_opacity: 25, start_ms: 200, end_ms: 800,
  }];
  return result;
}

describe("static screenshot compositor e2e", () => {
  renderWithFFmpeg("renders a still image with a silent compatible audio stream and concatenates it to recording video", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "cascade-still-render-"));
    try {
      const recordingPath = path.join(root, "recording.mp4");
      const screenshotPath = path.join(root, "step.png");
      // Generate test-only media at runtime. No product material is stored in the repository.
      runFFmpeg(["-y", "-f", "lavfi", "-i", "color=c=navy:s=320x180:r=30", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-c:v", "mpeg4", "-c:a", "aac", recordingPath]);
      generateScreenshot(screenshotPath);

      const result = await render({
        output_dir: path.join(root, "render"),
        asset_timeline_catalog: catalog(recordingPath, screenshotPath),
        edit_plan: plan(),
        render_profile: { mode: "preview", format: "mp4", width: 320, height: 180, fps: 30, preset: "ultrafast" },
      });

      const manifest = JSON.parse(await readFile(result.render_manifest_path, "utf8"));
      const output = await stat(result.video_path);
      const probe = spawnSync(ffmpegPath, ["-hide_banner", "-i", result.video_path], { encoding: "utf8", windowsHide: true });
      const mediaInfo = `${probe.stdout}\n${probe.stderr}`;

      expect(manifest).toMatchObject({ status: "rendered", compositor: { method: "ffmpeg_trim_concat" } });
      expect(output.size).toBeGreaterThan(0);
      expect(mediaInfo).toContain("320x180");
      expect(mediaInfo).toMatch(/Video:/);
      expect(mediaInfo).toMatch(/Audio:/);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }, 30_000);

  renderWithFFmpeg("burns a supported rectangle annotation into the rendered video", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "cascade-shape-render-"));
    try {
      const recordingPath = path.join(root, "recording.mp4");
      const screenshotPath = path.join(root, "step.png");
      runFFmpeg(["-y", "-f", "lavfi", "-i", "color=c=navy:s=320x180:r=30", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-c:v", "mpeg4", "-c:a", "aac", recordingPath]);
      generateScreenshot(screenshotPath);
      const result = await render({ output_dir: path.join(root, "render"), asset_timeline_catalog: catalog(recordingPath, screenshotPath), edit_plan: planWithShape(), render_profile: { mode: "preview", format: "mp4", width: 320, height: 180, fps: 30, preset: "ultrafast" } });
      const manifest = JSON.parse(await readFile(result.render_manifest_path, "utf8"));
      expect(manifest.compositor.applied_operations).toContain("highlight_box");
      expect(manifest.compositor.skipped_operations).not.toEqual(expect.arrayContaining([expect.objectContaining({ type: "highlight_box" })]));
      const savedPlan = JSON.parse(await readFile(result.demo_edit_plan_path, "utf8"));
      expect(savedPlan.shots[0].overlays[0]).toMatchObject({ rotation: 24, scale_x: 125, scale_y: 75 });
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }, 30_000);
});
