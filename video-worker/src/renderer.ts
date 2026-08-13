import { createHash } from "node:crypto";
import { spawn } from "node:child_process";
import { copyFile, mkdir, readFile, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const ASSET_TIMELINE_CATALOG_SCHEMA_VERSION = "demoops.asset_timeline_catalog.v1";
const DEMO_EDIT_PLAN_SCHEMA_VERSION = "demoops.demo_edit_plan.v1";
const DEMO_EDIT_PLAN_VALIDATION_SCHEMA_VERSION = "demoops.demo_edit_plan_validation.v1";
const MEDIA_NORMALIZATION_REPORT_SCHEMA_VERSION = "demoops.media_normalization_report.v1";
const REQUIREMENT_SATISFACTION_REPORT_SCHEMA_VERSION = "demoops.requirement_satisfaction_report.v1";
const DEMO_EDIT_SOURCE_AUTHORITY = "customer_side_agent";
const ALLOWED_SOURCE_AUTHORITIES = [DEMO_EDIT_SOURCE_AUTHORITY, "server_local_editor"] as const;
const DEMO_EDIT_MODEL_ROLE = "presentation_optimizer_only";

const ALLOWED_EDIT_OPERATIONS = [
  "trim",
  "crop",
  "zoom_pan",
  "pan",
  "hold",
  "speed",
  "transition",
  "caption",
  "callout",
  "highlight",
  "cursor_highlight",
  "blur_region",
  "color_grade",
] as const;

const ALLOWED_OVERLAY_TYPES = ["caption", "callout", "highlight_box", "cursor_highlight", "spotlight", "blur_region", "progress_marker"] as const;

const PROHIBITED_PLAN_KEYS = [
  "image_prompt",
  "video_prompt",
  "negative_prompt",
  "generate_asset",
  "generated_asset",
  "generated_image",
  "generated_video",
  "text_to_image",
  "text_to_video",
  "diffusion",
  "synthesize",
  "create_image",
  "create_video",
  "new_ui_action",
  "browser_action",
  "action_spec",
] as const;

const REQUIRED_LOCKED_FIELDS = [
  "source_authority",
  "model_role",
  "source_material_policy",
  "script_order_policy",
  "source_artifact_id",
  "source_step_id",
  "source_time_range_ms",
  "required_step_order",
] as const;

const ALLOWED_MODEL_EDITABLE_FIELDS = [
  "purpose",
  "overlays.text",
  "global_style.color_grade",
  "global_style.pacing",
  "global_style.transition_style",
  "operations.zoom",
  "operations.speed",
  "operations.style",
] as const;

type EditOperationType = (typeof ALLOWED_EDIT_OPERATIONS)[number];
type OverlayType = (typeof ALLOWED_OVERLAY_TYPES)[number];
type DemoEditSourceAuthority = (typeof ALLOWED_SOURCE_AUTHORITIES)[number];

export interface RenderRequest {
  graph?: DemoWorkflowGraph;
  recording_paths?: string[];
  output_dir?: string;
  duration_sec?: number;
  recording_run_spec?: RecordingRunSpec;
  execution_trace?: ExecutionTrace;
  execution_trace_path?: string;
  generated_assets?: ArtifactRef[];
  artifact_manifest_path?: string;
  recording_result_package?: RecordingResultPackage;
  recording_result_package_path?: string;
  asset_timeline_catalog?: AssetTimelineCatalog;
  edit_plan?: DemoEditPlan;
  render_profile?: RenderProfile;
  model_execution?: ModelExecutionAudit;
}

export interface ModelExecutionAudit {
  invoked: boolean;
  provider?: string;
  model?: string;
  request_trace_id?: string;
  plan_source: "deterministic_fixture" | "server_director";
  provider_call_status?: string;
  real_call_made?: boolean;
  provider_output_adopted?: boolean;
  suggestion_origin?: "deterministic_server_director" | "provider_output";
  suggestion_id?: string;
  patch_id?: string;
  patch_applied?: boolean;
  adopted_shot_ids?: string[];
  note?: string;
}

export interface RenderProfile {
  mode?: "preview" | "final";
  width?: number;
  height?: number;
  fps?: number;
  format?: "mp4" | "webm" | "mov";
  preset?: string;
  crf?: number;
}

export interface RenderResult {
  video_path: string;
  source_reference_video_path?: string;
  step_by_step_docs_path: string;
  asset_timeline_catalog_path: string;
  demo_edit_plan_path: string;
  validation_report_path: string;
  render_manifest_path: string;
  media_normalization_report_path?: string;
  requirement_satisfaction_report_path?: string;
  asset_timeline_catalog: AssetTimelineCatalog;
  demo_edit_plan: DemoEditPlan;
  validation_report: DemoEditPlanValidationReport;
  media_normalization_report?: MediaNormalizationReport;
  requirement_satisfaction_report?: RequirementSatisfactionReport;
}

interface DemoWorkflowGraph {
  id: string;
  version?: number;
  nodes?: GraphNode[];
  execution?: {
    viewports?: ViewportSpec[];
  };
  assets?: {
    demo_video_60s?: boolean;
    screenshot_pack?: boolean;
    step_by_step_docs?: boolean;
    target_duration_sec?: number;
    requested_assets?: RequestedAsset[];
  };
}

interface GraphNode {
  id: string;
  action?: string;
  selector?: string;
  expected_outcome?: string;
  is_screenshot?: boolean;
  action_spec?: {
    type?: string;
    target?: {
      selector?: string;
      test_id?: string;
      text?: string;
      label?: string;
      role?: string;
      url?: string;
    };
  };
  capture?: {
    screenshot?: boolean;
    video?: boolean;
    zoom?: boolean;
    full_page?: boolean;
    focus_selector?: string;
    asset_role?: string;
  };
  duration_hint_ms?: number;
}

interface RequestedAsset {
  id?: string;
  kind?: string;
  format?: string;
  duration_sec?: number;
  required?: boolean;
  status?: string;
}

interface ViewportSpec {
  name?: string;
  width?: number;
  height?: number;
  device?: string;
}

interface RecordingRunSpec {
  timeline?: {
    target_duration_sec?: number;
    max_duration_sec?: number;
    capture_windows?: Array<{
      id?: string;
      node_id?: string;
      start_ms?: number;
      duration_ms?: number;
      role?: string;
    }>;
  };
  browser?: {
    viewports?: ViewportSpec[];
  };
  outputs?: {
    raw_recording?: boolean;
    final_video?: boolean;
    screenshot_pack?: boolean;
    step_by_step_docs?: boolean;
    trace?: boolean;
    output_formats?: string[];
    resolution_width?: number;
    resolution_height?: number;
  };
}

interface ExecutionTrace {
  id?: string;
  workflow_graph_id?: string;
  graph_version?: number;
  started_at?: string;
  completed_at?: string;
  pass_rate?: number;
  step_results?: StepResult[];
  artifacts?: ArtifactRef[];
  environment?: Record<string, string>;
}

interface StepResult {
  node_id: string;
  status: string;
  started_at?: string;
  completed_at?: string;
  duration_ms?: number;
  observed_state?: string;
  artifacts?: ArtifactRef[];
}

interface ArtifactRef {
  id: string;
  kind: string;
  uri: string;
  mime_type?: string;
  label?: string;
  sha256?: string;
  size_bytes?: number;
  created_at?: string;
  sensitive?: boolean;
  source_node_id?: string;
  metadata?: Record<string, unknown>;
}

interface ArtifactManifest {
  run_id?: string;
  workflow_graph_id?: string;
  artifacts?: ArtifactRef[];
}

interface RecordingResultPackage {
  result_id?: string;
  source_package_id?: string;
  execution_trace?: ExecutionTrace;
  step_results?: StepResult[];
  generated_assets?: ArtifactRef[];
}

interface LoadedRenderInputs {
  graph?: DemoWorkflowGraph;
  executionTrace?: ExecutionTrace;
  artifactManifest?: ArtifactManifest;
  recordingResultPackage?: RecordingResultPackage;
  generatedAssets: ArtifactRef[];
}

export interface AssetTimelineCatalog {
  schema_version: string;
  catalog_id: string;
  workflow_graph_id: string;
  graph_version: number;
  run_id: string;
  source: {
    recording_result_package_id?: string;
    execution_trace_id?: string;
    generated_at: string;
  };
  constraints: {
    source_material_only: true;
    prohibit_new_image_or_video_generation: true;
    script_is_primary_storyline: true;
    allowed_edit_operations: EditOperationType[];
    prohibited_plan_keys: string[];
  };
  timeline: {
    duration_ms: number;
    recording_artifact_id?: string;
  };
  steps: TimelineStep[];
  artifacts: TimelineArtifact[];
}

interface TimelineStep {
  step_id: string;
  order: number;
  action: string;
  status: string;
  required: boolean;
  start_ms: number;
  end_ms: number;
  duration_ms: number;
  expected_outcome?: string;
  observed_state?: string;
  source_node?: {
    selector?: string;
    focus_selector?: string;
    asset_role?: string;
  };
  artifacts: string[];
}

interface BrowserTargetGeometry {
  schema_version: "demoops.browser_target_geometry.v1";
  target_semantic_id: string;
  resolution_strategy: string;
  selector_digest_sha256: string;
  captured_at: string;
  recording_offset_ms: number;
  viewport: { width: number; height: number; dpr: number };
  element_box_css_px: { x: number; y: number; width: number; height: number };
  element_box_normalized: { x: number; y: number; width: number; height: number };
  screenshot_artifact_id?: string;
  confidence: number;
}

interface TimelineArtifact {
  id: string;
  kind: string;
  uri: string;
  mime_type?: string;
  label?: string;
  sha256?: string;
  size_bytes?: number;
  sensitive?: boolean;
  source_step_id?: string;
  asset_role?: string;
  include_in_demo?: boolean;
  capture_scope?: string;
  metadata?: Record<string, unknown>;
  duration_ms?: number;
  local_path?: string;
}

export interface DemoEditPlan {
  schema_version?: string;
  plan_id: string;
  catalog_id?: string;
  objective?: string;
  source_authority: DemoEditSourceAuthority;
  model_role: typeof DEMO_EDIT_MODEL_ROLE;
  source_material_policy: "existing_assets_only";
  script_order_policy: "preserve_required_step_order";
  locked_fields: string[];
  model_editable_fields: string[];
  target_duration_ms?: number;
  shots: DemoEditShot[];
  global_style?: {
    color_grade?: string;
    pacing?: string;
    transition_style?: string;
  };
  audio?: {
    mode: "source" | "mute";
    volume_percent: number;
    split_points_ms?: number[];
    segment_settings?: Array<{ start_ms: number; end_ms: number; mode: "source" | "mute"; volume_percent: number }>;
  };
  narrations?: Array<{
    id: string;
    source_artifact_id: string;
    source_time_range_ms?: [number, number];
    output_time_range_ms: [number, number];
    volume_percent: number;
    duck_source_audio?: boolean;
    duck_source_to_percent?: number;
    source: "user_recorded" | "user_uploaded" | "tts_confirmed";
  }>;
  caption_cues?: Array<{
    id: string;
    output_range_ms: [number, number];
    text: string;
    source: "user_configured" | "asr_confirmed" | "model_confirmed";
  }>;
}

interface DemoEditShot {
  id: string;
  source_artifact_id: string;
  source_step_id?: string;
  source_time_range_ms?: [number, number];
  presentation_kind?: "video" | "still";
  output_duration_ms?: number;
  purpose: string;
  operations?: EditOperation[];
  overlays?: EditOverlay[];
}

interface EditOperation {
  type: EditOperationType;
  start_ms?: number;
  end_ms?: number;
  focus_selector?: string;
  zoom?: number;
  x?: number;
  y?: number;
  scale?: number;
  speed?: number;
  style?: string;
}

interface EditOverlay {
  type: OverlayType;
  id?: string;
  text?: string;
  source_step_id?: string;
  target_selector?: string;
  target_evidence_artifact_id?: string;
  geometry_source?: string;
  start_ms?: number;
  end_ms?: number;
  shape?: "rectangle" | "circle" | "polygon" | "star" | "line" | "arrow" | string;
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  color?: string;
  stroke_width?: number;
  rotation?: number;
  opacity?: number;
  fill_color?: string;
  fill_opacity?: number;
  tilt_preset?: string;
  scale_x?: number;
  scale_y?: number;
  tilt_x?: number;
  tilt_y?: number;
}

export interface DemoEditPlanValidationReport {
  schema_version: string;
  valid: boolean;
  checked_at: string;
  plan_id?: string;
  errors: ValidationFinding[];
  warnings: ValidationFinding[];
}

interface ValidationFinding {
  code: string;
  message: string;
  path?: string;
}

interface CompositorResult {
  status: "rendered" | "planned";
  video_path: string;
  method: "ffmpeg_trim_concat" | "copy_source_recording" | "pending_compositor";
  ffmpeg_available?: boolean;
  quality_status?: "ok" | "degraded";
  video_codec?: string;
  audio_codec?: string;
  audio_mode?: "source" | "mute";
  source_artifact_id?: string;
  source_uri?: string;
  output_container?: string;
  shot_plan?: CompositorShot[];
  planned_operations?: string[];
  applied_operations?: string[];
  skipped_operations?: Array<{ type: string; reason: string }>;
  encoding_audit?: VideoEncodingAudit;
  fallback_reason?: string;
  note: string;
}

interface VideoEncodingAudit {
  source_master_preserved: true;
  segment_video_encode_passes: number;
  concat_video_mode: "stream_copy" | "not_applicable" | "fallback_transcode";
  post_process_video_mode: "stream_copy" | "not_applicable";
  max_lossy_video_encode_passes_per_output_frame: number;
  global_captions_embedded_during_segment_encode: boolean;
}

interface CompositorShot {
  id: string;
  source_artifact_id: string;
  source_step_id?: string;
  source_uri?: string;
  source_path?: string;
  source_time_range_ms?: [number, number];
  presentation_kind: "video" | "still";
  duration_ms: number;
  operations: string[];
  edit_operations: Array<{
    type: string;
    start_ms?: number;
    end_ms?: number;
    zoom?: number;
    x?: number;
    y?: number;
    scale?: number;
    speed?: number;
    style?: string;
  }>;
  overlays: CompositorOverlay[];
}

interface CompositorOverlay {
  type: string;
  id?: string;
  text?: string;
  start_ms?: number;
  end_ms?: number;
  shape?: string;
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  color?: string;
  stroke_width?: number;
  rotation?: number;
  opacity?: number;
  fill_color?: string;
  fill_opacity?: number;
  tilt_preset?: string;
  scale_x?: number;
  scale_y?: number;
  tilt_x?: number;
  tilt_y?: number;
  target_evidence_artifact_id?: string;
  geometry_source?: string;
  target_geometry_verified?: boolean;
  target_geometry_issue?: string;
  coordinate_space?: "post_edit_normalized";
}

interface VideoCodecConfig {
  name: string;
  args: string[];
}

interface CaptionCue {
  start_ms: number;
  end_ms: number;
  text: string;
}

interface NarrationRenderInput {
  id: string;
  source_path: string;
  source_time_range_ms: [number, number];
  output_time_range_ms: [number, number];
  volume_percent: number;
  duck_source_audio: boolean;
  duck_source_to_percent: number;
}

interface MediaNormalizationTarget {
  container: "mp4";
  video_codec: "h264";
  audio_codec: "aac";
  width: number;
  height: number;
  fps: number;
  pixel_format: "yuv420p";
}

interface MediaProbeSummary {
  format?: string;
  duration_sec?: number;
  video_codec?: string;
  audio_codec?: string;
  width?: number;
  height?: number;
  fps?: number;
  pixel_format?: string;
}

interface MediaNormalizationReport {
  schema_version: typeof MEDIA_NORMALIZATION_REPORT_SCHEMA_VERSION;
  status: "ok" | "skipped" | "failed";
  checked_at: string;
  target: MediaNormalizationTarget;
  ffmpeg_available: boolean;
  ffprobe_available?: boolean;
  source_artifact_id?: string;
  source_uri?: string;
  source_path?: string;
  reference_video_path?: string;
  input?: MediaProbeSummary;
  output?: MediaProbeSummary;
  validation?: Record<string, boolean>;
  error?: string;
  note?: string;
}

interface RequirementSatisfactionReport {
  schema_version: typeof REQUIREMENT_SATISFACTION_REPORT_SCHEMA_VERSION;
  status: "satisfied" | "satisfied_with_warnings" | "not_satisfied";
  checked_at: string;
  source_priority: {
    product_requirements: string[];
    recording_execution_plan: string[];
    actual_material: string[];
  };
  adopted_values: {
    target_duration_sec: RequirementAdoptedValue<number>;
    output_resolution: RequirementAdoptedValue<{ width: number; height: number }>;
    final_video_format: RequirementAdoptedValue<string>;
  };
  requested: {
    product_target_duration_sec: number | undefined;
    recording_target_duration_sec: number | undefined;
    recording_max_duration_sec: number | undefined;
    final_video_formats: string[] | undefined;
    screenshot_pack: boolean;
    step_by_step_docs: boolean;
    graph_viewports: ViewportSpec[] | undefined;
    recording_viewports: ViewportSpec[] | undefined;
    output_resolution: { width: number; height: number } | undefined;
    required_assets: RequestedAsset[] | undefined;
    required_step_ids: string[];
    screenshot_step_ids: string[];
  };
  actual: {
    demo_video_path: string | undefined;
    demo_video_format: string | undefined;
    source_reference_video_path: string | undefined;
    source_reference_video_format: string | undefined;
    media_normalization_status: string | undefined;
    screenshot_count: number;
    step_by_step_docs_path: string | undefined;
    timeline_duration_sec: number;
    edit_plan_target_duration_sec: number | undefined;
    rendered_video_duration_sec?: number;
    rendered_video_expected_duration_sec?: number;
    rendered_operations: string[];
    planned_overlay_types: string[];
    applied_overlay_types: string[];
    represented_step_ids: string[];
    passed_step_count: number;
    failed_step_ids: string[];
    screenshot_step_ids: string[];
  };
  step_checks: RequirementStepCheck[];
  asset_checks: RequirementAssetCheck[];
  checks: RequirementCheck[];
  warnings: RequirementFinding[];
  errors: RequirementFinding[];
}

interface RequirementAdoptedValue<T> {
  value?: T | undefined;
  source: string;
  reason: string;
  ignored?: Array<{ source: string; value?: T | undefined; reason: string }>;
}

interface RequirementStepCheck {
  step_id: string;
  action: string;
  required: boolean;
  execution_status: string;
  represented_in_edit_plan: boolean;
  screenshot_required: boolean;
  screenshot_artifact_ids: string[];
  status: "pass" | "warn" | "fail";
  findings: RequirementFinding[];
}

interface RequirementAssetCheck {
  kind: string;
  required: boolean;
  status: "pass" | "warn" | "fail";
  artifact_ids: string[];
  message: string;
}

interface RequirementCheck {
  code: string;
  status: "pass" | "warn" | "fail";
  message: string;
}

interface RequirementFinding {
  code: string;
  message: string;
  path?: string;
}

const MEDIA_NORMALIZATION_TARGET: MediaNormalizationTarget = {
  container: "mp4",
  video_codec: "h264",
  audio_codec: "aac",
  width: 2560,
  height: 1440,
  fps: 30,
  pixel_format: "yuv420p",
};

export async function render(request: RenderRequest): Promise<RenderResult> {
  const outputDir = request.output_dir || "artifacts/rendered";
  await mkdir(outputDir, { recursive: true });

  const loaded = await loadRenderInputs(request);
  const catalog = request.asset_timeline_catalog || (await buildAssetTimelineCatalog(request, loaded));
  const editPlan = request.edit_plan ? normalizeEditPlan(request.edit_plan, catalog) : defaultEditPlan(catalog, request.duration_sec);
  const validationReport = validateDemoEditPlan(editPlan, catalog);

  if (!validationReport.valid) {
    throw new Error(`demo edit plan rejected: ${validationReport.errors.map((error) => `${error.code}: ${error.message}`).join("; ")}`);
  }

  const targetDurationSec = Math.max(1, Math.round((editPlan.target_duration_ms || (request.duration_sec || 60) * 1000) / 1000));
  const requestedFormats = requestedFinalVideoFormats(request.graph, request.recording_run_spec);
  const targetContainer = request.render_profile?.format || preferredFinalVideoContainer(requestedFormats);
  const compositor = await composeFinalVideo(outputDir, targetDurationSec, catalog, editPlan, targetContainer, request.render_profile);
  if (request.render_profile && compositor.status !== "rendered") {
    throw new Error(`editor compositor did not render the edit plan: ${compositor.fallback_reason || compositor.method}`);
  }
  if (request.render_profile && compositor.method !== "ffmpeg_trim_concat") {
    throw new Error(`editor compositor refused degraded output: ${compositor.fallback_reason || compositor.method}`);
  }
  const mediaNormalization =
    request.render_profile?.mode === "preview"
      ? await skippedPreviewNormalization(outputDir, request.render_profile)
      : await normalizeSourceReferenceVideo(outputDir, catalog, editPlan, compositor.video_path, mediaNormalizationTarget(request.render_profile));
  const sourceReferenceVideoPath = mediaNormalization.report.status === "ok" ? mediaNormalization.report.reference_video_path : undefined;
  const videoPath = sourceReferenceVideoPath || compositor.video_path;
  const stepDocsPath = path.join(outputDir, "step_by_step.md");
  const catalogPath = path.join(outputDir, "asset_timeline_catalog.json");
  const editPlanPath = path.join(outputDir, "demo_edit_plan.json");
  const validationReportPath = path.join(outputDir, "demo_edit_plan_validation.json");
  const renderManifestPath = path.join(outputDir, "render_manifest.json");
  const requirementReport = buildRequirementSatisfactionReport(request, catalog, editPlan, compositor, mediaNormalization.report, videoPath, stepDocsPath);
  const requirementReportPath = path.join(outputDir, "requirement_satisfaction_report.json");

  await writeJSON(catalogPath, catalog);
  await writeJSON(editPlanPath, editPlan);
  await writeJSON(validationReportPath, validationReport);
  await writeJSON(requirementReportPath, requirementReport);
  await writeJSON(renderManifestPath, {
    schema_version: "demoops.render_manifest.v1",
    status: compositor.status,
    source_material_policy: "existing_assets_only",
    source_authority: DEMO_EDIT_SOURCE_AUTHORITY,
    model_role: DEMO_EDIT_MODEL_ROLE,
    compositor,
    note: compositor.note,
    video_path: videoPath,
    source_reference_video_path: sourceReferenceVideoPath,
    step_by_step_docs_path: stepDocsPath,
    asset_timeline_catalog_path: catalogPath,
    demo_edit_plan_path: editPlanPath,
    validation_report_path: validationReportPath,
    media_normalization_report_path: mediaNormalization.report_path,
    media_normalization_report: mediaNormalization.report,
    model_execution: request.model_execution || {
      invoked: false,
      plan_source: "deterministic_fixture",
      note: "No model call was made; the supplied edit plan was executed as-is.",
    },
    requirement_satisfaction_report_path: requirementReportPath,
    requirement_satisfaction_report: requirementReport,
  });
  await writeFile(stepDocsPath, stepDocsFor(catalog, editPlan), "utf8");

  const result: RenderResult = {
    video_path: videoPath,
    step_by_step_docs_path: stepDocsPath,
    asset_timeline_catalog_path: catalogPath,
    demo_edit_plan_path: editPlanPath,
    validation_report_path: validationReportPath,
    render_manifest_path: renderManifestPath,
    media_normalization_report_path: mediaNormalization.report_path,
    requirement_satisfaction_report_path: requirementReportPath,
    asset_timeline_catalog: catalog,
    demo_edit_plan: editPlan,
    validation_report: validationReport,
    media_normalization_report: mediaNormalization.report,
    requirement_satisfaction_report: requirementReport,
  };
  if (sourceReferenceVideoPath) {
    result.source_reference_video_path = sourceReferenceVideoPath;
  }
  return result;
}

async function loadRenderInputs(request: RenderRequest): Promise<LoadedRenderInputs> {
  const recordingResultPackage =
    request.recording_result_package || (request.recording_result_package_path ? await readJSON<RecordingResultPackage>(request.recording_result_package_path) : undefined);
  const executionTrace =
    request.execution_trace || recordingResultPackage?.execution_trace || (request.execution_trace_path ? await readJSON<ExecutionTrace>(request.execution_trace_path) : undefined);
  const artifactManifest = request.artifact_manifest_path ? await readJSON<ArtifactManifest>(request.artifact_manifest_path) : undefined;
  const generatedAssets = uniqueArtifacts([
    ...(request.generated_assets || []),
    ...(recordingResultPackage?.generated_assets || []),
    ...(executionTrace?.artifacts || []),
    ...(artifactManifest?.artifacts || []),
    ...(request.recording_paths || []).map((recordingPath, index) => artifactFromRecordingPath(recordingPath, index)),
  ]);

  const result: LoadedRenderInputs = { generatedAssets };
  if (request.graph) result.graph = request.graph;
  if (executionTrace) result.executionTrace = executionTrace;
  if (artifactManifest) result.artifactManifest = artifactManifest;
  if (recordingResultPackage) result.recordingResultPackage = recordingResultPackage;
  return result;
}

async function buildAssetTimelineCatalog(request: RenderRequest, loaded: LoadedRenderInputs): Promise<AssetTimelineCatalog> {
  const graph = request.graph || loaded.graph;
  const trace = loaded.executionTrace;
  const workflowGraphID = graph?.id || trace?.workflow_graph_id || loaded.artifactManifest?.workflow_graph_id || "unknown_graph";
  const graphVersion = graph?.version || trace?.graph_version || 1;
  const runID = loaded.artifactManifest?.run_id || trace?.id?.replace(/^trace_/, "") || `render_${safeName(workflowGraphID)}`;
  const nodesByID = new Map((graph?.nodes || []).map((node) => [node.id, node]));
  const traceSteps = trace?.step_results || loaded.recordingResultPackage?.step_results || [];
  const steps = traceSteps.length > 0 ? buildTimelineStepsFromTrace(traceSteps, nodesByID) : buildTimelineStepsFromGraph(graph?.nodes || []);
  const traceDurationMS = durationFromDates(trace?.started_at, trace?.completed_at);
  const requestedDurationMS = (request.duration_sec || 0) * 1000;
  const totalDurationMS = Math.max(maxStepEnd(steps), traceDurationMS, requestedDurationMS);
  const recordingArtifact = firstRecordingArtifact(loaded.generatedAssets);
  const artifacts = await Promise.all(
    loaded.generatedAssets.map((artifact) => timelineArtifactFromRef(artifact, artifact.id === recordingArtifact?.id ? totalDurationMS : undefined)),
  );

  return {
    schema_version: ASSET_TIMELINE_CATALOG_SCHEMA_VERSION,
    catalog_id: `catalog_${safeName(runID)}`,
    workflow_graph_id: workflowGraphID,
    graph_version: graphVersion,
    run_id: runID,
    source: sourceInfo(loaded, trace),
    constraints: {
      source_material_only: true,
      prohibit_new_image_or_video_generation: true,
      script_is_primary_storyline: true,
      allowed_edit_operations: [...ALLOWED_EDIT_OPERATIONS],
      prohibited_plan_keys: [...PROHIBITED_PLAN_KEYS],
    },
    timeline: timelineInfo(totalDurationMS, recordingArtifact),
    steps,
    artifacts,
  };
}

function buildTimelineStepsFromTrace(stepResults: StepResult[], nodesByID: Map<string, GraphNode>): TimelineStep[] {
  let cursor = 0;
  return stepResults.map((step, index) => {
    const node = nodesByID.get(step.node_id);
    const durationMS = positiveDuration(step.duration_ms) || durationFromDates(step.started_at, step.completed_at) || durationHint(node);
    const startMS = cursor;
    const endMS = startMS + durationMS;
    cursor = endMS;
    return timelineStep(step.node_id, index, actionForNode(node), step.status, startMS, endMS, node, step.observed_state, step.artifacts || []);
  });
}

function buildTimelineStepsFromGraph(nodes: GraphNode[]): TimelineStep[] {
  let cursor = 0;
  return nodes.map((node, index) => {
    const durationMS = durationHint(node);
    const startMS = cursor;
    const endMS = startMS + durationMS;
    cursor = endMS;
    return timelineStep(node.id, index, actionForNode(node), "planned", startMS, endMS, node, undefined, []);
  });
}

function timelineStep(
  stepID: string,
  order: number,
  action: string,
  status: string,
  startMS: number,
  endMS: number,
  node: GraphNode | undefined,
  observedState: string | undefined,
  artifacts: ArtifactRef[],
): TimelineStep {
  const step: TimelineStep = {
    step_id: stepID,
    order,
    action,
    status,
    required: action !== "wait",
    start_ms: startMS,
    end_ms: endMS,
    duration_ms: endMS - startMS,
    artifacts: artifacts.map((artifact) => artifact.id),
  };
  if (node?.expected_outcome) step.expected_outcome = node.expected_outcome;
  if (observedState) step.observed_state = observedState;
  const sourceNode = sourceNodeInfo(node);
  if (sourceNode) step.source_node = sourceNode;
  return step;
}

export function defaultEditPlan(catalog: AssetTimelineCatalog, durationSec?: number): DemoEditPlan {
  const demoArtifacts = catalog.artifacts.filter(isDemoMaterial);
  const recordingArtifactID =
    findDemoArtifactByID(catalog, catalog.timeline.recording_artifact_id)?.id || demoArtifacts.find((artifact) => artifact.kind === "raw_recording")?.id;
  const fallbackArtifactID = recordingArtifactID || demoArtifacts[0]?.id || "missing_source_artifact";
  const eligibleSteps = catalog.steps.filter((step) => step.status !== "failed");
  const steps = eligibleSteps.length > 0 ? eligibleSteps : catalog.steps;
  const targetDurationMS = (durationSec || Math.max(1, Math.ceil(catalog.timeline.duration_ms / 1000))) * 1000;
  const stillArtifacts = demoArtifacts.filter(isPresentationStillArtifact);
  // The visible local-test handoff intentionally starts without a recording
  // context so manual credentials can never enter a video artifact. Its final
  // MP4 is composed only from post-action, redacted screenshots.
  if (!recordingArtifactID && stillArtifacts.length > 0) {
    const candidates = steps.flatMap((step, stepIndex) => {
      const geometryEvidence = targetGeometryEvidenceForStep(catalog, step);
      return presentationStillArtifactsForStep(catalog, step, geometryEvidence).map((artifact, artifactIndex) => ({
        step,
        stepIndex,
        artifact,
        artifactIndex,
        geometryEvidence: geometryEvidence?.artifact.id === artifact.id ? geometryEvidence : undefined,
      }));
    });
    const selected = selectStillCandidatesForDuration(candidates, steps.length, targetDurationMS);
    const durations = distributeStillDurations(selected.length, targetDurationMS);
    const stillShots = selected.map((candidate, index) => {
      const shot = defaultShotForStep(candidate.step, candidate.stepIndex, candidate.artifact.id, catalog, candidate.geometryEvidence ?? null);
      shot.id = `shot_${String(index + 1).padStart(3, "0")}_${safeName(candidate.step.step_id)}_${candidate.artifactIndex + 1}`;
      shot.presentation_kind = "still";
      shot.output_duration_ms = durations[index] as number;
      delete shot.source_time_range_ms;
      shot.operations = [];
      const shotDurationMS = shot.output_duration_ms;
      shot.overlays = (shot.overlays || []).map((overlay) => ({
        ...overlay,
        start_ms: Math.min(overlay.start_ms ?? 0, Math.max(0, shotDurationMS - 100)),
        end_ms: Math.min(overlay.end_ms ?? shotDurationMS, shotDurationMS),
      }));
      return shot;
    });
    const actualDurationMS = stillShots.reduce((total, shot) => total + (shot.output_duration_ms || 0), 0);
    return {
      schema_version: DEMO_EDIT_PLAN_SCHEMA_VERSION,
      plan_id: `edit_plan_${safeName(catalog.run_id)}`,
      catalog_id: catalog.catalog_id,
      objective: "Create a concise demo from verified post-action screenshots. Do not create new images or video.",
      source_authority: DEMO_EDIT_SOURCE_AUTHORITY,
      model_role: DEMO_EDIT_MODEL_ROLE,
      source_material_policy: "existing_assets_only",
      script_order_policy: "preserve_required_step_order",
      locked_fields: [...REQUIRED_LOCKED_FIELDS],
      model_editable_fields: [...ALLOWED_MODEL_EDITABLE_FIELDS],
      target_duration_ms: actualDurationMS,
      shots: stillShots,
      global_style: { color_grade: "neutral_product_ui", pacing: "clear_and_direct", transition_style: "simple_cut" },
      audio: { mode: "mute", volume_percent: 0 },
    };
  }
  const shots = capDefaultShotsToTargetDuration(
    steps.map((step, index) => {
      const geometryEvidence = targetGeometryEvidenceForStep(catalog, step);
      const sourceArtifactID = recordingArtifactID || geometryEvidence?.artifact.id || step.artifacts[0] || fallbackArtifactID;
      return defaultShotForStep(step, index, sourceArtifactID, catalog, geometryEvidence);
    }),
    targetDurationMS,
  );

  return {
    schema_version: DEMO_EDIT_PLAN_SCHEMA_VERSION,
    plan_id: `edit_plan_${safeName(catalog.run_id)}`,
    catalog_id: catalog.catalog_id,
    objective: "Create a concise demo from the recorded product interaction. Do not create new images or video.",
    source_authority: DEMO_EDIT_SOURCE_AUTHORITY,
    model_role: DEMO_EDIT_MODEL_ROLE,
    source_material_policy: "existing_assets_only",
    script_order_policy: "preserve_required_step_order",
    locked_fields: [...REQUIRED_LOCKED_FIELDS],
    model_editable_fields: [...ALLOWED_MODEL_EDITABLE_FIELDS],
    target_duration_ms: targetDurationMS,
    shots,
    global_style: {
      color_grade: "neutral_product_ui",
      pacing: "clear_and_direct",
      transition_style: "simple_cut",
    },
    audio: { mode: "source", volume_percent: 100 },
  };
}

function defaultShotForStep(
  step: TimelineStep,
  index: number,
  sourceArtifactID: string,
  catalog?: AssetTimelineCatalog,
  knownGeometryEvidence?: { artifact: TimelineArtifact; geometry: BrowserTargetGeometry } | null,
): DemoEditShot {
  const geometryEvidence = knownGeometryEvidence === null
    ? undefined
    : knownGeometryEvidence || (catalog ? targetGeometryEvidenceForStep(catalog, step) : undefined);
  const sourceArtifact = catalog ? findDemoArtifactByID(catalog, sourceArtifactID) : undefined;
  const still = Boolean(sourceArtifact && isPresentationStillArtifact(sourceArtifact));
  const shot: DemoEditShot = {
    id: `shot_${String(index + 1).padStart(3, "0")}_${safeName(step.step_id)}`,
    source_artifact_id: sourceArtifactID,
    source_step_id: step.step_id,
    purpose: step.expected_outcome || step.observed_state || `Show ${step.action} step ${step.step_id}`,
    ...(still
      ? { presentation_kind: "still" as const, output_duration_ms: Math.max(250, Math.min(15_000, step.duration_ms)), operations: [] }
      : { source_time_range_ms: [step.start_ms, step.end_ms] as [number, number], operations: defaultOperationsForStep(step, geometryEvidence?.geometry) }),
    overlays: [
      {
        type: "caption",
        text: step.expected_outcome || titleForAction(step),
        source_step_id: step.step_id,
        start_ms: 0,
        end_ms: Math.min(step.duration_ms, 3000),
      },
    ],
  };
  if (geometryEvidence) {
    const box = paddedNormalizedTargetBox(geometryEvidence.geometry.element_box_normalized);
    shot.overlays?.push({
      type: "highlight_box",
      shape: "rectangle",
      x: box.x,
      y: box.y,
      width: box.width,
      height: box.height,
      color: "#1da7ff",
      stroke_width: 4,
      start_ms: 0,
      end_ms: Math.min(step.duration_ms, 1500),
      target_evidence_artifact_id: geometryEvidence.artifact.id,
      geometry_source: "browser_agent_target_geometry_v1",
    });
    shot.overlays?.push({
      type: "cursor_highlight",
      color: "#ffd166",
      stroke_width: 5,
      start_ms: 0,
      end_ms: Math.min(step.duration_ms, 1200),
      target_evidence_artifact_id: geometryEvidence.artifact.id,
      geometry_source: "browser_agent_target_geometry_v1",
    });
  }
  return shot;
}

function defaultOperationsForStep(step: TimelineStep, geometry?: BrowserTargetGeometry): EditOperation[] {
  const operations: EditOperation[] = [{ type: "trim", start_ms: step.start_ms, end_ms: step.end_ms }];
  const focusSelector = step.source_node?.focus_selector || step.source_node?.selector;
  if (geometry) {
    const centerX = geometry.element_box_normalized.x + geometry.element_box_normalized.width / 2;
    const centerY = geometry.element_box_normalized.y + geometry.element_box_normalized.height / 2;
    const zoom = targetAwareZoom(geometry.element_box_normalized);
    const zoomPan: EditOperation = {
      type: "zoom_pan",
      zoom,
      x: panForTargetCenter(centerX, zoom),
      y: panForTargetCenter(centerY, zoom),
      start_ms: step.start_ms,
      end_ms: step.end_ms,
    };
    if (focusSelector) zoomPan.focus_selector = focusSelector;
    operations.push(zoomPan);
  }
  return operations;
}

function targetGeometryEvidenceForStep(catalog: AssetTimelineCatalog, step: TimelineStep): { artifact: TimelineArtifact; geometry: BrowserTargetGeometry } | undefined {
  for (const artifactID of step.artifacts) {
    const artifact = findDemoArtifactByID(catalog, artifactID);
    const geometry = targetGeometryFromMetadata(artifact?.metadata);
    if (artifact && geometry && (!geometry.screenshot_artifact_id || geometry.screenshot_artifact_id === artifact.id)) return { artifact, geometry };
  }
  const artifact = catalog.artifacts.find((candidate) => candidate.source_step_id === step.step_id && targetGeometryFromMetadata(candidate.metadata));
  const geometry = targetGeometryFromMetadata(artifact?.metadata);
  return artifact && geometry ? { artifact, geometry } : undefined;
}

function targetGeometryFromMetadata(metadata?: Record<string, unknown>): BrowserTargetGeometry | undefined {
  const value = metadata?.target_geometry as BrowserTargetGeometry | undefined;
  if (!value || value.schema_version !== "demoops.browser_target_geometry.v1" || value.confidence !== 1) return undefined;
  const box = value.element_box_normalized;
  if (!box || ![box.x, box.y, box.width, box.height].every(Number.isFinite) || box.x < 0 || box.y < 0 || box.width <= 0 || box.height <= 0 || box.x + box.width > 1.000001 || box.y + box.height > 1.000001) return undefined;
  if (!value.selector_digest_sha256?.match(/^[a-f0-9]{64}$/)) return undefined;
  return value;
}

function paddedNormalizedTargetBox(box: BrowserTargetGeometry["element_box_normalized"]): { x: number; y: number; width: number; height: number } {
  const padX = Math.max(0.008, box.width * 0.12);
  const padY = Math.max(0.008, box.height * 0.2);
  const x = clamp01(box.x - padX);
  const y = clamp01(box.y - padY);
  return { x, y, width: Math.min(1 - x, box.width + padX * 2), height: Math.min(1 - y, box.height + padY * 2) };
}

function targetAwareZoom(box: BrowserTargetGeometry["element_box_normalized"]): number {
  const dominant = Math.max(box.width, box.height);
  return Math.max(1.08, Math.min(1.8, dominant > 0 ? 0.42 / dominant : 1.18));
}

function panForTargetCenter(center: number, zoom: number): number {
  if (zoom <= 1) return 0.5;
  return clamp01((center * zoom - 0.5) / (zoom - 1));
}

function clamp01(value: number): number {
  return Math.max(0, Math.min(1, value));
}

function normalizeEditPlan(plan: DemoEditPlan, catalog: AssetTimelineCatalog): DemoEditPlan {
  const artifactByID = new Map(catalog.artifacts.map((artifact) => [artifact.id, artifact]));
  const normalizedShots = (plan.shots || []).map((shot) => {
    const artifact = artifactByID.get(shot.source_artifact_id);
    // A screenshot is a presentation still unless an upstream plan explicitly
    // declares it as video. This repairs legacy/director plans that omitted the
    // presentation field while preserving validation for contradictory plans.
    if (!shot.presentation_kind && artifact && isPresentationStillArtifact(artifact)) {
      const { source_time_range_ms: _sourceTimeRange, ...stillShot } = shot;
      const sourceDuration = shot.source_time_range_ms
        ? Math.max(250, shot.source_time_range_ms[1] - shot.source_time_range_ms[0])
        : 1_000;
      return {
        ...stillShot,
        presentation_kind: "still" as const,
        output_duration_ms: Math.min(15_000, sourceDuration),
        operations: [],
      };
    }
    return shot;
  });
  return {
    ...plan,
    shots: normalizedShots,
    schema_version: plan.schema_version || DEMO_EDIT_PLAN_SCHEMA_VERSION,
    catalog_id: plan.catalog_id || catalog.catalog_id,
    source_authority: plan.source_authority || DEMO_EDIT_SOURCE_AUTHORITY,
    model_role: plan.model_role || DEMO_EDIT_MODEL_ROLE,
    source_material_policy: plan.source_material_policy || "existing_assets_only",
    script_order_policy: plan.script_order_policy || "preserve_required_step_order",
    locked_fields: plan.locked_fields || [...REQUIRED_LOCKED_FIELDS],
    model_editable_fields: plan.model_editable_fields || [...ALLOWED_MODEL_EDITABLE_FIELDS],
    audio: plan.audio || { mode: "source", volume_percent: 100 },
    ...(plan.narrations?.length ? { narrations: plan.narrations.map((narration) => ({ ...narration, ...(narration.source_time_range_ms ? { source_time_range_ms: [...narration.source_time_range_ms] as [number, number] } : {}), output_time_range_ms: [...narration.output_time_range_ms] as [number, number] })) } : {}),
    ...(plan.caption_cues?.length ? { caption_cues: plan.caption_cues.map((cue) => ({ ...cue, output_range_ms: [...cue.output_range_ms] as [number, number] })) } : {}),
  };
}

function normalizeAudioPolicy(audio: DemoEditPlan["audio"]): NonNullable<DemoEditPlan["audio"]> {
  return {
    mode: audio?.mode ?? "source",
    volume_percent: audio?.volume_percent ?? 100,
    ...(audio?.split_points_ms?.length ? { split_points_ms: [...audio.split_points_ms] } : {}),
    ...(audio?.segment_settings?.length ? { segment_settings: audio.segment_settings.map((segment) => ({ ...segment })) } : {}),
  };
}

function validateDemoEditPlan(plan: DemoEditPlan, catalog: AssetTimelineCatalog): DemoEditPlanValidationReport {
  const errors: ValidationFinding[] = [];
  const warnings: ValidationFinding[] = [];
  const artifactByID = new Map(catalog.artifacts.map((artifact) => [artifact.id, artifact]));
  const stepByID = new Map(catalog.steps.map((step) => [step.step_id, step]));
  const requiredOrder = new Map(catalog.steps.filter((step) => step.required).map((step) => [step.step_id, step.order]));

  if (plan.schema_version && plan.schema_version !== DEMO_EDIT_PLAN_SCHEMA_VERSION) {
    errors.push(finding("unsupported_schema", `Unsupported edit plan schema ${plan.schema_version}`, "schema_version"));
  }
  if (plan.source_material_policy !== "existing_assets_only") {
    errors.push(finding("invalid_source_policy", "source_material_policy must be existing_assets_only", "source_material_policy"));
  }
  if (plan.script_order_policy !== "preserve_required_step_order") {
    errors.push(finding("invalid_script_order_policy", "script_order_policy must preserve required step order", "script_order_policy"));
  }
  if (!ALLOWED_SOURCE_AUTHORITIES.includes(plan.source_authority)) {
    errors.push(finding("invalid_source_authority", `source_authority must be one of: ${ALLOWED_SOURCE_AUTHORITIES.join(", ")}`, "source_authority"));
  }
  if (plan.model_role !== DEMO_EDIT_MODEL_ROLE) {
    errors.push(finding("invalid_model_role", "model_role must be presentation_optimizer_only", "model_role"));
  }
  if (plan.audio && plan.audio.mode !== "source" && plan.audio.mode !== "mute") {
    errors.push(finding("invalid_audio_mode", "audio.mode must be source or mute", "audio.mode"));
  }
  if (plan.audio && (!Number.isFinite(plan.audio.volume_percent) || plan.audio.volume_percent < 0 || plan.audio.volume_percent > 200)) {
    errors.push(finding("invalid_audio_volume", "audio.volume_percent must be between 0 and 200", "audio.volume_percent"));
  }
  validateAudioSplitPoints(plan, errors);
  validateNarrations(plan, catalog, errors);
  validateCaptionCues(plan, errors);
  validateCollaborationBoundary(plan, errors);
  if (!Array.isArray(plan.shots) || plan.shots.length === 0) {
    errors.push(finding("missing_shots", "DemoEditPlan must contain at least one shot", "shots"));
  }
  errors.push(...prohibitedKeyFindings(plan));

  let lastRequiredStepOrder = -1;
  plan.shots.forEach((shot, shotIndex) => {
    const shotPath = `shots[${shotIndex}]`;
    const artifact = artifactByID.get(shot.source_artifact_id);
    const still = isStillShot(shot);
    if (!artifact) {
      errors.push(finding("unknown_source_artifact", `Shot references missing source artifact ${shot.source_artifact_id}`, `${shotPath}.source_artifact_id`));
    } else if (artifact.sensitive) {
      errors.push(finding("sensitive_source_artifact", `Shot references sensitive artifact ${shot.source_artifact_id}`, `${shotPath}.source_artifact_id`));
    } else if (isGeneratedCandidateArtifact(artifact)) {
      validateGeneratedCandidateShot(shot, artifact, shotPath, errors);
    } else if (!isDemoMaterial(artifact) && !isVerifiedTargetGeometryStillArtifact(artifact)) {
      errors.push(finding("non_demo_source_artifact", `Shot references non-demo/debug artifact ${shot.source_artifact_id}`, `${shotPath}.source_artifact_id`));
    }

    if (shot.source_step_id && !stepByID.has(shot.source_step_id)) {
      errors.push(finding("unknown_source_step", `Shot references missing source step ${shot.source_step_id}`, `${shotPath}.source_step_id`));
    }
    // A verified Browser Agent still is authoritative page evidence for its
    // own source step. It participates in the same immutable order checks as
    // video evidence; unrelated presentation stills do not.
    const representsSourceStep = shotRepresentsSourceStep(shot, artifact);
    if (representsSourceStep && shot.source_step_id && requiredOrder.has(shot.source_step_id)) {
      const currentOrder = requiredOrder.get(shot.source_step_id) as number;
      if (currentOrder < lastRequiredStepOrder) {
        errors.push(finding("required_step_order_changed", "Required script steps cannot be reordered by the edit plan", `${shotPath}.source_step_id`));
      }
      lastRequiredStepOrder = Math.max(lastRequiredStepOrder, currentOrder);
    }

    validateShotPresentation(shot, artifact, shotPath, errors);
    validateShotTimeRange(shot, artifact, catalog, `${shotPath}.source_time_range_ms`, errors);
    validateOperations(shot.operations || [], `${shotPath}.operations`, errors);
    validateOverlays(shot.overlays || [], stepByID, artifactByID, shot.source_step_id, shot.source_artifact_id, still, `${shotPath}.overlays`, errors, warnings);
  });

  const representedSteps = new Set(plan.shots
    .filter((shot) => shotRepresentsSourceStep(shot, artifactByID.get(shot.source_artifact_id)))
    .map((shot) => shot.source_step_id)
    .filter((stepID): stepID is string => Boolean(stepID)));
  for (const step of catalog.steps) {
    if (step.required && step.status !== "failed" && !representedSteps.has(step.step_id)) {
      warnings.push(finding("required_step_omitted", `Required script step ${step.step_id} is not represented in the edit plan`, "shots"));
    }
  }

  const report: DemoEditPlanValidationReport = {
    schema_version: DEMO_EDIT_PLAN_VALIDATION_SCHEMA_VERSION,
    valid: errors.length === 0,
    checked_at: new Date().toISOString(),
    errors,
    warnings,
  };
  if (plan.plan_id) report.plan_id = plan.plan_id;
  return report;
}

// A requested delivery duration is an output contract. Keep the ordered source
// steps, but trim only the final selected range when the capture has a little
// extra settling time after the business outcome.
function capDefaultShotsToTargetDuration(shots: DemoEditShot[], targetDurationMS: number): DemoEditShot[] {
  let remainingMS = Math.max(0, targetDurationMS);
  const selected: DemoEditShot[] = [];
  for (const shot of shots) {
    if (isStillShot(shot)) {
      if (remainingMS <= 0) break;
      const outputDurationMS = Math.max(250, shot.output_duration_ms ?? 0);
      if (outputDurationMS <= remainingMS) {
        remainingMS -= outputDurationMS;
        selected.push(shot);
      } else if (remainingMS >= 250) {
        selected.push({ ...shot, output_duration_ms: remainingMS });
        remainingMS = 0;
      }
      continue;
    }
    const range = shot.source_time_range_ms;
    if (!range || remainingMS <= 0) break;
    const sourceDurationMS = Math.max(0, range[1] - range[0]);
    if (sourceDurationMS <= remainingMS) {
      remainingMS -= sourceDurationMS;
      selected.push(shot);
      continue;
    }
    const endMS = range[0] + remainingMS;
    remainingMS = 0;
    const trimmed: DemoEditShot = { ...shot, source_time_range_ms: [range[0], endMS] };
    if (shot.operations) {
      trimmed.operations = shot.operations.map((operation) => operation.type === "trim" ? { ...operation, end_ms: endMS } : operation);
    }
    if (shot.overlays) {
      trimmed.overlays = shot.overlays.map((overlay) => overlay.type === "caption" ? { ...overlay, end_ms: Math.min(overlay.end_ms ?? sourceDurationMS, endMS - range[0]) } : overlay);
    }
    selected.push(trimmed);
    break;
  }
  return selected;
}

interface ShapeCue {
  type: "rectangle" | "circle" | "polygon" | "star" | "line" | "arrow";
  start_ms: number;
  end_ms: number;
  x: number;
  y: number;
  width: number;
  height: number;
  color: string;
  stroke_width: number;
  rotation: number;
  opacity: number;
  fill_color: string;
  fill_opacity: number;
  scale_x: number;
  scale_y: number;
}

function isStillShot(shot: DemoEditShot): boolean {
  return shot.presentation_kind === "still";
}

function shotRepresentsSourceStep(shot: DemoEditShot, artifact: TimelineArtifact | undefined): boolean {
  if (!shot.source_step_id || !artifact) return false;
  if (!isStillShot(shot)) return true;
  return artifact.source_step_id === shot.source_step_id && isPresentationStillArtifact(artifact);
}

function validateShotPresentation(shot: DemoEditShot, artifact: TimelineArtifact | undefined, shotPath: string, errors: ValidationFinding[]): void {
  if (shot.presentation_kind && shot.presentation_kind !== "video" && shot.presentation_kind !== "still") {
    errors.push(finding("invalid_presentation_kind", "presentation_kind must be video or still", `${shotPath}.presentation_kind`));
    return;
  }
  if (!isStillShot(shot)) {
    if (shot.output_duration_ms !== undefined) {
      errors.push(finding("video_output_duration_not_supported", "video shots derive duration from source_time_range_ms", `${shotPath}.output_duration_ms`));
    }
    return;
  }
  if (!artifact || !isPresentationStillArtifact(artifact)) {
    errors.push(finding("invalid_still_source", "still shots must reference a non-sensitive presentation_only step_screenshot image", `${shotPath}.source_artifact_id`));
  }
  if (shot.source_time_range_ms) {
    errors.push(finding("still_source_time_range_not_allowed", "still shots use output_duration_ms instead of source_time_range_ms", `${shotPath}.source_time_range_ms`));
  }
  if (!Number.isInteger(shot.output_duration_ms) || (shot.output_duration_ms ?? 0) < 250 || (shot.output_duration_ms ?? 0) > 15000) {
    errors.push(finding("invalid_still_output_duration", "still output_duration_ms must be an integer between 250 and 15000", `${shotPath}.output_duration_ms`));
  }
  if ((shot.operations?.length ?? 0) > 0) {
    errors.push(finding("still_operations_not_supported", "still shots do not support edit operations in the first renderer version", `${shotPath}.operations`));
  }
}

function isPresentationStillArtifact(artifact: TimelineArtifact): boolean {
  if (artifact.sensitive || !artifact.mime_type?.startsWith("image/")) return false;
  if (isVerifiedTargetGeometryStillArtifact(artifact)) return true;
  if (artifact.kind === "step_screenshot" && artifact.metadata?.presentation_only === true) return true;
  return artifact.kind === "screenshot" && artifact.include_in_demo === true;
}

function isVerifiedTargetGeometryStillArtifact(artifact: TimelineArtifact): boolean {
  if (artifact.kind !== "target_geometry_screenshot" || artifact.sensitive || !artifact.mime_type?.startsWith("image/")) return false;
  const geometry = targetGeometryFromMetadata(artifact.metadata);
  return Boolean(geometry && geometry.screenshot_artifact_id === artifact.id);
}

function validateCollaborationBoundary(plan: DemoEditPlan, errors: ValidationFinding[]): void {
  if (!Array.isArray(plan.locked_fields)) {
    errors.push(finding("missing_locked_fields", "locked_fields must declare customer-owned factual fields", "locked_fields"));
  } else {
    const missing = missingValues(REQUIRED_LOCKED_FIELDS, plan.locked_fields);
    if (missing.length > 0) {
      errors.push(finding("missing_locked_fields", `locked_fields must include: ${missing.join(", ")}`, "locked_fields"));
    }
  }

  if (!Array.isArray(plan.model_editable_fields)) {
    errors.push(finding("missing_model_editable_fields", "model_editable_fields must declare the cloud AIGC edit whitelist", "model_editable_fields"));
    return;
  }

  const unsupported = unsupportedValues(plan.model_editable_fields, ALLOWED_MODEL_EDITABLE_FIELDS);
  if (unsupported.length > 0) {
    errors.push(finding("unsupported_model_editable_field", `model_editable_fields contains unsupported fields: ${unsupported.join(", ")}`, "model_editable_fields"));
  }

  const lockedEditable = overlappingValues(plan.locked_fields || [], plan.model_editable_fields);
  if (lockedEditable.length > 0) {
    errors.push(finding("locked_field_marked_editable", `locked fields cannot be model editable: ${lockedEditable.join(", ")}`, "model_editable_fields"));
  }
}

function validateShotTimeRange(
  shot: DemoEditShot,
  artifact: TimelineArtifact | undefined,
  catalog: AssetTimelineCatalog,
  pathValue: string,
  errors: ValidationFinding[],
): void {
  if (isStillShot(shot)) return;
  if (!shot.source_time_range_ms) {
    if (artifact?.kind === "raw_recording") {
      errors.push(finding("missing_time_range", "Raw recording shots must declare source_time_range_ms", pathValue));
    }
    return;
  }
  const [startMS, endMS] = shot.source_time_range_ms;
  if (!Number.isFinite(startMS) || !Number.isFinite(endMS) || startMS < 0 || endMS <= startMS) {
    errors.push(finding("invalid_time_range", "source_time_range_ms must be a positive [start, end] range", pathValue));
    return;
  }
  const sourceDurationMS = artifact?.duration_ms || catalog.timeline.duration_ms;
  if (sourceDurationMS > 0 && endMS > sourceDurationMS) {
    errors.push(finding("time_range_outside_source", `Shot time range exceeds source duration ${sourceDurationMS}ms`, pathValue));
  }
}

function validateOperations(operations: EditOperation[], pathValue: string, errors: ValidationFinding[]): void {
  operations.forEach((operation, index) => {
    if (!ALLOWED_EDIT_OPERATIONS.includes(operation.type)) {
      errors.push(finding("unsupported_operation", `Unsupported edit operation ${operation.type}`, `${pathValue}[${index}].type`));
    }
  });
}

function validateOverlays(
  overlays: EditOverlay[],
  stepByID: Map<string, TimelineStep>,
  artifactByID: Map<string, TimelineArtifact>,
  shotStepID: string | undefined,
  shotSourceArtifactID: string,
  still: boolean,
  pathValue: string,
  errors: ValidationFinding[],
  warnings: ValidationFinding[],
): void {
  overlays.forEach((overlay, index) => {
    const overlayPath = `${pathValue}[${index}]`;
    if (!ALLOWED_OVERLAY_TYPES.includes(overlay.type)) {
      errors.push(finding("unsupported_overlay", `Unsupported overlay type ${overlay.type}`, `${overlayPath}.type`));
    }
    if (overlay.source_step_id && !stepByID.has(overlay.source_step_id)) {
      errors.push(finding("unknown_overlay_step", `Overlay references missing source step ${overlay.source_step_id}`, `${overlayPath}.source_step_id`));
    }
    if (isTargetOrientedOverlay(overlay)) {
      const evidenceIssue = targetOverlayEvidenceIssue(overlay, artifactByID, shotStepID, shotSourceArtifactID, still);
      if (evidenceIssue) {
        warnings.push(finding(
          evidenceIssue === "missing_target_geometry" ? "missing_target_geometry" : "invalid_target_geometry_evidence",
          `Target overlay will be retained for audit but not rendered: ${evidenceIssue}`,
          overlayPath,
        ));
      }
    }
    if (overlay.type !== "highlight_box") return;
    if (!overlay.shape || !["rectangle", "circle", "polygon", "star", "line", "arrow"].includes(overlay.shape)) {
      errors.push(finding("unsupported_shape_renderer", `Final renderer does not support shape ${overlay.shape || "unknown"}`, `${overlayPath}.shape`));
    }
    const has3DTilt = (overlay.tilt_preset && overlay.tilt_preset !== "flat" && overlay.tilt_preset !== "reset") || (overlay.tilt_x ?? 0) !== 0 || (overlay.tilt_y ?? 0) !== 0;
    if (has3DTilt) {
      errors.push(finding("unsupported_shape_3d_tilt", "Final renderer does not support 3D shape tilt", overlayPath));
    }
  });
}

async function composeFinalVideo(
  outputDir: string,
  targetDurationSec: number,
  catalog: AssetTimelineCatalog,
  editPlan: DemoEditPlan,
  targetContainer?: "mp4" | "webm" | "mov" | "m4v",
  renderProfile?: RenderProfile,
): Promise<CompositorResult> {
  const shotPlan = buildCompositorShotPlan(catalog, editPlan);
  const primaryShot = shotPlan.find((shot) => shot.source_path);
  if (shotPlan.length === 0 || !primaryShot) {
    return plannedCompositorResult(
      outputDir,
      targetDurationSec,
      shotPlan,
      "No local raw recording artifact is available. The edit plan is valid, but final video rendering is waiting for a compositor input.",
      editPlan,
    );
  }

  const sourceStat = await stat(primaryShot.source_path as string).catch(() => undefined);
  if (!sourceStat?.isFile()) {
    return plannedCompositorResult(
      outputDir,
      targetDurationSec,
      shotPlan,
      `Raw recording artifact ${primaryShot.source_artifact_id} is not available as a local file. The edit plan is valid, but final video rendering is pending.`,
      editPlan,
    );
  }

  const ffmpegPath = process.env.CASCADE_FFMPEG_PATH || "ffmpeg";
  const ffmpegAvailable = await isCommandAvailable(ffmpegPath);
  const sourceArtifact = findDemoArtifactByID(catalog, primaryShot.source_artifact_id);
  const sourceExtension = sourceArtifact ? videoExtensionForArtifact(sourceArtifact) : ".webm";
  const targetExtension = extensionForContainer(targetContainer);
  const extension = ffmpegAvailable ? targetExtension || ".mp4" : sourceExtension;
  const videoPath = path.join(outputDir, `demo_${targetDurationSec}s${extension}`);
  if (!ffmpegAvailable) {
    if (hasPostProcessingWork(editPlan)) {
      return plannedCompositorResult(
        outputDir,
        targetDurationSec,
        shotPlan,
        "The edit plan requires narration or global captions, but FFmpeg is unavailable. The source recording was not copied as a successful edited video.",
        editPlan,
        "ffmpeg_not_available_for_audio_caption_post_process",
      );
    }
    await copyFile(primaryShot.source_path as string, videoPath);
    const result: CompositorResult = {
      status: "rendered",
      video_path: videoPath,
      method: "copy_source_recording",
      ffmpeg_available: false,
      quality_status: "degraded",
      source_artifact_id: primaryShot.source_artifact_id,
      output_container: sourceExtension.replace(/^\./, ""),
      shot_plan: shotPlan,
      planned_operations: plannedOperations(shotPlan, editPlan),
      applied_operations: ["fallback_copy"],
      skipped_operations: skippedOperationsForCurrentCompositor(shotPlan, editPlan, true),
      fallback_reason: "ffmpeg_not_available",
      note: "Rendered a deterministic fallback demo video by copying the source browser recording. The edit plan is valid and recorded in the render manifest, but requested delivery formatting and trim/concat execution require ffmpeg.",
    };
    if (primaryShot.source_uri) result.source_uri = primaryShot.source_uri;
    return result;
  }

  const audio = normalizeAudioPolicy(editPlan.audio);
  const composed = await composeWithFFmpeg(ffmpegPath, outputDir, videoPath, shotPlan, extension, renderProfile, audio, catalog, editPlan);
  if (!composed.rendered) {
    if (hasPostProcessingWork(editPlan)) {
      return plannedCompositorResult(
        outputDir,
        targetDurationSec,
        shotPlan,
        `The edit plan requires narration or global captions, but the deterministic FFmpeg post-process failed (${composed.error || "unknown error"}). The source recording was not copied as a successful edited video.`,
        editPlan,
        composed.error || "ffmpeg_post_process_failed",
      );
    }
    const fallbackVideoPath = path.join(outputDir, `demo_${targetDurationSec}s.mp4`);
    const fallbackVideoCodec = await preferredVideoCodec(ffmpegPath, ".mp4", renderProfile);
    const fallbackAudioCodec = await preferredAudioCodec(ffmpegPath, ".mp4");
    const transcodeResult = await runCommand(ffmpegPath, [
      "-y", "-i", primaryShot.source_path as string,
      "-map", "0:v:0", "-map", "0:a?",
      "-c:v", fallbackVideoCodec.name, ...fallbackVideoCodec.args,
      "-c:a", fallbackAudioCodec.name, ...fallbackAudioCodec.args,
      "-movflags", "+faststart", fallbackVideoPath,
    ]);
    if (transcodeResult.code !== 0) {
      return plannedCompositorResult(
        outputDir,
        targetDurationSec,
        shotPlan,
        `FFmpeg edit and MP4 fallback transcode both failed (${compactProcessError("mp4 fallback", transcodeResult)}).`,
        editPlan,
        "mp4_delivery_failed",
      );
    }
    const result: CompositorResult = {
      status: "rendered",
      video_path: fallbackVideoPath,
      method: "ffmpeg_trim_concat",
      ffmpeg_available: true,
      quality_status: "degraded",
      source_artifact_id: primaryShot.source_artifact_id,
      output_container: "mp4",
      shot_plan: shotPlan,
      planned_operations: plannedOperations(shotPlan, editPlan),
      applied_operations: ["fallback_mp4_transcode"],
      skipped_operations: skippedOperationsForCurrentCompositor(shotPlan, editPlan, true),
      fallback_reason: composed.error || "ffmpeg_render_failed",
      note: "Rendered a deterministic MP4 fallback by transcoding the source browser recording after the requested edit composition failed. No image or video generation was used.",
    };
    result.video_codec = fallbackVideoCodec.name;
    result.audio_codec = fallbackAudioCodec.name;
    if (primaryShot.source_uri) result.source_uri = primaryShot.source_uri;
    return result;
  }

  const result: CompositorResult = {
    status: "rendered",
    video_path: videoPath,
    method: "ffmpeg_trim_concat",
    ffmpeg_available: true,
    quality_status: "ok",
    source_artifact_id: primaryShot.source_artifact_id,
    output_container: extension.replace(/^\./, ""),
    shot_plan: shotPlan,
    planned_operations: plannedOperations(shotPlan, editPlan),
    applied_operations: appliedOperationsForCurrentCompositor(shotPlan, editPlan),
    skipped_operations: skippedOperationsForCurrentCompositor(shotPlan, editPlan, false),
    note: "Rendered a deterministic demo video by trimming and concatenating existing source browser recording segments, then applying only confirmed narration and global captions. No image or video generation was used.",
  };
  if (composed.video_codec) result.video_codec = composed.video_codec;
  if (composed.audio_codec) result.audio_codec = composed.audio_codec;
  if (composed.encoding_audit) result.encoding_audit = composed.encoding_audit;
  result.audio_mode = audio.mode;
  if (primaryShot.source_uri) result.source_uri = primaryShot.source_uri;
  return result;
}

function plannedCompositorResult(outputDir: string, targetDurationSec: number, shotPlan: CompositorShot[], note: string, editPlan?: DemoEditPlan, fallbackReason = "missing_local_source_video"): CompositorResult {
  return {
    status: "planned",
    video_path: path.join(outputDir, `demo_${targetDurationSec}s.mp4`),
    method: "pending_compositor",
    ffmpeg_available: false,
    quality_status: "degraded",
    output_container: "mp4",
    shot_plan: shotPlan,
    planned_operations: plannedOperations(shotPlan, editPlan),
    applied_operations: [],
    skipped_operations: skippedOperationsForCurrentCompositor(shotPlan, editPlan, true),
    fallback_reason: fallbackReason,
    note,
  };
}

function buildRequirementSatisfactionReport(
  request: RenderRequest,
  catalog: AssetTimelineCatalog,
  editPlan: DemoEditPlan,
  compositor: CompositorResult,
  mediaNormalization: MediaNormalizationReport,
  finalVideoPath: string,
  stepDocsPath: string,
): RequirementSatisfactionReport {
  const graph = request.graph;
  const runSpec = request.recording_run_spec;
  const checks: RequirementCheck[] = [];
  const warnings: RequirementFinding[] = [];
  const errors: RequirementFinding[] = [];
  const productTargetDuration = resolveProductTargetDurationSec(graph);
  const recordingTargetDurationSec = runSpec?.timeline?.target_duration_sec;
  const editPlanTargetDurationSec = editPlan.target_duration_ms ? Math.round(editPlan.target_duration_ms / 1000) : undefined;
  const finalFormat = formatForVideoPath(finalVideoPath);
  const requestedFormats = requestedFinalVideoFormats(graph, runSpec);
  const screenshotCount = catalog.artifacts.filter((artifact) => isScreenshotArtifact(artifact)).length;
  const plannedOverlayTypes = plannedOverlayTypesForPlan(editPlan);
  const appliedOverlayTypes = overlayTypesAppliedByCompositor(compositor);
  const adoptedValues = adoptedRequirementValues(graph, runSpec, editPlanTargetDurationSec, requestedFormats, finalFormat);
  const stepChecks = buildStepRequirementChecks(graph, catalog, editPlan);
  const assetChecks = buildAssetRequirementChecks(graph, runSpec, catalog, compositor, mediaNormalization, finalVideoPath, stepDocsPath);
  const representedStepIDs = representedStepIDsForPlan(editPlan);
  const failedStepIDs = catalog.steps.filter((step) => isFailedExecutionStatus(step.status)).map((step) => step.step_id);
  const passedStepCount = catalog.steps.filter((step) => isPassedExecutionStatus(step.status)).length;
  const actualScreenshotStepIDs = screenshotStepIDsForCatalog(catalog);
  const expectedRenderedDurationMS = outputDurationMS(editPlan);
  const actualRenderedDurationSec = mediaNormalization.output?.duration_sec;

  if (mediaNormalization.status === "ok" && actualRenderedDurationSec !== undefined && expectedRenderedDurationMS > 0) {
    const expectedRenderedDurationSec = expectedRenderedDurationMS / 1000;
    const toleranceSec = Math.max(0.25, expectedRenderedDurationSec * 0.05);
    if (Math.abs(actualRenderedDurationSec - expectedRenderedDurationSec) > toleranceSec) {
      errors.push(requirementFinding(
        "rendered_duration_mismatch",
        `Rendered video duration is ${actualRenderedDurationSec.toFixed(3)}s, but the edit plan contributes ${expectedRenderedDurationSec.toFixed(3)}s (tolerance ${toleranceSec.toFixed(3)}s).`,
        "media_normalization_report.output.duration_sec",
      ));
      checks.push({
        code: "rendered_duration",
        status: "fail",
        message: "Rendered video duration does not match the effective edit-plan timeline.",
      });
    } else {
      checks.push({
        code: "rendered_duration",
        status: "pass",
        message: "Rendered video duration matches the effective edit-plan timeline.",
      });
    }
  }

  if (productTargetDuration && recordingTargetDurationSec && productTargetDuration !== recordingTargetDurationSec) {
    warnings.push(requirementFinding(
      "duration_intent_conflict",
      `Product requirement asks for ${productTargetDuration}s, while recording_run_spec asks for ${recordingTargetDurationSec}s. Final editing should treat the product requirement as delivery intent and the recording spec as capture strategy.`,
      "workflow_graph.assets.target_duration_sec",
    ));
    checks.push({
      code: "duration_intent_alignment",
      status: "warn",
      message: "Product-level and recording-level duration intents differ.",
    });
  } else {
    checks.push({
      code: "duration_intent_alignment",
      status: "pass",
      message: "No conflicting duration intent was detected.",
    });
  }

  if (adoptedValues.target_duration_sec.value && editPlanTargetDurationSec && adoptedValues.target_duration_sec.value !== editPlanTargetDurationSec) {
    warnings.push(requirementFinding(
      "edit_plan_duration_differs_from_delivery_intent",
      `Delivery intent resolves to ${adoptedValues.target_duration_sec.value}s from ${adoptedValues.target_duration_sec.source}, while the current deterministic edit plan targets ${editPlanTargetDurationSec}s.`,
      "demo_edit_plan.target_duration_ms",
    ));
    checks.push({
      code: "target_duration_resolution",
      status: "warn",
      message: "Delivery duration intent is preserved separately from the current deterministic render duration.",
    });
  } else {
    checks.push({
      code: "target_duration_resolution",
      status: "pass",
      message: `Resolved target duration from ${adoptedValues.target_duration_sec.source}.`,
    });
  }

  if (productTargetDuration && Math.ceil(catalog.timeline.duration_ms / 1000) < productTargetDuration) {
    warnings.push(requirementFinding(
      "requested_duration_exceeds_source_material",
      `Requested product duration is ${productTargetDuration}s, but captured source material is about ${Math.ceil(catalog.timeline.duration_ms / 1000)}s. The renderer must extend only with source-derived holds, still frames, captions, or pacing changes.`,
      "workflow_graph.assets.target_duration_sec",
    ));
  }

  const graphViewport = firstViewport(graph?.execution?.viewports);
  const recordingViewport = firstViewport(runSpec?.browser?.viewports);
  const outputResolution = requestedOutputResolution(runSpec);
  if (graphViewport && recordingViewport && (graphViewport.width !== recordingViewport.width || graphViewport.height !== recordingViewport.height)) {
    warnings.push(requirementFinding(
      "viewport_intent_conflict",
      `workflow_graph.execution requests ${graphViewport.width}x${graphViewport.height}, while recording_run_spec captures ${recordingViewport.width}x${recordingViewport.height}.`,
      "recording_run_spec.browser.viewports",
    ));
    checks.push({
      code: "viewport_intent_alignment",
      status: "warn",
      message: "Workflow graph viewport and recording viewport differ.",
    });
  } else {
    checks.push({
      code: "viewport_intent_alignment",
      status: "pass",
      message: "No conflicting viewport intent was detected.",
    });
  }

  if (outputResolution && mediaNormalization.output && (mediaNormalization.output.width !== outputResolution.width || mediaNormalization.output.height !== outputResolution.height)) {
    warnings.push(requirementFinding(
      "normalized_resolution_differs_from_recording_output",
      `recording_run_spec.outputs requests ${outputResolution.width}x${outputResolution.height}, while normalized MP4 is ${mediaNormalization.output.width}x${mediaNormalization.output.height}.`,
      "recording_run_spec.outputs",
    ));
  }

  if (requiresScreenshotPack(graph, runSpec) && screenshotCount === 0) {
    errors.push(requirementFinding("missing_required_screenshot_pack", "The package requested screenshots, but no screenshot artifact is available.", "workflow_graph.assets.screenshot_pack"));
    checks.push({
      code: "screenshot_pack",
      status: "fail",
      message: "Required screenshot pack is missing.",
    });
  } else if (requiresScreenshotPack(graph, runSpec)) {
    checks.push({
      code: "screenshot_pack",
      status: "pass",
      message: `Screenshot requirement has ${screenshotCount} captured artifact(s).`,
    });
  }

  if (requiresStepByStepDocs(graph, runSpec) && !stepDocsPath) {
    errors.push(requirementFinding("missing_step_by_step_docs", "Step-by-step docs were requested but no docs path was produced.", "workflow_graph.assets.step_by_step_docs"));
  }

  if (requestedFormats.includes("mp4") && finalFormat !== "mp4") {
    errors.push(requirementFinding("requested_format_not_satisfied", `A final MP4 was requested, but the preferred final video is ${finalFormat || "unknown"}.`, "recording_run_spec.outputs.output_formats"));
    checks.push({
      code: "final_video_format",
      status: "fail",
      message: "Final video is not MP4.",
    });
  } else if (requestedFormats.includes("mp4")) {
    checks.push({
      code: "final_video_format",
      status: "pass",
      message: "Final video is available as MP4.",
    });
  }

  if (mediaNormalization.status !== "ok") {
    const findingValue = requirementFinding(
      "media_normalization_not_ok",
      `MP4/H.264 normalization status is ${mediaNormalization.status}${mediaNormalization.error ? `: ${mediaNormalization.error}` : ""}.`,
      "media_normalization_report.status",
    );
    if (requestedFormats.includes("mp4")) {
      errors.push(findingValue);
    } else {
      warnings.push(findingValue);
    }
  }

  for (const stepCheck of stepChecks) {
    checks.push({
      code: `step_${safeName(stepCheck.step_id)}`,
      status: stepCheck.status,
      message: stepRequirementMessage(stepCheck),
    });
    for (const findingValue of stepCheck.findings) {
      if (stepCheck.status === "fail") {
        errors.push(findingValue);
      } else if (stepCheck.status === "warn") {
        warnings.push(findingValue);
      }
    }
  }

  for (const assetCheck of assetChecks) {
    checks.push({
      code: `asset_${safeName(assetCheck.kind)}`,
      status: assetCheck.status,
      message: assetCheck.message,
    });
    if (assetCheck.status === "fail") {
      errors.push(requirementFinding("required_asset_not_satisfied", assetCheck.message, `assets.${assetCheck.kind}`));
    } else if (assetCheck.status === "warn") {
      warnings.push(requirementFinding("asset_satisfied_with_warning", assetCheck.message, `assets.${assetCheck.kind}`));
    }
  }

  for (const overlayType of plannedOverlayTypes) {
    if (!appliedOverlayTypes.includes(overlayType)) {
      warnings.push(requirementFinding(
        "planned_overlay_not_rendered",
        `DemoEditPlan includes ${overlayType} overlays, but the current compositor did not render that overlay type into pixels.`,
        "demo_edit_plan.shots.overlays",
      ));
    }
  }

  const status: RequirementSatisfactionReport["status"] = errors.length > 0 ? "not_satisfied" : warnings.length > 0 ? "satisfied_with_warnings" : "satisfied";
  return {
    schema_version: REQUIREMENT_SATISFACTION_REPORT_SCHEMA_VERSION,
    status,
    checked_at: new Date().toISOString(),
    source_priority: {
      product_requirements: ["workflow_graph.assets", "workflow_graph.execution", "workflow_graph.nodes[].capture"],
      recording_execution_plan: ["recording_run_spec.timeline", "recording_run_spec.browser", "recording_run_spec.outputs"],
      actual_material: ["execution_trace", "generated_assets", "asset_timeline_catalog"],
    },
    adopted_values: adoptedValues,
    requested: {
      product_target_duration_sec: productTargetDuration,
      recording_target_duration_sec: recordingTargetDurationSec,
      recording_max_duration_sec: runSpec?.timeline?.max_duration_sec,
      final_video_formats: requestedFormats,
      screenshot_pack: requiresScreenshotPack(graph, runSpec),
      step_by_step_docs: requiresStepByStepDocs(graph, runSpec),
      graph_viewports: graph?.execution?.viewports,
      recording_viewports: runSpec?.browser?.viewports,
      output_resolution: outputResolution,
      required_assets: graph?.assets?.requested_assets?.filter((asset) => asset.required),
      required_step_ids: catalog.steps.filter((step) => step.required).map((step) => step.step_id),
      screenshot_step_ids: requestedScreenshotStepIDs(graph, catalog),
    },
    actual: {
      demo_video_path: finalVideoPath,
      demo_video_format: finalFormat,
      source_reference_video_path: mediaNormalization.reference_video_path,
      source_reference_video_format: formatForVideoPath(mediaNormalization.reference_video_path || ""),
      media_normalization_status: mediaNormalization.status,
      screenshot_count: screenshotCount,
      step_by_step_docs_path: stepDocsPath,
      timeline_duration_sec: Math.round(catalog.timeline.duration_ms / 1000),
      edit_plan_target_duration_sec: editPlanTargetDurationSec,
      ...(actualRenderedDurationSec !== undefined ? { rendered_video_duration_sec: actualRenderedDurationSec } : {}),
      ...(expectedRenderedDurationMS > 0 ? { rendered_video_expected_duration_sec: expectedRenderedDurationMS / 1000 } : {}),
      rendered_operations: compositor.applied_operations || [],
      planned_overlay_types: plannedOverlayTypes,
      applied_overlay_types: appliedOverlayTypes,
      represented_step_ids: representedStepIDs,
      passed_step_count: passedStepCount,
      failed_step_ids: failedStepIDs,
      screenshot_step_ids: actualScreenshotStepIDs,
    },
    step_checks: stepChecks,
    asset_checks: assetChecks,
    checks,
    warnings,
    errors,
  };
}

function adoptedRequirementValues(
  graph: DemoWorkflowGraph | undefined,
  runSpec: RecordingRunSpec | undefined,
  editPlanTargetDurationSec: number | undefined,
  requestedFormats: string[],
  finalFormat: string | undefined,
): RequirementSatisfactionReport["adopted_values"] {
  const productTargetDuration = resolveProductTargetDurationSec(graph);
  const recordingTargetDuration = runSpec?.timeline?.target_duration_sec;
  const targetDuration: RequirementAdoptedValue<number> = {
    value: productTargetDuration || recordingTargetDuration || editPlanTargetDurationSec,
    source: productTargetDuration
      ? "workflow_graph.assets"
      : recordingTargetDuration
        ? "recording_run_spec.timeline"
        : editPlanTargetDurationSec
          ? "demo_edit_plan.target_duration_ms"
          : "default",
    reason: productTargetDuration
      ? "Product-level delivery intent has priority over this run's capture strategy."
      : recordingTargetDuration
        ? "No product-level duration was provided, so the recording execution plan defines the target."
        : "No upstream duration intent was provided, so the edit plan/default renderer duration is used.",
  };
  const ignoredDurations: RequirementAdoptedValue<number>["ignored"] = [];
  if (productTargetDuration && recordingTargetDuration && productTargetDuration !== recordingTargetDuration) {
    ignoredDurations.push({
      source: "recording_run_spec.timeline",
      value: recordingTargetDuration,
      reason: "Recording duration is treated as capture strategy when product delivery intent is present.",
    });
  }
  if (targetDuration.value && editPlanTargetDurationSec && targetDuration.value !== editPlanTargetDurationSec) {
    ignoredDurations.push({
      source: "demo_edit_plan.target_duration_ms",
      value: editPlanTargetDurationSec,
      reason: "Current deterministic render duration is preserved as actual behavior, but not treated as the product delivery target.",
    });
  }
  if (ignoredDurations.length > 0) targetDuration.ignored = ignoredDurations;

  const outputResolution = requestedOutputResolution(runSpec);
  const recordingViewport = firstViewport(runSpec?.browser?.viewports);
  const graphViewport = firstViewport(graph?.execution?.viewports);
  const resolution = outputResolution || recordingViewportSize(recordingViewport) || recordingViewportSize(graphViewport) || {
    width: MEDIA_NORMALIZATION_TARGET.width,
    height: MEDIA_NORMALIZATION_TARGET.height,
  };
  const resolutionSource = outputResolution
    ? "recording_run_spec.outputs"
    : recordingViewport
      ? "recording_run_spec.browser.viewports"
      : graphViewport
        ? "workflow_graph.execution.viewports"
        : "media_normalization_target";
  const outputResolutionValue: RequirementAdoptedValue<{ width: number; height: number }> = {
    value: resolution,
    source: resolutionSource,
    reason: outputResolution
      ? "Explicit output resolution controls captured and normalized deliverables."
      : "Fallback resolution follows the most specific viewport or the normalization target.",
  };
  if (outputResolution && graphViewport && (outputResolution.width !== graphViewport.width || outputResolution.height !== graphViewport.height)) {
    outputResolutionValue.ignored = [{
      source: "workflow_graph.execution.viewports",
      value: recordingViewportSize(graphViewport),
      reason: "Explicit recording output resolution is more specific for final media normalization.",
    }];
  }

  const preferredFormat = requestedFormats.includes("mp4") ? "mp4" : requestedFormats[0] || finalFormat || "mp4";
  return {
    target_duration_sec: targetDuration,
    output_resolution: outputResolutionValue,
    final_video_format: {
      value: preferredFormat,
      source: requestedFormats.length > 0 ? "workflow_graph.assets.requested_assets + recording_run_spec.outputs.output_formats" : "renderer_default",
      reason: requestedFormats.includes("mp4")
        ? "MP4 is preferred for delivery and future model/media compatibility."
        : "No MP4 requirement was provided, so the first requested/current format is used.",
    },
  };
}

function buildStepRequirementChecks(graph: DemoWorkflowGraph | undefined, catalog: AssetTimelineCatalog, editPlan: DemoEditPlan): RequirementStepCheck[] {
  const nodeByID = new Map((graph?.nodes || []).map((node) => [node.id, node]));
  const representedSteps = new Set(representedStepIDsForPlan(editPlan));
  return catalog.steps.map((step) => {
    const node = nodeByID.get(step.step_id);
    const screenshotArtifacts = screenshotArtifactsForStep(catalog, step);
    const screenshotRequired = nodeRequiresScreenshot(node);
    const findings: RequirementFinding[] = [];
    let status: RequirementStepCheck["status"] = "pass";

    if (step.required && isFailedExecutionStatus(step.status)) {
      status = "fail";
      findings.push(requirementFinding(
        "required_step_failed",
        `Required script step ${step.step_id} ended with status ${step.status}.`,
        `execution_trace.step_results.${step.step_id}`,
      ));
    } else if (step.required && !isPassedExecutionStatus(step.status)) {
      status = "warn";
      findings.push(requirementFinding(
        "required_step_not_confirmed_passed",
        `Required script step ${step.step_id} is ${step.status}; it is not confirmed as passed.`,
        `execution_trace.step_results.${step.step_id}`,
      ));
    }

    if (step.required && !representedSteps.has(step.step_id) && !isFailedExecutionStatus(step.status)) {
      status = worseRequirementStatus(status, "warn");
      findings.push(requirementFinding(
        "required_step_not_represented_in_edit_plan",
        `Required script step ${step.step_id} passed but is not represented in DemoEditPlan shots.`,
        "demo_edit_plan.shots",
      ));
    }

    if (screenshotRequired && screenshotArtifacts.length === 0) {
      status = worseRequirementStatus(status, "fail");
      findings.push(requirementFinding(
        "required_step_screenshot_missing",
        `Step ${step.step_id} requested a screenshot, but no screenshot artifact is linked to it.`,
        `workflow_graph.nodes.${step.step_id}.capture`,
      ));
    }

    return {
      step_id: step.step_id,
      action: step.action,
      required: step.required,
      execution_status: step.status,
      represented_in_edit_plan: representedSteps.has(step.step_id),
      screenshot_required: screenshotRequired,
      screenshot_artifact_ids: screenshotArtifacts.map((artifact) => artifact.id),
      status,
      findings,
    };
  });
}

function buildAssetRequirementChecks(
  graph: DemoWorkflowGraph | undefined,
  runSpec: RecordingRunSpec | undefined,
  catalog: AssetTimelineCatalog,
  compositor: CompositorResult,
  mediaNormalization: MediaNormalizationReport,
  finalVideoPath: string,
  stepDocsPath: string,
): RequirementAssetCheck[] {
  const checks: RequirementAssetCheck[] = [];
  const requestedAssets = graph?.assets?.requested_assets || [];
  const requiredAssetKinds = new Set(requestedAssets.filter((asset) => asset.required && asset.kind).map((asset) => asset.kind as string));
  if (runSpec?.outputs?.final_video || requiredAssetKinds.has("demo_video")) requiredAssetKinds.add("demo_video");
  if (requiresScreenshotPack(graph, runSpec)) requiredAssetKinds.add("screenshot_pack");
  if (requiresStepByStepDocs(graph, runSpec)) requiredAssetKinds.add("step_by_step_docs");
  if (runSpec?.outputs?.raw_recording) requiredAssetKinds.add("raw_recording");
  if (runSpec?.outputs?.trace) requiredAssetKinds.add("trace");

  for (const kind of [...requiredAssetKinds].sort()) {
    checks.push(assetRequirementCheck(kind, catalog, compositor, mediaNormalization, finalVideoPath, stepDocsPath));
  }
  return checks;
}

function assetRequirementCheck(
  kind: string,
  catalog: AssetTimelineCatalog,
  compositor: CompositorResult,
  mediaNormalization: MediaNormalizationReport,
  finalVideoPath: string,
  stepDocsPath: string,
): RequirementAssetCheck {
  switch (kind) {
    case "demo_video": {
      const rendered = compositor.status === "rendered" && Boolean(finalVideoPath);
      return {
        kind,
        required: true,
        status: rendered ? "pass" : "fail",
        artifact_ids: rendered ? ["final_demo_video"] : [],
        message: rendered
          ? `Final demo video was rendered to ${finalVideoPath}.`
          : "Final demo video is required, but the compositor only produced a planned output.",
      };
    }
    case "screenshot_pack": {
      const screenshots = catalog.artifacts.filter((artifact) => isScreenshotArtifact(artifact));
      return {
        kind,
        required: true,
        status: screenshots.length > 0 ? "pass" : "fail",
        artifact_ids: screenshots.map((artifact) => artifact.id),
        message: screenshots.length > 0
          ? `Screenshot pack has ${screenshots.length} screenshot artifact(s).`
          : "Screenshot pack is required, but no screenshot artifact is available.",
      };
    }
    case "step_by_step_docs": {
      return {
        kind,
        required: true,
        status: stepDocsPath ? "pass" : "fail",
        artifact_ids: stepDocsPath ? ["step_by_step_docs"] : [],
        message: stepDocsPath ? `Step-by-step docs were written to ${stepDocsPath}.` : "Step-by-step docs are required, but no docs path was produced.",
      };
    }
    case "raw_recording": {
      const recordings = catalog.artifacts.filter((artifact) => artifact.kind === "raw_recording" || (!isGeneratedCandidateArtifact(artifact) && isVideoArtifact(artifact)));
      return {
        kind,
        required: true,
        status: recordings.length > 0 ? "pass" : "fail",
        artifact_ids: recordings.map((artifact) => artifact.id),
        message: recordings.length > 0 ? "Raw recording source material is available." : "Raw recording was requested but no video source artifact is available.",
      };
    }
    case "trace": {
      const traces = catalog.artifacts.filter((artifact) => artifact.kind === "browser_trace" || artifact.kind === "execution_trace");
      return {
        kind,
        required: true,
        status: traces.length > 0 ? "pass" : "fail",
        artifact_ids: traces.map((artifact) => artifact.id),
        message: traces.length > 0 ? "Trace artifact is available for diagnostics." : "Trace output was requested but no trace artifact is available.",
      };
    }
    case "source_reference_video": {
      const ready = mediaNormalization.status === "ok" && Boolean(mediaNormalization.reference_video_path);
      return {
        kind,
        required: true,
        status: ready ? "pass" : "fail",
        artifact_ids: ready ? ["source_reference_video"] : [],
        message: ready ? "Source reference MP4 is ready for future model input." : "Source reference video is required but media normalization did not succeed.",
      };
    }
    default:
      return {
        kind,
        required: true,
        status: "fail",
        artifact_ids: catalog.artifacts.filter((artifact) => artifact.kind === kind).map((artifact) => artifact.id),
        message: `Required asset kind ${kind} is not produced by the current renderer.`,
      };
  }
}

function representedStepIDsForPlan(plan: DemoEditPlan): string[] {
  return [...new Set(plan.shots.map((shot) => shot.source_step_id).filter((stepID): stepID is string => Boolean(stepID)))];
}

function requestedScreenshotStepIDs(graph: DemoWorkflowGraph | undefined, catalog: AssetTimelineCatalog): string[] {
  const nodeByID = new Map((graph?.nodes || []).map((node) => [node.id, node]));
  return catalog.steps.filter((step) => nodeRequiresScreenshot(nodeByID.get(step.step_id))).map((step) => step.step_id);
}

function screenshotStepIDsForCatalog(catalog: AssetTimelineCatalog): string[] {
  const stepIDs = new Set<string>();
  for (const step of catalog.steps) {
    if (screenshotArtifactsForStep(catalog, step).length > 0) {
      stepIDs.add(step.step_id);
    }
  }
  return [...stepIDs];
}

function screenshotArtifactsForStep(catalog: AssetTimelineCatalog, step: TimelineStep): TimelineArtifact[] {
  const stepArtifactIDs = new Set(step.artifacts || []);
  return catalog.artifacts.filter((artifact) => {
    if (!isScreenshotArtifact(artifact)) return false;
    return artifact.source_step_id === step.step_id || stepArtifactIDs.has(artifact.id);
  });
}

function nodeRequiresScreenshot(node: GraphNode | undefined): boolean {
  return node?.is_screenshot === true || node?.capture?.screenshot === true;
}

function isPassedExecutionStatus(status: string | undefined): boolean {
  return String(status || "").toLowerCase() === "passed";
}

function isFailedExecutionStatus(status: string | undefined): boolean {
  const normalized = String(status || "").toLowerCase();
  return normalized === "failed" || normalized === "error" || normalized === "timed_out" || normalized === "timeout";
}

function worseRequirementStatus(left: RequirementCheck["status"], right: RequirementCheck["status"]): RequirementCheck["status"] {
  const rank = { pass: 0, warn: 1, fail: 2 };
  return rank[right] > rank[left] ? right : left;
}

function stepRequirementMessage(check: RequirementStepCheck): string {
  const screenshot = check.screenshot_required ? `, screenshots=${check.screenshot_artifact_ids.length}` : "";
  return `Step ${check.step_id} status=${check.execution_status}, represented=${check.represented_in_edit_plan}${screenshot}.`;
}

function recordingViewportSize(viewport: ViewportSpec | undefined): { width: number; height: number } | undefined {
  return viewport?.width && viewport?.height ? { width: viewport.width, height: viewport.height } : undefined;
}

function resolveProductTargetDurationSec(graph: DemoWorkflowGraph | undefined): number | undefined {
  const requestedDemoVideo = graph?.assets?.requested_assets?.find((asset) => asset.kind === "demo_video" && asset.duration_sec && asset.duration_sec > 0);
  if (requestedDemoVideo?.duration_sec) return requestedDemoVideo.duration_sec;
  const targetDuration = graph?.assets?.target_duration_sec;
  return targetDuration && targetDuration > 0 ? targetDuration : undefined;
}

function requestedFinalVideoFormats(graph: DemoWorkflowGraph | undefined, runSpec: RecordingRunSpec | undefined): string[] {
  const formats = (graph?.assets?.requested_assets || [])
    .filter((asset) => asset.kind === "demo_video" && asset.format)
    .map((asset) => (asset.format as string).toLowerCase());
  for (const format of runSpec?.outputs?.output_formats || []) {
    const normalized = format.toLowerCase();
    if (["mp4", "webm", "mov", "m4v"].includes(normalized)) {
      formats.push(normalized);
    }
  }
  return [...new Set(formats)];
}

function validateAudioSplitPoints(plan: DemoEditPlan, errors: ValidationFinding[]): void {
  const durationMS = outputDurationMS(plan);
  if (!plan.audio) return;
  let previous = 0;
  (plan.audio.split_points_ms ?? []).forEach((point, index) => {
    const pathValue = `audio.split_points_ms[${index}]`;
    if (!Number.isFinite(point) || !Number.isInteger(point)) {
      errors.push(finding("invalid_audio_split_point", "audio split points must be integer milliseconds", pathValue));
    } else if (point <= 0 || point >= durationMS) {
      errors.push(finding("audio_split_point_outside_timeline", `audio split point must be inside the ${durationMS}ms output timeline`, pathValue));
    } else if (point <= previous) {
      errors.push(finding("audio_split_points_not_ordered", "audio split points must be unique and strictly increasing", pathValue));
    }
    previous = point;
  });
  const boundaries = new Set([0, durationMS, ...(plan.audio.split_points_ms ?? [])]);
  let previousEndMS = 0;
  (plan.audio.segment_settings ?? []).forEach((segment, index) => {
    const pathValue = `audio.segment_settings[${index}]`;
    if (!Number.isInteger(segment.start_ms) || !Number.isInteger(segment.end_ms) || segment.start_ms < 0 || segment.end_ms <= segment.start_ms || segment.end_ms > durationMS) {
      errors.push(finding("invalid_audio_segment_range", `audio segment must be an integer [start, end] range inside the ${durationMS}ms output timeline`, pathValue));
    }
    if (!boundaries.has(segment.start_ms) || !boundaries.has(segment.end_ms)) {
      errors.push(finding("audio_segment_not_aligned", "audio segment settings must align with declared split points or timeline boundaries", pathValue));
    }
    if (index > 0 && segment.start_ms < previousEndMS) {
      errors.push(finding("audio_segments_overlap", "audio segment settings must be ordered and non-overlapping", pathValue));
    }
    if (segment.mode !== "source" && segment.mode !== "mute") {
      errors.push(finding("invalid_audio_segment_mode", "audio segment mode must be source or mute", `${pathValue}.mode`));
    }
    if (!Number.isFinite(segment.volume_percent) || segment.volume_percent < 0 || segment.volume_percent > 200) {
      errors.push(finding("invalid_audio_segment_volume", "audio segment volume_percent must be between 0 and 200", `${pathValue}.volume_percent`));
    }
    previousEndMS = segment.end_ms;
  });
}

function validateNarrations(plan: DemoEditPlan, catalog: AssetTimelineCatalog, errors: ValidationFinding[]): void {
  const totalDurationMS = outputDurationMS(plan);
  const seenIDs = new Set<string>();
  const allowedSources = new Set(["user_recorded", "user_uploaded", "tts_confirmed"]);
  (plan.narrations ?? []).forEach((narration, index) => {
    const pathValue = `narrations[${index}]`;
    if (!narration.id || seenIDs.has(narration.id)) {
      errors.push(finding("invalid_narration_id", "narration id must be non-empty and unique", `${pathValue}.id`));
    }
    seenIDs.add(narration.id);
    const artifact = catalog.artifacts.find((candidate) => candidate.id === narration.source_artifact_id);
    if (!artifact) {
      errors.push(finding("unknown_narration_artifact", `Narration references missing asset ${narration.source_artifact_id}`, `${pathValue}.source_artifact_id`));
    } else if (!isDemoMaterial(artifact) || isGeneratedCandidateArtifact(artifact) || !isAudioArtifact(artifact)) {
      errors.push(finding("invalid_narration_artifact", "Narration must reference a non-sensitive imported audio asset, not a generated candidate", `${pathValue}.source_artifact_id`));
    }
    validateOutputRange(narration.output_time_range_ms, totalDurationMS, `${pathValue}.output_time_range_ms`, "narration", errors);
    const sourceRange = narration.source_time_range_ms ?? [0, artifact?.duration_ms ?? 0];
    if (!sourceRange[1] || sourceRange[1] <= sourceRange[0]) {
      errors.push(finding("invalid_narration_source_range", "Narration source range must be declared or its asset duration must be known", `${pathValue}.source_time_range_ms`));
    } else {
      if (!Number.isInteger(sourceRange[0]) || !Number.isInteger(sourceRange[1]) || sourceRange[0] < 0) {
        errors.push(finding("invalid_narration_source_range", "Narration source range must be integer milliseconds", `${pathValue}.source_time_range_ms`));
      }
      if (artifact?.duration_ms && sourceRange[1] > artifact.duration_ms) {
        errors.push(finding("narration_source_range_outside_asset", `Narration source range exceeds asset duration ${artifact.duration_ms}ms`, `${pathValue}.source_time_range_ms`));
      }
      const outputDuration = narration.output_time_range_ms[1] - narration.output_time_range_ms[0];
      if (sourceRange[1] - sourceRange[0] < outputDuration) {
        errors.push(finding("narration_source_too_short", "Narration source range must cover its output duration before any future speed processing is approved", `${pathValue}.source_time_range_ms`));
      }
    }
    if (!Number.isFinite(narration.volume_percent) || narration.volume_percent < 0 || narration.volume_percent > 200) {
      errors.push(finding("invalid_narration_volume", "Narration volume_percent must be between 0 and 200", `${pathValue}.volume_percent`));
    }
    if (narration.duck_source_to_percent !== undefined && (!Number.isFinite(narration.duck_source_to_percent) || narration.duck_source_to_percent < 0 || narration.duck_source_to_percent > 100)) {
      errors.push(finding("invalid_narration_duck", "Narration duck_source_to_percent must be between 0 and 100", `${pathValue}.duck_source_to_percent`));
    }
    if (!allowedSources.has(narration.source)) {
      errors.push(finding("invalid_narration_source", "Narration source must be user_recorded, user_uploaded, or tts_confirmed", `${pathValue}.source`));
    }
  });
}

function validateCaptionCues(plan: DemoEditPlan, errors: ValidationFinding[]): void {
  const totalDurationMS = outputDurationMS(plan);
  const seenIDs = new Set<string>();
  const allowedSources = new Set(["user_configured", "asr_confirmed", "model_confirmed"]);
  (plan.caption_cues ?? []).forEach((cue, index) => {
    const pathValue = `caption_cues[${index}]`;
    if (!cue.id || seenIDs.has(cue.id)) {
      errors.push(finding("invalid_caption_cue_id", "caption cue id must be non-empty and unique", `${pathValue}.id`));
    }
    seenIDs.add(cue.id);
    validateOutputRange(cue.output_range_ms, totalDurationMS, `${pathValue}.output_range_ms`, "caption cue", errors);
    if (!cue.text.trim()) {
      errors.push(finding("empty_caption_cue", "caption cue text must be non-empty", `${pathValue}.text`));
    }
    if (!allowedSources.has(cue.source)) {
      errors.push(finding("invalid_caption_cue_source", "caption cue source must be user_configured, asr_confirmed, or model_confirmed", `${pathValue}.source`));
    }
  });
}

function validateOutputRange(range: [number, number], totalDurationMS: number, pathValue: string, label: string, errors: ValidationFinding[]): void {
  const [startMS, endMS] = range;
  if (!Number.isInteger(startMS) || !Number.isInteger(endMS) || startMS < 0 || endMS <= startMS || endMS > totalDurationMS) {
    errors.push(finding(`invalid_${label.replace(/\s+/g, "_")}_range`, `${label} range must be an integer [start, end] inside the ${totalDurationMS}ms output timeline`, pathValue));
  }
}

function outputDurationMS(plan: DemoEditPlan): number {
  return plan.shots.reduce((total, shot) => total + shotOutputDurationMS(shot), 0);
}

function shotOutputDurationMS(shot: DemoEditShot): number {
  if (isStillShot(shot)) return Math.max(0, shot.output_duration_ms ?? 0);
  return shot.source_time_range_ms ? Math.max(0, shot.source_time_range_ms[1] - shot.source_time_range_ms[0]) : 0;
}

export function validateEditPlan(request: { catalog: AssetTimelineCatalog; edit_plan: DemoEditPlan }): DemoEditPlanValidationReport {
  if (!request?.catalog || !request?.edit_plan) {
    throw new Error("catalog and edit_plan are required");
  }
  return validateDemoEditPlan(normalizeEditPlan(request.edit_plan, request.catalog), request.catalog);
}

function preferredFinalVideoContainer(formats: string[]): "mp4" | "webm" | "mov" | "m4v" | undefined {
  if (formats.includes("mp4")) return "mp4";
  const first = formats.find((format): format is "mp4" | "webm" | "mov" | "m4v" => ["mp4", "webm", "mov", "m4v"].includes(format));
  return first;
}

function extensionForContainer(container: "mp4" | "webm" | "mov" | "m4v" | undefined): ".mp4" | ".webm" | ".mov" | ".m4v" | undefined {
  switch (container) {
    case "mp4":
      return ".mp4";
    case "webm":
      return ".webm";
    case "mov":
      return ".mov";
    case "m4v":
      return ".m4v";
    default:
      return undefined;
  }
}

function firstViewport(viewports: ViewportSpec[] | undefined): ViewportSpec | undefined {
  return viewports?.find((viewport) => viewport.width && viewport.height);
}

function requestedOutputResolution(runSpec: RecordingRunSpec | undefined): { width: number; height: number } | undefined {
  const width = runSpec?.outputs?.resolution_width;
  const height = runSpec?.outputs?.resolution_height;
  return width && height ? { width, height } : undefined;
}

function requiresScreenshotPack(graph: DemoWorkflowGraph | undefined, runSpec: RecordingRunSpec | undefined): boolean {
  return graph?.assets?.screenshot_pack === true || runSpec?.outputs?.screenshot_pack === true || (graph?.assets?.requested_assets || []).some((asset) => asset.kind === "screenshot_pack" && asset.required);
}

function requiresStepByStepDocs(graph: DemoWorkflowGraph | undefined, runSpec: RecordingRunSpec | undefined): boolean {
  return graph?.assets?.step_by_step_docs === true || runSpec?.outputs?.step_by_step_docs === true || (graph?.assets?.requested_assets || []).some((asset) => asset.kind === "step_by_step_docs" && asset.required);
}

function isScreenshotArtifact(artifact: TimelineArtifact): boolean {
  if (isGeneratedCandidateArtifact(artifact)) return false;
  return artifact.kind === "screenshot" || artifact.kind === "webpage_screenshot" || artifact.mime_type === "image/png" || artifact.mime_type === "image/jpeg";
}

function formatForVideoPath(filePath: string | undefined): string | undefined {
  if (!filePath) return undefined;
  const extension = path.extname(filePath).replace(/^\./, "").toLowerCase();
  return extension || undefined;
}

function plannedOverlayTypesForPlan(plan: DemoEditPlan): string[] {
  const values = new Set<string>();
  for (const shot of plan.shots) {
    for (const overlay of shot.overlays || []) {
      values.add(overlay.type);
    }
  }
  return [...values].sort();
}

function overlayTypesAppliedByCompositor(compositor: CompositorResult): string[] {
  const values = new Set<string>();
  for (const operation of compositor.applied_operations || []) {
    if (ALLOWED_OVERLAY_TYPES.includes(operation as OverlayType)) {
      values.add(operation);
    }
  }
  return [...values].sort();
}

function requirementFinding(code: string, message: string, pathValue?: string): RequirementFinding {
  const result: RequirementFinding = { code, message };
  if (pathValue) result.path = pathValue;
  return result;
}

async function normalizeSourceReferenceVideo(
  outputDir: string,
  catalog: AssetTimelineCatalog,
  editPlan: DemoEditPlan,
  preferredSourcePath?: string,
  target: MediaNormalizationTarget = MEDIA_NORMALIZATION_TARGET,
): Promise<{ report_path: string; report: MediaNormalizationReport }> {
  const reportPath = path.join(outputDir, "media_normalization_report.json");
  const report: MediaNormalizationReport = {
    schema_version: MEDIA_NORMALIZATION_REPORT_SCHEMA_VERSION,
    status: "skipped",
    checked_at: new Date().toISOString(),
    target,
    ffmpeg_available: false,
  };

  // Prefer the compositor output. It is the Server-rendered delivery asset;
  // source artifacts can remain sensitive and must not be made demo-visible
  // merely to create a normalized local reference.
  const sourceArtifact = firstCompositableVideoArtifact(catalog, editPlan)
    || catalog.artifacts.find((artifact) => artifact.id === catalog.timeline.recording_artifact_id && isVideoArtifact(artifact));
  const sourcePath = preferredSourcePath || sourceArtifact?.local_path || (sourceArtifact?.uri ? filePathFromURI(sourceArtifact.uri) : undefined);
  if (!sourcePath) {
    report.note = "No source video artifact is available for media normalization.";
    await writeJSON(reportPath, report);
    return { report_path: reportPath, report };
  }
  if (sourceArtifact) {
    report.source_artifact_id = sourceArtifact.id;
    report.source_uri = sourceArtifact.uri;
  }
  report.source_path = sourcePath;
  if (preferredSourcePath) {
    report.note = "Normalized the rendered compositor output into the MP4/H.264 reference video used as the preferred final demo deliverable.";
  }
  if (!sourcePath) {
    report.note = "The source video artifact is not available as a local file path.";
    await writeJSON(reportPath, report);
    return { report_path: reportPath, report };
  }

  const sourceStat = await stat(sourcePath).catch(() => undefined);
  if (!sourceStat?.isFile()) {
    report.note = `The source video artifact does not exist on disk: ${sourcePath}`;
    await writeJSON(reportPath, report);
    return { report_path: reportPath, report };
  }

  const ffmpegPath = process.env.CASCADE_FFMPEG_PATH || "ffmpeg";
  const ffprobePath = ffprobePathFor(ffmpegPath);
  const ffmpegAvailable = await isCommandAvailable(ffmpegPath);
  const ffprobeAvailable = await isCommandAvailable(ffprobePath);
  report.ffmpeg_available = ffmpegAvailable;
  report.ffprobe_available = ffprobeAvailable;

  if (ffprobeAvailable) {
    report.input = await probeMedia(ffprobePath, sourcePath).catch((error: unknown) => ({
      format: `probe_failed: ${error instanceof Error ? error.message : String(error)}`,
    }));
  }

  if (!ffmpegAvailable) {
    report.status = "failed";
    report.error = "ffmpeg_not_available";
    await writeJSON(reportPath, report);
    return { report_path: reportPath, report };
  }

  const codec = await preferredReferenceH264Codec(ffmpegPath);
  if (!codec) {
    report.status = "failed";
    report.error = "h264_encoder_not_available";
    await writeJSON(reportPath, report);
    return { report_path: reportPath, report };
  }

  const referenceVideoPath = path.join(outputDir, "source_reference.mp4");
  const inputValidation = validateNormalizedMedia(report.input, target);
  if (ffprobeAvailable && Object.values(inputValidation).every(Boolean)) {
    const remuxResult = await runCommand(ffmpegPath, [
      "-y", "-i", sourcePath,
      "-map", "0:v:0", "-map", "0:a:0",
      "-c", "copy", "-movflags", "+faststart", referenceVideoPath,
    ]);
    if (remuxResult.code !== 0) {
      report.status = "failed";
      report.error = compactProcessError("media_normalization_remux_failed", remuxResult);
      await writeJSON(reportPath, report);
      return { report_path: reportPath, report };
    }
    report.reference_video_path = referenceVideoPath;
    report.output = await probeMedia(ffprobePath, referenceVideoPath).catch((error: unknown) => ({
      format: `probe_failed: ${error instanceof Error ? error.message : String(error)}`,
    }));
    report.validation = validateNormalizedMedia(report.output, target);
    report.status = Object.values(report.validation).every(Boolean) ? "ok" : "failed";
    report.note = "The compositor output already matched the locked delivery profile; normalization used stream-copy remux with faststart and did not re-encode video or audio.";
    if (report.status === "failed") report.error = "remuxed_media_does_not_match_target";
    await writeJSON(reportPath, report);
    return { report_path: reportPath, report };
  }
  const sourceHasAudio = await hasAudioStream(ffmpegPath, sourcePath);
  const transcodeArgs = [
    "-y",
    "-i",
    sourcePath,
  ];
  if (!sourceHasAudio) {
    transcodeArgs.push("-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000");
  }
  transcodeArgs.push(
    "-map",
    "0:v:0",
    "-map",
    sourceHasAudio ? "0:a:0" : "1:a:0",
    "-vf",
    `scale=${target.width}:${target.height}:force_original_aspect_ratio=decrease,pad=${target.width}:${target.height}:(ow-iw)/2:(oh-ih)/2,fps=${target.fps},format=${target.pixel_format}`,
    "-c:v",
    codec.name,
    ...codec.args,
    "-c:a",
    "aac",
    "-b:a",
    "128k",
    "-shortest",
    "-movflags",
    "+faststart",
    referenceVideoPath,
  );
  const transcodeResult = await runCommand(ffmpegPath, transcodeArgs);
  if (transcodeResult.code !== 0) {
    report.status = "failed";
    report.error = compactProcessError("media_normalization_failed", transcodeResult);
    await writeJSON(reportPath, report);
    return { report_path: reportPath, report };
  }

  report.reference_video_path = referenceVideoPath;
  if (ffprobeAvailable) {
    report.output = await probeMedia(ffprobePath, referenceVideoPath).catch((error: unknown) => ({
      format: `probe_failed: ${error instanceof Error ? error.message : String(error)}`,
    }));
    report.validation = validateNormalizedMedia(report.output, target);
    report.status = Object.values(report.validation).every(Boolean) ? "ok" : "failed";
    if (report.status === "failed") {
      report.error = "normalized_media_does_not_match_target";
    }
  } else {
    report.status = "ok";
    report.note = "Reference MP4 was created, but ffprobe is unavailable so detailed media validation was skipped.";
  }

  await writeJSON(reportPath, report);
  return { report_path: reportPath, report };
}

async function skippedPreviewNormalization(
  outputDir: string,
  profile: RenderProfile,
): Promise<{ report_path: string; report: MediaNormalizationReport }> {
  const reportPath = path.join(outputDir, "media_normalization_report.json");
  const report: MediaNormalizationReport = {
    schema_version: MEDIA_NORMALIZATION_REPORT_SCHEMA_VERSION,
    status: "skipped",
    checked_at: new Date().toISOString(),
    target: {
      ...MEDIA_NORMALIZATION_TARGET,
      width: boundedInteger(profile.width, 1280, 320, 3840),
      height: boundedInteger(profile.height, 720, 180, 2160),
      fps: boundedInteger(profile.fps, 30, 1, 60),
    },
    ffmpeg_available: true,
    note: "Preview rendering uses the requested low-cost profile and skips final-delivery normalization.",
  };
  await writeJSON(reportPath, report);
  return { report_path: reportPath, report };
}

function ffprobePathFor(ffmpegPath: string): string {
  if (process.env.CASCADE_FFPROBE_PATH) {
    return process.env.CASCADE_FFPROBE_PATH;
  }
  const baseName = path.basename(ffmpegPath).toLowerCase();
  if (baseName === "ffmpeg.exe") {
    return path.join(path.dirname(ffmpegPath), "ffprobe.exe");
  }
  if (baseName === "ffmpeg") {
    return path.join(path.dirname(ffmpegPath), "ffprobe");
  }
  if (ffmpegPath.includes("/") || ffmpegPath.includes("\\")) {
    return path.join(path.dirname(ffmpegPath), process.platform === "win32" ? "ffprobe.exe" : "ffprobe");
  }
  return "ffprobe";
}

async function preferredReferenceH264Codec(ffmpegPath: string): Promise<VideoCodecConfig | undefined> {
  const encoderList = await runCommand(ffmpegPath, ["-hide_banner", "-encoders"]);
  const encoders = `${encoderList.stdout}\n${encoderList.stderr}`;
  const candidates: VideoCodecConfig[] = [
    { name: "libx264", args: ["-preset", "medium", "-crf", "18", "-g", "30", "-keyint_min", "30", "-pix_fmt", "yuv420p"] },
    { name: "h264_nvenc", args: ["-preset", "p5", "-cq", "23", "-g", "30", "-pix_fmt", "yuv420p"] },
    { name: "h264_qsv", args: ["-global_quality", "23", "-g", "30", "-pix_fmt", "yuv420p"] },
    { name: "h264_amf", args: ["-quality", "speed", "-qp_i", "23", "-qp_p", "23", "-g", "30", "-pix_fmt", "yuv420p"] },
    { name: "h264_mf", args: ["-pix_fmt", "yuv420p"] },
  ];
  return candidates.find((candidate) => encoders.includes(candidate.name));
}

async function probeMedia(ffprobePath: string, filePath: string): Promise<MediaProbeSummary> {
  const result = await runCommand(ffprobePath, [
    "-v",
    "error",
    "-show_entries",
    "format=format_name,duration:stream=codec_type,codec_name,width,height,avg_frame_rate,r_frame_rate,pix_fmt",
    "-of",
    "json",
    filePath,
  ]);
  if (result.code !== 0) {
    throw new Error(compactProcessError("ffprobe_failed", result));
  }
  return parseMediaProbeOutput(result.stdout);
}

function parseMediaProbeOutput(stdout: string): MediaProbeSummary {
  const payload = JSON.parse(stdout) as {
    format?: { format_name?: unknown; duration?: unknown };
    streams?: Array<Record<string, unknown>>;
  };
  const summary: MediaProbeSummary = {};
  const formatName = stringValue(payload.format?.format_name);
  if (formatName) summary.format = formatName;
  const duration = numberValue(payload.format?.duration);
  if (duration !== undefined) summary.duration_sec = duration;

  const videoStream = payload.streams?.find((stream) => stringValue(stream.codec_type) === "video");
  if (videoStream) {
    const codecName = stringValue(videoStream.codec_name);
    const width = numberValue(videoStream.width);
    const height = numberValue(videoStream.height);
    const fps = parseFrameRate(stringValue(videoStream.avg_frame_rate) || stringValue(videoStream.r_frame_rate));
    const pixelFormat = stringValue(videoStream.pix_fmt);
    if (codecName) summary.video_codec = codecName;
    if (width !== undefined) summary.width = width;
    if (height !== undefined) summary.height = height;
    if (fps !== undefined) summary.fps = fps;
    if (pixelFormat) summary.pixel_format = pixelFormat;
  }

  const audioStream = payload.streams?.find((stream) => stringValue(stream.codec_type) === "audio");
  if (audioStream) {
    const codecName = stringValue(audioStream.codec_name);
    if (codecName) summary.audio_codec = codecName;
  }
  return summary;
}

function validateNormalizedMedia(output: MediaProbeSummary | undefined, target: MediaNormalizationTarget = MEDIA_NORMALIZATION_TARGET): Record<string, boolean> {
  return {
    container_mp4: output?.format?.split(",").includes("mp4") === true,
    video_codec_h264: output?.video_codec === "h264",
    resolution_matches_target: output?.width === target.width && output?.height === target.height,
    fps_matches_target: output?.fps !== undefined && Math.abs(output.fps - target.fps) <= 0.25,
    pixel_format_yuv420p: output?.pixel_format === target.pixel_format,
    audio_aac_or_silent: output?.audio_codec === "aac",
  };
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() !== "" ? value : undefined;
}

function numberValue(value: unknown): number | undefined {
  const numeric = typeof value === "number" ? value : typeof value === "string" ? Number(value) : Number.NaN;
  return Number.isFinite(numeric) ? numeric : undefined;
}

function parseFrameRate(value: string | undefined): number | undefined {
  if (!value || value === "0/0") return undefined;
  const parts = value.split("/");
  if (parts.length === 2) {
    const numerator = Number(parts[0]);
    const denominator = Number(parts[1]);
    if (Number.isFinite(numerator) && Number.isFinite(denominator) && denominator !== 0) {
      return Math.round((numerator / denominator) * 1000) / 1000;
    }
  }
  const fps = Number(value);
  return Number.isFinite(fps) ? fps : undefined;
}

function firstCompositableVideoArtifact(catalog: AssetTimelineCatalog, editPlan: DemoEditPlan): TimelineArtifact | undefined {
	// A step screenshot can represent a still in the edit plan, but it must
	// never replace the captured recording as the source for MP4 normalization.
	const timelineRecording = findDemoArtifactByID(catalog, catalog.timeline.recording_artifact_id);
	if (timelineRecording && isVideoArtifact(timelineRecording)) return timelineRecording;
	for (const shot of editPlan.shots) {
		const artifact = findDemoArtifactByID(catalog, shot.source_artifact_id);
		if (artifact && isVideoArtifact(artifact)) return artifact;
	}
	return catalog.artifacts.find((artifact) => isDemoMaterial(artifact) && isVideoArtifact(artifact));
}

function buildCompositorShotPlan(catalog: AssetTimelineCatalog, editPlan: DemoEditPlan): CompositorShot[] {
  const fallbackArtifact = firstCompositableVideoArtifact(catalog, editPlan);
  const artifactByID = new Map(catalog.artifacts.map((artifact) => [artifact.id, artifact]));
  return editPlan.shots
    .map((shot) => {
      const still = isStillShot(shot);
      // Never fall back from a missing screenshot to an unrelated recording.
      const artifact = findDemoArtifactByID(catalog, shot.source_artifact_id) || (still ? undefined : fallbackArtifact);
      const sourcePath = artifact?.local_path || (artifact?.uri ? filePathFromURI(artifact.uri) : undefined);
      const sourceRange = still ? undefined : shot.source_time_range_ms || sourceRangeForStep(catalog, shot.source_step_id);
      const durationMS = still ? Math.max(0, shot.output_duration_ms ?? 0) : sourceRange ? Math.max(0, sourceRange[1] - sourceRange[0]) : 0;
      const operations = (shot.operations || []).map((operation) => operation.type);
      const editOperations = (shot.operations || []).map((operation) => ({
        type: operation.type,
        ...(operation.start_ms !== undefined ? { start_ms: operation.start_ms } : {}),
        ...(operation.end_ms !== undefined ? { end_ms: operation.end_ms } : {}),
        ...(operation.zoom !== undefined ? { zoom: operation.zoom } : {}),
        ...(operation.x !== undefined ? { x: operation.x } : {}),
        ...(operation.y !== undefined ? { y: operation.y } : {}),
        ...(operation.scale !== undefined ? { scale: operation.scale } : {}),
        ...(operation.speed !== undefined ? { speed: operation.speed } : {}),
        ...(operation.style !== undefined ? { style: operation.style } : {}),
      }));
      const overlays = (shot.overlays || []).map((overlay) => {
        const plannedOverlay: CompositorShot["overlays"][number] = { type: overlay.type };
        if (overlay.text !== undefined) plannedOverlay.text = overlay.text;
        if (overlay.start_ms !== undefined) plannedOverlay.start_ms = overlay.start_ms;
        if (overlay.end_ms !== undefined) plannedOverlay.end_ms = overlay.end_ms;
        if (overlay.id !== undefined) plannedOverlay.id = overlay.id;
        if (overlay.shape !== undefined) plannedOverlay.shape = overlay.shape;
        if (overlay.x !== undefined) plannedOverlay.x = overlay.x;
        if (overlay.y !== undefined) plannedOverlay.y = overlay.y;
        if (overlay.width !== undefined) plannedOverlay.width = overlay.width;
        if (overlay.height !== undefined) plannedOverlay.height = overlay.height;
        if (overlay.color !== undefined) plannedOverlay.color = overlay.color;
        if (overlay.stroke_width !== undefined) plannedOverlay.stroke_width = overlay.stroke_width;
        if (overlay.rotation !== undefined) plannedOverlay.rotation = overlay.rotation;
        if (overlay.opacity !== undefined) plannedOverlay.opacity = overlay.opacity;
        if (overlay.fill_color !== undefined) plannedOverlay.fill_color = overlay.fill_color;
        if (overlay.fill_opacity !== undefined) plannedOverlay.fill_opacity = overlay.fill_opacity;
        if (overlay.tilt_preset !== undefined) plannedOverlay.tilt_preset = overlay.tilt_preset;
        if (overlay.scale_x !== undefined) plannedOverlay.scale_x = overlay.scale_x;
        if (overlay.scale_y !== undefined) plannedOverlay.scale_y = overlay.scale_y;
        if (overlay.tilt_x !== undefined) plannedOverlay.tilt_x = overlay.tilt_x;
        if (overlay.tilt_y !== undefined) plannedOverlay.tilt_y = overlay.tilt_y;
        if (overlay.target_evidence_artifact_id !== undefined) plannedOverlay.target_evidence_artifact_id = overlay.target_evidence_artifact_id;
        if (overlay.geometry_source !== undefined) plannedOverlay.geometry_source = overlay.geometry_source;
        if (isTargetOrientedOverlay(overlay)) {
          const issue = targetOverlayEvidenceIssue(overlay, artifactByID, shot.source_step_id, shot.source_artifact_id, still);
          if (issue) {
            plannedOverlay.target_geometry_verified = false;
            plannedOverlay.target_geometry_issue = issue;
          } else {
            const evidenceArtifact = artifactByID.get(overlay.target_evidence_artifact_id as string);
            const geometry = targetGeometryFromMetadata(evidenceArtifact?.metadata) as BrowserTargetGeometry;
            const transformed = transformTargetBoxForShot(geometry.element_box_normalized, editOperations);
            const targetBox = overlay.type === "cursor_highlight"
              ? cursorBoxAtTargetCenter(transformed)
              : paddedNormalizedTargetBox(transformed);
            plannedOverlay.x = targetBox.x;
            plannedOverlay.y = targetBox.y;
            plannedOverlay.width = targetBox.width;
            plannedOverlay.height = targetBox.height;
            plannedOverlay.target_geometry_verified = true;
            plannedOverlay.coordinate_space = "post_edit_normalized";
          }
        }
        return plannedOverlay;
      });
      const planned: CompositorShot = {
        id: shot.id,
        source_artifact_id: artifact?.id || shot.source_artifact_id,
        presentation_kind: still ? "still" : "video",
        duration_ms: durationMS,
        operations,
        edit_operations: editOperations,
        overlays,
      };
      if (shot.source_step_id) planned.source_step_id = shot.source_step_id;
      if (artifact?.uri) planned.source_uri = artifact.uri;
      if (sourcePath) planned.source_path = sourcePath;
      if (sourceRange) planned.source_time_range_ms = sourceRange;
      return planned;
    })
    .filter((shot) => shot.source_path && shot.duration_ms > 0 && (shot.presentation_kind === "still" || shot.source_time_range_ms));
}

function sourceRangeForStep(catalog: AssetTimelineCatalog, stepID?: string): [number, number] | undefined {
  if (!stepID) return undefined;
  const step = catalog.steps.find((candidate) => candidate.step_id === stepID);
  if (!step) return undefined;
  return [step.start_ms, step.end_ms];
}

function plannedOperations(shots: CompositorShot[], editPlan?: DemoEditPlan): string[] {
  const values = new Set<string>();
  for (const shot of shots) {
    for (const operation of shot.operations) {
      values.add(operation);
    }
    if (shot.presentation_kind === "video" && shot.source_time_range_ms) {
      values.add("trim");
    }
    for (const overlay of shot.overlays) {
      values.add(overlay.type);
    }
  }
  if ((editPlan?.narrations?.length || 0) > 0) {
    values.add("narration");
  }
  if ((editPlan?.caption_cues?.length || 0) > 0) {
    values.add("caption");
  }
  return [...values].sort();
}

function mediaNormalizationTarget(profile?: RenderProfile): MediaNormalizationTarget {
  if (!profile) return { ...MEDIA_NORMALIZATION_TARGET };
  return {
    ...MEDIA_NORMALIZATION_TARGET,
    width: boundedInteger(profile.width, MEDIA_NORMALIZATION_TARGET.width, 320, 3840),
    height: boundedInteger(profile.height, MEDIA_NORMALIZATION_TARGET.height, 180, 2160),
    fps: boundedInteger(profile.fps, MEDIA_NORMALIZATION_TARGET.fps, 1, 60),
  };
}

function presentationStillArtifactsForStep(
  catalog: AssetTimelineCatalog,
  step: TimelineStep,
  geometryEvidence?: { artifact: TimelineArtifact; geometry: BrowserTargetGeometry },
): TimelineArtifact[] {
  const candidates = [
    ...(geometryEvidence ? [geometryEvidence.artifact] : []),
    ...step.artifacts.map((artifactID) => findDemoArtifactByID(catalog, artifactID)),
    ...catalog.artifacts.filter((artifact) => artifact.source_step_id === step.step_id),
  ];
  const seen = new Set<string>();
  return candidates.filter((artifact): artifact is TimelineArtifact => {
    if (!artifact || seen.has(artifact.id) || !isPresentationStillArtifact(artifact)) return false;
    seen.add(artifact.id);
    return true;
  });
}

function selectStillCandidatesForDuration<T extends { stepIndex: number }>(candidates: T[], stepCount: number, targetDurationMS: number): T[] {
  const maxShots = Math.max(1, Math.floor(targetDurationMS / 250));
  if (candidates.length <= maxShots) return candidates;
  const primary = candidates.filter((candidate, index) => candidates.findIndex((item) => item.stepIndex === candidate.stepIndex) === index);
  if (primary.length >= maxShots) return primary.slice(0, maxShots);
  if (targetDurationMS < candidates.length * 250 || stepCount > maxShots) return primary;
  return candidates.slice(0, maxShots);
}

function distributeStillDurations(count: number, targetDurationMS: number): number[] {
  if (count <= 0) return [];
  const deliverableDurationMS = Math.min(targetDurationMS, count * 15_000);
  const base = Math.max(250, Math.floor(deliverableDurationMS / count));
  let remainder = Math.max(0, deliverableDurationMS - base * count);
  return Array.from({ length: count }, () => {
    const extra = Math.min(remainder, 15_000 - base);
    remainder -= extra;
    return base + extra;
  });
}

function isTargetOrientedOverlay(overlay: { type: string }): boolean {
  return overlay.type === "highlight_box" || overlay.type === "cursor_highlight";
}

function targetOverlayEvidenceIssue(
  overlay: Pick<EditOverlay, "target_evidence_artifact_id" | "geometry_source">,
  artifactByID: Map<string, TimelineArtifact>,
  shotStepID?: string,
  shotSourceArtifactID?: string,
  still = false,
): string | undefined {
  if (!overlay.target_evidence_artifact_id || overlay.geometry_source !== "browser_agent_target_geometry_v1") {
    return "missing_target_geometry";
  }
  const artifact = artifactByID.get(overlay.target_evidence_artifact_id);
  const geometry = targetGeometryFromMetadata(artifact?.metadata);
  if (!artifact || !geometry) return "invalid_target_geometry_evidence";
  if (artifact.source_step_id && shotStepID && artifact.source_step_id !== shotStepID) return "target_geometry_step_mismatch";
  if (geometry.screenshot_artifact_id && geometry.screenshot_artifact_id !== artifact.id) return "target_geometry_artifact_mismatch";
  if (still && shotSourceArtifactID !== artifact.id) return "target_geometry_source_mismatch";
  return undefined;
}

function transformTargetBoxForShot(
  sourceBox: BrowserTargetGeometry["element_box_normalized"],
  operations: CompositorShot["edit_operations"],
): BrowserTargetGeometry["element_box_normalized"] {
  let box = { ...sourceBox };
  for (const operation of operations) {
    if (operation.type === "crop" || operation.type === "pan") {
      const margin = operation.type === "crop" ? 0.04 : 0.03;
      box = transformNormalizedBoxThroughWindow(box, margin, margin, 1 - margin * 2, 1 - margin * 2);
      continue;
    }
    if (operation.type !== "zoom_pan") continue;
    const zoom = boundedNumber(operation.zoom ?? operation.scale ?? 1.08, 1.08, 1, 3);
    const panX = boundedNumber(operation.x ?? 0.5, 0.5, 0, 1);
    const panY = boundedNumber(operation.y ?? 0.5, 0.5, 0, 1);
    const windowWidth = 1 / zoom;
    const windowHeight = 1 / zoom;
    box = transformNormalizedBoxThroughWindow(
      box,
      (1 - windowWidth) * panX,
      (1 - windowHeight) * panY,
      windowWidth,
      windowHeight,
    );
  }
  return box;
}

function transformNormalizedBoxThroughWindow(
  box: BrowserTargetGeometry["element_box_normalized"],
  windowX: number,
  windowY: number,
  windowWidth: number,
  windowHeight: number,
): BrowserTargetGeometry["element_box_normalized"] {
  const left = clamp01((box.x - windowX) / windowWidth);
  const top = clamp01((box.y - windowY) / windowHeight);
  const right = clamp01((box.x + box.width - windowX) / windowWidth);
  const bottom = clamp01((box.y + box.height - windowY) / windowHeight);
  return { x: left, y: top, width: Math.max(0.001, right - left), height: Math.max(0.001, bottom - top) };
}

function cursorBoxAtTargetCenter(box: BrowserTargetGeometry["element_box_normalized"]): BrowserTargetGeometry["element_box_normalized"] {
  const width = Math.min(0.06, Math.max(0.025, box.width * 0.35));
  const height = Math.min(0.09, Math.max(0.04, box.height * 0.55));
  const centerX = box.x + box.width / 2;
  const centerY = box.y + box.height / 2;
  const x = clamp01(centerX - width / 2);
  const y = clamp01(centerY - height / 2);
  return { x, y, width: Math.min(width, 1 - x), height: Math.min(height, 1 - y) };
}

function appliedOperationsForCurrentCompositor(shots: CompositorShot[], editPlan: DemoEditPlan): string[] {
  const values = new Set<string>();
  for (const shot of shots) {
    for (const operation of shot.edit_operations || []) {
      if (["crop", "zoom_pan", "pan", "speed", "transition", "color_grade"].includes(operation.type) && shot.presentation_kind === "video") values.add(operation.type);
    }
    if (shot.presentation_kind === "video" && shot.source_time_range_ms) {
      values.add("trim");
    }
    if (captionCuesForShot(shot).length > 0) {
      values.add("caption");
    }
    if (shapeCuesForShot(shot).length > 0) {
      for (const overlay of shot.overlays) {
        if (overlay.shape && ["rectangle", "circle", "polygon", "star", "line", "arrow"].includes(overlay.shape)) values.add(overlay.type);
      }
    }
    if (cursorHighlightCuesForShot(shot).length > 0) values.add("cursor_highlight");
    if (shot.overlays.some((overlay) => overlay.type === "blur_region")) values.add("blur_region");
  }
  if (shots.length > 1) {
    values.add("concat");
  }
  if ((editPlan.narrations?.length || 0) > 0) {
    values.add("narration");
  }
  if ((editPlan.caption_cues?.length || 0) > 0) {
    values.add("caption");
  }
  return [...values].sort();
}

function skippedOperationsForCurrentCompositor(shots: CompositorShot[], editPlan: DemoEditPlan | undefined, skipTrim: boolean): Array<{ type: string; reason: string }> {
  const skipped = new Map<string, string>();
  const applied = skipTrim || !editPlan
    ? new Set<string>()
    : new Set(appliedOperationsForCurrentCompositor(shots, editPlan));
  for (const shot of shots) {
    for (const overlay of shot.overlays) {
      if (isTargetOrientedOverlay(overlay) && overlay.target_geometry_verified !== true) {
        skipped.set(overlay.type, overlay.target_geometry_issue || "missing_target_geometry");
      }
      if (overlay.shape && !["rectangle", "circle", "polygon", "star", "line", "arrow"].includes(overlay.shape)) {
        skipped.set(`shape:${overlay.shape}`, "current deterministic compositor does not burn this shape type into pixels yet");
      }
    }
  }
  for (const operation of plannedOperations(shots, editPlan)) {
    if (operation === "trim" && !skipTrim) {
      continue;
    }
    if (operation === "trim") {
      skipped.set(operation, "ffmpeg trim/concat was not executed in fallback mode");
      continue;
    }
    if (applied.has(operation)) {
      continue;
    }
    if (skipped.has(operation)) {
      continue;
    }
    skipped.set(operation, "current deterministic compositor does not burn this operation into pixels yet");
  }
  return [...skipped.entries()].map(([type, reason]) => ({ type, reason }));
}

function captionCuesForShot(shot: CompositorShot): CaptionCue[] {
  const cues: CaptionCue[] = [];
  for (const overlay of shot.overlays) {
    if (overlay.type !== "caption" || !overlay.text?.trim()) {
      continue;
    }
    const startMS = Math.max(0, overlay.start_ms ?? 0);
    const endMS = Math.min(shot.duration_ms, Math.max(startMS + 500, overlay.end_ms ?? Math.min(shot.duration_ms, startMS + 3000)));
    if (endMS <= startMS) {
      continue;
    }
    cues.push({
      start_ms: startMS,
      end_ms: endMS,
      text: overlay.text.trim(),
    });
  }
  return cues;
}

function shapeCuesForShot(shot: CompositorShot): ShapeCue[] {
  const cues: ShapeCue[] = [];
  for (const overlay of shot.overlays) {
    if (!overlay.shape || !["rectangle", "circle", "polygon", "star", "line", "arrow"].includes(overlay.shape)) continue;
    if (overlay.type === "highlight_box" && overlay.target_geometry_verified !== true) continue;
    const startMS = Math.max(0, overlay.start_ms ?? 0);
    const endMS = Math.min(shot.duration_ms, Math.max(startMS + 100, overlay.end_ms ?? shot.duration_ms));
    if (endMS <= startMS) continue;
    cues.push({
      type: overlay.shape as ShapeCue["type"], start_ms: startMS, end_ms: endMS,
      x: normalizedShapeNumber(overlay.x, 0.3), y: normalizedShapeNumber(overlay.y, 0.3),
      width: normalizedShapeNumber(overlay.width, 0.3), height: normalizedShapeNumber(overlay.height, 0.2),
      color: overlay.color || "#dc58d5", stroke_width: boundedInteger(overlay.stroke_width ?? 4, 4, 1, 24),
      rotation: boundedNumber(overlay.rotation ?? 0, 0, -180, 180), opacity: boundedNumber(overlay.opacity ?? 100, 100, 0, 100),
      fill_color: overlay.fill_color || overlay.color || "#dc58d5", fill_opacity: boundedNumber(overlay.fill_opacity ?? 10, 10, 0, 100),
      scale_x: boundedNumber(overlay.scale_x ?? 100, 100, 20, 250), scale_y: boundedNumber(overlay.scale_y ?? 100, 100, 20, 250),
    });
  }
  return cues;
}

function cursorHighlightCuesForShot(shot: CompositorShot): ShapeCue[] {
  return shot.overlays.filter((overlay) => overlay.type === "cursor_highlight" && overlay.target_geometry_verified === true).map((overlay) => ({
    type: "circle",
    start_ms: Math.max(0, overlay.start_ms ?? 0),
    end_ms: Math.min(shot.duration_ms, Math.max((overlay.start_ms ?? 0) + 100, overlay.end_ms ?? shot.duration_ms)),
    x: normalizedShapeNumber(overlay.x, 0.5), y: normalizedShapeNumber(overlay.y, 0.5),
    width: normalizedShapeNumber(overlay.width, 0.045), height: normalizedShapeNumber(overlay.height, 0.07),
    color: overlay.color || "#ffd166", stroke_width: boundedInteger(overlay.stroke_width ?? 5, 5, 1, 24),
    rotation: 0, opacity: boundedNumber(overlay.opacity ?? 95, 95, 0, 100),
    fill_color: overlay.fill_color || "#ffd166", fill_opacity: boundedNumber(overlay.fill_opacity ?? 18, 18, 0, 100),
    scale_x: 100, scale_y: 100,
  }));
}

function buildVideoOperationFilters(operations: CompositorShot["edit_operations"], durationMS: number): { filters: string[]; audioTempo: string | undefined } {
  const filters: string[] = [];
  let audioTempo: string | undefined;
  for (const operation of operations || []) {
    switch (operation.type) {
      case "crop": filters.push("crop=iw*0.92:ih*0.92:iw*0.04:ih*0.04"); break;
      case "zoom_pan": {
        const zoom = boundedNumber(operation.zoom ?? operation.scale ?? 1.08, 1.08, 1, 3);
        const x = boundedNumber(operation.x ?? 0.5, 0.5, 0, 1); const y = boundedNumber(operation.y ?? 0.5, 0.5, 0, 1);
        filters.push(`scale=iw*${zoom.toFixed(3)}:ih*${zoom.toFixed(3)},crop=iw/${zoom.toFixed(3)}:ih/${zoom.toFixed(3)}:(iw-ow)*${x.toFixed(3)}:(ih-oh)*${y.toFixed(3)}`);
        break;
      }
      case "pan": filters.push("crop=iw*0.94:ih*0.94:iw*0.03:ih*0.03"); break;
      case "speed": { const speed = boundedNumber(operation.speed ?? 1, 1, 0.5, 2); if (Math.abs(speed - 1) > 0.001) { filters.push(`setpts=PTS/${speed.toFixed(3)}`); audioTempo = `atempo=${speed.toFixed(3)}`; } break; }
      case "transition": filters.push(`fade=t=in:st=0:d=${Math.min(0.4, Math.max(0.1, durationMS / 1000)).toFixed(3)}`); break;
      case "color_grade": filters.push("eq=contrast=1.03:saturation=1.04:brightness=0.01"); break;
      // Regional privacy blur is composed in composeWithFFmpeg after the
      // normalized frame is available. Do not add a global blur here.
      case "blur_region": break;
    }
  }
  return { filters, audioTempo };
}

function normalizedShapeNumber(value: number | undefined, fallback: number): number {
  return boundedNumber(value ?? fallback, fallback, 0, 1);
}

function boundedNumber(value: number, fallback: number, min: number, max: number): number {
  return Number.isFinite(value) ? Math.max(min, Math.min(max, value)) : fallback;
}

function assSubtitleForCaptions(cues: CaptionCue[]): string {
  const lines = [
    "[Script Info]",
    "ScriptType: v4.00+",
    "PlayResX: 1920",
    "PlayResY: 1080",
    "WrapStyle: 2",
    "ScaledBorderAndShadow: yes",
    "YCbCr Matrix: TV.709",
    "",
    "[V4+ Styles]",
    "Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding",
    "Style: Default,Arial,34,&H00FFFFFF,&H00FFFFFF,&HAA000000,&HAA000000,0,0,0,0,100,100,0,0,3,1.2,0,2,160,160,72,1",
    "",
    "[Events]",
    "Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text",
  ];
  for (const cue of cues) {
    lines.push(`Dialogue: 0,${assTimestamp(cue.start_ms)},${assTimestamp(cue.end_ms)},Default,,0,0,0,,${escapeAssText(wrapCaptionText(cue.text))}`);
  }
  return `${lines.join("\n")}\n`;
}

function assSubtitleForShotOverlays(captions: CaptionCue[], shapes: ShapeCue[]): string {
  const lines = assHeader();
  for (const cue of captions) lines.push(`Dialogue: 0,${assTimestamp(cue.start_ms)},${assTimestamp(cue.end_ms)},Default,,0,0,0,,${escapeAssText(wrapCaptionText(cue.text))}`);
  for (const shape of shapes) lines.push(`Dialogue: 10,${assTimestamp(shape.start_ms)},${assTimestamp(shape.end_ms)},Shape,,0,0,0,,${assShapeDrawing(shape)}`);
  return `${lines.join("\n")}\n`;
}

function assHeader(): string[] {
  return [
    "[Script Info]", "ScriptType: v4.00+", "PlayResX: 1920", "PlayResY: 1080", "WrapStyle: 2", "ScaledBorderAndShadow: yes", "YCbCr Matrix: TV.709", "",
    "[V4+ Styles]", "Format: Name,Fontname,Fontsize,PrimaryColour,SecondaryColour,OutlineColour,BackColour,Bold,Italic,Underline,StrikeOut,ScaleX,ScaleY,Spacing,Angle,BorderStyle,Outline,Shadow,Alignment,MarginL,MarginR,MarginV,Encoding",
    "Style: Default,Arial,34,&H00FFFFFF,&H00FFFFFF,&HAA000000,&HAA000000,0,0,0,0,100,100,0,0,3,1.2,0,2,160,160,72,1",
    "Style: Shape,Arial,20,&H00000000,&H00000000,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,1,0,7,0,0,0,1", "",
    "[Events]", "Format: Layer,Start,End,Style,Name,MarginL,MarginR,MarginV,Effect,Text",
  ];
}

function assShapeDrawing(shape: ShapeCue): string {
  const baseWidth = Math.max(12, Math.round(shape.width * 1920));
  const baseHeight = Math.max(12, Math.round(shape.height * 1080));
  const width = Math.max(12, Math.round(baseWidth * shape.scale_x / 100));
  const height = Math.max(12, Math.round(baseHeight * shape.scale_y / 100));
  // Match CSS transform-origin:center: keep the visual center fixed while scaling.
  const x = Math.round(shape.x * 1920 + (baseWidth - width) / 2);
  const y = Math.round(shape.y * 1080 + (baseHeight - height) / 2);
  const centerX = x + Math.round(width / 2);
  const centerY = y + Math.round(height / 2);
  const stroke = Math.max(1, shape.stroke_width);
  const outline = assColor(shape.color, shape.opacity);
  const fill = assColor(shape.fill_color, shape.opacity * shape.fill_opacity / 100);
  const drawing = shape.type === "circle" ? assEllipsePath(width, height) : shape.type === "polygon" ? assPolygonPath(width, height) : shape.type === "star" ? assStarPath(width, height) : shape.type === "line" ? `m 0 ${height / 2} l ${width} ${height / 2}` : shape.type === "arrow" ? assArrowPath(width, height) : `m 0 0 l ${width} 0 l ${width} ${height} l 0 ${height}`;
  const rotation = shape.rotation ? `\\frz${Math.round(shape.rotation)}` : "";
  return `{\\p1\\pos(${x},${y})\\org(${centerX},${centerY})\\bord${stroke}\\c${fill}\\3c${outline}${rotation}}${drawing}{\\p0}`;
}

function assEllipsePath(width: number, height: number): string {
  const rx = width / 2;
  const ry = height / 2;
  const k = 0.55228475;
  return `m ${rx} 0 b ${rx + k * rx} 0 ${width} ${ry - k * ry} ${width} ${ry} b ${width} ${ry + k * ry} ${rx + k * rx} ${height} ${rx} ${height} b ${rx - k * rx} ${height} 0 ${ry + k * ry} 0 ${ry} b 0 ${ry - k * ry} ${rx - k * rx} 0 ${rx} 0`;
}

function assArrowPath(width: number, height: number): string {
  const mid = Math.round(height / 2);
  const head = Math.max(14, Math.min(64, Math.round(width * 0.2)));
  return `m 0 ${mid - 3} l ${width - head} ${mid - 3} l ${width - head} 0 l ${width} ${mid} l ${width - head} ${height} l ${width - head} ${mid + 3} l 0 ${mid + 3}`;
}

function assPolygonPath(width: number, height: number): string {
  return `m ${width / 2} 0 l ${width} ${height * 0.25} l ${width * 0.82} ${height} l ${width * 0.18} ${height} l 0 ${height * 0.25}`;
}

function assStarPath(width: number, height: number): string {
  const points: Array<[number, number]> = [
    [0.5, 0], [0.61, 0.35], [0.98, 0.35], [0.68, 0.56], [0.79, 0.92],
    [0.5, 0.7], [0.21, 0.92], [0.32, 0.56], [0.02, 0.35], [0.39, 0.35],
  ];
  return points.map(([x, y], index) => `${index === 0 ? "m" : "l"} ${Math.round(x * width)} ${Math.round(y * height)}`).join(" ");
}

function assColor(value: string, opacityPercent: number): string {
  const hex = String(value || "#dc58d5").replace("#", "");
  const red = /^[0-9a-f]{6}$/i.test(hex) ? Number.parseInt(hex.slice(0, 2), 16) : 220;
  const green = /^[0-9a-f]{6}$/i.test(hex) ? Number.parseInt(hex.slice(2, 4), 16) : 88;
  const blue = /^[0-9a-f]{6}$/i.test(hex) ? Number.parseInt(hex.slice(4, 6), 16) : 213;
  const alpha = Math.round(255 * (1 - boundedNumber(opacityPercent, 100, 0, 100) / 100));
  return `&H${alpha.toString(16).padStart(2, "0").toUpperCase()}${blue.toString(16).padStart(2, "0").toUpperCase()}${green.toString(16).padStart(2, "0").toUpperCase()}${red.toString(16).padStart(2, "0").toUpperCase()}&`;
}

function wrapCaptionText(text: string): string {
  const normalized = text.replace(/\s+/g, " ").trim();
  if (normalized.length <= 34) return normalized;
  const words = normalized.split(" ");
  if (words.length === 1) {
    return normalized.replace(/(.{24})/g, "$1\n").trim();
  }
  const lines: string[] = [];
  let current = "";
  for (const word of words) {
    const candidate = current ? `${current} ${word}` : word;
    if (candidate.length > 34 && current) {
      lines.push(current);
      current = word;
    } else {
      current = candidate;
    }
  }
  if (current) lines.push(current);
  return lines.slice(0, 2).join("\n");
}

function escapeAssText(text: string): string {
  return text.replace(/\r\n|\r|\n/g, "\\N").replace(/[{}]/g, "");
}

function assTimestamp(valueMS: number): string {
  const totalCentiseconds = Math.max(0, Math.round(valueMS / 10));
  const centiseconds = totalCentiseconds % 100;
  const totalSeconds = Math.floor(totalCentiseconds / 100);
  const seconds = totalSeconds % 60;
  const totalMinutes = Math.floor(totalSeconds / 60);
  const minutes = totalMinutes % 60;
  const hours = Math.floor(totalMinutes / 60);
  return `${hours}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}.${String(centiseconds).padStart(2, "0")}`;
}

async function composeWithFFmpeg(
  ffmpegPath: string,
  outputDir: string,
  videoPath: string,
  shots: CompositorShot[],
  extension: string,
  renderProfile?: RenderProfile,
  audioPolicy: NonNullable<DemoEditPlan["audio"]> = { mode: "source", volume_percent: 100 },
  catalog?: AssetTimelineCatalog,
  editPlan?: DemoEditPlan,
): Promise<{ rendered: boolean; error?: string; video_codec?: string; audio_codec?: string; encoding_audit?: VideoEncodingAudit }> {
  const segmentsDir = path.join(outputDir, "segments");
  await mkdir(segmentsDir, { recursive: true });
  const segmentPaths: string[] = [];
  const codec = await preferredVideoCodec(ffmpegPath, extension, renderProfile);
  const audioCodec = await preferredAudioCodec(ffmpegPath, extension);
  const sourceAudio = new Map<string, boolean>();
  let outputCursorMS = 0;
  for (const [index, shot] of shots.entries()) {
    if (!shot.source_path) {
      continue;
    }
    const still = shot.presentation_kind === "still";
    if (!still && !shot.source_time_range_ms) continue;
    const [startMS, endMS] = shot.source_time_range_ms ?? [0, shot.duration_ms];
    const segmentStartMS = still ? 0 : startMS;
    const segmentDurationMS = still ? shot.duration_ms : endMS - startMS;
    if (segmentDurationMS <= 0) {
      continue;
    }
    const segmentPath = path.join(segmentsDir, `${String(index + 1).padStart(3, "0")}_${safeName(shot.id)}${extension}`);
    const videoFilters: string[] = [];
    let outputFPS = 30;
    if (renderProfile) {
      const width = boundedInteger(renderProfile.width, renderProfile.mode === "preview" ? 1280 : 2560, 320, 3840);
      const height = boundedInteger(renderProfile.height, renderProfile.mode === "preview" ? 720 : 1440, 180, 2160);
      const fps = boundedInteger(renderProfile.fps, 30, 1, 60);
      outputFPS = fps;
      videoFilters.push(`scale=${width}:${height}:force_original_aspect_ratio=decrease,pad=${width}:${height}:(ow-iw)/2:(oh-ih)/2,fps=${fps},format=yuv420p`);
    }
    const operationFilterResult = buildVideoOperationFilters(shot.edit_operations, segmentDurationMS);
    videoFilters.unshift(...operationFilterResult.filters);
    const captionCues = [
      ...captionCuesForShot(shot),
      ...globalCaptionCuesForOutputWindow(editPlan, outputCursorMS, segmentDurationMS),
    ];
    const shapeCues = shapeCuesForShot(shot);
    const cursorCues = cursorHighlightCuesForShot(shot);
    if (captionCues.length > 0 || shapeCues.length > 0 || cursorCues.length > 0) {
      const subtitlePath = path.join(segmentsDir, `${String(index + 1).padStart(3, "0")}_${safeName(shot.id)}.ass`);
      await writeFile(subtitlePath, assSubtitleForShotOverlays(captionCues, [...shapeCues, ...cursorCues]), "utf8");
      videoFilters.push(`subtitles='${ffmpegFilterPath(subtitlePath)}'`);
    }
    const blurOverlays = shot.overlays.filter((overlay) => overlay.type === "blur_region");
    const volumeExpression = audioVolumeExpression(audioPolicy, outputCursorMS, segmentDurationMS, segmentStartMS);
    let useSourceAudio = false;
    const ffmpegArgs = still
      ? [
          "-y", "-loop", "1", "-framerate", String(outputFPS), "-t", secondsArg(segmentDurationMS), "-i", shot.source_path,
          "-f", "lavfi", "-t", secondsArg(segmentDurationMS), "-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
        ]
      : ["-y", "-i", shot.source_path];
    if (!still) {
      let sourceHasAudio = sourceAudio.get(shot.source_path);
      if (sourceHasAudio === undefined) {
        sourceHasAudio = await hasAudioStream(ffmpegPath, shot.source_path);
        sourceAudio.set(shot.source_path, sourceHasAudio);
      }
      useSourceAudio = volumeExpression.usesSource && sourceHasAudio;
      if (!useSourceAudio) ffmpegArgs.push("-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000");
    }
    if (!still) ffmpegArgs.push("-ss", secondsArg(segmentStartMS));
    ffmpegArgs.push(
      "-t", secondsArg(segmentDurationMS),
      "-map", useSourceAudio ? "0:a:0" : "1:a:0",
    );
    if (blurOverlays.length > 0) {
      // Regional blur must split the already-normalized frame, blur a crop,
      // and overlay that crop back. A plain boxblur would incorrectly blur
      // the entire page and make the deliverable look unusably soft.
      const base = videoFilters.length > 0 ? videoFilters.join(",") : "null";
      const graphParts = [`[0:v]${base},split=2[main][blur_src]`];
      let current = "main";
      blurOverlays.forEach((overlay, blurIndex) => {
        const x = boundedNumber(overlay.x ?? 0, 0, 0, 1);
        const y = boundedNumber(overlay.y ?? 0, 0, 0, 1);
        const width = boundedNumber(overlay.width ?? 0.2, 0.2, 0.01, 1);
        const height = boundedNumber(overlay.height ?? 0.12, 0.12, 0.01, 1);
        const start = Math.max(0, overlay.start_ms ?? 0) / 1000;
        const end = Math.max(start + 0.1, Math.min(segmentDurationMS, overlay.end_ms ?? segmentDurationMS) / 1000);
        const cropLabel = `blur_crop_${blurIndex}`;
        const outLabel = `blur_main_${blurIndex}`;
        graphParts.push(`[blur_src]crop=iw*${width.toFixed(4)}:ih*${height.toFixed(4)}:iw*${x.toFixed(4)}:ih*${y.toFixed(4)},boxblur=luma_radius=8:luma_power=2[${cropLabel}]`);
        graphParts.push(`[${current}][${cropLabel}]overlay=main_w*${x.toFixed(4)}:main_h*${y.toFixed(4)}:enable='between(t,${start.toFixed(3)},${end.toFixed(3)})'[${outLabel}]`);
        current = outLabel;
      });
      graphParts.push(`[${current}]null[vout]`);
      ffmpegArgs.push("-filter_complex", graphParts.join(";"), "-map", "[vout]");
    } else if (videoFilters.length > 0) {
      ffmpegArgs.push("-vf", videoFilters.join(","), "-map", "0:v:0");
    } else {
      ffmpegArgs.push("-map", "0:v:0");
    }
    if (useSourceAudio) {
      const tempo = operationFilterResult.audioTempo ? `${operationFilterResult.audioTempo},` : "";
      ffmpegArgs.push("-af", `asetpts=PTS-STARTPTS,${tempo}volume='${volumeExpression.expression}':eval=frame,aresample=48000`);
    }
    if (still) {
      // Some Windows FFmpeg builds do not reliably stop a looped image input
      // at output -t. A deterministic frame cap gives both video and the
      // synthetic audio stream a finite endpoint.
      ffmpegArgs.push("-frames:v", String(Math.max(1, Math.ceil(segmentDurationMS * outputFPS / 1000))));
    }
    ffmpegArgs.push(
      "-c:v",
      codec.name,
      ...codec.args,
      "-c:a",
      audioCodec.name,
      ...audioCodec.args,
      "-avoid_negative_ts",
      "make_zero",
      "-shortest",
      segmentPath,
    );
    const result = await runCommand(ffmpegPath, ffmpegArgs);
    if (result.code !== 0) {
      return { rendered: false, error: compactProcessError("ffmpeg_segment_failed", result) };
    }
    segmentPaths.push(segmentPath);
    outputCursorMS += segmentDurationMS;
  }
  if (segmentPaths.length === 0) {
    return { rendered: false, error: "no_segments_created" };
  }
  if (segmentPaths.length === 1) {
    const onlySegmentPath = segmentPaths[0];
    if (!onlySegmentPath) {
      return { rendered: false, error: "missing_single_segment" };
    }
    await copyFile(onlySegmentPath, videoPath);
    return applyNarrationsAndGlobalCaptions(ffmpegPath, outputDir, videoPath, extension, codec, audioCodec, catalog, editPlan, {
      source_master_preserved: true,
      segment_video_encode_passes: 1,
      concat_video_mode: "not_applicable",
      post_process_video_mode: "not_applicable",
      max_lossy_video_encode_passes_per_output_frame: 1,
      global_captions_embedded_during_segment_encode: (editPlan?.caption_cues?.length || 0) > 0,
    });
  }
  const concatListPath = path.join(segmentsDir, "concat.txt");
  await writeFile(concatListPath, segmentPaths.map((segmentPath) => `file '${ffmpegConcatPath(segmentPath)}'`).join("\n") + "\n", "utf8");
  const concatResult = await runCommand(ffmpegPath, [
    "-y", "-f", "concat", "-safe", "0", "-i", concatListPath,
    // Every segment was produced by the same encoder/profile above. Stream
    // copy preserves those pixels and prevents a second lossy video encode.
    "-map", "0:v:0", "-map", "0:a:0", "-c", "copy", "-movflags", "+faststart", videoPath,
  ]);
  if (concatResult.code !== 0) {
    return { rendered: false, error: compactProcessError("ffmpeg_concat_failed", concatResult) };
  }
  return applyNarrationsAndGlobalCaptions(ffmpegPath, outputDir, videoPath, extension, codec, audioCodec, catalog, editPlan, {
    source_master_preserved: true,
    segment_video_encode_passes: 1,
    concat_video_mode: "stream_copy",
    post_process_video_mode: "not_applicable",
    max_lossy_video_encode_passes_per_output_frame: 1,
    global_captions_embedded_during_segment_encode: (editPlan?.caption_cues?.length || 0) > 0,
  });
}

function hasPostProcessingWork(editPlan: DemoEditPlan): boolean {
  return (editPlan.narrations?.length || 0) > 0 || (editPlan.caption_cues?.length || 0) > 0;
}

function hasAudioPostProcessingWork(editPlan: DemoEditPlan): boolean {
  return (editPlan.narrations?.length || 0) > 0;
}

export function globalCaptionCuesForOutputWindow(editPlan: DemoEditPlan | undefined, outputStartMS: number, durationMS: number): CaptionCue[] {
  const outputEndMS = outputStartMS + durationMS;
  return (editPlan?.caption_cues || []).flatMap((cue) => {
    const startMS = Math.max(outputStartMS, cue.output_range_ms[0]);
    const endMS = Math.min(outputEndMS, cue.output_range_ms[1]);
    if (endMS <= startMS) return [];
    return [{ start_ms: startMS - outputStartMS, end_ms: endMS - outputStartMS, text: cue.text }];
  });
}

async function buildNarrationRenderInputs(catalog: AssetTimelineCatalog, editPlan: DemoEditPlan): Promise<{ inputs?: NarrationRenderInput[]; error?: string }> {
  const inputs: NarrationRenderInput[] = [];
  for (const narration of editPlan.narrations || []) {
    const artifact = findDemoArtifactByID(catalog, narration.source_artifact_id);
    const sourcePath = artifact?.local_path || (artifact?.uri ? filePathFromURI(artifact.uri) : undefined);
    if (!artifact || !sourcePath || !(await stat(sourcePath).catch(() => undefined))?.isFile()) {
      return { error: `narration_source_unavailable:${narration.source_artifact_id}` };
    }
    const sourceRange = narration.source_time_range_ms || [0, artifact.duration_ms || 0] as [number, number];
    if (sourceRange[1] <= sourceRange[0]) {
      return { error: `invalid_narration_source_range:${narration.id}` };
    }
    inputs.push({
      id: narration.id,
      source_path: sourcePath,
      source_time_range_ms: sourceRange,
      output_time_range_ms: narration.output_time_range_ms,
      volume_percent: narration.volume_percent,
      duck_source_audio: narration.duck_source_audio === true,
      duck_source_to_percent: narration.duck_source_to_percent ?? 30,
    });
  }
  return { inputs };
}

async function applyNarrationsAndGlobalCaptions(
  ffmpegPath: string,
  outputDir: string,
  videoPath: string,
  extension: string,
  videoCodec: VideoCodecConfig,
  audioCodec: VideoCodecConfig,
  catalog?: AssetTimelineCatalog,
  editPlan?: DemoEditPlan,
  encodingAudit?: VideoEncodingAudit,
): Promise<{ rendered: boolean; error?: string; video_codec?: string; audio_codec?: string; encoding_audit?: VideoEncodingAudit }> {
  if (!editPlan || !hasAudioPostProcessingWork(editPlan)) {
    return {
      rendered: true,
      video_codec: videoCodec.name,
      audio_codec: audioCodec.name,
      ...(encodingAudit ? { encoding_audit: encodingAudit } : {}),
    };
  }
  if (!catalog) {
    return { rendered: false, error: "missing_catalog_for_audio_post_process" };
  }
  const narrationPlan = await buildNarrationRenderInputs(catalog, editPlan);
  if (!narrationPlan.inputs) {
    return { rendered: false, error: narrationPlan.error || "invalid_narration_plan" };
  }

  const postProcessDir = path.join(outputDir, "postprocess");
  await mkdir(postProcessDir, { recursive: true });
  const postProcessPath = path.join(postProcessDir, `rendered_${safeName(editPlan.plan_id)}${extension}`);
  const ffmpegArgs = ["-y", "-i", videoPath];
  for (const narration of narrationPlan.inputs) {
    ffmpegArgs.push("-i", narration.source_path);
  }

  const filters: string[] = [];
  if (narrationPlan.inputs.length > 0) {
    filters.push(`[0:a]volume='${duckedSourceVolumeExpression(narrationPlan.inputs)}':eval=frame,asetpts=PTS-STARTPTS[base_audio]`);
    const narrationLabels: string[] = [];
    narrationPlan.inputs.forEach((narration, index) => {
      const label = `narration_${index}`;
      narrationLabels.push(`[${label}]`);
      filters.push(
        `[${index + 1}:a]atrim=start=${secondsArg(narration.source_time_range_ms[0])}:end=${secondsArg(narration.source_time_range_ms[1])},asetpts=PTS-STARTPTS,aresample=48000,aformat=channel_layouts=stereo,volume=${(narration.volume_percent / 100).toFixed(2)},adelay=${Math.round(narration.output_time_range_ms[0])}:all=1[${label}]`,
      );
    });
    filters.push(`[base_audio]${narrationLabels.join("")}amix=inputs=${narrationPlan.inputs.length + 1}:duration=first:normalize=0[mixed_audio]`);
  }

  if (filters.length > 0) {
    ffmpegArgs.push("-filter_complex", filters.join(";"));
  }
  // Global captions are burned into each segment during its only video encode.
  // Narration changes audio only, so preserve the already-rendered video bitstream.
  ffmpegArgs.push("-map", "0:v:0", "-map", narrationPlan.inputs.length > 0 ? "[mixed_audio]" : "0:a:0", "-c:v", "copy", "-c:a", audioCodec.name, ...audioCodec.args, "-movflags", "+faststart", postProcessPath);
  const result = await runCommand(ffmpegPath, ffmpegArgs);
  if (result.code !== 0) {
    return { rendered: false, error: compactProcessError("ffmpeg_audio_caption_post_process_failed", result) };
  }
  await copyFile(postProcessPath, videoPath);
  const renderedResult: { rendered: boolean; video_codec: string; audio_codec: string; encoding_audit?: VideoEncodingAudit } = {
    rendered: true,
    video_codec: videoCodec.name,
    audio_codec: audioCodec.name,
  };
  if (encodingAudit) renderedResult.encoding_audit = { ...encodingAudit, post_process_video_mode: "stream_copy" };
  return renderedResult;
}

function duckedSourceVolumeExpression(narrations: NarrationRenderInput[]): string {
  let expression = "1.00";
  for (const narration of [...narrations].reverse()) {
    if (!narration.duck_source_audio) continue;
    const start = secondsArg(narration.output_time_range_ms[0]);
    const end = secondsArg(narration.output_time_range_ms[1]);
    const volume = (narration.duck_source_to_percent / 100).toFixed(2);
    expression = `if(between(t,${start},${end}),min(${volume},${expression}),${expression})`;
  }
  return expression;
}

export function audioVolumeExpression(audioPolicy: NonNullable<DemoEditPlan["audio"]>, outputStartMS: number, durationMS: number, sourceTimestampOffsetMS = 0): { expression: string; usesSource: boolean } {
  const outputEndMS = outputStartMS + durationMS;
  const baseVolume = audioPolicy.mode === "mute" ? 0 : audioPolicy.volume_percent / 100;
  const overlapping = (audioPolicy.segment_settings ?? []).filter((segment) => segment.end_ms > outputStartMS && segment.start_ms < outputEndMS);
  let expression = baseVolume.toFixed(2);
  let usesSource = baseVolume > 0;
  for (const segment of [...overlapping].reverse()) {
    const startSec = (sourceTimestampOffsetMS + Math.max(0, segment.start_ms - outputStartMS)) / 1000;
    const endSec = (sourceTimestampOffsetMS + Math.min(durationMS, segment.end_ms - outputStartMS)) / 1000;
    const volume = segment.mode === "mute" ? 0 : segment.volume_percent / 100;
    if (volume > 0) usesSource = true;
    expression = `if(between(t,${startSec.toFixed(3)},${endSec.toFixed(3)}),${volume.toFixed(2)},${expression})`;
  }
  return { expression, usesSource };
}

async function preferredAudioCodec(ffmpegPath: string, extension: string): Promise<VideoCodecConfig> {
  if (extension.toLowerCase() === ".webm") {
    const encoderList = await runCommand(ffmpegPath, ["-hide_banner", "-encoders"]);
    const encoders = `${encoderList.stdout}\n${encoderList.stderr}`;
    if (encoders.includes("libopus")) return { name: "libopus", args: ["-b:a", "128k", "-ar", "48000", "-ac", "2"] };
    return { name: "libvorbis", args: ["-q:a", "4", "-ar", "48000", "-ac", "2"] };
  }
  return { name: "aac", args: ["-b:a", "128k", "-ar", "48000", "-ac", "2"] };
}

async function hasAudioStream(ffmpegPath: string, filePath: string): Promise<boolean> {
  const result = await runCommand(ffmpegPath, ["-hide_banner", "-i", filePath]);
  return /Stream\s+#\S+.*Audio:/i.test(`${result.stderr}\n${result.stdout}`);
}

async function preferredVideoCodec(ffmpegPath: string, extension: string, renderProfile?: RenderProfile): Promise<VideoCodecConfig> {
  const encoderList = await runCommand(ffmpegPath, ["-hide_banner", "-encoders"]);
  const encoders = `${encoderList.stdout}\n${encoderList.stderr}`;
  const normalizedExtension = extension.toLowerCase();
  if (normalizedExtension === ".mp4" || normalizedExtension === ".m4v" || normalizedExtension === ".mov") {
    if (encoders.includes("libx264")) {
      const preset = allowedPreset(renderProfile?.preset, renderProfile?.mode === "preview" ? "ultrafast" : "medium");
      const crf = boundedInteger(renderProfile?.crf, renderProfile?.mode === "preview" ? 28 : 18, 0, 51);
      return { name: "libx264", args: ["-preset", preset, "-crf", String(crf), "-g", "30", "-keyint_min", "30", "-pix_fmt", "yuv420p"] };
    }
    return { name: "mpeg4", args: ["-q:v", "4", "-g", "12"] };
  }
  if (encoders.includes("libvpx-vp9")) {
    return { name: "libvpx-vp9", args: ["-b:v", "1M", "-g", "12", "-keyint_min", "12", "-deadline", "realtime", "-cpu-used", "4"] };
  }
  return { name: "libvpx", args: ["-b:v", "1M", "-g", "12", "-keyint_min", "12", "-deadline", "realtime", "-cpu-used", "4"] };
}

function boundedInteger(value: number | undefined, fallback: number, minimum: number, maximum: number): number {
  if (!Number.isFinite(value)) return fallback;
  return Math.min(maximum, Math.max(minimum, Math.round(value as number)));
}

function allowedPreset(value: string | undefined, fallback: string): string {
  const allowed = new Set(["ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow"]);
  return value && allowed.has(value) ? value : fallback;
}

async function isCommandAvailable(command: string): Promise<boolean> {
  const result = await runCommand(command, ["-version"]);
  return result.code === 0;
}

function runCommand(command: string, args: string[]): Promise<{ code: number | null; stderr: string; stdout: string; error?: string }> {
  return new Promise((resolve) => {
    const child = spawn(command, args, { windowsHide: true });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => {
      stdout += String(chunk);
    });
    child.stderr.on("data", (chunk) => {
      stderr += String(chunk);
    });
    child.on("error", (error) => {
      resolve({ code: -1, stderr, stdout, error: error.message });
    });
    child.on("close", (code) => {
      resolve({ code, stderr, stdout });
    });
  });
}

