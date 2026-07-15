import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

import { render } from "../dist/renderer.js";

const root = await mkdtemp(path.join(tmpdir(), "cascade-render-requirements-"));

try {
  const screenshotPath = path.join(root, "step-001.png");
  const outputDir = path.join(root, "render");
  const request = {
    output_dir: outputDir,
    duration_sec: 12,
    graph: {
      id: "graph_requirements_smoke",
      version: 1,
      execution: {
        viewports: [{ name: "desktop", width: 1440, height: 900, device: "desktop" }],
      },
      assets: {
        demo_video_60s: true,
        screenshot_pack: true,
        step_by_step_docs: true,
        target_duration_sec: 60,
        requested_assets: [
          { id: "demo_video_60s", kind: "demo_video", format: "mp4", duration_sec: 60, required: true },
          { id: "screenshot_pack", kind: "screenshot_pack", format: "png", required: true },
          { id: "step_by_step_docs", kind: "step_by_step_docs", format: "markdown", required: true },
        ],
      },
      nodes: [
        {
          id: "node_open",
          action: "navigate",
          expected_outcome: "Product page loads",
          is_screenshot: true,
          capture: { screenshot: true, full_page: true, asset_role: "primary" },
          duration_hint_ms: 1200,
        },
      ],
    },
    recording_run_spec: {
      timeline: { target_duration_sec: 12, max_duration_sec: 30 },
      browser: { viewports: [{ name: "desktop", width: 1440, height: 900, device: "desktop" }] },
      outputs: {
        raw_recording: true,
        final_video: true,
        screenshot_pack: true,
        step_by_step_docs: true,
        trace: true,
        output_formats: ["mp4", "png", "json"],
        resolution_width: 960,
        resolution_height: 640,
      },
    },
    execution_trace: {
      id: "trace_requirements_smoke",
      workflow_graph_id: "graph_requirements_smoke",
      graph_version: 1,
      step_results: [
        {
          node_id: "node_open",
          status: "passed",
          duration_ms: 1200,
          observed_state: "Product page loads",
          artifacts: [
            {
              id: "artifact_screenshot_001",
              kind: "screenshot",
              uri: pathToFileURL(screenshotPath).href,
              mime_type: "image/png",
              source_node_id: "node_open",
              metadata: { asset_role: "primary", include_in_demo: true, capture_scope: "full_page" },
            },
          ],
        },
      ],
      artifacts: [
        {
          id: "artifact_screenshot_001",
          kind: "screenshot",
          uri: pathToFileURL(screenshotPath).href,
          mime_type: "image/png",
          source_node_id: "node_open",
          metadata: { asset_role: "primary", include_in_demo: true, capture_scope: "full_page" },
        },
      ],
    },
    generated_assets: [
      {
        id: "artifact_screenshot_001",
        kind: "screenshot",
        uri: pathToFileURL(screenshotPath).href,
        mime_type: "image/png",
        source_node_id: "node_open",
        metadata: { asset_role: "primary", include_in_demo: true, capture_scope: "full_page" },
      },
    ],
  };

  const result = await render(request);
  const report = JSON.parse(await readFile(result.requirement_satisfaction_report_path, "utf8"));

  assert.equal(report.schema_version, "demoops.requirement_satisfaction_report.v1");
  assert.equal(report.adopted_values.target_duration_sec.value, 60);
  assert.equal(report.adopted_values.target_duration_sec.source, "workflow_graph.assets");
  assert.equal(report.requested.recording_target_duration_sec, 12);
  assert.deepEqual(report.requested.required_step_ids, ["node_open"]);
  assert.deepEqual(report.requested.screenshot_step_ids, ["node_open"]);
  assert.deepEqual(report.actual.represented_step_ids, ["node_open"]);
  assert.deepEqual(report.actual.screenshot_step_ids, ["node_open"]);
  assert.equal(report.step_checks.length, 1);
  assert.equal(report.step_checks[0].status, "pass");
  assert.deepEqual(report.step_checks[0].screenshot_artifact_ids, ["artifact_screenshot_001"]);
  assert.ok(report.warnings.some((finding) => finding.code === "duration_intent_conflict"));
  assert.ok(report.asset_checks.some((check) => check.kind === "demo_video" && check.status === "fail"));
  assert.equal(report.status, "not_satisfied");
} finally {
  await rm(root, { recursive: true, force: true });
}
