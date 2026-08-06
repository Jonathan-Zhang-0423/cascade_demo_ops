import { describe, expect, it } from "vitest";
import { createWorkspace } from "./mockWorkspace";
import type { ProjectActivityStateView } from "./domain";
import { isEditorTakeover, resolveProjectCanvasMode } from "./projectCanvasState";

function state(overrides: Partial<ProjectActivityStateView>): ProjectActivityStateView {
  return { projectID: "project_1", mode: "empty", recent: [], ...overrides };
}

describe("resolveProjectCanvasMode", () => {
  const workspace = createWorkspace("product_demo");

  it("keeps an inactive project blank", () => {
    expect(resolveProjectCanvasMode(workspace, state({}))).toBe("empty");
  });

  it("prioritizes active browser and editor work", () => {
    expect(resolveProjectCanvasMode(workspace, state({ mode: "browser", current: { id: "1", projectID: workspace.id, runID: "r", mode: "browser", kind: "observe", status: "running", title: "Observe", occurredAt: new Date().toISOString() } }))).toBe("browser");
    expect(resolveProjectCanvasMode(workspace, state({ mode: "editor", current: { id: "2", projectID: workspace.id, runID: "r", mode: "editor", kind: "edit", status: "running", title: "Edit", occurredAt: new Date().toISOString() } }))).toBe("editor");
  });

  it("uses act for other active tool work", () => {
    expect(resolveProjectCanvasMode(workspace, state({ mode: "act", current: { id: "3", projectID: workspace.id, runID: "r", mode: "act", kind: "plan", status: "waiting_for_approval", title: "Plan", occurredAt: new Date().toISOString() } }))).toBe("act");
  });

  it("reopens a valid editor session for review", () => {
    const activity = state({ mode: "empty", editorSessionID: "editor_1" });
    expect(resolveProjectCanvasMode(workspace, activity)).toBe("editor");
    expect(isEditorTakeover(activity)).toBe(false);
  });

  it("keeps failed browser and editor surfaces visible", () => {
    expect(resolveProjectCanvasMode(workspace, state({ current: { id: "4", projectID: workspace.id, runID: "r", mode: "browser", kind: "run", status: "failed", title: "Failed", occurredAt: new Date().toISOString() } }))).toBe("browser");
    expect(resolveProjectCanvasMode(workspace, state({ current: { id: "5", projectID: workspace.id, runID: "r", mode: "editor", kind: "edit", status: "failed", title: "Failed", occurredAt: new Date().toISOString() } }))).toBe("editor");
  });
});

