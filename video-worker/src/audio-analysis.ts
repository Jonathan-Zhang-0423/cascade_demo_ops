import { spawn } from "node:child_process";
import { stat } from "node:fs/promises";
import path from "node:path";

export interface AudioAnalysisRequest {
  path: string;
  bucket_ms?: number;
  silence_threshold_db?: number;
  min_silence_ms?: number;
}

export interface AudioAnalysisResult {
  schema_version: "demoops.audio_analysis.v1";
  path: string;
  has_audio: boolean;
  duration_ms: number;
  bucket_ms: number;
  sample_rate_hz: number;
  peak_dbfs: number[];
  rms_dbfs: number[];
  silence_ranges_ms: Array<[number, number]>;
  silence_threshold_db: number;
  min_silence_ms: number;
  ffmpeg_available: true;
}

export type PCMAnalysis = Pick<AudioAnalysisResult, "duration_ms" | "peak_dbfs" | "rms_dbfs">;

const SAMPLE_RATE_HZ = 1000;
const MIN_DBFS = -120;

export async function analyzeAudioFile(request: AudioAnalysisRequest): Promise<AudioAnalysisResult> {
  const inputPath = path.resolve(request.path || "");
  const info = await stat(inputPath);
  if (!info.isFile()) throw new Error("audio analysis path is not a file");

  const bucketMS = boundedInteger(request.bucket_ms, 100, 20, 1000, "bucket_ms");
  const silenceThresholdDB = boundedNumber(request.silence_threshold_db, -45, -120, 0, "silence_threshold_db");
  const minSilenceMS = boundedInteger(request.min_silence_ms, 500, bucketMS, 60_000, "min_silence_ms");
  const ffmpegPath = process.env.CASCADE_FFMPEG_PATH || "ffmpeg";
  const analyzer = new PCMStreamAnalyzer(SAMPLE_RATE_HZ, bucketMS);
  const decoded = await decodeAudio(ffmpegPath, inputPath, (chunk) => analyzer.append(chunk));
  if (decoded.unavailable) throw new Error(`ffmpeg_not_available: ${decoded.error || ffmpegPath}`);

  if (decoded.noAudio) {
    return {
      schema_version: "demoops.audio_analysis.v1",
      path: inputPath,
      has_audio: false,
      duration_ms: 0,
      bucket_ms: bucketMS,
      sample_rate_hz: SAMPLE_RATE_HZ,
      peak_dbfs: [],
      rms_dbfs: [],
      silence_ranges_ms: [],
      silence_threshold_db: silenceThresholdDB,
      min_silence_ms: minSilenceMS,
      ffmpeg_available: true,
    };
  }
  if (decoded.code !== 0) throw new Error(`audio_analysis_failed: ${compactOutput(decoded.error || "unknown ffmpeg error")}`);

  const analysis = analyzer.finish();
  return {
    schema_version: "demoops.audio_analysis.v1",
    path: inputPath,
    has_audio: true,
    ...analysis,
    bucket_ms: bucketMS,
    sample_rate_hz: SAMPLE_RATE_HZ,
    silence_ranges_ms: deriveSilenceRanges(analysis.rms_dbfs, analysis.duration_ms, bucketMS, silenceThresholdDB, minSilenceMS),
    silence_threshold_db: silenceThresholdDB,
    min_silence_ms: minSilenceMS,
    ffmpeg_available: true,
  };
}

export function analyzePCM16LE(buffer: Buffer, sampleRateHZ = SAMPLE_RATE_HZ, bucketMS = 100): PCMAnalysis {
  const analyzer = new PCMStreamAnalyzer(sampleRateHZ, bucketMS);
  analyzer.append(buffer);
  return analyzer.finish();
}

export function deriveSilenceRanges(rmsDBFS: number[], durationMS: number, bucketMS: number, thresholdDB: number, minSilenceMS: number): Array<[number, number]> {
  const ranges: Array<[number, number]> = [];
  let startMS: number | undefined;
  for (let index = 0; index < rmsDBFS.length; index += 1) {
    const bucketStartMS = index * bucketMS;
    const silent = (rmsDBFS[index] ?? 0) <= thresholdDB;
    if (silent && startMS === undefined) startMS = bucketStartMS;
    const endOfAnalysis = index === rmsDBFS.length - 1;
    if (startMS !== undefined && (!silent || endOfAnalysis)) {
      const endMS = Math.min(durationMS, silent && endOfAnalysis ? (index + 1) * bucketMS : bucketStartMS);
      if (endMS - startMS >= minSilenceMS) ranges.push([startMS, endMS]);
      startMS = undefined;
    }
  }
  return ranges;
}

