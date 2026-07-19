import type { EditorArtifact, EditorPlan, EditorShot, EditorTimelineStep } from "./editor";
import { frameToMilliseconds, millisecondsToFrame } from "./presentationComposition";

export type TrimEdge = "start" | "end";

export type TimelineSnapResult = {
  valueMS: number;
  snapped: boolean;
  snapPointMS?: number;
};

export type TimelineEditResult = {
  plan: EditorPlan;
  changed: boolean;
  blockedReason?: string;
};

export type SplitShotResult = TimelineEditResult & {
  leftShotID?: string;
  rightShotID?: string;
  sourceSplitMS?: number;
  outputSplitMS?: number;
};

export type AudioSegment = {
  id: string;
  index: number;
  startMS: number;
  endMS: number;
  durationMS: number;
  mode: "source" | "mute";
  volumePercent: number;
};

export type AudioPreviewState = {
  muted: boolean;
  volume: number;
  requestedVolumePercent: number;
  limited: boolean;
};

export function snapMilliseconds(valueMS: number, fps: number, snapPointsMS: number[] = [], thresholdMS = 120): TimelineSnapResult {
  const frameMS = frameToMilliseconds(millisecondsToFrame(valueMS, fps), fps);
  let explicitPoint: number | undefined;
  let explicitDistance = Number.POSITIVE_INFINITY;
  for (const point of snapPointsMS) {
    const pointDistance = Math.abs(valueMS - point);
    if (pointDistance <= thresholdMS && pointDistance < explicitDistance) {
      explicitPoint = point;
      explicitDistance = pointDistance;
    }
  }
  if (explicitPoint !== undefined) return { valueMS: Math.max(0, explicitPoint), snapped: true, snapPointMS: explicitPoint };
  return { valueMS: Math.max(0, frameMS), snapped: frameMS !== valueMS };
}

export function trimShotInPlan(args: {
  plan: EditorPlan;
  shotID: string;
  edge: TrimEdge;
  valueMS: number;
  fps: number;
  sourceDurationMS?: number;
  snapPointsMS?: number[];
  minDurationMS?: number;
}): TimelineEditResult {
  const index = args.plan.shots.findIndex((shot) => shot.id === args.shotID);
  const shot = args.plan.shots[index];
  if (!shot?.source_time_range_ms) return { plan: args.plan, changed: false, blockedReason: "片段没有可裁剪的源时间范围。" };
  const minDurationMS = Math.max(frameToMilliseconds(1, args.fps), args.minDurationMS ?? 500);
  const [baseStart, baseEnd] = shot.source_time_range_ms;
  const snapped = snapMilliseconds(args.valueMS, args.fps, args.snapPointsMS ?? [], Math.max(80, frameToMilliseconds(4, args.fps)));
  const sourceDurationMS = Math.max(baseEnd, args.sourceDurationMS ?? baseEnd);
  const nextStart = args.edge === "start" ? Math.min(baseEnd - minDurationMS, Math.max(0, snapped.valueMS)) : baseStart;
  const nextEnd = args.edge === "end" ? Math.max(baseStart + minDurationMS, Math.min(sourceDurationMS, snapped.valueMS)) : baseEnd;
  const nextRange: [number, number] = [Math.round(nextStart), Math.round(nextEnd)];
  if (nextRange[0] === baseStart && nextRange[1] === baseEnd) return { plan: args.plan, changed: false };

  const durationMS = nextRange[1] - nextRange[0];
  const removedFromStartMS = nextRange[0] - baseStart;
  const overlays = trimOverlays(shot, removedFromStartMS, durationMS);
  const shots = [...args.plan.shots];
  shots[index] = withShotRange(shot, nextRange, overlays);
  const nextPlan = remapAudioForDurationChange(args.plan, { ...args.plan, shots }, index, baseEnd - baseStart, durationMS, args.edge);
  return { plan: nextPlan, changed: true };
}

