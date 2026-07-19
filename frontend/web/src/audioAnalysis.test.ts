import { describe, expect, it } from "vitest";

import type { EditorAudioAnalysis, EditorPlan } from "./editor";
import { buildOutputWaveform, buildSilenceCandidates, waveformBarsForRange } from "./audioAnalysis";

const analysis: EditorAudioAnalysis = {
  schema_version: "demoops.audio_analysis.v1",
  artifact_id: "raw",
  path: "D:\\raw.mp4",
  has_audio: true,
  duration_ms: 4000,
  bucket_ms: 1000,
  sample_rate_hz: 1000,
  peak_dbfs: [-6, -12, -120, -18],
  rms_dbfs: [-12, -18, -120, -24],
  silence_ranges_ms: [[2000, 3000]],
  silence_threshold_db: -45,
  min_silence_ms: 500,
  ffmpeg_available: true,
};

function plan(): EditorPlan {
  return {
    plan_id: "plan",
    source_authority: "server_local_editor",
    model_role: "presentation_optimizer_only",
    source_material_policy: "existing_assets_only",
    script_order_policy: "preserve_required_step_order",
    locked_fields: [],
    model_editable_fields: [],
    shots: [
      { id: "later", source_artifact_id: "raw", source_time_range_ms: [2000, 4000], purpose: "later" },
      { id: "earlier", source_artifact_id: "raw", source_time_range_ms: [0, 2000], purpose: "earlier" },
    ],
  };
}

describe("output audio analysis mapping", () => {
  it("maps source buckets through trimmed and reordered shots", () => {
    expect(buildOutputWaveform(plan(), { raw: analysis })).toEqual([
      { startMS: 0, endMS: 1000, peakDBFS: -120, rmsDBFS: -120, artifactID: "raw" },
      { startMS: 1000, endMS: 2000, peakDBFS: -18, rmsDBFS: -24, artifactID: "raw" },
      { startMS: 2000, endMS: 3000, peakDBFS: -6, rmsDBFS: -12, artifactID: "raw" },
      { startMS: 3000, endMS: 4000, peakDBFS: -12, rmsDBFS: -18, artifactID: "raw" },
    ]);
  });

  it("builds reviewable output-time silence candidates without changing the plan", () => {
    expect(buildSilenceCandidates(plan(), { raw: analysis })).toEqual([{
      id: "silence_later_2000_3000",
      startMS: 0,
      endMS: 1000,
      action: "mute",
      signalConfidence: 0.99,
      reason: "detected_silence",
      sourceArtifactIDs: ["raw"],
    }]);
    expect(plan().audio).toBeUndefined();
  });

  it("downsamples waveform peaks to a bounded number of visual bars", () => {
    const waveform = buildOutputWaveform(plan(), { raw: analysis });
    expect(waveformBarsForRange(waveform, 0, 4000, 2)).toEqual([0.7, 0.9]);
    expect(waveformBarsForRange([], 0, 1000, 20)).toEqual([]);
  });
});
