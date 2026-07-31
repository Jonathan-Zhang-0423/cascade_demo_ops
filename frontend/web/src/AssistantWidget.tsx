import { useEffect, useRef, useState, type FormEvent } from "react";
import type {
  AssistantContextView,
  AssistantMessageView,
  AssistantProposalView,
  AssistantSessionView,
  ConfigurationSourceRefView,
  ProjectWorkstationView,
} from "./domain";
import type { DesktopBridgeClient } from "./bridge";

type AssistantWidgetProps = {
  bridge: DesktopBridgeClient;
  context: AssistantContextView;
  onOpenProject?: (projectID: string) => void;
  onOpenRepositoryForm?: () => void;
  onWorkstationChange?: (view: ProjectWorkstationView) => void;
  initialMessage?: string;
};

type AssistantConversationPanelProps = AssistantWidgetProps & {
  embedded?: boolean;
  showHeader?: boolean;
};

export function AssistantWidget({ bridge, context, onOpenProject, onOpenRepositoryForm, onWorkstationChange }: AssistantWidgetProps) {
  const [open, setOpen] = useState(false);
  const launcherRef = useRef<HTMLButtonElement>(null);

  function close() {
    setOpen(false);
    window.requestAnimationFrame(() => launcherRef.current?.focus());
  }

  return (
    <div className="assistant-widget">
      {!open ? (
        <button ref={launcherRef} type="button" className="assistant-launcher" aria-label="Ask Cascade" title="Ask Cascade" onClick={() => setOpen(true)}>
          <span className="assistant-logo-mark" aria-hidden="true"><img src="/Cascade_launcher_mark.png" alt="" /></span>
        </button>
      ) : (
        <AssistantConversationPanel
          bridge={bridge}
          context={context}
          {...(onOpenProject ? { onOpenProject } : {})}
          {...(onOpenRepositoryForm ? { onOpenRepositoryForm } : {})}
          {...(onWorkstationChange ? { onWorkstationChange } : {})}
          onClose={close}
        />
      )}
    </div>
  );
}

