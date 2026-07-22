import { spawnSync } from "node:child_process";
import { mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
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

describe("static screenshot compositor e2e", () => {
  renderWithFFmpeg("renders a still image with a silent compatible audio stream and concatenates it to recording video", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "cascade-still-render-"));
    try {
      const recordingPath = path.join(root, "recording.mp4");
      const screenshotPath = path.join(root, "step.png");
      // Generate test-only media at runtime. No product material is stored in the repository.
      runFFmpeg(["-y", "-f", "lavfi", "-i", "color=c=navy:s=320x180:r=30", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-c:v", "mpeg4", "-c:a", "aac", recordingPath]);
      await writeFile(screenshotPath, Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9WlIP9sAAAAASUVORK5CYII=", "base64"));

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
  });
});
