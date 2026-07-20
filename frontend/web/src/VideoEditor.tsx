import { useEffect, useMemo, useRef, useState } from "react";
import { createEditorClient } from "./editor";
import type { EditorArtifact, EditorPlan, EditorSession, EditorShot, EditorTimelineStep, EditorValidationReport } from "./editor";
import { editorPlansEqual, emptyEditorHistory, recordEditorHistory, redoEditorHistory, undoEditorHistory } from "./editorHistory";
import { compilePresentationComposition, finalRendererSupports } from "./presentationComposition";
import { buildOutputWaveform, buildSilenceCandidates, waveformBarsForRange } from "./audioAnalysis";
import { activeCaptionText, audioPreviewState, buildAudioSegments, deleteShotInPlan, mergeAudioSegmentInPlan, muteAudioRangeInPlan, patchAudioSegmentInPlan, reorderShotInPlan, requiredStepCoverage, sourceSnapPoints, splitAudioAtOutputMS, splitShotAtOutputMS, targetIndexForOutputMS, trimShotInPlan } from "./timelineEditing";
import type { AudioEditCandidate } from "./audioAnalysis";
import type { EditorAudioAnalysis } from "./editor";
import "./editorWorkspace.css";

type SaveState = "saved" | "dirty" | "saving" | "error";
type ImportMode = "upload" | "path" | "result" | "empty";
type MediaTab = "media" | "captions" | "callouts" | "generated";
type PreviewMode = "source" | "rendered";
type SelectedTrack = "video" | "audio";
type TimelineTrack = "evidence" | "video" | "caption" | "audio";

type TimelineShot = {
  shot: EditorShot;
  index: number;
  startMS: number;
  endMS: number;
  durationMS: number;
};

type TimelineInteraction =
  | { kind: "trim"; shotID: string; edge: "start" | "end"; startClientX: number; startValueMS: number; basePlan: EditorPlan }
  | { kind: "move"; shotID: string; basePlan: EditorPlan }
  | { kind: "playhead" };

export type ExportCheck = {
  id: string;
  label: string;
  detail: string;
  tone: "pass" | "warning" | "error";
};

