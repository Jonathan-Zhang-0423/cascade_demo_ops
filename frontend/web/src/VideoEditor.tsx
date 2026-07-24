import { useEffect, useMemo, useRef, useState } from "react";
import { createEditorClient } from "./editor";
import type { EditorArtifact, EditorOverlay, EditorPlan, EditorSession, EditorShot, EditorStyleDraft, EditorTimelineStep, EditorValidationReport, VideoStyleTemplate } from "./editor";
import { editorPlansEqual, emptyEditorHistory, recordEditorHistory, redoEditorHistory, undoEditorHistory } from "./editorHistory";
import { compilePresentationComposition, finalRendererSupports, finalRendererSupportsOverlay, overlayExportStatus } from "./presentationComposition";
import { activeCaptionText, audioPreviewState, buildAudioSegments, deleteShotInPlan, mergeAudioSegmentInPlan, patchAudioSegmentInPlan, reorderShotInPlan, requiredStepCoverage, setStillDurationInPlan, snapMilliseconds, sourceSnapPoints, splitAudioAtOutputMS, splitShotAtOutputMS, targetIndexForOutputMS, trimShotInPlan } from "./timelineEditing";
import "./editorWorkspace.css";

type SaveState = "saved" | "dirty" | "saving" | "error";
type ImportMode = "upload" | "path" | "result" | "empty";
type MediaTab = "media" | "captions" | "callouts" | "generated" | "style";
type PreviewMode = "source" | "rendered";
type SelectedTrack = "video" | "audio";
type TimelineTrack = "evidence" | "video" | "caption" | "audio";
type EditorTool = "select" | "assets" | "text" | "shapes" | "captions" | "generated" | "style";
type AssetFilter = "all" | "video" | "image" | "audio";
type AssetRatioFilter = "all" | "landscape" | "portrait" | "square";
type ShapeKind = NonNullable<EditorOverlay["shape"]>;

type TimelineShot = {
  shot: EditorShot;
  index: number;
  startMS: number;
  endMS: number;
  durationMS: number;
};

type TimelineInteraction =
  | { kind: "trim"; shotID: string; edge: "start" | "end"; startClientX: number; startValueMS: number; basePlan: EditorPlan }
  | { kind: "still-duration"; shotID: string; startClientX: number; startValueMS: number; basePlan: EditorPlan }
  | { kind: "move"; shotID: string; basePlan: EditorPlan }
  | { kind: "playhead" };

type ShapeCanvasInteraction = {
  mode: "move" | "resize" | "rotate";
  overlayID: string;
  startX: number;
  startY: number;
  startLeft: number;
  startTop: number;
  startWidth: number;
  startHeight: number;
  canvasWidth: number;
  canvasHeight: number;
  centerClientX: number;
  centerClientY: number;
  startRotation: number;
  startPointerAngle: number;
  corner?: "nw" | "ne" | "sw" | "se";
  basePlan: EditorPlan;
};

export type ExportCheck = {
  id: string;
  label: string;
  detail: string;
  tone: "pass" | "warning" | "error";
};

export type ExportIssue = {
  id: string;
  title: string;
  detail: string;
  shotID?: string;
  overlayID?: string;
};

