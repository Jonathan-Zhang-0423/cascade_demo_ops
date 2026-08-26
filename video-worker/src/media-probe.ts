import { createHash } from "node:crypto";
import { spawn } from "node:child_process";
import { createReadStream } from "node:fs";
import { mkdir, stat } from "node:fs/promises";
import path from "node:path";

export interface MediaProbeRequest {
  path: string;
  contact_sheet_path?: string;
  contact_sheet_frames?: number;
  analyze_temporal_quality?: boolean;
}

export interface MediaProbeResult {
  path: string;
  file_name: string;
  size_bytes: number;
  sha256: string;
  mime_type: string;
  format?: string;
  duration_ms?: number;
  video_codec?: string;
  audio_codec?: string;
  width?: number;
  height?: number;
  fps?: number;
  pixel_format?: string;
  ffprobe_available: boolean;
  quality_analysis_available?: boolean;
  black_duration_ms?: number;
  freeze_duration_ms?: number;
  silence_duration_ms?: number;
  verified_silence?: boolean;
  integrated_lufs?: number;
  true_peak_db?: number;
  temporal_analysis_available?: boolean;
  full_screen_flash_count?: number;
  jitter_measurement_available?: boolean;
  jitter_score?: number;
  contact_sheet_path?: string;
}

const SUPPORTED_EXTENSIONS = new Map([
  [".mp4", "video/mp4"],
  [".webm", "video/webm"],
  [".mov", "video/quicktime"],
  [".m4v", "video/x-m4v"],
  [".wav", "audio/wav"],
  [".mp3", "audio/mpeg"],
  [".m4a", "audio/mp4"],
  [".aac", "audio/aac"],
  [".ogg", "audio/ogg"],
  [".flac", "audio/flac"],
]);

export async function probeMediaFile(request: MediaProbeRequest): Promise<MediaProbeResult> {
  const inputPath = path.resolve(request.path || "");
  const extension = path.extname(inputPath).toLowerCase();
  const mimeType = SUPPORTED_EXTENSIONS.get(extension);
  if (!mimeType) {
    throw new Error(`unsupported media extension: ${extension || "none"}`);
  }
  const info = await stat(inputPath);
  if (!info.isFile()) {
    throw new Error("media path is not a file");
  }

  const result: MediaProbeResult = {
    path: inputPath,
    file_name: path.basename(inputPath),
    size_bytes: info.size,
    sha256: await sha256File(inputPath),
    mime_type: mimeType,
    ffprobe_available: false,
  };
  const ffprobePath = process.env.CASCADE_FFPROBE_PATH || ffprobePathFor(process.env.CASCADE_FFMPEG_PATH || "ffmpeg");
  const availability = await runCommand(ffprobePath, ["-version"]);
  if (availability.code !== 0) {
    const ffmpegPath = process.env.CASCADE_FFMPEG_PATH || "ffmpeg";
    const ffmpegAvailability = await runCommand(ffmpegPath, ["-version"]);
    if (ffmpegAvailability.code === 0) {
      const fallback = await runCommand(ffmpegPath, ["-hide_banner", "-i", inputPath]);
      applyFFmpegProbeFallback(result, fallback.stderr || fallback.stdout);
    }
    return result;
  }
  result.ffprobe_available = true;

  const probe = await runCommand(ffprobePath, [
    "-v",
    "error",
    "-show_entries",
    "format=format_name,duration:stream=codec_type,codec_name,width,height,avg_frame_rate,r_frame_rate,pix_fmt",
    "-of",
    "json",
    inputPath,
  ]);
  if (probe.code !== 0) {
    throw new Error(`ffprobe_failed: ${compactOutput(probe.stderr || probe.stdout || probe.error || "unknown error")}`);
  }
  const parsed = JSON.parse(probe.stdout) as {
    format?: { format_name?: unknown; duration?: unknown };
    streams?: Array<Record<string, unknown>>;
  };
  const format = stringValue(parsed.format?.format_name);
  const durationSec = numberValue(parsed.format?.duration);
  const video = parsed.streams?.find((stream) => stringValue(stream.codec_type) === "video");
  const audio = parsed.streams?.find((stream) => stringValue(stream.codec_type) === "audio");
  if (format) result.format = format;
  if (durationSec !== undefined) result.duration_ms = Math.max(0, Math.round(durationSec * 1000));
  if (video) {
    const codec = stringValue(video.codec_name);
    const width = numberValue(video.width);
    const height = numberValue(video.height);
    const fps = parseFrameRate(stringValue(video.avg_frame_rate) || stringValue(video.r_frame_rate));
    const pixelFormat = stringValue(video.pix_fmt);
    if (codec) result.video_codec = codec;
    if (width !== undefined) result.width = width;
    if (height !== undefined) result.height = height;
    if (fps !== undefined) result.fps = fps;
    if (pixelFormat) result.pixel_format = pixelFormat;
  }
  const audioCodec = stringValue(audio?.codec_name);
  if (audioCodec) result.audio_codec = audioCodec;
  const ffmpegPath = process.env.CASCADE_FFMPEG_PATH || "ffmpeg";
  const quality = await runCommand(ffmpegPath, [
    "-hide_banner", "-nostats", "-i", inputPath,
    "-vf", "blackdetect=d=0.1:pix_th=0.10,freezedetect=n=-50dB:d=0.5",
    ...(audio ? ["-af", "ebur128=peak=true,silencedetect=noise=-50dB:d=0.5"] : []),
    "-f", "null", "-",
  ]);
  if (quality.code === 0) applyFFmpegQualityAnalysis(result, quality.stderr || quality.stdout);
  if (request.analyze_temporal_quality) {
    await applyTemporalQualityAnalysis(result, ffmpegPath, inputPath);
  }
  if (request.contact_sheet_path && video) {
    const frames = Math.max(4, Math.min(24, Math.trunc(request.contact_sheet_frames || 8)));
    const sheetPath = path.resolve(request.contact_sheet_path);
    await mkdir(path.dirname(sheetPath), { recursive: true });
    const duration = Math.max(0.1, (result.duration_ms || 1000) / 1000);
    const columns = Math.min(4, frames);
    const rows = Math.ceil(frames / columns);
    const sheet = await runCommand(ffmpegPath, [
      "-hide_banner", "-loglevel", "error", "-y", "-i", inputPath,
      "-vf", `fps=${frames}/${duration},scale=480:-2:flags=lanczos,tile=${columns}x${rows}:padding=4:margin=4`,
      "-frames:v", "1", sheetPath,
    ]);
    if (sheet.code === 0) result.contact_sheet_path = sheetPath;
  }
  return result;
}