function compactProcessError(prefix: string, result: { stderr: string; stdout: string; error?: string }): string {
  const message = result.error || result.stderr || result.stdout || "unknown error";
  return `${prefix}: ${message.replace(/\s+/g, " ").slice(0, 300)}`;
}

function secondsArg(valueMS: number): string {
  return (Math.max(0, valueMS) / 1000).toFixed(3);
}

function ffmpegConcatPath(filePath: string): string {
  return path.resolve(filePath).replace(/\\/g, "/").replace(/'/g, "'\\''");
}

function ffmpegFilterPath(filePath: string): string {
  return path.resolve(filePath).replace(/\\/g, "/").replace(/:/g, "\\:").replace(/'/g, "\\'");
}

function findDemoArtifactByID(catalog: AssetTimelineCatalog, id: string | undefined): TimelineArtifact | undefined {
  if (!id) return undefined;
  const artifact = catalog.artifacts.find((candidate) => candidate.id === id);
  return artifact && (isDemoMaterial(artifact) || isVerifiedTargetGeometryStillArtifact(artifact) || isApprovedGeneratedCandidateArtifact(artifact)) ? artifact : undefined;
}

function isDemoMaterial(artifact: TimelineArtifact): boolean {
  if (isGeneratedCandidateArtifact(artifact)) return false;
  if (artifact.include_in_demo === false) return false;
  if (artifact.sensitive) return false;
  if (artifact.kind === "browser_trace" || artifact.kind === "execution_trace" || artifact.kind === "artifact_manifest") return false;
  if (artifact.asset_role?.startsWith("debug")) return false;
  return true;
}

function isGeneratedCandidateArtifact(artifact: TimelineArtifact | undefined): boolean {
  if (!artifact) return false;
  const metadata = artifact.metadata || {};
  return (
    artifact.kind.startsWith("generated_") ||
    metadata.source_material_policy === "non_authoritative_generated_candidate" ||
    typeof metadata.provider_output_url === "string"
  );
}

function isApprovedGeneratedCandidateArtifact(artifact: TimelineArtifact | undefined): boolean {
  if (!artifact) return false;
  if (!isGeneratedCandidateArtifact(artifact)) return false;
  const metadata = artifact.metadata || {};
  return (
    artifact.kind === "generated_video_candidate" &&
    artifact.sensitive !== true &&
    metadata.approved_for_demo === true &&
    metadata.non_authoritative === true &&
    metadata.source_material_policy === "non_authoritative_generated_candidate" &&
    metadata.artifact_variant === "normalized" &&
    metadata.normalization_status === "ok" &&
    metadata.media_probe_status === "ok" &&
    metadata.normalization_profile === "editor_mp4_h264_yuv420p_1920x1080_cfr30_v1"
  );
}

function validateGeneratedCandidateShot(shot: DemoEditShot, artifact: TimelineArtifact, shotPath: string, errors: ValidationFinding[]): void {
  if (!isApprovedGeneratedCandidateArtifact(artifact)) {
    errors.push(
      finding(
        "generated_candidate_not_approved",
        "Generated candidate artifacts require approval, non-authoritative policy, and a probed normalized editor MP4/H.264/yuv420p/1920x1080/CFR30 derivative before use.",
        `${shotPath}.source_artifact_id`,
      ),
    );
    return;
  }
  if (shot.source_step_id) {
    errors.push(
      finding(
        "generated_candidate_cannot_represent_script_step",
        "Generated candidate artifacts are presentation-only and cannot represent a customer-side product interaction step.",
        `${shotPath}.source_step_id`,
      ),
    );
  }
  if (!shot.source_time_range_ms) {
    errors.push(
      finding(
        "generated_candidate_missing_time_range",
        "Generated video candidate shots must declare source_time_range_ms so the compositor uses an explicit approved segment.",
        `${shotPath}.source_time_range_ms`,
      ),
    );
  }
  if (!isVideoArtifact(artifact)) {
    errors.push(
      finding(
        "generated_candidate_not_video",
        "The current deterministic compositor can only render approved generated video candidates, not generated images.",
        `${shotPath}.source_artifact_id`,
      ),
    );
  }
}

function isVideoArtifact(artifact: TimelineArtifact): boolean {
  return artifact.kind === "raw_recording" || artifact.mime_type?.startsWith("video/") === true;
}

function isAudioArtifact(artifact: TimelineArtifact): boolean {
  return artifact.kind === "narration_audio" || artifact.asset_role === "narration_audio" || artifact.mime_type?.startsWith("audio/") === true;
}

function videoExtensionForArtifact(artifact: TimelineArtifact): ".mp4" | ".webm" | ".mov" {
  const uri = artifact.local_path || artifact.uri;
  if (artifact.mime_type === "video/mp4" || uri.toLowerCase().endsWith(".mp4")) return ".mp4";
  if (artifact.mime_type === "video/quicktime" || uri.toLowerCase().endsWith(".mov")) return ".mov";
  return ".webm";
}

function prohibitedKeyFindings(value: unknown, pathValue = ""): ValidationFinding[] {
  if (!value || typeof value !== "object") {
    return [];
  }
  const findings: ValidationFinding[] = [];
  for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
    const nextPath = pathValue ? `${pathValue}.${key}` : key;
    if (PROHIBITED_PLAN_KEYS.includes(key.toLowerCase() as (typeof PROHIBITED_PLAN_KEYS)[number])) {
      findings.push(finding("prohibited_generation_instruction", `Edit plan cannot contain generation field ${key}`, nextPath));
    }
    if (Array.isArray(child)) {
      child.forEach((item, index) => findings.push(...prohibitedKeyFindings(item, `${nextPath}[${index}]`)));
    } else if (child && typeof child === "object") {
      findings.push(...prohibitedKeyFindings(child, nextPath));
    }
  }
  return findings;
}

function artifactFromRecordingPath(recordingPath: string, index: number): ArtifactRef {
  return {
    id: index === 0 ? "artifact_raw_recording" : `artifact_raw_recording_${index + 1}`,
    kind: "raw_recording",
    uri: fileURI(recordingPath),
    mime_type: recordingPath.endsWith(".webm") ? "video/webm" : "application/octet-stream",
    label: "Raw browser recording",
  };
}

async function timelineArtifactFromRef(artifact: ArtifactRef, durationMS?: number): Promise<TimelineArtifact> {
  const filePath = filePathFromURI(artifact.uri);
  const fileStat = filePath ? await stat(filePath).catch(() => undefined) : undefined;
  const sha256 = artifact.sha256 || (filePath ? await sha256File(filePath).catch(() => undefined) : undefined);
  const result: TimelineArtifact = {
    id: artifact.id,
    kind: artifact.kind,
    uri: artifact.uri,
  };
  if (artifact.mime_type) result.mime_type = artifact.mime_type;
  if (artifact.label) result.label = artifact.label;
  if (sha256) result.sha256 = sha256;
  if (artifact.size_bytes !== undefined || fileStat?.size !== undefined) result.size_bytes = artifact.size_bytes || fileStat?.size || 0;
  if (artifact.sensitive !== undefined) result.sensitive = artifact.sensitive;
  if (artifact.source_node_id) result.source_step_id = artifact.source_node_id;
  if (artifact.metadata) {
    result.metadata = artifact.metadata;
    if (typeof artifact.metadata.asset_role === "string") result.asset_role = artifact.metadata.asset_role;
    if (typeof artifact.metadata.include_in_demo === "boolean") result.include_in_demo = artifact.metadata.include_in_demo;
    if (typeof artifact.metadata.capture_scope === "string") result.capture_scope = artifact.metadata.capture_scope;
    if (typeof artifact.metadata.duration_ms === "number") result.duration_ms = artifact.metadata.duration_ms;
  }
  if (durationMS !== undefined && result.duration_ms === undefined) result.duration_ms = durationMS;
  if (filePath) result.local_path = filePath;
  return result;
}

function sourceInfo(loaded: LoadedRenderInputs, trace: ExecutionTrace | undefined): AssetTimelineCatalog["source"] {
  const source: AssetTimelineCatalog["source"] = { generated_at: new Date().toISOString() };
  const resultPackageID = loaded.recordingResultPackage?.result_id || loaded.recordingResultPackage?.source_package_id;
  if (resultPackageID) {
    source.recording_result_package_id = resultPackageID;
  }
  if (trace?.id) source.execution_trace_id = trace.id;
  return source;
}

function timelineInfo(durationMS: number, recordingArtifact: ArtifactRef | undefined): AssetTimelineCatalog["timeline"] {
  const timeline: AssetTimelineCatalog["timeline"] = { duration_ms: durationMS };
  if (recordingArtifact) timeline.recording_artifact_id = recordingArtifact.id;
  return timeline;
}

function sourceNodeInfo(node: GraphNode | undefined): TimelineStep["source_node"] | undefined {
  if (!node) return undefined;
  const sourceNode: NonNullable<TimelineStep["source_node"]> = {};
  const selector = node.action_spec?.target?.selector || node.selector;
  if (selector) sourceNode.selector = selector;
  if (node.capture?.focus_selector) sourceNode.focus_selector = node.capture.focus_selector;
  if (node.capture?.asset_role) sourceNode.asset_role = node.capture.asset_role;
  return Object.keys(sourceNode).length > 0 ? sourceNode : undefined;
}

function firstRecordingArtifact(artifacts: ArtifactRef[]): ArtifactRef | undefined {
  return artifacts.find((artifact) => artifact.kind === "raw_recording") || artifacts.find((artifact) => artifact.mime_type?.startsWith("video/"));
}

function uniqueArtifacts(artifacts: ArtifactRef[]): ArtifactRef[] {
  const byID = new Map<string, ArtifactRef>();
  for (const artifact of artifacts) {
    if (artifact.id && !byID.has(artifact.id)) {
      byID.set(artifact.id, artifact);
    }
  }
  return [...byID.values()];
}

function actionForNode(node: GraphNode | undefined): string {
  return node?.action_spec?.type || node?.action || "inspect";
}

function durationHint(node: GraphNode | undefined): number {
  if (node?.duration_hint_ms && node.duration_hint_ms > 0) {
    return node.duration_hint_ms;
  }
  const action = actionForNode(node);
  if (action === "navigate") return 1800;
  if (action === "wait") return 1000;
  return 1200;
}

function maxStepEnd(steps: TimelineStep[]): number {
  return steps.reduce((max, step) => Math.max(max, step.end_ms), 0);
}

function durationFromDates(start?: string, end?: string): number {
  if (!start || !end) return 0;
  const startMS = Date.parse(start);
  const endMS = Date.parse(end);
  return Number.isFinite(startMS) && Number.isFinite(endMS) && endMS > startMS ? endMS - startMS : 0;
}

function positiveDuration(value?: number): number {
  return value && value > 0 ? value : 0;
}

function titleForAction(step: TimelineStep): string {
  if (step.action === "navigate") return "Open the product page";
  if (step.action === "click") return "Trigger the next product action";
  if (step.action === "fill") return "Enter demo data";
  if (step.action === "assert") return "Confirm the expected result";
  return `Show ${step.step_id}`;
}

function stepDocsFor(catalog: AssetTimelineCatalog, plan: DemoEditPlan): string {
  const lines = [
    "# Demo Edit Plan",
    "",
    `Catalog: ${catalog.catalog_id}`,
    "Policy: existing source assets only",
    "Authority: customer-side agent owns what is demonstrated; cloud AIGC only optimizes presentation",
    "",
    "## Shots",
    "",
  ];
  for (const shot of plan.shots) {
    const range = shot.source_time_range_ms ? ` ${shot.source_time_range_ms[0]}-${shot.source_time_range_ms[1]}ms` : "";
    lines.push(`- ${shot.id}: ${shot.purpose} (${shot.source_artifact_id}${range})`);
  }
  lines.push("");
  return `${lines.join("\n")}\n`;
}

function finding(code: string, message: string, pathValue?: string): ValidationFinding {
  const result: ValidationFinding = { code, message };
  if (pathValue) result.path = pathValue;
  return result;
}

function missingValues<T extends string>(required: readonly T[], values: string[]): string[] {
  const present = new Set(values);
  return required.filter((value) => !present.has(value));
}

function unsupportedValues(values: string[], allowed: readonly string[]): string[] {
  const allowlist = new Set(allowed);
  return values.filter((value) => !allowlist.has(value));
}

function overlappingValues(left: string[], right: string[]): string[] {
  const rightValues = new Set(right);
  return left.filter((value) => rightValues.has(value));
}

async function readJSON<T>(filePath: string): Promise<T> {
  return JSON.parse(await readFile(filePath, "utf8")) as T;
}

async function writeJSON(filePath: string, value: unknown): Promise<void> {
  await writeFile(filePath, `${JSON.stringify(value, null, 2)}\n`, "utf8");
}

async function sha256File(filePath: string): Promise<string> {
  const data = await readFile(filePath);
  return createHash("sha256").update(data).digest("hex");
}

function fileURI(filePath: string): string {
  return pathToFileURL(path.resolve(filePath)).toString();
}

function filePathFromURI(uri: string): string | undefined {
  if (!uri.startsWith("file://")) {
    if (uri.startsWith("http://") || uri.startsWith("https://") || uri.startsWith("asset://")) return undefined;
    if (!uri.trim()) return undefined;
    return path.isAbsolute(uri) ? uri : path.resolve(uri);
  }
  return fileURLToPath(uri);
}

function safeName(value: string): string {
  return value.replace(/[^a-zA-Z0-9_-]+/g, "_").replace(/^_+|_+$/g, "") || "item";
}
