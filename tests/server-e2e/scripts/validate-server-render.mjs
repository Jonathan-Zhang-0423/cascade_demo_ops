import { existsSync } from "node:fs";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const args = process.argv.slice(2);
const renderDir = optionValue("--render-dir") || firstPositionalArg();
const expectedFormat = optionValue("--expect-format") || "mp4";
const expectedVideoCodec = optionValue("--expect-video-codec") || "h264";
const expectedAudioCodec = optionValue("--expect-audio-codec") || "aac";
const expectedResolution = parseResolution(optionValue("--expect-resolution") || "1920x1080");
const expectedFPS = Number(optionValue("--expect-fps") || "30");
const ffprobePath = optionValue("--ffprobe") || process.env.CASCADE_FFPROBE_PATH || "ffprobe";
const skipFFProbe = args.includes("--skip-ffprobe");
const requiredOperations = optionValues("--require-operation");
if (requiredOperations.length === 0) {
  requiredOperations.push("caption", "trim");
}

if (args.includes("--help")) {
  printHelp();
  process.exit(0);
}

if (!renderDir) {
  printHelp();
  process.exit(1);
}

const checks = [];
const failures = [];

const manifestPath = path.join(renderDir, "render_manifest.json");
const requirementPath = path.join(renderDir, "requirement_satisfaction_report.json");
const mediaPath = path.join(renderDir, "media_normalization_report.json");
const applyPath = path.join(renderDir, "director_edit_plan_patch_apply_result.json");

const manifest = await readRequiredJSON(manifestPath, "render_manifest");
const requirement = await readRequiredJSON(requirementPath, "requirement_satisfaction_report");
const media = await readRequiredJSON(mediaPath, "media_normalization_report");
const applyResult = existsSync(applyPath) ? await readJSON(applyPath) : undefined;

const finalVideoPath = resolveArtifactPath(renderDir, manifest.video_path);
check(Boolean(finalVideoPath), "final video path is present", `render_manifest.video_path is missing`);
check(finalVideoPath && existsSync(finalVideoPath), "final video file exists", `final video file does not exist: ${finalVideoPath || "(missing)"}`);
check(path.extname(finalVideoPath || "").replace(".", "").toLowerCase() === expectedFormat, `final video extension is .${expectedFormat}`, `final video extension is ${path.extname(finalVideoPath || "") || "(none)"}`);

const compositor = manifest.compositor || {};
check(compositor.status !== "planned", "compositor produced a rendered output", "compositor status is planned");
check(compositor.method !== "copy_source_recording", "compositor did not use fallback copy", `compositor method is ${compositor.method || "(missing)"}`);
check(compositor.quality_status === "ok", "compositor quality is ok", `compositor quality_status is ${compositor.quality_status || "(missing)"}`);
check(compositor.output_container === expectedFormat, `compositor output container is ${expectedFormat}`, `compositor output_container is ${compositor.output_container || "(missing)"}`);
check(!compositor.fallback_reason, "compositor has no fallback reason", `compositor fallback_reason is ${compositor.fallback_reason}`);
check(!includes(compositor.applied_operations, "fallback_copy"), "fallback_copy is not an applied operation", "fallback_copy is present in applied_operations");

for (const operation of requiredOperations) {
  if (includes(compositor.planned_operations, operation)) {
    check(includes(compositor.applied_operations, operation), `${operation} operation was applied`, `${operation} was planned but not applied`);
  }
  const skippedOperation = (compositor.skipped_operations || []).find((item) => item?.type === operation);
  check(!skippedOperation, `${operation} operation was not skipped`, `${operation} was skipped: ${skippedOperation?.reason || "no reason"}`);
}

check(requirement.status !== "not_satisfied", "requirement report is not blocking", `requirement_satisfaction_report.status is ${requirement.status}`);
check((requirement.errors || []).length === 0, "requirement report has no errors", `requirement report errors: ${findingCodes(requirement.errors)}`);

check(media.status === "ok", "media normalization is ok", `media_normalization_report.status is ${media.status}${media.error ? `: ${media.error}` : ""}`);
check(media.reference_video_path ? existsSync(resolveArtifactPath(renderDir, media.reference_video_path)) : true, "media reference path exists when present", `media reference path does not exist: ${media.reference_video_path}`);

if (applyResult) {
  check(applyResult.rerendered === true, "director patch was rerendered", `director patch rerendered is ${applyResult.rerendered}`);
  check(!String(applyResult.status || "").includes("pending_operations"), "director patch has no pending render operations", `director patch status is ${applyResult.status}`);
  const pending = applyResult.render_verification?.pending_operation_types || [];
  check(pending.length === 0, "director patch render verification has no pending operations", `pending operations: ${pending.join(", ")}`);
}

