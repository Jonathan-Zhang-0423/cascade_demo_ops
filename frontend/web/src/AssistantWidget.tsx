import { useEffect, useRef, useState, type FormEvent } from "react";
import type {
  AssistantContextView,
  AssistantMessageView,
  AssistantProposalView,
  AssistantSessionView,
  ConfigurationSourceRefView,
  ProjectConfigurationDraftView,
  ProjectConfigurationPatchView,
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
  suggestedMessage?: { id: string; text: string };
  onSessionChange?: (session: AssistantSessionView) => void;
};

type AssistantConversationPanelProps = AssistantWidgetProps & {
  embedded?: boolean;
  showHeader?: boolean;
};

type ManualConfigurationForm = {
  projectName: string;
  productURL: string;
  objective: string;
  targetAudience: string;
  targetDurationSec: string;
  mustShow: string;
  mustNotShow: string;
  forbiddenPages: string;
  brandTone: string;
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

export function AssistantConversationPanel({ bridge, context, onOpenProject, onOpenRepositoryForm, onWorkstationChange, onSessionChange, initialMessage, suggestedMessage, embedded = false, showHeader = true, onClose }: AssistantConversationPanelProps & { onClose?: () => void }) {
  const [session, setSession] = useState<AssistantSessionView>();
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [credentialEntry, setCredentialEntry] = useState<{ proposal: AssistantProposalView; ref: string; username: string; password: string }>();
  const [githubEntry, setGithubEntry] = useState<{ proposal: AssistantProposalView; url: string }>();
  const [sourceEntry, setSourceEntry] = useState<AssistantProposalView>();
  const [manualEntry, setManualEntry] = useState<ManualConfigurationForm>();
  const [localProjectEntry, setLocalProjectEntry] = useState<{ proposal: AssistantProposalView; path: string }>();
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const messageListRef = useRef<HTMLDivElement>(null);
  const submittedInitialMessageRef = useRef("");
  const appliedSuggestionRef = useRef("");
  const active = embedded || Boolean(onClose);

  function applySession(next: AssistantSessionView) {
    setSession(next);
    onSessionChange?.(next);
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
    if (!active || !suggestedMessage || appliedSuggestionRef.current === suggestedMessage.id) return;
    appliedSuggestionRef.current = suggestedMessage.id;
    setInput(suggestedMessage.text);
    window.requestAnimationFrame(() => inputRef.current?.focus());
  }, [active, suggestedMessage]);

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
    if (action === "confirm" && isClientActionProposal(proposal.kind)) {
      await executeClientProposal(proposal);
      return;
    }
    await decideProposal(proposal, action);
  }

  async function decideProposal(proposal: AssistantProposalView, action: "confirm" | "dismiss") {
    if (!session) return false;
    setLoading(true);
    setError("");
    const agentAction = session.actions?.find((candidate) => candidate.proposalID === proposal.id);
    const actionBatch = agentAction?.batchID ? session.actionBatches?.find((candidate) => candidate.id === agentAction.batchID) : undefined;
    const result = action === "confirm" && actionBatch && session.intentPlan
      ? await bridge.confirmAssistantActionBatch(session.id, actionBatch.id, session.intentPlan.digest, `${actionBatch.id}:confirm`, actionBatch.approvalSubjectDigest)
      : action === "confirm"
        ? await bridge.confirmAssistantProposal(session.id, proposal.id, proposal.baseVersion, proposal.idempotencyKey)
      : await bridge.dismissAssistantProposal(session.id, proposal.id, proposal.baseVersion, proposal.idempotencyKey);
    if (result.ok && result.data) {
      applySession(result.data);
      const analysisProjectID = result.data.configuration.analysisProjectID;
      if (action === "confirm" && analysisProjectID && (proposal.kind === "confirm_configuration" || proposal.kind === "start_local_analysis")) onOpenProject?.(analysisProjectID);
      if (action === "confirm" && proposal.targetID && (proposal.kind === "open_project" || proposal.kind === "inspect_project")) onOpenProject?.(proposal.targetID);
      if (action === "confirm" && proposal.kind === "open_repository_form") onOpenRepositoryForm?.();
      if (action === "confirm" && result.data.activeWorkstation) onWorkstationChange?.(result.data.activeWorkstation);
      setLoading(false);
      return true;
    } else {
      setError(result.error ?? "That action could not be updated.");
      setLoading(false);
      return false;
    }
  }

  async function completeClientAction(proposal: AssistantProposalView, selectedSources: ConfigurationSourceRefView[] = [], credentialRefs: string[] = []) {
    if (!session) return false;
    setLoading(true);
    setError("");
    const agentAction = session.actions?.find((candidate) => candidate.proposalID === proposal.id);
    const result = agentAction
      ? await bridge.completeAssistantAction(session.id, agentAction.id, agentAction.dependencyDigest, { selectedSources, credentialRefs }, `${agentAction.idempotencyKey}:client-result`)
      : await bridge.completeAssistantClientAction(session.id, proposal.id, proposal.baseVersion, { selectedSources, credentialRefs }, `${proposal.idempotencyKey}:client-result`);
    if (result.ok && result.data) {
      applySession(result.data);
      setSourceEntry(undefined);
      if (result.data.configuration.analysisProjectID) onOpenProject?.(result.data.configuration.analysisProjectID);
    } else setError(result.error ?? "安全选择无法写入 configuration。");
    setLoading(false);
    return Boolean(result.ok && result.data);
  }

  function openManualConfiguration() {
    if (!session) return;
    const configuration = session.configuration;
    setManualEntry({
      projectName: configuration.projectName ?? "",
      productURL: configuration.productURL ?? "",
      objective: configuration.objective ?? "",
      targetAudience: configuration.targetAudience ?? "",
      targetDurationSec: String(configuration.targetDurationSec || 60),
      mustShow: configuration.mustShow?.join("\n") ?? "",
      mustNotShow: configuration.mustNotShow?.join("\n") ?? "",
      forbiddenPages: configuration.forbiddenPages?.join("\n") ?? "",
      brandTone: configuration.brandTone ?? "",
    });
  }

  async function proposeManualConfiguration(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!session || !manualEntry || loading) return;
    const duration = Number.parseInt(manualEntry.targetDurationSec, 10);
    if (!Number.isFinite(duration) || duration < 5 || duration > 600) {
      setError("目标时长需要在 5–600 秒之间。");
      return;
    }
    const list = (value: string) => value.split(/\r?\n|，|,/).map((item) => item.trim()).filter(Boolean);
    const patch: ProjectConfigurationPatchView = {
      projectName: manualEntry.projectName.trim(),
      productURL: manualEntry.productURL.trim(),
      objective: manualEntry.objective.trim(),
      targetAudience: manualEntry.targetAudience.trim(),
      targetDurationSec: duration,
      mustShow: list(manualEntry.mustShow),
      mustNotShow: list(manualEntry.mustNotShow),
      forbiddenPages: list(manualEntry.forbiddenPages),
      brandTone: manualEntry.brandTone.trim(),
    };
    setLoading(true);
    setError("");
    const result = await bridge.proposeAssistantConfigurationPatch(session.id, patch, session.configuration.version, `manual-${session.configuration.version}-${Date.now()}`);
    if (result.ok && result.data) {
      applySession(result.data);
      setManualEntry(undefined);
    } else setError(result.error ?? "手动 configuration 提案无法保存。");
    setLoading(false);
  }

  async function requestSafeConfigurationAction(message: string) {
    if (!session || loading) return;
    setLoading(true);
    setError("");
    const result = await bridge.submitAssistantTurn(session.id, message, `manual-action-${session.configuration.version}-${message}`);
    if (result.ok && result.data) applySession(result.data);
    else setError(result.error ?? "无法创建安全配置动作。");
    setLoading(false);
  }

  async function selectLocalSource(proposal: AssistantProposalView) {
    const selected = await bridge.selectLocalProjectDirectory();
    if (selected.ok && selected.data) await completeClientAction(proposal, [selected.data]);
    else if (bridge.mode === "local") {
      setLocalProjectEntry({ proposal, path: "" });
      setSourceEntry(undefined);
      setError("");
    } else setError(desktopPickerError(selected.error, "本地项目目录"));
  }

  async function executeClientProposal(proposal: AssistantProposalView) {
    if (proposal.kind === "select_project_source") {
      setSourceEntry(proposal);
      return;
    }
    if (proposal.kind === "select_local_project") {
      const detected = detectedLocalSourceForProposal(session, proposal);
      if (detected) await completeClientAction(proposal, [detected]);
      else await selectLocalSource(proposal);
      return;
    }
    if (proposal.kind === "attach_requirement_document") {
      const selected = await bridge.selectRequirementDocuments();
      if (selected.ok && selected.data) {
        await completeClientAction(proposal, selected.data);
      } else setError(desktopPickerError(selected.error, "需求文档"));
      return;
    }
    if (proposal.kind === "attach_brand_asset") {
      const selected = await bridge.selectBrandAssets();
      if (selected.ok && selected.data) {
        await completeClientAction(proposal, selected.data);
      } else setError(desktopPickerError(selected.error, "品牌素材"));
      return;
    }
    if (proposal.kind === "store_demo_credential") {
      setCredentialEntry({ proposal, ref: `assistant-${proposal.id}`, username: "", password: "" });
      return;
    }
    if (proposal.kind === "connect_github") {
      setGithubEntry({ proposal, url: "" });
    }
  }

  async function saveCredential(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!credentialEntry || !credentialEntry.username.trim() || !credentialEntry.password || !session) return;
    setLoading(true);
    const stored = await bridge.storeDemoCredential(credentialEntry.ref, credentialEntry.username, credentialEntry.password);
    if (stored.ok && stored.data?.configured) {
      const proposal = credentialEntry.proposal;
      setCredentialEntry(undefined);
      await completeClientAction(proposal, [], [stored.data.secretRef]);
    } else {
      setError(stored.error ?? "演示凭据无法保存到本地凭据库。");
      setLoading(false);
    }
  }

  async function saveGitHubSource(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const url = githubEntry?.url.trim() ?? "";
    if (!/^https:\/\/github\.com\/[^/]+\/[^/]+\/?$/i.test(url)) {
      setError("请输入 https://github.com/owner/repository 格式的仓库地址。");
      return;
    }
    const proposal = githubEntry?.proposal;
    if (!proposal) return;
    setGithubEntry(undefined);
    await completeClientAction(proposal, [{ ref: "", kind: "github_repository", label: url.split("/").filter(Boolean).at(-1) ?? "GitHub repository", url }]);
  }

  async function saveDevLocalProject(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const path = localProjectEntry?.path.trim() ?? "";
    if (!path || !session) return;
    setLoading(true);
    setError("");
    const registered = await bridge.registerDevLocalProjectDirectory(path);
    if (registered.ok && registered.data && localProjectEntry) {
      const proposal = localProjectEntry.proposal;
      setLocalProjectEntry(undefined);
      await completeClientAction(proposal, [registered.data]);
    } else {
      setError(registered.error ?? "本地项目目录登记失败。");
      setLoading(false);
    }
  }

  const activeProposal = session?.messages.flatMap((message) => message.proposals ?? []).find((proposal) => proposal.status === "available" && proposal.id === session.nextAction.proposalID);

  return (
    <section className={embedded ? "project-agent-conversation" : "assistant-dialogue"} aria-label={`${context.surface === "projects" ? "Projects" : "Repositories"} Cascade Agent`}>
      {showHeader ? (
        <header className="assistant-dialogue-header">
          <div><span className="assistant-dialogue-kicker">Cascade Agent</span><strong>{context.surface === "projects" ? "Projects" : "Repositories"}</strong><small>{context.projectName ?? context.repositoryLabel ?? "Current workspace context"}</small></div>
          {onClose ? <button type="button" className="assistant-close" aria-label="Close Cascade Agent" onClick={onClose}>×</button> : null}
        </header>
      ) : null}
      <div ref={messageListRef} className="assistant-message-list" aria-live="polite">
        {session?.messages.map((message) => <AssistantMessage key={message.id} message={message} />)}
        {loading ? <div className="assistant-typing"><span /><span /><span />Cascade 正在整理</div> : null}
        {error ? <div className="assistant-error" role="alert">{error}<button type="button" className="assistant-retry" onClick={() => { setError(""); inputRef.current?.focus(); }}>重试</button></div> : null}
      </div>
      {session?.nextAction.requiresUserAction ? <AssistantNextAction action={session.nextAction} {...(activeProposal ? { proposal: activeProposal } : {})} loading={loading} onConfirm={() => { if (activeProposal) void updateProposal(activeProposal, "confirm"); }} onDismiss={() => { if (activeProposal) void updateProposal(activeProposal, "dismiss"); }} onFocusComposer={() => { inputRef.current?.focus(); }} /> : null}
      <form className="assistant-composer" onSubmit={send}>
        <textarea ref={inputRef} value={input} onChange={(event) => setInput(event.currentTarget.value)} rows={2} placeholder={context.surface === "projects" ? "告诉 Cascade 需要补充或调整什么…" : "询问已连接的代码仓库…"} aria-label="发送消息给 Cascade Agent" />
        <div className="assistant-composer-footer"><span>字段变更和敏感动作都需要你确认。</span><button type="submit" disabled={loading || !input.trim()}>发送 <span aria-hidden="true">↗</span></button></div>
      </form>
      <div className="assistant-manual-toolbar">
        <span>对话不方便？</span>
        <button type="button" disabled={loading || !session || session.nextAction.proposalID !== undefined} onClick={openManualConfiguration}>手动填写配置</button>
        <button type="button" disabled={loading || !session || session.nextAction.proposalID !== undefined} onClick={() => void requestSafeConfigurationAction("选择本地项目目录")}>选择本地项目</button>
        <button type="button" disabled={loading || !session || session.nextAction.proposalID !== undefined} onClick={() => void requestSafeConfigurationAction("连接 GitHub 仓库")}>连接 GitHub</button>
      </div>
      {manualEntry ? (
        <form className="assistant-manual-configuration" onSubmit={proposeManualConfiguration}>
          <header><div><span>Manual fallback</span><strong>手动填写 Configuration</strong></div><button type="button" className="assistant-dismiss" onClick={() => setManualEntry(undefined)}>关闭</button></header>
          <div className="assistant-manual-grid">
            <label><span>项目名称</span><input required value={manualEntry.projectName} onChange={(event) => setManualEntry({ ...manualEntry, projectName: event.currentTarget.value })} /></label>
            <label><span>产品地址</span><input required type="url" placeholder="https://product.example" value={manualEntry.productURL} onChange={(event) => setManualEntry({ ...manualEntry, productURL: event.currentTarget.value })} /></label>
            <label><span>目标受众</span><input required value={manualEntry.targetAudience} onChange={(event) => setManualEntry({ ...manualEntry, targetAudience: event.currentTarget.value })} /></label>
            <label><span>目标时长（秒）</span><input required type="number" min={5} max={600} value={manualEntry.targetDurationSec} onChange={(event) => setManualEntry({ ...manualEntry, targetDurationSec: event.currentTarget.value })} /></label>
            <label className="wide"><span>演示目标</span><textarea required rows={3} value={manualEntry.objective} onChange={(event) => setManualEntry({ ...manualEntry, objective: event.currentTarget.value })} /></label>
            <label><span>必须展示（每行一项）</span><textarea rows={3} value={manualEntry.mustShow} onChange={(event) => setManualEntry({ ...manualEntry, mustShow: event.currentTarget.value })} /></label>
            <label><span>禁止展示（每行一项）</span><textarea rows={3} value={manualEntry.mustNotShow} onChange={(event) => setManualEntry({ ...manualEntry, mustNotShow: event.currentTarget.value })} /></label>
            <label><span>禁止页面（每行一项）</span><textarea rows={2} value={manualEntry.forbiddenPages} onChange={(event) => setManualEntry({ ...manualEntry, forbiddenPages: event.currentTarget.value })} /></label>
            <label><span>品牌语气</span><input value={manualEntry.brandTone} onChange={(event) => setManualEntry({ ...manualEntry, brandTone: event.currentTarget.value })} /></label>
          </div>
          <footer><span>提交后只生成待确认 patch，不会直接写入、分析或上传。</span><button type="submit" className="assistant-confirm" disabled={loading}>生成变更提案</button></footer>
        </form>
      ) : null}
      {sourceEntry ? (
        <section className="assistant-source-entry" aria-label="选择项目来源">
          <strong>项目代码在哪里？</strong>
          <span>只需选择一种。源码留在本机，configuration 仅保存安全引用。</span>
          <button type="button" className="assistant-source-option" disabled={loading} onClick={() => void selectLocalSource(sourceEntry)}><b>本地项目目录</b><small>使用 Windows 原生目录选择器</small></button>
          <button type="button" className="assistant-source-option" disabled={loading} onClick={() => { setSourceEntry(undefined); setGithubEntry({ proposal: sourceEntry, url: "" }); }}><b>GitHub 仓库</b><small>公开仓库使用 HTTPS URL</small></button>
          <button type="button" className="assistant-dismiss" onClick={() => setSourceEntry(undefined)}>取消</button>
        </section>
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
          <input value={localProjectEntry.path} onChange={(event) => setLocalProjectEntry({ ...localProjectEntry, path: event.currentTarget.value })} placeholder="D:\\path\\to\\project" autoComplete="off" />
          <div><button type="button" className="assistant-dismiss" onClick={() => setLocalProjectEntry(undefined)}>取消</button><button type="submit" className="assistant-confirm" disabled={loading || !localProjectEntry.path.trim()}>登记目录</button></div>
        </form>
      ) : null}
      {githubEntry !== undefined ? (
        <form className="assistant-secure-entry" onSubmit={saveGitHubSource}>
          <strong>连接 GitHub 仓库</strong>
          <span>公开仓库只保存 HTTPS URL；私有仓库凭据请通过本地 GitHub 授权单独配置。</span>
          <input value={githubEntry.url} onChange={(event) => setGithubEntry({ ...githubEntry, url: event.currentTarget.value })} placeholder="https://github.com/owner/repository" autoComplete="off" />
          <div><button type="button" className="assistant-dismiss" onClick={() => setGithubEntry(undefined)}>取消</button><button type="submit" className="assistant-confirm" disabled={loading || !githubEntry.url.trim()}>连接</button></div>
        </form>
      ) : null}
    </section>
  );
}