async function applyTemporalQualityAnalysis(result: MediaProbeResult, ffmpegPath: string, inputPath: string): Promise<void> {
  const signal = await runCommand(ffmpegPath, [
    "-hide_banner", "-nostats", "-i", inputPath,
    "-vf", "signalstats,metadata=print:key=lavfi.signalstats.YAVG",
    "-an", "-f", "null", "-",
  ]);
  if (signal.code === 0) {
    const values = [...(signal.stderr || signal.stdout).matchAll(/lavfi\.signalstats\.YAVG=([0-9]+(?:\.[0-9]+)?)/g)]
      .map((match) => Number(match[1])).filter(Number.isFinite);
    result.temporal_analysis_available = values.length >= 2;
    result.full_screen_flash_count = countFullScreenFlashes(values);
  }
  // vidstabdetect is measurement-only here. We never feed its transform file
  // into vidstabtransform, because quality gates must not hide a bad source.
  const transformsPath = `${inputPath}.quality.trf`;
  const jitter = await runCommand(ffmpegPath, [
    "-hide_banner", "-nostats", "-i", inputPath,
    "-vf", `vidstabdetect=shakiness=5:accuracy=9:result=${escapeFFmpegFilterPath(transformsPath)}`,
    "-an", "-f", "null", "-",
  ]);
  if (jitter.code === 0) {
    result.jitter_measurement_available = true;
    const output = jitter.stderr || jitter.stdout;
    const score = [...output.matchAll(/(?:average|avg)[^0-9-]*(-?[0-9]+(?:\.[0-9]+)?)/gi)].at(-1)?.[1];
    if (score !== undefined && Number.isFinite(Number(score))) result.jitter_score = Math.max(0, Number(score));
  }
}

export function countFullScreenFlashes(values: number[], threshold = 48): number {
  let flashes = 0;
  for (let index = 1; index < values.length; index++) {
    const current = values[index];
    const previous = values[index - 1];
    if (current !== undefined && previous !== undefined && Math.abs(current - previous) >= threshold) flashes++;
  }
  return flashes;
}

