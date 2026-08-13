import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render } from "../dist/renderer.js";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const catalogPath = path.resolve(process.argv[2] || path.join(
  repoRoot,
  ".cascade-dev",
  "artifacts",
  "dev-test-only",
  "app-package-waiver",
  "test_waiver_1786563531040170300",
  "execution",
  "render",
  "asset_timeline_catalog.json",
));
const targetDurationSec = Number(process.argv[3] || 60);
if (!Number.isInteger(targetDurationSec) || targetDurationSec < 1 || targetDurationSec > 300) {
  throw new Error("target duration must be an integer between 1 and 300 seconds");
}

const catalog = JSON.parse(await readFile(catalogPath, "utf8"));
const recording = catalog.artifacts?.find((artifact) => artifact.id === catalog.timeline?.recording_artifact_id);
if (recording && recording.sensitive !== true) {
  throw new Error("safe still acceptance requires the raw recording to remain sensitive");
}

const outputDir = path.join(
  repoRoot,
  ".cascade-dev",
  "artifacts",
  "dev-test-only",
  "safe-still-editor-acceptance",
  `run-${new Date().toISOString().replace(/[:.]/g, "-")}`,
);
await mkdir(outputDir, { recursive: true });

const request = {
  output_dir: outputDir,
  duration_sec: targetDurationSec,
  graph: {
    id: catalog.workflow_graph_id,
    version: catalog.graph_version,
    assets: { target_duration_sec: targetDurationSec, final_video_formats: ["mp4"] },
  },
  asset_timeline_catalog: catalog,
  model_execution: {
    invoked: false,
    plan_source: "deterministic_server_director",
    note: "Offline Server editor acceptance using only approved, non-sensitive screenshots. No model provider or Browser Agent was called.",
  },
  render_profile: { mode: "final", format: "mp4", width: 2560, height: 1440, fps: 30, preset: "medium", crf: 18 },
};

await writeFile(path.join(outputDir, "safe-still-editor-request.json"), JSON.stringify(request, null, 2), "utf8");
const result = await render(request);
const editPlan = JSON.parse(await readFile(result.demo_edit_plan_path, "utf8"));
const manifest = JSON.parse(await readFile(result.render_manifest_path, "utf8"));
const report = JSON.parse(await readFile(result.requirement_satisfaction_report_path, "utf8"));
const shots = editPlan.shots || [];
const sourceIDs = shots.map((shot) => shot.source_artifact_id);
const summary = {
  status: report.status,
  render_status: manifest.compositor?.status,
  output: result.video_path,
  requested_duration_sec: targetDurationSec,
  edit_plan_duration_sec: (editPlan.target_duration_ms || 0) / 1000,
  rendered_duration_sec: report.actual?.rendered_video_duration_sec,
  shot_count: shots.length,
  unique_source_count: new Set(sourceIDs).size,
  source_artifact_ids: sourceIDs,
  applied_operations: manifest.compositor?.applied_operations || [],
  skipped_operations: manifest.compositor?.skipped_operations || [],
  model_execution: manifest.model_execution,
  traceability: {
    source_catalog: catalogPath,
    request: path.join(outputDir, "safe-still-editor-request.json"),
    edit_plan: result.demo_edit_plan_path,
    render_manifest: result.render_manifest_path,
    requirement_report: result.requirement_satisfaction_report_path,
  },
};

if (summary.shot_count !== summary.unique_source_count) {
  throw new Error("safe still acceptance reused a screenshot artifact");
}
if (summary.edit_plan_duration_sec !== targetDurationSec) {
  throw new Error(`safe still acceptance produced ${summary.edit_plan_duration_sec}s instead of ${targetDurationSec}s`);
}
if (summary.model_execution?.invoked !== false) {
  throw new Error("safe still acceptance unexpectedly reported a model invocation");
}

await writeFile(path.join(outputDir, "safe-still-editor-summary.json"), JSON.stringify(summary, null, 2), "utf8");
console.log(JSON.stringify(summary, null, 2));
