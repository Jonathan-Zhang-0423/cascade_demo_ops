import type { EditorArtifact, EditorPlan, EditorSession, EditorShot } from "./editor";

export const PresentationCompositionSchemaVersion = "demoops.presentation_composition.v1";

export type EditorCapability = {
  preview: "source" | "composition" | "none";
  finalRenderer: boolean;
  enabled: boolean;
};

export const editorCapabilities = {
  trim: { preview: "source", finalRenderer: true, enabled: true },
  concat: { preview: "source", finalRenderer: true, enabled: true },
  caption: { preview: "composition", finalRenderer: true, enabled: true },
  sourceAudio: { preview: "source", finalRenderer: true, enabled: true },
  zoomPan: { preview: "none", finalRenderer: false, enabled: false },
  highlightBox: { preview: "none", finalRenderer: false, enabled: false },
  cursorHighlight: { preview: "none", finalRenderer: false, enabled: false },
  blurRegion: { preview: "none", finalRenderer: false, enabled: false },
  transition: { preview: "none", finalRenderer: false, enabled: false },
} as const satisfies Record<string, EditorCapability>;

export function finalRendererSupports(type: string): boolean {
  const capabilityKey = ({
    trim: "trim",
    caption: "caption",
    zoom_pan: "zoomPan",
    highlight_box: "highlightBox",
    highlight: "highlightBox",
    cursor_highlight: "cursorHighlight",
    blur_region: "blurRegion",
    transition: "transition",
  } as Record<string, keyof typeof editorCapabilities>)[type];
  return capabilityKey ? editorCapabilities[capabilityKey].finalRenderer : false;
}

export type PresentationSequenceKind = "video" | "audio" | "caption" | "callout";

export type PresentationSequence = {
  id: string;
  kind: PresentationSequenceKind;
  fromFrame: number;
  durationInFrames: number;
  layer: number;
  sourceArtifactId?: string;
  sourceStepId?: string;
  sourceStartFrame?: number;
  sourceEndFrame?: number;
  sourceURI?: string;
  props: Record<string, unknown>;
};

export type PresentationComposition = {
  schemaVersion: typeof PresentationCompositionSchemaVersion;
  compositionId: string;
  planId: string;
  sessionId: string;
  fps: number;
  width: number;
  height: number;
  durationInFrames: number;
  sequences: PresentationSequence[];
};

export function millisecondsToFrame(valueMS: number, fps: number): number {
  if (!Number.isFinite(valueMS) || !Number.isFinite(fps) || fps <= 0) return 0;
  return Math.max(0, Math.round(valueMS * fps / 1000));
}

export function frameToMilliseconds(frame: number, fps: number): number {
  if (!Number.isFinite(frame) || !Number.isFinite(fps) || fps <= 0) return 0;
  return Math.max(0, Math.round(frame * 1000 / fps));
}

