export type EditorValidationFinding = {
  code: string;
  message: string;
  path?: string;
};

export type EditorValidationReport = {
  schema_version: string;
  valid: boolean;
  checked_at: string;
  plan_id?: string;
  errors: EditorValidationFinding[];
  warnings: EditorValidationFinding[];
};

export type EditorArtifact = {
  id: string;
  kind: string;
  uri: string;
  mime_type?: string;
  label?: string;
  sha256?: string;
  size_bytes?: number;
  duration_ms?: number;
  local_path?: string;
  source_step_id?: string;
  include_in_demo?: boolean;
  metadata?: Record<string, unknown>;
};

export type VideoStyleOutput = {
  aspect_ratio: string;
  width: number;
  height: number;
  fps: number;
  target_duration_ms: number;
  min_duration_ms: number;
  max_duration_ms: number;
};

export type VideoStylePresentation = {
  pacing: string;
  transition_style: string;
  color_direction: string;
  caption_mode: string;
  max_caption_chars: number;
  source_volume_percent: number;
  narration_preferred: boolean;
  allow_presentation_still: boolean;
};

export type VideoStyleTemplate = {
  schema_version: string;
  template_id: string;
  name: string;
  summary: string;
  category: string;
  output: VideoStyleOutput;
  presentation: VideoStylePresentation;
  constraints: { existing_assets_only: boolean; preserve_required_step_order: boolean; allowed_operations: string[]; unavailable_effects?: string[] };
};

export type EditorStyleDraftWarning = { code: string; message: string };

export type EditorStyleDraft = {
  schema_version: string;
  draft_id: string;
  session_id: string;
  base_revision: number;
  prompt?: string;
  template: VideoStyleTemplate;
  style_profile: {
    source: string;
    analysis_status: string;
    confidence?: number;
    reference?: { asset_id: string; user_declared_rights: boolean; content_copied: boolean };
    unsupported_features?: Array<{ feature: string; status: string; reason: string }>;
  };
  proposed_edit_plan: EditorPlan;
  proposed_preview_profile: EditorSession["preview_profile"];
  proposed_final_profile: EditorSession["final_profile"];
  validation?: EditorValidationReport;
  requires_confirmation: boolean;
  warnings?: EditorStyleDraftWarning[];
};

export type EditorStyleDraftRequest = {
  expected_revision: number;
  prompt?: string;
  template_id?: string;
  reference_asset_id?: string;
  reference_rights_confirmed: boolean;
};

export type EditorTimelineStep = {
  step_id: string;
  order: number;
  action: string;
  status: string;
  required: boolean;
  start_ms: number;
  end_ms: number;
  duration_ms: number;
  expected_outcome?: string;
  observed_state?: string;
  artifacts?: string[];
};

export type EditorOverlay = {
  id?: string;
  type: string;
  text?: string;
  start_ms?: number;
  end_ms?: number;
  // Presentation-only geometry is normalized to the source frame so a shape
  // stays attached to its video or still-image shot at every output size.
  shape?: "rectangle" | "circle" | "polygon" | "star" | "line" | "arrow";
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  color?: string;
  stroke_width?: number;
  rotation?: number;
  opacity?: number;
  fill_color?: string;
  fill_opacity?: number;
  tilt_preset?: string;
  scale_x?: number;
  scale_y?: number;
  tilt_x?: number;
  tilt_y?: number;
};

export type EditorShot = {
  id: string;
  source_artifact_id: string;
  source_step_id?: string;
  source_time_range_ms?: [number, number];
  presentation_kind?: "video" | "still";
  output_duration_ms?: number;
  purpose: string;
  operations?: Array<{ type: string; [key: string]: unknown }>;
  overlays?: EditorOverlay[];
};