function escapeFFmpegFilterPath(value: string): string {
  return value.replace(/\\/g, "/").replace(/:/g, "\\:").replace(/'/g, "\\'");
}

export function applyFFmpegQualityAnalysis(result: MediaProbeResult, output: string): void {
  result.quality_analysis_available = true;
  result.black_duration_ms = sumDetectedIntervals(output, "black", result.duration_ms);
  result.freeze_duration_ms = sumDetectedIntervals(output, "freeze", result.duration_ms);
  result.silence_duration_ms = sumDetectedIntervals(output, "silence", result.duration_ms);
  result.verified_silence = (result.duration_ms ?? 0) > 0 && result.silence_duration_ms >= Math.max(0, (result.duration_ms ?? 0) - 500);
  const loudness = [...output.matchAll(/\bI:\s*(-?[0-9]+(?:\.[0-9]+)?)\s*LUFS/g)].at(-1)?.[1];
  const peak = [...output.matchAll(/\bPeak:\s*(-?[0-9]+(?:\.[0-9]+)?)\s*dBFS/g)].at(-1)?.[1];
  if (loudness !== undefined && Number.isFinite(Number(loudness))) result.integrated_lufs = Number(loudness);
  if (peak !== undefined && Number.isFinite(Number(peak))) result.true_peak_db = Number(peak);
}

function sumDetectedIntervals(output: string, kind: "black" | "freeze" | "silence", durationMS: number | undefined): number {
  const pattern = new RegExp(`${kind}_(start|end):\\s*([0-9]+(?:\\.[0-9]+)?)`, "g");
  let totalSeconds = 0;
  let openStart: number | undefined;
  for (const match of output.matchAll(pattern)) {
    const value = Number(match[2]);
    if (!Number.isFinite(value)) continue;
    if (match[1] === "start") {
      openStart = value;
      continue;
    }
    if (openStart !== undefined) {
      totalSeconds += Math.max(0, value - openStart);
      openStart = undefined;
    }
  }
  // freezedetect and silencedetect can leave the final interval open at EOF.
  // Counting only emitted *_duration lines systematically under-reports a
  // frozen or silent tail and makes baseline-relative quality gates unstable.
  if (openStart !== undefined && durationMS !== undefined) {
    totalSeconds += Math.max(0, durationMS / 1000 - openStart);
  }
  return Math.max(0, Math.round(totalSeconds * 1000));
}

export function applyFFmpegProbeFallback(result: MediaProbeResult, output: string): void {
  const duration = output.match(/Duration:\s*(\d+):(\d+):(\d+(?:\.\d+)?)/i);
  if (duration) {
    const hours = Number(duration[1]);
    const minutes = Number(duration[2]);
    const seconds = Number(duration[3]);
    if ([hours, minutes, seconds].every(Number.isFinite)) {
      result.duration_ms = Math.max(0, Math.round((hours * 3600 + minutes * 60 + seconds) * 1000));
    }
  }
  const videoLine = output.split(/\r?\n/).find((line) => /Video:/i.test(line));
  if (videoLine) {
    const codec = videoLine.match(/Video:\s*([^,\s]+)/i)?.[1];
    const resolution = videoLine.match(/(?:^|[\s,])(\d{2,5})x(\d{2,5})(?:[\s,]|$)/);
    const fps = videoLine.match(/(\d+(?:\.\d+)?)\s*fps/i)?.[1];
    const pixelFormat = videoLine.match(/Video:[^,]+,\s*([^,\s]+)/i)?.[1];
    if (codec) result.video_codec = codec;
    if (resolution) {
      result.width = Number(resolution[1]);
      result.height = Number(resolution[2]);
    }
    if (fps && Number.isFinite(Number(fps))) result.fps = Number(fps);
    if (pixelFormat && !/^\d+x\d+$/i.test(pixelFormat)) result.pixel_format = pixelFormat;
  }
  const audioCodec = output.match(/Audio:\s*([^,\s]+)/i)?.[1];
  if (audioCodec) result.audio_codec = audioCodec;
}

function ffprobePathFor(ffmpegPath: string): string {
  const baseName = path.basename(ffmpegPath).toLowerCase();
  if (baseName === "ffmpeg.exe") return path.join(path.dirname(ffmpegPath), "ffprobe.exe");
  if (baseName === "ffmpeg") return path.join(path.dirname(ffmpegPath), "ffprobe");
  if (ffmpegPath.includes("/") || ffmpegPath.includes("\\")) {
    return path.join(path.dirname(ffmpegPath), process.platform === "win32" ? "ffprobe.exe" : "ffprobe");
  }
  return "ffprobe";
}

function sha256File(filePath: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const digest = createHash("sha256");
    const input = createReadStream(filePath);
    input.on("data", (chunk) => digest.update(chunk));
    input.on("error", reject);
    input.on("end", () => resolve(`sha256:${digest.digest("hex")}`));
  });
}

function runCommand(command: string, args: string[]): Promise<{ code: number | null; stdout: string; stderr: string; error?: string }> {
  return new Promise((resolve) => {
    const child = spawn(command, args, { windowsHide: true });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => (stdout += String(chunk)));
    child.stderr.on("data", (chunk) => (stderr += String(chunk)));
    child.on("error", (error) => resolve({ code: -1, stdout, stderr, error: error.message }));
    child.on("close", (code) => resolve({ code, stdout, stderr }));
  });
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function numberValue(value: unknown): number | undefined {
  const numeric = typeof value === "number" ? value : typeof value === "string" ? Number(value) : Number.NaN;
  return Number.isFinite(numeric) ? numeric : undefined;
}

function parseFrameRate(value: string | undefined): number | undefined {
  if (!value || value === "0/0") return undefined;
  const [numeratorText, denominatorText] = value.split("/");
  const numerator = Number(numeratorText);
  const denominator = denominatorText === undefined ? 1 : Number(denominatorText);
  if (!Number.isFinite(numerator) || !Number.isFinite(denominator) || denominator === 0) return undefined;
  return Math.round((numerator / denominator) * 1000) / 1000;
}

function compactOutput(value: string): string {
  return value.replace(/\s+/g, " ").trim().slice(0, 300);
}
