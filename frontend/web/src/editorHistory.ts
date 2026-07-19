import type { EditorPlan } from "./editor";

export type EditorHistory = {
  past: EditorPlan[];
  future: EditorPlan[];
};

export function emptyEditorHistory(): EditorHistory {
  return { past: [], future: [] };
}

export function recordEditorHistory(history: EditorHistory, current: EditorPlan, limit = 50): EditorHistory {
  return { past: [...history.past, structuredClone(current)].slice(-limit), future: [] };
}

export function undoEditorHistory(history: EditorHistory, current: EditorPlan): { history: EditorHistory; plan?: EditorPlan } {
  const plan = history.past.at(-1);
  if (!plan) return { history };
  return {
    history: { past: history.past.slice(0, -1), future: [...history.future, structuredClone(current)] },
    plan: structuredClone(plan),
  };
}

export function redoEditorHistory(history: EditorHistory, current: EditorPlan): { history: EditorHistory; plan?: EditorPlan } {
  const plan = history.future.at(-1);
  if (!plan) return { history };
  return {
    history: { past: [...history.past, structuredClone(current)], future: history.future.slice(0, -1) },
    plan: structuredClone(plan),
  };
}

export function editorPlansEqual(left?: EditorPlan, right?: EditorPlan): boolean {
  return Boolean(left && right && JSON.stringify(left) === JSON.stringify(right));
}