export type EditorPlan = {
  schema_version?: string;
  plan_id: string;
  catalog_id?: string;
  objective?: string;
  source_authority: string;
  model_role: string;
  source_material_policy: string;
  script_order_policy: string;
  locked_fields: string[];
  model_editable_fields: string[];
  target_duration_ms?: number;
  shots: EditorShot[];
  global_style?: Record<string, string>;
  audio?: {
    mode: "source" | "mute";
    volume_percent: number;
    split_points_ms?: number[];
    segment_settings?: Array<{ start_ms: number; end_ms: number; mode: "source" | "mute"; volume_percent: number }>;
  };
  narrations?: Array<{
    id: string;
    source_artifact_id: string;
    source_time_range_ms?: [number, number];
    output_time_range_ms: [number, number];
    volume_percent: number;
    duck_source_audio?: boolean;
    duck_source_to_percent?: number;
    source: "user_recorded" | "user_uploaded" | "tts_confirmed";
  }>;
  caption_cues?: Array<{
    id: string;
    output_range_ms: [number, number];
    text: string;
    source: "user_configured" | "asr_confirmed" | "model_confirmed";
  }>;
};

export type EditorRenderState = {
  status: "not_started" | "running" | "ready" | "failed" | "cancelled";
  job_id?: string;
  phase?: string;
  progress?: number;
  cancel_requested?: boolean;
  revision?: number;
  video_path?: string;
  render_manifest_path?: string;
  error?: string;
};

export type EditorPresentationCapabilityProfile = {
  capability: "presentation_video_candidate" | string;
  profile_version: string;
  available: boolean;
  limits: {
    max_reference_assets: number; max_reference_images: number; max_reference_videos: number; max_reference_audios: number;
    reference_video_min_sec: number; reference_video_max_sec: number; reference_video_total_max_sec: number;
    candidate_duration_min_sec: number; candidate_duration_max_sec: number; recommended_candidate_duration_sec: number;
    accepted_image_formats: string[]; accepted_video_formats: string[]; max_request_body_bytes: number; generated_audio_enabled: boolean;
  };
  policies: {
    presentation_only: boolean; requires_explicit_review: boolean; may_replace_captured_ui: boolean; failure_blocks_recording_delivery: boolean;
  };
};

export type EditorAutomationSummary = {
	 source_package_id?: string;
	 execution_runtime?: string;
	 validation_state: "result_only" | "verified" | "blocked" | "repaired_or_review" | "legacy_result" | string;
	 latest_decision?: "continue" | "repair_allowed" | "stop_and_report" | "reunderstanding_required";
	 validation_report_count: number;
	 evidence_backed_report_count: number;
	 patch_count: number;
	 applied_patch_count: number;
	 rolled_back_patch_count: number;
	 stage_event_audit_available: boolean;
};

export type EditorSession = {
  schema_version: string;
  session_id: string;
  name: string;
  mode: string;
  status: string;
  revision: number;
  asset_catalog: {
    catalog_id: string;
    workflow_graph_id?: string;
    graph_version?: number;
    run_id?: string;
    source?: {
      recording_result_package_id?: string;
      execution_trace_id?: string;
      generated_at?: string;
    };
    timeline: { duration_ms: number; recording_artifact_id?: string };
    steps?: EditorTimelineStep[];
    artifacts: EditorArtifact[];
  };
  edit_plan: EditorPlan;
  validation?: EditorValidationReport;
  preview_profile: { width: number; height: number; fps: number; format: string; crf?: number };
  final_profile: { width: number; height: number; fps: number; format: string; crf?: number };
  preview: EditorRenderState;
  final_render: EditorRenderState;
  presentation_capabilities: EditorPresentationCapabilityProfile[];
	 automation?: EditorAutomationSummary;
};

export type EditorClientResult<T> = { ok: boolean; data?: T; error?: string };

