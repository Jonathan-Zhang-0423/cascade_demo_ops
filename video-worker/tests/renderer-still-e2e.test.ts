import { spawnSync } from "node:child_process";
import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { describe, expect, it } from "vitest";

import { buildTimelineStepsFromTrace, defaultEditPlan, render, type AssetTimelineCatalog, type DemoEditPlan } from "../src/renderer.js";

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

function planWithTargetEvidence(): DemoEditPlan {
  const result = plan();
  result.shots[0]!.operations = [{ type: "zoom_pan", zoom: 2, x: 0.9, y: 0.5 }];
  result.shots[0]!.overlays = [{
    type: "highlight_box",
    shape: "rectangle",
    x: 0.01,
    y: 0.01,
    width: 0.02,
    height: 0.02,
    target_evidence_artifact_id: "screenshot",
    geometry_source: "browser_agent_target_geometry_v1",
    start_ms: 0,
    end_ms: 900,
  }];
  return result;
}

function catalogWithTargetEvidence(recordingPath: string, screenshotPath: string): AssetTimelineCatalog {
  const result = catalog(recordingPath, screenshotPath);
  result.artifacts[1]!.metadata = {
    presentation_only: true,
    target_geometry: {
      schema_version: "demoops.browser_target_geometry.v1",
      target_semantic_id: "build_button",
      resolution_strategy: "role_name",
      selector_digest_sha256: "a".repeat(64),
      captured_at: "2026-08-13T00:00:00Z",
      recording_offset_ms: 500,
      viewport: { width: 2560, height: 1440, dpr: 1 },
      element_box_css_px: { x: 1664, y: 648, width: 256, height: 144 },
      element_box_normalized: { x: 0.65, y: 0.45, width: 0.1, height: 0.1 },
      screenshot_artifact_id: "screenshot",
      confidence: 1,
    },
  };
  return result;
}

function catalogForDefaultTargetStill(recordingPath: string, afterPath: string, targetPath: string): AssetTimelineCatalog {
  const result = catalog(recordingPath, afterPath);
  result.timeline.recording_artifact_id = "recording";
  result.artifacts[0]!.sensitive = true;
  result.artifacts[1]!.id = "after_screenshot";
  result.artifacts[1]!.kind = "screenshot";
  result.artifacts[1]!.source_step_id = "create_project";
  result.artifacts[1]!.metadata = { capture_phase: "after" };
  result.artifacts.push({
    id: "target_screenshot",
    kind: "target_geometry_screenshot",
    uri: pathToFileURL(targetPath).href,
    local_path: targetPath,
    mime_type: "image/png",
    source_step_id: "create_project",
    include_in_demo: false,
    metadata: {
      capture_phase: "target",
      target_geometry: {
        schema_version: "demoops.browser_target_geometry.v1",
        target_semantic_id: "new_project",
        resolution_strategy: "approved_evidence_css",
        selector_digest_sha256: "b".repeat(64),
        captured_at: "2026-08-13T00:00:00Z",
        recording_offset_ms: 500,
        viewport: { width: 2560, height: 1440, dpr: 1 },
        element_box_css_px: { x: 1536, y: 576, width: 256, height: 144 },
        element_box_normalized: { x: 0.6, y: 0.4, width: 0.1, height: 0.1 },
        screenshot_artifact_id: "target_screenshot",
        confidence: 1,
      },
    },
  });
  result.steps = [{
    step_id: "create_project",
    order: 0,
    action: "click",
    status: "passed",
    required: true,
    start_ms: 0,
    end_ms: 1000,
    duration_ms: 1000,
    artifacts: ["after_screenshot", "target_screenshot"],
    expected_outcome: "Open the new project dialog",
  }];
  return result;
}