if (!skipFFProbe && finalVideoPath) {
  const probe = probeMedia(ffprobePath, finalVideoPath);
  const videoStream = probe.streams.find((stream) => stream.codec_type === "video");
  const audioStream = probe.streams.find((stream) => stream.codec_type === "audio");
  check(Boolean(videoStream), "ffprobe found a video stream", "ffprobe did not find a video stream");
  check(Boolean(audioStream), "ffprobe found an audio stream", "ffprobe did not find an audio stream");
  if (videoStream) {
    check(videoStream.codec_name === expectedVideoCodec, `video codec is ${expectedVideoCodec}`, `video codec is ${videoStream.codec_name || "(missing)"}`);
    check(videoStream.width === expectedResolution.width && videoStream.height === expectedResolution.height, `video resolution is ${expectedResolution.width}x${expectedResolution.height}`, `video resolution is ${videoStream.width || "?"}x${videoStream.height || "?"}`);
    const fps = parseFPS(videoStream.avg_frame_rate || videoStream.r_frame_rate);
    check(Number.isFinite(fps) && Math.abs(fps - expectedFPS) <= 0.25, `video fps is about ${expectedFPS}`, `video fps is ${Number.isFinite(fps) ? fps.toFixed(3) : "(unknown)"}`);
  }
  if (audioStream) {
    check(audioStream.codec_name === expectedAudioCodec, `audio codec is ${expectedAudioCodec}`, `audio codec is ${audioStream.codec_name || "(missing)"}`);
  }
}

const summary = {
  ok: failures.length === 0,
  render_dir: path.resolve(renderDir),
  final_video_path: finalVideoPath,
  compositor: {
    method: compositor.method,
    output_container: compositor.output_container,
    planned_operations: compositor.planned_operations || [],
    applied_operations: compositor.applied_operations || [],
  },
  requirement_status: requirement.status,
  media_normalization_status: media.status,
  checks,
  failures,
};

console.log(JSON.stringify(summary, null, 2));
if (failures.length > 0) {
  process.exit(1);
}

async function readRequiredJSON(filePath, label) {
  if (!existsSync(filePath)) {
    failures.push(`${label} missing at ${filePath}`);
    return {};
  }
  return readJSON(filePath);
}

async function readJSON(filePath) {
  return JSON.parse(await readFile(filePath, "utf8"));
}

function check(condition, passMessage, failMessage) {
  checks.push({ status: condition ? "pass" : "fail", message: condition ? passMessage : failMessage });
  if (!condition) failures.push(failMessage);
}

function probeMedia(command, filePath) {
  const result = spawnSync(command, [
    "-v",
    "error",
    "-show_entries",
    "format=format_name,duration:stream=codec_type,codec_name,width,height,avg_frame_rate,r_frame_rate,pix_fmt",
    "-of",
    "json",
    filePath,
  ], { encoding: "utf8" });
  if (result.error || result.status !== 0) {
    const message = result.error?.message || result.stderr || result.stdout || `exit ${result.status}`;
    throw new Error(`ffprobe failed: ${message}`);
  }
  return JSON.parse(result.stdout || "{}");
}

function resolveArtifactPath(baseDir, value) {
  if (!value) return "";
  if (value.startsWith("file://")) return fileURLToPath(value);
  if (path.isAbsolute(value)) return value;
  return path.resolve(baseDir, value);
}

function optionValue(name) {
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === name) return args[index + 1] || "";
    if (arg.startsWith(`${name}=`)) return arg.slice(name.length + 1);
  }
  return "";
}

function optionValues(name) {
  const values = [];
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === name && args[index + 1]) values.push(args[index + 1]);
    if (arg.startsWith(`${name}=`)) values.push(arg.slice(name.length + 1));
  }
  return values.flatMap((value) => value.split(",")).map((value) => value.trim()).filter(Boolean);
}

function firstPositionalArg() {
  return args.find((arg) => !arg.startsWith("-")) || "";
}

function parseResolution(value) {
  const match = String(value).match(/^(\d+)x(\d+)$/);
  if (!match) throw new Error(`Invalid --expect-resolution value: ${value}`);
  return { width: Number(match[1]), height: Number(match[2]) };
}

function parseFPS(value) {
  if (!value) return Number.NaN;
  const [left, right] = String(value).split("/").map(Number);
  if (!right) return left;
  return left / right;
}

function includes(values, target) {
  return Array.isArray(values) && values.includes(target);
}

function findingCodes(findings) {
  return (findings || []).map((finding) => finding.code || finding.message || "unknown").join(", ");
}

function printHelp() {
  console.log(`Usage:
  node tests/server-e2e/scripts/validate-server-render.mjs <render-dir>
  node tests/server-e2e/scripts/validate-server-render.mjs --render-dir .cascade-dev/artifacts/exchange/xpkg_.../render

Checks a completed server replay render directory for production-ready media:
  - final video exists and matches expected container
  - compositor did not fallback-copy the raw recording
  - caption/trim operations are applied when planned
  - requirement report has no blocking errors
  - media normalization succeeded
  - ffprobe confirms codec, audio, resolution, and fps

Options:
  --expect-format mp4
  --expect-video-codec h264
  --expect-audio-codec aac
  --expect-resolution 1920x1080
  --expect-fps 30
  --require-operation caption --require-operation trim
  --ffprobe /path/to/ffprobe
  --skip-ffprobe  JSON-only validation, for local fixture tests only
`);
}
