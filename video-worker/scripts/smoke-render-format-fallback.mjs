import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

import { render } from "../dist/renderer.js";

const root = await mkdtemp(path.join(tmpdir(), "cascade-render-format-fallback-"));
const previousFFmpeg = process.env.CASCADE_FFMPEG_PATH;

try {
  process.env.CASCADE_FFMPEG_PATH = "definitely-not-installed-ffmpeg-for-format-smoke";
  const rawRecordingPath = path.join(root, "recording.webm");
  await writeFile(rawRecordingPath, Buffer.from("webm bytes"));

  const result = await render({
    output_dir: path.join(root, "render"),
    duration_sec: 2,
    graph: {
      id: "graph_format_fallback_smoke",
      version: 1,
      nodes: [{ id: "node_open", action: "navigate", expected_outcome: "Product page loads", duration_hint_ms: 1000 }],
      assets: {
        requested_assets: [{ id: "demo_video", kind: "demo_video", format: "mp4", duration_sec: 2, required: true }],
      },
    },
    recording_run_spec: {
      outputs: { final_video: true, output_formats: ["mp4"] },
    },
    execution_trace: {
      id: "trace_format_fallback_smoke",
      workflow_graph_id: "graph_format_fallback_smoke",
      graph_version: 1,
      step_results: [{ node_id: "node_open", status: "passed", duration_ms: 1000 }],
      artifacts: [
        {
          id: "artifact_raw_recording",
          kind: "raw_recording",
          uri: pathToFileURL(rawRecordingPath).href,
          mime_type: "video/webm",
          metadata: { asset_role: "raw_recording", include_in_demo: true },
        },
      ],
    },
  });

  const manifest = JSON.parse(await readFile(result.render_manifest_path, "utf8"));
  const report = JSON.parse(await readFile(result.requirement_satisfaction_report_path, "utf8"));

  assert.equal(path.extname(result.video_path), ".webm");
  assert.equal(manifest.compositor.output_container, "webm");
  assert.equal(manifest.compositor.fallback_reason, "ffmpeg_not_available");
  assert.ok(report.errors.some((finding) => finding.code === "requested_format_not_satisfied"));
  assert.ok(report.errors.some((finding) => finding.code === "media_normalization_not_ok"));
} finally {
  if (previousFFmpeg === undefined) {
    delete process.env.CASCADE_FFMPEG_PATH;
  } else {
    process.env.CASCADE_FFMPEG_PATH = previousFFmpeg;
  }
  await rm(root, { recursive: true, force: true });
}