export function splitShotAtOutputMS(args: {
  plan: EditorPlan;
  outputMS: number;
  fps: number;
  newShotID: string;
  minDurationMS?: number;
}): SplitShotResult {
  let outputCursorMS = 0;
  let shotIndex = -1;
  let shotOutputStartMS = 0;

  for (const [index, shot] of args.plan.shots.entries()) {
    const range = shot.source_time_range_ms;
    const durationMS = range ? Math.max(0, range[1] - range[0]) : 0;
    const outputEndMS = outputCursorMS + durationMS;
    if (args.outputMS > outputCursorMS && args.outputMS < outputEndMS) {
      shotIndex = index;
      shotOutputStartMS = outputCursorMS;
      break;
    }
    outputCursorMS = outputEndMS;
  }

  const shot = args.plan.shots[shotIndex];
  if (!shot?.source_time_range_ms) {
    return { plan: args.plan, changed: false, blockedReason: "播放头不在视频片段内。" };
  }

  const [sourceStartMS, sourceEndMS] = shot.source_time_range_ms;
  const sourceSplitMS = Math.round(snapMilliseconds(sourceStartMS + args.outputMS - shotOutputStartMS, args.fps).valueMS);
  const minDurationMS = Math.max(frameToMilliseconds(1, args.fps), args.minDurationMS ?? 500);
  if (sourceSplitMS - sourceStartMS < minDurationMS || sourceEndMS - sourceSplitMS < minDurationMS) {
    return { plan: args.plan, changed: false, blockedReason: "播放头距离片段边缘过近，无法分割。" };
  }

  const splitOffsetMS = sourceSplitMS - sourceStartMS;
  const sourceDurationMS = sourceEndMS - sourceStartMS;
  const { left: leftOverlays, right: rightOverlays } = splitOverlays(shot, splitOffsetMS, sourceDurationMS);
  const left = withShotRange(shot, [sourceStartMS, sourceSplitMS], leftOverlays);
  const right: EditorShot = {
    ...withShotRange(shot, [sourceSplitMS, sourceEndMS], rightOverlays),
    id: args.newShotID,
    purpose: `${shot.purpose}（后半段）`,
  };
  const shots = [...args.plan.shots];
  shots.splice(shotIndex, 1, left, right);
  return {
    plan: normalizeAudioSplitPoints({ ...args.plan, shots }),
    changed: true,
    leftShotID: left.id,
    rightShotID: right.id,
    sourceSplitMS,
    outputSplitMS: shotOutputStartMS + sourceSplitMS - sourceStartMS,
  };
}

export function activeCaptionText(shot: EditorShot | undefined, relativeMS: number): string {
  const caption = shot?.overlays?.find((overlay) => {
    if (overlay.type !== "caption") return false;
    const startMS = overlay.start_ms ?? 0;
    const endMS = overlay.end_ms ?? Number.POSITIVE_INFINITY;
    return relativeMS >= startMS && relativeMS < endMS;
  });
  return caption?.text ?? "";
}

export function buildAudioSegments(plan: EditorPlan, totalDurationMS: number): AudioSegment[] {
  const boundaries = [0, ...validAudioSplitPoints(plan, totalDurationMS), totalDurationMS];
  return boundaries.slice(0, -1).map((startMS, index) => {
    const endMS = boundaries[index + 1] ?? totalDurationMS;
    const settings = plan.audio?.segment_settings?.find((segment) => segment.start_ms === startMS && segment.end_ms === endMS);
    return {
      id: `audio_${startMS}_${endMS}`,
      index,
      startMS,
      endMS,
      durationMS: endMS - startMS,
      mode: settings?.mode ?? plan.audio?.mode ?? "source",
      volumePercent: settings?.volume_percent ?? plan.audio?.volume_percent ?? 100,
    };
  }).filter((segment) => segment.durationMS > 0);
}

