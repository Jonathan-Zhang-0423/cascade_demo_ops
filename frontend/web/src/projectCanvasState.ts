import type { ProjectActivityStateView, ProjectCanvasMode, ProjectWorkspaceView } from "./domain";

const activeStatuses = new Set(["queued", "running", "waiting_for_approval"]);

export function resolveProjectCanvasMode(workspace: ProjectWorkspaceView, activity?: ProjectActivityStateView): ProjectCanvasMode {
  const current = activity?.current;
  if (current && activeStatuses.has(current.status)) {
    if (current.mode === "browser") return "browser";
    if (current.mode === "editor") return "editor";
    return "act";
  }
  if (current?.mode === "browser" && current.status === "failed") return "browser";
  if (current?.mode === "editor" && current.status === "failed") return "editor";
  if (activity?.editorSessionID || workspace.cloudRun.editorSessionID) return "editor";
  if (workspace.cloudRun.status === "running" || workspace.cloudRun.status === "queued") {
    return workspace.cloudRun.stage === "browser_execution" ? "browser" : "act";
  }
  return "empty";
}

export function isEditorTakeover(activity?: ProjectActivityStateView): boolean {
  return activity?.current?.mode === "editor" && activeStatuses.has(activity.current.status);
}