export type EditorClient = {
  mode: "local" | "mock";
  listSessions(): Promise<EditorClientResult<EditorSession[]>>;
  createSession(name: string, sourcePath?: string): Promise<EditorClientResult<EditorSession>>;
  createSessionFromResultPackage(resultPackagePath: string, recordingPath?: string, name?: string): Promise<EditorClientResult<EditorSession>>;
  getSession(sessionID: string): Promise<EditorClientResult<EditorSession>>;
  importAsset(sessionID: string, sourcePath: string): Promise<EditorClientResult<EditorSession>>;
  uploadAsset(sessionID: string, file: File): Promise<EditorClientResult<EditorSession>>;
  importStyleReference(sessionID: string, sourcePath: string): Promise<EditorClientResult<EditorSession>>;
  uploadStyleReference(sessionID: string, file: File): Promise<EditorClientResult<EditorSession>>;
  listStyleTemplates(): Promise<EditorClientResult<VideoStyleTemplate[]>>;
  createStyleDraft(sessionID: string, request: EditorStyleDraftRequest): Promise<EditorClientResult<EditorStyleDraft>>;
  applyStyleDraft(sessionID: string, draftID: string, expectedRevision: number): Promise<EditorClientResult<EditorSession>>;
  savePlan(sessionID: string, expectedRevision: number, plan: EditorPlan): Promise<EditorClientResult<EditorSession>>;
  reviewPresentationCandidate(sessionID: string, expectedRevision: number, artifactID: string, approved: boolean): Promise<EditorClientResult<EditorSession>>;
  validate(sessionID: string): Promise<EditorClientResult<EditorValidationReport>>;
  preview(sessionID: string): Promise<EditorClientResult<EditorSession>>;
  render(sessionID: string): Promise<EditorClientResult<EditorSession>>;
  cancelRender(sessionID: string, kind: "preview" | "final"): Promise<EditorClientResult<EditorSession>>;
  mediaURL(sessionID: string, kind: "preview" | "final" | "asset", assetID?: string): string;
};

type BridgeEnvelope<T> = { ok: boolean; data?: T; error?: string };

