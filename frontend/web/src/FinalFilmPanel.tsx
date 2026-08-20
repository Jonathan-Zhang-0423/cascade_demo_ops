import { useMemo, useState } from "react";
import type { ReactNode } from "react";
import type { EditorSession } from "./editor";
import { buildFinalFilmIntent, createFinalFilmClient } from "./finalFilm";
import type { FinalFilmCandidate, FinalFilmEvent, FinalFilmJob, FinalFilmProvider, FinalFilmPurpose, FinalFilmSelection } from "./finalFilm";

type SafetyKey = "purpose" | "ui" | "facts" | "text" | "references";

const safetyLabels: Record<SafetyKey, string> = {
  purpose: "内容只服务于已声明的展示用途",
  ui: "没有伪造或替换产品 UI",
  facts: "没有声称业务步骤、结果或事实",
  text: "没有未经验证的文字、数字或 Logo",
  references: "没有篡改参考素材中的业务事实",
};

export function FinalFilmPanel({ session, onClose }: { session: EditorSession; onClose: () => void }) {
  const client = useMemo(() => createFinalFilmClient(), []);
  const [job, setJob] = useState<FinalFilmJob>();
  const [events, setEvents] = useState<FinalFilmEvent[]>([]);
  const [resumeID, setResumeID] = useState("");
  const [purposes, setPurposes] = useState<Record<FinalFilmPurpose, boolean>>({ intro: true, outro: true, section_divider: false, abstract_broll: false, brand_atmosphere: false });
  const [durationSec, setDurationSec] = useState(5);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [safety, setSafety] = useState<Record<SafetyKey, boolean>>({ purpose: false, ui: false, facts: false, text: false, references: false });
  const [reviewReason, setReviewReason] = useState("已在本地播放器完整检查，内容仅作展示用途。");
  const [anchorByIntent, setAnchorByIntent] = useState<Record<string, string>>({});
  const [preferredProvider, setPreferredProvider] = useState<FinalFilmProvider>("minimax-h3");

  const track = job?.generated_track;
  const pendingCandidate = track?.candidates?.find((candidate) => !track.content_reviews?.some((review) => review.candidate_id === candidate.candidate_id));
  const pendingSet = track?.candidate_sets?.find((set) => !track.selections?.some((selection) => selection.set_id === set.set_id));
  const pendingSelection = track?.selections?.find((selection) => !track.editor_approvals?.some((approval) => approval.selection_id === selection.selection_id));
  const purposeByIntent = Object.fromEntries((job?.presentation_intents ?? []).map((intent) => [intent.intent_id, intent.purpose])) as Record<string, FinalFilmPurpose>;
  const pendingPurpose = pendingSelection ? purposeByIntent[pendingSelection.intent_id] : undefined;

  async function accept(result: Awaited<ReturnType<typeof client.get>>, action: string) {
    if (!result.ok || !result.data) {
      setError(result.error || `${action}失败`);
      return;
    }
    setJob(result.data);
    setResumeID(result.data.job_id);
    setError("");
    const audit = await client.events(result.data.job_id);
    if (audit.ok && audit.data) setEvents(audit.data);
  }

  async function run(action: string, operation: () => Promise<Awaited<ReturnType<typeof client.get>>>) {
    setBusy(action);
    setError("");
    try {
      await accept(await operation(), action);
    } finally {
      setBusy("");
    }
  }

  async function createJob() {
    const selected = (Object.keys(purposes) as FinalFilmPurpose[]).filter((purpose) => purposes[purpose]);
    const nonce = Date.now();
    const intents = selected.map((purpose, index) => buildFinalFilmIntent(purpose, durationSec, nonce + index));
    await run("create", () => client.create(session, intents));
  }

  async function resumeJob() {
    const id = resumeID.trim();
    if (!id) return setError("请输入要恢复的 FinalFilm Job ID。");
    await run("resume", () => client.get(id));
  }

  async function submitReview(approved: boolean) {
    if (!job || !pendingCandidate) return;
    const structural = track?.structural_reviews?.find((review) => review.candidate_id === pendingCandidate.candidate_id);
    if (!structural) return setError("候选缺少结构审核记录，不能进入人工内容审核。");
    if (approved && !Object.values(safety).every(Boolean)) return setError("批准前必须逐项确认全部五条内容安全断言。");
    if (!approved && !reviewReason.trim()) return setError("拒绝候选时必须填写原因。");
    const now = new Date().toISOString();
    await run("review", () => client.review(job.job_id, job.revision, {
      review_id: localID("content_review"), structural_review_id: structural.review_id,
      candidate_id: pendingCandidate.candidate_id, intent_id: pendingCandidate.intent_id,
      reviewer_id: "local_human_reviewer", reviewer_kind: "human", reviewed_at: now,
      decision: approved ? "approve" : "reject",
      assertions: {
        purpose_matches_intent: approved && safety.purpose,
        no_captured_ui_replacement: approved && safety.ui,
        no_business_fact_claims: approved && safety.facts,
        no_unverified_text_or_numbers: approved && safety.text,
        no_reference_fact_mutation: approved && safety.references,
      },
      evidence_refs: [`review-ui://${job.job_id}/${pendingCandidate.candidate_id}/${now}`], reason: reviewReason.trim(),
    }));
    setSafety({ purpose: false, ui: false, facts: false, text: false, references: false });
  }

  async function selectCandidate(candidate: FinalFilmCandidate) {
    if (!job || !pendingSet) return;
    await run("select", () => client.select(job.job_id, job.revision, {
      selection_id: localID("selection"), set_id: pendingSet.set_id, candidate_id: candidate.candidate_id,
      selector_id: "local_human_selector", selector_kind: "human", selected_at: new Date().toISOString(),
      evidence_refs: [`selection-ui://${job.job_id}/${pendingSet.set_id}`], reason: "人工比较并选择该展示候选。",
    }));
  }

  async function approveSelection() {
    if (!job || !pendingSelection) return;
    const purpose = purposeByIntent[pendingSelection.intent_id];
    if (!purpose) return setError("选择记录缺少对应的展示用途，不能创建 Editor 补丁。");
    const placement = placementForPurpose(purpose);
    const anchor = anchorByIntent[pendingSelection.intent_id] || defaultAnchor(job, purpose);
    if ((placement === "between_sections" || placement === "presentation_gap") && !anchor) return setError("章节分隔/B-roll 必须选择一个事实步骤锚点。");
    await run("approve", () => client.approve(job.job_id, job.revision, {
      approval_id: localID("editor_approval"), selection_id: pendingSelection.selection_id,
      candidate_id: pendingSelection.selected_candidate_id, approver_id: "local_human_editor", approver_kind: "human",
      approved_at: new Date().toISOString(), target_plan_id: job.baseline_plan.plan_id,
      expected_plan_revision: job.editor_revision, placement,
      ...(anchor ? { anchor_after_step_id: anchor } : {}),
      evidence_refs: [`editor-ui://${job.job_id}/${pendingSelection.selection_id}`],
      reason: "已确认展示镜头位置，不改变事实步骤、素材绑定或源时间范围。",
    }));
  }

  const terminal = job && ["completed", "completed_without_generated_track", "failed", "cancelled"].includes(job.state);
  const stateAction = job ? primaryAction(job, client, run, preferredProvider, setPreferredProvider) : undefined;

  return (
    <div className="studio-modal-backdrop final-film-backdrop" role="presentation" onPointerDown={(event) => { if (event.currentTarget === event.target && !busy) onClose(); }}>
      <section className="studio-modal final-film-modal" role="dialog" aria-modal="true" aria-label="最终成片工作流">
        <header className="studio-modal-header">
          <div><strong>最终成片 · 受控生成工作流</strong><small>{job ? `${job.job_id} · r${job.revision}` : "事实轨优先，生成轨可拒绝、可回退"}</small></div>
          <button className="studio-icon-button" disabled={Boolean(busy)} onClick={onClose}>×</button>
        </header>
        <div className="final-film-body">
          <aside className="final-film-steps">
            {workflowSteps.map((step, index) => <div key={step.state} className={stepTone(job, step.states)}><span>{index + 1}</span><div><strong>{step.label}</strong><small>{step.detail}</small></div></div>)}
          </aside>
          <main className="final-film-main">
            {client.mode !== "local" ? <div className="final-film-notice blocked">当前是前端 Mock 模式；请使用本地 Bridge 启动后执行真实 FinalFilm workflow。</div> : null}
            {!job ? <>
              <section className="final-film-card">
                <h3>1. 选择可选展示镜头</h3>
                <p>它们不能替代录屏中的业务步骤。未选任何项目时仍可创建纯事实轨成片。</p>
                <div className="final-film-purpose-grid">{purposeOptions.map((option) => <label key={option.value}><input type="checkbox" checked={purposes[option.value]} onChange={(event) => setPurposes((current) => ({ ...current, [option.value]: event.target.checked }))} /><span><strong>{option.label}</strong><small>{option.detail}</small></span></label>)}</div>
                <label className="studio-field"><span>每段期望时长（4–15 秒）</span><input type="number" min="4" max="15" value={durationSec} onChange={(event) => setDurationSec(Number(event.target.value))} /></label>
                <button className="studio-primary-button" disabled={Boolean(busy) || client.mode !== "local"} onClick={() => void createJob()}>{busy === "create" ? "创建中" : "锁定事实轨并创建作业"}</button>
              </section>
              <section className="final-film-card compact"><h3>恢复已有作业</h3><div className="final-film-inline"><input value={resumeID} onChange={(event) => setResumeID(event.target.value)} placeholder="finalfilm_..." /><button className="studio-outline-button" disabled={Boolean(busy) || client.mode !== "local"} onClick={() => void resumeJob()}>恢复</button></div></section>
            </> : <>
              <section className="final-film-status-card">
                <div><span>当前状态</span><strong>{stateLabel(job.state)}</strong><small>{job.phase || "—"}</small></div>
                <div><span>事实步骤</span><strong>{job.constraints.required_step_order.length}</strong><small>顺序与时间范围锁定</small></div>
                <div><span>生成授权</span><strong>{job.generation_authorized ? "已授权" : "未授权"}</strong><small>API key 不等于授权</small></div>
                <div><span>最终输出</span><strong>{job.final_output_validation?.status || job.final_render?.status || "待生成"}</strong><small>{job.final_output_validation ? `${job.final_output_validation.width ?? 0}×${job.final_output_validation.height ?? 0}` : "失败自动回退 baseline"}</small></div>
              </section>

              {stateAction ? <section className="final-film-card"><h3>{stateAction.title}</h3><p>{stateAction.detail}</p><button className="studio-primary-button" disabled={Boolean(busy)} onClick={() => void stateAction.run()}>{busy ? "处理中…" : stateAction.label}</button>{stateAction.secondary}</section> : null}

              {job.state === "awaiting_generated_content_review" && pendingCandidate ? <CandidateReview job={job} candidate={pendingCandidate} client={client} safety={safety} reason={reviewReason} busy={busy} onSafety={(key, checked) => setSafety((current) => ({ ...current, [key]: checked }))} onReason={setReviewReason} onDecision={(approved) => void submitReview(approved)} /> : null}

              {job.state === "awaiting_generated_candidate_selection" && pendingSet ? <section className="final-film-card"><h3>选择候选</h3><p>选择只是偏好记录，不会自动进入时间线；下一步仍需独立 Editor 批准。</p><div className="final-film-candidates">{(track?.candidates ?? []).filter((candidate) => pendingSet.candidates.some((entry) => entry.candidate_id === candidate.candidate_id)).map((candidate) => <CandidateTile key={candidate.candidate_id} job={job} candidate={candidate} mediaURL={client.candidateMediaURL(job.job_id, candidate.candidate_id)}><button className="studio-primary-button" disabled={Boolean(busy)} onClick={() => void selectCandidate(candidate)}>选择此候选</button></CandidateTile>)}</div></section> : null}

              {job.state === "awaiting_generated_editor_approval" && pendingSelection && pendingPurpose ? <EditorApproval job={job} selection={pendingSelection} purpose={pendingPurpose} anchor={anchorByIntent[pendingSelection.intent_id] || defaultAnchor(job, pendingPurpose)} busy={busy} onAnchor={(anchor) => setAnchorByIntent((current) => ({ ...current, [pendingSelection.intent_id]: anchor }))} onApprove={() => void approveSelection()} /> : null}

              {job.state === "awaiting_generated_patch_apply" ? <section className="final-film-card final-film-explicit"><h3>显式应用全部补丁</h3><p>将按下面的顺序一次校验、一次写入隔离计划并只渲染一次；任何一项失败都会整批拒绝。</p><ol>{(track?.patch_proposals ?? []).map((patch) => <li key={patch.patch_id}><strong>{purposeLabel(purposeByIntent[patch.intent_id])}</strong><span>{patch.placement}{patch.anchor_after_step_id ? ` · after ${patch.anchor_after_step_id}` : ""}</span></li>)}</ol><button className="studio-primary-button" disabled={Boolean(busy)} onClick={() => void run("apply", () => client.apply(job.job_id, job.revision, (track?.patch_proposals ?? []).map((patch) => patch.patch_id)))}>{busy === "apply" ? "合成与验收中…" : "确认应用并生成最终成片"}</button></section> : null}

              {terminal ? <section className={`final-film-card final-film-result ${job.state === "completed" ? "success" : "fallback"}`}><h3>{job.state === "completed" ? "最终成片已通过验收" : "工作流已收敛"}</h3><p>{job.generation_skip_reason || job.last_error?.message || (job.state === "completed" ? "事实轨与已批准展示镜头已完成确定性合成。" : "最终输出使用事实轨基线。")}</p>{job.final_render?.video_path ? <code>{job.final_render.video_path}</code> : null}</section> : null}

              <section className="final-film-audit"><div><h3>审计事件</h3><button className="studio-ghost-button" disabled={Boolean(busy)} onClick={() => void resumeJob()}>刷新</button></div>{events.slice().reverse().map((event) => <article key={event.event_id}><span>#{event.sequence}</span><div><strong>{event.message}</strong><small>{event.state} · {new Date(event.created_at).toLocaleString("zh-CN", { hour12: false })}</small></div></article>)}</section>
            </>}
            {error ? <div className="final-film-error"><strong>操作未完成</strong><span>{error}</span></div> : null}
          </main>
        </div>
      </section>
    </div>
  );
}

function CandidateReview({ job, candidate, client, safety, reason, busy, onSafety, onReason, onDecision }: {
  job: FinalFilmJob; candidate: FinalFilmCandidate; client: ReturnType<typeof createFinalFilmClient>; safety: Record<SafetyKey, boolean>; reason: string; busy: string;
  onSafety: (key: SafetyKey, checked: boolean) => void; onReason: (value: string) => void; onDecision: (approved: boolean) => void;
}) {
  return <section className="final-film-card"><h3>人工内容审核</h3><p>必须在播放器中完整检查候选。系统不会用模型替你勾选这些断言。</p><CandidateTile job={job} candidate={candidate} mediaURL={client.candidateMediaURL(job.job_id, candidate.candidate_id)} />
    <div className="final-film-safety">{(Object.keys(safetyLabels) as SafetyKey[]).map((key) => <label key={key}><input type="checkbox" checked={safety[key]} onChange={(event) => onSafety(key, event.target.checked)} /><span>{safetyLabels[key]}</span></label>)}</div>
    <label className="studio-field"><span>审核证据说明 / 拒绝原因</span><textarea rows={3} value={reason} onChange={(event) => onReason(event.target.value)} /></label>
    <div className="final-film-actions"><button className="studio-ghost-button" disabled={Boolean(busy)} onClick={() => onDecision(false)}>拒绝并保留事实轨</button><button className="studio-primary-button" disabled={Boolean(busy) || !Object.values(safety).every(Boolean)} onClick={() => onDecision(true)}>确认五项并批准内容</button></div>
  </section>;
}

function CandidateTile({ candidate, mediaURL, children }: { job: FinalFilmJob; candidate: FinalFilmCandidate; mediaURL: string; children?: ReactNode }) {
  return <article className="final-film-candidate"><video src={mediaURL} controls preload="metadata" /><div><strong>{candidate.candidate_id}</strong><small>{candidate.provider} · {candidate.normalized_artifact.probe.width ?? 0}×{candidate.normalized_artifact.probe.height ?? 0} · {candidate.normalized_artifact.probe.duration_sec ?? 0}s</small><code>sha256 {candidate.normalized_artifact.sha256.slice(0, 16)}…</code>{children}</div></article>;
}

function EditorApproval({ job, selection, purpose, anchor, busy, onAnchor, onApprove }: { job: FinalFilmJob; selection: FinalFilmSelection; purpose: FinalFilmPurpose; anchor: string; busy: string; onAnchor: (value: string) => void; onApprove: () => void }) {
  const needsAnchor = ["section_divider", "abstract_broll", "brand_atmosphere"].includes(purpose);
  const anchors = needsAnchor && purpose === "section_divider" ? job.constraints.required_step_order.slice(0, -1) : job.constraints.required_step_order;
  return <section className="final-film-card final-film-explicit"><h3>Editor 独立批准</h3><p>候选已通过内容审核和选择，但此操作只授权创建补丁，不会自动应用或渲染。</p><dl><div><dt>用途</dt><dd>{purposeLabel(purpose)}</dd></div><div><dt>候选</dt><dd>{selection.selected_candidate_id}</dd></div><div><dt>位置</dt><dd>{placementForPurpose(purpose)}</dd></div><div><dt>目标计划</dt><dd>{job.baseline_plan.plan_id} · editor r{job.editor_revision}</dd></div></dl>{needsAnchor ? <label className="studio-field"><span>插入到哪个事实步骤之后</span><select value={anchor} onChange={(event) => onAnchor(event.target.value)}>{anchors.map((stepID) => <option key={stepID} value={stepID}>{stepID}</option>)}</select></label> : null}<button className="studio-primary-button" disabled={Boolean(busy) || (needsAnchor && !anchor)} onClick={onApprove}>批准创建非事实轨补丁</button></section>;
}

function primaryAction(job: FinalFilmJob, client: ReturnType<typeof createFinalFilmClient>, run: (name: string, operation: () => Promise<Awaited<ReturnType<typeof client.get>>>) => Promise<void>, preferredProvider: FinalFilmProvider, onProvider: (provider: FinalFilmProvider) => void) {
  if (job.state === "baseline_ready") return { title: "渲染事实轨基线", detail: "先完成无需任何视频模型的确定性基线；之后生成失败仍可交付它。", label: "渲染 baseline", run: () => run("baseline", () => client.renderBaseline(job.job_id)), secondary: null };
  if (job.state === "awaiting_generation_approval" && !job.director_plan) return { title: "Director 制定受控展示方案", detail: "模型只选择视觉风格、运动与色板枚举；服务端负责安全提示词、时长、比例和引用锁定。", label: "运行 Director 规划", run: () => run("director", () => client.planDirector(job.job_id, job.revision)), secondary: null };
  if (job.state === "awaiting_generation_approval" && job.director_plan) return { title: "生成费用与权限确认", detail: `Director 计划 ${job.director_plan.plan_id} 已锁定。只有点击批准后，Server 才能调用视频 Provider。`, label: "批准本次生成", run: () => run("authorize", () => client.decideGeneration(job.job_id, job.revision, true)), secondary: <button className="studio-ghost-button" onClick={() => void run("reject", () => client.decideGeneration(job.job_id, job.revision, false, "用户选择仅交付事实轨基线"))}>拒绝生成并使用 baseline</button> };
  if (job.state === "generating_candidates") return { title: "执行视频候选生成", detail: "Server 将重新检查显式授权、幂等键、Provider capability 和输出目录；H3 还会检查价格预算，Seedance 还要求独立 final-film opt-in。", label: `开始真实 ${preferredProvider === "minimax-h3" ? "H3" : "Seedance 2.5"} workflow`, run: () => run("generate", () => client.generate(job.job_id, job.revision, preferredProvider)), secondary: <label className="studio-field"><span>本次 Provider</span><select value={preferredProvider} onChange={(event) => onProvider(event.target.value as FinalFilmProvider)}><option value="minimax-h3">MiniMax H3</option><option value="seedance-2.5">Seedance 2.5</option></select></label> };
  return undefined;
}

const purposeOptions: Array<{ value: FinalFilmPurpose; label: string; detail: string }> = [
  { value: "intro", label: "片头", detail: "放在第一个事实步骤之前" },
  { value: "outro", label: "片尾", detail: "放在最后一个事实步骤之后" },
  { value: "section_divider", label: "章节分隔", detail: "必须绑定一个步骤锚点" },
  { value: "abstract_broll", label: "抽象 B-roll", detail: "只插入展示空档" },
  { value: "brand_atmosphere", label: "品牌氛围", detail: "不包含 Logo 或可读文字" },
];

const workflowSteps = [
  { state: "baseline", label: "事实轨", detail: "锁定并渲染 baseline", states: ["baseline_ready", "rendering_baseline"] },
  { state: "director", label: "Director", detail: "受控展示配方", states: ["awaiting_generation_approval"] },
  { state: "provider", label: "Provider", detail: "显式授权后生成", states: ["generating_candidates"] },
  { state: "review", label: "人工审核", detail: "内容、选择、Editor", states: ["awaiting_generated_content_review", "awaiting_generated_candidate_selection", "awaiting_generated_editor_approval"] },
  { state: "compose", label: "合成验收", detail: "原子补丁与回退", states: ["awaiting_generated_patch_apply", "validating_final_plan", "rendering", "validating_output", "completed", "completed_without_generated_track"] },
];

function stepTone(job: FinalFilmJob | undefined, states: string[]) {
  if (!job) return "";
  if (states.includes(job.state)) return "active";
  const order = workflowSteps.findIndex((step) => step.states.includes(job.state));
  const own = workflowSteps.findIndex((step) => step.states === states);
  return own < order ? "done" : "";
}

function placementForPurpose(purpose: FinalFilmPurpose): string {
  if (purpose === "intro") return "before_first_required_step";
  if (purpose === "outro") return "after_last_required_step";
  if (purpose === "section_divider") return "between_sections";
  return "presentation_gap";
}

function defaultAnchor(job: FinalFilmJob, purpose: FinalFilmPurpose): string {
  if (purpose === "intro" || purpose === "outro") return "";
  return job.constraints.required_step_order[0] || "";
}

function purposeLabel(purpose: FinalFilmPurpose | undefined): string {
  return purposeOptions.find((option) => option.value === purpose)?.label || purpose || "未知展示用途";
}

function stateLabel(state: string): string {
  return ({ baseline_ready: "基线待渲染", rendering_baseline: "基线渲染中", awaiting_generation_approval: "等待生成决策", generating_candidates: "等待执行生成", awaiting_generated_content_review: "等待内容审核", awaiting_generated_candidate_selection: "等待候选选择", awaiting_generated_editor_approval: "等待 Editor 批准", awaiting_generated_patch_apply: "等待显式应用补丁", rendering: "最终合成中", validating_output: "最终验收中", completed: "已完成", completed_without_generated_track: "已回退事实轨", failed: "失败", cancelled: "已取消" } as Record<string, string>)[state] || state;
}

function localID(prefix: string): string {
  const id = typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `${Date.now()}_${Math.random().toString(16).slice(2)}`;
  return `${prefix}_${id}`;
}