export function VideoEditor() {
  const client = useMemo(() => createEditorClient(), []);
  const [sessions, setSessions] = useState<EditorSession[]>([]);
  const [session, setSession] = useState<EditorSession>();
  const [draftPlan, setDraftPlan] = useState<EditorPlan>();
  const [history, setHistory] = useState(emptyEditorHistory);
  const [saveState, setSaveState] = useState<SaveState>("saved");
  const [selectedShotID, setSelectedShotID] = useState("");
  const [selectedTrack, setSelectedTrack] = useState<SelectedTrack>("video");
  const [selectedAudioSegmentID, setSelectedAudioSegmentID] = useState("");
  const [newName, setNewName] = useState("产品演示剪辑");
  const [sourcePath, setSourcePath] = useState("");
  const [resultPackagePath, setResultPackagePath] = useState("");
  const [resultRecordingPath, setResultRecordingPath] = useState("");
  const [busy, setBusy] = useState("");
  const [message, setMessage] = useState("");
  const [importOpen, setImportOpen] = useState(false);
  const [importMode, setImportMode] = useState<ImportMode>("upload");
  const [mediaTab, setMediaTab] = useState<MediaTab>("media");
  const [validationOpen, setValidationOpen] = useState(false);
  const [validationReport, setValidationReport] = useState<EditorValidationReport>();
  const [previewMode, setPreviewMode] = useState<PreviewMode>("source");
  const [playheadMS, setPlayheadMS] = useState(0);
  const [isPlaying, setIsPlaying] = useState(false);
  const [timelineScale, setTimelineScale] = useState(48);
  const [hiddenTracks, setHiddenTracks] = useState<Record<TimelineTrack, boolean>>({ evidence: false, video: false, caption: false, audio: false });
  const [timelineInteractionKind, setTimelineInteractionKind] = useState<TimelineInteraction["kind"]>();
  const [snapGuideMS, setSnapGuideMS] = useState<number>();
  const [pendingFile, setPendingFile] = useState<File>();
  const [audioAnalyses, setAudioAnalyses] = useState<Record<string, EditorAudioAnalysis>>({});
  const [audioAnalysisBusy, setAudioAnalysisBusy] = useState(false);
  const [dismissedAudioCandidateIDs, setDismissedAudioCandidateIDs] = useState<string[]>([]);
  const sessionRef = useRef<EditorSession>();
  const draftPlanRef = useRef<EditorPlan>();
  const videoRef = useRef<HTMLVideoElement>(null);
  const timelineScrollRef = useRef<HTMLDivElement>(null);
  const timelineContentRef = useRef<HTMLDivElement>(null);
  const saveInFlightRef = useRef(false);
  const editGroupRef = useRef<{ key: string; at: number }>();
  const pendingSeekRef = useRef<{ shotID: string; sourceSeconds: number; outputMS: number }>();
  const timelineInteractionRef = useRef<TimelineInteraction>();
  const continuePlaybackRef = useRef(false);

  useEffect(() => {
    sessionRef.current = session;
  }, [session]);

  useEffect(() => {
    draftPlanRef.current = draftPlan;
  }, [draftPlan]);

  useEffect(() => {
    void refreshSessions();
  }, []);

  useEffect(() => {
    const activeKind = session?.preview.status === "running" ? "preview" : session?.final_render.status === "running" ? "final" : "";
    if (!session || !activeKind) return;
    const sessionID = session.session_id;
    const timer = window.setInterval(async () => {
      const result = await client.getSession(sessionID);
      if (!result.ok || !result.data) return;
      const next = result.data;
      setSession(next);
      setSessions((current) => current.map((item) => (item.session_id === next.session_id ? next : item)));
      const state = activeKind === "preview" ? next.preview : next.final_render;
      if (state.status === "ready") {
        if (activeKind === "preview") setPreviewMode("rendered");
        setMessage(activeKind === "preview" ? "预览渲染完成。" : `成片已导出：${next.final_render.video_path ?? "本地输出目录"}`);
      } else if (state.status === "failed") {
        setMessage(`渲染失败：${state.error ?? "未知错误"}`);
      } else if (state.status === "cancelled") {
        setMessage("渲染已取消。");
      }
    }, 700);
    return () => window.clearInterval(timer);
  }, [client, session?.session_id, session?.preview.status, session?.final_render.status]);

  useEffect(() => {
    if (!session || !draftPlan || saveState !== "dirty" || session.status === "rendering" || timelineInteractionKind) return;
    const timer = window.setTimeout(() => void persistDraft(false), 900);
    return () => window.clearTimeout(timer);
  }, [draftPlan, saveState, session?.revision, session?.session_id, session?.status, timelineInteractionKind]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const editingText = target?.tagName === "INPUT" || target?.tagName === "TEXTAREA" || target?.tagName === "SELECT";
      if (event.key === "Escape") {
        cancelTimelineInteraction();
        setImportOpen(false);
        setValidationOpen(false);
      }
      if (event.code === "Space" && !editingText && session) {
        event.preventDefault();
        togglePlayback();
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s" && session) {
        event.preventDefault();
        void persistDraft(true);
        return;
      }
      if ((event.ctrlKey || event.metaKey) && !editingText && event.key.toLowerCase() === "z") {
        event.preventDefault();
        event.shiftKey ? redo() : undo();
      }
      if ((event.key === "Delete" || event.key === "Backspace") && !editingText && (selectedTrack === "audio" ? selectedAudioSegment : selectedShot)) {
        event.preventDefault();
        deleteSelected();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  });

  const selectedShot = draftPlan?.shots.find((shot) => shot.id === selectedShotID) ?? draftPlan?.shots[0];
  const selectedIndex = selectedShot ? draftPlan?.shots.findIndex((shot) => shot.id === selectedShot.id) ?? -1 : -1;
  const selectedStep = session?.asset_catalog.steps?.find((step) => step.step_id === selectedShot?.source_step_id);
  const selectedArtifact = session?.asset_catalog.artifacts.find((artifact) => artifact.id === selectedShot?.source_artifact_id);
  const timelineShots = useMemo(() => buildTimelineShots(draftPlan), [draftPlan]);
  const totalDurationMS = timelineShots.at(-1)?.endMS ?? 0;
  const audioSegments = useMemo(() => draftPlan ? buildAudioSegments(draftPlan, totalDurationMS) : [], [draftPlan, totalDurationMS]);
  const selectedAudioSegment = audioSegments.find((segment) => segment.id === selectedAudioSegmentID) ?? audioSegments[0];
  const presentationComposition = useMemo(() => session && draftPlan ? compilePresentationComposition(session, draftPlan) : undefined, [session, draftPlan]);
  const previewURL = session?.preview.status === "ready" && client.mode === "local" ? `${client.mediaURL(session.session_id, "preview")}?r=${session.preview.revision ?? session.revision}` : "";
  const sourceURL = session && selectedShot && client.mode === "local" ? client.mediaURL(session.session_id, "asset", selectedShot.source_artifact_id) : "";
  const mediaURL = previewMode === "rendered" && previewURL ? previewURL : sourceURL;
  const effectivePreviewMode: PreviewMode = previewMode === "rendered" && previewURL ? "rendered" : "source";
  const currentValidation = saveState === "saved" ? validationReport ?? session?.validation : undefined;
  const exportChecks = useMemo(() => buildExportChecks(session, draftPlan, currentValidation), [session, draftPlan, currentValidation]);
  const failedChecks = exportChecks.filter((check) => check.tone === "error");
  const passedChecks = exportChecks.filter((check) => check.tone === "pass");
  const timelineWidth = Math.max(840, Math.ceil(totalDurationMS / 1000) * timelineScale + 32);
  const selectedTimelineShot = timelineShots.find((item) => item.shot.id === selectedShot?.id);
  const sourcePreviewCaption = activeCaptionText(selectedShot, Math.max(0, playheadMS - (selectedTimelineShot?.startMS ?? 0)));
  const sourcePreviewAudio = useMemo(() => draftPlan ? audioPreviewState(draftPlan, totalDurationMS, playheadMS) : undefined, [draftPlan, totalDurationMS, playheadMS]);
  const outputWaveform = useMemo(() => draftPlan ? buildOutputWaveform(draftPlan, audioAnalyses) : [], [draftPlan, audioAnalyses]);
  const audioCandidates = useMemo(() => draftPlan ? buildSilenceCandidates(draftPlan, audioAnalyses).filter((candidate) => !dismissedAudioCandidateIDs.includes(candidate.id)) : [], [draftPlan, audioAnalyses, dismissedAudioCandidateIDs]);

  useEffect(() => {
    if (previewMode === "rendered" && !previewURL) setPreviewMode("source");
  }, [previewMode, previewURL]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    if (effectivePreviewMode === "source" && sourcePreviewAudio) {
      video.muted = sourcePreviewAudio.muted;
      video.volume = sourcePreviewAudio.volume;
      return;
    }
    // Rendered previews already contain the plan's audio processing.
    video.muted = false;
    video.volume = 1;
  }, [effectivePreviewMode, mediaURL, sourcePreviewAudio]);

  useEffect(() => {
    const pending = pendingSeekRef.current;
    if (!pending || pending.shotID !== selectedShot?.id || effectivePreviewMode !== "source") return;
    const video = videoRef.current;
    if (!video) return;
    const applySeek = () => {
      video.currentTime = pending.sourceSeconds;
      setPlayheadMS(pending.outputMS);
      pendingSeekRef.current = undefined;
      if (continuePlaybackRef.current) {
        continuePlaybackRef.current = false;
        void video.play();
      }
    };
    if (video.readyState >= 1) applySeek();
    else video.addEventListener("loadedmetadata", applySeek, { once: true });
  }, [selectedShot?.id, sourceURL, effectivePreviewMode]);

  useEffect(() => {
    if (!timelineInteractionKind) return;
    const onPointerMove = (event: PointerEvent) => updateTimelineInteraction(event.clientX);
    const onPointerUp = () => finishTimelineInteraction();
    window.addEventListener("pointermove", onPointerMove);
    window.addEventListener("pointerup", onPointerUp, { once: true });
    window.addEventListener("pointercancel", onPointerUp, { once: true });
    return () => {
      window.removeEventListener("pointermove", onPointerMove);
      window.removeEventListener("pointerup", onPointerUp);
      window.removeEventListener("pointercancel", onPointerUp);
    };
  }, [timelineInteractionKind, timelineScale, session?.session_id]);

  async function refreshSessions() {
    const result = await client.listSessions();
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "无法读取编辑项目");
      return;
    }
    setSessions(result.data);
    if (!sessionRef.current && result.data[0]) loadSession(result.data[0]);
  }

  function loadSession(next: EditorSession) {
    setSession(next);
    setDraftPlan(structuredClone(next.edit_plan));
    setHistory(emptyEditorHistory());
    setSaveState("saved");
    setValidationReport(next.validation);
    setPlayheadMS(0);
    setIsPlaying(false);
    editGroupRef.current = undefined;
    setSelectedShotID(next.edit_plan.shots[0]?.id ?? "");
    setSelectedTrack("video");
    setSelectedAudioSegmentID("");
    setHiddenTracks({ evidence: false, video: false, caption: false, audio: false });
    setAudioAnalyses({});
    setDismissedAudioCandidateIDs([]);
    setMessage("");
    if (next.preview.status === "ready") setPreviewMode("rendered");
    else setPreviewMode("source");
  }

  async function createSession() {
    setBusy("create");
    const result = await client.createSession(newName.trim() || "未命名演示", importMode === "path" && sourcePath.trim() ? sourcePath.trim() : undefined);
    setBusy("");
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "创建失败");
      return;
    }
    setSessions((current) => [result.data!, ...current.filter((item) => item.session_id !== result.data!.session_id)]);
    loadSession(result.data);
    setImportOpen(false);
    setSourcePath("");
    setMessage("编辑项目已创建。");
  }

  async function importAsset() {
    if (!sourcePath.trim()) return;
    if (!session) {
      await createSession();
      return;
    }
    setBusy("import");
    const result = await client.importAsset(session.session_id, sourcePath.trim());
    setBusy("");
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "素材导入失败");
      return;
    }
    loadSession(result.data);
    setSourcePath("");
    setImportOpen(false);
    setMessage("素材已加入时间线，原始文件保持只读。");
  }

  async function uploadAsset(file = pendingFile) {
    if (!file) return;
    if (!session) {
      setMessage("请先新建空项目，再从本机选择视频。");
      setImportMode("empty");
      return;
    }
    setBusy("upload");
    const result = await client.uploadAsset(session.session_id, file);
    setBusy("");
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "素材上传失败");
      return;
    }
    loadSession(result.data);
    setPendingFile(undefined);
    setImportOpen(false);
    const isAudio = file.type.startsWith("audio/") || /\.(wav|mp3|m4a|aac|ogg|flac)$/i.test(file.name);
    setMessage(isAudio ? "音频已上传到 Server 管理目录，可作为只读配音素材使用。" : "视频已上传到 Server 管理目录并加入时间线。");
  }

  async function createFromResultPackage() {
    if (!resultPackagePath.trim()) return;
    setBusy("result-package");
    const result = await client.createSessionFromResultPackage(resultPackagePath.trim(), resultRecordingPath.trim() || undefined, newName.trim() || undefined);
    setBusy("");
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "结果包导入失败");
      return;
    }
    setSessions((current) => [result.data!, ...current.filter((item) => item.session_id !== result.data!.session_id)]);
    loadSession(result.data);
    setResultPackagePath("");
    setResultRecordingPath("");
    setImportOpen(false);
    setMessage("结果包已转换为编辑项目，步骤顺序和时间范围已保留为来源证据。");
  }

  async function submitImport() {
    if (importMode === "result") await createFromResultPackage();
    else if (importMode === "path") await importAsset();
    else if (importMode === "upload") await uploadAsset();
    else await createSession();
  }

  function commitPlan(next: EditorPlan, editKey: string) {
    if (!draftPlan || editorPlansEqual(draftPlan, next)) return;
    const now = Date.now();
    const grouped = editGroupRef.current?.key === editKey && now - editGroupRef.current.at < 800;
    if (!grouped) setHistory((current) => recordEditorHistory(current, draftPlan));
    editGroupRef.current = { key: editKey, at: now };
    setDraftPlan(next);
    setSaveState("dirty");
    setValidationReport(undefined);
  }

  function beginTrim(event: React.PointerEvent<HTMLSpanElement>, item: TimelineShot, edge: "start" | "end") {
    if (!draftPlan || session?.status === "rendering" || event.button !== 0 || !item.shot.source_time_range_ms) return;
    event.preventDefault();
    event.stopPropagation();
    event.currentTarget.setPointerCapture(event.pointerId);
    setSelectedShotID(item.shot.id);
    setSelectedTrack("video");
    setPreviewMode("source");
    const interaction: TimelineInteraction = {
      kind: "trim",
      shotID: item.shot.id,
      edge,
      startClientX: event.clientX,
      startValueMS: item.shot.source_time_range_ms[edge === "start" ? 0 : 1],
      basePlan: draftPlan,
    };
    timelineInteractionRef.current = interaction;
    setTimelineInteractionKind("trim");
    setSnapGuideMS(undefined);
  }

  function beginMove(event: React.PointerEvent<HTMLButtonElement>, item: TimelineShot) {
    if (!draftPlan || session?.status === "rendering" || event.button !== 0) return;
    event.preventDefault();
    event.stopPropagation();
    setSelectedShotID(item.shot.id);
    setSelectedTrack("video");
    const interaction: TimelineInteraction = { kind: "move", shotID: item.shot.id, basePlan: draftPlan };
    timelineInteractionRef.current = interaction;
    setTimelineInteractionKind("move");
  }

  function beginPlayheadDrag(event: React.PointerEvent<HTMLDivElement>) {
    if (event.button !== 0) return;
    const target = event.target as HTMLElement;
    if (target.closest(".studio-timeline-clip") || target.closest(".studio-trim-handle")) return;
    event.preventDefault();
    const interaction: TimelineInteraction = { kind: "playhead" };
    timelineInteractionRef.current = interaction;
    setTimelineInteractionKind("playhead");
    updatePlayheadFromClientX(event.clientX);
  }

  function updateTimelineInteraction(clientX: number) {
    const interaction = timelineInteractionRef.current;
    if (!interaction) return;
    if (interaction.kind === "playhead") {
      updatePlayheadFromClientX(clientX);
      return;
    }
    if (interaction.kind === "move") {
      const outputMS = outputMSFromClientX(clientX);
      const targetIndex = targetIndexForOutputMS(interaction.basePlan, outputMS);
      const result = reorderShotInPlan(interaction.basePlan, interaction.shotID, targetIndex, session?.asset_catalog.steps ?? []);
      if (result.blockedReason) setMessage(result.blockedReason);
      draftPlanRef.current = result.plan;
      setDraftPlan(result.plan);
      setSaveState(result.changed ? "dirty" : planSaveState(interaction.basePlan, sessionRef.current));
      setValidationReport(undefined);
      setSnapGuideMS(outputBoundaryForIndex(interaction.basePlan, targetIndex));
      return;
    }
    const baseShot = interaction.basePlan.shots.find((shot) => shot.id === interaction.shotID);
    if (!baseShot?.source_time_range_ms) return;
    const deltaMS = (clientX - interaction.startClientX) / timelineScale * 1000;
    const step = session?.asset_catalog.steps?.find((candidate) => candidate.step_id === baseShot.source_step_id);
    const artifact = session?.asset_catalog.artifacts.find((candidate) => candidate.id === baseShot.source_artifact_id);
    const result = trimShotInPlan({
      plan: interaction.basePlan,
      shotID: interaction.shotID,
      edge: interaction.edge,
      valueMS: interaction.startValueMS + deltaMS,
      fps: session?.final_profile.fps ?? 30,
      snapPointsMS: sourceSnapPoints(baseShot, step, artifact),
      ...(artifact?.duration_ms === undefined ? {} : { sourceDurationMS: artifact.duration_ms }),
    });
    draftPlanRef.current = result.plan;
    setDraftPlan(result.plan);
    setSaveState(result.changed ? "dirty" : planSaveState(interaction.basePlan, sessionRef.current));
    setValidationReport(undefined);
    const changedShot = result.plan.shots.find((shot) => shot.id === interaction.shotID);
    const sourceValue = changedShot?.source_time_range_ms?.[interaction.edge === "start" ? 0 : 1];
    const timelineItem = buildTimelineShots(result.plan).find((item) => item.shot.id === interaction.shotID);
    if (sourceValue !== undefined && timelineItem && changedShot) {
      setSnapGuideMS(interaction.edge === "start" ? timelineItem.startMS : timelineItem.endMS);
      selectShotPreviewTime(changedShot, timelineItem, interaction.edge === "start" ? timelineItem.startMS : Math.max(timelineItem.startMS, timelineItem.endMS - 1));
    }
  }

  function finishTimelineInteraction() {
    const interaction = timelineInteractionRef.current;
    timelineInteractionRef.current = undefined;
    setTimelineInteractionKind(undefined);
    setSnapGuideMS(undefined);
    if (!interaction || interaction.kind === "playhead") return;
    const currentPlan = draftPlanRef.current;
    if (!currentPlan || editorPlansEqual(interaction.basePlan, currentPlan)) {
      setSaveState(planSaveState(interaction.basePlan, sessionRef.current));
      return;
    }
    setHistory((current) => recordEditorHistory(current, interaction.basePlan));
    editGroupRef.current = undefined;
    setSaveState("dirty");
  }

  function toggleTimelineTrack(track: TimelineTrack) {
    setHiddenTracks((current) => ({ ...current, [track]: !current[track] }));
  }

  function cancelTimelineInteraction() {
    const interaction = timelineInteractionRef.current;
    if (!interaction) return;
    timelineInteractionRef.current = undefined;
    setTimelineInteractionKind(undefined);
    setSnapGuideMS(undefined);
    if (interaction.kind !== "playhead") {
      draftPlanRef.current = interaction.basePlan;
      setDraftPlan(interaction.basePlan);
      setSaveState(planSaveState(interaction.basePlan, sessionRef.current));
      setValidationReport(sessionRef.current?.validation);
    }
  }

  function outputMSFromClientX(clientX: number): number {
    const content = timelineContentRef.current;
    if (!content) return 0;
    const bounds = content.getBoundingClientRect();
    return Math.max(0, Math.min(totalDurationMS, (clientX - bounds.left) / timelineScale * 1000));
  }

  function updatePlayheadFromClientX(clientX: number) {
    seekTimeline(outputMSFromClientX(clientX));
  }

  function selectShotPreviewTime(shot: EditorShot, item: TimelineShot, outputMS: number) {
    const sourceStartMS = shot.source_time_range_ms?.[0] ?? 0;
    const sourceSeconds = (sourceStartMS + Math.max(0, outputMS - item.startMS)) / 1000;
    pendingSeekRef.current = { shotID: shot.id, sourceSeconds, outputMS };
    if (selectedShot?.id === shot.id && videoRef.current) {
      videoRef.current.currentTime = sourceSeconds;
      setPlayheadMS(outputMS);
      pendingSeekRef.current = undefined;
    }
  }

  function patchShot(patch: Partial<EditorShot>, editKey = "shot") {
    if (!draftPlan || !selectedShot) return;
    commitPlan({ ...draftPlan, shots: draftPlan.shots.map((shot) => (shot.id === selectedShot.id ? { ...shot, ...patch } : shot)) }, `${editKey}:${selectedShot.id}`);
  }

  function patchRange(position: 0 | 1, seconds: number) {
    if (!draftPlan || !selectedShot || !Number.isFinite(seconds)) return;
    const artifact = session?.asset_catalog.artifacts.find((candidate) => candidate.id === selectedShot.source_artifact_id);
    const step = session?.asset_catalog.steps?.find((candidate) => candidate.step_id === selectedShot.source_step_id);
    const result = trimShotInPlan({
      plan: draftPlan,
      shotID: selectedShot.id,
      edge: position === 0 ? "start" : "end",
      valueMS: Math.max(0, Math.round(seconds * 1000)),
      fps: session?.final_profile.fps ?? 30,
      snapPointsMS: sourceSnapPoints(selectedShot, step, artifact),
      ...(artifact?.duration_ms === undefined ? {} : { sourceDurationMS: artifact.duration_ms }),
    });
    if (!result.changed) {
      if (result.blockedReason) setMessage(result.blockedReason);
      return;
    }
    commitPlan(result.plan, `range-${position}:${selectedShot.id}`);
  }

  function patchCaption(text: string) {
    if (!selectedShot) return;
    const overlays = [...(selectedShot.overlays ?? [])];
    const captionIndex = overlays.findIndex((overlay) => overlay.type === "caption");
    if (!text.trim()) {
      patchShot({ overlays: overlays.filter((_, index) => index !== captionIndex) }, "caption");
      return;
    }
    const caption = { type: "caption", text, start_ms: 0, end_ms: Math.min(3000, shotDuration(selectedShot)) };
    if (captionIndex >= 0) overlays[captionIndex] = { ...overlays[captionIndex], ...caption };
    else overlays.push(caption);
    patchShot({ overlays }, "caption");
  }

  function patchAudio(patch: Partial<NonNullable<EditorPlan["audio"]>>) {
    if (!draftPlan) return;
    const audio = { mode: "source" as const, volume_percent: 100, ...(draftPlan.audio ?? {}), ...patch };
    commitPlan({ ...draftPlan, audio }, "audio");
  }

  function patchSelectedAudioSegment(patch: { mode?: "source" | "mute"; volumePercent?: number }) {
    if (!draftPlan || !selectedAudioSegment) return;
    const result = patchAudioSegmentInPlan({ plan: draftPlan, segmentID: selectedAudioSegment.id, totalDurationMS, patch });
    if (!result.changed) {
      if (result.blockedReason) setMessage(result.blockedReason);
      return;
    }
    commitPlan(result.plan, `audio-segment:${selectedAudioSegment.id}`);
  }

  function mergeSelectedAudioSegment() {
    if (!draftPlan || !selectedAudioSegment) return;
    const result = mergeAudioSegmentInPlan({ plan: draftPlan, segmentID: selectedAudioSegment.id, totalDurationMS });
    if (!result.changed) {
      setMessage(result.blockedReason ?? "当前音频片段无法合并。");
      return;
    }
    commitPlan(result.plan, `merge-audio:${selectedAudioSegment.id}:${Date.now()}`);
    const nextSegments = buildAudioSegments(result.plan, totalDurationMS);
    setSelectedAudioSegmentID(nextSegments.find((segment) => playheadMS >= segment.startMS && playheadMS < segment.endMS)?.id ?? nextSegments[0]?.id ?? "");
    setMessage("已合并音频边界，视频时间线保持不变。");
  }

  async function analyzeTimelineAudio() {
    if (!session || !draftPlan || audioAnalysisBusy) return;
    const artifactIDs = [...new Set(draftPlan.shots.map((shot) => shot.source_artifact_id))];
    if (!artifactIDs.length) {
      setMessage("时间线中没有可分析的源素材。");
      return;
    }
    setAudioAnalysisBusy(true);
    const results = await Promise.all(artifactIDs.map(async (artifactID) => ({ artifactID, result: await client.analyzeAudio(session.session_id, artifactID) })));
    setAudioAnalysisBusy(false);
    const successful = results.filter((item): item is { artifactID: string; result: { ok: true; data: EditorAudioAnalysis } } => item.result.ok && Boolean(item.result.data));
    if (successful.length) {
      setAudioAnalyses((current) => ({ ...current, ...Object.fromEntries(successful.map((item) => [item.artifactID, item.result.data])) }));
      setDismissedAudioCandidateIDs([]);
    }
    const failures = results.filter((item) => !item.result.ok);
    if (failures.length) {
      const oldBridge = failures.every((item) => /HTTP 404|unknown method/i.test(item.result.error ?? ""));
      setMessage(oldBridge ? "当前运行的 Bridge 版本尚未包含音频分析接口；源码已就绪，需在 Go 应用控制解除后重建 Bridge。" : `已分析 ${successful.length}/${results.length} 个素材；失败：${failures.map((item) => item.result.error ?? item.artifactID).join("；")}`);
    } else {
      const noAudio = successful.filter((item) => !item.result.data.has_audio).length;
      setMessage(`音频分析完成：${successful.length} 个素材${noAudio ? `，${noAudio} 个没有音轨` : ""}。静音区间仅作为待确认候选。`);
    }
  }

  function applyAudioCandidate(candidate: AudioEditCandidate) {
    if (!draftPlan) return;
    const result = muteAudioRangeInPlan({ plan: draftPlan, startMS: candidate.startMS, endMS: candidate.endMS, totalDurationMS });
    if (!result.changed) {
      setMessage(result.blockedReason ?? "该区间已经静音。");
      return;
    }
    commitPlan(result.plan, `audio-candidate:${candidate.id}`);
    const segments = buildAudioSegments(result.plan, totalDurationMS);
    const segment = segments.find((item) => item.startMS >= candidate.startMS && item.endMS <= candidate.endMS);
    setSelectedTrack("audio");
    setSelectedAudioSegmentID(segment?.id ?? "");
    setDismissedAudioCandidateIDs((current) => [...current, candidate.id]);
    setMessage("已接受自动静音候选；仅修改音频，视频和业务步骤未改变。");
  }

  function deleteSelected() {
    if (selectedTrack === "audio") {
      patchSelectedAudioSegment({ mode: "mute" });
      setMessage("已将所选音频片段静音，视频时长保持不变。");
      return;
    }
    deleteShot();
  }

  function moveShot(direction: -1 | 1) {
    if (!draftPlan || !selectedShot || selectedIndex < 0) return;
    const target = selectedIndex + direction;
    if (target < 0 || target >= draftPlan.shots.length) return;
    const shots = [...draftPlan.shots];
    [shots[selectedIndex], shots[target]] = [shots[target]!, shots[selectedIndex]!];
    if (!preservesRequiredStepOrder(shots, session?.asset_catalog.steps ?? [])) {
      setMessage("必需业务步骤顺序已锁定，不能这样移动。");
      return;
    }
    commitPlan({ ...draftPlan, shots }, `move-${selectedShot.id}-${Date.now()}`);
  }

  function splitShot() {
    if (!draftPlan) return;
    if (selectedTrack === "audio") {
      const result = splitAudioAtOutputMS({ plan: draftPlan, outputMS: playheadMS, totalDurationMS, fps: session?.final_profile.fps ?? 30 });
      if (!result.changed) {
        setMessage(result.blockedReason ?? "当前播放头位置无法分割音频。");
        return;
      }
      commitPlan(result.plan, `split-audio-${Date.now()}`);
      const nextSegments = buildAudioSegments(result.plan, totalDurationMS);
      setSelectedAudioSegmentID(nextSegments.find((segment) => playheadMS >= segment.startMS && playheadMS < segment.endMS)?.id ?? "");
      setMessage(`已在播放头 ${formatTimecode(playheadMS)} 处分割音频。`);
      return;
    }
    const result = splitShotAtOutputMS({
      plan: draftPlan,
      outputMS: playheadMS,
      fps: session?.final_profile.fps ?? 30,
      newShotID: `shot_${Date.now()}`,
    });
    if (!result.changed || !result.rightShotID) {
      setMessage(result.blockedReason ?? "当前播放头位置无法分割。");
      return;
    }
    commitPlan(result.plan, `split-${result.leftShotID}-${Date.now()}`);
    setSelectedShotID(result.rightShotID);
    setPlayheadMS(result.outputSplitMS ?? playheadMS);
    setMessage(`已在播放头 ${formatTimecode(result.outputSplitMS ?? playheadMS)} 处分割视频。`);
  }

  function deleteShot() {
    if (!draftPlan || !selectedShot) return;
    const result = deleteShotInPlan(draftPlan, selectedShot.id, session?.asset_catalog.steps ?? []);
    if (!result.changed) {
      setMessage(result.blockedReason ?? "当前片段无法删除。");
      return;
    }
    commitPlan(result.plan, `delete-${selectedShot.id}-${Date.now()}`);
    setSelectedShotID(result.plan.shots[Math.min(selectedIndex, result.plan.shots.length - 1)]?.id ?? "");
  }

  function undo() {
    if (!draftPlan) return;
    const result = undoEditorHistory(history, draftPlan);
    if (!result.plan) return;
    setHistory(result.history);
    setDraftPlan(result.plan);
    setSelectedShotID((current) => result.plan!.shots.some((shot) => shot.id === current) ? current : result.plan!.shots[0]?.id ?? "");
    setSaveState("dirty");
    setValidationReport(undefined);
    editGroupRef.current = undefined;
  }

  function redo() {
    if (!draftPlan) return;
    const result = redoEditorHistory(history, draftPlan);
    if (!result.plan) return;
    setHistory(result.history);
    setDraftPlan(result.plan);
    setSelectedShotID((current) => result.plan!.shots.some((shot) => shot.id === current) ? current : result.plan!.shots[0]?.id ?? "");
    setSaveState("dirty");
    setValidationReport(undefined);
    editGroupRef.current = undefined;
  }

  async function persistDraft(manual: boolean): Promise<EditorSession | undefined> {
    const activeSession = sessionRef.current;
    const plan = draftPlanRef.current;
    if (!activeSession || !plan) return undefined;
    if (editorPlansEqual(activeSession.edit_plan, plan)) {
      setSaveState("saved");
      return activeSession;
    }
    if (saveInFlightRef.current) return undefined;
    saveInFlightRef.current = true;
    if (manual) setBusy("save");
    setSaveState("saving");
    const submittedPlan = structuredClone(plan);
    const result = await client.savePlan(activeSession.session_id, activeSession.revision, submittedPlan);
    saveInFlightRef.current = false;
    if (manual) setBusy("");
    if (!result.ok || !result.data) {
      setSaveState("error");
      setMessage(result.error ?? "保存失败");
      return undefined;
    }
    const next = result.data;
    if (sessionRef.current?.session_id !== next.session_id) return next;
    setSession(next);
    setSessions((current) => current.map((item) => (item.session_id === next.session_id ? next : item)));
    setValidationReport(next.validation);
    if (editorPlansEqual(draftPlanRef.current, submittedPlan)) {
      setDraftPlan(structuredClone(next.edit_plan));
      setSaveState("saved");
    } else {
      setSaveState("dirty");
    }
    if (manual) setMessage(next.validation?.valid ? "编辑计划已保存并通过校验。" : "计划已保存，但仍有校验问题。");
    return next;
  }

  async function savePlan() {
    await persistDraft(true);
  }

  async function validatePlan(openDrawer = true): Promise<EditorValidationReport | undefined> {
    if (!session) return undefined;
    if (!editorPlansEqual(session.edit_plan, draftPlan)) {
      const saved = await persistDraft(true);
      if (!saved) return undefined;
    }
    setBusy("validate");
    const result = await client.validate(session.session_id);
    setBusy("");
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "计划校验失败");
      return undefined;
    }
    setValidationReport(result.data);
    if (openDrawer) setValidationOpen(true);
    return result.data;
  }

  async function render(kind: "preview" | "final") {
    if (!session) return;
    if (kind === "final") {
      const report = await validatePlan(false);
      const checks = buildExportChecks(sessionRef.current ?? session, draftPlanRef.current ?? draftPlan, report);
      if (!report?.valid || checks.some((check) => check.tone === "error")) {
        setValidationOpen(true);
        setMessage("导出前校验未通过，请先处理阻断项。");
        return;
      }
    } else if (!editorPlansEqual(session.edit_plan, draftPlan)) {
      const saved = await persistDraft(true);
      if (!saved) return;
    }
    setBusy(kind);
    const result = kind === "preview" ? await client.preview(session.session_id) : await client.render(session.session_id);
    setBusy("");
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "渲染失败");
      return;
    }
    loadSession(result.data);
    setMessage(kind === "preview" ? "预览任务已启动。" : "成片导出任务已启动。");
  }

  async function cancelRender(kind: "preview" | "final") {
    if (!session) return;
    setBusy(`cancel-${kind}`);
    const result = await client.cancelRender(session.session_id, kind);
    setBusy("");
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "取消渲染失败");
      return;
    }
    setSession(result.data);
    setMessage("正在停止渲染进程...");
  }

  function selectShot(shot: EditorShot, outputMS?: number, activateTrack = true) {
    setSelectedShotID(shot.id);
    if (activateTrack) setSelectedTrack("video");
    setPreviewMode("source");
    const timelineShot = timelineShots.find((item) => item.shot.id === shot.id);
    const nextOutputMS = outputMS ?? timelineShot?.startMS ?? 0;
    const offsetMS = Math.max(0, nextOutputMS - (timelineShot?.startMS ?? 0));
    const sourceStartMS = shot.source_time_range_ms?.[0] ?? 0;
    selectShotPreviewTime(shot, timelineShot ?? { shot, index: 0, startMS: nextOutputMS - offsetMS, endMS: nextOutputMS - offsetMS + shotDuration(shot), durationMS: shotDuration(shot) }, nextOutputMS);
  }

  function selectAudioSegment(segment: ReturnType<typeof buildAudioSegments>[number], outputMS?: number) {
    setSelectedTrack("audio");
    setSelectedAudioSegmentID(segment.id);
    if (outputMS !== undefined) seekTimeline(outputMS);
  }

  function selectVideoClip(item: TimelineShot) {
    const outputMS = playheadMS >= item.startMS && playheadMS < item.endMS ? playheadMS : item.startMS;
    selectShot(item.shot, outputMS);
  }

  function seekTimeline(outputMS: number) {
    const bounded = Math.max(0, Math.min(totalDurationMS, outputMS));
    if (effectivePreviewMode === "rendered" && videoRef.current) {
      videoRef.current.currentTime = bounded / 1000;
      setPlayheadMS(bounded);
      return;
    }
    const item = timelineShots.find((candidate, index) => bounded >= candidate.startMS && (bounded < candidate.endMS || (index === timelineShots.length - 1 && bounded === candidate.endMS))) ?? timelineShots.at(-1);
    if (item) selectShot(item.shot, bounded, false);
  }

  function handleVideoTimeUpdate() {
    const video = videoRef.current;
    if (!video) return;
    if (effectivePreviewMode === "rendered") {
      setPlayheadMS(Math.min(totalDurationMS, video.currentTime * 1000));
      return;
    }
    const timelineShot = timelineShots.find((item) => item.shot.id === selectedShot?.id);
    if (!timelineShot || !selectedShot?.source_time_range_ms) return;
    const [sourceStartMS, sourceEndMS] = selectedShot.source_time_range_ms;
    if (video.currentTime * 1000 >= sourceEndMS) {
      const next = timelineShots[timelineShot.index + 1];
      if (next && !video.paused) {
        continuePlaybackRef.current = true;
        selectShot(next.shot, next.startMS, false);
        return;
      }
      video.pause();
      setIsPlaying(false);
      setPlayheadMS(timelineShot.endMS);
      return;
    }
    const offsetMS = Math.max(0, video.currentTime * 1000 - sourceStartMS);
    setPlayheadMS(Math.min(timelineShot.endMS, timelineShot.startMS + offsetMS));
  }

  function togglePlayback() {
    const video = videoRef.current;
    if (!video) return;
    if (video.paused) void video.play();
    else video.pause();
  }

  function changePreviewMode(mode: PreviewMode) {
    if (mode === "rendered" && !previewURL) {
      setMessage("请先生成预览，再切换到成片预览模式。");
      return;
    }
    setPreviewMode(mode);
    setPlayheadMS(0);
  }

  return (
    <div className="studio-shell">
      <header className="studio-topbar">
        <div className="studio-topbar-left">
          <select className="studio-project-select" aria-label="编辑项目" value={session?.session_id ?? ""} onChange={(event) => {
            const next = sessions.find((item) => item.session_id === event.target.value);
            if (next) loadSession(next);
          }}>
            <option value="">选择编辑项目</option>
            {sessions.map((item) => <option key={item.session_id} value={item.session_id}>{sessionDisplayName(item)}</option>)}
          </select>
          {session ? <span className="studio-revision">r{session.revision}</span> : null}
          <button className={`studio-save-state ${saveState}`} disabled={!session || saveState === "saved" || busy === "save"} onClick={savePlan}>{saveStateLabel(saveState)}</button>
        </div>

        <div className="studio-history-actions">
          <button className="studio-icon-button" title="撤销 Ctrl+Z" disabled={!draftPlan || history.past.length === 0 || busy !== ""} onClick={undo}>↶</button>
          <button className="studio-icon-button" title="重做 Ctrl+Shift+Z" disabled={!draftPlan || history.future.length === 0 || busy !== ""} onClick={redo}>↷</button>
          <button className="studio-icon-button studio-split-icon-button" aria-label={`在播放头处分割${selectedTrack === "audio" ? "音频" : "视频"}`} title={`在播放头处分割${selectedTrack === "audio" ? "音频" : "视频"}`} disabled={!draftPlan || busy !== ""} onClick={splitShot}><ScissorsIcon /></button>
        </div>

        <div className="studio-topbar-actions">
          <button className={`studio-validation-button ${failedChecks.length > 0 ? "blocked" : ""}`} disabled={!session || busy === "validate"} onClick={() => void validatePlan(true)}>
            <span>{failedChecks.length > 0 ? "!" : "✓"}</span>
            <span>{busy === "validate" ? "校验中" : `导出校验 ${passedChecks.length}/${exportChecks.length}`}</span>
          </button>
          <button className="studio-ghost-button" onClick={() => setImportOpen(true)}>导入</button>
          {session?.preview.status === "running" ? (
            <button className="studio-outline-button" disabled={busy !== ""} onClick={() => void cancelRender("preview")}>{busy === "cancel-preview" ? "停止中" : "取消预览"}</button>
          ) : (
            <button className="studio-outline-button" disabled={!session || busy !== "" || saveState === "saving" || session?.final_render.status === "running"} onClick={() => void render("preview")}>{busy === "preview" ? "启动中" : "生成预览"}</button>
          )}
          {session?.final_render.status === "running" ? (
            <button className="studio-primary-button" disabled={busy !== ""} onClick={() => void cancelRender("final")}>{busy === "cancel-final" ? "停止中" : "取消导出"}</button>
          ) : (
            <button className="studio-primary-button" disabled={!session || busy !== "" || saveState === "saving" || session?.preview.status === "running"} onClick={() => void render("final")}>{busy === "final" ? "启动中" : "导出 MP4"}</button>
          )}
        </div>
      </header>

      {session ? (
        <div className="studio-workspace">
          <div className="studio-upper-workspace">
            <section className="studio-panel studio-media-panel">
              <div className="studio-panel-heading"><strong>素材</strong><small>{session.asset_catalog.artifacts.length} 项</small></div>
              <div className="studio-panel-tabs">
                {(["media", "captions", "callouts", "generated"] as MediaTab[]).map((tab) => (
                  <button key={tab} className={mediaTab === tab ? "active" : ""} onClick={() => setMediaTab(tab)}>{mediaTabLabel(tab)}</button>
                ))}
              </div>
              <div className="studio-media-content">
                <button className="studio-import-zone" onClick={() => setImportOpen(true)}>＋ 从本机选择，或导入结果包</button>
                <MediaPanelContent tab={mediaTab} session={session} plan={draftPlan} selectedShotID={selectedShot?.id} onSelectShot={(shot) => selectShot(shot)} />
              </div>
            </section>

            <section className="studio-panel studio-preview-panel">
              <div className="studio-panel-heading">
                <div className="studio-heading-group"><strong>预览</strong><span>{effectivePreviewMode === "rendered" ? "FFmpeg 预览" : "源素材"}</span></div>
                <div className="studio-preview-switch">
                  <button className={effectivePreviewMode === "source" ? "active" : ""} onClick={() => changePreviewMode("source")}>源素材</button>
                  <button className={effectivePreviewMode === "rendered" ? "active" : ""} disabled={!previewURL} onClick={() => changePreviewMode("rendered")}>渲染预览</button>
                  <span>{effectivePreviewMode === "rendered" ? `${session.preview_profile.width}×${session.preview_profile.height}` : mediaResolution(selectedArtifact)}</span>
                </div>
              </div>
              <div className="studio-video-stage">
                {mediaURL ? (
                  <div className="studio-video-frame">
                    <video
                      ref={videoRef}
                      key={`${effectivePreviewMode}-${mediaURL}`}
                      src={mediaURL}
                      onTimeUpdate={handleVideoTimeUpdate}
                      onPlay={() => setIsPlaying(true)}
                      onPause={() => setIsPlaying(false)}
                      onEnded={() => setIsPlaying(false)}
                      onLoadedMetadata={() => {
                        if (effectivePreviewMode === "source" && selectedShot?.source_time_range_ms && !pendingSeekRef.current && videoRef.current) {
                          videoRef.current.currentTime = selectedShot.source_time_range_ms[0] / 1000;
                        }
                      }}
                    />
                    {effectivePreviewMode === "source" && sourcePreviewCaption ? <div className="studio-caption-preview">{sourcePreviewCaption}</div> : null}
                    {selectedStep ? <div className={`studio-step-preview ${stepTone(selectedStep.status)}`}>步骤 {stepDisplayNumber(selectedStep, session.asset_catalog.steps ?? [])} · {stepTitle(selectedStep)}</div> : null}
                  </div>
                ) : (
                  <div className="studio-video-placeholder"><strong>还没有可预览的素材</strong><span>导入本地录屏或 RecordingResultPackage 后开始编辑。</span><button className="studio-primary-button" onClick={() => setImportOpen(true)}>导入素材</button></div>
                )}
              </div>
              <div className="studio-transport">
                <button className="studio-icon-button" disabled={!mediaURL} onClick={() => seekTimeline(playheadMS - 1000 / session.preview_profile.fps)}>│‹</button>
                <button className="studio-play-button" disabled={!mediaURL} onClick={togglePlayback}>{isPlaying ? "Ⅱ" : "▶"}</button>
                <button className="studio-icon-button" disabled={!mediaURL} onClick={() => seekTimeline(playheadMS + 1000 / session.preview_profile.fps)}>›│</button>
                <span>{formatTimecode(playheadMS)} / {formatTimecode(totalDurationMS)}</span>
                <span className="studio-render-status">预览：{renderStateLabel(session.preview)} · 成片：{renderStateLabel(session.final_render)}</span>
              </div>
            </section>

            <aside className="studio-panel studio-properties-panel">
              <div className="studio-panel-heading"><strong>属性</strong><small>{selectedTrack === "audio" && selectedAudioSegment ? `音频 ${selectedAudioSegment.index + 1}/${audioSegments.length}` : selectedShot ? `视频 ${selectedIndex + 1}/${draftPlan?.shots.length ?? 0}` : "未选择"}</small></div>
              {selectedTrack === "audio" && selectedAudioSegment ? (
                <div className="studio-properties-scroll">
                  <PropertySection title="音频片段" meta={selectedAudioSegment.id}>
                    <label className="studio-field"><span>时间范围</span><input value={`${formatTimecode(selectedAudioSegment.startMS)}—${formatTimecode(selectedAudioSegment.endMS)}`} readOnly /></label>
                    <label className="studio-field"><span>持续时间</span><input value={formatTimecode(selectedAudioSegment.durationMS)} readOnly /></label>
                    <button className="studio-outline-button" disabled={audioSegments.length <= 1} onClick={mergeSelectedAudioSegment}>{selectedAudioSegment.index > 0 ? "与前一段合并" : "与后一段合并"}</button>
                  </PropertySection>
                  <PropertySection title="片段声音" meta={selectedAudioSegment.mode === "mute" ? "此段静音" : "此段源音轨"}>
                    <div className="studio-radio-row">
                      <label><input type="radio" name="studio-audio-segment-mode" checked={selectedAudioSegment.mode === "source"} onChange={() => patchSelectedAudioSegment({ mode: "source" })} />保留源音频</label>
                      <label><input type="radio" name="studio-audio-segment-mode" checked={selectedAudioSegment.mode === "mute"} onChange={() => patchSelectedAudioSegment({ mode: "mute" })} />静音此段</label>
                    </div>
                    <label className="studio-field"><span>片段音量 {selectedAudioSegment.volumePercent}%</span><input type="range" min="0" max="200" step="5" disabled={selectedAudioSegment.mode === "mute"} value={selectedAudioSegment.volumePercent} onChange={(event) => patchSelectedAudioSegment({ volumePercent: Number(event.target.value) })} /></label>
                    <small className="studio-property-note">仅修改当前音频段；全局默认仍为 {draftPlan?.audio?.mode === "mute" ? "静音" : `${draftPlan?.audio?.volume_percent ?? 100}%`}。</small>
                    {selectedAudioSegment.volumePercent > 100 ? <small className="studio-property-note">浏览器源素材预览最高为 100%；导出时 FFmpeg 会应用 {selectedAudioSegment.volumePercent}% 的实际增益。</small> : null}
                  </PropertySection>
                  <PropertySection title="自动静音候选" meta={audioCandidates.length ? `${audioCandidates.length} 个待确认` : "无待确认项"}>
                    {audioCandidates.slice(0, 5).map((candidate) => <div className="studio-audio-candidate-card" key={candidate.id}><div><strong>{formatTimecode(candidate.startMS)}—{formatTimecode(candidate.endMS)}</strong><small>信号置信度 {Math.round(candidate.signalConfidence * 100)}% · 仅代表低音量检测</small></div><div><button className="studio-outline-button" onClick={() => applyAudioCandidate(candidate)}>静音</button><button className="studio-ghost-button" onClick={() => setDismissedAudioCandidateIDs((current) => [...current, candidate.id])}>忽略</button></div></div>)}
                    {!audioCandidates.length ? <small className="studio-property-note">点击时间线“分析音频”生成候选；系统不会自动修改计划。</small> : null}
                  </PropertySection>
                </div>
              ) : selectedShot ? (
                <div className="studio-properties-scroll">
                  <PropertySection title="片段" meta={selectedShot.id}>
                    <label className="studio-field"><span>片段说明</span><input value={selectedShot.purpose} onChange={(event) => patchShot({ purpose: event.target.value }, "purpose")} /></label>
                    <div className="studio-field-pair">
                      <label className="studio-field"><span>入点</span><input type="number" min="0" step="0.1" value={(selectedShot.source_time_range_ms?.[0] ?? 0) / 1000} onChange={(event) => patchRange(0, Number(event.target.value))} /></label>
                      <label className="studio-field"><span>出点</span><input type="number" min="0.5" step="0.1" value={(selectedShot.source_time_range_ms?.[1] ?? 1000) / 1000} onChange={(event) => patchRange(1, Number(event.target.value))} /></label>
                    </div>
                    <label className="studio-field"><span>持续时间</span><input value={formatTimecode(shotDuration(selectedShot))} readOnly /></label>
                  </PropertySection>

                  <PropertySection title="字幕" meta="确定性烧录">
                    <label className="studio-field"><span>烧录文本</span><textarea rows={4} value={captionText(selectedShot)} onChange={(event) => patchCaption(event.target.value)} placeholder="输入需要烧录到画面的字幕" /></label>
                  </PropertySection>

                  <PropertySection title="音频" meta={draftPlan?.audio?.mode === "mute" ? "静音" : "源音轨"}>
                    <div className="studio-radio-row">
                      <label><input type="radio" name="studio-audio-mode" checked={(draftPlan?.audio?.mode ?? "source") === "source"} onChange={() => patchAudio({ mode: "source" })} />保留源音频</label>
                      <label><input type="radio" name="studio-audio-mode" checked={draftPlan?.audio?.mode === "mute"} onChange={() => patchAudio({ mode: "mute" })} />静音</label>
                    </div>
                    <label className="studio-field"><span>音量 {draftPlan?.audio?.volume_percent ?? 100}%</span><input type="range" min="0" max="200" step="5" disabled={draftPlan?.audio?.mode === "mute"} value={draftPlan?.audio?.volume_percent ?? 100} onChange={(event) => patchAudio({ volume_percent: Number(event.target.value) })} /></label>
                  </PropertySection>

                  <PropertySection title="业务证据" meta={selectedStep?.required ? "事实锁定" : "展示片段"}>
                    <div className={`studio-evidence-card ${selectedStep ? stepTone(selectedStep.status) : "neutral"}`}>
                      <EvidenceFact label="来源步骤" value={selectedShot.source_step_id ?? "未绑定"} />
                      <EvidenceFact label="执行状态" value={selectedStep?.status ?? "本地素材"} />
                      <EvidenceFact label="源时间范围" value={selectedShot.source_time_range_ms ? `${formatTimecode(selectedShot.source_time_range_ms[0])}—${formatTimecode(selectedShot.source_time_range_ms[1])}` : "未声明"} />
                      <EvidenceFact label="素材校验" value={selectedArtifact?.sha256 ? "SHA-256 已记录" : "未提供校验值"} />
                    </div>
                  </PropertySection>
                </div>
              ) : <div className="studio-properties-empty">选择时间线片段后编辑属性。</div>}
              <div className="studio-property-actions">
                <button className="studio-outline-button studio-split-button" title={`在播放头处分割${selectedTrack === "audio" ? "音频" : "视频"}`} disabled={!draftPlan} onClick={splitShot}><ScissorsIcon /><span>分割{selectedTrack === "audio" ? "音频" : "视频"}</span></button>
                <button className="studio-ghost-button" disabled={selectedTrack === "video" ? !selectedShot : !selectedAudioSegment} onClick={deleteSelected}>{selectedTrack === "audio" ? "静音此段" : "删除"}</button>
              </div>
            </aside>
          </div>

          <section className="studio-timeline-panel">
            <div className="studio-timeline-toolbar">
              <div className="studio-timeline-tools">
                <strong>时间线</strong>
                <button className="studio-icon-button" title="前移" disabled={selectedTrack !== "video" || !selectedShot || selectedIndex <= 0} onClick={() => moveShot(-1)}>←</button>
                <button className="studio-icon-button" title="后移" disabled={selectedTrack !== "video" || !selectedShot || selectedIndex >= (draftPlan?.shots.length ?? 0) - 1} onClick={() => moveShot(1)}>→</button>
                <button className="studio-icon-button studio-split-icon-button" aria-label={`在播放头处分割${selectedTrack === "audio" ? "音频" : "视频"}`} title={`在播放头处分割${selectedTrack === "audio" ? "音频" : "视频"}`} disabled={!draftPlan} onClick={splitShot}><ScissorsIcon /></button>
                <button className="studio-icon-button" title={selectedTrack === "audio" ? "静音所选音频段" : "删除"} disabled={selectedTrack === "video" ? !selectedShot : !selectedAudioSegment} onClick={deleteSelected}>⌫</button>
                <button className="studio-outline-button studio-analyze-audio" disabled={!draftPlan?.shots.length || audioAnalysisBusy} onClick={() => void analyzeTimelineAudio()}>{audioAnalysisBusy ? "分析中…" : outputWaveform.length ? "重新分析音频" : "分析音频"}</button>
                <span className="studio-snap-status">● 帧吸附</span>
              </div>
              <div className="studio-timeline-tools"><span>{presentationComposition?.durationInFrames ?? 0} 帧 · 总时长 {formatTimecode(totalDurationMS)}</span><input type="range" min="28" max="100" value={timelineScale} onChange={(event) => setTimelineScale(Number(event.target.value))} /></div>
            </div>
            <div className="studio-timeline-body" ref={timelineScrollRef}>
              <div className="studio-track-labels">
                <div />
                <TrackLabel track="evidence" label="业务步骤" locked hidden={hiddenTracks.evidence} onToggle={toggleTimelineTrack} />
                <TrackLabel track="video" label="视频" hidden={hiddenTracks.video} onToggle={toggleTimelineTrack} />
                <TrackLabel track="caption" label="字幕 / 标注" hidden={hiddenTracks.caption} onToggle={toggleTimelineTrack} />
                <TrackLabel track="audio" label="音频" hidden={hiddenTracks.audio} onToggle={toggleTimelineTrack} />
              </div>
              <div ref={timelineContentRef} className={`studio-timeline-scroll ${timelineInteractionKind ? `interacting ${timelineInteractionKind}` : ""}`} style={{ width: timelineWidth, backgroundSize: `${timeRulerStep(totalDurationMS) * timelineScale}px 100%` }} onPointerDown={beginPlayheadDrag}>
                <TimeRuler totalDurationMS={totalDurationMS} scale={timelineScale} />
                <div className={`studio-track-row studio-evidence-track ${hiddenTracks.evidence ? "track-hidden" : ""}`}>
                  {!hiddenTracks.evidence && timelineShots.map((item) => {
                    const step = session.asset_catalog.steps?.find((candidate) => candidate.step_id === item.shot.source_step_id);
                    return <div key={item.shot.id} className={`studio-evidence-clip ${step ? stepTone(step.status) : "neutral"}`} style={clipStyle(item, timelineScale)}><span>{step?.required ? "🔒" : "◇"}</span><strong>{step ? `${stepDisplayNumber(step, session.asset_catalog.steps ?? [])} ${stepTitle(step)}` : "本地展示片段"}</strong></div>;
                  })}
                </div>
                <div className={`studio-track-row studio-video-track ${hiddenTracks.video ? "track-hidden" : ""}`}>
                  {!hiddenTracks.video && timelineShots.map((item) => (
                    <button
                      key={item.shot.id}
                      className={`studio-timeline-clip video ${selectedTrack === "video" && selectedShot?.id === item.shot.id ? "selected" : ""} ${timelineInteractionKind === "move" && selectedShotID === item.shot.id ? "dragging" : ""}`}
                      style={clipStyle(item, timelineScale)}
                      onPointerDown={(event) => beginMove(event, item)}
                      onClick={() => selectVideoClip(item)}
                    >
                      <span className="studio-trim-handle start" title="拖动裁剪入点" onPointerDown={(event) => beginTrim(event, item, "start")} />
                      <span className="studio-clip-index">{item.index + 1}</span>
                      <strong>{item.shot.purpose}</strong>
                      <small>{formatDuration(item.durationMS)}</small>
                      <span className="studio-trim-handle end" title="拖动裁剪出点" onPointerDown={(event) => beginTrim(event, item, "end")} />
                    </button>
                  ))}
                </div>
                <div className={`studio-track-row studio-caption-track ${hiddenTracks.caption ? "track-hidden" : ""}`}>
                  {!hiddenTracks.caption && timelineShots.map((item) => captionText(item.shot) ? <button key={item.shot.id} className="studio-timeline-clip caption" style={clipStyle(item, timelineScale)} onPointerDown={(event) => event.stopPropagation()} onClick={() => selectShot(item.shot)}><strong>{captionText(item.shot)}</strong></button> : null)}
                </div>
                <div className={`studio-track-row studio-audio-track ${hiddenTracks.audio ? "track-hidden" : ""}`}>
                  {!hiddenTracks.audio && audioSegments.map((segment) => {
                    const clipWidth = Math.max(1, segment.durationMS / 1000 * timelineScale);
                    const bars = waveformBarsForRange(outputWaveform, segment.startMS, segment.endMS, Math.floor(clipWidth / 3));
                    return <button key={segment.id} className={`studio-audio-clip ${segment.mode === "mute" ? "muted" : ""} ${selectedTrack === "audio" && selectedAudioSegment?.id === segment.id ? "selected" : ""}`} style={{ left: segment.startMS / 1000 * timelineScale, width: clipWidth }} title={`${segment.mode === "mute" ? "静音" : `音量 ${segment.volumePercent}%`} · ${formatTimecode(segment.startMS)}—${formatTimecode(segment.endMS)}`} onPointerDown={(event) => event.stopPropagation()} onClick={(event) => { event.stopPropagation(); selectAudioSegment(segment); }}><span className="studio-audio-waveform" aria-hidden="true">{bars.map((height, index) => <i key={index} style={{ height: `${Math.round(height * 90)}%` }} />)}</span><strong>{segment.mode === "mute" ? "静音" : `源音频 ${segment.index + 1} · ${segment.volumePercent}%`}</strong><small>{formatDuration(segment.durationMS)}</small></button>;
                  })}
                  {!hiddenTracks.audio && audioCandidates.map((candidate) => <span key={candidate.id} className="studio-audio-candidate-range" style={{ left: candidate.startMS / 1000 * timelineScale, width: Math.max(2, (candidate.endMS - candidate.startMS) / 1000 * timelineScale) }} />)}
                </div>
                <div className="studio-playhead" style={{ left: playheadMS / 1000 * timelineScale }} />
                {snapGuideMS !== undefined ? <div className="studio-snap-guide" style={{ left: snapGuideMS / 1000 * timelineScale }} /> : null}
              </div>
            </div>
          </section>
        </div>
      ) : (
        <div className="studio-empty-state">
          <div className="studio-empty-illustration">▶</div>
          <strong>创建第一个本地视频项目</strong>
          <span>从录屏、Server 本机路径或 RecordingResultPackage 开始。</span>
          <button className="studio-primary-button" onClick={() => setImportOpen(true)}>新建或导入</button>
        </div>
      )}

      {importOpen ? <ImportDialog mode={importMode} onModeChange={setImportMode} newName={newName} onNameChange={setNewName} sourcePath={sourcePath} onSourcePathChange={setSourcePath} resultPackagePath={resultPackagePath} onResultPackagePathChange={setResultPackagePath} resultRecordingPath={resultRecordingPath} onResultRecordingPathChange={setResultRecordingPath} pendingFile={pendingFile} onFileChange={setPendingFile} busy={busy} hasSession={Boolean(session)} onClose={() => setImportOpen(false)} onSubmit={() => void submitImport()} /> : null}
      {validationOpen ? <ValidationDrawer checks={exportChecks} report={currentValidation} onClose={() => setValidationOpen(false)} /> : null}
      {message ? <div className="studio-toast"><span>{message}</span><button onClick={() => setMessage("")}>×</button></div> : null}
    </div>
  );
}

