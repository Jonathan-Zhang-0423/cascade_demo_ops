import type { EditorClientResult } from "./editor";

export type ExecutionPortState = "created" | "admitted" | "queued" | "running" | "waiting_input" | "waiting_external" | "succeeded" | "failed" | "canceled" | "expired";

export type ExperimentArtifactRef = { artifact_id: string; revision: number; role: string };
export type ExperimentCheckpoint = {
  checkpoint_id: string;
  verified_phase: string;
  state_fingerprint_ref?: string;
  result_entry_ref?: string;
  segment_refs?: ExperimentArtifactRef[];
  once_effect_records: Array<{ effect_id: string; kind: string; status: "started" | "confirmed" | "uncertain"; idempotency_key: string; evidence_refs?: string[] }>;
};
export type ExperimentLeg = {
  leg_id: string;
  kind: "main" | "recovery";
  project_name: string;
  state: ExecutionPortState;
  phase: string;
  browser_attempt: number;
  visual_calls_used: number;
  target_submissions: number;
  checkpoint?: ExperimentCheckpoint;
  artifact_refs?: ExperimentArtifactRef[];
};
export type ExperimentRun = {
  schema_version: "demoops.experiment_run.v1";
  run_id: string;
  definition_id: string;
  workflow_template_id: "async-product-build-demo-v1";
  state: ExecutionPortState;
  phase: string;
  revision: number;
  budget: { target_submissions: number; final_film_jobs: number; provider_calls: number; visual_calls_per_run: number };
  legs: ExperimentLeg[];
  provider_calls_used: number;
  final_film?: { job_id: string; revision: number; package_id?: string; decision?: string };
  waiting?: { reason: string; responsibility: string; next_action: string };
  last_error?: { code: string; message: string; retryable: boolean; responsibility: string; evidence_refs?: string[] };
  report?: { valid: boolean; model_consumption: { visual_calls: number; provider_calls: number }; automation_attribution: { out_of_band_browser_actions: number; allowed_human_gates: number; system_actions: number } };
};
export type ExperimentEvent = { event_id: string; sequence: number; state: ExecutionPortState; phase: string; leg_id?: string; type: string; summary: string; evidence_refs?: string[]; created_at: string };

export type StartExperimentRequest = { definition_ref: string; target_url: string; credential_ref: string; authorization_ref: string; idempotency_key: string };

export type ExperimentRunClient = {
  mode: "local" | "unavailable";
  start(request: StartExperimentRequest): Promise<EditorClientResult<ExperimentRun>>;
  get(runID: string): Promise<EditorClientResult<ExperimentRun>>;
  events(runID: string): Promise<EditorClientResult<ExperimentEvent[]>>;
  resume(runID: string, revision: number): Promise<EditorClientResult<ExperimentRun>>;
  finalReview(runID: string, revision: number, finalFilmRevision: number, decision: "accept" | "reject", packageID: string, reviewerRef: string, reason?: string): Promise<EditorClientResult<ExperimentRun>>;
};

type BridgeEnvelope<T> = { ok: boolean; data?: T; error?: string };

export function createExperimentRunClient(): ExperimentRunClient {
  const baseURL = String(import.meta.env.VITE_CASCADE_BRIDGE_URL || "").replace(/\/$/, "");
  if (import.meta.env.VITE_CASCADE_BRIDGE !== "local") return unavailableClient();
  const request = async <T>(path: string, init?: RequestInit): Promise<EditorClientResult<T>> => {
    try {
      const response = await fetch(`${baseURL}${path}`, { ...init, headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) } });
      const raw = await response.text();
      let envelope: BridgeEnvelope<T>;
      try { envelope = JSON.parse(raw) as BridgeEnvelope<T>; } catch { return { ok: false, error: `HTTP ${response.status}: ${raw.replace(/\s+/g, " ").slice(0, 160)}` }; }
      if (!response.ok || !envelope.ok || envelope.data === undefined) return { ok: false, error: envelope.error || `HTTP ${response.status}` };
      return { ok: true, data: envelope.data };
    } catch (error) {
      return { ok: false, error: error instanceof Error ? error.message : String(error) };
    }
  };
  const post = <T>(path: string, body: unknown) => request<T>(path, { method: "POST", body: JSON.stringify(body) });
  const runPath = (runID: string, suffix = "") => `/v1/experiment-runs/${encodeURIComponent(runID)}${suffix}`;
  return {
    mode: "local",
    start: (value) => post<ExperimentRun>("/v1/experiment-runs", value),
    get: (runID) => request<ExperimentRun>(runPath(runID)),
    events: (runID) => request<ExperimentEvent[]>(runPath(runID, "/events")),
    resume: (runID, revision) => post<ExperimentRun>(runPath(runID, "/resume"), { expected_revision: revision }),
    finalReview: (runID, revision, finalFilmRevision, decision, packageID, reviewerRef, reason) => post<ExperimentRun>(runPath(runID, "/final-review"), { expected_revision: revision, final_film_revision: finalFilmRevision, decision, package_id: packageID, reviewer_ref: reviewerRef, ...(reason ? { reason } : {}) }),
  };
}

export function experimentRunStatusCopy(run: ExperimentRun): { title: string; detail: string; blocked: boolean } {
  if (run.state === "waiting_input") return { title: "实验等待处理", detail: run.waiting?.reason || `当前阶段：${run.phase}`, blocked: true };
  if (run.state === "waiting_external") return { title: "正在等待外部结果", detail: `当前阶段：${run.phase}`, blocked: false };
  if (run.state === "succeeded") return { title: "全链路实验已完成", detail: "审核包和自动化归因报告已冻结。", blocked: false };
  if (["failed", "canceled", "expired"].includes(run.state)) return { title: "实验已停止", detail: run.last_error?.message || `终态：${run.state}`, blocked: true };
  return { title: "实验执行中", detail: `当前阶段：${run.phase}`, blocked: false };
}

function unavailableClient(): ExperimentRunClient {
  const unavailable = async <T>(): Promise<EditorClientResult<T>> => ({ ok: false, error: "模块化实验只在本地 Bridge 模式可用。" });
  return { mode: "unavailable", start: unavailable, get: unavailable, events: unavailable, resume: unavailable, finalReview: unavailable };
}
