import { describe, expect, it } from "vitest";
import type { EditorPlan, EditorShot, EditorTimelineStep } from "./editor";
import { activeCaptionText, audioPreviewState, buildAudioSegments, deleteShotInPlan, mergeAudioSegmentInPlan, muteAudioRangeInPlan, patchAudioSegmentInPlan, reorderShotInPlan, requiredStepCoverage, snapMilliseconds, sourceSnapPoints, splitAudioAtOutputMS, splitShotAtOutputMS, targetIndexForOutputMS, trimShotInPlan } from "./timelineEditing";

const steps: EditorTimelineStep[] = [
  { step_id: "open", order: 0, action: "navigate", status: "passed", required: true, start_ms: 1000, end_ms: 3000, duration_ms: 2000 },
  { step_id: "create", order: 1, action: "click", status: "passed", required: true, start_ms: 3000, end_ms: 6000, duration_ms: 3000 },
];

function shot(id: string, stepID: string, range: [number, number]): EditorShot {
  return { id, source_artifact_id: "raw", source_step_id: stepID, source_time_range_ms: range, purpose: id, overlays: [{ type: "caption", text: id, start_ms: 0, end_ms: range[1] - range[0] }] };
}

function plan(shots: EditorShot[]): EditorPlan {
  return {
    plan_id: "plan",
    source_authority: "server_local_editor",
    model_role: "presentation_optimizer_only",
    source_material_policy: "existing_assets_only",
    script_order_policy: "preserve_required_step_order",
    locked_fields: [],
    model_editable_fields: [],
    shots,
  };
}