function MediaPanelContent({ tab, session, plan, selectedShotID, onSelectShot }: { tab: MediaTab; session: EditorSession; plan: EditorPlan | undefined; selectedShotID: string | undefined; onSelectShot: (shot: EditorShot) => void }) {
  if (tab === "captions") {
    const shots = plan?.shots.filter((shot) => captionText(shot)) ?? [];
    return <div className="studio-list-content">{shots.map((shot) => <button key={shot.id} className={selectedShotID === shot.id ? "selected" : ""} onClick={() => onSelectShot(shot)}><strong>{captionText(shot)}</strong><small>{shot.purpose}</small></button>)}{shots.length === 0 ? <PanelEmpty text="当前计划没有字幕。" /> : null}</div>;
  }
  if (tab === "callouts") {
    const rows = plan?.shots.flatMap((shot) => (shot.overlays ?? []).filter((overlay) => overlay.type !== "caption").map((overlay) => ({ shot, overlay }))) ?? [];
    return <div className="studio-list-content">{rows.map(({ shot, overlay }, index) => <button key={`${shot.id}-${index}`} onClick={() => onSelectShot(shot)}><strong>{overlayTypeLabel(overlay.type)}</strong><small>{shot.purpose}</small></button>)}{rows.length === 0 ? <PanelEmpty text="尚无标注。仅在 Renderer 支持后开放新的标注类型。" /> : null}</div>;
  }
  if (tab === "generated") {
    const generated = session.asset_catalog.artifacts.filter(isGeneratedArtifact);
    const capability = session.provider_capabilities.find((item) => item.provider === "seedance");
    return <div className="studio-generated-content"><div className="studio-provider-card"><div><strong>Seedance</strong><span>{capability?.configured ? capability.mode : "未配置"}</span></div><p>候选素材必须人工审查，不能自动加入时间线或代表真实业务步骤。</p><small>auto_include=false · presentation_only=true</small></div>{generated.map((artifact) => <AssetCard key={artifact.id} artifact={artifact} generated />)}{generated.length === 0 ? <PanelEmpty text="当前没有生成候选素材。" /> : null}</div>;
  }
  return (
    <>
      <p className="studio-section-label">真实执行素材</p>
      <div className="studio-asset-grid">
        {session.asset_catalog.artifacts.filter((artifact) => !isGeneratedArtifact(artifact)).map((artifact) => {
          const shot = plan?.shots.find((candidate) => candidate.source_artifact_id === artifact.id);
          return <AssetCard key={artifact.id} artifact={artifact} selected={shot?.id === selectedShotID} onClick={shot ? () => onSelectShot(shot) : undefined} />;
        })}
      </div>
      {session.asset_catalog.artifacts.length === 0 ? <PanelEmpty text="导入浏览器录屏或本地视频后，系统会创建第一个片段。" /> : null}
    </>
  );
}

