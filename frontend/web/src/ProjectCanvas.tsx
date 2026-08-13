import type { DesktopBridgeClient } from "./bridge";
import type { ProjectActivityEventView, ProjectActivityStateView, ProjectCanvasMode, ProjectWorkspaceView } from "./domain";
import { VideoEditor } from "./VideoEditor";
import { isEditorTakeover, resolveProjectCanvasMode } from "./projectCanvasState";

type ProjectCanvasProps = {
  bridge: DesktopBridgeClient;
  workspace: ProjectWorkspaceView;
  activity?: ProjectActivityStateView;
  events?: ProjectActivityEventView[];
  forcedMode?: ProjectCanvasMode;
};

const statusLabels: Record<ProjectActivityEventView["status"], string> = {
  queued: "Queued",
  running: "In progress",
  waiting_for_approval: "Waiting for your approval",
  completed: "Completed",
  failed: "Needs attention",
  canceled: "Canceled",
};

export function ProjectCanvas({ bridge, workspace, activity, events = [], forcedMode }: ProjectCanvasProps) {
  const mode = forcedMode ?? resolveProjectCanvasMode(workspace, activity);
  if (mode === "empty" && !forcedMode && workspace.inputBundle.raw_user_prompt) {
    return <ActCanvas workspace={workspace} {...(activity ? { activity } : {})} events={events} />;
  }
  if (mode === "empty") return (
    <section className="ai-canvas ai-canvas-empty" aria-label="Idle project canvas" data-canvas-mode="empty">
      <div className="canvas-idle">
        <span className="canvas-idle-mark" aria-hidden="true" />
        <p>Cascade is standing by.</p>
        <small>Ask for a plan, a capture run, or an edit — the work will appear here.</small>
      </div>
    </section>
  );
  if (mode === "act") return <ActCanvas workspace={workspace} {...(activity ? { activity } : {})} events={events} />;
  if (mode === "browser") return <BrowserCanvas bridge={bridge} workspace={workspace} {...(activity ? { activity } : {})} events={events} {...(forcedMode ? { forcedMode } : {})} />;
  return <EditorCanvas workspace={workspace} {...(activity ? { activity } : {})} />;
}

function ActCanvas({ workspace, activity, events }: Pick<ProjectCanvasProps, "workspace" | "activity" | "events">) {
  const current = activity?.current ?? events?.at(-1);
  const progress = typeof current?.progress === "number" ? Math.max(0, Math.min(100, current.progress)) : null;
  return <section className="ai-canvas ai-canvas-act" aria-label="Current project artifact" data-canvas-mode="act">
    <div className="artifact-canvas">
      <header><span>Current artifact</span><strong>{workspace.name}</strong><p>{workspace.inputBundle.raw_user_prompt || "Your demo brief will take shape here as the conversation develops."}</p></header>
      <div className="artifact-canvas-grid">
        <article><span>Story</span><strong>Shaping the product narrative</strong><small>{workspace.targetAudience ? `For ${workspace.targetAudience}` : "Audience will be refined in the conversation"}</small></article>
        <article><span>Evidence</span><strong>{workspace.understanding.evidenceRefs.length || "—"}</strong><small>{workspace.understanding.evidenceRefs.length ? "reviewable source references" : "Connect a source to begin"}</small></article>
        <article><span>Sequence</span><strong>{workspace.planReview.graph.nodes.length || "—"}</strong><small>{workspace.planReview.graph.nodes.length ? "planned product moments" : "A plan will appear here"}</small></article>
        <article><span>Output</span><strong>{workspace.assets.length || "—"}</strong><small>{workspace.assets.length ? "generated assets ready to inspect" : "No generated assets yet"}</small></article>
      </div>
      {current ? <footer className={`artifact-run-state artifact-run-state-${current.status}`} aria-live="polite">
        <span className="artifact-run-dot" aria-hidden="true" />
        <div><small>{statusLabels[current.status]}</small><strong>{current.title}</strong>{current.detail ? <p>{current.detail}</p> : null}</div>
        {progress !== null ? <div className="activity-progress-block">
          <div
            className="activity-progress"
            role="progressbar"
            aria-label={`${current.title} progress`}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={progress}
          >
            <span style={{ width: `${progress}%` }} />
          </div>
          <small>{progress}%</small>
        </div> : null}
      </footer> : null}
    </div>
  </section>;
}