function AssistantMessage({ message }: { message: AssistantMessageView }) {
  return <article className={`assistant-message assistant-message-${message.role}`}>
    <div className="assistant-message-meta"><span>{message.role === "agent" ? "Cascade" : message.role === "user" ? "你" : "系统"}</span><span>{assistantGenerationLabel(message)}</span></div>
    <p>{message.text}</p>
    {message.generationSource === "deterministic_fallback" ? <small className="assistant-generation-note">真实模型本轮不可用，已使用本地规则整理；字段仍需你确认。</small> : null}
    {message.targetWorkstation ? <div className="assistant-workstation-target">工作台 · {workstationLabel(message.targetWorkstation)}</div> : null}
    {message.evidence?.length ? <div className="assistant-evidence-list">{message.evidence.map((item) => <div className="assistant-evidence" key={item.id}><strong>{item.label}</strong><small>{item.source} · {item.confidence ? `${Math.round(item.confidence * 100)}% confidence` : "Evidence"}</small><span>{item.summary}</span></div>)}</div> : null}
    {message.proposals?.some((proposal) => proposal.status !== "available") ? <div className="assistant-action-history">{message.proposals.filter((proposal) => proposal.status !== "available").map((proposal) => <span key={proposal.id}>{proposal.status === "confirmed" ? "✓" : "—"} {proposal.title}</span>)}</div> : null}
  </article>;
}