export function AssistantConversationPanel({ bridge, context, onOpenProject, onOpenRepositoryForm, onWorkstationChange, initialMessage, embedded = false, showHeader = true, onClose }: AssistantConversationPanelProps & { onClose?: () => void }) {
  const [session, setSession] = useState<AssistantSessionView>();
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [credentialEntry, setCredentialEntry] = useState<{ ref: string; username: string; password: string }>();
  const [githubEntry, setGithubEntry] = useState<string>();
  const [localProjectEntry, setLocalProjectEntry] = useState<string>();
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const messageListRef = useRef<HTMLDivElement>(null);
  const submittedInitialMessageRef = useRef("");
  const active = embedded || Boolean(onClose);

  function applySession(next: AssistantSessionView) {
    setSession(next);
    if (next.activeWorkstation) onWorkstationChange?.(next.activeWorkstation);
  }

  useEffect(() => {
    if (!active) return;
    let mounted = true;
    setLoading(true);
    setError("");
    bridge.createAssistantSession(context).then((result) => {
      if (!mounted) return;
      if (result.ok && result.data) {
        applySession(result.data);
        window.requestAnimationFrame(() => inputRef.current?.focus());
      } else {
        setError(result.error ?? "Cascade Agent is unavailable.");
      }
      setLoading(false);
    });
    return () => { mounted = false; };
  }, [active, bridge, context.projectID, context.projectName, context.repositoryID, context.repositoryLabel, context.scopeKey, context.surface]);

  useEffect(() => {
    const message = initialMessage?.trim();
    if (!active || !session || !message || loading || submittedInitialMessageRef.current === message) return;
    submittedInitialMessageRef.current = message;
    setLoading(true);
    setError("");
    void bridge.submitAssistantTurn(session.id, message, `initial-${context.scopeKey}`).then((result) => {
      if (result.ok && result.data) applySession(result.data);
      else {
        submittedInitialMessageRef.current = "";
        setError(result.error ?? "Cascade could not start the configuration conversation.");
      }
      setLoading(false);
    });
  }, [active, bridge, context.scopeKey, initialMessage, loading, session?.id]);

  useEffect(() => {
    if (!active || !session) return;
    const timer = window.setInterval(() => {
      void bridge.listAssistantEvents(session.id, session.lastEventID).then((result) => {
        if (!result.ok || !result.data?.length) return;
        void bridge.getAssistantSession(session.id).then((next) => {
          if (next.ok && next.data) applySession(next.data);
        });
      });
    }, 1500);
    return () => window.clearInterval(timer);
  }, [active, bridge, session]);

  useEffect(() => {
    const list = messageListRef.current;
    if (list) list.scrollTop = list.scrollHeight;
  }, [session?.messages.length, loading]);

  useEffect(() => {
    if (!active || embedded) return;
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") onClose?.();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [active, embedded, onClose]);

  async function send(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const message = input.trim();
    if (!message || !session || loading) return;
    setLoading(true);
    setError("");
    const result = await bridge.submitAssistantTurn(session.id, message);
    if (result.ok && result.data) {
      applySession(result.data);
      setInput("");
    } else {
      setError(result.error ?? "Cascade could not respond.");
    }
    setLoading(false);
  }

  async function updateProposal(proposal: AssistantProposalView, action: "confirm" | "dismiss") {
    if (!session) return;
    const result = action === "confirm"
      ? await bridge.confirmAssistantProposal(session.id, proposal.id)
      : await bridge.dismissAssistantProposal(session.id, proposal.id);
    if (result.ok && result.data) {
      applySession(result.data);
      const analysisProjectID = result.data.configuration.analysisProjectID;
      if (action === "confirm" && analysisProjectID && (proposal.kind === "confirm_configuration" || proposal.kind === "start_local_analysis")) onOpenProject?.(analysisProjectID);
      if (action === "confirm" && proposal.targetID && (proposal.kind === "open_project" || proposal.kind === "inspect_project")) onOpenProject?.(proposal.targetID);
      if (action === "confirm" && proposal.kind === "open_repository_form") onOpenRepositoryForm?.();
      if (action === "confirm" && proposal.targetWorkstation) onWorkstationChange?.(proposal.targetWorkstation);
      if (action === "confirm") await executeClientProposal(proposal);
    } else {
      setError(result.error ?? "That action could not be updated.");
    }
  }

  async function submitSafeSelections(selectedSources: ConfigurationSourceRefView[] = [], credentialRefs: string[] = []) {
    if (!session) return;
    setLoading(true);
    const result = await bridge.submitAssistantTurn(session.id, "安全选择已完成，请将引用加入 configuration。", `selection-${Date.now()}`, { selectedSources, credentialRefs });
    if (result.ok && result.data) applySession(result.data);
    else setError(result.error ?? "安全选择无法写入 configuration。");
    setLoading(false);
  }

  async function executeClientProposal(proposal: AssistantProposalView) {
    if (proposal.kind === "select_local_project") {
      const selected = await bridge.selectLocalProjectDirectory();
      if (selected.ok && selected.data) {
        await submitSafeSelections([selected.data]);
      } else if (bridge.mode === "local") {
        setLocalProjectEntry("");
        setError("");
      } else {
        setError(selected.error ?? "未选择本地项目。");
      }
      return;
    }
    if (proposal.kind === "attach_requirement_document") {
      const selected = await bridge.selectRequirementDocuments();
      if (selected.ok && selected.data) await submitSafeSelections(selected.data);
      else setError(selected.error ?? "未选择需求文档。");
      return;
    }
    if (proposal.kind === "attach_brand_asset") {
      const selected = await bridge.selectBrandAssets();
      if (selected.ok && selected.data) await submitSafeSelections(selected.data);
      else setError(selected.error ?? "未选择品牌素材。");
      return;
    }
    if (proposal.kind === "store_demo_credential") {
      setCredentialEntry({ ref: `assistant-${proposal.id}`, username: "", password: "" });
      return;
    }
    if (proposal.kind === "connect_github") {
      setGithubEntry("");
      onOpenRepositoryForm?.();
    }
  }

  async function saveCredential(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!credentialEntry || !credentialEntry.username.trim() || !credentialEntry.password || !session) return;
    setLoading(true);
    const stored = await bridge.storeDemoCredential(credentialEntry.ref, credentialEntry.username, credentialEntry.password);
    if (stored.ok && stored.data?.configured) {
      setCredentialEntry(undefined);
      await submitSafeSelections([], [stored.data.secretRef]);
    } else {
      setError(stored.error ?? "演示凭据无法保存到本地凭据库。");
      setLoading(false);
    }
  }

  async function saveGitHubSource(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const url = githubEntry?.trim() ?? "";
    if (!/^https:\/\/github\.com\/[^/]+\/[^/]+\/?$/i.test(url)) {
      setError("请输入 https://github.com/owner/repository 格式的仓库地址。");
      return;
    }
    setGithubEntry(undefined);
    await submitSafeSelections([{ ref: "", kind: "github_repository", label: url.split("/").filter(Boolean).at(-1) ?? "GitHub repository", url }]);
  }

  async function saveDevLocalProject(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const path = localProjectEntry?.trim() ?? "";
    if (!path || !session) return;
    setLoading(true);
    setError("");
    const registered = await bridge.registerDevLocalProjectDirectory(path);
    if (registered.ok && registered.data) {
      setLocalProjectEntry(undefined);
      await submitSafeSelections([registered.data]);
    } else {
      setError(registered.error ?? "本地项目目录登记失败。");
      setLoading(false);
    }
  }

  return (
    <section className={embedded ? "project-agent-conversation" : "assistant-dialogue"} aria-label={`${context.surface === "projects" ? "Projects" : "Repositories"} Cascade Agent`}>
      {showHeader ? (
        <header className="assistant-dialogue-header">
          <div><span className="assistant-dialogue-kicker">Cascade Agent</span><strong>{context.surface === "projects" ? "Projects" : "Repositories"}</strong><small>{context.projectName ?? context.repositoryLabel ?? "Current workspace context"}</small></div>
          {onClose ? <button type="button" className="assistant-close" aria-label="Close Cascade Agent" onClick={onClose}>×</button> : null}
        </header>
      ) : null}
      <div ref={messageListRef} className="assistant-message-list" aria-live="polite">
        {session?.messages.map((message) => <AssistantMessage key={message.id} message={message} onProposal={updateProposal} />)}
        {loading ? <div className="assistant-typing"><span /><span /><span />Cascade is thinking</div> : null}
        {error ? <div className="assistant-error" role="alert">{error}<button type="button" className="assistant-retry" onClick={() => { setError(""); inputRef.current?.focus(); }}>Try again</button></div> : null}
      </div>
      <form className="assistant-composer" onSubmit={send}>
        <textarea ref={inputRef} value={input} onChange={(event) => setInput(event.currentTarget.value)} rows={2} placeholder={context.surface === "projects" ? "Ask Cascade what to do next…" : "Ask about connected repositories…"} aria-label="Message Cascade Agent" />
        <div className="assistant-composer-footer"><span>Suggestions require confirmation.</span><button type="submit" disabled={loading || !input.trim()}>Send <span aria-hidden="true">↗</span></button></div>
      </form>
      {bridge.mode === "local" && localProjectEntry === undefined && session?.configuration.missingFields?.includes("sources") ? (
        <div className="assistant-secure-entry">
          <strong>还缺本地项目目录</strong>
          <span>浏览器联调必须先在本机 Dev Bridge 登记目录，计划中只保留引用、结构摘要和哈希。</span>
          <div><button type="button" className="assistant-confirm" onClick={() => setLocalProjectEntry("")}>登记本地项目目录</button></div>
        </div>
      ) : null}
      {credentialEntry ? (
        <form className="assistant-secure-entry" onSubmit={saveCredential}>
          <strong>安全保存演示账号</strong>
          <span>内容只进入本机凭据库；聊天和 configuration 仅保存 opaque ref。</span>
          <input value={credentialEntry.username} onChange={(event) => setCredentialEntry({ ...credentialEntry, username: event.currentTarget.value })} placeholder="账号" autoComplete="username" />
          <input type="password" value={credentialEntry.password} onChange={(event) => setCredentialEntry({ ...credentialEntry, password: event.currentTarget.value })} placeholder="密码" autoComplete="current-password" />
          <div><button type="button" className="assistant-dismiss" onClick={() => setCredentialEntry(undefined)}>取消</button><button type="submit" className="assistant-confirm" disabled={loading || !credentialEntry.username.trim() || !credentialEntry.password}>保存引用</button></div>
        </form>
      ) : null}
      {localProjectEntry !== undefined && bridge.mode === "local" ? (
        <form className="assistant-secure-entry" onSubmit={saveDevLocalProject}>
          <strong>登记本地项目目录（仅本地测试）</strong>
          <span>路径只保存在 127.0.0.1 Dev Bridge，本次执行包仅包含引用、结构摘要和哈希。</span>
          <input value={localProjectEntry} onChange={(event) => setLocalProjectEntry(event.currentTarget.value)} placeholder="D:\\path\\to\\project" autoComplete="off" />
          <div><button type="button" className="assistant-dismiss" onClick={() => setLocalProjectEntry(undefined)}>取消</button><button type="submit" className="assistant-confirm" disabled={loading || !localProjectEntry.trim()}>登记目录</button></div>
        </form>
      ) : null}
      {githubEntry !== undefined ? (
        <form className="assistant-secure-entry" onSubmit={saveGitHubSource}>
          <strong>连接 GitHub 仓库</strong>
          <span>公开仓库只保存 HTTPS URL；私有仓库凭据请通过本地 GitHub 授权单独配置。</span>
          <input value={githubEntry} onChange={(event) => setGithubEntry(event.currentTarget.value)} placeholder="https://github.com/owner/repository" autoComplete="off" />
          <div><button type="button" className="assistant-dismiss" onClick={() => setGithubEntry(undefined)}>取消</button><button type="submit" className="assistant-confirm" disabled={loading || !githubEntry.trim()}>连接</button></div>
        </form>
      ) : null}
    </section>
  );
}

function AssistantMessage({ message, onProposal }: { message: AssistantMessageView; onProposal: (proposal: AssistantProposalView, action: "confirm" | "dismiss") => void }) {
  return <article className={`assistant-message assistant-message-${message.role}`}>
    <div className="assistant-message-meta"><span>{message.role === "agent" ? "Cascade" : message.role === "user" ? "You" : "System"}</span><span>{message.kind}</span></div>
    <p>{message.text}</p>
    {message.targetWorkstation ? <div className="assistant-workstation-target">Workstation · {workstationLabel(message.targetWorkstation)}</div> : null}
    {message.evidence?.length ? <div className="assistant-evidence-list">{message.evidence.map((item) => <div className="assistant-evidence" key={item.id}><strong>{item.label}</strong><small>{item.source} · {item.confidence ? `${Math.round(item.confidence * 100)}% confidence` : "Evidence"}</small><span>{item.summary}</span></div>)}</div> : null}
    {message.proposals?.length ? <div className="assistant-proposal-list">{message.proposals.map((proposal) => <div className="assistant-proposal" key={proposal.id}><strong>{proposal.title}</strong><span>{proposal.description}</span><div>{proposal.status === "available" ? <><button type="button" className="assistant-confirm" onClick={() => onProposal(proposal, "confirm")}>Confirm</button><button type="button" className="assistant-dismiss" onClick={() => onProposal(proposal, "dismiss")}>Not now</button></> : <small>{proposal.status === "confirmed" ? "Confirmed" : "Dismissed"}</small>}</div></div>)}</div> : null}
  </article>;
}

function workstationLabel(view: ProjectWorkstationView): string {
  const labels: Record<ProjectWorkstationView, string> = {
    overview: "Overview",
    evidence: "Evidence",
    plan: "Plan",
    approval: "Approval",
    execution: "Execution",
    repair: "Repair",
    assets: "Assets",
    editor: "Editor",
  };
  return labels[view];
}