function AssetCard({ artifact, selected, generated, onClick }: { artifact: EditorArtifact; selected?: boolean; generated?: boolean; onClick?: (() => void) | undefined }) {
  const content = <><div className={`studio-asset-thumb ${generated ? "generated" : ""}`}><span className={`studio-asset-status ${generated ? "review" : ""}`}>{generated ? "待审查" : artifact.sha256 ? "已校验" : "本地"}</span><span className="studio-asset-duration">{formatDuration(artifact.duration_ms ?? 0)}</span></div><div><strong>{artifact.label ?? artifact.id}</strong><small>{mediaResolution(artifact)} · {artifact.mime_type ?? artifact.kind}</small></div></>;
  if (!onClick) return <article className="studio-asset-card static" aria-label={`${artifact.label ?? artifact.id}（仅展示）`}>{content}</article>;
  return (
    <button className={`studio-asset-card ${selected ? "selected" : ""}`} onClick={onClick}>{content}</button>
  );
}

function ImportDialog(props: {
  mode: ImportMode;
  onModeChange: (mode: ImportMode) => void;
  newName: string;
  onNameChange: (value: string) => void;
  sourcePath: string;
  onSourcePathChange: (value: string) => void;
  resultPackagePath: string;
  onResultPackagePathChange: (value: string) => void;
  resultRecordingPath: string;
  onResultRecordingPathChange: (value: string) => void;
  pendingFile: File | undefined;
  onFileChange: (file?: File) => void;
  busy: string;
  hasSession: boolean;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const canSubmit = props.mode === "empty" || (props.mode === "upload" ? Boolean(props.pendingFile && props.hasSession) : props.mode === "path" ? Boolean(props.sourcePath.trim()) : Boolean(props.resultPackagePath.trim()));
  return (
    <div className="studio-modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) props.onClose(); }}>
      <div className="studio-modal" role="dialog" aria-modal="true" aria-label="导入素材或结果包">
        <div className="studio-modal-header"><strong>导入素材或结果包</strong><button className="studio-icon-button" onClick={props.onClose}>×</button></div>
        <div className="studio-modal-content">
          <div className="studio-import-choices">
            <ImportChoice active={props.mode === "upload"} title="本机视频或音频" detail="浏览器选择并上传到 Server 管理目录" onClick={() => props.onModeChange("upload")} />
            <ImportChoice active={props.mode === "path"} title="Server 路径" detail="读取本机已有视频，不重复上传" onClick={() => props.onModeChange("path")} />
            <ImportChoice active={props.mode === "result"} title="结果包" detail="保留步骤时间和证据来源" onClick={() => props.onModeChange("result")} />
            <ImportChoice active={props.mode === "empty"} title="空项目" detail="先建项目，稍后再加入素材" onClick={() => props.onModeChange("empty")} />
          </div>
          {(props.mode === "empty" || props.mode === "result" || (!props.hasSession && props.mode === "path")) ? <label className="studio-field"><span>项目名称</span><input value={props.newName} onChange={(event) => props.onNameChange(event.target.value)} /></label> : null}
          {props.mode === "upload" ? <label className="studio-file-field"><span>{props.pendingFile?.name ?? (props.hasSession ? "选择视频或 WAV、MP3、M4A、AAC、OGG、FLAC 音频" : "请先创建空项目")}</span><input type="file" accept="video/mp4,video/webm,video/quicktime,.m4v,audio/wav,audio/mpeg,audio/mp4,audio/aac,audio/ogg,audio/flac,.wav,.mp3,.m4a,.aac,.ogg,.flac" disabled={!props.hasSession} onChange={(event) => props.onFileChange(event.target.files?.[0])} /></label> : null}
          {props.mode === "path" ? <label className="studio-field"><span>Server 本机视频路径</span><input value={props.sourcePath} onChange={(event) => props.onSourcePathChange(event.target.value)} placeholder="D:\recordings\demo.mp4" /></label> : null}
          {props.mode === "result" ? <><label className="studio-field"><span>结果包 JSON 路径</span><input value={props.resultPackagePath} onChange={(event) => props.onResultPackagePathChange(event.target.value)} placeholder="D:\results\recording-result.json" /></label><label className="studio-field"><span>已下载解密的录屏路径（远程或加密素材必填）</span><input value={props.resultRecordingPath} onChange={(event) => props.onResultRecordingPathChange(event.target.value)} placeholder="D:\results\raw-recording.mp4" /></label></> : null}
          <p className="studio-dialog-note">原始素材保持只读；导入后执行 SHA-256、FFprobe 和媒体兼容性检查。</p>
        </div>
        <div className="studio-modal-footer"><button className="studio-ghost-button" onClick={props.onClose}>取消</button><button className="studio-primary-button" disabled={!canSubmit || props.busy !== ""} onClick={props.onSubmit}>{props.busy ? "处理中" : props.mode === "empty" ? "创建项目" : "导入并校验"}</button></div>
      </div>
    </div>
  );
}

function ImportChoice({ active, title, detail, onClick }: { active: boolean; title: string; detail: string; onClick: () => void }) {
  return <button className={active ? "active" : ""} onClick={onClick}><strong>{title}</strong><small>{detail}</small></button>;
}

function ValidationDrawer({ checks, report, onClose }: { checks: ExportCheck[]; report: EditorValidationReport | undefined; onClose: () => void }) {
  const errors = checks.filter((check) => check.tone === "error").length;
  return (
    <div className="studio-drawer-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <aside className="studio-validation-drawer">
        <div className="studio-drawer-header"><div><strong>导出前校验</strong><small>{report ? `检查于 ${new Date(report.checked_at).toLocaleTimeString("zh-CN", { hour12: false })}` : "当前草稿规则检查"}</small></div><button className="studio-icon-button" onClick={onClose}>×</button></div>
        <div className={`studio-validation-summary ${errors ? "blocked" : ""}`}>{errors ? `${errors} 个阻断项尚未解决，当前不能导出。` : "所有阻断项均已通过，可以生成最终 MP4。"}</div>
        <div className="studio-check-list">
          {checks.map((check) => <div key={check.id} className={`studio-check-item ${check.tone}`}><span>{check.tone === "pass" ? "✓" : check.tone === "warning" ? "△" : "!"}</span><div><strong>{check.label}</strong><small>{check.detail}</small></div><em>{check.tone === "pass" ? "通过" : check.tone === "warning" ? "提醒" : "阻断"}</em></div>)}
        </div>
        {report?.warnings.length ? <div className="studio-report-findings"><strong>Worker 提醒</strong>{report.warnings.map((finding) => <p key={`${finding.code}-${finding.path ?? ""}`}>{finding.message}</p>)}</div> : null}
      </aside>
    </div>
  );
}

function PropertySection({ title, meta, children }: { title: string; meta?: string; children: React.ReactNode }) {
  return <section className="studio-property-section"><div className="studio-property-title"><strong>{title}</strong>{meta ? <span>{meta}</span> : null}</div>{children}</section>;
}

function EvidenceFact({ label, value }: { label: string; value: string }) {
  return <div><span>{label}</span><strong>{value}</strong></div>;
}

function PanelEmpty({ text }: { text: string }) {
  return <div className="studio-panel-empty">{text}</div>;
}

function ScissorsIcon() {
  return <svg className="studio-scissors-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><circle cx="6" cy="7" r="2.75" /><circle cx="6" cy="17" r="2.75" /><path d="m8.35 8.55 10.15 6.9" /><path d="m8.35 15.45 10.15-6.9" /></svg>;
}

function TrackLabel({ track, label, locked = false, hidden, onToggle }: { track: TimelineTrack; label: string; locked?: boolean; hidden: boolean; onToggle: (track: TimelineTrack) => void }) {
  return <div className={hidden ? "track-label-hidden" : ""}><strong>{label}</strong><span className="studio-track-label-actions">{locked ? <LockIcon /> : null}<button className="studio-track-visibility-toggle" aria-label={`${hidden ? "显示" : "隐藏"}${label}轨道片段`} title={`${hidden ? "显示" : "隐藏"}${label}轨道片段；仅影响时间线显示`} onClick={() => onToggle(track)}><EyeIcon hidden={hidden} /></button></span></div>;
}

function LockIcon() {
  return <svg className="studio-lock-icon" viewBox="0 0 24 24" aria-label="业务步骤受约束"><rect x="5" y="10" width="14" height="10" rx="2" /><path d="M8 10V7a4 4 0 0 1 8 0v3" /><path d="M12 14v2" /></svg>;
}

function EyeIcon({ hidden }: { hidden: boolean }) {
  return hidden
    ? <svg className="studio-eye-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="m3 3 18 18" /><path d="M10.7 6.2A10.8 10.8 0 0 1 12 6.1c5.5 0 8.8 5.9 8.8 5.9a16.6 16.6 0 0 1-3 3.6" /><path d="M6.2 6.3A16.5 16.5 0 0 0 3.2 12S6.5 17.9 12 17.9a9.2 9.2 0 0 0 2.2-.3" /><path d="M9.7 9.7a3.2 3.2 0 0 0 4.6 4.6" /></svg>
    : <svg className="studio-eye-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M3.2 12S6.5 6.1 12 6.1 20.8 12 20.8 12 17.5 17.9 12 17.9 3.2 12 3.2 12Z" /><circle cx="12" cy="12" r="3.1" /></svg>;
}

function TimeRuler({ totalDurationMS, scale }: { totalDurationMS: number; scale: number }) {
  const seconds = Math.max(10, Math.ceil(totalDurationMS / 1000));
  const step = timeRulerStep(totalDurationMS);
  const ticks = Array.from({ length: Math.ceil(seconds / step) + 1 }, (_, index) => index * step);
  return <div className="studio-time-ruler">{ticks.map((second) => <span key={second} style={{ left: second * scale }}>{formatTimecode(second * 1000)}</span>)}</div>;
}

function timeRulerStep(totalDurationMS: number): number {
  const seconds = Math.max(10, Math.ceil(totalDurationMS / 1000));
  return seconds > 90 ? 10 : seconds > 40 ? 5 : 2;
}

export function buildTimelineShots(plan?: EditorPlan): TimelineShot[] {
  let cursor = 0;
  return (plan?.shots ?? []).map((shot, index) => {
    const durationMS = shotDuration(shot);
    const item = { shot, index, startMS: cursor, endMS: cursor + durationMS, durationMS };
    cursor += durationMS;
    return item;
  });
}

export function buildExportChecks(session?: EditorSession, plan?: EditorPlan, report?: EditorValidationReport): ExportCheck[] {
  const steps = session?.asset_catalog.steps ?? [];
  const required = steps.filter((step) => step.required && step.status !== "failed");
  const coverage = plan ? requiredStepCoverage(plan, steps) : [];
  const missing = coverage.filter((item) => !item.covered);
  const unsupported = new Set<string>();
  for (const shot of plan?.shots ?? []) {
    for (const operation of shot.operations ?? []) {
      if (!finalRendererSupports(operation.type)) unsupported.add(operation.type);
    }
    for (const overlay of shot.overlays ?? []) {
      if (!finalRendererSupports(overlay.type)) unsupported.add(overlay.type);
    }
  }
  const assets = session?.asset_catalog.artifacts ?? [];
  const allChecksums = assets.length > 0 && assets.every((artifact) => Boolean(artifact.sha256));
  const generatedInBusinessShots = (plan?.shots ?? []).filter((shot) => isGeneratedArtifact(assets.find((asset) => asset.id === shot.source_artifact_id)) && Boolean(shot.source_step_id));
  const orderPreserved = preservesRequiredStepOrder(plan?.shots ?? [], steps);
  return [
    { id: "plan", label: "编辑计划可渲染", detail: plan?.shots.length ? `${plan.shots.length} 个视频片段已进入计划。` : "时间线中还没有视频片段。", tone: plan?.shots.length ? "pass" : "error" },
    { id: "required", label: "必需业务步骤完整", detail: required.length ? `${required.length - missing.length}/${required.length} 个必需步骤的来源时间范围被完整覆盖。` : "该项目没有声明必需业务步骤。", tone: missing.length ? "error" : required.length ? "pass" : "warning" },
    { id: "order", label: "步骤顺序未改变", detail: orderPreserved ? "编辑计划保持来源业务步骤顺序。" : "必需业务步骤发生了乱序。", tone: orderPreserved ? "pass" : "error" },
    { id: "worker", label: "Worker 计划校验", detail: report ? report.valid ? "DemoEditPlan 已通过 Worker 校验。" : `${report.errors.length} 个 Worker 校验错误。` : "保存或点击校验后获取最终结果。", tone: report ? report.valid ? "pass" : "error" : "warning" },
    { id: "assets", label: "素材完整性", detail: allChecksums ? "所有素材均记录 SHA-256。" : "部分本地素材没有声明 SHA-256；导入链路仍会保留探测结果。", tone: allChecksums ? "pass" : "warning" },
    { id: "generated", label: "生成素材边界", detail: generatedInBusinessShots.length ? "生成候选错误地绑定了业务步骤。" : "生成候选未代表真实业务步骤。", tone: generatedInBusinessShots.length ? "error" : "pass" },
    { id: "operations", label: "渲染能力匹配", detail: unsupported.size ? `当前 Renderer 尚未烧录：${[...unsupported].join("、")}。` : "当前计划只使用已实现的裁剪、拼接、字幕和音频能力。", tone: unsupported.size ? "error" : "pass" },
    { id: "output", label: "输出参数", detail: session ? `${session.final_profile.width}×${session.final_profile.height} · ${session.final_profile.fps} FPS · ${session.final_profile.format.toUpperCase()}。` : "等待项目参数。", tone: session ? "pass" : "error" },
  ];
}

export function preservesRequiredStepOrder(shots: EditorShot[], steps: EditorTimelineStep[]): boolean {
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

function planSaveState(plan: EditorPlan, session?: EditorSession): SaveState {
  return session && editorPlansEqual(plan, session.edit_plan) ? "saved" : "dirty";
}

function outputBoundaryForIndex(plan: EditorPlan, index: number): number {
  return plan.shots.slice(0, Math.max(0, index)).reduce((total, shot) => total + shotDuration(shot), 0);
}

function clipStyle(item: TimelineShot, scale: number): React.CSSProperties {
  return { left: item.startMS / 1000 * scale, width: Math.max(1, item.durationMS / 1000 * scale) };
}

function shotDuration(shot?: EditorShot): number {
  return shot?.source_time_range_ms ? Math.max(0, shot.source_time_range_ms[1] - shot.source_time_range_ms[0]) : 0;
}

function captionText(shot?: EditorShot): string {
  return shot?.overlays?.find((overlay) => overlay.type === "caption")?.text ?? "";
}

function formatDuration(valueMS: number): string {
  const seconds = Math.max(0, Math.round(valueMS / 100) / 10);
  return `${seconds.toFixed(seconds % 1 === 0 ? 0 : 1)}s`;
}

export function formatTimecode(valueMS: number): string {
  const totalTenths = Math.max(0, Math.round(valueMS / 100));
  const hours = Math.floor(totalTenths / 36000);
  const minutes = Math.floor((totalTenths % 36000) / 600);
  const seconds = Math.floor((totalTenths % 600) / 10);
  const tenth = totalTenths % 10;
  return hours > 0 ? `${pad(hours)}:${pad(minutes)}:${pad(seconds)}.${tenth}` : `${pad(minutes)}:${pad(seconds)}.${tenth}`;
}

function pad(value: number): string {
  return String(value).padStart(2, "0");
}

function renderStatusLabel(status: string): string {
  return ({ not_started: "未开始", running: "渲染中", ready: "已就绪", failed: "失败", cancelled: "已取消" } as Record<string, string>)[status] ?? status;
}

function renderStateLabel(state: EditorSession["preview"]): string {
  const phase = ({ queued: "排队", validating: "校验", rendering: "FFmpeg 渲染", cancelling: "停止中" } as Record<string, string>)[state.phase ?? ""];
  return phase ? `${phase}${state.progress ? ` ${state.progress}%` : ""}` : renderStatusLabel(state.status);
}

function saveStateLabel(state: SaveState): string {
  return ({ saved: "已保存", dirty: "待自动保存", saving: "保存中", error: "保存失败" } as const)[state];
}

function sessionDisplayName(session: EditorSession): string {
  const name = session.name.trim();
  if (name && !/[?]{2,}/.test(name)) return name;
  return `本地编辑项目 · ${session.session_id.slice(-6)}`;
}

function mediaTabLabel(tab: MediaTab): string {
  return ({ media: "媒体", captions: "字幕", callouts: "标注", generated: "生成" } as const)[tab];
}

function mediaResolution(artifact?: EditorArtifact): string {
  const width = artifact?.metadata?.width;
  const height = artifact?.metadata?.height;
  return typeof width === "number" && typeof height === "number" ? `${width}×${height}` : "待探测";
}

function isGeneratedArtifact(artifact?: EditorArtifact): boolean {
  if (!artifact) return false;
  return artifact.kind.includes("generated") || artifact.kind.includes("candidate") || artifact.metadata?.presentation_only === true;
}

function stepTitle(step: EditorTimelineStep): string {
  return step.expected_outcome || step.observed_state || step.action || step.step_id;
}

function stepDisplayNumber(step: EditorTimelineStep, steps: EditorTimelineStep[]): number {
  const sorted = [...steps].sort((left, right) => left.order - right.order);
  const index = sorted.findIndex((candidate) => candidate.step_id === step.step_id);
  return index >= 0 ? index + 1 : Math.max(1, step.order);
}

function stepTone(status: string): "pass" | "warning" | "error" | "neutral" {
  const normalized = status.toLowerCase();
  if (["passed", "succeeded", "completed", "success"].includes(normalized)) return "pass";
  if (["failed", "error", "blocked"].includes(normalized)) return "error";
  if (["warning", "attention", "partial"].includes(normalized)) return "warning";
  return "neutral";
}

function overlayTypeLabel(type: string): string {
  return ({ callout: "文字标注", highlight_box: "高亮框", cursor_highlight: "鼠标高亮", spotlight: "聚光", blur_region: "隐私模糊", progress_marker: "进度标记" } as Record<string, string>)[type] ?? type;
}