export function audioPreviewState(plan: EditorPlan, totalDurationMS: number, playheadMS: number): AudioPreviewState {
  const segments = buildAudioSegments(plan, totalDurationMS);
  const boundedMS = Math.max(0, Math.min(totalDurationMS, playheadMS));
  const active = segments.find((segment, index) => boundedMS >= segment.startMS && (boundedMS < segment.endMS || (index === segments.length - 1 && boundedMS === segment.endMS)));
  const requestedVolumePercent = Math.max(0, active?.volumePercent ?? plan.audio?.volume_percent ?? 100);
  return {
    muted: (active?.mode ?? plan.audio?.mode ?? "source") === "mute",
    volume: Math.min(1, requestedVolumePercent / 100),
    requestedVolumePercent,
    limited: requestedVolumePercent > 100,
  };
}

export function splitAudioAtOutputMS(args: { plan: EditorPlan; outputMS: number; totalDurationMS: number; fps: number; minDurationMS?: number }): TimelineEditResult {
  const snappedMS = Math.round(snapMilliseconds(args.outputMS, args.fps).valueMS);
  const segment = buildAudioSegments(args.plan, args.totalDurationMS).find((item) => snappedMS > item.startMS && snappedMS < item.endMS);
  if (!segment) return { plan: args.plan, changed: false, blockedReason: "播放头不在音频片段内。" };
  const minDurationMS = Math.max(frameToMilliseconds(1, args.fps), args.minDurationMS ?? 500);
  if (snappedMS - segment.startMS < minDurationMS || segment.endMS - snappedMS < minDurationMS) {
    return { plan: args.plan, changed: false, blockedReason: "播放头距离音频片段边缘过近，无法分割。" };
  }
  const previousSegments = buildAudioSegments(args.plan, args.totalDurationMS);
  const audio = {
    mode: "source" as const,
    volume_percent: 100,
    ...(args.plan.audio ?? {}),
    split_points_ms: [...validAudioSplitPoints(args.plan, args.totalDurationMS), snappedMS].sort((left, right) => left - right),
  };
  const splitPlan = { ...args.plan, audio };
  const nextSegments = buildAudioSegments(splitPlan, args.totalDurationMS);
  audio.segment_settings = compactAudioSegmentSettings(nextSegments.map((item) => {
    const previous = previousSegments.find((candidate) => item.startMS >= candidate.startMS && item.endMS <= candidate.endMS);
    return { ...item, mode: previous?.mode ?? item.mode, volumePercent: previous?.volumePercent ?? item.volumePercent };
  }), audio.mode, audio.volume_percent);
  return { plan: { ...args.plan, audio }, changed: true };
}

export function patchAudioSegmentInPlan(args: { plan: EditorPlan; segmentID: string; totalDurationMS: number; patch: { mode?: "source" | "mute"; volumePercent?: number } }): TimelineEditResult {
  const segments = buildAudioSegments(args.plan, args.totalDurationMS);
  const index = segments.findIndex((segment) => segment.id === args.segmentID);
  const segment = segments[index];
  if (!segment) return { plan: args.plan, changed: false, blockedReason: "未找到音频片段。" };
  const mode = args.patch.mode ?? segment.mode;
  const volumePercent = Math.round(args.patch.volumePercent ?? segment.volumePercent);
  if (!Number.isFinite(volumePercent) || volumePercent < 0 || volumePercent > 200) {
    return { plan: args.plan, changed: false, blockedReason: "音频片段音量必须在 0% 到 200% 之间。" };
  }
  const updated = segments.map((item) => item.id === segment.id ? { ...item, mode, volumePercent } : item);
  const base = { mode: "source" as const, volume_percent: 100, ...(args.plan.audio ?? {}) };
  const audio = { ...base, segment_settings: compactAudioSegmentSettings(updated, base.mode, base.volume_percent) };
  return { plan: { ...args.plan, audio }, changed: mode !== segment.mode || volumePercent !== segment.volumePercent };
}