class PCMStreamAnalyzer {
  private readonly samplesPerBucket: number;
  private carry: Buffer = Buffer.alloc(0);
  private bucketPeak = 0;
  private bucketSumSquares = 0;
  private bucketSampleCount = 0;
  private totalSamples = 0;
  private readonly peakDBFS: number[] = [];
  private readonly rmsDBFS: number[] = [];

  constructor(private readonly sampleRateHZ: number, bucketMS: number) {
    if (!Number.isFinite(sampleRateHZ) || sampleRateHZ <= 0) throw new Error("sample rate must be positive");
    this.samplesPerBucket = Math.max(1, Math.round(sampleRateHZ * bucketMS / 1000));
  }

  append(chunk: Buffer): void {
    const input = this.carry.length ? Buffer.concat([this.carry, chunk]) : chunk;
    const usableBytes = input.length - input.length % 2;
    for (let offset = 0; offset < usableBytes; offset += 2) {
      const amplitude = Math.abs(input.readInt16LE(offset)) / 32768;
      this.bucketPeak = Math.max(this.bucketPeak, amplitude);
      this.bucketSumSquares += amplitude * amplitude;
      this.bucketSampleCount += 1;
      this.totalSamples += 1;
      if (this.bucketSampleCount === this.samplesPerBucket) this.flushBucket();
    }
    this.carry = usableBytes === input.length ? Buffer.alloc(0) : input.subarray(usableBytes);
  }

  finish(): PCMAnalysis {
    if (this.bucketSampleCount > 0) this.flushBucket();
    return {
      duration_ms: Math.round(this.totalSamples / this.sampleRateHZ * 1000),
      peak_dbfs: [...this.peakDBFS],
      rms_dbfs: [...this.rmsDBFS],
    };
  }

  private flushBucket(): void {
    this.peakDBFS.push(toDBFS(this.bucketPeak));
    this.rmsDBFS.push(toDBFS(Math.sqrt(this.bucketSumSquares / this.bucketSampleCount)));
    this.bucketPeak = 0;
    this.bucketSumSquares = 0;
    this.bucketSampleCount = 0;
  }
}

function decodeAudio(command: string, inputPath: string, onChunk: (chunk: Buffer) => void): Promise<{ code: number | null; error?: string; unavailable?: boolean; noAudio?: boolean }> {
  return new Promise((resolve) => {
    const child = spawn(command, ["-v", "error", "-i", inputPath, "-map", "0:a:0", "-vn", "-ac", "1", "-ar", String(SAMPLE_RATE_HZ), "-f", "s16le", "pipe:1"], { windowsHide: true });
    let stderr = "";
    let settled = false;
    child.stdout.on("data", (chunk: Buffer) => onChunk(chunk));
    child.stderr.on("data", (chunk) => (stderr += String(chunk)));
    child.on("error", (error) => {
      if (settled) return;
      settled = true;
      resolve({ code: -1, error: error.message, unavailable: true });
    });
    child.on("close", (code) => {
      if (settled) return;
      settled = true;
      const noAudio = /matches no streams|does not contain any stream|stream specifier.*no streams/i.test(stderr);
      resolve({ code, ...(stderr ? { error: stderr } : {}), ...(noAudio ? { noAudio: true } : {}) });
    });
  });
}

function toDBFS(amplitude: number): number {
  if (amplitude <= 0) return MIN_DBFS;
  return Math.max(MIN_DBFS, Math.round(20 * Math.log10(amplitude) * 100) / 100);
}

function boundedInteger(value: number | undefined, fallback: number, min: number, max: number, name: string): number {
  const actual = value ?? fallback;
  if (!Number.isInteger(actual) || actual < min || actual > max) throw new Error(`${name} must be an integer from ${min} to ${max}`);
  return actual;
}

function boundedNumber(value: number | undefined, fallback: number, min: number, max: number, name: string): number {
  const actual = value ?? fallback;
  if (!Number.isFinite(actual) || actual < min || actual > max) throw new Error(`${name} must be from ${min} to ${max}`);
  return actual;
}

function compactOutput(value: string): string {
  return value.replace(/\s+/g, " ").trim().slice(0, 500);
}
