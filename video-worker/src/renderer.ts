import { createHash } from "node:crypto";
import { mkdir, readFile, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const ASSET_TIMELINE_CATALOG_SCHEMA_VERSION = "demoops.asset_timeline_catalog.v1";
const DEMO_EDIT_PLAN_SCHEMA_VERSION = "demoops.demo_edit_plan.v1";
const DEMO_EDIT_PLAN_VALIDATION_SCHEMA_VERSION = "demoops.demo_edit_plan_validation.v1";
const DEMO_EDIT_SOURCE_AUTHORITY = "customer_side_agent";
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

export interface RenderRequest {
  graph?: DemoWorkflowGraph;
  recording_paths?: string[];
  output_dir?: string;
  duration_sec?: number;
  execution_trace?: ExecutionTrace;
  execution_trace_path?: string;
  generated_assets?: ArtifactRef[];
  artifact_manifest_path?: string;
  recording_result_package?: RecordingResultPackage;
  recording_result_package_path?: string;
  edit_plan?: DemoEditPlan;
}

export interface RenderResult {
  video_path: string;
  step_by_step_docs_path: string;
  asset_timeline_catalog_path: string;
  demo_edit_plan_path: string;
  validation_report_path: string;
  render_manifest_path: string;
  asset_timeline_catalog: AssetTimelineCatalog;
  demo_edit_plan: DemoEditPlan;
  validation_report: DemoEditPlanValidationReport;
}

interface DemoWorkflowGraph {
  id: string;
  version?: number;
  nodes?: GraphNode[];
}

interface GraphNode {
  id: string;
  action?: string;
  selector?: string;
  expected_outcome?: string;
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
    focus_selector?: string;
    asset_role?: string;
  };
  duration_hint_ms?: number;
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

interface AssetTimelineCatalog {
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
  duration_ms?: number;
  local_path?: string;
}

interface DemoEditPlan {
  schema_version?: string;
  plan_id: string;
  catalog_id?: string;
  objective?: string;
  source_authority: typeof DEMO_EDIT_SOURCE_AUTHORITY;
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
}

interface DemoEditShot {
  id: string;
  source_artifact_id: string;
  source_step_id?: string;
  source_time_range_ms?: [number, number];
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
  text?: string;
  source_step_id?: string;
  target_selector?: string;
  start_ms?: number;
  end_ms?: number;
}

interface DemoEditPlanValidationReport {
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

export async function render(request: RenderRequest): Promise<RenderResult> {
  const outputDir = request.output_dir || "artifacts/rendered";
  await mkdir(outputDir, { recursive: true });

  const loaded = await loadRenderInputs(request);
  const catalog = await buildAssetTimelineCatalog(request, loaded);
  const editPlan = request.edit_plan ? normalizeEditPlan(request.edit_plan, catalog) : defaultEditPlan(catalog, request.duration_sec);
  const validationReport = validateDemoEditPlan(editPlan, catalog);

  if (!validationReport.valid) {
    throw new Error(`demo edit plan rejected: ${validationReport.errors.map((error) => `${error.code}: ${error.message}`).join("; ")}`);
  }

  const targetDurationSec = Math.max(1, Math.round((editPlan.target_duration_ms || (request.duration_sec || 60) * 1000) / 1000));
  const videoPath = path.join(outputDir, `demo_${targetDurationSec}s.mp4`);
  const stepDocsPath = path.join(outputDir, "step_by_step.md");
  const catalogPath = path.join(outputDir, "asset_timeline_catalog.json");
  const editPlanPath = path.join(outputDir, "demo_edit_plan.json");
  const validationReportPath = path.join(outputDir, "demo_edit_plan_validation.json");
  const renderManifestPath = path.join(outputDir, "render_manifest.json");

  await writeJSON(catalogPath, catalog);
  await writeJSON(editPlanPath, editPlan);
  await writeJSON(validationReportPath, validationReport);
  await writeJSON(renderManifestPath, {
    schema_version: "demoops.render_manifest.v1",
    status: "planned",
    source_material_policy: "existing_assets_only",
    source_authority: DEMO_EDIT_SOURCE_AUTHORITY,
    model_role: DEMO_EDIT_MODEL_ROLE,
    note: "This stage creates a validated source-only edit plan. A deterministic compositor must render the final video from referenced artifacts.",
    video_path: videoPath,
    step_by_step_docs_path: stepDocsPath,
    asset_timeline_catalog_path: catalogPath,
    demo_edit_plan_path: editPlanPath,
    validation_report_path: validationReportPath,
  });
  await writeFile(stepDocsPath, stepDocsFor(catalog, editPlan), "utf8");

  return {
    video_path: videoPath,
    step_by_step_docs_path: stepDocsPath,
    asset_timeline_catalog_path: catalogPath,
    demo_edit_plan_path: editPlanPath,
    validation_report_path: validationReportPath,
    render_manifest_path: renderManifestPath,
    asset_timeline_catalog: catalog,
    demo_edit_plan: editPlan,
    validation_report: validationReport,
  };
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

function defaultEditPlan(catalog: AssetTimelineCatalog, durationSec?: number): DemoEditPlan {
  const recordingArtifactID = catalog.timeline.recording_artifact_id || catalog.artifacts.find((artifact) => artifact.kind === "raw_recording")?.id;
  const fallbackArtifactID = recordingArtifactID || catalog.artifacts[0]?.id || "missing_source_artifact";
  const eligibleSteps = catalog.steps.filter((step) => step.status !== "failed");
  const steps = eligibleSteps.length > 0 ? eligibleSteps : catalog.steps;
  const shots = steps.map((step, index) => defaultShotForStep(step, index, recordingArtifactID || step.artifacts[0] || fallbackArtifactID));

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
    target_duration_ms: (durationSec || Math.max(1, Math.ceil(catalog.timeline.duration_ms / 1000))) * 1000,
    shots,
    global_style: {
      color_grade: "neutral_product_ui",
      pacing: "clear_and_direct",
      transition_style: "simple_cut",
    },
  };
}

function defaultShotForStep(step: TimelineStep, index: number, sourceArtifactID: string): DemoEditShot {
  const shot: DemoEditShot = {
    id: `shot_${String(index + 1).padStart(3, "0")}_${safeName(step.step_id)}`,
    source_artifact_id: sourceArtifactID,
    source_step_id: step.step_id,
    source_time_range_ms: [step.start_ms, step.end_ms],
    purpose: step.expected_outcome || step.observed_state || `Show ${step.action} step ${step.step_id}`,
    operations: defaultOperationsForStep(step),
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
  return shot;
}

function defaultOperationsForStep(step: TimelineStep): EditOperation[] {
  const operations: EditOperation[] = [{ type: "trim", start_ms: step.start_ms, end_ms: step.end_ms }];
  const focusSelector = step.source_node?.focus_selector || step.source_node?.selector;
  if (step.action === "click" || step.action === "fill" || focusSelector) {
    const zoomPan: EditOperation = {
      type: "zoom_pan",
      zoom: 1.18,
      start_ms: step.start_ms,
      end_ms: step.end_ms,
    };
    if (focusSelector) zoomPan.focus_selector = focusSelector;
    operations.push(zoomPan);
    operations.push({ type: "cursor_highlight", start_ms: step.start_ms, end_ms: Math.min(step.end_ms, step.start_ms + 1200) });
  }
  return operations;
}

function normalizeEditPlan(plan: DemoEditPlan, catalog: AssetTimelineCatalog): DemoEditPlan {
  return {
    ...plan,
    schema_version: plan.schema_version || DEMO_EDIT_PLAN_SCHEMA_VERSION,
    catalog_id: plan.catalog_id || catalog.catalog_id,
    source_authority: plan.source_authority || DEMO_EDIT_SOURCE_AUTHORITY,
    model_role: plan.model_role || DEMO_EDIT_MODEL_ROLE,
    source_material_policy: plan.source_material_policy || "existing_assets_only",
    script_order_policy: plan.script_order_policy || "preserve_required_step_order",
    locked_fields: plan.locked_fields || [...REQUIRED_LOCKED_FIELDS],
    model_editable_fields: plan.model_editable_fields || [...ALLOWED_MODEL_EDITABLE_FIELDS],
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
  if (plan.source_authority !== DEMO_EDIT_SOURCE_AUTHORITY) {
    errors.push(finding("invalid_source_authority", "source_authority must be customer_side_agent", "source_authority"));
  }
  if (plan.model_role !== DEMO_EDIT_MODEL_ROLE) {
    errors.push(finding("invalid_model_role", "model_role must be presentation_optimizer_only", "model_role"));
  }
  validateCollaborationBoundary(plan, errors);
  if (!Array.isArray(plan.shots) || plan.shots.length === 0) {
    errors.push(finding("missing_shots", "DemoEditPlan must contain at least one shot", "shots"));
  }
  errors.push(...prohibitedKeyFindings(plan));

  let lastRequiredStepOrder = -1;
  plan.shots.forEach((shot, shotIndex) => {
    const shotPath = `shots[${shotIndex}]`;
    const artifact = artifactByID.get(shot.source_artifact_id);
    if (!artifact) {
      errors.push(finding("unknown_source_artifact", `Shot references missing source artifact ${shot.source_artifact_id}`, `${shotPath}.source_artifact_id`));
    } else if (artifact.sensitive) {
      errors.push(finding("sensitive_source_artifact", `Shot references sensitive artifact ${shot.source_artifact_id}`, `${shotPath}.source_artifact_id`));
    }

    if (shot.source_step_id && !stepByID.has(shot.source_step_id)) {
      errors.push(finding("unknown_source_step", `Shot references missing source step ${shot.source_step_id}`, `${shotPath}.source_step_id`));
    }
    if (shot.source_step_id && requiredOrder.has(shot.source_step_id)) {
      const currentOrder = requiredOrder.get(shot.source_step_id) as number;
      if (currentOrder < lastRequiredStepOrder) {
        errors.push(finding("required_step_order_changed", "Required script steps cannot be reordered by the edit plan", `${shotPath}.source_step_id`));
      }
      lastRequiredStepOrder = Math.max(lastRequiredStepOrder, currentOrder);
    }

    validateShotTimeRange(shot, artifact, catalog, `${shotPath}.source_time_range_ms`, errors);
    validateOperations(shot.operations || [], `${shotPath}.operations`, errors);
    validateOverlays(shot.overlays || [], stepByID, `${shotPath}.overlays`, errors);
  });

  const representedSteps = new Set(plan.shots.map((shot) => shot.source_step_id).filter((stepID): stepID is string => Boolean(stepID)));
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

function validateOverlays(overlays: EditOverlay[], stepByID: Map<string, TimelineStep>, pathValue: string, errors: ValidationFinding[]): void {
  overlays.forEach((overlay, index) => {
    if (!ALLOWED_OVERLAY_TYPES.includes(overlay.type)) {
      errors.push(finding("unsupported_overlay", `Unsupported overlay type ${overlay.type}`, `${pathValue}[${index}].type`));
    }
    if (overlay.source_step_id && !stepByID.has(overlay.source_step_id)) {
      errors.push(finding("unknown_overlay_step", `Overlay references missing source step ${overlay.source_step_id}`, `${pathValue}[${index}].source_step_id`));
    }
  });
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
  if (durationMS !== undefined) result.duration_ms = durationMS;
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
    return undefined;
  }
  return fileURLToPath(uri);
}

function safeName(value: string): string {
  return value.replace(/[^a-zA-Z0-9_-]+/g, "_").replace(/^_+|_+$/g, "") || "item";
}