export function muteAudioRangeInPlan(args: { plan: EditorPlan; startMS: number; endMS: number; totalDurationMS: number }): TimelineEditResult {
  const startMS = Math.max(0, Math.round(args.startMS));
  const endMS = Math.min(args.totalDurationMS, Math.round(args.endMS));
  if (endMS <= startMS) return { plan: args.plan, changed: false, blockedReason: "自动静音候选范围无效。" };

  const previousSegments = buildAudioSegments(args.plan, args.totalDurationMS);
  const splitPoints = [...validAudioSplitPoints(args.plan, args.totalDurationMS), startMS, endMS]
    .filter((point) => point > 0 && point < args.totalDurationMS);
  const base = { mode: "source" as const, volume_percent: 100, ...(args.plan.audio ?? {}), split_points_ms: [...new Set(splitPoints)].sort((left, right) => left - right) };
  const nextSegments = buildAudioSegments({ ...args.plan, audio: base }, args.totalDurationMS).map((segment) => {
    const previous = previousSegments.find((candidate) => segment.startMS >= candidate.startMS && segment.startMS < candidate.endMS);
    const inherited = previous ? { ...segment, mode: previous.mode, volumePercent: previous.volumePercent } : segment;
    return segment.startMS >= startMS && segment.endMS <= endMS ? { ...inherited, mode: "mute" as const } : inherited;
  });
  const audio = { ...base, segment_settings: compactAudioSegmentSettings(nextSegments, base.mode, base.volume_percent) };
  const changed = nextSegments.some((segment) => segment.startMS >= startMS && segment.endMS <= endMS && previousSegments.find((previous) => segment.startMS >= previous.startMS && segment.startMS < previous.endMS)?.mode !== "mute");
  return { plan: { ...args.plan, audio }, changed };
}

export function mergeAudioSegmentInPlan(args: { plan: EditorPlan; segmentID: string; totalDurationMS: number }): TimelineEditResult {
  const segments = buildAudioSegments(args.plan, args.totalDurationMS);
  const index = segments.findIndex((segment) => segment.id === args.segmentID);
  if (index < 0) return { plan: args.plan, changed: false, blockedReason: "未找到音频片段。" };
  if (segments.length <= 1) return { plan: args.plan, changed: false, blockedReason: "音频轨至少保留一个片段。" };
  const boundaryToRemove = index > 0 ? segments[index]?.startMS : segments[index]?.endMS;
  if (!boundaryToRemove) return { plan: args.plan, changed: false, blockedReason: "音频片段没有可合并的边界。" };
  const base = { mode: "source" as const, volume_percent: 100, ...(args.plan.audio ?? {}) };
  const split_points_ms = validAudioSplitPoints(args.plan, args.totalDurationMS).filter((point) => point !== boundaryToRemove);
  const planWithoutBoundary = { ...args.plan, audio: { ...base, split_points_ms } };
  const nextSegments = buildAudioSegments(planWithoutBoundary, args.totalDurationMS);
  const segment_settings = compactAudioSegmentSettings(nextSegments.map((item) => {
    const inherited = segments.find((candidate) => item.startMS >= candidate.startMS && item.startMS < candidate.endMS) ?? segments[0]!;
    return { ...item, mode: inherited.mode, volumePercent: inherited.volumePercent };
  }), base.mode, base.volume_percent);
  return { plan: { ...args.plan, audio: { ...base, split_points_ms, segment_settings } }, changed: true };
}

export function reorderShotInPlan(plan: EditorPlan, shotID: string, targetIndex: number, steps: EditorTimelineStep[]): TimelineEditResult {
  const sourceIndex = plan.shots.findIndex((shot) => shot.id === shotID);
  if (sourceIndex < 0) return { plan, changed: false, blockedReason: "未找到待移动片段。" };
  const boundedTarget = Math.max(0, Math.min(plan.shots.length - 1, targetIndex));
  if (boundedTarget === sourceIndex) return { plan, changed: false };
  const shots = [...plan.shots];
  const [moving] = shots.splice(sourceIndex, 1);
  if (!moving) return { plan, changed: false };
  shots.splice(boundedTarget, 0, moving);
  if (!preservesRequiredStepOrder(shots, steps)) return { plan, changed: false, blockedReason: "必需业务步骤顺序已锁定，不能移动到该位置。" };
  return { plan: normalizeAudioSplitPoints({ ...plan, shots }), changed: true };
}