export function createEditorClient(): EditorClient {
  if (import.meta.env.MODE === "test" || import.meta.env.VITE_CASCADE_BRIDGE !== "local") {
    return createMockEditorClient();
  }
  const baseURL = String(import.meta.env.VITE_CASCADE_BRIDGE_URL || "").replace(/\/$/, "");
  const request = async <T>(path: string, init?: RequestInit): Promise<EditorClientResult<T>> => {
    try {
      const response = await fetch(`${baseURL}${path}`, {
        ...init,
        headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
      });
      return decodeEditorBridgeResponse<T>(response);
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : String(error) };
    }
  };
  const upload = async (sessionID: string, file: File): Promise<EditorClientResult<EditorSession>> => {
    try {
      const form = new FormData();
      form.append("file", file, file.name);
      const response = await fetch(`${baseURL}/v1/editor/sessions/${encodeURIComponent(sessionID)}/uploads`, { method: "POST", body: form });
      const payload = (await response.json()) as BridgeEnvelope<EditorSession>;
      if (!response.ok || !payload.ok || payload.data === undefined) {
        return { ok: false, error: payload.error || `HTTP ${response.status}` };
      }
      return { ok: true, data: payload.data };
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : String(error) };
    }
  };
  const uploadStyleReference = async (sessionID: string, file: File): Promise<EditorClientResult<EditorSession>> => {
    try {
      const form = new FormData();
      form.append("file", file, file.name);
      const response = await fetch(`${baseURL}/v1/editor/sessions/${encodeURIComponent(sessionID)}/style-references/uploads`, { method: "POST", body: form });
      const payload = (await response.json()) as BridgeEnvelope<EditorSession>;
      if (!response.ok || !payload.ok || payload.data === undefined) return { ok: false, error: payload.error || `HTTP ${response.status}` };
      return { ok: true, data: payload.data };
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : String(error) };
    }
  };
  return {
    mode: "local",
    listSessions: () => request<EditorSession[]>("/v1/editor/sessions"),
    createSession: (name, sourcePath) =>
      request<EditorSession>("/v1/editor/sessions", {
        method: "POST",
        body: JSON.stringify({ name, ...(sourcePath ? { source_path: sourcePath } : {}) }),
      }),
    createSessionFromResultPackage: (resultPackagePath, recordingPath, name) =>
      request<EditorSession>("/v1/editor/sessions/from-result-package", {
        method: "POST",
        body: JSON.stringify({
          result_package_path: resultPackagePath,
          ...(recordingPath ? { recording_path: recordingPath } : {}),
          ...(name ? { name } : {}),
        }),
      }),
    getSession: (sessionID) => request<EditorSession>(`/v1/editor/sessions/${encodeURIComponent(sessionID)}`),
    importAsset: (sessionID, sourcePath) =>
      request<EditorSession>(`/v1/editor/sessions/${encodeURIComponent(sessionID)}/assets`, {
        method: "POST",
        body: JSON.stringify({ path: sourcePath }),
      }),
    uploadAsset: upload,
    importStyleReference: (sessionID, sourcePath) => request<EditorSession>(`/v1/editor/sessions/${encodeURIComponent(sessionID)}/style-references/assets`, { method: "POST", body: JSON.stringify({ path: sourcePath }) }),
    uploadStyleReference,
    listStyleTemplates: () => request<VideoStyleTemplate[]>("/v1/editor/style-templates"),
    createStyleDraft: (sessionID, styleRequest) => request<EditorStyleDraft>(`/v1/editor/sessions/${encodeURIComponent(sessionID)}/style-drafts`, { method: "POST", body: JSON.stringify(styleRequest) }),
    applyStyleDraft: (sessionID, draftID, expectedRevision) => request<EditorSession>(`/v1/editor/sessions/${encodeURIComponent(sessionID)}/style-drafts/${encodeURIComponent(draftID)}/apply`, { method: "POST", body: JSON.stringify({ expected_revision: expectedRevision }) }),
    savePlan: (sessionID, expectedRevision, plan) =>
      request<EditorSession>(`/v1/editor/sessions/${encodeURIComponent(sessionID)}/plan`, {
        method: "POST",
        body: JSON.stringify({ expected_revision: expectedRevision, edit_plan: plan }),
      }),
    reviewPresentationCandidate: (sessionID, expectedRevision, artifactID, approved) => request<EditorSession>(`/v1/editor/sessions/${encodeURIComponent(sessionID)}/presentation-candidates/review`, { method: "POST", body: JSON.stringify({ expected_revision: expectedRevision, artifact_id: artifactID, approved }) }),
    validate: (sessionID) => request<EditorValidationReport>(`/v1/editor/sessions/${encodeURIComponent(sessionID)}/validate`, { method: "POST" }),
    preview: (sessionID) => request<EditorSession>(`/v1/editor/sessions/${encodeURIComponent(sessionID)}/preview`, { method: "POST" }),
    render: (sessionID) => request<EditorSession>(`/v1/editor/sessions/${encodeURIComponent(sessionID)}/render`, { method: "POST" }),
    cancelRender: (sessionID, kind) => request<EditorSession>(`/v1/editor/sessions/${encodeURIComponent(sessionID)}/${kind === "preview" ? "preview" : "render"}/cancel`, { method: "POST" }),
    mediaURL: (sessionID, kind, assetID) => {
      const ref = kind === "asset" ? `assets/${encodeURIComponent(assetID || "")}` : kind;
      return `${baseURL}/v1/editor/sessions/${encodeURIComponent(sessionID)}/media/${ref}`;
    },
  };
}

export async function decodeEditorBridgeResponse<T>(response: Response): Promise<EditorClientResult<T>> {
  const raw = await response.text();
  if ([502, 503, 504].includes(response.status)) {
    return { ok: false, error: "本地编辑服务不可用（Bridge: 127.0.0.1:4317）。请启动本地编辑器服务后重试。" };
  }
  let payload: BridgeEnvelope<T> | undefined;
  try {
    payload = JSON.parse(raw) as BridgeEnvelope<T>;
  } catch {
    const snippet = raw.replace(/\s+/g, " ").trim().slice(0, 160);
    return { ok: false, error: `HTTP ${response.status}${snippet ? `: ${snippet}` : ""}` };
  }
  if (!response.ok || !payload.ok || payload.data === undefined) {
    return { ok: false, error: payload.error || `HTTP ${response.status}` };
  }
  return { ok: true, data: payload.data };
}