export function VideoEditor() {
  const client = useMemo(() => createEditorClient(), []);
  const [sessions, setSessions] = useState<EditorSession[]>([]);
  const [session, setSession] = useState<EditorSession>();
  const [draftPlan, setDraftPlan] = useState<EditorPlan>();
  const [history, setHistory] = useState(emptyEditorHistory);
  const [saveState, setSaveState] = useState<SaveState>("saved");
  const [selectedShotID, setSelectedShotID] = useState("");
  const [selectedAssetID, setSelectedAssetID] = useState("");
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
  const [activeTool, setActiveTool] = useState<EditorTool>("select");
  const [shapeMenuOpen, setShapeMenuOpen] = useState(false);
  const [assetQuery, setAssetQuery] = useState("");
  const [assetFilter, setAssetFilter] = useState<AssetFilter>("all");
  const [assetRatioFilter, setAssetRatioFilter] = useState<AssetRatioFilter>("all");
  const [selectedOverlayID, setSelectedOverlayID] = useState("");
  const [captionImportOpen, setCaptionImportOpen] = useState(false);
  const [captionImportText, setCaptionImportText] = useState("");
  const [validationOpen, setValidationOpen] = useState(false);
	const [automationOpen, setAutomationOpen] = useState(false);
  const [validationReport, setValidationReport] = useState<EditorValidationReport>();
  const [previewMode, setPreviewMode] = useState<PreviewMode>("source");
  const [playheadMS, setPlayheadMS] = useState(0);
  const [isPlaying, setIsPlaying] = useState(false);
  const [timelineScale, setTimelineScale] = useState(48);
  const [frameSnappingEnabled, setFrameSnappingEnabled] = useState(() => {
    try {
      return typeof window === "undefined" || window.localStorage.getItem("cascade.editor.frame-snapping") !== "false";
    } catch {
      return true;
    }
  });
  const [hiddenTracks, setHiddenTracks] = useState<Record<TimelineTrack, boolean>>({ evidence: false, video: false, caption: false, audio: false });
  const [timelineInteractionKind, setTimelineInteractionKind] = useState<TimelineInteraction["kind"]>();
  const [snapGuideMS, setSnapGuideMS] = useState<number>();
  const [timelineHoverMS, setTimelineHoverMS] = useState<number>();
  const [trackHeaderWidth, setTrackHeaderWidth] = useState(() => readTrackHeaderWidth());
  const [isResizingTrackHeader, setIsResizingTrackHeader] = useState(false);
  const [isTransformingShape, setIsTransformingShape] = useState(false);
  const [pendingFile, setPendingFile] = useState<File>();
  const [styleTemplates, setStyleTemplates] = useState<VideoStyleTemplate[]>([]);
  const [styleTemplateID, setStyleTemplateID] = useState("");
  const [stylePrompt, setStylePrompt] = useState("");
  const [styleReferenceID, setStyleReferenceID] = useState("");
  const [styleRightsConfirmed, setStyleRightsConfirmed] = useState(false);
  const [styleDraft, setStyleDraft] = useState<EditorStyleDraft>();
  const sessionRef = useRef<EditorSession>();
  const draftPlanRef = useRef<EditorPlan>();
  const videoRef = useRef<HTMLVideoElement>(null);
  const timelineScrollRef = useRef<HTMLDivElement>(null);
  const timelineContentRef = useRef<HTMLDivElement>(null);
  const saveInFlightRef = useRef(false);
  const editGroupRef = useRef<{ key: string; at: number }>();
  const pendingSeekRef = useRef<{ shotID: string; sourceSeconds: number; outputMS: number }>();
  const timelineInteractionRef = useRef<TimelineInteraction>();
  const trackHeaderResizeRef = useRef<{ startX: number; startWidth: number }>();
  const shapeCanvasInteractionRef = useRef<ShapeCanvasInteraction>();
  const shapeToolRef = useRef<HTMLDivElement>(null);
  const continuePlaybackRef = useRef(false);

  useEffect(() => {
    sessionRef.current = session;
  }, [session]);

  useEffect(() => {
    try {
      window.localStorage.setItem("cascade.editor.frame-snapping", String(frameSnappingEnabled));
    } catch {
      // Storage is a convenience only; editing remains available when it is disabled.
    }
  }, [frameSnappingEnabled]);

  useEffect(() => {
    try {
      window.localStorage.setItem("cascade.editor.track-header-width", String(trackHeaderWidth));
    } catch {
      // The timeline remains usable if the preferred visual width cannot persist.
    }
  }, [trackHeaderWidth]);

  useEffect(() => {
    if (!isResizingTrackHeader) return;
    const onMove = (event: PointerEvent) => {
      const interaction = trackHeaderResizeRef.current;
      if (!interaction) return;
      setTrackHeaderWidth(clampTrackHeaderWidth(interaction.startWidth + event.clientX - interaction.startX));
    };
    const onEnd = () => {
      trackHeaderResizeRef.current = undefined;
      setIsResizingTrackHeader(false);
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onEnd);
    return () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onEnd);
    };
  }, [isResizingTrackHeader]);

  useEffect(() => {
    if (!isTransformingShape) return;
    const onMove = (event: PointerEvent) => {
      const interaction = shapeCanvasInteractionRef.current;
      const plan = draftPlanRef.current;
      if (!interaction || !plan || !selectedShotID) return;
      const deltaX = (event.clientX - interaction.startX) / interaction.canvasWidth;
      const deltaY = (event.clientY - interaction.startY) / interaction.canvasHeight;
      let patch: Partial<EditorOverlay>;
      if (interaction.mode === "move") {
        patch = { x: clampNormalized(interaction.startLeft + deltaX, 1 - interaction.startWidth), y: clampNormalized(interaction.startTop + deltaY, 1 - interaction.startHeight) };
      } else if (interaction.mode === "resize") {
        const corner = interaction.corner ?? "se";
        const right = interaction.startLeft + interaction.startWidth;
        const bottom = interaction.startTop + interaction.startHeight;
        let left = interaction.startLeft;
        let top = interaction.startTop;
        let nextRight = right;
        let nextBottom = bottom;
        if (corner.includes("w")) left = clampNormalized(interaction.startLeft + deltaX, Math.max(0, right - 0.05));
        if (corner.includes("n")) top = clampNormalized(interaction.startTop + deltaY, Math.max(0, bottom - 0.05));
        if (corner.includes("e")) nextRight = Math.max(interaction.startLeft + 0.05, Math.min(1, right + deltaX));
        if (corner.includes("s")) nextBottom = Math.max(interaction.startTop + 0.05, Math.min(1, bottom + deltaY));
        const x = Math.min(left, 0.95);
        const y = Math.min(top, 0.95);
        const width = Math.max(0.05, nextRight - x);
        const height = Math.max(0.05, nextBottom - y);
        patch = { x, y, width, height };
      } else {
        const currentAngle = Math.atan2(event.clientY - interaction.centerClientY, event.clientX - interaction.centerClientX);
        patch = { rotation: Math.round(interaction.startRotation + normalizedAngleDelta(currentAngle - interaction.startPointerAngle) * 180 / Math.PI) };
      }
      const shots = plan.shots.map((shot) => shot.id !== selectedShotID ? shot : { ...shot, overlays: (shot.overlays ?? []).map((overlay) => overlay.id === interaction.overlayID ? { ...overlay, ...patch } : overlay) });
      const nextPlan = { ...plan, shots };
      // Keep fast pointer updates based on the latest drag result, not a stale render.
      draftPlanRef.current = nextPlan;
      setDraftPlan(nextPlan);
      setSaveState("dirty");
      setValidationReport(undefined);
    };
    const onEnd = () => {
      const interaction = shapeCanvasInteractionRef.current;
      shapeCanvasInteractionRef.current = undefined;
      setIsTransformingShape(false);
      if (interaction && draftPlanRef.current && !editorPlansEqual(interaction.basePlan, draftPlanRef.current)) {
        setHistory((current) => recordEditorHistory(current, interaction.basePlan));
        editGroupRef.current = undefined;
      }
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onEnd);
    return () => { window.removeEventListener("pointermove", onMove); window.removeEventListener("pointerup", onEnd); };
  }, [isTransformingShape, selectedShotID]);

  useEffect(() => {
    if (!shapeMenuOpen) return;
    const onPointerDown = (event: PointerEvent) => {
      if (shapeToolRef.current?.contains(event.target as Node)) return;
      setShapeMenuOpen(false);
    };
    window.addEventListener("pointerdown", onPointerDown);
    return () => window.removeEventListener("pointerdown", onPointerDown);
  }, [shapeMenuOpen]);

  useEffect(() => {
    draftPlanRef.current = draftPlan;
  }, [draftPlan]);

  useEffect(() => {
    void refreshSessions();
  }, []);

  useEffect(() => {
    void refreshStyleTemplates();
  }, []);

  useEffect(() => {
    const timer = window.setInterval(() => void refreshSessions(), 2000);
    return () => window.clearInterval(timer);
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
        setShapeMenuOpen(false);
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
  const selectedShapeOverlay = selectedShot?.overlays?.find((overlay) => overlay.id === selectedOverlayID && overlay.shape);
  const selectedIndex = selectedShot ? draftPlan?.shots.findIndex((shot) => shot.id === selectedShot.id) ?? -1 : -1;
  const selectedStep = session?.asset_catalog.steps?.find((step) => step.step_id === selectedShot?.source_step_id);
  const selectedArtifact = session?.asset_catalog.artifacts.find((artifact) => artifact.id === selectedAssetID) ?? session?.asset_catalog.artifacts.find((artifact) => artifact.id === selectedShot?.source_artifact_id);
  const timelineShots = useMemo(() => buildTimelineShots(draftPlan), [draftPlan]);
  const totalDurationMS = timelineShots.at(-1)?.endMS ?? 0;
  const audioSegments = useMemo(() => draftPlan ? buildAudioSegments(draftPlan, totalDurationMS) : [], [draftPlan, totalDurationMS]);
  const selectedAudioSegment = audioSegments.find((segment) => segment.id === selectedAudioSegmentID) ?? audioSegments[0];
  const presentationComposition = useMemo(() => session && draftPlan ? compilePresentationComposition(session, draftPlan) : undefined, [session, draftPlan]);
  const previewURL = session?.preview.status === "ready" && client.mode === "local" ? `${client.mediaURL(session.session_id, "preview")}?r=${session.preview.revision ?? session.revision}` : "";
  const sourceURL = session && selectedArtifact && client.mode === "local" ? client.mediaURL(session.session_id, "asset", selectedArtifact.id) : "";
  const mediaURL = previewMode === "rendered" && previewURL ? previewURL : sourceURL;
  const effectivePreviewMode: PreviewMode = previewMode === "rendered" && previewURL ? "rendered" : "source";
  const imagePreview = effectivePreviewMode === "source" && Boolean(selectedArtifact?.mime_type?.startsWith("image/"));
  const currentValidation = saveState === "saved" ? validationReport ?? session?.validation : undefined;
  const exportChecks = useMemo(() => buildExportChecks(session, draftPlan, currentValidation), [session, draftPlan, currentValidation]);
  const exportIssues = useMemo(() => buildExportIssues(draftPlan), [draftPlan]);
  const failedChecks = exportChecks.filter((check) => check.tone === "error");
  const passedChecks = exportChecks.filter((check) => check.tone === "pass");
  const timelineWidth = Math.max(840, Math.ceil(totalDurationMS / 1000) * timelineScale + 32);
  const selectedTimelineShot = timelineShots.find((item) => item.shot.id === selectedShot?.id);
  const sourcePreviewCaption = activeCaptionText(selectedShot, Math.max(0, playheadMS - (selectedTimelineShot?.startMS ?? 0)));
  const sourcePreviewAudio = useMemo(() => draftPlan ? audioPreviewState(draftPlan, totalDurationMS, playheadMS) : undefined, [draftPlan, totalDurationMS, playheadMS]);

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

  async function refreshStyleTemplates() {
    const result = await client.listStyleTemplates();
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "无法读取视频制作模板");
      return;
    }
    setStyleTemplates(result.data);
    setStyleTemplateID((current) => current || result.data?.[0]?.template_id || "");
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
    setSelectedAssetID(next.edit_plan.shots[0]?.source_artifact_id ?? next.asset_catalog.artifacts[0]?.id ?? "");
    setSelectedTrack("video");
    setSelectedAudioSegmentID("");
    setHiddenTracks({ evidence: false, video: false, caption: false, audio: false });
    setStyleDraft(undefined);
    setStyleReferenceID("");
    setStyleRightsConfirmed(false);
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

  async function importStyleReference(file?: File) {
    if (!session) return;
    setBusy("style-reference");
    const result = file
      ? await client.uploadStyleReference(session.session_id, file)
      : sourcePath.trim() ? await client.importStyleReference(session.session_id, sourcePath.trim()) : undefined;
    setBusy("");
    if (!result?.ok || !result.data) {
      setMessage(result?.error ?? "请选择一个参考视频");
      return;
    }
    loadSession(result.data);
    const reference = result.data.asset_catalog.artifacts.find(isStyleReferenceArtifact);
    setStyleReferenceID(reference?.id ?? "");
    setStyleRightsConfirmed(false);
    setMessage("参考视频已隔离登记：不会加入时间线或成片，需确认使用权后才可进入风格分析。");
  }

  async function createStyleDraft() {
    if (!session) return;
    setBusy("style-draft");
    const request = {
      expected_revision: session.revision,
      reference_rights_confirmed: styleRightsConfirmed,
      ...(styleTemplateID ? { template_id: styleTemplateID } : {}),
      ...(stylePrompt.trim() ? { prompt: stylePrompt.trim() } : {}),
      ...(styleReferenceID ? { reference_asset_id: styleReferenceID } : {}),
    };
    const result = await client.createStyleDraft(session.session_id, request);
    setBusy("");
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "制作草案生成失败");
      return;
    }
    setStyleDraft(result.data);
    setMessage("已生成可审阅的制作草案；原始时间线尚未被修改。");
  }

  async function applyStyleDraft() {
    if (!session || !styleDraft) return;
    setBusy("style-apply");
    const result = await client.applyStyleDraft(session.session_id, styleDraft.draft_id, session.revision);
    setBusy("");
    if (!result.ok || !result.data) {
      setMessage(result.error ?? "应用制作草案失败");
      return;
    }
    loadSession(result.data);
    setStyleDraft(undefined);
    setMessage("制作草案已应用。请生成预览确认画幅、剪辑、字幕与音频效果。");
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

  function beginStillDuration(event: React.PointerEvent<HTMLSpanElement>, item: TimelineShot) {
    if (!draftPlan || session?.status === "rendering" || event.button !== 0 || item.shot.presentation_kind !== "still") return;
    event.preventDefault();
    event.stopPropagation();
    event.currentTarget.setPointerCapture(event.pointerId);
    setSelectedShotID(item.shot.id);
    setSelectedTrack("video");
    const interaction: TimelineInteraction = {
      kind: "still-duration",
      shotID: item.shot.id,
      startClientX: event.clientX,
      startValueMS: item.durationMS,
      basePlan: draftPlan,
    };
    timelineInteractionRef.current = interaction;
    setTimelineInteractionKind("still-duration");
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
    if (interaction.kind === "still-duration") {
      const deltaMS = (clientX - interaction.startClientX) / timelineScale * 1000;
      const result = setStillDurationInPlan({
        plan: interaction.basePlan,
        shotID: interaction.shotID,
        durationMS: interaction.startValueMS + deltaMS,
        fps: session?.final_profile.fps ?? 30,
        snapToFrame: frameSnappingEnabled,
      });
      draftPlanRef.current = result.plan;
      setDraftPlan(result.plan);
      setSaveState(result.changed ? "dirty" : planSaveState(interaction.basePlan, sessionRef.current));
      setValidationReport(undefined);
      const item = buildTimelineShots(result.plan).find((candidate) => candidate.shot.id === interaction.shotID);
      if (item) {
        setSnapGuideMS(item.endMS);
        setPlayheadMS(Math.min(item.endMS, Math.max(item.startMS, playheadMS)));
      }
      return;
    }
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
      snapToFrame: frameSnappingEnabled,
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

  function updateTimelineHover(clientX: number) {
    const next = outputMSFromClientX(clientX);
    setTimelineHoverMS((current) => current !== undefined && Math.abs(current - next) < 15 ? current : next);
  }

  function updatePlayheadFromClientX(clientX: number) {
    const outputMS = outputMSFromClientX(clientX);
    const snapped = snapMilliseconds(outputMS, session?.final_profile.fps ?? 30, [], 0, frameSnappingEnabled);
    seekTimeline(snapped.valueMS);
  }

  function selectShotPreviewTime(shot: EditorShot, item: TimelineShot, outputMS: number) {
    if (shot.presentation_kind === "still") {
      pendingSeekRef.current = undefined;
      setPlayheadMS(Math.max(item.startMS, Math.min(item.endMS, outputMS)));
      return;
    }
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
      snapToFrame: frameSnappingEnabled,
      ...(artifact?.duration_ms === undefined ? {} : { sourceDurationMS: artifact.duration_ms }),
    });
    if (!result.changed) {
      if (result.blockedReason) setMessage(result.blockedReason);
      return;
    }
    commitPlan(result.plan, `range-${position}:${selectedShot.id}`);
  }

  function patchStillDuration(seconds: number) {
    if (!selectedShot || selectedShot.presentation_kind !== "still" || !Number.isFinite(seconds)) return;
    if (!draftPlan) return;
    const result = setStillDurationInPlan({
      plan: draftPlan,
      shotID: selectedShot.id,
      durationMS: seconds * 1000,
      fps: session?.final_profile.fps ?? 30,
      snapToFrame: frameSnappingEnabled,
    });
    if (!result.changed) return;
    commitPlan(result.plan, `still-duration:${selectedShot.id}`);
  }

  function addStillToTimeline(asset: EditorArtifact) {
    if (!draftPlan || !isTimelineStillAsset(asset)) {
      setMessage("只有已登记的步骤截图可以作为展示画面加入时间线。");
      return;
    }
    const id = `still_${Date.now()}`;
    const shot: EditorShot = {
      id,
      source_artifact_id: asset.id,
      ...(asset.source_step_id ? { source_step_id: asset.source_step_id } : {}),
      presentation_kind: "still",
      output_duration_ms: 1500,
      purpose: asset.label?.trim() || "步骤截图",
      operations: [],
      overlays: [],
    };
    const insertAt = selectedIndex >= 0 ? selectedIndex + 1 : draftPlan.shots.length;
    const shots = [...draftPlan.shots];
    shots.splice(insertAt, 0, shot);
    commitPlan({ ...draftPlan, shots }, `add-still:${asset.id}:${id}`);
    setSelectedShotID(id);
    setSelectedAssetID(asset.id);
    setSelectedTrack("video");
    setPreviewMode("source");
    setMessage("步骤截图已加入时间线，默认展示 1.5 秒；它只作展示参考，不代表业务步骤证据。");
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

  function addShapeOverlay(shape: ShapeKind) {
    if (!selectedShot) {
      setMessage("请先在时间线选择一个视频或图片片段，再添加标注。");
      return;
    }
    const durationMS = shotDuration(selectedShot);
    const overlay: EditorOverlay = {
      id: `shape_${Date.now()}`,
      type: "highlight_box",
      shape,
      start_ms: Math.max(0, playheadMS - (timelineShots.find((item) => item.shot.id === selectedShot.id)?.startMS ?? playheadMS)),
      end_ms: durationMS,
      x: 0.32,
      y: 0.3,
      width: 0.36,
      height: 0.24,
      color: "#dc58d5",
      stroke_width: 4,
    };
    overlay.end_ms = Math.max((overlay.start_ms ?? 0) + 500, durationMS);
    patchShot({ overlays: [...(selectedShot.overlays ?? []), overlay] }, `shape:${shape}`);
    setSelectedOverlayID(overlay.id!);
    setMediaTab("callouts");
    setActiveTool("shapes");
    setShapeMenuOpen(false);
    setMessage(`${shapeLabel(shape)}已添加到当前${selectedShot.presentation_kind === "still" ? "图片" : "视频"}片段。它会随片段时间和画面一起移动。`);
  }

  function patchSelectedOverlay(patch: Partial<EditorOverlay>) {
    if (!selectedShot || !selectedOverlayID) return;
    patchShot({ overlays: (selectedShot.overlays ?? []).map((overlay) => overlay.id === selectedOverlayID ? { ...overlay, ...patch } : overlay) }, `overlay:${selectedOverlayID}`);
  }

  function beginShapeMove(event: React.PointerEvent<HTMLButtonElement>, overlay: EditorOverlay) {
    beginShapeTransform(event, overlay, "move");
  }

  function beginShapeTransform(event: React.PointerEvent<HTMLButtonElement>, overlay: EditorOverlay, mode: ShapeCanvasInteraction["mode"], corner?: ShapeCanvasInteraction["corner"]) {
    if (!draftPlan || !selectedShot || !overlay.id || event.button !== 0) return;
    event.preventDefault();
    event.stopPropagation();
    const canvas = event.currentTarget.closest(".studio-shape-overlay-layer");
    const bounds = canvas?.getBoundingClientRect();
    if (!bounds || bounds.width <= 0 || bounds.height <= 0) return;
    const startLeft = overlay.x ?? 0.3;
    const startTop = overlay.y ?? 0.3;
    const startWidth = Math.max(0.05, overlay.width ?? 0.3);
    const startHeight = Math.max(0.05, overlay.height ?? 0.2);
    const centerClientX = bounds.left + bounds.width * (startLeft + startWidth / 2);
    const centerClientY = bounds.top + bounds.height * (startTop + startHeight / 2);
    setSelectedOverlayID(overlay.id);
    setSelectedTrack("video");
    shapeCanvasInteractionRef.current = { mode, overlayID: overlay.id, startX: event.clientX, startY: event.clientY, startLeft, startTop, startWidth, startHeight, canvasWidth: bounds.width, canvasHeight: bounds.height, centerClientX, centerClientY, startRotation: overlay.rotation ?? 0, startPointerAngle: Math.atan2(event.clientY - centerClientY, event.clientX - centerClientX), ...(corner ? { corner } : {}), basePlan: draftPlan };
    setIsTransformingShape(true);
  }

  function removeSelectedOverlay() {
    if (!selectedShot || !selectedOverlayID) return;
    patchShot({ overlays: (selectedShot.overlays ?? []).filter((overlay) => overlay.id !== selectedOverlayID) }, `remove-overlay:${selectedOverlayID}`);
    setSelectedOverlayID("");
  }

  function importCaptionsFromText(source: string) {
    if (!draftPlan) return;
    const lines = parseCaptionLines(source);
    if (!lines.length) {
      setMessage("没有读取到可用字幕。TXT 请每行一句；MD 会读取正文和列表项。");
      return;
    }
    const duration = Math.max(1000, totalDurationMS || lines.length * 2000);
    const slot = Math.max(700, Math.floor(duration / lines.length));
    const cues = lines.map((text, index) => ({ id: `caption_import_${Date.now()}_${index}`, text, output_range_ms: [index * slot, Math.min(duration, (index + 1) * slot)] as [number, number], source: "user_configured" as const }));
    commitPlan({ ...draftPlan, caption_cues: cues }, `caption-import:${Date.now()}`);
    setCaptionImportOpen(false);
    setCaptionImportText("");
    setMediaTab("captions");
    setMessage(`已导入 ${cues.length} 条字幕，并按当前成片时长均分；可继续在时间线上调整。`);
  }

  function beginTrackHeaderResize(event: React.PointerEvent<HTMLDivElement>) {
    event.preventDefault();
    event.stopPropagation();
    trackHeaderResizeRef.current = { startX: event.clientX, startWidth: trackHeaderWidth };
    setIsResizingTrackHeader(true);
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
      const result = splitAudioAtOutputMS({ plan: draftPlan, outputMS: playheadMS, totalDurationMS, fps: session?.final_profile.fps ?? 30, snapToFrame: frameSnappingEnabled });
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
    if (selectedShot?.presentation_kind === "still") {
      setMessage("步骤截图是静态展示画面，第一版请在右侧直接设置展示时长，暂不支持分割。");
      return;
    }
    const result = splitShotAtOutputMS({
      plan: draftPlan,
      outputMS: playheadMS,
      fps: session?.final_profile.fps ?? 30,
      newShotID: `shot_${Date.now()}`,
      snapToFrame: frameSnappingEnabled,
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
    setSelectedAssetID(shot.source_artifact_id);
    if (activateTrack) setSelectedTrack("video");
    setPreviewMode("source");
    const timelineShot = timelineShots.find((item) => item.shot.id === shot.id);
    const nextOutputMS = outputMS ?? timelineShot?.startMS ?? 0;
    const offsetMS = Math.max(0, nextOutputMS - (timelineShot?.startMS ?? 0));
    const sourceStartMS = shot.source_time_range_ms?.[0] ?? 0;
    selectShotPreviewTime(shot, timelineShot ?? { shot, index: 0, startMS: nextOutputMS - offsetMS, endMS: nextOutputMS - offsetMS + shotDuration(shot), durationMS: shotDuration(shot) }, nextOutputMS);
  }

  function selectAsset(asset: EditorArtifact) {
    setSelectedAssetID(asset.id);
    const shot = draftPlan?.shots.find((item) => item.source_artifact_id === asset.id);
    if (shot) {
      selectShot(shot);
      return;
    }
    videoRef.current?.pause();
    setIsPlaying(false);
    setPreviewMode("source");
    setMessage(isTimelineStillAsset(asset)
      ? "步骤截图已在预览区打开；确认需要展示后，可点击素材卡下方的“加入时间线”。"
      : `${assetKindLabel(asset)}已在预览区打开；该素材仅作展示，尚不能加入时间线。`);
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

  function chooseEditorTool(tool: EditorTool) {
    setActiveTool(tool);
    setShapeMenuOpen(false);
    if (tool === "assets" || tool === "select") setMediaTab("media");
    if (tool === "text" || tool === "captions") setMediaTab("captions");
    if (tool === "shapes") setMediaTab("callouts");
    if (tool === "generated") setMediaTab("generated");
    if (tool === "style") setMediaTab("style");
  }

  const panelTitle = activeTool === "style" ? "智能制作" : activeTool === "captions" ? "字幕库" : activeTool === "text" ? "文本与字幕" : activeTool === "shapes" ? "标注" : activeTool === "generated" ? "生成候选" : "素材";
	const automation = editorAutomationForDisplay(session);

  return (
    <div className="studio-shell">
      <header className="studio-topbar">
        <div className="studio-topbar-left">
          <select className="studio-project-select" aria-label="编辑项目" value={session?.session_id ?? ""} onChange={(event) => {
            const next = sessions.find((item) => item.session_id === event.target.value);
            if (next) loadSession(next);
          }}>
            <option value="">选择编辑项目</option>
            {sessions.map((item) => <option key={item.session_id} value={item.session_id}>{sessionSelectLabel(item)}</option>)}
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
		  {automation ? <button className={`studio-automation-button ${automationTone(automation.validation_state)}`} onClick={() => setAutomationOpen(true)} title="查看素材如何由 Server Browser Agent 执行、验证和修复">
			<span aria-hidden="true">✦</span><span>{automationStatusLabel(automation.validation_state)}</span>
		  </button> : null}
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
            <nav className="studio-tool-rail" aria-label="编辑工具">
              <button type="button" className={activeTool === "select" ? "active" : ""} aria-label="选择工具" title="选择工具" onClick={() => chooseEditorTool("select")}><span aria-hidden="true">⌖</span></button>
              <button type="button" className={activeTool === "assets" ? "active" : ""} aria-label="素材" title="素材" onClick={() => chooseEditorTool("assets")}><span aria-hidden="true">▣</span></button>
              <button type="button" className={activeTool === "text" ? "active" : ""} aria-label="文本与字幕" title="文本与字幕" onClick={() => chooseEditorTool("text")}><span aria-hidden="true">T</span></button>
              <div ref={shapeToolRef} className="studio-shape-tool">
                <button type="button" className={activeTool === "shapes" || shapeMenuOpen ? "active" : ""} aria-label="标注形状" title="标注形状" aria-expanded={shapeMenuOpen} aria-haspopup="menu" onClick={() => { chooseEditorTool("shapes"); setShapeMenuOpen((open) => !open); }}><span aria-hidden="true">[]</span></button>
                {shapeMenuOpen ? <div className="studio-shape-menu" role="menu" aria-label="标注形状">
                  <button type="button" role="menuitem" onClick={() => addShapeOverlay("rectangle")}><span>矩形</span><kbd>R</kbd></button>
                  <button type="button" role="menuitem" onClick={() => addShapeOverlay("circle")}><span>圆形</span><kbd>O</kbd></button>
                  <button type="button" role="menuitem" onClick={() => addShapeOverlay("polygon")}><span>多边形</span><kbd>P</kbd></button>
                  <button type="button" role="menuitem" onClick={() => addShapeOverlay("star")}><span>星形</span><kbd>Shift S</kbd></button>
                  <button type="button" role="menuitem" onClick={() => addShapeOverlay("line")}><span>直线</span><kbd>L</kbd></button>
                  <button type="button" role="menuitem" onClick={() => addShapeOverlay("arrow")}><span>箭头</span><kbd>Shift A</kbd></button>
                </div> : null}
              </div>
              <button type="button" className={activeTool === "captions" ? "active" : ""} aria-label="字幕样式" title="字幕样式" onClick={() => chooseEditorTool("captions")}><span aria-hidden="true">CC</span></button>
              <button type="button" className={activeTool === "generated" ? "active" : ""} aria-label="生成候选素材" title="生成候选素材" onClick={() => chooseEditorTool("generated")}><span aria-hidden="true">AI</span></button>
              <button type="button" className={activeTool === "style" ? "active" : ""} aria-label="智能制作" title="智能制作" onClick={() => chooseEditorTool("style")}><span aria-hidden="true">✦</span></button>
              <button type="button" aria-label="导入素材" title="导入素材" onClick={() => setImportOpen(true)}><span aria-hidden="true">⇧</span></button>
              <button type="button" aria-label="导出校验" title="导出校验" onClick={() => setValidationOpen(true)}><span aria-hidden="true">✓</span></button>
            </nav>
            <section className="studio-panel studio-media-panel">
              <div className="studio-panel-heading"><strong>{panelTitle}</strong><small>{session.asset_catalog.artifacts.length} 项</small></div>
              <div className="studio-panel-tabs">
                {(["media", "captions", "callouts", "generated", "style"] as MediaTab[]).map((tab) => (
                  <button key={tab} className={mediaTab === tab ? "active" : ""} onClick={() => setMediaTab(tab)}>{mediaTabLabel(tab)}</button>
                ))}
              </div>
              <div className="studio-media-content">
                {mediaTab === "media" ? <div className="studio-asset-browser-controls">
                  <input type="search" value={assetQuery} onChange={(event) => setAssetQuery(event.target.value)} placeholder="搜索本地视频、截图或音频" aria-label="搜索素材" />
                  <select value={assetFilter} aria-label="素材类型" onChange={(event) => setAssetFilter(event.target.value as AssetFilter)}>
                    <option value="all">全部类型</option>
                    <option value="video">视频</option>
                    <option value="image">图片</option>
                    <option value="audio">音频</option>
                  </select>
                  <select value={assetRatioFilter} aria-label="图片与视频画面比例" onChange={(event) => setAssetRatioFilter(event.target.value as AssetRatioFilter)}>
                    <option value="all">全部比例</option>
                    <option value="landscape">横向</option>
                    <option value="portrait">竖向</option>
                    <option value="square">方形</option>
                  </select>
                </div> : null}
                {mediaTab === "media" ? <button className="studio-import-zone" onClick={() => setImportOpen(true)}>＋ 从本机选择，或导入结果包</button> : null}
                {mediaTab === "style" ? <StyleProductionPanel templates={styleTemplates} templateID={styleTemplateID} prompt={stylePrompt} referenceID={styleReferenceID} references={session.asset_catalog.artifacts.filter(isStyleReferenceArtifact)} rightsConfirmed={styleRightsConfirmed} {...(styleDraft ? { draft: styleDraft } : {})} busy={busy} onTemplateChange={setStyleTemplateID} onPromptChange={setStylePrompt} onReferenceChange={setStyleReferenceID} onRightsChange={setStyleRightsConfirmed} onReferenceUpload={(file) => void importStyleReference(file)} onCreateDraft={() => void createStyleDraft()} onApplyDraft={() => void applyStyleDraft()} /> : <MediaPanelContent tab={mediaTab} session={session} plan={draftPlan} selectedShotID={selectedShot?.id} selectedAssetID={selectedArtifact?.id} assetQuery={assetQuery} assetFilter={assetFilter} assetRatioFilter={assetRatioFilter} selectedOverlayID={selectedOverlayID} onSelectShot={(shot) => selectShot(shot)} onSelectAsset={selectAsset} onAddStill={addStillToTimeline} onAddCaption={() => setCaptionImportOpen(true)} onAddShape={addShapeOverlay} onSelectOverlay={(shot, overlay) => { selectShot(shot); setSelectedOverlayID(overlay.id ?? ""); }} />}
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
                    {imagePreview ? <img className="studio-image-preview" src={mediaURL} alt={selectedArtifact?.label ?? "步骤截图"} /> : <video
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
                    />}
                    {effectivePreviewMode === "source" && !imagePreview && sourcePreviewCaption ? <div className="studio-caption-preview">{sourcePreviewCaption}</div> : null}
                    {effectivePreviewMode === "source" ? <ShapeOverlayPreview shot={selectedShot} playheadMS={playheadMS} timelineShots={timelineShots} selectedOverlayID={selectedOverlayID} onSelect={(id) => { setSelectedTrack("video"); setSelectedOverlayID(id); }} onMoveStart={beginShapeMove} onTransformStart={beginShapeTransform} /> : null}
                    {selectedStep && !imagePreview ? <div className={`studio-step-preview ${stepTone(selectedStep.status)}`}>步骤 {stepDisplayNumber(selectedStep, session.asset_catalog.steps ?? [])} · {stepTitle(selectedStep)}</div> : null}
                  </div>
                ) : (
                  <div className="studio-video-placeholder"><strong>还没有可预览的素材</strong><span>导入本地录屏或 RecordingResultPackage 后开始编辑。</span><button className="studio-primary-button" onClick={() => setImportOpen(true)}>导入素材</button></div>
                )}
              </div>
			  <div className="studio-transport" aria-label="预览播放控制">
                <button className="studio-icon-button" disabled={!mediaURL || imagePreview} onClick={() => seekTimeline(playheadMS - 1000 / session.preview_profile.fps)}>│‹</button>
                <button className="studio-play-button" disabled={!mediaURL || imagePreview} onClick={togglePlayback}>{isPlaying ? "Ⅱ" : "▶"}</button>
                <button className="studio-icon-button" disabled={!mediaURL || imagePreview} onClick={() => seekTimeline(playheadMS + 1000 / session.preview_profile.fps)}>›│</button>
                <span>{formatTimecode(playheadMS)} / {formatTimecode(totalDurationMS)}</span>
                <span className="studio-render-status">预览：{renderStateLabel(session.preview)} · 成片：{renderStateLabel(session.final_render)}</span>
              </div>
            </section>

            <aside className="studio-panel studio-properties-panel">
              <div className="studio-panel-heading"><strong>{selectedTrack === "audio" ? "音频" : selectedShapeOverlay ? shapeLabel(selectedShapeOverlay.shape!) : "属性"}</strong><small>{selectedTrack === "audio" && selectedAudioSegment ? `音频 ${selectedAudioSegment.index + 1}/${audioSegments.length}` : selectedShapeOverlay ? "标注图层" : selectedShot ? `视频 ${selectedIndex + 1}/${draftPlan?.shots.length ?? 0}` : "未选择"}</small></div>
              <div className="studio-inspector-tabs" role="tablist" aria-label="属性面板">
                <button type="button" role="tab" aria-selected={selectedTrack === "video"} className={selectedTrack === "video" ? "active" : ""} onClick={() => setSelectedTrack("video")}>{selectedShapeOverlay ? "属性" : "片段"}</button>
                <button type="button" role="tab" aria-selected={selectedTrack === "audio"} className={selectedTrack === "audio" ? "active" : ""} disabled={!selectedAudioSegment} onClick={() => setSelectedTrack("audio")}>音频</button>
              </div>
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
                </div>
              ) : selectedShapeOverlay ? (
                <ShapePropertiesPanel overlay={selectedShapeOverlay} onPatch={patchSelectedOverlay} onRemove={removeSelectedOverlay} />
              ) : selectedShot ? (
                <div className="studio-properties-scroll">
                  <PropertySection title="片段" meta={selectedShot.id}>
                    <label className="studio-field"><span>片段说明</span><input value={selectedShot.purpose} onChange={(event) => patchShot({ purpose: event.target.value }, "purpose")} /></label>
                    {selectedShot.presentation_kind === "still" ? <>
                      <label className="studio-field"><span>展示时长（秒）</span><input type="number" min="0.25" max="15" step="0.1" value={(selectedShot.output_duration_ms ?? 1500) / 1000} onChange={(event) => patchStillDuration(Number(event.target.value))} /></label>
                      <small className="studio-property-note">静态截图将按此时长铺满画面并生成静音音轨；仅作展示参考，不替代业务步骤证据。</small>
                    </> : <>
                      <div className="studio-field-pair">
                        <label className="studio-field"><span>入点</span><input type="number" min="0" step="0.1" value={(selectedShot.source_time_range_ms?.[0] ?? 0) / 1000} onChange={(event) => patchRange(0, Number(event.target.value))} /></label>
                        <label className="studio-field"><span>出点</span><input type="number" min="0.5" step="0.1" value={(selectedShot.source_time_range_ms?.[1] ?? 1000) / 1000} onChange={(event) => patchRange(1, Number(event.target.value))} /></label>
                      </div>
                      <label className="studio-field"><span>持续时间</span><input value={formatTimecode(shotDuration(selectedShot))} readOnly /></label>
                    </>}
                  </PropertySection>

                  <PropertySection title="字幕" meta="确定性烧录">
                    <label className="studio-field"><span>烧录文本</span><textarea rows={4} value={captionText(selectedShot)} onChange={(event) => patchCaption(event.target.value)} placeholder="输入需要烧录到画面的字幕" /></label>
                  </PropertySection>

                  {(selectedShot.overlays ?? []).some((overlay) => overlay.shape) ? <PropertySection title="标注形状" meta="绑定当前片段">
                    <div className="studio-overlay-list">{(selectedShot.overlays ?? []).filter((overlay) => overlay.shape).map((overlay) => <button key={overlay.id ?? overlay.shape} className={selectedOverlayID === overlay.id ? "selected" : ""} onClick={() => setSelectedOverlayID(overlay.id ?? "")}><span className={`studio-shape-swatch ${overlay.shape}`} /><strong>{shapeLabel(overlay.shape!)}</strong></button>)}</div>
                    {selectedOverlayID ? <button className="studio-outline-button" onClick={() => setSelectedOverlayID(selectedOverlayID)}>在右侧编辑属性</button> : null}
                    <small className="studio-property-note">当前标注会显示在编辑预览和时间线；最终 MP4 的形状像素渲染仍待 Renderer 接入，导出校验会明确阻止误导性导出。</small>
                  </PropertySection> : null}

                  {selectedShot.presentation_kind !== "still" ? <PropertySection title="音频" meta={draftPlan?.audio?.mode === "mute" ? "静音" : "源音轨"}>
                    <div className="studio-radio-row">
                      <label><input type="radio" name="studio-audio-mode" checked={(draftPlan?.audio?.mode ?? "source") === "source"} onChange={() => patchAudio({ mode: "source" })} />保留源音频</label>
                      <label><input type="radio" name="studio-audio-mode" checked={draftPlan?.audio?.mode === "mute"} onChange={() => patchAudio({ mode: "mute" })} />静音</label>
                    </div>
                    <label className="studio-field"><span>音量 {draftPlan?.audio?.volume_percent ?? 100}%</span><input type="range" min="0" max="200" step="5" disabled={draftPlan?.audio?.mode === "mute"} value={draftPlan?.audio?.volume_percent ?? 100} onChange={(event) => patchAudio({ volume_percent: Number(event.target.value) })} /></label>
                  </PropertySection> : null}

                  <PropertySection title={selectedShot.presentation_kind === "still" ? "展示素材" : "业务证据"} meta={selectedShot.presentation_kind === "still" ? "不代表业务步骤" : selectedStep?.required ? "事实锁定" : "展示片段"}>
                    <div className={`studio-evidence-card ${selectedStep ? stepTone(selectedStep.status) : "neutral"}`}>
                      <EvidenceFact label="来源步骤" value={selectedShot.source_step_id ?? "未绑定（展示素材）"} />
                      <EvidenceFact label="执行状态" value={selectedStep?.status ?? "本地素材"} />
                      <EvidenceFact label={selectedShot.presentation_kind === "still" ? "展示时长" : "源时间范围"} value={selectedShot.presentation_kind === "still" ? formatTimecode(shotDuration(selectedShot)) : selectedShot.source_time_range_ms ? `${formatTimecode(selectedShot.source_time_range_ms[0])}—${formatTimecode(selectedShot.source_time_range_ms[1])}` : "未声明"} />
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
                <label className={`studio-snap-toggle ${frameSnappingEnabled ? "enabled" : ""}`} title="开启后，拖动播放头、裁剪和分割会对齐到视频帧；业务步骤边界仍始终受保护。"><input type="checkbox" checked={frameSnappingEnabled} onChange={(event) => setFrameSnappingEnabled(event.currentTarget.checked)} /><span>帧吸附</span></label>
              </div>
              <div className="studio-timeline-tools"><span>{presentationComposition?.durationInFrames ?? 0} 帧 · 总时长 {formatTimecode(totalDurationMS)}</span><input type="range" min="28" max="100" value={timelineScale} onChange={(event) => setTimelineScale(Number(event.target.value))} /></div>
            </div>
            <div className={`studio-timeline-body ${isResizingTrackHeader ? "resizing-track-header" : ""}`} ref={timelineScrollRef} style={{ gridTemplateColumns: `${trackHeaderWidth}px minmax(0, 1fr)` }}>
              <div className="studio-track-labels">
                <div />
                <TrackLabel track="evidence" label="业务步骤" locked hidden={hiddenTracks.evidence} onToggle={toggleTimelineTrack} />
                <TrackLabel track="video" label="视频" hidden={hiddenTracks.video} onToggle={toggleTimelineTrack} />
                <TrackLabel track="caption" label="字幕 / 标注" hidden={hiddenTracks.caption} onToggle={toggleTimelineTrack} />
                <TrackLabel track="audio" label="音频" hidden={hiddenTracks.audio} onToggle={toggleTimelineTrack} />
                <div className="studio-track-header-resizer" role="separator" aria-label="调整轨道标题宽度" aria-orientation="vertical" onPointerDown={beginTrackHeaderResize} />
              </div>
              <div ref={timelineContentRef} className={`studio-timeline-scroll ${timelineInteractionKind ? `interacting ${timelineInteractionKind}` : ""}`} style={{ width: timelineWidth, backgroundSize: `${timeRulerStep(totalDurationMS) * timelineScale}px 100%` }} onPointerDown={beginPlayheadDrag} onPointerMove={(event) => updateTimelineHover(event.clientX)} onPointerLeave={() => setTimelineHoverMS(undefined)}>
                <TimeRuler totalDurationMS={totalDurationMS} scale={timelineScale} />
                <div className={`studio-track-row studio-evidence-track ${hiddenTracks.evidence ? "track-hidden" : ""}`}>
                  {!hiddenTracks.evidence && timelineShots.map((item) => {
                    if (item.shot.presentation_kind === "still") return <div key={item.shot.id} className="studio-evidence-clip presentation" style={clipStyle(item, timelineScale)}><span>◇</span><strong>展示截图（非业务证据）</strong></div>;
                    const step = session.asset_catalog.steps?.find((candidate) => candidate.step_id === item.shot.source_step_id);
                    return <div key={item.shot.id} className={`studio-evidence-clip ${step ? stepTone(step.status) : "neutral"}`} style={clipStyle(item, timelineScale)}><span>{step?.required ? "🔒" : "◇"}</span><strong>{step ? `${stepDisplayNumber(step, session.asset_catalog.steps ?? [])} ${stepTitle(step)}` : "本地展示片段"}</strong></div>;
                  })}
                </div>
                <div className={`studio-track-row studio-video-track ${hiddenTracks.video ? "track-hidden" : ""}`}>
                  {!hiddenTracks.video && timelineShots.map((item) => (
                    <button
                      key={item.shot.id}
                      className={`studio-timeline-clip video ${item.shot.presentation_kind === "still" ? "still" : ""} ${selectedTrack === "video" && selectedShot?.id === item.shot.id ? "selected" : ""} ${timelineInteractionKind === "move" && selectedShotID === item.shot.id ? "dragging" : ""}`}
                      style={clipStyle(item, timelineScale)}
                      onPointerDown={(event) => beginMove(event, item)}
                      onClick={() => selectVideoClip(item)}
                    >
                      {item.shot.presentation_kind !== "still" ? <span className="studio-trim-handle start" title="拖动裁剪入点" onPointerDown={(event) => beginTrim(event, item, "start")} /> : null}
                      <span className="studio-clip-index">{item.index + 1}</span>
                      <strong>{item.shot.presentation_kind === "still" ? `▣ ${item.shot.purpose}` : item.shot.purpose}</strong>
                      <small>{formatDuration(item.durationMS)}</small>
                      {item.shot.presentation_kind === "still"
                        ? <span className="studio-trim-handle still-duration" title="拖动调整展示时长" onPointerDown={(event) => beginStillDuration(event, item)} />
                        : <span className="studio-trim-handle end" title="拖动裁剪出点" onPointerDown={(event) => beginTrim(event, item, "end")} />}
                    </button>
                  ))}
                </div>
                <div className={`studio-track-row studio-caption-track ${hiddenTracks.caption ? "track-hidden" : ""}`}>
                  {!hiddenTracks.caption && timelineShots.map((item) => <>{captionText(item.shot) ? <button key={`${item.shot.id}-caption`} className="studio-timeline-clip caption" style={clipStyle(item, timelineScale)} onPointerDown={(event) => event.stopPropagation()} onClick={() => selectShot(item.shot)}><strong>{captionText(item.shot)}</strong></button> : null}{(item.shot.overlays ?? []).filter((overlay) => overlay.shape).map((overlay, index) => <button key={overlay.id ?? `${item.shot.id}-shape-${index}`} className={`studio-timeline-clip callout ${selectedOverlayID === overlay.id ? "selected" : ""}`} style={clipStyle(item, timelineScale)} onPointerDown={(event) => event.stopPropagation()} onClick={() => { selectShot(item.shot); setSelectedOverlayID(overlay.id ?? ""); }}><strong>{shapeLabel(overlay.shape!)}</strong></button>)}</>)}
                </div>
                <div className={`studio-track-row studio-audio-track ${hiddenTracks.audio ? "track-hidden" : ""}`}>
                  {!hiddenTracks.audio && audioSegments.map((segment) => {
                    const clipWidth = Math.max(1, segment.durationMS / 1000 * timelineScale);
                    return <button key={segment.id} className={`studio-audio-clip ${segment.mode === "mute" ? "muted" : ""} ${selectedTrack === "audio" && selectedAudioSegment?.id === segment.id ? "selected" : ""}`} style={{ left: segment.startMS / 1000 * timelineScale, width: clipWidth }} title={`${segment.mode === "mute" ? "静音" : `音量 ${segment.volumePercent}%`} · ${formatTimecode(segment.startMS)}—${formatTimecode(segment.endMS)}`} onPointerDown={(event) => event.stopPropagation()} onClick={(event) => { event.stopPropagation(); selectAudioSegment(segment); }}><strong>{segment.mode === "mute" ? "静音" : `源音频 ${segment.index + 1} · ${segment.volumePercent}%`}</strong><small>{formatDuration(segment.durationMS)}</small></button>;
                  })}
                </div>
                {timelineHoverMS !== undefined ? <div className="studio-timeline-hover-guide" style={{ left: timelineHoverMS / 1000 * timelineScale }}><span>{formatTimecode(timelineHoverMS)}</span></div> : null}
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
      {captionImportOpen ? <CaptionImportDialog value={captionImportText} onChange={setCaptionImportText} onClose={() => setCaptionImportOpen(false)} onImport={importCaptionsFromText} /> : null}
      {validationOpen ? <ValidationDrawer checks={exportChecks} issues={exportIssues} report={currentValidation} onClose={() => setValidationOpen(false)} onFocusIssue={(issue) => { if (issue.shotID) setSelectedShotID(issue.shotID); if (issue.overlayID) setSelectedOverlayID(issue.overlayID); setSelectedTrack("video"); setValidationOpen(false); }} /> : null}
	  {automationOpen && automation ? <AutomationDrawer automation={automation} onClose={() => setAutomationOpen(false)} /> : null}
      {message ? <div className="studio-toast"><span>{message}</span><button onClick={() => setMessage("")}>×</button></div> : null}
    </div>
  );
}

export function editorAutomationForDisplay(session?: EditorSession): EditorSession["automation"] {
	if (session?.automation) return session.automation;
	if (!session?.asset_catalog.source?.recording_result_package_id) return undefined;
	return {
		execution_runtime: "",
		validation_state: "legacy_result",
		validation_report_count: 0,
		evidence_backed_report_count: 0,
		patch_count: 0,
		applied_patch_count: 0,
		rolled_back_patch_count: 0,
		stage_event_audit_available: false,
	};
}

function AutomationDrawer({ automation, onClose }: { automation: NonNullable<EditorSession["automation"]>; onClose: () => void }) {
	const runtimeIsOutline = automation.execution_runtime === "browser-agent-outline-v1";
	return (
		<div className="studio-automation-scrim" role="presentation" onPointerDown={(event) => { if (event.currentTarget === event.target) onClose(); }}>
			<aside className="studio-automation-drawer" role="dialog" aria-modal="true" aria-label="智能执行记录">
				<div className="studio-drawer-header"><div><strong>智能执行记录</strong><small>来自 Server 结果包，只显示可追溯事实</small></div><button className="studio-icon-button" onClick={onClose}>×</button></div>
				<div className={`studio-automation-summary ${automationTone(automation.validation_state)}`}>
					<span aria-hidden="true">{automation.validation_state === "blocked" ? "!" : "✓"}</span>
					<div><strong>{automationStatusLabel(automation.validation_state)}</strong><p>{automationStatusDetail(automation)}</p></div>
				</div>
				<div className="studio-automation-metrics">
					<AutomationMetric label="执行方式" value={runtimeIsOutline ? "浏览器智能执行" : automation.execution_runtime ? "兼容脚本执行" : "结果包未声明"} />
					<AutomationMetric label="验证报告" value={`${automation.evidence_backed_report_count}/${automation.validation_report_count} 份有真实证据`} />
					<AutomationMetric label="运行时修复" value={`${automation.applied_patch_count}/${automation.patch_count} 项已应用`} />
					<AutomationMetric label="事件审计" value={automation.stage_event_audit_available ? "已保留" : "结果包未附带"} />
				</div>
				<div className="studio-automation-boundary">
					<strong>系统可以做什么</strong><p>可以修正元素定位、等待策略和截图时机等机械问题，并在权限规则通过后继续执行。</p>
					<strong>系统不会做什么</strong><p>不会修改 App 已审批的业务步骤、输入含义、执行顺序、允许域名或安全边界。</p>
				</div>
				{automation.rolled_back_patch_count > 0 ? <div className="studio-automation-warning">有 {automation.rolled_back_patch_count} 项修复已回滚，请在导出前复核对应素材。</div> : null}
				<small className="studio-automation-source">来源包：{automation.source_package_id ?? "未声明"} · 最新决策：{automationDecisionLabel(automation.latest_decision)}</small>
			</aside>
		</div>
	);
}

function AutomationMetric({ label, value }: { label: string; value: string }) {
	return <div><span>{label}</span><strong>{value}</strong></div>;
}

export function automationStatusLabel(state: string): string {
	return ({
		verified: "智能执行已验证",
		repaired_or_review: "已自动修复，建议复核",
		blocked: "执行验证未通过",
		result_only: "执行结果已接收",
		legacy_result: "兼容模式结果",
	} as Record<string, string>)[state] ?? "智能执行状态未知";
}

function automationStatusDetail(automation: NonNullable<EditorSession["automation"]>): string {
	if (automation.validation_state === "verified") return "业务步骤已有真实浏览器观察或产物证据，可以进入视频编排。";
	if (automation.validation_state === "repaired_or_review") return "执行过程中发生了协议允许的小修复，修改记录已保留。";
	if (automation.validation_state === "blocked") return "验证器要求停止或重新理解，当前素材不应被当作业务成功证据。";
	if (automation.validation_state === "legacy_result") return "这是旧执行链产物，未携带新 Browser Agent 的阶段验证报告。";
	return "结果包已进入编辑器，但尚未携带新架构的阶段验证报告。";
}

function automationTone(state: string): string {
	if (state === "blocked") return "blocked";
	if (state === "repaired_or_review") return "review";
	if (state === "verified") return "verified";
	return "neutral";
}

function automationDecisionLabel(value: NonNullable<EditorSession["automation"]>["latest_decision"]): string {
	return ({ continue: "继续执行", repair_allowed: "允许受控修复", stop_and_report: "停止并报告", reunderstanding_required: "需要重新理解" } as Record<string, string>)[value ?? ""] ?? "未声明";
}

function StyleProductionPanel({ templates, templateID, prompt, referenceID, references, rightsConfirmed, draft, busy, onTemplateChange, onPromptChange, onReferenceChange, onRightsChange, onReferenceUpload, onCreateDraft, onApplyDraft }: { templates: VideoStyleTemplate[]; templateID: string; prompt: string; referenceID: string; references: EditorArtifact[]; rightsConfirmed: boolean; draft?: EditorStyleDraft; busy: string; onTemplateChange: (value: string) => void; onPromptChange: (value: string) => void; onReferenceChange: (value: string) => void; onRightsChange: (value: boolean) => void; onReferenceUpload: (file: File) => void; onCreateDraft: () => void; onApplyDraft: () => void }) {
  const selectedTemplate = templates.find((item) => item.template_id === templateID);
  return <div className="studio-style-production">
    <div className="studio-style-intro"><strong>从素材直接制作成片</strong><p>选择模板，补充一句制作目标；系统先生成草案，再由你确认应用。真实业务步骤、原始素材和顺序不会被改写。</p></div>
    <label className="studio-field"><span>成片模板</span><select value={templateID} onChange={(event) => onTemplateChange(event.target.value)}>{templates.map((item) => <option key={item.template_id} value={item.template_id}>{item.name}</option>)}</select></label>
    {selectedTemplate ? <div className="studio-style-template-note"><strong>{selectedTemplate.summary}</strong><small>{selectedTemplate.output.aspect_ratio} · {Math.round(selectedTemplate.output.target_duration_ms / 1000)} 秒目标 · 源音量 {selectedTemplate.presentation.source_volume_percent}%</small></div> : null}
    <label className="studio-field"><span>制作提示</span><textarea rows={4} value={prompt} onChange={(event) => onPromptChange(event.target.value)} placeholder="例如：用简洁、可信的语气介绍三项功能，保留最后的结果画面。" /></label>
    <div className="studio-style-reference"><div><strong>参考视频（可选）</strong><small>只提取节奏、字幕、画幅等可解释参数；不会复制画面、人物、标识、音乐、文案或镜头。</small></div><label className="studio-style-upload"><span>上传参考视频</span><input type="file" accept="video/mp4,video/webm,video/quicktime,.m4v" disabled={busy !== ""} onChange={(event) => { const file = event.target.files?.[0]; if (file) onReferenceUpload(file); event.currentTarget.value = ""; }} /></label></div>
    {references.length ? <><label className="studio-field"><span>已登记参考视频</span><select value={referenceID} onChange={(event) => onReferenceChange(event.target.value)}><option value="">不使用参考视频</option>{references.map((asset) => <option key={asset.id} value={asset.id}>{asset.label || asset.id}</option>)}</select></label>{referenceID ? <label className="studio-style-rights"><input type="checkbox" checked={rightsConfirmed} onChange={(event) => onRightsChange(event.currentTarget.checked)} />我确认拥有该参考视频的使用权，并同意仅提取可解释的风格参数。</label> : null}</> : null}
    <button className="studio-primary-button" disabled={busy !== "" || (Boolean(referenceID) && !rightsConfirmed)} onClick={onCreateDraft}>{busy === "style-draft" ? "正在生成草案" : "生成制作草案"}</button>
    {draft ? <div className="studio-style-draft"><div><strong>待确认制作草案</strong><span>{draft.template.name} · {draft.proposed_final_profile.width}×{draft.proposed_final_profile.height} · {draft.proposed_final_profile.fps} FPS</span></div><p>{draft.style_profile.analysis_status === "pending_analysis" ? "参考视频已登记，自动风格分析尚未接入；当前先按所选模板生成。" : "当前草案来自平台模板，可直接预览确认。"}</p>{draft.warnings?.map((warning) => <small key={warning.code}>{warning.message}</small>)}<button className="studio-outline-button" disabled={busy !== "" || !draft.requires_confirmation} onClick={onApplyDraft}>{busy === "style-apply" ? "正在应用" : "确认应用并生成预览"}</button></div> : null}
  </div>;
}

function MediaPanelContent({ tab, session, plan, selectedShotID, selectedAssetID, assetQuery, assetFilter, assetRatioFilter, selectedOverlayID, onSelectShot, onSelectAsset, onAddStill, onAddCaption, onAddShape, onSelectOverlay }: { tab: Exclude<MediaTab, "style">; session: EditorSession; plan: EditorPlan | undefined; selectedShotID: string | undefined; selectedAssetID: string | undefined; assetQuery: string; assetFilter: AssetFilter; assetRatioFilter: AssetRatioFilter; selectedOverlayID: string; onSelectShot: (shot: EditorShot) => void; onSelectAsset: (asset: EditorArtifact) => void; onAddStill: (asset: EditorArtifact) => void; onAddCaption: () => void; onAddShape: (shape: ShapeKind) => void; onSelectOverlay: (shot: EditorShot, overlay: EditorOverlay) => void }) {
  if (tab === "captions") {
    const shots = plan?.shots.filter((shot) => captionText(shot)) ?? [];
    const cues = plan?.caption_cues ?? [];
    return <div className="studio-list-content"><button className="studio-panel-create-button" onClick={onAddCaption}><strong>导入 TXT / MD 字幕</strong><small>每行一句，自动均分到当前成片时长</small></button>{shots.map((shot) => <button key={shot.id} className={selectedShotID === shot.id ? "selected" : ""} onClick={() => onSelectShot(shot)}><strong>{captionText(shot)}</strong><small>{shot.purpose}</small></button>)}{cues.map((cue) => <article key={cue.id} className="studio-caption-cue"><strong>{cue.text}</strong><small>{formatTimecode(cue.output_range_ms[0])} - {formatTimecode(cue.output_range_ms[1])}</small></article>)}{shots.length === 0 && cues.length === 0 ? <PanelEmpty text="尚未添加字幕。可导入 TXT 或 MD，也可在右侧为某个片段直接输入。" /> : null}</div>;
  }
  if (tab === "callouts") {
    const rows = plan?.shots.flatMap((shot) => (shot.overlays ?? []).filter((overlay) => overlay.type !== "caption").map((overlay) => ({ shot, overlay }))) ?? [];
    return <div className="studio-list-content"><div className="studio-shape-quick-actions">{(["rectangle", "circle", "line", "arrow"] as ShapeKind[]).map((shape) => <button key={shape} onClick={() => onAddShape(shape)}><span className={`studio-shape-swatch ${shape}`} /><strong>{shapeLabel(shape)}</strong></button>)}</div>{rows.map(({ shot, overlay }, index) => <button key={overlay.id ?? `${shot.id}-${index}`} className={selectedOverlayID === overlay.id ? "selected" : ""} onClick={() => onSelectOverlay(shot, overlay)}><strong>{overlay.shape ? shapeLabel(overlay.shape) : overlayTypeLabel(overlay.type)}</strong><small>{shot.purpose}</small></button>)}{rows.length === 0 ? <PanelEmpty text="先选择一个视频或图片片段，再添加高亮框、圆形、直线或箭头。" /> : null}</div>;
  }
  if (tab === "generated") {
    const generated = session.asset_catalog.artifacts.filter(isGeneratedArtifact);
    const capability = session.provider_capabilities.find((item) => item.provider === "seedance");
    return <div className="studio-generated-content"><div className="studio-provider-card"><div><strong>Seedance</strong><span>{capability?.configured ? capability.mode : "未配置"}</span></div><p>候选素材必须人工审查，不能自动加入时间线或代表真实业务步骤。</p><small>auto_include=false · presentation_only=true</small></div>{generated.map((artifact) => <AssetCard key={artifact.id} artifact={artifact} generated />)}{generated.length === 0 ? <PanelEmpty text="当前没有生成候选素材。" /> : null}</div>;
  }
  const query = assetQuery.trim().toLocaleLowerCase("zh-CN");
  const assets = session.asset_catalog.artifacts.filter((artifact) => {
    if (isGeneratedArtifact(artifact)) return false;
    const mime = artifact.mime_type ?? "";
    const kind = artifact.kind.toLocaleLowerCase("en-US");
    const matchesType = assetFilter === "all" || (assetFilter === "video" && (mime.startsWith("video/") || kind.includes("recording"))) || (assetFilter === "image" && mime.startsWith("image/")) || (assetFilter === "audio" && (mime.startsWith("audio/") || kind.includes("audio")));
    const matchesRatio = matchesAssetRatio(artifact, assetRatioFilter);
    const text = `${artifact.label ?? ""} ${artifact.id} ${artifact.kind}`.toLocaleLowerCase("zh-CN");
    return matchesType && matchesRatio && (!query || text.includes(query));
  });
  return (
    <>
      <p className="studio-section-label">真实执行素材</p>
      <div className="studio-asset-grid">
        {assets.map((artifact) => {
          const shot = plan?.shots.find((candidate) => candidate.source_artifact_id === artifact.id);
          return <AssetCard key={artifact.id} artifact={artifact} selected={artifact.id === selectedAssetID || shot?.id === selectedShotID} onClick={() => onSelectAsset(artifact)} {...(isTimelineStillAsset(artifact) ? { actionLabel: "加入时间线", onAction: () => onAddStill(artifact) } : {})} />;
        })}
      </div>
      {session.asset_catalog.artifacts.length === 0 ? <PanelEmpty text="导入浏览器录屏或本地视频后，系统会创建第一个片段。" /> : null}
      {session.asset_catalog.artifacts.length > 0 && assets.length === 0 ? <PanelEmpty text="没有符合当前搜索或类型筛选的素材。" /> : null}
    </>
  );
}

function AssetCard({ artifact, selected, generated, onClick, actionLabel, onAction }: { artifact: EditorArtifact; selected?: boolean; generated?: boolean; onClick?: (() => void) | undefined; actionLabel?: string; onAction?: () => void }) {
  const content = <><div className={`studio-asset-thumb ${generated ? "generated" : ""} ${artifact.mime_type?.startsWith("image/") ? "screenshot" : ""}`}><span className={`studio-asset-status ${generated ? "review" : ""}`}>{generated ? "待审查" : artifact.sha256 ? "已校验" : "本地"}</span><span className="studio-asset-duration">{assetKindLabel(artifact)}</span></div><div><strong>{artifact.label ?? artifact.id}</strong><small>{mediaResolution(artifact)} · {artifact.mime_type ?? artifact.kind}</small></div></>;
  if (!onClick) return <article className="studio-asset-card static" aria-label={`${artifact.label ?? artifact.id}（仅展示）`}>{content}</article>;
  if (!actionLabel || !onAction) return <button className={`studio-asset-card ${selected ? "selected" : ""}`} onClick={onClick}>{content}</button>;
  return <article className={`studio-asset-card with-action ${selected ? "selected" : ""}`}><button className="studio-asset-preview-button" onClick={onClick}>{content}</button><button className="studio-asset-action" onClick={onAction}>{actionLabel}</button></article>;
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

function ValidationDrawer({ checks, issues, report, onClose, onFocusIssue }: { checks: ExportCheck[]; issues: ExportIssue[]; report: EditorValidationReport | undefined; onClose: () => void; onFocusIssue: (issue: ExportIssue) => void }) {
  const errors = checks.filter((check) => check.tone === "error").length;
  return (
    <div className="studio-drawer-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <aside className="studio-validation-drawer">
        <div className="studio-drawer-header"><div><strong>导出前校验</strong><small>{report ? `检查于 ${new Date(report.checked_at).toLocaleTimeString("zh-CN", { hour12: false })}` : "当前草稿规则检查"}</small></div><button className="studio-icon-button" onClick={onClose}>×</button></div>
        <div className={`studio-validation-summary ${errors ? "blocked" : ""}`}>{errors ? `${errors} 个阻断项尚未解决，当前不能导出。` : "所有阻断项均已通过，可以生成最终 MP4。"}</div>
        {issues.length ? <section className="studio-export-issues"><div className="studio-export-issues-header"><strong>需要处理的位置</strong><small>点击可直接定位到对应片段或标注</small></div>{issues.map((issue) => <button key={issue.id} type="button" className="studio-export-issue" onClick={() => onFocusIssue(issue)}><span>!</span><div><strong>{issue.title}</strong><small>{issue.detail}</small></div><em>定位</em></button>)}</section> : null}
        <div className="studio-check-list">
          {checks.map((check) => <div key={check.id} className={`studio-check-item ${check.tone}`}><span>{check.tone === "pass" ? "✓" : check.tone === "warning" ? "△" : "!"}</span><div><strong>{check.label}</strong><small>{check.detail}</small></div><em>{check.tone === "pass" ? "通过" : check.tone === "warning" ? "提醒" : "阻断"}</em></div>)}
        </div>
        {report?.errors.length || report?.warnings.length ? <div className="studio-report-findings"><strong>Worker 校验结果</strong>{report.errors.map((finding) => <p className="error" key={`error-${finding.code}-${finding.path ?? ""}`}>{finding.message}</p>)}{report.warnings.map((finding) => <p key={`warning-${finding.code}-${finding.path ?? ""}`}>{finding.message}</p>)}</div> : null}
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

function CaptionImportDialog({ value, onChange, onClose, onImport }: { value: string; onChange: (value: string) => void; onClose: () => void; onImport: (value: string) => void }) {
  const fileRef = useRef<HTMLInputElement>(null);
  return <div className="studio-modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}><div className="studio-modal studio-caption-import-modal" role="dialog" aria-modal="true" aria-label="导入字幕"><div className="studio-modal-header"><strong>导入字幕文本</strong><button className="studio-icon-button" onClick={onClose}>×</button></div><div className="studio-modal-content"><p className="studio-dialog-note">支持 TXT 和 MD。每行将成为一条字幕；MD 会自动忽略标题、列表和强调符号。</p><input ref={fileRef} type="file" accept="text/plain,text/markdown,.txt,.md" hidden onChange={async (event) => { const file = event.currentTarget.files?.[0]; if (file) onChange(await file.text()); event.currentTarget.value = ""; }} /><button className="studio-outline-button" onClick={() => fileRef.current?.click()}>选择 TXT / MD 文件</button><label className="studio-field"><span>字幕内容</span><textarea rows={12} value={value} onChange={(event) => onChange(event.target.value)} placeholder={"第一句字幕\n第二句字幕\n第三句字幕"} /></label></div><div className="studio-modal-footer"><button className="studio-ghost-button" onClick={onClose}>取消</button><button className="studio-primary-button" disabled={!value.trim()} onClick={() => onImport(value)}>导入并排布</button></div></div></div>;
}

function ShapeOverlayPreview({ shot, playheadMS, timelineShots, selectedOverlayID, onSelect, onMoveStart, onTransformStart }: { shot: EditorShot | undefined; playheadMS: number; timelineShots: TimelineShot[]; selectedOverlayID: string; onSelect: (id: string) => void; onMoveStart: (event: React.PointerEvent<HTMLButtonElement>, overlay: EditorOverlay) => void; onTransformStart: (event: React.PointerEvent<HTMLButtonElement>, overlay: EditorOverlay, mode: ShapeCanvasInteraction["mode"], corner?: ShapeCanvasInteraction["corner"]) => void }) {
  if (!shot) return null;
  const item = timelineShots.find((candidate) => candidate.shot.id === shot.id);
  const localTime = Math.max(0, playheadMS - (item?.startMS ?? playheadMS));
  return <div className="studio-shape-overlay-layer">{(shot.overlays ?? []).filter((overlay) => overlay.shape && localTime >= (overlay.start_ms ?? 0) && localTime <= (overlay.end_ms ?? Infinity)).map((overlay, index) => <div key={overlay.id ?? `${overlay.shape}-${index}`} className={`studio-shape-overlay-frame ${selectedOverlayID === overlay.id ? "selected" : ""}`} style={shapeOverlayFrameStyle(overlay)}><button type="button" className={`studio-shape-overlay ${overlay.shape}`} style={shapeOverlayVisualStyle(overlay)} aria-label={`选择或移动${shapeLabel(overlay.shape!)}`} onPointerDown={(event) => onMoveStart(event, overlay)} onClick={() => onSelect(overlay.id ?? "")}><span /></button>{selectedOverlayID === overlay.id ? <><button type="button" className="studio-shape-rotate-handle" aria-label="旋转形状" onPointerDown={(event) => onTransformStart(event, overlay, "rotate")} /><button type="button" className="studio-shape-resize-handle nw" aria-label="调整左上角" onPointerDown={(event) => onTransformStart(event, overlay, "resize", "nw")} /><button type="button" className="studio-shape-resize-handle ne" aria-label="调整右上角" onPointerDown={(event) => onTransformStart(event, overlay, "resize", "ne")} /><button type="button" className="studio-shape-resize-handle sw" aria-label="调整左下角" onPointerDown={(event) => onTransformStart(event, overlay, "resize", "sw")} /><button type="button" className="studio-shape-resize-handle se" aria-label="调整右下角" onPointerDown={(event) => onTransformStart(event, overlay, "resize", "se")} /></> : null}</div>)}</div>;
}

function ShapePropertiesPanel({ overlay, onPatch, onRemove }: { overlay: EditorOverlay; onPatch: (patch: Partial<EditorOverlay>) => void; onRemove: () => void }) {
  const patchNumber = (key: "x" | "y" | "width" | "height" | "rotation" | "opacity" | "fill_opacity" | "stroke_width" | "scale_x" | "scale_y" | "tilt_x" | "tilt_y", value: string) => { const next = Number(value); if (Number.isFinite(next)) onPatch({ [key]: key === "x" || key === "y" || key === "width" || key === "height" ? clampNormalized(next / 100, 1) : next }); };
  const applyPosition = (x: number, y: number) => onPatch({ x, y });
  const exportStatus = overlayExportStatus(overlay);
  return <div className="studio-properties-scroll studio-shape-properties">
    <div className={`studio-overlay-export-status ${exportStatus.exportable ? "exportable" : "preview-only"}`}><strong>{exportStatus.exportable ? "可导出" : "仅画布预览"}</strong><span>{exportStatus.exportable ? "位置、尺寸、旋转、缩放与外观会烧录到 MP4。" : `${exportStatus.reason} 当前不会写入最终 MP4；请恢复为平面基础形状后再导出。`}</span></div>
    <PropertySection title="布局" meta="可在画布中直接拖动">
      <div className="studio-layout-presets"><button onClick={() => applyPosition(0.08, 0.08)}>↖</button><button onClick={() => applyPosition(0.36, 0.08)}>↑</button><button onClick={() => applyPosition(0.64, 0.08)}>↗</button><button onClick={() => applyPosition(0.08, 0.4)}>←</button><button onClick={() => applyPosition(0.36, 0.4)}>◎</button><button onClick={() => applyPosition(0.64, 0.4)}>→</button></div>
      <div className="studio-field-pair"><label className="studio-field"><span>X 位置 (%)</span><input type="number" min="0" max="100" step="1" value={Math.round((overlay.x ?? 0) * 100)} onChange={(event) => patchNumber("x", event.target.value)} /></label><label className="studio-field"><span>Y 位置 (%)</span><input type="number" min="0" max="100" step="1" value={Math.round((overlay.y ?? 0) * 100)} onChange={(event) => patchNumber("y", event.target.value)} /></label></div>
      <div className="studio-field-pair"><label className="studio-field"><span>宽度 (%)</span><input type="number" min="5" max="100" step="1" value={Math.round((overlay.width ?? 0.3) * 100)} onChange={(event) => patchNumber("width", event.target.value)} /></label><label className="studio-field"><span>高度 (%)</span><input type="number" min="5" max="100" step="1" value={Math.round((overlay.height ?? 0.2) * 100)} onChange={(event) => patchNumber("height", event.target.value)} /></label></div>
    </PropertySection>
    <PropertySection title="方向与变换" meta="画布坐标">
      <label className="studio-field"><span>旋转 {Math.round(overlay.rotation ?? 0)}°</span><input type="range" min="-180" max="180" step="1" value={overlay.rotation ?? 0} onChange={(event) => patchNumber("rotation", event.target.value)} /></label>
    </PropertySection>
    <PropertySection title="变换" meta="可手动微调">
      <div className="studio-transform-subtitle">缩放</div>
      <div className="studio-field-pair"><label className="studio-field"><span>X {Math.round(overlay.scale_x ?? 100)}%</span><input type="number" min="20" max="250" step="1" value={overlay.scale_x ?? 100} onChange={(event) => patchNumber("scale_x", event.target.value)} /></label><label className="studio-field"><span>Y {Math.round(overlay.scale_y ?? 100)}%</span><input type="number" min="20" max="250" step="1" value={overlay.scale_y ?? 100} onChange={(event) => patchNumber("scale_y", event.target.value)} /></label></div>
      <div className="studio-transform-subtitle">3D 倾斜</div>
      <div className="studio-field-pair"><label className="studio-field"><span>X {Math.round(overlay.tilt_x ?? 0)}°</span><input type="number" min="-60" max="60" step="1" value={overlay.tilt_x ?? 0} onChange={(event) => patchNumber("tilt_x", event.target.value)} /></label><label className="studio-field"><span>Y {Math.round(overlay.tilt_y ?? 0)}°</span><input type="number" min="-60" max="60" step="1" value={overlay.tilt_y ?? 0} onChange={(event) => patchNumber("tilt_y", event.target.value)} /></label></div>
      <div className="studio-transform-subtitle">3D 倾斜预设</div>
      <div className="studio-tilt-presets">{shapeTiltPresets.map((preset) => <button key={preset.id} className={overlay.tilt_preset === preset.id ? "selected" : ""} title={preset.label} aria-label={preset.label} onClick={() => onPatch({ tilt_preset: preset.id, tilt_x: preset.tiltX, tilt_y: preset.tiltY })}><span style={{ transform: preset.previewTransform }} /></button>)}</div>
      <small className="studio-property-note">用于强调标注层的透视感；不改变底层视频或图片素材。3D 倾斜目前仅用于画布预览，导出前会明确阻止，避免成片与编辑效果不一致。</small>
    </PropertySection>
    <PropertySection title="外观" meta={shapeLabel(overlay.shape!)}>
      <label className="studio-field"><span>图层不透明度 {Math.round(overlay.opacity ?? 100)}%</span><input type="range" min="10" max="100" step="1" value={overlay.opacity ?? 100} onChange={(event) => patchNumber("opacity", event.target.value)} /></label>
      <div className="studio-field-pair"><label className="studio-field"><span>填充颜色</span><input type="color" value={overlay.fill_color ?? "#ffffff"} onChange={(event) => onPatch({ fill_color: event.target.value })} /></label><label className="studio-field"><span>填充透明度 {Math.round(overlay.fill_opacity ?? 10)}%</span><input type="number" min="0" max="100" value={overlay.fill_opacity ?? 10} onChange={(event) => patchNumber("fill_opacity", event.target.value)} /></label></div>
      <div className="studio-field-pair"><label className="studio-field"><span>描边颜色</span><input type="color" value={overlay.color ?? "#dc58d5"} onChange={(event) => onPatch({ color: event.target.value })} /></label><label className="studio-field"><span>描边宽度</span><input type="number" min="1" max="24" value={overlay.stroke_width ?? 4} onChange={(event) => patchNumber("stroke_width", event.target.value)} /></label></div>
    </PropertySection>
    <PropertySection title="时间范围" meta="当前片段内"><div className="studio-field-pair"><label className="studio-field"><span>开始 (ms)</span><input type="number" min="0" value={overlay.start_ms ?? 0} onChange={(event) => onPatch({ start_ms: Number(event.target.value) })} /></label><label className="studio-field"><span>结束 (ms)</span><input type="number" min="0" value={overlay.end_ms ?? 0} onChange={(event) => onPatch({ end_ms: Number(event.target.value) })} /></label></div></PropertySection>
    <button className="studio-ghost-button studio-remove-shape" onClick={onRemove}>删除此标注</button>
  </div>;
}

function shapeOverlayFrameStyle(overlay: EditorOverlay): React.CSSProperties {
  return { left: `${clampNormalized(overlay.x ?? 0.3, 0.95) * 100}%`, top: `${clampNormalized(overlay.y ?? 0.3, 0.95) * 100}%`, width: `${clampNormalized(overlay.width ?? 0.3, 1) * 100}%`, height: `${clampNormalized(overlay.height ?? 0.2, 1) * 100}%` };
}

function shapeOverlayVisualStyle(overlay: EditorOverlay): React.CSSProperties {
  const opacity = Math.min(100, Math.max(0, overlay.opacity ?? 100)) / 100;
  const fillOpacity = Math.min(100, Math.max(0, overlay.fill_opacity ?? 10)) / 100;
  const tilt = shapeTiltPreset(overlay.tilt_preset);
  const tiltX = overlay.tilt_x ?? tilt.tiltX;
  const tiltY = overlay.tilt_y ?? tilt.tiltY;
  return { color: overlay.color ?? "#dc58d5", borderWidth: overlay.stroke_width ?? 4, opacity, backgroundColor: colorWithAlpha(overlay.fill_color ?? overlay.color ?? "#dc58d5", fillOpacity), transform: `perspective(640px) rotateX(${tiltX}deg) rotateY(${tiltY}deg) rotate(${overlay.rotation ?? 0}deg) scale(${Math.max(0.2, Math.min(2.5, (overlay.scale_x ?? 100) / 100))}, ${Math.max(0.2, Math.min(2.5, (overlay.scale_y ?? 100) / 100))})`, transformOrigin: "center center" };
}

const shapeTiltPresets = [
  { id: "flat", label: "平面", tiltX: 0, tiltY: 0, previewTransform: "rotateX(0deg) rotateY(0deg)" },
  { id: "tilt_left", label: "向左倾斜", tiltX: 0, tiltY: -18, previewTransform: "rotateY(-35deg)" },
  { id: "tilt_right", label: "向右倾斜", tiltX: 0, tiltY: 18, previewTransform: "rotateY(35deg)" },
  { id: "tilt_up", label: "向上倾斜", tiltX: 18, tiltY: 0, previewTransform: "rotateX(35deg)" },
  { id: "tilt_down", label: "向下倾斜", tiltX: -18, tiltY: 0, previewTransform: "rotateX(-35deg)" },
  { id: "corner_left", label: "左上透视", tiltX: 13, tiltY: -16, previewTransform: "rotateX(28deg) rotateY(-32deg)" },
  { id: "corner_right", label: "右上透视", tiltX: 13, tiltY: 16, previewTransform: "rotateX(28deg) rotateY(32deg)" },
  { id: "corner_lower_left", label: "左下透视", tiltX: -13, tiltY: -16, previewTransform: "rotateX(-28deg) rotateY(-32deg)" },
  { id: "corner_lower_right", label: "右下透视", tiltX: -13, tiltY: 16, previewTransform: "rotateX(-28deg) rotateY(32deg)" },
  { id: "reset", label: "重置 3D 倾斜", tiltX: 0, tiltY: 0, previewTransform: "rotate(0deg)" },
] as const;

function shapeTiltPreset(value?: string) {
  return shapeTiltPresets.find((preset) => preset.id === value) ?? shapeTiltPresets[0];
}

function shapeLabel(shape: ShapeKind): string {
  return ({ rectangle: "矩形高亮", circle: "圆形高亮", polygon: "多边形", star: "星形", line: "直线", arrow: "箭头" } as const)[shape];
}

function parseCaptionLines(source: string): string[] {
  return source.replace(/\r/g, "").split("\n").map((line) => line.replace(/^\s{0,3}(#{1,6}\s*|[-*+]\s+|\d+[.)]\s+)/, "").replace(/[*_`>#]/g, "").trim()).filter((line) => line.length > 0);
}

function matchesAssetRatio(artifact: EditorArtifact, filter: AssetRatioFilter): boolean {
  if (filter === "all") return true;
  const width = artifact.metadata?.width;
  const height = artifact.metadata?.height;
  if (typeof width !== "number" || typeof height !== "number" || width <= 0 || height <= 0) return false;
  const ratio = width / height;
  if (filter === "landscape") return ratio >= 1.2;
  if (filter === "portrait") return ratio <= 0.8;
  return ratio > 0.8 && ratio < 1.2;
}

function readTrackHeaderWidth(): number {
  try {
    return clampTrackHeaderWidth(Number(window.localStorage.getItem("cascade.editor.track-header-width")) || 148);
  } catch {
    return 148;
  }
}

function clampTrackHeaderWidth(value: number): number {
  return Math.max(120, Math.min(248, Math.round(value)));
}

function clampNormalized(value: number, max = 1): number {
  if (!Number.isFinite(value)) return 0;
  return Math.max(0, Math.min(max, value));
}

function normalizedAngleDelta(value: number): number {
  let normalized = value;
  while (normalized > Math.PI) normalized -= Math.PI * 2;
  while (normalized < -Math.PI) normalized += Math.PI * 2;
  return normalized;
}

function normalizedOverlayWidth(interaction: ShapeCanvasInteraction): number {
  const overlay = draftPlanForOverlay(interaction.basePlan, interaction.overlayID);
  return Math.max(0.05, Math.min(1, overlay?.width ?? 0.3));
}

function normalizedOverlayHeight(interaction: ShapeCanvasInteraction): number {
  const overlay = draftPlanForOverlay(interaction.basePlan, interaction.overlayID);
  return Math.max(0.05, Math.min(1, overlay?.height ?? 0.2));
}

function draftPlanForOverlay(plan: EditorPlan, overlayID: string): EditorOverlay | undefined {
  return plan.shots.flatMap((shot) => shot.overlays ?? []).find((overlay) => overlay.id === overlayID);
}

function colorWithAlpha(color: string, alpha: number): string {
  const hex = color.trim().replace("#", "");
  if (/^[0-9a-fA-F]{6}$/.test(hex)) {
    const red = Number.parseInt(hex.slice(0, 2), 16);
    const green = Number.parseInt(hex.slice(2, 4), 16);
    const blue = Number.parseInt(hex.slice(4, 6), 16);
    return `rgb(${red} ${green} ${blue} / ${alpha})`;
  }
  return color;
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
      if (!finalRendererSupportsOverlay(overlay)) unsupported.add(overlayExportStatus(overlay).reason ?? (overlay.shape ? `${overlay.type}:${overlay.shape}` : overlay.type));
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

export function buildExportIssues(plan?: EditorPlan): ExportIssue[] {
  const issues: ExportIssue[] = [];
  for (const [shotIndex, shot] of (plan?.shots ?? []).entries()) {
    for (const [overlayIndex, overlay] of (shot.overlays ?? []).entries()) {
      const status = overlayExportStatus(overlay);
      if (status.exportable) continue;
      const shape = overlay.shape ? shapeLabel(overlay.shape) : overlay.type;
      issues.push({
        id: `${shot.id}:${overlay.id ?? overlayIndex}`,
        title: `片段 ${shotIndex + 1}：${shape}`,
        detail: status.reason === "3D 倾斜标注" ? "请在右侧属性中将 3D 倾斜恢复为“平面”，再导出 MP4。" : `${status.reason ?? "该标注"} 目前仅能在画布预览；请改为矩形、圆形、多边形、星形、直线或箭头。`,
        shotID: shot.id,
        ...(overlay.id ? { overlayID: overlay.id } : {}),
      });
    }
  }
  return issues;
}

export function preservesRequiredStepOrder(shots: EditorShot[], steps: EditorTimelineStep[]): boolean {
  const order = new Map(steps.filter((step) => step.required).map((step) => [step.step_id, step.order]));
  let last = -1;
  for (const shot of shots) {
    if (shot.presentation_kind === "still") continue;
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
  if (shot?.presentation_kind === "still") return Math.max(0, shot.output_duration_ms ?? 0);
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

function sessionSelectLabel(session: EditorSession): string {
  const waitingForEdit = Boolean(session.asset_catalog.source?.recording_result_package_id) && session.final_render.status === "not_started";
  return `${sessionDisplayName(session)}${waitingForEdit ? " · 待编辑" : ""}`;
}

function mediaTabLabel(tab: MediaTab): string {
  return ({ media: "媒体", captions: "字幕", callouts: "标注", generated: "生成", style: "制作" } as const)[tab];
}

function mediaResolution(artifact?: EditorArtifact): string {
  const width = artifact?.metadata?.width;
  const height = artifact?.metadata?.height;
  return typeof width === "number" && typeof height === "number" ? `${width}×${height}` : "待探测";
}

function assetKindLabel(artifact: EditorArtifact): string {
  if (artifact.mime_type?.startsWith("image/") || artifact.kind === "step_screenshot") return "步骤截图";
  if (artifact.kind === "narration_audio") return "配音素材";
  return artifact.kind === "raw_recording" ? "原始录屏" : artifact.kind;
}

function isGeneratedArtifact(artifact?: EditorArtifact): boolean {
  if (!artifact) return false;
  return artifact.kind.includes("generated") || artifact.kind.includes("candidate") || artifact.metadata?.source_material_policy === "non_authoritative_generated_candidate";
}

function isStyleReferenceArtifact(artifact: EditorArtifact): boolean {
  return artifact.kind === "style_reference_video" || artifact.metadata?.style_reference === true;
}

function isTimelineStillAsset(artifact?: EditorArtifact): boolean {
  return Boolean(artifact && artifact.kind === "step_screenshot" && artifact.mime_type?.startsWith("image/") && artifact.metadata?.presentation_only === true && artifact.include_in_demo !== false);
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