export function deleteShotInPlan(plan: EditorPlan, shotID: string, steps: EditorTimelineStep[]): TimelineEditResult {
  const index = plan.shots.findIndex((shot) => shot.id === shotID);
  const shot = plan.shots[index];
  if (!shot) return { plan, changed: false, blockedReason: "未找到待删除片段。" };
  const shots = plan.shots.filter((candidate) => candidate.id !== shotID);
  const nextPlan = remapAudioForShotRemoval(plan, { ...plan, shots }, index, shotDurationMS(shot));
  const lostRequiredStep = requiredStepCoverage(nextPlan, steps).find((coverage) => !coverage.covered);
  if (lostRequiredStep) return { plan, changed: false, blockedReason: `删除后会破坏必需步骤 ${lostRequiredStep.stepID} 的完整证据，操作已阻止。` };
  return { plan: nextPlan, changed: true };
}

export function requiredStepCoverage(plan: EditorPlan, steps: EditorTimelineStep[]): Array<{ stepID: string; covered: boolean; requiredRange: [number, number]; coveredRanges: Array<[number, number]> }> {
  return steps.filter((step) => step.required && step.status !== "failed").map((step) => {
    const ranges = plan.shots
      .filter((shot) => shot.source_step_id === step.step_id && shot.source_time_range_ms)
      .map((shot) => shot.source_time_range_ms as [number, number])
      .sort((left, right) => left[0] - right[0]);
    let cursor = step.start_ms;
    for (const range of ranges) {
      if (range[1] <= cursor || range[0] > cursor) continue;
      cursor = Math.max(cursor, range[1]);
      if (cursor >= step.end_ms) break;
    }
    return { stepID: step.step_id, covered: cursor >= step.end_ms, requiredRange: [step.start_ms, step.end_ms], coveredRanges: ranges };
  });
}

export function sourceSnapPoints(shot: EditorShot, step: EditorTimelineStep | undefined, artifact: EditorArtifact | undefined): number[] {
  const points = new Set<number>([0]);
  if (artifact?.duration_ms) points.add(artifact.duration_ms);
  if (step) {
    points.add(step.start_ms);
    points.add(step.end_ms);
  }
  if (shot.source_time_range_ms) {
    points.add(shot.source_time_range_ms[0]);
    points.add(shot.source_time_range_ms[1]);
  }
  return [...points].sort((left, right) => left - right);
}

export function targetIndexForOutputMS(plan: EditorPlan, outputMS: number): number {
  let cursor = 0;
  for (const [index, shot] of plan.shots.entries()) {
    const range = shot.source_time_range_ms;
    const durationMS = range ? Math.max(0, range[1] - range[0]) : 0;
    if (outputMS < cursor + durationMS / 2) return index;
    cursor += durationMS;
  }
  return Math.max(0, plan.shots.length - 1);
}

function preservesRequiredStepOrder(shots: EditorShot[], steps: EditorTimelineStep[]): boolean {
  const order = new Map(steps.filter((step) => step.required).map((step) => [step.step_id, step.order]));
  let last = -1;
  for (const shot of shots) {
    if (!shot.source_step_id || !order.has(shot.source_step_id)) continue;
    const current = order.get(shot.source_step_id)!;
    if (current < last) return false;
    last = current;
  }
  return true;
}

