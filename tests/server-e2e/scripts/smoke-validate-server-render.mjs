import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const root = await mkdtemp(path.join(tmpdir(), "cascade-validate-server-render-"));
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..", "..");
const validator = path.join(repoRoot, "tests", "server-e2e", "scripts", "validate-server-render.mjs");

try {
  const passingDir = path.join(root, "passing", "render");
  await mkdir(passingDir, { recursive: true });
  await writeFixture(passingDir, {
    videoPath: path.join(passingDir, "demo_12s.mp4"),
    method: "ffmpeg_trim_concat",
    outputContainer: "mp4",
    qualityStatus: "ok",
    plannedOperations: ["caption", "trim"],
    appliedOperations: ["caption", "trim"],
    skippedOperations: [],
    requirementStatus: "satisfied_with_warnings",
    requirementErrors: [],
    mediaStatus: "ok",
    applyStatus: "applied_and_rerendered",
    pendingOperations: [],
  });

  const passing = runValidator(passingDir);
  assert.equal(passing.status, 0, passing.stderr || passing.stdout);

  const fallbackDir = path.join(root, "fallback", "render");
  await mkdir(fallbackDir, { recursive: true });
  await writeFixture(fallbackDir, {
    videoPath: path.join(fallbackDir, "demo_12s.webm"),
    method: "copy_source_recording",
    outputContainer: "webm",
    qualityStatus: "degraded",
    fallbackReason: "ffmpeg_not_available",
    plannedOperations: ["caption", "trim"],
    appliedOperations: ["fallback_copy"],
    skippedOperations: [
      { type: "caption", reason: "not burned" },
      { type: "trim", reason: "not executed" },
    ],
    requirementStatus: "not_satisfied",
    requirementErrors: [{ code: "requested_format_not_satisfied" }],
    mediaStatus: "failed",
    mediaError: "ffmpeg_not_available",
    applyStatus: "applied_and_rerendered_with_pending_operations",
    pendingOperations: ["caption", "trim"],
  });

  const failing = runValidator(fallbackDir);
  assert.notEqual(failing.status, 0, "fallback fixture should fail strict server render validation");
  assert.match(failing.stdout, /fallback_copy|ffmpeg_not_available|requested_format_not_satisfied/);
} finally {
  await rm(root, { recursive: true, force: true });
}

async function writeFixture(renderDir, fixture) {
  await writeFile(fixture.videoPath, "fixture video bytes");
  await writeJSON(path.join(renderDir, "render_manifest.json"), {
    schema_version: "demoops.render_manifest.v1",
    status: "rendered",
    video_path: fixture.videoPath,
    source_reference_video_path: fixture.videoPath,
    compositor: {
      status: "rendered",
      video_path: fixture.videoPath,
      method: fixture.method,
      output_container: fixture.outputContainer,
      quality_status: fixture.qualityStatus,
      ffmpeg_available: fixture.method !== "copy_source_recording",
      fallback_reason: fixture.fallbackReason,
      planned_operations: fixture.plannedOperations,
      applied_operations: fixture.appliedOperations,
      skipped_operations: fixture.skippedOperations,
    },
  });
  await writeJSON(path.join(renderDir, "requirement_satisfaction_report.json"), {
    schema_version: "demoops.requirement_satisfaction_report.v1",
    status: fixture.requirementStatus,
    warnings: [],
    errors: fixture.requirementErrors,
  });
  await writeJSON(path.join(renderDir, "media_normalization_report.json"), {
    schema_version: "demoops.media_normalization_report.v1",
    status: fixture.mediaStatus,
    error: fixture.mediaError,
    reference_video_path: fixture.mediaStatus === "ok" ? fixture.videoPath : undefined,
  });
  await writeJSON(path.join(renderDir, "director_edit_plan_patch_apply_result.json"), {
    schema_version: "demoops.director_edit_plan_patch_apply_result.v1",
    status: fixture.applyStatus,
    rerendered: true,
    render_verification: {
      status: fixture.pendingOperations.length > 0 ? "satisfied_with_pending_operations" : "satisfied",
      pending_operation_types: fixture.pendingOperations,
    },
  });
}

async function writeJSON(filePath, data) {
  await writeFile(filePath, `${JSON.stringify(data, null, 2)}\n`);
}

function runValidator(renderDir) {
  return spawnSync(process.execPath, [validator, renderDir, "--skip-ffprobe"], {
    cwd: repoRoot,
    encoding: "utf8",
  });
}
