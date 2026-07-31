import { writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { describe, expect, it } from "vitest";

import { applyFFmpegProbeFallback, probeMediaFile, type MediaProbeResult } from "../src/media-probe.js";

describe("FFmpeg media probe fallback", () => {
  it("extracts duration and stream metadata without ffprobe", () => {
    const result: MediaProbeResult = {
      path: "D:\\media\\source.mp4",
      file_name: "source.mp4",
      size_bytes: 1024,
      sha256: "sha256:test",
      mime_type: "video/mp4",
      ffprobe_available: false,
    };

    applyFFmpegProbeFallback(
      result,
      [
        "Duration: 00:00:08.02, start: 0.000000, bitrate: 136 kb/s",
        "Stream #0:0: Video: h264 (High), yuv420p(progressive), 1280x720, 30 fps, 30 tbr",
        "Stream #0:1: Audio: aac (LC), 44100 Hz, stereo, fltp",
      ].join("\n"),
    );

    expect(result).toMatchObject({
      duration_ms: 8020,
      video_codec: "h264",
      audio_codec: "aac",
      width: 1280,
      height: 720,
      fps: 30,
      pixel_format: "yuv420p(progressive)",
      ffprobe_available: false,
    });
  });

  it("leaves optional metadata empty when FFmpeg output has no streams", () => {
    const result: MediaProbeResult = {
      path: "D:\\media\\source.mp4",
      file_name: "source.mp4",
      size_bytes: 1024,
      sha256: "sha256:test",
      mime_type: "video/mp4",
      ffprobe_available: false,
    };

    applyFFmpegProbeFallback(result, "source.mp4: Invalid data found when processing input");

    expect(result.duration_ms).toBeUndefined();
    expect(result.video_codec).toBeUndefined();
    expect(result.audio_codec).toBeUndefined();
  });

  it("accepts an imported audio extension before optional FFprobe metadata is available", async () => {
    const inputPath = path.join(tmpdir(), `cascade-media-probe-${Date.now()}.wav`);
    const previousFFprobe = process.env.CASCADE_FFPROBE_PATH;
    const previousFFmpeg = process.env.CASCADE_FFMPEG_PATH;
    try {
      process.env.CASCADE_FFPROBE_PATH = "definitely-not-installed-ffprobe-for-import-test";
      process.env.CASCADE_FFMPEG_PATH = "definitely-not-installed-ffmpeg-for-import-test";
      await writeFile(inputPath, "fixture");
      const result = await probeMediaFile({ path: inputPath });
      expect(result).toMatchObject({ file_name: path.basename(inputPath), mime_type: "audio/wav" });
    } finally {
      if (previousFFprobe === undefined) delete process.env.CASCADE_FFPROBE_PATH;
      else process.env.CASCADE_FFPROBE_PATH = previousFFprobe;
      if (previousFFmpeg === undefined) delete process.env.CASCADE_FFMPEG_PATH;
      else process.env.CASCADE_FFMPEG_PATH = previousFFmpeg;
      await import("node:fs/promises").then(({ rm }) => rm(inputPath, { force: true }));
    }
  });
});