function splitOverlays(shot: EditorShot, splitOffsetMS: number, sourceDurationMS: number): { left: NonNullable<EditorShot["overlays"]>; right: NonNullable<EditorShot["overlays"]> } {
  const left: NonNullable<EditorShot["overlays"]> = [];
  const right: NonNullable<EditorShot["overlays"]> = [];

  for (const overlay of shot.overlays ?? []) {
    if (overlay.start_ms === undefined && overlay.end_ms === undefined) {
      left.push({ ...overlay });
      right.push({ ...overlay });
      continue;
    }

    const startMS = Math.max(0, Math.min(sourceDurationMS, overlay.start_ms ?? 0));
    const endMS = Math.max(startMS, Math.min(sourceDurationMS, overlay.end_ms ?? sourceDurationMS));
    if (endMS <= splitOffsetMS) {
      if (endMS > startMS) left.push({ ...overlay, start_ms: startMS, end_ms: endMS });
      continue;
    }
    if (startMS >= splitOffsetMS) {
      if (endMS > startMS) right.push({ ...overlay, start_ms: startMS - splitOffsetMS, end_ms: endMS - splitOffsetMS });
      continue;
    }

    left.push({ ...overlay, start_ms: startMS, end_ms: splitOffsetMS });
    right.push({ ...overlay, start_ms: 0, end_ms: endMS - splitOffsetMS });
  }
  return { left, right };
}

function trimOverlays(shot: EditorShot, removedFromStartMS: number, durationMS: number): NonNullable<EditorShot["overlays"]> {
  const overlays: NonNullable<EditorShot["overlays"]> = [];
  for (const overlay of shot.overlays ?? []) {
    if (overlay.start_ms === undefined && overlay.end_ms === undefined) {
      overlays.push(overlay);
      continue;
    }
    const originalStartMS = overlay.start_ms ?? 0;
    const originalEndMS = overlay.end_ms ?? durationMS + removedFromStartMS;
    const startMS = Math.max(0, originalStartMS - removedFromStartMS);
    const endMS = Math.min(durationMS, originalEndMS - removedFromStartMS);
    if (endMS > startMS) overlays.push({ ...overlay, start_ms: startMS, end_ms: endMS });
  }
  return overlays;
}

function validAudioSplitPoints(plan: EditorPlan, totalDurationMS: number): number[] {
  return [...new Set((plan.audio?.split_points_ms ?? []).filter((point) => Number.isInteger(point) && point > 0 && point < totalDurationMS))].sort((left, right) => left - right);
}

function normalizeAudioSplitPoints(plan: EditorPlan): EditorPlan {
  if (!plan.audio?.split_points_ms?.length && !plan.audio?.segment_settings?.length) return plan;
  const totalDurationMS = plan.shots.reduce((total, shot) => total + (shot.source_time_range_ms ? Math.max(0, shot.source_time_range_ms[1] - shot.source_time_range_ms[0]) : 0), 0);
  const audio = { ...(plan.audio ?? { mode: "source" as const, volume_percent: 100 }), split_points_ms: validAudioSplitPoints(plan, totalDurationMS) };
  const segments = buildAudioSegments({ ...plan, audio }, totalDurationMS);
  return { ...plan, audio: { ...audio, segment_settings: compactAudioSegmentSettings(segments, audio.mode, audio.volume_percent) } };
}

function remapAudioForDurationChange(basePlan: EditorPlan, nextPlan: EditorPlan, shotIndex: number, oldDurationMS: number, newDurationMS: number, edge: TrimEdge): EditorPlan {
  if (!basePlan.audio?.split_points_ms?.length && !basePlan.audio?.segment_settings?.length) return nextPlan;
  const baseDurationMS = basePlan.shots.reduce((total, shot) => total + shotDurationMS(shot), 0);
  const previousSegments = buildAudioSegments(basePlan, baseDurationMS);
  const shotStartMS = basePlan.shots.slice(0, shotIndex).reduce((total, shot) => total + shotDurationMS(shot), 0);
  const oldEndMS = shotStartMS + oldDurationMS;
  const newEndMS = shotStartMS + newDurationMS;
  const deltaMS = newDurationMS - oldDurationMS;
  const removedFromStartMS = edge === "start" ? oldDurationMS - newDurationMS : 0;
  const splitPoints = (basePlan.audio?.split_points_ms ?? []).flatMap((point) => {
    if (point <= shotStartMS) return [point];
    if (point < oldEndMS) {
      if (edge === "start") return point > shotStartMS + removedFromStartMS ? [point - removedFromStartMS] : [];
      return point < newEndMS ? [point] : [];
    }
    return [point + deltaMS];
  });
  const audio = { ...basePlan.audio, split_points_ms: splitPoints };
  const remapped = normalizeAudioSplitPoints({ ...nextPlan, audio });
  return inheritAudioSettingsByMappedTime(remapped, previousSegments, (timeMS) => timeMS < shotStartMS ? timeMS : timeMS < newEndMS ? timeMS + removedFromStartMS : timeMS - deltaMS);
}

