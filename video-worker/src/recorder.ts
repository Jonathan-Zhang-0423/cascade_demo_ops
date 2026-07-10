import { createHash } from "node:crypto";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { recordWithPlaywright } from "./browser-recorder.js";
import { executeScript } from "./script-runner.js";

export interface RecordRequest {
  graph: unknown;
  output_dir: string;
  viewport?: { width: number; height: number };
  headless?: boolean;
  source_package_id?: string;
  recording_run_spec?: unknown;
  executable_script_bundle?: unknown;
  recording_mode?: "dry_run" | "playwright";
}

export interface RecordResult {
  recording_path?: string;
  screenshot_paths?: string[];
  trace_path?: string;
  artifact_manifest_path?: string;
  generated_assets?: ArtifactRef[];
  step_results?: Array<{ node_id: string; status: string; duration_ms?: number }>;
  worker_id?: string;
  runtime_versions?: Record<string, string>;
  started_at?: string;
  completed_at?: string;
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
  source_node_id?: string;
  metadata?: Record<string, unknown>;
}

export async function record(request: RecordRequest): Promise<RecordResult> {
  const outputDir = request.output_dir || "artifacts/rehearsal";
  await mkdir(outputDir, { recursive: true });
  if (request.executable_script_bundle) {
    const startedAt = new Date().toISOString();
    const execution = await executeScript({ bundle: request.executable_script_bundle as never, output_dir: outputDir });
    if (!execution.ok) {
      throw new Error(execution.error || "script execution failed");
    }
    if (shouldUsePlaywright(request)) {
      const browserResult = await recordWithPlaywright(request as never);
      const completedAt = new Date().toISOString();
      return withRecordingMetadata(outputDir, {
        ...browserResult,
        worker_id: "video-worker-local",
        started_at: startedAt,
        completed_at: completedAt,
      });
    }

    const completedAt = new Date().toISOString();
    const tracePath = path.join(outputDir, "script_execution_trace.json");
    await writeJSON(tracePath, {
      schema_version: "demoops.script_execution_trace.v1",
      mode: "dry_run",
      started_at: startedAt,
      completed_at: completedAt,
      step_results: execution.step_results || [],
      note: "Dry-run validates and orders the executable script bundle. It does not create product screenshots or video.",
    });
    const traceAsset = await artifactRef("artifact_script_execution_trace", "execution_trace", tracePath, "application/json", {
      asset_role: "debug_trace",
      include_in_demo: false,
      execution_mode: "dry_run",
    });
    const dryRunResult: RecordResult = {
      trace_path: tracePath,
      generated_assets: [traceAsset],
      worker_id: "video-worker-local",
      runtime_versions: { runner: "playwright-restricted-sandbox-dry-run" },
      started_at: startedAt,
      completed_at: completedAt,
    };
    if (execution.step_results) {
      dryRunResult.step_results = execution.step_results;
    }
    return withRecordingMetadata(outputDir, dryRunResult);
  }

  const tracePath = path.join(outputDir, "recording_plan_trace.json");
  await writeJSON(tracePath, {
    schema_version: "demoops.recording_plan_trace.v1",
    mode: "dry_run",
    graph: request.graph,
    note: "Dry-run without executable script bundle. No product screenshots or video were created.",
  });
  const traceAsset = await artifactRef("artifact_recording_plan_trace", "execution_trace", tracePath, "application/json", {
    asset_role: "debug_trace",
    include_in_demo: false,
    execution_mode: "dry_run",
  });
  return withRecordingMetadata(outputDir, {
    trace_path: tracePath,
    generated_assets: [traceAsset],
    worker_id: "video-worker-local",
    runtime_versions: { runner: "recording-dry-run" },
  });
}

function shouldUsePlaywright(request: RecordRequest): boolean {
  return request.recording_mode === "playwright" || process.env.CASCADE_RECORDING_MODE === "playwright";
}

async function withRecordingMetadata(outputDir: string, result: RecordResult): Promise<RecordResult> {
  const assets = result.generated_assets || [];
  const manifestPath = path.join(outputDir, "recording_artifact_manifest.json");
  await writeJSON(manifestPath, {
    schema_version: "demoops.recording_artifact_manifest.v1",
    generated_at: new Date().toISOString(),
    artifacts: assets,
  });
  return { ...result, generated_assets: assets, artifact_manifest_path: manifestPath };
}

async function artifactRef(id: string, kind: string, filePath: string, mimeType: string, metadata?: Record<string, unknown>): Promise<ArtifactRef> {
  const data = await readFile(filePath);
  const ref: ArtifactRef = {
    id,
    kind,
    uri: pathToFileURL(path.resolve(filePath)).toString(),
    mime_type: mimeType,
    sha256: createHash("sha256").update(data).digest("hex"),
    size_bytes: data.length,
    created_at: new Date().toISOString(),
  };
  if (metadata && Object.keys(metadata).length > 0) {
    ref.metadata = metadata;
  }
  return ref;
}

async function writeJSON(filePath: string, value: unknown): Promise<void> {
  await writeFile(filePath, `${JSON.stringify(value, null, 2)}\n`, "utf8");
}
