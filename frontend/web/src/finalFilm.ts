import type { EditorClientResult, EditorPlan, EditorSession } from "./editor";

export type FinalFilmPurpose = "intro" | "outro" | "section_divider" | "abstract_broll" | "brand_atmosphere";
export type FinalFilmProvider = "minimax-h3" | "seedance-2.5";

export type FinalFilmPresentationIntent = {
  intent_id: string;
  capability: "presentation_video_candidate";
  purpose: FinalFilmPurpose;
  required: false;
  reference_asset_refs?: string[];
  requested_slot: { preferred_duration_sec: number; aspect_ratio: "16:9" };
  content_policy: {
    presentation_only: true;
    may_represent_business_step: false;
    may_replace_captured_ui: false;
    requires_explicit_review: true;
  };
  failure_policy: "continue_without_generated_candidate";
};

export type FinalFilmCandidate = {
  candidate_id: string;
  intent_id: string;
  provider: string;
  status: string;
  normalized_artifact: {
    path: string;
    mime_type: string;
    sha256: string;
    size_bytes: number;
    probe: { width?: number; height?: number; fps?: number; duration_sec?: number };
  };
};

export type FinalFilmGeneratedTrack = {
  intents?: Array<{ intent_id: string; purpose: string }>;
  candidates?: FinalFilmCandidate[];
  structural_reviews?: Array<{ review_id: string; candidate_id: string; intent_id: string; status: string }>;
  content_reviews?: Array<{ review_id: string; candidate_id: string; decision: string; content_approved: boolean }>;
  candidate_sets?: Array<{ set_id: string; intent_id: string; candidates: Array<{ candidate_id: string; provider: string }> }>;
  selections?: FinalFilmSelection[];
  editor_approvals?: Array<{ approval_id: string; selection_id: string; candidate_id: string; placement: string; anchor_after_step_id?: string }>;
  patch_proposals?: Array<{ patch_id: string; candidate_id: string; intent_id: string; placement: string; anchor_after_step_id?: string }>;
};

export type FinalFilmSelection = { selection_id: string; set_id: string; intent_id: string; selected_candidate_id: string };

export type FinalFilmJob = {
  schema_version: string;
  job_id: string;
  editor_session_id: string;
  editor_revision: number;
  state: string;
  phase?: string;
  revision: number;
  constraints: {
    constraint_set_id: string;
    required_step_order: string[];
    target_duration_ms: number;
  };
  baseline_plan: EditorPlan;
  presentation_intents?: FinalFilmPresentationIntent[];
  director_plan?: { plan_id: string; specs: Array<{ spec_id: string; intent_id: string; purpose: string; prompt_sha256: string }> };
  generation_authorized: boolean;
  generated_track?: FinalFilmGeneratedTrack;
  generation_skip_reason?: string;
  baseline_render?: { status?: string; video_path?: string };
  final_render?: { status?: string; video_path?: string };
  applied_generated_patch_ids?: string[];
  final_output_validation?: { status: string; width?: number; height?: number; fps?: number; duration_ms?: number; error?: string };
  last_error?: { code: string; message: string; retryable: boolean };
  automation_profile?: "guided-demo-v1";
  run_authorization?: { authorization_ref: string; max_provider_calls: number; provider_calls_used: number };
  evidence_digest?: { digest_id: string; source_duration_ms: number; interaction_density: number };
  quality_reports?: Array<{ report_id: string; intent_id: string; provider: string; attempt: number; score: number; decision: string; findings?: string[] }>;
  review_package?: { package_id: string; job_revision: number; directory_path: string; zip_path: string; manifest_path: string };
  final_review?: { decision: string; reason?: string; reviewer_ref: string };
};

export type FinalFilmEvent = {
  event_id: string;
  sequence: number;
  state: string;
  phase?: string;
  message: string;
  created_at: string;
};

export type FinalFilmClient = {
  mode: "local" | "unavailable";
  create(session: EditorSession, intents: FinalFilmPresentationIntent[]): Promise<EditorClientResult<FinalFilmJob>>;
  createAutomated(session: EditorSession): Promise<EditorClientResult<FinalFilmJob>>;
  get(jobID: string): Promise<EditorClientResult<FinalFilmJob>>;
  events(jobID: string): Promise<EditorClientResult<FinalFilmEvent[]>>;
  renderBaseline(jobID: string): Promise<EditorClientResult<FinalFilmJob>>;
  planDirector(jobID: string, revision: number): Promise<EditorClientResult<FinalFilmJob>>;
  decideGeneration(jobID: string, revision: number, approved: boolean, reason?: string): Promise<EditorClientResult<FinalFilmJob>>;
  generate(jobID: string, revision: number, preferredProvider: FinalFilmProvider): Promise<EditorClientResult<FinalFilmJob>>;
  review(jobID: string, revision: number, decision: Record<string, unknown>): Promise<EditorClientResult<FinalFilmJob>>;
  select(jobID: string, revision: number, decision: Record<string, unknown>): Promise<EditorClientResult<FinalFilmJob>>;
  approve(jobID: string, revision: number, decision: Record<string, unknown>): Promise<EditorClientResult<FinalFilmJob>>;
  apply(jobID: string, revision: number, patchIDs: string[]): Promise<EditorClientResult<FinalFilmJob>>;
  candidateMediaURL(jobID: string, candidateID: string): string;
  runAutomation(jobID: string, revision: number, authorizationRef: string, maxProviderCalls: number): Promise<EditorClientResult<FinalFilmJob>>;
  finalReview(jobID: string, revision: number, decision: "accept" | "reject", reviewerRef: string, packageID: string, reason?: string): Promise<EditorClientResult<FinalFilmJob>>;
  finalMediaURL(jobID: string): string;
  reviewPackageURL(jobID: string): string;
};