describe("static screenshot compositor e2e", () => {
  it("rejects a dual delivery request unless both approved profiles are supplied", async () => {
    await expect(render({
      output_dir: path.join(tmpdir(), "cascade-invalid-dual-delivery"),
      delivery_profiles: [{ id: "final_master_2k", mode: "final", width: 2560, height: 1440, fps: 30, format: "mp4" }],
    })).rejects.toThrow("dual_delivery_requires_two_profiles");
  });

  renderWithFFmpeg("renders both required delivery files from one edit plan", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "cascade-dual-delivery-"));
    try {
      const recordingPath = path.join(root, "recording.mp4");
      const screenshotPath = path.join(root, "screenshot.png");
      runFFmpeg(["-y", "-f", "lavfi", "-i", "color=c=navy:s=320x180:r=30", "-t", "1", "-c:v", "mpeg4", recordingPath]);
      generateScreenshot(screenshotPath);
      const result = await render({
        output_dir: path.join(root, "render"),
        asset_timeline_catalog: catalog(recordingPath, screenshotPath),
        edit_plan: plan(),
        delivery_profiles: [
          { id: "final_master_2k", mode: "final", width: 2560, height: 1440, fps: 30, format: "mp4" },
          { id: "final_delivery_1080p", mode: "final", width: 1920, height: 1080, fps: 30, format: "mp4" },
        ],
      });
      expect(result.delivery_status).toBe("complete");
      expect(result.deliverables?.map((item) => item.id)).toEqual(["final_master_2k", "final_delivery_1080p"]);
      expect(result.deliverables?.every((item) => item.status === "complete" && item.sha256 && item.size_bytes)).toBe(true);
      expect(result.deliverables?.[0]?.video_path).toMatch(/final_master_2k\.mp4$/);
      expect(result.deliverables?.[1]?.video_path).toMatch(/final_delivery_1080p\.mp4$/);
      const manifest = JSON.parse(await readFile(result.delivery_manifest_path!, "utf8"));
      expect(manifest.status).toBe("complete");
      expect(manifest.deliverables).toHaveLength(2);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }, 90_000);

  it("uses the Browser Agent recording origin instead of compacting elapsed stage durations", () => {
    const recordingStartMS = Date.parse("2026-08-17T12:00:00.000Z");
    const steps = buildTimelineStepsFromTrace([
      { node_id: "new_project", status: "passed", started_at: "2026-08-17T12:00:24.003Z", completed_at: "2026-08-17T12:00:24.893Z" },
      { node_id: "project_name", status: "passed", started_at: "2026-08-17T12:00:27.690Z", completed_at: "2026-08-17T12:00:28.200Z" },
    ], new Map(), recordingStartMS);
    expect(steps).toEqual(expect.arrayContaining([
      expect.objectContaining({ step_id: "new_project", start_ms: 24003, end_ms: 24893 }),
      expect.objectContaining({ step_id: "project_name", start_ms: 27690, end_ms: 28200 }),
    ]));
  });

  it("uses distinct approved stage screenshots to satisfy a 60 second delivery intent", () => {
    const evidenceCatalog = catalogForDefaultTargetStill("recording.mp4", "after.png", "target.png");
    evidenceCatalog.timeline.recording_artifact_id = "recording";
    evidenceCatalog.artifacts[0]!.sensitive = true;
    evidenceCatalog.steps = [];
    evidenceCatalog.artifacts = [evidenceCatalog.artifacts[0]!];

    for (let stage = 1; stage <= 6; stage++) {
      const stepID = `stage_${stage}`;
      const afterID = `${stepID}_after`;
      const targetID = `${stepID}_target`;
      evidenceCatalog.artifacts.push({
        id: afterID,
        kind: "screenshot",
        uri: pathToFileURL(`${afterID}.png`).href,
        local_path: `${afterID}.png`,
        mime_type: "image/png",
        source_step_id: stepID,
        include_in_demo: true,
        metadata: { capture_phase: "after" },
      }, {
        id: targetID,
        kind: "target_geometry_screenshot",
        uri: pathToFileURL(`${targetID}.png`).href,
        local_path: `${targetID}.png`,
        mime_type: "image/png",
        source_step_id: stepID,
        include_in_demo: false,
        metadata: {
          capture_phase: "target",
          target_geometry: {
            schema_version: "demoops.browser_target_geometry.v1",
            target_semantic_id: `target_${stage}`,
            resolution_strategy: "approved_evidence_css",
            selector_digest_sha256: String(stage).repeat(64),
            captured_at: "2026-08-13T00:00:00Z",
            recording_offset_ms: stage * 1000,
            viewport: { width: 2560, height: 1440, dpr: 1 },
            element_box_css_px: { x: 100, y: 100, width: 200, height: 80 },
            element_box_normalized: { x: 0.04, y: 0.07, width: 0.08, height: 0.06 },
            screenshot_artifact_id: targetID,
            confidence: 1,
          },
        },
      });
      evidenceCatalog.steps.push({
        step_id: stepID,
        order: stage - 1,
        action: "click",
        status: "passed",
        required: true,
        start_ms: (stage - 1) * 1000,
        end_ms: stage * 1000,
        duration_ms: 1000,
        artifacts: [afterID, targetID],
        expected_outcome: `Complete stage ${stage}`,
      });
    }

    const editPlan = defaultEditPlan(evidenceCatalog, 60);
    expect(editPlan.target_duration_ms).toBe(60_000);
    expect(editPlan.shots).toHaveLength(12);
    expect(new Set(editPlan.shots.map((shot) => shot.source_artifact_id)).size).toBe(12);
    expect(editPlan.shots.reduce((total, shot) => total + (shot.output_duration_ms || 0), 0)).toBe(60_000);
    expect(editPlan.shots.every((shot) => (shot.output_duration_ms || 0) <= 15_000)).toBe(true);
    for (const shot of editPlan.shots) {
      const targetOverlays = (shot.overlays || []).filter((overlay) => overlay.type === "highlight_box" || overlay.type === "cursor_highlight");
      if (shot.source_artifact_id.endsWith("_target")) {
        expect(targetOverlays).not.toHaveLength(0);
        expect(targetOverlays.every((overlay) => overlay.target_evidence_artifact_id === shot.source_artifact_id)).toBe(true);
      } else {
        expect(targetOverlays).toHaveLength(0);
      }
    }
  });

  renderWithFFmpeg("builds the default target callout from the exact same verified screenshot", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "cascade-default-target-still-"));
    try {
      const recordingPath = path.join(root, "recording.mp4");
      const afterPath = path.join(root, "after.png");
      const targetPath = path.join(root, "target.png");
      runFFmpeg(["-y", "-f", "lavfi", "-i", "color=c=navy:s=320x180:r=30", "-t", "1", "-c:v", "mpeg4", recordingPath]);
      generateScreenshot(afterPath);
      generateScreenshot(targetPath);
      const result = await render({
        output_dir: path.join(root, "render"),
        asset_timeline_catalog: catalogForDefaultTargetStill(recordingPath, afterPath, targetPath),
        render_profile: { mode: "preview", format: "mp4", width: 320, height: 180, fps: 30, preset: "ultrafast" },
      });
      const savedPlan = JSON.parse(await readFile(result.demo_edit_plan_path, "utf8"));
      const manifest = JSON.parse(await readFile(result.render_manifest_path, "utf8"));
      expect(savedPlan.shots[0]).toMatchObject({
        source_artifact_id: "target_screenshot",
        presentation_kind: "still",
        overlays: expect.arrayContaining([
          expect.objectContaining({ type: "highlight_box", target_evidence_artifact_id: "target_screenshot" }),
          expect.objectContaining({ type: "cursor_highlight", target_evidence_artifact_id: "target_screenshot" }),
        ]),
      });
      expect(manifest.compositor.applied_operations).toEqual(expect.arrayContaining(["highlight_box", "cursor_highlight"]));
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }, 30_000);

  renderWithFFmpeg("reports a blocking mismatch when shot duration is shorter than the declared edit target", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "cascade-edit-duration-gate-"));
    try {
      const recordingPath = path.join(root, "recording.mp4");
      const screenshotPath = path.join(root, "screenshot.png");
      runFFmpeg(["-y", "-f", "lavfi", "-i", "color=c=navy:s=320x180:r=30", "-t", "1", "-c:v", "mpeg4", recordingPath]);
      generateScreenshot(screenshotPath);
      const editPlan = plan();
      editPlan.target_duration_ms = 3000;
      const result = await render({
        output_dir: path.join(root, "render"),
        asset_timeline_catalog: catalog(recordingPath, screenshotPath),
        edit_plan: editPlan,
        render_profile: { mode: "preview", format: "mp4", width: 320, height: 180, fps: 30, preset: "ultrafast" },
      });
      const report = JSON.parse(await readFile(result.requirement_satisfaction_report_path, "utf8"));
      expect(report.status).toBe("not_satisfied");
      expect(report.errors).toEqual(expect.arrayContaining([
        expect.objectContaining({ code: "edit_plan_timeline_duration_mismatch", path: "demo_edit_plan.shots" }),
      ]));
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }, 30_000);

  renderWithFFmpeg("does not draw target geometry over a different still from the same step", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "cascade-target-source-mismatch-"));
    try {
      const recordingPath = path.join(root, "recording.mp4");
      const afterPath = path.join(root, "after.png");
      const targetPath = path.join(root, "target.png");
      runFFmpeg(["-y", "-f", "lavfi", "-i", "color=c=navy:s=320x180:r=30", "-t", "1", "-c:v", "mpeg4", recordingPath]);
      generateScreenshot(afterPath);
      generateScreenshot(targetPath);
      const evidenceCatalog = catalogForDefaultTargetStill(recordingPath, afterPath, targetPath);
      const editPlan: DemoEditPlan = {
        ...plan(),
        target_duration_ms: 1000,
        shots: [{
          id: "mismatched_still",
          source_artifact_id: "after_screenshot",
          source_step_id: "create_project",
          presentation_kind: "still",
          output_duration_ms: 1000,
          purpose: "Show result",
          overlays: [{
            type: "highlight_box",
            shape: "rectangle",
            target_evidence_artifact_id: "target_screenshot",
            geometry_source: "browser_agent_target_geometry_v1",
          }],
        }],
      };
      const result = await render({
        output_dir: path.join(root, "render"),
        asset_timeline_catalog: evidenceCatalog,
        edit_plan: editPlan,
        render_profile: { mode: "preview", format: "mp4", width: 320, height: 180, fps: 30, preset: "ultrafast" },
      });
      const manifest = JSON.parse(await readFile(result.render_manifest_path, "utf8"));
      expect(manifest.compositor.applied_operations).not.toContain("highlight_box");
      expect(manifest.compositor.skipped_operations).toEqual(expect.arrayContaining([
        expect.objectContaining({ type: "highlight_box", reason: "target_geometry_source_mismatch" }),
      ]));
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }, 30_000);

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

      expect(manifest).toMatchObject({
        status: "rendered",
        compositor: {
          method: "ffmpeg_trim_concat",
          encoding_audit: {
            source_master_preserved: true,
            segment_video_encode_passes: 1,
            concat_video_mode: "stream_copy",
            post_process_video_mode: "not_applicable",
            max_lossy_video_encode_passes_per_output_frame: 1,
            global_captions_embedded_during_segment_encode: false,
          },
        },
      });
      expect(output.size).toBeGreaterThan(0);
      expect(mediaInfo).toContain("320x180");
      expect(mediaInfo).toMatch(/Video:/);
      expect(mediaInfo).toMatch(/Audio:/);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }, 30_000);

  renderWithFFmpeg("skips target annotations that have no Browser Agent geometry evidence", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "cascade-shape-render-"));
    try {
      const recordingPath = path.join(root, "recording.mp4");
      const screenshotPath = path.join(root, "step.png");
      runFFmpeg(["-y", "-f", "lavfi", "-i", "color=c=navy:s=320x180:r=30", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-c:v", "mpeg4", "-c:a", "aac", recordingPath]);
      generateScreenshot(screenshotPath);
      const result = await render({ output_dir: path.join(root, "render"), asset_timeline_catalog: catalog(recordingPath, screenshotPath), edit_plan: planWithShape(), render_profile: { mode: "preview", format: "mp4", width: 320, height: 180, fps: 30, preset: "ultrafast" } });
      const manifest = JSON.parse(await readFile(result.render_manifest_path, "utf8"));
      expect(manifest.compositor.applied_operations).not.toContain("highlight_box");
      expect(manifest.compositor.skipped_operations).toEqual(expect.arrayContaining([
        expect.objectContaining({ type: "highlight_box", reason: "missing_target_geometry" }),
      ]));
      const savedPlan = JSON.parse(await readFile(result.demo_edit_plan_path, "utf8"));
      expect(savedPlan.shots[0].overlays[0]).toMatchObject({ rotation: 24, scale_x: 125, scale_y: 75 });
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }, 30_000);

  renderWithFFmpeg("renders evidence-bound target geometry after zoom and preserves its audit binding", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "cascade-evidence-shape-render-"));
    try {
      const recordingPath = path.join(root, "recording.mp4");
      const screenshotPath = path.join(root, "step.png");
      runFFmpeg(["-y", "-f", "lavfi", "-i", "color=c=navy:s=320x180:r=30", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "1", "-c:v", "mpeg4", "-c:a", "aac", recordingPath]);
      generateScreenshot(screenshotPath);
      const result = await render({
        output_dir: path.join(root, "render"),
        asset_timeline_catalog: catalogWithTargetEvidence(recordingPath, screenshotPath),
        edit_plan: planWithTargetEvidence(),
        render_profile: { mode: "preview", format: "mp4", width: 320, height: 180, fps: 30, preset: "ultrafast" },
      });
      const manifest = JSON.parse(await readFile(result.render_manifest_path, "utf8"));
      const overlay = manifest.compositor.shot_plan[0].overlays[0];
      expect(manifest.compositor.applied_operations).toContain("highlight_box");
      expect(overlay).toMatchObject({
        target_evidence_artifact_id: "screenshot",
        geometry_source: "browser_agent_target_geometry_v1",
        target_geometry_verified: true,
        coordinate_space: "post_edit_normalized",
        evidence_recording_offset_ms: 500,
        evidence_source_time_range_ms: [0, 1000],
        output_time_range_ms: [0, 900],
      });
      expect(overlay.x + overlay.width / 2).toBeCloseTo(0.5, 2);
      expect(overlay.y + overlay.height / 2).toBeCloseTo(0.5, 2);
      expect(overlay.x).not.toBeCloseTo(0.01, 2);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }, 30_000);

  renderWithFFmpeg("skips target annotations when the captured click is outside the video shot time range", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "cascade-target-time-mismatch-"));
    try {
      const recordingPath = path.join(root, "recording.mp4");
      const screenshotPath = path.join(root, "step.png");
      runFFmpeg(["-y", "-f", "lavfi", "-i", "color=c=navy:s=320x180:r=30", "-t", "1", "-c:v", "mpeg4", recordingPath]);
      generateScreenshot(screenshotPath);
      const editPlan = planWithTargetEvidence();
      editPlan.shots[0]!.source_time_range_ms = [0, 200];
      const result = await render({
        output_dir: path.join(root, "render"),
        asset_timeline_catalog: catalogWithTargetEvidence(recordingPath, screenshotPath),
        edit_plan: editPlan,
        render_profile: { mode: "preview", format: "mp4", width: 320, height: 180, fps: 30, preset: "ultrafast" },
      });
      const manifest = JSON.parse(await readFile(result.render_manifest_path, "utf8"));
      const report = JSON.parse(await readFile(result.requirement_satisfaction_report_path, "utf8"));
      expect(manifest.compositor.applied_operations).not.toContain("highlight_box");
      expect(manifest.compositor.skipped_operations).toEqual(expect.arrayContaining([
        expect.objectContaining({ type: "highlight_box", reason: "target_geometry_time_mismatch" }),
      ]));
      expect(report.status).toBe("not_satisfied");
      expect(report.actual.target_overlay_evidence_failures).toEqual(expect.arrayContaining([
        expect.objectContaining({ type: "highlight_box", reason: "target_geometry_time_mismatch", shot_id: "recording_shot", target_evidence_artifact_id: "screenshot" }),
      ]));
      expect(report.actual.target_overlay_evidence_root_causes).toEqual(expect.arrayContaining([
        expect.objectContaining({ reason: "target_geometry_time_mismatch", target_evidence_artifact_id: "screenshot", overlay_types: ["highlight_box"] }),
      ]));
      expect(report.errors).toEqual(expect.arrayContaining([
        expect.objectContaining({ code: "target_overlay_evidence_unusable", path: "asset_timeline_catalog.artifacts[screenshot].metadata.target_geometry.recording_offset_ms" }),
      ]));
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }, 30_000);

  renderWithFFmpeg("keeps privacy blur ahead of an overlapping target annotation", async () => {
    const root = await mkdtemp(path.join(tmpdir(), "cascade-target-privacy-overlap-"));
    try {
      const recordingPath = path.join(root, "recording.mp4");
      const screenshotPath = path.join(root, "step.png");
      runFFmpeg(["-y", "-f", "lavfi", "-i", "color=c=navy:s=320x180:r=30", "-t", "1", "-c:v", "mpeg4", recordingPath]);
      generateScreenshot(screenshotPath);
      const editPlan = planWithTargetEvidence();
      editPlan.shots[0]!.overlays!.push({ type: "blur_region", x: 0.55, y: 0.35, width: 0.3, height: 0.3, start_ms: 0, end_ms: 900 });
      const result = await render({
        output_dir: path.join(root, "render"),
        asset_timeline_catalog: catalogWithTargetEvidence(recordingPath, screenshotPath),
        edit_plan: editPlan,
        render_profile: { mode: "preview", format: "mp4", width: 320, height: 180, fps: 30, preset: "ultrafast" },
      });
      const manifest = JSON.parse(await readFile(result.render_manifest_path, "utf8"));
      expect(manifest.compositor.applied_operations).not.toContain("highlight_box");
      expect(manifest.compositor.applied_operations).toContain("blur_region");
      expect(manifest.compositor.skipped_operations).toEqual(expect.arrayContaining([
        expect.objectContaining({ type: "highlight_box", reason: "target_geometry_privacy_overlap" }),
      ]));
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }, 30_000);
});
