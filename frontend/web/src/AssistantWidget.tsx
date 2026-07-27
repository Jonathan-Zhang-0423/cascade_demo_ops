import { useEffect, useRef, useState, type FormEvent } from "react";
import type {
  AssistantContextView,
  AssistantMessageView,
  AssistantProposalView,
  AssistantSessionView,
  ProjectWorkstationView,
} from "./domain";
import type { DesktopBridgeClient } from "./bridge";

type AssistantWidgetProps = {
  bridge: DesktopBridgeClient;
  context: AssistantContextView;
  onOpenProject?: (projectID: string) => void;
  onOpenRepositoryForm?: () => void;
  onWorkstationChange?: (view: ProjectWorkstationView) => void;
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

export function AssistantConversationPanel({ bridge, context, onOpenProject, onOpenRepositoryForm, onWorkstationChange, embedded = false, showHeader = true, onClose }: AssistantConversationPanelProps & { onClose?: () => void }) {
  const [session, setSession] = useState<AssistantSessionView>();
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const messageListRef = useRef<HTMLDivElement>(null);
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
      if (action === "confirm" && proposal.targetID && (proposal.kind === "open_project" || proposal.kind === "inspect_project")) onOpenProject?.(proposal.targetID);
      if (action === "confirm" && proposal.kind === "open_repository_form") onOpenRepositoryForm?.();
      if (action === "confirm" && proposal.targetWorkstation) onWorkstationChange?.(proposal.targetWorkstation);
    } else {
      setError(result.error ?? "That action could not be updated.");
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