function createMockEditorClient(): EditorClient {
  const sessions = new Map<string, EditorSession>();
  const result = <T>(data: T): EditorClientResult<T> => ({ ok: true, data });
  return {
    mode: "mock",
    async listSessions() {
      return result([...sessions.values()]);
    },
    async createSession(name) {
      const session = mockSession(name);
      sessions.set(session.session_id, session);
      return result(session);
    },
    async createSessionFromResultPackage(_resultPackagePath, _recordingPath, name) {
      const session = mockSession(name || "结果包剪辑");
      sessions.set(session.session_id, session);
      return result(session);
    },
    async getSession(sessionID) {
      const session = sessions.get(sessionID);
      return session ? result(session) : { ok: false, error: "未找到编辑项目" };
    },
    async importAsset(sessionID, sourcePath) {
      const session = sessions.get(sessionID);
      if (!session) return { ok: false, error: "未找到编辑项目" };
      const assetID = `asset_${Date.now()}`;
      const fileName = sourcePath.split(/[\\/]/).pop();
      const isNarrationAudio = /\.(wav|mp3|m4a|aac|ogg|flac)$/i.test(sourcePath);
      const artifact: EditorArtifact = {
        id: assetID, kind: isNarrationAudio ? "narration_audio" : "raw_recording", uri: sourcePath, duration_ms: 15_000,
        ...(isNarrationAudio ? { mime_type: "audio/wav", metadata: { presentation_only: true } } : {}),
        ...(fileName ? { label: fileName } : {}),
      };
      if (isNarrationAudio) {
        const next = { ...session, revision: session.revision + 1, asset_catalog: { ...session.asset_catalog, artifacts: [...session.asset_catalog.artifacts, artifact] } };
        sessions.set(sessionID, next);
        return result(next);
      }
      const shot: EditorShot = { id: `shot_${Date.now()}`, source_artifact_id: assetID, source_time_range_ms: [0, 15_000], purpose: artifact.label || "本地素材", operations: [{ type: "trim" }], overlays: [] };
      const next = { ...session, revision: session.revision + 1, asset_catalog: { ...session.asset_catalog, artifacts: [...session.asset_catalog.artifacts, artifact] }, edit_plan: { ...session.edit_plan, shots: [...session.edit_plan.shots, shot] } };
      sessions.set(sessionID, next);
      return result(next);
    },
    async uploadAsset(sessionID, file) {
      return this.importAsset(sessionID, file.name);
    },
    async importStyleReference(sessionID, sourcePath) {
      const session = sessions.get(sessionID);
      if (!session) return { ok: false, error: "未找到编辑项目" };
      const fileName = sourcePath.split(/[\\/]/).pop();
      const asset: EditorArtifact = { id: `style_reference_${Date.now()}`, kind: "style_reference_video", uri: sourcePath, mime_type: "video/mp4", ...(fileName ? { label: fileName } : {}), include_in_demo: false, metadata: { style_reference: true, presentation_only: true, rights_confirmation_required: true } };
      const next = { ...session, revision: session.revision + 1, asset_catalog: { ...session.asset_catalog, artifacts: [...session.asset_catalog.artifacts, asset] } };
      sessions.set(sessionID, next);
      return result(next);
    },
    async uploadStyleReference(sessionID, file) {
      return this.importStyleReference(sessionID, file.name);
    },
    async listStyleTemplates() {
      return result(mockStyleTemplates());
    },
    async createStyleDraft(sessionID, styleRequest) {
      const session = sessions.get(sessionID);
      if (!session) return { ok: false, error: "未找到编辑项目" };
      if (styleRequest.expected_revision !== session.revision) return { ok: false, error: "编辑版本冲突，请重新加载" };
      const template = mockStyleTemplates().find((item) => item.template_id === styleRequest.template_id) ?? mockStyleTemplates()[0]!;
      if (styleRequest.reference_asset_id && !styleRequest.reference_rights_confirmed) return { ok: false, error: "使用参考视频前需要确认拥有使用权" };
      const hasReference = Boolean(styleRequest.reference_asset_id);
      const draft: EditorStyleDraft = { schema_version: "demoops.video_style_draft.v1", draft_id: `style_${Date.now()}`, session_id: sessionID, base_revision: session.revision, ...(styleRequest.prompt ? { prompt: styleRequest.prompt } : {}), template, style_profile: { source: hasReference ? "reference_video_pending_analysis" : "platform_template", analysis_status: hasReference ? "pending_analysis" : "template_ready", confidence: hasReference ? 0 : 1, ...(hasReference ? { reference: { asset_id: styleRequest.reference_asset_id!, user_declared_rights: true, content_copied: false } } : {}) }, proposed_edit_plan: { ...session.edit_plan, plan_id: `plan_style_${Date.now()}`, objective: styleRequest.prompt || template.name, global_style: { color_grade: template.presentation.color_direction, pacing: template.presentation.pacing, transition_style: template.presentation.transition_style }, audio: { mode: "source", volume_percent: template.presentation.source_volume_percent } }, proposed_preview_profile: { ...session.preview_profile, width: template.output.width > template.output.height ? 1280 : 720, height: template.output.width > template.output.height ? 720 : 1280, fps: template.output.fps }, proposed_final_profile: { ...session.final_profile, width: template.output.width, height: template.output.height, fps: template.output.fps }, requires_confirmation: true, warnings: [{ code: "style_effects_partially_rendered", message: "当前仅实际应用画幅、裁剪、字幕和音频；复杂动画与运镜会记录在计划中，但尚未渲染为像素效果。" }, ...(hasReference ? [{ code: "reference_analysis_pending", message: "参考视频已登记；第一版先使用模板，后续才会提取节奏和字幕等可解释参数。" }] : [])] };
      return result(draft);
    },
    async applyStyleDraft(sessionID, draftID, expectedRevision) {
      const session = sessions.get(sessionID);
      if (!session) return { ok: false, error: "未找到编辑项目" };
      if (session.revision !== expectedRevision) return { ok: false, error: "编辑版本冲突，请重新加载" };
      return { ok: false, error: `模拟模式不能应用草案 ${draftID}；请启动本地 Bridge 验证。` };
    },
    async savePlan(sessionID, expectedRevision, plan) {
      const session = sessions.get(sessionID);
      if (!session) return { ok: false, error: "未找到编辑项目" };
      if (session.revision !== expectedRevision) return { ok: false, error: "编辑版本冲突，请重新加载" };
      const next = { ...session, revision: session.revision + 1, edit_plan: plan };
      sessions.set(sessionID, next);
      return result(next);
    },
    async reviewPresentationCandidate(sessionID, expectedRevision, artifactID, approved) {
      const session = sessions.get(sessionID);
      if (!session) return { ok: false, error: "未找到编辑项目" };
      if (session.revision !== expectedRevision) return { ok: false, error: "编辑版本冲突，请重新加载" };
      const artifact = session.asset_catalog.artifacts.find((item) => item.id === artifactID);
      if (!artifact || artifact.kind !== "generated_video_candidate" || artifact.metadata?.media_eligible !== true) return { ok: false, error: "候选素材尚未通过媒体检查" };
      const nextArtifact = { ...artifact, include_in_demo: false, metadata: { ...artifact.metadata, approved_for_demo: approved, approval_mode: "explicit_user_review", explicit_review_required: true, include_in_demo: false } };
      const next = { ...session, revision: session.revision + 1, asset_catalog: { ...session.asset_catalog, artifacts: session.asset_catalog.artifacts.map((item) => item.id === artifactID ? nextArtifact : item) } };
      sessions.set(sessionID, next);
      return result(next);
    },
    async validate(sessionID) {
      const session = sessions.get(sessionID);
      if (!session) return { ok: false, error: "未找到编辑项目" };
      return result({ schema_version: "demoops.demo_edit_plan_validation.v1", valid: session.edit_plan.shots.length > 0, checked_at: new Date().toISOString(), errors: [], warnings: [] });
    },
    async preview(sessionID) {
      const session = sessions.get(sessionID);
      if (!session) return { ok: false, error: "未找到编辑项目" };
      return result({ ...session, preview: { status: "ready", revision: session.revision } });
    },
    async render(sessionID) {
      const session = sessions.get(sessionID);
      if (!session) return { ok: false, error: "未找到编辑项目" };
      return result({ ...session, final_render: { status: "ready", revision: session.revision } });
    },
    async cancelRender(sessionID, kind) {
      const session = sessions.get(sessionID);
      if (!session) return { ok: false, error: "未找到编辑项目" };
      const field = kind === "preview" ? "preview" : "final_render";
      const next = { ...session, [field]: { ...session[field], status: "cancelled" as const, phase: "cancelled" } };
      sessions.set(sessionID, next);
      return result(next);
    },
    mediaURL: () => "",
  };
}

