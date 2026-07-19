import type { EditorAudioAnalysis, EditorPlan } from "./editor";

export type OutputWaveformBucket = {
  startMS: number;
  endMS: number;
  peakDBFS: number;
  rmsDBFS: number;
  artifactID: string;
};

export type AudioEditCandidate = {
  id: string;
  startMS: number;
  endMS: number;
  action: "mute";
  signalConfidence: number;
  reason: "detected_silence";
  sourceArtifactIDs: string[];
};

export function buildOutputWaveform(plan: EditorPlan, analyses: Record<string, EditorAudioAnalysis>): OutputWaveformBucket[] {
  const result: OutputWaveformBucket[] = [];
  let outputCursorMS = 0;
  for (const shot of plan.shots) {
    const range = shot.source_time_range_ms;
    const durationMS = range ? Math.max(0, range[1] - range[0]) : 0;
    const analysis = analyses[shot.source_artifact_id];
    if (range && analysis?.has_audio) {
      const firstBucket = Math.max(0, Math.floor(range[0] / analysis.bucket_ms));
      const lastBucket = Math.min(analysis.peak_dbfs.length - 1, Math.floor(Math.max(range[0], range[1] - 1) / analysis.bucket_ms));
      for (let index = firstBucket; index <= lastBucket; index += 1) {
        const sourceStartMS = Math.max(range[0], index * analysis.bucket_ms);
        const sourceEndMS = Math.min(range[1], (index + 1) * analysis.bucket_ms, analysis.duration_ms);
        if (sourceEndMS <= sourceStartMS) continue;
        result.push({
          startMS: outputCursorMS + sourceStartMS - range[0],
          endMS: outputCursorMS + sourceEndMS - range[0],
          peakDBFS: analysis.peak_dbfs[index] ?? -120,
          rmsDBFS: analysis.rms_dbfs[index] ?? -120,
          artifactID: shot.source_artifact_id,
        });
      }
    }
    outputCursorMS += durationMS;
  }
  return result;
}

export function waveformBarsForRange(buckets: OutputWaveformBucket[], startMS: number, endMS: number, maxBars: number): number[] {
  const overlapping = buckets.filter((bucket) => bucket.endMS > startMS && bucket.startMS < endMS);
  if (!overlapping.length || maxBars <= 0) return [];
  const count = Math.min(Math.max(1, Math.round(maxBars)), overlapping.length);
  const bars: number[] = [];
  for (let index = 0; index < count; index += 1) {
    const from = Math.floor(index * overlapping.length / count);
    const to = Math.max(from + 1, Math.ceil((index + 1) * overlapping.length / count));
    const peakDBFS = Math.max(...overlapping.slice(from, to).map((bucket) => bucket.peakDBFS));
    bars.push(Math.max(0.06, Math.min(1, (peakDBFS + 60) / 60)));
  }
  return bars;
}

export function buildSilenceCandidates(plan: EditorPlan, analyses: Record<string, EditorAudioAnalysis>, minOutputDurationMS = 500): AudioEditCandidate[] {
  const candidates: AudioEditCandidate[] = [];
  let outputCursorMS = 0;
  for (const shot of plan.shots) {
    const range = shot.source_time_range_ms;
    const durationMS = range ? Math.max(0, range[1] - range[0]) : 0;
    const analysis = analyses[shot.source_artifact_id];
    if (range && analysis?.has_audio) {
      for (const silence of analysis.silence_ranges_ms) {
        const sourceStartMS = Math.max(range[0], silence[0]);
        const sourceEndMS = Math.min(range[1], silence[1]);
        if (sourceEndMS - sourceStartMS < minOutputDurationMS) continue;
        const startMS = outputCursorMS + sourceStartMS - range[0];
        const endMS = outputCursorMS + sourceEndMS - range[0];
        const rms = analysis.rms_dbfs.slice(Math.floor(sourceStartMS / analysis.bucket_ms), Math.ceil(sourceEndMS / analysis.bucket_ms));
        const averageRMS = rms.length ? rms.reduce((total, value) => total + value, 0) / rms.length : analysis.silence_threshold_db;
        const marginDB = Math.max(0, analysis.silence_threshold_db - averageRMS);
        const signalConfidence = Math.round(Math.min(0.99, 0.5 + marginDB / 120) * 100) / 100;
        candidates.push({
          id: `silence_${shot.id}_${sourceStartMS}_${sourceEndMS}`,
          startMS,
          endMS,
          action: "mute",
          signalConfidence,
          reason: "detected_silence",
          sourceArtifactIDs: [shot.source_artifact_id],
        });
      }
    }
    outputCursorMS += durationMS;
  }
  return mergeAdjacentCandidates(candidates);
}

function mergeAdjacentCandidates(candidates: AudioEditCandidate[]): AudioEditCandidate[] {
  const merged: AudioEditCandidate[] = [];
  for (const candidate of candidates.sort((left, right) => left.startMS - right.startMS)) {
    const previous = merged.at(-1);
    if (previous && candidate.startMS <= previous.endMS + 1) {
      previous.endMS = Math.max(previous.endMS, candidate.endMS);
      previous.signalConfidence = Math.min(previous.signalConfidence, candidate.signalConfidence);
      previous.sourceArtifactIDs = [...new Set([...previous.sourceArtifactIDs, ...candidate.sourceArtifactIDs])];
    } else {
      merged.push({ ...candidate, sourceArtifactIDs: [...candidate.sourceArtifactIDs] });
    }
  }
  return merged;
}