describe("timeline editing", () => {
  it("snaps to frames and source evidence boundaries", () => {
    expect(snapMilliseconds(1009, 30).valueMS).toBe(1000);
    expect(snapMilliseconds(2940, 30, [3000], 100)).toMatchObject({ valueMS: 3000, snapped: true, snapPointMS: 3000 });
    expect(sourceSnapPoints(shot("one", "open", [1000, 3000]), steps[0], { id: "raw", kind: "raw_recording", uri: "file:///raw", duration_ms: 8000 })).toEqual([0, 1000, 3000, 8000]);
  });

  it("trims one source edge and clamps timed overlays", () => {
    const base = plan([shot("one", "open", [1000, 3000])]);
    base.shots[0]!.operations = [{ type: "trim", start_ms: 1000, end_ms: 3000 }];
    const result = trimShotInPlan({ plan: base, shotID: "one", edge: "end", valueMS: 2200, fps: 30, sourceDurationMS: 8000 });
    expect(result.changed).toBe(true);
    expect(result.plan.shots[0]?.source_time_range_ms).toEqual([1000, 2200]);
    expect(result.plan.shots[0]?.overlays?.[0]).toMatchObject({ start_ms: 0, end_ms: 1200 });
    expect(result.plan.shots[0]?.operations?.[0]).toMatchObject({ type: "trim", start_ms: 1000, end_ms: 2200 });
  });

  it("relocates overlays when trimming the source start and removes overlays outside the result", () => {
    const baseShot = shot("one", "open", [1000, 4000]);
    baseShot.overlays = [
      { type: "caption", text: "gone", start_ms: 0, end_ms: 400 },
      { type: "caption", text: "shift", start_ms: 800, end_ms: 1800 },
      { type: "watermark", text: "untimed" },
    ];
    const result = trimShotInPlan({ plan: plan([baseShot]), shotID: "one", edge: "start", valueMS: 2000, fps: 30 });
    expect(result.plan.shots[0]?.overlays).toEqual([
      { type: "caption", text: "shift", start_ms: 0, end_ms: 800 },
      { type: "watermark", text: "untimed" },
    ]);
  });

  it("allows presentation reordering but blocks required step inversion", () => {
    const free = plan([shot("one", "", [0, 1000]), shot("two", "", [1000, 2000])]);
    expect(reorderShotInPlan(free, "two", 0, steps).plan.shots.map((item) => item.id)).toEqual(["two", "one"]);

    const locked = plan([shot("one", "open", [1000, 3000]), shot("two", "create", [3000, 6000])]);
    const blocked = reorderShotInPlan(locked, "two", 0, steps);
    expect(blocked.changed).toBe(false);
    expect(blocked.blockedReason).toContain("步骤顺序");
    expect(targetIndexForOutputMS(locked, 500)).toBe(0);
    expect(targetIndexForOutputMS(locked, 5000)).toBe(1);
  });

  it("requires the full declared source range for required step evidence", () => {
    const complete = plan([shot("one-a", "open", [1000, 2000]), shot("one-b", "open", [2000, 3000]), shot("two", "create", [3000, 6000])]);
    expect(requiredStepCoverage(complete, steps).map((item) => item.covered)).toEqual([true, true]);

    const trimmed = plan([shot("one", "open", [1200, 3000]), shot("two", "create", [3000, 6000])]);
    expect(requiredStepCoverage(trimmed, steps)[0]?.covered).toBe(false);
  });

  it("splits the shot under the output playhead and maps it to source time", () => {
    const base = plan([shot("one", "open", [1000, 3500])]);
    base.shots[0]!.operations = [{ type: "trim", start_ms: 1000, end_ms: 3500 }];
    const result = splitShotAtOutputMS({ plan: base, outputMS: 1500, fps: 30, newShotID: "one-right" });

    expect(result.changed).toBe(true);
    expect(result.sourceSplitMS).toBe(2500);
    expect(result.outputSplitMS).toBe(1500);
    expect(result.plan.shots.map((item) => item.source_time_range_ms)).toEqual([[1000, 2500], [2500, 3500]]);
    expect(result.plan.shots.map((item) => item.source_step_id)).toEqual(["open", "open"]);
    expect(result.plan.shots.map((item) => item.operations?.[0])).toEqual([
      { type: "trim", start_ms: 1000, end_ms: 2500 },
      { type: "trim", start_ms: 2500, end_ms: 3500 },
    ]);
    expect(result.rightShotID).toBe("one-right");
    expect(requiredStepCoverage(result.plan, steps)[0]?.covered).toBe(true);
  });

  it("splits and relocates overlays that cross or follow the playhead", () => {
    const baseShot = shot("one", "open", [1000, 3500]);
    baseShot.overlays = [
      { type: "caption", text: "cross", start_ms: 1000, end_ms: 2000 },
      { type: "callout", text: "right", start_ms: 1800, end_ms: 2300 },
      { type: "watermark", text: "whole shot" },
    ];
    const result = splitShotAtOutputMS({ plan: plan([baseShot]), outputMS: 1500, fps: 30, newShotID: "one-right" });

    expect(result.plan.shots[0]?.overlays).toEqual([
      { type: "caption", text: "cross", start_ms: 1000, end_ms: 1500 },
      { type: "watermark", text: "whole shot" },
    ]);
    expect(result.plan.shots[1]?.overlays).toEqual([
      { type: "caption", text: "cross", start_ms: 0, end_ms: 500 },
      { type: "callout", text: "right", start_ms: 300, end_ms: 800 },
      { type: "watermark", text: "whole shot" },
    ]);
  });

  it("shows source-preview captions only during their declared interval", () => {
    const item = shot("one", "open", [0, 3000]);
    item.overlays = [{ type: "caption", text: "active", start_ms: 500, end_ms: 1500 }];
    expect(activeCaptionText(item, 499)).toBe("");
    expect(activeCaptionText(item, 500)).toBe("active");
    expect(activeCaptionText(item, 1499)).toBe("active");
    expect(activeCaptionText(item, 1500)).toBe("");
  });

  it("blocks splitting at clip boundaries, outside clips, or too close to an edge", () => {
    const base = plan([shot("one", "open", [1000, 3500])]);
    for (const outputMS of [0, 2500, 3000]) {
      const result = splitShotAtOutputMS({ plan: base, outputMS, fps: 30, newShotID: "one-right" });
      expect(result.changed).toBe(false);
      expect(result.blockedReason).toBe("播放头不在视频片段内。");
    }

    const nearEdge = splitShotAtOutputMS({ plan: base, outputMS: 300, fps: 30, newShotID: "one-right" });
    expect(nearEdge.changed).toBe(false);
    expect(nearEdge.blockedReason).toBe("播放头距离片段边缘过近，无法分割。");
  });

  it("splits only the selected audio track without changing video shots", () => {
    const base = plan([shot("one", "open", [1000, 3500]), shot("two", "create", [3500, 6000])]);
    const result = splitAudioAtOutputMS({ plan: base, outputMS: 1500, totalDurationMS: 5000, fps: 30 });
    expect(result.changed).toBe(true);
    expect(result.plan.shots).toEqual(base.shots);
    expect(result.plan.audio?.split_points_ms).toEqual([1500]);
    expect(buildAudioSegments(result.plan, 5000)).toEqual([
      { id: "audio_0_1500", index: 0, startMS: 0, endMS: 1500, durationMS: 1500, mode: "source", volumePercent: 100 },
      { id: "audio_1500_5000", index: 1, startMS: 1500, endMS: 5000, durationMS: 3500, mode: "source", volumePercent: 100 },
    ]);
  });

  it("applies per-segment mute and volume without changing the video timeline", () => {
    const split = splitAudioAtOutputMS({ plan: plan([shot("one", "", [0, 5000])]), outputMS: 1500, totalDurationMS: 5000, fps: 30 });
    const muted = patchAudioSegmentInPlan({ plan: split.plan, segmentID: "audio_1500_5000", totalDurationMS: 5000, patch: { mode: "mute", volumePercent: 60 } });
    expect(muted.changed).toBe(true);
    expect(muted.plan.shots).toEqual(split.plan.shots);
    expect(muted.plan.audio?.segment_settings).toEqual([{ start_ms: 1500, end_ms: 5000, mode: "mute", volume_percent: 60 }]);
    expect(buildAudioSegments(muted.plan, 5000)[1]).toMatchObject({ mode: "mute", volumePercent: 60 });

    const restored = patchAudioSegmentInPlan({ plan: muted.plan, segmentID: "audio_1500_5000", totalDurationMS: 5000, patch: { mode: "source", volumePercent: 100 } });
    expect(restored.plan.audio?.segment_settings).toEqual([]);
  });

  it("maps per-segment settings to browser-safe source preview audio", () => {
    const base = {
      ...plan([shot("one", "", [0, 5000])]),
      audio: {
        mode: "source" as const,
        volume_percent: 80,
        split_points_ms: [1500, 3000],
        segment_settings: [
          { start_ms: 1500, end_ms: 3000, mode: "mute" as const, volume_percent: 60 },
          { start_ms: 3000, end_ms: 5000, mode: "source" as const, volume_percent: 150 },
        ],
      },
    };

    expect(audioPreviewState(base, 5000, 1000)).toEqual({ muted: false, volume: 0.8, requestedVolumePercent: 80, limited: false });
    expect(audioPreviewState(base, 5000, 1500)).toEqual({ muted: true, volume: 0.6, requestedVolumePercent: 60, limited: false });
    expect(audioPreviewState(base, 5000, 3500)).toEqual({ muted: false, volume: 1, requestedVolumePercent: 150, limited: true });
    expect(audioPreviewState(base, 5000, 5000)).toEqual({ muted: false, volume: 1, requestedVolumePercent: 150, limited: true });
  });

  it("applies a reviewed mute candidate without changing video timing or unrelated audio settings", () => {
    const base = {
      ...plan([shot("one", "", [0, 5000])]),
      audio: { mode: "source" as const, volume_percent: 100, split_points_ms: [1000], segment_settings: [{ start_ms: 0, end_ms: 1000, mode: "source" as const, volume_percent: 60 }] },
    };
    const result = muteAudioRangeInPlan({ plan: base, startMS: 1500, endMS: 3000, totalDurationMS: 5000 });
    expect(result.changed).toBe(true);
    expect(result.plan.shots).toEqual(base.shots);
    expect(result.plan.audio?.split_points_ms).toEqual([1000, 1500, 3000]);
    expect(result.plan.audio?.segment_settings).toEqual([
      { start_ms: 0, end_ms: 1000, mode: "source", volume_percent: 60 },
      { start_ms: 1500, end_ms: 3000, mode: "mute", volume_percent: 100 },
    ]);
  });

  it("inherits settings when splitting and merges an audio boundary deterministically", () => {
    const base = { ...plan([shot("one", "", [0, 5000])]), audio: { mode: "source" as const, volume_percent: 100, split_points_ms: [1500], segment_settings: [{ start_ms: 1500, end_ms: 5000, mode: "mute" as const, volume_percent: 80 }] } };
    const split = splitAudioAtOutputMS({ plan: base, outputMS: 3000, totalDurationMS: 5000, fps: 30 });
    expect(buildAudioSegments(split.plan, 5000).slice(1).map((segment) => segment.mode)).toEqual(["mute", "mute"]);
    const merged = mergeAudioSegmentInPlan({ plan: split.plan, segmentID: "audio_3000_5000", totalDurationMS: 5000 });
    expect(merged.plan.audio?.split_points_ms).toEqual([1500]);
    expect(merged.plan.audio?.segment_settings).toEqual([{ start_ms: 1500, end_ms: 5000, mode: "mute", volume_percent: 80 }]);
  });

  it("blocks duplicate and near-edge audio splits", () => {
    const base = { ...plan([shot("one", "open", [0, 5000])]), audio: { mode: "source" as const, volume_percent: 100, split_points_ms: [1500] } };
    expect(splitAudioAtOutputMS({ plan: base, outputMS: 1500, totalDurationMS: 5000, fps: 30 }).blockedReason).toBe("播放头不在音频片段内。");
    expect(splitAudioAtOutputMS({ plan: base, outputMS: 1750, totalDurationMS: 5000, fps: 30 }).blockedReason).toBe("播放头距离音频片段边缘过近，无法分割。");
  });

  it("keeps audio split points aligned after video duration changes", () => {
    const base = { ...plan([shot("one", "open", [0, 2000]), shot("two", "create", [2000, 5000])]), audio: { mode: "source" as const, volume_percent: 100, split_points_ms: [1000, 3000, 4500] } };
    const trimmed = trimShotInPlan({ plan: base, shotID: "one", edge: "end", valueMS: 1500, fps: 30 });
    expect(trimmed.plan.audio?.split_points_ms).toEqual([1000, 2500, 4000]);
    const trimmedStart = trimShotInPlan({ plan: base, shotID: "one", edge: "start", valueMS: 500, fps: 30 });
    expect(trimmedStart.plan.audio?.split_points_ms).toEqual([500, 2500, 4000]);
    const removed = deleteShotInPlan(trimmed.plan, "one", steps);
    expect(removed.changed).toBe(false);

    const free = { ...plan([shot("one", "", [0, 1500]), shot("two", "", [2000, 5000])]), audio: trimmed.plan.audio! };
    expect(deleteShotInPlan(free, "one", []).plan.audio?.split_points_ms).toEqual([1000, 2500]);
  });

  it("blocks deleting any fragment that would leave a required evidence gap", () => {
    const splitEvidence = plan([shot("one-a", "open", [1000, 2000]), shot("one-b", "open", [2000, 3000]), shot("two", "create", [3000, 6000])]);
    const result = deleteShotInPlan(splitEvidence, "one-a", steps);
    expect(result.changed).toBe(false);
    expect(result.blockedReason).toContain("完整证据");
  });
});
