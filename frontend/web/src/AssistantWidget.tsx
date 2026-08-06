import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
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
  onInspectWorkstation?: (view: ProjectWorkstationView) => void;
  initialMessage?: string;
  suggestedMessage?: { id: string; text: string };
  onSessionChange?: (session: AssistantSessionView) => void;
  onThreadStart?: () => void;
  onProjectCreated?: (projectID: string, initialMessage?: string) => void;
  focusComposer?: boolean;
};

type AssistantConversationPanelProps = AssistantWidgetProps & {
  embedded?: boolean;
  showHeader?: boolean;
  contextualContent?: ReactNode;
  presentation?: "default" | "home";
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

export function AssistantWidget({ bridge, context, onOpenProject, onOpenRepositoryForm, onInspectWorkstation, onProjectCreated }: AssistantWidgetProps) {
  const [open, setOpen] = useState(false);
  const launcherRef = useRef<HTMLButtonElement>(null);

  function close() {
    setOpen(false);
    window.requestAnimationFrame(() => launcherRef.current?.focus());
  }

  return (
    <div className="assistant-widget">
      {!open ? (
        <button ref={launcherRef} type="button" className="assistant-launcher" aria-label="Open Cascade assistant" title="Open Cascade assistant" onClick={() => setOpen(true)}>
          <span className="assistant-logo-mark" aria-hidden="true"><img src="/Logo_simple_white.png" alt="" /></span>
          <span className="assistant-launcher-label">Assistant</span>
        </button>
      ) : (
        <AssistantConversationPanel
          bridge={bridge}
          context={context}
          {...(onOpenProject ? { onOpenProject } : {})}
          {...(onOpenRepositoryForm ? { onOpenRepositoryForm } : {})}
          {...(onInspectWorkstation ? { onInspectWorkstation } : {})}
          {...(onProjectCreated ? { onProjectCreated } : {})}
          onClose={close}
        />
      )}
    </div>
  );
}

export function AssistantConversationPanel({ bridge, context, onOpenProject, onOpenRepositoryForm, onInspectWorkstation, onSessionChange, onThreadStart, onProjectCreated, initialMessage, suggestedMessage, focusComposer = false, embedded = false, showHeader = true, contextualContent, presentation = "default", onClose }: AssistantConversationPanelProps & { onClose?: () => void }) {
  const [session, setSession] = useState<AssistantSessionView>();
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [lastFailedMessage, setLastFailedMessage] = useState("");
  const [pendingMessage, setPendingMessage] = useState("");
  const [credentialEntry, setCredentialEntry] = useState<{ proposal: AssistantProposalView; ref: string; username: string; password: string }>();
  const [githubEntry, setGithubEntry] = useState<{ proposal: AssistantProposalView; url: string }>();
  const [sourceEntry, setSourceEntry] = useState<AssistantProposalView>();
  const [manualEntry, setManualEntry] = useState<ManualConfigurationForm>();
  const [localProjectEntry, setLocalProjectEntry] = useState<{ proposal: AssistantProposalView; path: string }>();
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const messageListRef = useRef<HTMLDivElement>(null);
  const submittedInitialMessageRef = useRef("");
  const appliedSuggestionRef = useRef("");
  const requestEpochRef = useRef(0);
  const projectHandoffRef = useRef(false);
  const active = embedded || Boolean(onClose);

  function applySession(next: AssistantSessionView) {
    setSession(next);
    onSessionChange?.(next);
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
  }, [active, bridge, context.createProjectOnFirstTurn, context.projectID, context.projectName, context.repositoryID, context.repositoryLabel, context.scopeKey, context.surface]);

  useEffect(() => {
    const message = initialMessage?.trim();
    if (!active || !session || !message || loading || submittedInitialMessageRef.current === message) return;
    submittedInitialMessageRef.current = message;
    setPendingMessage(message);
    setLoading(true);
    setError("");
    void bridge.submitAssistantTurn(session.id, message, `initial-${context.scopeKey}`).then((result) => {
      if (result.ok && result.data) {
        applySession(result.data);
        setPendingMessage("");
        setLastFailedMessage("");
      }
      else {
        setError(result.error ?? "Cascade could not start the configuration conversation.");
        setLastFailedMessage(message);
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
    if (!active || !focusComposer) return;
    window.requestAnimationFrame(() => inputRef.current?.focus());
  }, [active, focusComposer]);

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
    const composer = inputRef.current;
    if (!composer) return;
    composer.style.height = "auto";
    composer.style.height = `${Math.min(composer.scrollHeight, 180)}px`;
  }, [input]);

  useEffect(() => {
    if (!active || !onClose) return;
    function onKeyDown(event: globalThis.KeyboardEvent) {
      if (event.key === "Escape") onClose?.();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [active, onClose]);

  async function submitMessage(message: string) {
    if (!message || !session || loading) return;
    setInput("");
    if (context.createProjectOnFirstTurn && context.projectID && onProjectCreated && !projectHandoffRef.current) {
      projectHandoffRef.current = true;
      onThreadStart?.();
      onProjectCreated(context.projectID, message);
      return;
    }
    const requestEpoch = ++requestEpochRef.current;
    setPendingMessage(message);
    if (presentation === "home") {
      onThreadStart?.();
    }
    setLoading(true);
    setError("");
    const result = await bridge.submitAssistantTurn(session.id, message);
    if (requestEpoch !== requestEpochRef.current) return;
    if (result.ok && result.data) {
      applySession(result.data);
      setLastFailedMessage("");
      setPendingMessage("");
      const createdProjectID = result.data.context.createProjectOnFirstTurn ? result.data.context.projectID : undefined;
      if (createdProjectID && onProjectCreated && !projectHandoffRef.current) {
        projectHandoffRef.current = true;
        onProjectCreated(createdProjectID);
      }
    } else {
      setError(result.error ?? "Cascade could not respond.");
      setLastFailedMessage(message);
    }
    setLoading(false);
  }

  async function send(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    await submitMessage(input.trim());
  }

  function handleComposerKeyDown(event: ReactKeyboardEvent<HTMLTextAreaElement>) {
    if (!isComposerSubmitKey(event)) return;
    event.preventDefault();
    void submitMessage(input.trim());
  }

  async function stopResponse() {
    if (!session) return;
    requestEpochRef.current += 1;
    setLoading(false);
    setPendingMessage("");
    const result = await bridge.cancelAssistantSession(session.id);
    if (result.ok && result.data) applySession(result.data);
    else if (!result.ok) setError(result.error ?? "Cascade could not stop the current response.");
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
    const result = action === "confirm"
      ? await bridge.confirmAssistantProposal(session.id, proposal.id, proposal.baseVersion, proposal.idempotencyKey)
      : await bridge.dismissAssistantProposal(session.id, proposal.id, proposal.baseVersion, proposal.idempotencyKey);
    if (result.ok && result.data) {
      applySession(result.data);
      const analysisProjectID = result.data.configuration.analysisProjectID;
      if (action === "confirm" && analysisProjectID && (proposal.kind === "confirm_configuration" || proposal.kind === "start_local_analysis")) onOpenProject?.(analysisProjectID);
      if (action === "confirm" && proposal.targetID && (proposal.kind === "open_project" || proposal.kind === "inspect_project")) onOpenProject?.(proposal.targetID);
      if (action === "confirm" && proposal.kind === "open_repository_form") onOpenRepositoryForm?.();
      if (action === "confirm" && proposal.kind === "open_workstation" && proposal.targetWorkstation) onInspectWorkstation?.(proposal.targetWorkstation);
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
    const result = await bridge.completeAssistantClientAction(session.id, proposal.id, proposal.baseVersion, { selectedSources, credentialRefs }, `${proposal.idempotencyKey}:client-result`);
    if (result.ok && result.data) {
      applySession(result.data);
      setSourceEntry(undefined);
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
      await selectLocalSource(proposal);
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

  const visibleMessages = visibleAssistantMessages(session?.messages ?? [], presentation);
  const homeHasThread = presentation === "home" && (visibleMessages.length > 0 || Boolean(pendingMessage) || Boolean(error));
  const panelClassName = presentation === "home"
    ? `assistant-home-panel ${homeHasThread ? "has-thread" : "is-empty"}`
    : embedded ? "project-agent-conversation" : "assistant-dialogue";

  return (
    <section className={panelClassName} aria-label={`${context.surface === "projects" ? "Projects" : "Repositories"} Cascade Agent`}>
      {showHeader ? (
        <header className="assistant-dialogue-header">
          <div><span className="assistant-dialogue-kicker">Cascade Agent</span><strong>{context.surface === "projects" ? "Projects" : "Repositories"}</strong></div>
          {onClose ? <button type="button" className="assistant-close" aria-label="Close Cascade Agent" onClick={onClose}>×</button> : null}
        </header>
      ) : null}
      <div ref={messageListRef} className="assistant-message-list" aria-live="polite">
        {visibleMessages.map((message) => (
          <AssistantMessage
            key={message.id}
            message={message}
            loading={loading}
            onConfirm={(proposal) => void updateProposal(proposal, "confirm")}
            onDismiss={(proposal) => void updateProposal(proposal, "dismiss")}
            onSuggestion={(suggestion) => { setInput(suggestion); window.requestAnimationFrame(() => inputRef.current?.focus()); }}
          />
        ))}
        {pendingMessage ? <article className="assistant-message assistant-message-user assistant-pending-user-message"><p>{pendingMessage}</p></article> : null}
        {contextualContent}
        {loading ? <div className="assistant-typing"><span /><span /><span /><span className="assistant-typing-label">Cascade is working</span></div> : null}
        {error ? <div className="assistant-error" role="alert"><span>{error}</span><div className="assistant-error-actions">{lastFailedMessage ? <button type="button" className="assistant-retry" onClick={() => void submitMessage(lastFailedMessage)}>Retry</button> : <button type="button" className="assistant-retry" onClick={() => { setError(""); inputRef.current?.focus(); }}>Try again</button>}{context.surface === "projects" && session ? <button type="button" className="assistant-manual-recovery" onClick={openManualConfiguration}>Advanced recovery</button> : null}</div></div> : null}
      </div>
      <form className="assistant-composer" onSubmit={send}>
        <textarea ref={inputRef} value={input} onChange={(event) => setInput(event.currentTarget.value)} onKeyDown={handleComposerKeyDown} rows={1} placeholder={presentation === "home" ? "What would you like to show today?" : "Message Cascade…"} aria-label="Message Cascade" />
        <div className="assistant-composer-footer">{loading ? <button type="button" className="assistant-stop" aria-label="Stop Cascade" onClick={() => void stopResponse()}><span aria-hidden="true">■</span></button> : <button type="submit" aria-label="Send message" disabled={!input.trim()}><span aria-hidden="true">↑</span></button>}</div>
      </form>
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

export function visibleAssistantMessages(messages: AssistantMessageView[], presentation: "default" | "home"): AssistantMessageView[] {
  if (presentation !== "home") return messages.filter((message) => message.id !== "welcome");
  const firstUserMessage = messages.findIndex((message) => message.role === "user");
  return firstUserMessage === -1 ? [] : messages.slice(firstUserMessage);
}

export function isComposerSubmitKey(event: Pick<ReactKeyboardEvent<HTMLTextAreaElement>, "key" | "shiftKey" | "nativeEvent">): boolean {
  return event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing;
}

function AssistantMessage({ message, loading, onConfirm, onDismiss, onSuggestion }: {
  message: AssistantMessageView;
  loading: boolean;
  onConfirm: (proposal: AssistantProposalView) => void;
  onDismiss: (proposal: AssistantProposalView) => void;
  onSuggestion: (suggestion: string) => void;
}) {
  const available = message.proposals?.filter((proposal) => proposal.status === "available") ?? [];
  const resolved = message.proposals?.filter((proposal) => proposal.status !== "available") ?? [];
  return <article className={`assistant-message assistant-message-${message.role}`}>
    {message.role !== "user" ? <div className="assistant-message-author">Cascade</div> : null}
    <p>{message.text}</p>
    {message.question?.suggestions?.length ? <div className="assistant-suggestions">{message.question.suggestions.map((suggestion) => <button key={suggestion} type="button" onClick={() => onSuggestion(suggestion)}>{suggestion}</button>)}</div> : null}
    {message.evidence?.length ? <details className="assistant-evidence-disclosure"><summary>View sources <span>{message.evidence.length}</span></summary><div className="assistant-evidence-list">{message.evidence.map((item) => <div className="assistant-evidence" key={item.id}><strong>{item.label}</strong><small>{item.source} · {item.confidence ? `${Math.round(item.confidence * 100)}% confidence` : "Evidence"}</small><span>{item.summary}</span></div>)}</div></details> : null}
    {available.map((proposal) => <InlineConfirmation key={proposal.id} proposal={proposal} loading={loading} onConfirm={() => onConfirm(proposal)} onDismiss={() => onDismiss(proposal)} />)}
    {resolved.length ? <div className="assistant-action-history">{resolved.map((proposal) => <span key={proposal.id}>{proposal.status === "confirmed" ? "✓ Approved by you" : "Declined by you"} · {proposal.title}</span>)}</div> : null}
    {message.generationSource === "deterministic_fallback" ? <details className="assistant-response-details"><summary>Response details</summary><small>Cascade used its local fallback because the configured model was unavailable. Any change still requires your confirmation.</small></details> : null}
  </article>;
}

function InlineConfirmation({ proposal, loading, onConfirm, onDismiss }: { proposal: AssistantProposalView; loading: boolean; onConfirm: () => void; onDismiss: () => void }) {
  return <section className="assistant-inline-confirmation" aria-label={proposal.title}>
    <strong>{proposal.title}</strong>
    <p>{proposal.description}</p>
    {proposal?.patch ? <ConfigurationPatchPreview patch={proposal.patch} /> : null}
    <div>
      <button type="button" className="assistant-confirm" disabled={loading} onClick={onConfirm}>{proposalConfirmLabel(proposal.kind)}</button>
      <button type="button" className="assistant-dismiss" disabled={loading} onClick={onDismiss}>Not now</button>
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

function proposalConfirmLabel(kind: AssistantProposalView["kind"]): string {
  if (kind === "configuration_patch") return "Use this";
  if (kind === "confirm_configuration" || kind === "start_local_analysis") return "Continue";
  if (kind === "select_project_source") return "Choose source";
  if (kind === "select_local_project") return "Choose folder";
  if (kind === "connect_github") return "Connect GitHub";
  if (kind === "attach_requirement_document") return "Choose document";
  if (kind === "attach_brand_asset") return "Choose assets";
  if (kind === "store_demo_credential") return "Save securely";
  return "Confirm";
}

function desktopPickerError(error: string | undefined, label: string): string {
  if (error?.includes("Wails")) return `请在 DemoOps 桌面应用中选择${label}；浏览器测试模式不会接收本地绝对路径。`;
  return error ?? `未选择${label}。`;
}
