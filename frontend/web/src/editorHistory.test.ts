import { describe, expect, it } from "vitest";
import type { EditorPlan } from "./editor";
import { emptyEditorHistory, recordEditorHistory, redoEditorHistory, undoEditorHistory } from "./editorHistory";

function plan(purpose: string): EditorPlan {
  return {
    plan_id: "plan_1",
    source_authority: "server_local_editor",
    model_role: "presentation_optimizer_only",
    source_material_policy: "existing_assets_only",
    script_order_policy: "preserve_required_step_order",
    locked_fields: [],
    model_editable_fields: [],
    shots: [{ id: "shot_1", source_artifact_id: "asset_1", purpose }],
  };
}

describe("editor history", () => {
  it("supports bounded undo and redo snapshots", () => {
    let history = emptyEditorHistory();
    history = recordEditorHistory(history, plan("one"), 2);
    history = recordEditorHistory(history, plan("two"), 2);
    history = recordEditorHistory(history, plan("three"), 2);
    expect(history.past.map((item) => item.shots[0]?.purpose)).toEqual(["two", "three"]);

    const undone = undoEditorHistory(history, plan("four"));
    expect(undone.plan?.shots[0]?.purpose).toBe("three");
    const redone = redoEditorHistory(undone.history, undone.plan!);
    expect(redone.plan?.shots[0]?.purpose).toBe("four");
  });
});
