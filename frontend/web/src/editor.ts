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
  type: string;
  text?: string;
  start_ms?: number;
  end_ms?: number;
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

export type EditorProviderCapability = {
  provider: string;
  task: string;
  mode: string;
  configured: boolean;
  model?: string;
  output_kind: string;
  auto_include: boolean;
  requires_review: boolean;
  presentation_only: boolean;
  can_represent_business_step: boolean;
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
  provider_capabilities: EditorProviderCapability[];
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
  savePlan(sessionID: string, expectedRevision: number, plan: EditorPlan): Promise<EditorClientResult<EditorSession>>;
  validate(sessionID: string): Promise<EditorClientResult<EditorValidationReport>>;
  preview(sessionID: string): Promise<EditorClientResult<EditorSession>>;
  render(sessionID: string): Promise<EditorClientResult<EditorSession>>;
  cancelRender(sessionID: string, kind: "preview" | "final"): Promise<EditorClientResult<EditorSession>>;
  mediaURL(sessionID: string, kind: "preview" | "final" | "asset", assetID?: string): string;
};

type BridgeEnvelope<T> = { ok: boolean; data?: T; error?: string };

export function createEditorClient(): EditorClient {
  if (import.meta.env.VITE_CASCADE_BRIDGE !== "local") {
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
    savePlan: (sessionID, expectedRevision, plan) =>
      request<EditorSession>(`/v1/editor/sessions/${encodeURIComponent(sessionID)}/plan`, {
        method: "POST",
        body: JSON.stringify({ expected_revision: expectedRevision, edit_plan: plan }),
      }),
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
    async savePlan(sessionID, expectedRevision, plan) {
      const session = sessions.get(sessionID);
      if (!session) return { ok: false, error: "未找到编辑项目" };
      if (session.revision !== expectedRevision) return { ok: false, error: "编辑版本冲突，请重新加载" };
      const next = { ...session, revision: session.revision + 1, edit_plan: plan };
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
    provider_capabilities: [{ provider: "seedance", task: "generated_video_candidate", mode: "dry_run", configured: false, output_kind: "generated_video_candidate", auto_include: false, requires_review: true, presentation_only: true, can_represent_business_step: false }],
  };
}