function remapAudioForShotRemoval(basePlan: EditorPlan, nextPlan: EditorPlan, shotIndex: number, removedDurationMS: number): EditorPlan {
  if (!basePlan.audio?.split_points_ms?.length && !basePlan.audio?.segment_settings?.length) return nextPlan;
  const baseDurationMS = basePlan.shots.reduce((total, shot) => total + shotDurationMS(shot), 0);
  const previousSegments = buildAudioSegments(basePlan, baseDurationMS);
  const removedStartMS = basePlan.shots.slice(0, shotIndex).reduce((total, shot) => total + shotDurationMS(shot), 0);
  const removedEndMS = removedStartMS + removedDurationMS;
  const splitPoints = (basePlan.audio?.split_points_ms ?? []).flatMap((point) => {
    if (point <= removedStartMS) return [point];
    if (point < removedEndMS) return [];
    return [point - removedDurationMS];
  });
  const remapped = normalizeAudioSplitPoints({ ...nextPlan, audio: { ...basePlan.audio, split_points_ms: splitPoints } });
  return inheritAudioSettingsByMappedTime(remapped, previousSegments, (timeMS) => timeMS < removedStartMS ? timeMS : timeMS + removedDurationMS);
}

function shotDurationMS(shot: EditorShot): number {
  return shot.source_time_range_ms ? Math.max(0, shot.source_time_range_ms[1] - shot.source_time_range_ms[0]) : 0;
}

function operationsForRange(shot: EditorShot, range: [number, number]): EditorShot["operations"] {
  return shot.operations?.map((operation) => operation.type === "trim" ? { ...operation, start_ms: range[0], end_ms: range[1] } : operation);
}

function withShotRange(shot: EditorShot, range: [number, number], overlays: NonNullable<EditorShot["overlays"]>): EditorShot {
  const next: EditorShot = { ...shot, source_time_range_ms: range, overlays };
  if (shot.operations) next.operations = operationsForRange(shot, range) ?? [];
  return next;
}

function compactAudioSegmentSettings(segments: AudioSegment[], baseMode: "source" | "mute", baseVolumePercent: number): NonNullable<NonNullable<EditorPlan["audio"]>["segment_settings"]> {
  return segments.filter((segment) => segment.mode !== baseMode || segment.volumePercent !== baseVolumePercent).map((segment) => ({
    start_ms: segment.startMS,
    end_ms: segment.endMS,
    mode: segment.mode,
    volume_percent: segment.volumePercent,
  }));
}

function inheritAudioSettingsByMappedTime(plan: EditorPlan, previousSegments: AudioSegment[], toPreviousTime: (timeMS: number) => number): EditorPlan {
  const totalDurationMS = plan.shots.reduce((total, shot) => total + shotDurationMS(shot), 0);
  const segments = buildAudioSegments(plan, totalDurationMS).map((segment) => {
    const previousTimeMS = toPreviousTime(segment.startMS + Math.min(1, Math.max(0, segment.durationMS - 1)));
    const previous = previousSegments.find((candidate) => previousTimeMS >= candidate.startMS && previousTimeMS < candidate.endMS);
    return previous ? { ...segment, mode: previous.mode, volumePercent: previous.volumePercent } : segment;
  });
  const base = plan.audio ?? { mode: "source" as const, volume_percent: 100 };
  return { ...plan, audio: { ...base, segment_settings: compactAudioSegmentSettings(segments, base.mode, base.volume_percent) } };
}
