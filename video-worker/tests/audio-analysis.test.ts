import { describe, expect, it } from "vitest";

import { analyzePCM16LE, deriveSilenceRanges } from "../src/audio-analysis.js";

function pcm(samples: number[]): Buffer {
  const result = Buffer.alloc(samples.length * 2);
  samples.forEach((sample, index) => result.writeInt16LE(sample, index * 2));
  return result;
}

describe("audio analysis", () => {
  it("computes deterministic peak and RMS dBFS buckets", () => {
    const analysis = analyzePCM16LE(pcm([...Array(100).fill(0), ...Array(100).fill(16384)]), 1000, 100);
    expect(analysis.duration_ms).toBe(200);
    expect(analysis.peak_dbfs).toEqual([-120, -6.02]);
    expect(analysis.rms_dbfs).toEqual([-120, -6.02]);
  });

  it("handles PCM chunks with a trailing byte and a partial final bucket", () => {
    const input = pcm([...Array(120).fill(8192)]);
    const analysis = analyzePCM16LE(input, 1000, 100);
    expect(analysis.duration_ms).toBe(120);
    expect(analysis.peak_dbfs).toEqual([-12.04, -12.04]);
    expect(analysis.rms_dbfs).toEqual([-12.04, -12.04]);
  });

  it("returns only silence runs meeting the minimum duration", () => {
    const rms = [...Array(6).fill(-120), ...Array(4).fill(-12), ...Array(5).fill(-60), -12];
    expect(deriveSilenceRanges(rms, 1600, 100, -45, 500)).toEqual([[0, 600], [1000, 1500]]);
    expect(deriveSilenceRanges([-12, -120, -120, -120], 350, 100, -45, 300)).toEqual([]);
  });
});