export function compilePresentationComposition(session: EditorSession, plan: EditorPlan = session.edit_plan): PresentationComposition {
  const fps = boundedInteger(session.final_profile.fps, 30, 1, 60);
  const width = boundedInteger(session.final_profile.width, 1920, 320, 3840);
  const height = boundedInteger(session.final_profile.height, 1080, 180, 2160);
  const artifacts = new Map(session.asset_catalog.artifacts.map((artifact) => [artifact.id, artifact]));
  const sequences: PresentationSequence[] = [];
  const audioSplitFrames = new Set((plan.audio?.split_points_ms ?? []).map((point) => millisecondsToFrame(point, fps)));
  let cursorFrame = 0;

  for (const shot of plan.shots) {
    const range = shot.source_time_range_ms;
    if (!range) continue;
    const durationInFrames = Math.max(1, millisecondsToFrame(range[1] - range[0], fps));
    const artifact = artifacts.get(shot.source_artifact_id);
    const common = sequenceSource(shot, artifact, fps);
    sequences.push({
      id: `video_${shot.id}`,
      kind: "video",
      fromFrame: cursorFrame,
      durationInFrames,
      layer: 0,
      ...common,
      props: {
        purpose: shot.purpose,
        operations: (shot.operations ?? []).map((operation) => operation.type),
      },
    });
    let audioCursorFrame = cursorFrame;
    const audioEndFrame = cursorFrame + durationInFrames;
    const audioBoundaries = [...audioSplitFrames].filter((frame) => frame > cursorFrame && frame < audioEndFrame).sort((left, right) => left - right);
    for (const [audioPartIndex, boundaryFrame] of [...audioBoundaries, audioEndFrame].entries()) {
      const partDurationInFrames = boundaryFrame - audioCursorFrame;
      const sourceOffsetFrames = audioCursorFrame - cursorFrame;
      const audioStartMS = frameToMilliseconds(audioCursorFrame, fps);
      const audioEndMS = frameToMilliseconds(boundaryFrame, fps);
      const segmentSettings = plan.audio?.segment_settings?.find((segment) => segment.start_ms === audioStartMS && segment.end_ms === audioEndMS);
      sequences.push({
        id: `audio_${shot.id}_${audioPartIndex}`,
        kind: "audio",
        fromFrame: audioCursorFrame,
        durationInFrames: partDurationInFrames,
        layer: 1,
        ...common,
        sourceStartFrame: (common.sourceStartFrame ?? 0) + sourceOffsetFrames,
        sourceEndFrame: (common.sourceStartFrame ?? 0) + sourceOffsetFrames + partDurationInFrames,
        props: {
          mode: segmentSettings?.mode ?? plan.audio?.mode ?? "source",
          volumePercent: segmentSettings?.volume_percent ?? plan.audio?.volume_percent ?? 100,
        },
      });
      audioCursorFrame = boundaryFrame;
    }
    for (const [overlayIndex, overlay] of (shot.overlays ?? []).entries()) {
      const overlayStartFrame = millisecondsToFrame(overlay.start_ms ?? 0, fps);
      const availableFrames = Math.max(1, durationInFrames - overlayStartFrame);
      const requestedFrames = millisecondsToFrame((overlay.end_ms ?? range[1] - range[0]) - (overlay.start_ms ?? 0), fps);
      sequences.push({
        id: `overlay_${shot.id}_${overlayIndex}`,
        kind: overlay.type === "caption" ? "caption" : "callout",
        fromFrame: cursorFrame + overlayStartFrame,
        durationInFrames: Math.max(1, Math.min(availableFrames, requestedFrames || availableFrames)),
        layer: overlay.type === "caption" ? 10 : 20,
        ...(shot.source_step_id ? { sourceStepId: shot.source_step_id } : {}),
        props: {
          type: overlay.type,
          text: overlay.text ?? "",
        },
      });
    }
    cursorFrame += durationInFrames;
  }

  return {
    schemaVersion: PresentationCompositionSchemaVersion,
    compositionId: `composition_${plan.plan_id}`,
    planId: plan.plan_id,
    sessionId: session.session_id,
    fps,
    width,
    height,
    durationInFrames: cursorFrame,
    sequences,
  };
}

function sequenceSource(shot: EditorShot, artifact: EditorArtifact | undefined, fps: number): Pick<PresentationSequence, "sourceArtifactId" | "sourceStepId" | "sourceStartFrame" | "sourceEndFrame" | "sourceURI"> {
  const range = shot.source_time_range_ms ?? [0, 0];
  return {
    sourceArtifactId: shot.source_artifact_id,
    ...(shot.source_step_id ? { sourceStepId: shot.source_step_id } : {}),
    sourceStartFrame: millisecondsToFrame(range[0], fps),
    sourceEndFrame: millisecondsToFrame(range[1], fps),
    ...(artifact?.uri ? { sourceURI: artifact.uri } : {}),
  };
}

function boundedInteger(value: number, fallback: number, min: number, max: number): number {
  if (!Number.isFinite(value)) return fallback;
  return Math.min(max, Math.max(min, Math.round(value)));
}