function BrowserCanvas({ bridge, workspace, activity, events, forcedMode }: ProjectCanvasProps) {
  const current = activity?.current ?? events?.at(-1);
  const browserEvent = [...(events ?? []), ...(activity?.recent ?? []), ...(current ? [current] : [])].reverse().find((event) => event.browser);
  const screenshot = [...(events ?? []), ...(activity?.recent ?? []), ...(current ? [current] : [])].reverse().find((event) => event.capture?.kind === "screenshot")?.capture;
  const recording = [...(events ?? []), ...(activity?.recent ?? []), ...(current ? [current] : [])].reverse().find((event) => event.capture?.kind === "recording")?.capture;
  const frameRef = browserEvent?.browser?.frameRef;
  const frameURL = frameRef ? bridge.browserFrameURL(workspace.id, frameRef) : "";
  return <section className="ai-canvas ai-canvas-browser" aria-label="Browser agent exploration" data-canvas-mode="browser">
    <div className="browser-agent-shell">
      <header className="browser-agent-bar">
        <div className="browser-agent-location"><span /> <div><strong>{browserEvent?.browser?.title || "Cascade browser"}</strong><small>{browserEvent?.browser?.url || "Waiting for a safe page location"}</small></div></div>
        <div className="capture-signals">
          <CaptureSignal label="Screenshot" {...(screenshot?.phase ? { phase: screenshot.phase } : {})} />
          <CaptureSignal label="Recording" {...(recording?.phase ? { phase: recording.phase } : {})} persistent />
        </div>
      </header>
      <div className="browser-agent-frame">
        {frameURL ? <img src={frameURL} alt="Latest redacted browser frame" /> : forcedMode ? <div className="browser-frame-fixture" aria-label="Sanitized browser frame preview"><span className="browser-frame-fixture-nav" /><section><span /><span /><span /></section></div> : <p>Waiting for the first redacted browser frame.</p>}
        {current ? <div className={`browser-agent-status browser-agent-status-${current.status}`}><span>{statusLabels[current.status]}</span><strong>{current.title}</strong>{typeof current.progress === "number" ? <small>{current.progress}%</small> : null}</div> : null}
      </div>
    </div>
  </section>;
}

function CaptureSignal({ label, phase, persistent = false }: { label: string; phase?: string; persistent?: boolean }) {
  const active = phase === "starting" || phase === "active";
  const complete = phase === "completed";
  return <span className={`capture-signal ${active ? "active" : ""} ${complete ? "complete" : ""} ${persistent ? "persistent" : ""}`}><i />{label}{complete ? " saved" : active ? " active" : " idle"}</span>;
}

function EditorCanvas({ workspace, activity }: Pick<ProjectCanvasProps, "workspace" | "activity">) {
  const current = activity?.current;
  const takeover = isEditorTakeover(activity);
  const failed = current?.mode === "editor" && current.status === "failed";
  const sessionID = activity?.editorSessionID || workspace.cloudRun.editorSessionID;
  return <section className={`ai-canvas ai-canvas-editor ${takeover ? "agent-editing" : ""}`} aria-label="Video editor canvas" data-canvas-mode="editor">
    <div className="ai-canvas-editor-surface"><VideoEditor {...(sessionID ? { initialSessionID: sessionID } : {})} /></div>
    {takeover || failed ? <div className={`editor-takeover ${failed ? "failed" : ""}`} role="status">
      <span className="editor-takeover-mark" />
      <strong>{failed ? "Cascade paused the edit" : current?.title || "Cascade is editing"}</strong>
      {current?.detail ? <p>{current.detail}</p> : <p>{failed ? "The last valid revision is still available. Ask Cascade to retry." : "The editor will unlock when this revision passes validation."}</p>}
    </div> : null}
  </section>;
}