function mockStyleTemplates(): VideoStyleTemplate[] {
  return [
    { schema_version: "demoops.video_style_template.v1", template_id: "concise_product_demo", name: "简洁产品演示", summary: "先讲清价值，再展示真实操作与结果。", category: "product_demo", output: { aspect_ratio: "16:9", width: 1920, height: 1080, fps: 30, target_duration_ms: 45000, min_duration_ms: 30000, max_duration_ms: 60000 }, presentation: { pacing: "clear_and_direct", transition_style: "simple_cut", color_direction: "neutral_product_ui", caption_mode: "sparse", max_caption_chars: 18, source_volume_percent: 55, narration_preferred: true, allow_presentation_still: true }, constraints: { existing_assets_only: true, preserve_required_step_order: true, allowed_operations: ["trim", "caption", "volume"] } },
    { schema_version: "demoops.video_style_template.v1", template_id: "fast_feature_demo", name: "快节奏功能亮点", summary: "以更快节奏串联关键功能和结果。", category: "product_demo", output: { aspect_ratio: "16:9", width: 1920, height: 1080, fps: 30, target_duration_ms: 40000, min_duration_ms: 25000, max_duration_ms: 50000 }, presentation: { pacing: "fast_with_result_hold", transition_style: "short_fade", color_direction: "cool_low_saturation", caption_mode: "short", max_caption_chars: 16, source_volume_percent: 35, narration_preferred: true, allow_presentation_still: true }, constraints: { existing_assets_only: true, preserve_required_step_order: true, allowed_operations: ["trim", "caption", "volume"] } },
  ];
}

