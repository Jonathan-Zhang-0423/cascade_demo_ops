import { describe, expect, it } from "vitest";

import { applyFFmpegProbeFallback, type MediaProbeResult } from "../src/media-probe.js";

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
});