type BridgeEnvelope<T> = { ok: boolean; data?: T; error?: string };

export function createFinalFilmClient(): FinalFilmClient {
  const baseURL = String(import.meta.env.VITE_CASCADE_BRIDGE_URL || "").replace(/\/$/, "");
  if (import.meta.env.VITE_CASCADE_BRIDGE !== "local") return unavailableFinalFilmClient();
  const request = async <T>(path: string, init?: RequestInit): Promise<EditorClientResult<T>> => {
    try {
      const response = await fetch(`${baseURL}${path}`, {
        ...init,
        headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
      });
      const raw = await response.text();
      let payload: BridgeEnvelope<T> | undefined;
      try {
        payload = JSON.parse(raw) as BridgeEnvelope<T>;
      } catch {
        return { ok: false, error: `HTTP ${response.status}: ${raw.replace(/\s+/g, " ").slice(0, 160)}` };
      }
      if (!response.ok || !payload.ok || payload.data === undefined) return { ok: false, error: payload.error || `HTTP ${response.status}` };
      return { ok: true, data: payload.data };
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : String(error) };
    }
  };
  const post = <T>(path: string, body?: unknown) => request<T>(path, { method: "POST", ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
  const jobPath = (jobID: string, suffix = "") => `/v1/final-film/jobs/${encodeURIComponent(jobID)}${suffix}`;
  return {
    mode: "local",
    create: (session, intents) => post<FinalFilmJob>("/v1/final-film/jobs", {
      editor_session_id: session.session_id,
      expected_revision: session.revision,
      presentation_generation_intents: intents,
    }),
    createAutomated: (session) => post<FinalFilmJob>("/v1/final-film/jobs", {
      editor_session_id: session.session_id,
      expected_revision: session.revision,
      automation_profile: "guided-demo-v1",
    }),
    get: (jobID) => request<FinalFilmJob>(jobPath(jobID)),
    events: (jobID) => request<FinalFilmEvent[]>(jobPath(jobID, "/events")),
    renderBaseline: (jobID) => post<FinalFilmJob>(jobPath(jobID, "/render")),
    planDirector: (jobID, revision) => post<FinalFilmJob>(jobPath(jobID, "/plan-director"), { expected_revision: revision }),
    decideGeneration: (jobID, revision, approved, reason) => post<FinalFilmJob>(jobPath(jobID, "/generation-approval"), { expected_revision: revision, approved, ...(reason ? { reason } : {}) }),
    generate: (jobID, revision, preferredProvider) => post<FinalFilmJob>(jobPath(jobID, "/generate"), { expected_revision: revision, preferred_provider: preferredProvider }),
    review: (jobID, revision, decision) => post<FinalFilmJob>(jobPath(jobID, "/content-review"), { expected_revision: revision, decision }),
    select: (jobID, revision, decision) => post<FinalFilmJob>(jobPath(jobID, "/selection"), { expected_revision: revision, decision }),
    approve: (jobID, revision, decision) => post<FinalFilmJob>(jobPath(jobID, "/editor-approval"), { expected_revision: revision, decision }),
    apply: (jobID, revision, patchIDs) => post<FinalFilmJob>(jobPath(jobID, "/apply-patch"), { expected_revision: revision, patch_ids: patchIDs }),
    candidateMediaURL: (jobID, candidateID) => jobPath(jobID, `/media/${encodeURIComponent(candidateID)}`),
    runAutomation: (jobID, revision, authorizationRef, maxProviderCalls) => post<FinalFilmJob>(jobPath(jobID, "/run"), { expected_revision: revision, authorization_ref: authorizationRef, max_provider_calls: maxProviderCalls }),
    finalReview: (jobID, revision, decision, reviewerRef, packageID, reason) => post<FinalFilmJob>(jobPath(jobID, "/final-review"), { expected_revision: revision, decision, reviewer_ref: reviewerRef, package_id: packageID, ...(reason ? { reason } : {}) }),
    finalMediaURL: (jobID) => jobPath(jobID, "/final-media"),
    reviewPackageURL: (jobID) => jobPath(jobID, "/review-package"),
  };
}

export function buildFinalFilmIntent(purpose: FinalFilmPurpose, durationSec: number, nonce = Date.now()): FinalFilmPresentationIntent {
  return {
    intent_id: `presentation_${purpose}_${nonce}`,
    capability: "presentation_video_candidate",
    purpose,
    required: false,
    requested_slot: { preferred_duration_sec: Math.max(4, Math.min(15, Math.round(durationSec))), aspect_ratio: "16:9" },
    content_policy: {
      presentation_only: true,
      may_represent_business_step: false,
      may_replace_captured_ui: false,
      requires_explicit_review: true,
    },
    failure_policy: "continue_without_generated_candidate",
  };
}

function unavailableFinalFilmClient(): FinalFilmClient {
  const unavailable = async <T>(): Promise<EditorClientResult<T>> => ({ ok: false, error: "最终成片工作流只在本地 Bridge 模式可用。" });
  return {
    mode: "unavailable",
    create: unavailable, createAutomated: unavailable, get: unavailable, events: unavailable, renderBaseline: unavailable, planDirector: unavailable,
    decideGeneration: unavailable, generate: unavailable, review: unavailable, select: unavailable, approve: unavailable, apply: unavailable,
    candidateMediaURL: () => "", runAutomation: unavailable, finalReview: unavailable, finalMediaURL: () => "", reviewPackageURL: () => "",
  };
}
