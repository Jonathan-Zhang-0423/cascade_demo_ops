import { useEffect, useRef, useState, type FormEvent } from "react";
import type { DesktopBridgeClient } from "./bridge";
import type { AssistantContextView, AssistantProposalView, AssistantSessionView, ConfigurationSourceRefView, ProjectWorkstationView } from "./domain";

type Props = {
  bridge: DesktopBridgeClient;
  context: AssistantContextView;
  onSessionChange?: (session: AssistantSessionView) => void;
  onWorkstationChange?: (view: ProjectWorkstationView) => void;
};

export function AssistantConversationPanel({ bridge, context, onSessionChange, onWorkstationChange }: Props) {
  const [session, setSession] = useState<AssistantSessionView>();
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [clientAction, setClientAction] = useState<AssistantProposalView>();
  const [githubURL, setGitHubURL] = useState("");
  const [githubToken, setGitHubToken] = useState("");
  const [credential, setCredential] = useState({ username: "", password: "" });
  const listRef = useRef<HTMLDivElement>(null);

  function applySession(next: AssistantSessionView) {
    setSession(next);
    onSessionChange?.(next);
    if (next.activeWorkstation) onWorkstationChange?.(next.activeWorkstation);
  }

  useEffect(() => {
    let mounted = true;
    setLoading(true);
    bridge.createAssistantSession(context).then((result) => {
      if (!mounted) return;
      if (result.ok && result.data) applySession(result.data);
      else setError(result.error ?? "Cascade Agent 暂时不可用");
      setLoading(false);
    });
    return () => { mounted = false; };
  }, [bridge, context.projectID, context.scopeKey, context.surface]);

  useEffect(() => {
    if (!session) return;
    const timer = window.setInterval(() => {
      void bridge.listAssistantEvents(session.id, session.lastEventID).then((events) => {
        if (!events.ok || !events.data?.length) return;
        void bridge.getAssistantSession(session.id).then((next) => { if (next.ok && next.data) applySession(next.data); });
      });
    }, 1800);
    return () => window.clearInterval(timer);
  }, [bridge, session?.id, session?.lastEventID]);

  useEffect(() => { if (listRef.current) listRef.current.scrollTop = listRef.current.scrollHeight; }, [session?.messages.length, loading]);

  async function send(event: FormEvent) {
    event.preventDefault();
    const message = input.trim();
    if (!message || !session || loading) return;
    setLoading(true); setError("");
    const result = await bridge.submitAssistantTurn(session.id, message, `turn_${crypto.randomUUID()}`);
    if (result.ok && result.data) { applySession(result.data); setInput(""); }
    else setError(result.error ?? "消息发送失败");
    setLoading(false);
  }

  async function decide(proposal: AssistantProposalView, action: "confirm" | "dismiss") {
    if (!session) return;
    setLoading(true); setError("");
    const result = action === "confirm"
      ? await bridge.confirmAssistantProposal(session.id, proposal.id, proposal.baseVersion, `${proposal.id}:confirm`)
      : await bridge.dismissAssistantProposal(session.id, proposal.id, proposal.baseVersion, `${proposal.id}:dismiss`);
    if (result.ok && result.data) {
      applySession(result.data);
      const confirmed = result.data.messages.flatMap((message) => message.proposals ?? []).find((item) => item.id === proposal.id);
      if (action === "confirm" && confirmed?.executionResult?.clientActionRequired) await executeClientAction(confirmed);
    } else setError(result.error ?? "提案处理失败");
    setLoading(false);
  }

  async function executeClientAction(proposal: AssistantProposalView) {
    let sources: ConfigurationSourceRefView[] = [];
    if (proposal.kind === "select_local_project") {
      const result = await bridge.selectLocalProjectDirectory();
      if (result.ok && result.data) sources = [result.data]; else setError(result.error ?? "目录选择失败");
    } else if (proposal.kind === "attach_requirement_document") {
      const result = await bridge.selectRequirementDocuments();
      if (result.ok && result.data) sources = result.data; else setError(result.error ?? "文档选择失败");
    } else if (proposal.kind === "attach_brand_asset") {
      const result = await bridge.selectBrandAssets();
      if (result.ok && result.data) sources = result.data; else setError(result.error ?? "素材选择失败");
    } else if (proposal.kind === "connect_github" || proposal.kind === "store_demo_credential") { setClientAction(proposal); return; }
    if (sources.length && session) await proposeSourcePatch(sources);
  }

  async function startSafeAction(kind: AssistantProposalView["kind"]) {
    const proposal: AssistantProposalView = { id: `client_${crypto.randomUUID()}`, kind, title: "安全输入", description: "由桌面应用处理", baseVersion: session?.configuration.version ?? 1, idempotencyKey: `client_${crypto.randomUUID()}`, requiresConfirmation: true, status: "confirmed", executionResult: { clientActionRequired: true } };
    await executeClientAction(proposal);
  }

  async function proposeSourcePatch(sources: ConfigurationSourceRefView[]) {
    if (!session) return;
    const existing = session.configuration.sources ?? [];
    const patch = { sources: [...existing, ...sources.filter((source) => !existing.some((item) => item.ref === source.ref))] };
    const result = await bridge.submitAssistantTurn(session.id, `已通过安全选择器添加 ${sources.length} 项来源，请生成 configuration patch。`, `source_${crypto.randomUUID()}`, { selectedSources: patch.sources });
    if (result.ok && result.data) applySession(result.data);
    else setError(result.error ?? "来源提案生成失败");
  }

  async function submitClientAction(event: FormEvent) {
    event.preventDefault();
    if (!clientAction) return;
    if (clientAction.kind === "connect_github") {
      if (githubToken.trim()) {
        const stored = await bridge.storeGitHubToken(githubToken);
        if (!stored.ok) { setError(stored.error ?? "GitHub 凭据保存失败"); return; }
      }
      if (session) {
        const source: ConfigurationSourceRefView = { ref: `github_${crypto.randomUUID()}`, kind: "github_repository", label: githubURL.trim().split("/").pop() || "GitHub repository", url: githubURL.trim() };
        const result = await bridge.submitAssistantTurn(session.id, "已通过安全卡片连接 GitHub，请生成 configuration patch。", `github_${crypto.randomUUID()}`, { selectedSources: [source] });
        if (result.ok && result.data) applySession(result.data); else { setError(result.error ?? "GitHub 来源提案失败"); return; }
      }
      setGitHubToken("");
    } else {
      const ref = `demo_${crypto.randomUUID()}`;
      const stored = await bridge.storeDemoCredential(ref, credential.username, credential.password);
      if (!stored.ok || !stored.data) { setError(stored.error ?? "测试账号保存失败"); return; }
      if (session) {
        const result = await bridge.submitAssistantTurn(session.id, "测试账号已安全保存，请生成 credential ref patch。", `credential_${crypto.randomUUID()}`, { credentialRefs: [stored.data.secretRef] });
        if (result.ok && result.data) applySession(result.data); else { setError(result.error ?? "凭据引用提案失败"); return; }
      }
      setCredential({ username: "", password: "" });
    }
    setClientAction(undefined);
  }

  return <section className="assistant-dialogue" aria-label="Cascade Agent">
    <header className="assistant-dialogue-header"><span className="assistant-logo-mark"><img src="/Cascade_launcher_mark.png" alt="" /></span><div><span>Cascade Agent</span><strong>项目 Configuration</strong><small>{session?.workstationStatus ?? "通过对话整理演示需求"}</small></div></header>
    <div ref={listRef} className="assistant-message-list" aria-live="polite">
      {session?.messages.map((message) => <article key={message.id} className={`assistant-message assistant-message-${message.role}`}><div className="assistant-message-meta"><span>{message.role === "agent" ? "Cascade" : "你"}</span><span>{message.kind}</span></div><p>{message.text}</p>{message.proposals?.length ? <div className="assistant-proposal-list">{message.proposals.map((proposal) => <div className="assistant-proposal" key={proposal.id}><strong>{proposal.title}</strong><span>{proposal.description}</span>{proposal.patch ? <PatchPreview patch={proposal.patch} /> : null}<div>{proposal.status === "available" ? <><button type="button" className="assistant-confirm" onClick={() => void decide(proposal, "confirm")}>确认</button><button type="button" className="assistant-dismiss" onClick={() => void decide(proposal, "dismiss")}>暂不</button></> : <small>{proposal.status === "confirmed" ? "已确认" : "已忽略"}</small>}</div></div>)}</div> : null}</article>)}
      {loading ? <div className="assistant-typing">Cascade 正在整理…</div> : null}
      {error ? <div className="assistant-error" role="alert">{error}</div> : null}
      <div className="assistant-safe-actions" aria-label="安全添加项目来源"><span>安全添加</span><button type="button" onClick={() => void startSafeAction("select_local_project")}>本地项目</button><button type="button" onClick={() => void startSafeAction("connect_github")}>GitHub</button><button type="button" onClick={() => void startSafeAction("attach_requirement_document")}>需求文档</button><button type="button" onClick={() => void startSafeAction("attach_brand_asset")}>品牌素材</button><button type="button" onClick={() => void startSafeAction("store_demo_credential")}>测试账号</button></div>
      {clientAction ? <form className="assistant-secure-card" onSubmit={submitClientAction}><strong>{clientAction.kind === "connect_github" ? "连接 GitHub" : "保存测试账号"}</strong><small>敏感值直接写入 Windows Credential Manager，不进入聊天或日志。</small>{clientAction.kind === "connect_github" ? <><input type="url" required value={githubURL} onChange={(event) => setGitHubURL(event.currentTarget.value)} placeholder="https://github.com/org/repo" /><input type="password" value={githubToken} onChange={(event) => setGitHubToken(event.currentTarget.value)} placeholder="私有仓库 token（可选）" /></> : <><input required value={credential.username} onChange={(event) => setCredential((current) => ({ ...current, username: event.currentTarget.value }))} placeholder="测试账号" /><input type="password" required value={credential.password} onChange={(event) => setCredential((current) => ({ ...current, password: event.currentTarget.value }))} placeholder="密码" /></>}<div><button type="submit" className="assistant-confirm">安全保存</button><button type="button" className="assistant-dismiss" onClick={() => setClientAction(undefined)}>取消</button></div></form> : null}
    </div>
    <form className="assistant-composer" onSubmit={send}><textarea value={input} onChange={(event) => setInput(event.currentTarget.value)} rows={3} placeholder="例如：为我们的开发者工具制作一段 60 秒演示，面向技术负责人…" /><div className="assistant-composer-footer"><span>所有变更都需要确认，上传另行审批</span><button type="submit" disabled={loading || !input.trim()}>发送</button></div></form>
  </section>;
}


function PatchPreview({ patch }: { patch: Record<string, unknown> }) {
  return <dl className="assistant-patch-preview">{Object.entries(patch).map(([key, value]) => <div key={key}><dt>{fieldLabel(key)}</dt><dd>{Array.isArray(value) ? value.map((item) => typeof item === "object" ? JSON.stringify(item) : String(item)).join("，") : String(value)}</dd></div>)}</dl>;
}

function fieldLabel(key: string) {
  return ({ projectName: "项目名称", productURL: "产品 URL", objective: "演示目标", targetAudience: "目标受众", targetDurationSec: "目标时长", mustShow: "必须展示", mustNotShow: "禁止展示", forbiddenPages: "禁止页面", forbiddenData: "敏感数据", brandTone: "品牌语气", sources: "项目来源", allowedDomains: "允许域名" } as Record<string, string>)[key] ?? key;
}