function assistantGenerationLabel(message: AssistantMessageView): string {
  if (message.generationSource === "llm") return message.modelName ? `LLM · ${message.modelName}` : "LLM 理解";
  if (message.generationSource === "deterministic_fallback") return "规则兜底";
  if (message.generationSource === "manual") return "手动提案";
  return assistantMessageKindLabel(message.kind);
}

function AssistantNextAction({ action, proposal, loading, onConfirm, onDismiss, onFocusComposer }: { action: AssistantSessionView["nextAction"]; proposal?: AssistantProposalView; loading: boolean; onConfirm: () => void; onDismiss: () => void; onFocusComposer: () => void }) {
  const needsProposal = Boolean(action.proposalID);
  return <section className="assistant-next-action" aria-label="当前下一步">
    <span>当前只需完成这一步</span>
    <strong>{action.title}</strong>
    <p>{action.description}</p>
    {proposal?.patch ? <ConfigurationPatchPreview patch={proposal.patch} /> : null}
    {action.missingFields?.length ? <small>待补充：{action.missingFields.map(configurationFieldLabel).join("、")}</small> : null}
    <div>
      <button type="button" className="assistant-confirm" disabled={loading || (needsProposal && !proposal)} onClick={needsProposal ? onConfirm : onFocusComposer}>{action.primaryLabel || "继续"}</button>
      {proposal ? <button type="button" className="assistant-dismiss" disabled={loading} onClick={onDismiss}>稍后</button> : null}
    </div>
  </section>;
}