function mockSession(name: string): EditorSession {
  const id = `edit_${Date.now()}`;
  return {
    schema_version: "demoops.editor_session.v1",
    session_id: id,
    name: name || "未命名演示",
    mode: "demo_safe",
    status: "editing",
    revision: 1,
    asset_catalog: { catalog_id: `catalog_${id}`, timeline: { duration_ms: 0 }, artifacts: [] },
    edit_plan: {
      schema_version: "demoops.demo_edit_plan.v1",
      plan_id: `plan_${id}`,
      catalog_id: `catalog_${id}`,
      source_authority: "server_local_editor",
      model_role: "presentation_optimizer_only",
      source_material_policy: "existing_assets_only",
      script_order_policy: "preserve_required_step_order",
      locked_fields: [],
      model_editable_fields: [],
      audio: { mode: "source", volume_percent: 100 },
      shots: [],
    },
    preview_profile: { width: 1280, height: 720, fps: 30, format: "mp4", crf: 28 },
    final_profile: { width: 1920, height: 1080, fps: 30, format: "mp4", crf: 21 },
    preview: { status: "not_started" },
    final_render: { status: "not_started" },
    presentation_capabilities: [{ capability: "presentation_video_candidate", profile_version: "server-presentation-video-v1", available: false, limits: { max_reference_assets: 4, max_reference_images: 4, max_reference_videos: 3, max_reference_audios: 0, reference_video_min_sec: 2, reference_video_max_sec: 15, reference_video_total_max_sec: 15, candidate_duration_min_sec: 4, candidate_duration_max_sec: 15, recommended_candidate_duration_sec: 5, accepted_image_formats: ["png", "jpeg"], accepted_video_formats: ["mp4", "mov"], max_request_body_bytes: 67108864, generated_audio_enabled: false }, policies: { presentation_only: true, requires_explicit_review: true, may_replace_captured_ui: false, failure_blocks_recording_delivery: false } }],
  };
}
