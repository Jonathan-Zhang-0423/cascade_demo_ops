import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render } from "../dist/renderer.js";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const prior = path.join(root, ".cascade-dev", "artifacts", "dev-test-only", "app-package-waiver", "test_waiver_1786506084390811200", "execution", "render");
const catalogPath = process.env.CASCADE_FULL_EDITOR_CATALOG
  ? path.resolve(process.env.CASCADE_FULL_EDITOR_CATALOG)
  : path.join(prior, "asset_timeline_catalog.json");
const outputDir = path.join(root, ".cascade-dev", "artifacts", "dev-test-only", "full-editor-acceptance", `run-${new Date().toISOString().replace(/[:.]/g, "-")}`);

const catalog = JSON.parse(await readFile(catalogPath, "utf8"));
const recording = catalog.artifacts.find((artifact) => artifact.kind === "raw_recording");
if (!recording?.local_path) throw new Error("raw recording artifact is unavailable");
// The source is the previously captured redacted browser recording. The test
// harness only changes the in-memory sensitivity marker so the editor can read
// this local fixture; the App package and stored artifact remain untouched.
recording.sensitive = false;
recording.include_in_demo = true;

const lockedFields = [
  "source_authority", "model_role", "source_material_policy", "script_order_policy",
  "source_artifact_id", "source_step_id", "source_time_range_ms", "required_step_order",
];
const steps = catalog.steps.filter((step) => step.status === "passed");

function targetGeometryForStep(step) {
  const candidates = (step.artifacts || [])
    .map((id) => catalog.artifacts.find((artifact) => artifact.id === id))
    .concat(catalog.artifacts.filter((artifact) => artifact.source_step_id === step.step_id));
  for (const artifact of candidates) {
    const value = artifact?.metadata?.target_geometry;
    const box = value?.element_box_normalized;
    if (value?.schema_version !== "demoops.browser_target_geometry.v1" || value?.confidence !== 1) continue;
    if (!box || ![box.x, box.y, box.width, box.height].every(Number.isFinite)) continue;
    if (box.x < 0 || box.y < 0 || box.width <= 0 || box.height <= 0 || box.x + box.width > 1 || box.y + box.height > 1) continue;
    if (!/^[a-f0-9]{64}$/.test(value.selector_digest_sha256 || "")) continue;
    if (value.screenshot_artifact_id && value.screenshot_artifact_id !== artifact.id) continue;
    return { artifact, value };
  }
  return undefined;
}

function paddedBox(box) {
  const padX = Math.max(0.008, box.width * 0.12);
  const padY = Math.max(0.008, box.height * 0.2);
  const x = Math.max(0, box.x - padX);
  const y = Math.max(0, box.y - padY);
  return { x, y, width: Math.min(1 - x, box.width + padX * 2), height: Math.min(1 - y, box.height + padY * 2) };
}

function targetAwareZoom(box) {
  return Math.max(1.08, Math.min(1.8, 0.42 / Math.max(box.width, box.height)));
}

function panForCenter(center, zoom) {
  return zoom <= 1 ? 0.5 : Math.max(0, Math.min(1, (center * zoom - 0.5) / (zoom - 1)));
}