function ConfigurationPatchPreview({ patch }: { patch: AssistantProposalView["patch"] }) {
  if (!patch) return null;
  const rows = configurationPatchRows(patch);
  return rows.length ? <dl className="assistant-patch-preview">{rows.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl> : null;
}

function configurationPatchRows(patch: NonNullable<AssistantProposalView["patch"]>): Array<[string, string]> {
  const rows: Array<[string, string]> = [];
  const push = (key: keyof ProjectConfigurationDraftView, value: unknown) => {
    if (value === undefined) return;
    const labels: Partial<Record<keyof ProjectConfigurationDraftView, string>> = { projectName: "项目名称", productURL: "产品地址", objective: "演示目标", targetAudience: "目标受众", targetDurationSec: "目标时长", mustShow: "必须展示", mustNotShow: "禁止展示", forbiddenPages: "禁止页面", forbiddenData: "禁止数据", brandTone: "品牌语气", allowedDomains: "允许域名", sources: "项目来源", credentialRefs: "演示账号" };
    const rendered = key === "sources" ? `${Array.isArray(value) ? value.length : 0} 个安全引用` : key === "credentialRefs" ? `${Array.isArray(value) ? value.length : 0} 个凭据引用` : key === "targetDurationSec" ? `${String(value)} 秒` : Array.isArray(value) ? value.join("、") || "清空" : String(value || "清空");
    rows.push([labels[key] ?? String(key), rendered]);
  };
  (Object.keys(patch) as Array<keyof typeof patch>).forEach((key) => push(key as keyof ProjectConfigurationDraftView, patch[key]));
  return rows;
}

function isClientActionProposal(kind: AssistantProposalView["kind"]): boolean {
  return ["select_project_source", "select_local_project", "connect_github", "attach_requirement_document", "attach_brand_asset", "store_demo_credential"].includes(kind);
}

function detectedLocalSourceForProposal(session: AssistantSessionView | undefined, proposal: AssistantProposalView): ConfigurationSourceRefView | undefined {
  const action = session?.actions?.find((candidate) => candidate.proposalID === proposal.id);
  const detected = action?.executionResult?.detectedSources;
  if (!Array.isArray(detected) || detected.length === 0) return undefined;
  const source = detected[0];
  if (!source || typeof source !== "object") return undefined;
  const value = source as Record<string, unknown>;
  if (typeof value.ref !== "string" || !value.ref.startsWith("source_") || value.kind !== "local_repository" || typeof value.label !== "string") return undefined;
  return { ref: value.ref, kind: "local_repository", label: value.label };
}

function proposalConfirmLabel(kind: AssistantProposalView["kind"]): string {
  if (kind === "configuration_patch") return "应用字段变更";
  if (kind === "confirm_configuration" || kind === "start_local_analysis") return "确认配置并启动本地理解";
  if (kind === "select_local_project") return "选择目录";
  if (kind === "connect_github") return "连接 GitHub";
  if (kind === "attach_requirement_document") return "选择需求文档";
  if (kind === "attach_brand_asset") return "选择品牌素材";
  if (kind === "store_demo_credential") return "安全保存账号";
  if (kind === "retry_page_scan") return "重新扫描";
  if (kind === "regenerate_execution_package") return "重新生成";
  return "确认";
}

function configurationFieldLabel(field: string): string {
  const labels: Record<string, string> = { projectName: "项目名称", productURL: "产品地址", objective: "演示目标", targetAudience: "目标受众", sources: "项目来源" };
  return labels[field] ?? field;
}

function desktopPickerError(error: string | undefined, label: string): string {
  if (error?.includes("Wails")) return `请在 DemoOps 桌面应用中选择${label}；浏览器测试模式不会接收本地绝对路径。`;
  return error ?? `未选择${label}。`;
}

function workstationLabel(view: ProjectWorkstationView): string {
  const labels: Record<ProjectWorkstationView, string> = {
    overview: "项目配置",
    evidence: "证据与理解",
    plan: "演示方案",
    approval: "执行审批",
    execution: "执行进度",
    repair: "修复",
    assets: "成品审核",
    editor: "视频编辑",
  };
  return labels[view];
}

function assistantMessageKindLabel(kind: AssistantMessageView["kind"]): string {
  const labels: Record<AssistantMessageView["kind"], string> = { answer: "回复", evidence: "证据", proposal: "待确认", status: "状态", error: "需要处理" };
  return labels[kind];
}