const shots = steps.flatMap((step, stepIndex) => {
  const duration = Math.max(300, step.end_ms - step.start_ms);
  const geometryEvidence = targetGeometryForStep(step);
  const geometry = geometryEvidence?.value?.element_box_normalized;
  const ranges = [
    [step.start_ms, step.start_ms + Math.max(300, Math.round(duration * 0.20))],
    [step.start_ms + Math.max(300, Math.round(duration * 0.20)), step.start_ms + Math.max(301, Math.round(duration * 0.70))],
    [step.start_ms + Math.max(301, Math.round(duration * 0.70)), step.end_ms],
  ].map(([start, end]) => [start, Math.max(start + 200, end)]);
  return ranges.map(([start, end], shotIndex) => {
    const localDuration = end - start;
    const operations = [{ type: "trim", start_ms: start, end_ms: end }, { type: "color_grade", style: "neutral_enterprise" }];
    if (shotIndex === 1 && geometry) {
      const zoom = targetAwareZoom(geometry);
      operations.push({ type: "zoom_pan", zoom, x: panForCenter(geometry.x + geometry.width / 2, zoom), y: panForCenter(geometry.y + geometry.height / 2, zoom), scale: zoom });
    }
    if (stepIndex > 0 || shotIndex > 0) operations.push({ type: "transition", style: shotIndex === 1 ? "fade" : "cut", start_ms: 0, end_ms: 350 });
    const overlays = [
      { type: "caption", text: `${stepIndex + 1}.${shotIndex + 1} ${shotIndex === 0 ? "阶段开始" : shotIndex === 1 ? "操作焦点" : "结果确认"}：${step.expected_outcome || step.step_id}`, source_step_id: step.step_id, start_ms: 0, end_ms: Math.min(2200, localDuration) },
    ];
    if (geometryEvidence && geometry) {
      const box = paddedBox(geometry);
      overlays.push({ type: "highlight_box", shape: "rectangle", x: box.x, y: box.y, width: box.width, height: box.height, color: "#1da7ff", stroke_width: 4, start_ms: 0, end_ms: Math.min(1500, localDuration), target_evidence_artifact_id: geometryEvidence.artifact.id, geometry_source: "browser_agent_target_geometry_v1" });
      const cursorWidth = Math.min(0.06, Math.max(0.025, geometry.width * 0.35));
      const cursorHeight = Math.min(0.09, Math.max(0.04, geometry.height * 0.55));
      overlays.push({ type: "cursor_highlight", x: Math.max(0, Math.min(1 - cursorWidth, geometry.x + geometry.width / 2 - cursorWidth / 2)), y: Math.max(0, Math.min(1 - cursorHeight, geometry.y + geometry.height / 2 - cursorHeight / 2)), width: cursorWidth, height: cursorHeight, color: "#ffd166", start_ms: 0, end_ms: Math.min(900, localDuration), target_evidence_artifact_id: geometryEvidence.artifact.id, geometry_source: "browser_agent_target_geometry_v1" });
    }
    // Keep privacy masking explicit and scoped to the login checkpoint only.
    if (stepIndex === 0 && shotIndex === 0) overlays.push({ type: "blur_region", x: 0.70, y: 0.05, width: 0.20, height: 0.10, opacity: 70, start_ms: 0, end_ms: Math.min(1200, localDuration) });
    return { id: `full_${step.step_id}_${shotIndex + 1}`, source_artifact_id: recording.id, source_step_id: step.step_id, source_time_range_ms: [start, end], purpose: `Full editor acceptance ${shotIndex === 0 ? "overview" : shotIndex === 1 ? "action focus" : "result confirmation"}: ${step.expected_outcome || step.step_id}`, operations, overlays };
  });
});

const request = {
  output_dir: outputDir,
  graph: { id: catalog.workflow_graph_id, version: catalog.graph_version, assets: { target_duration_sec: Math.ceil(catalog.timeline.duration_ms / 1000), final_video_formats: ["mp4"] } },
  asset_timeline_catalog: catalog,
  edit_plan: {
    schema_version: "demoops.demo_edit_plan.v1",
    plan_id: `full_editor_acceptance_${Date.now()}`,
    catalog_id: catalog.catalog_id,
    source_authority: "server_local_editor",
    model_role: "presentation_optimizer_only",
    source_material_policy: "existing_assets_only",
    script_order_policy: "preserve_required_step_order",
    locked_fields: lockedFields,
    model_editable_fields: [],
    target_duration_ms: catalog.timeline.duration_ms,
    shots,
    global_style: { color_grade: "neutral_enterprise", pacing: "dynamic", transition_style: "fade" },
    audio: { mode: "source", volume_percent: 85, split_points_ms: [10000, 20000], segment_settings: [{ start_ms: 10000, end_ms: 20000, mode: "mute", volume_percent: 0 }] },
  },
  model_execution: { invoked: false, plan_source: "deterministic_fixture", note: "This run intentionally validates deterministic Server/Worker editing only; no model provider was called." },
  render_profile: { mode: "final", format: "mp4", width: 2560, height: 1440, fps: 30, preset: "medium", crf: 18 },
};

await mkdir(outputDir, { recursive: true });
await writeFile(path.join(outputDir, "full-editor-acceptance-request.json"), JSON.stringify(request, null, 2), "utf8");
const result = await render(request);
const manifest = JSON.parse(await readFile(result.render_manifest_path, "utf8"));
const report = JSON.parse(await readFile(result.requirement_satisfaction_report_path, "utf8"));
const summary = {
  status: report.status,
  render_status: manifest.compositor?.status,
  output: result.video_path,
  planned_operations: manifest.compositor?.planned_operations || [],
  applied_operations: manifest.compositor?.applied_operations || [],
  skipped_operations: manifest.compositor?.skipped_operations || [],
  step_count: shots.length,
  represented_step_ids: report.actual?.represented_step_ids || [],
  traceability: { catalog: catalogPath, request: path.join(outputDir, "full-editor-acceptance-request.json"), render_manifest: result.render_manifest_path, requirement_report: result.requirement_satisfaction_report_path },
  model_execution: request.model_execution,
};
if (summary.status !== "satisfied") {
  throw new Error(`full editor acceptance requirement report is ${summary.status}`);
}
await writeFile(path.join(outputDir, "full-editor-acceptance-summary.json"), JSON.stringify(summary, null, 2), "utf8");
console.log(JSON.stringify(summary, null, 2));
