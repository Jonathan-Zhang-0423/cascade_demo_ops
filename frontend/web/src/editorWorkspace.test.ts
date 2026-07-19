import { describe, expect, it } from "vitest";
import type { EditorPlan, EditorSession, EditorShot, EditorTimelineStep, EditorValidationReport } from "./editor";
import { buildExportChecks, buildTimelineShots, formatTimecode, preservesRequiredStepOrder } from "./VideoEditor";

const steps: EditorTimelineStep[] = [
  { step_id: "open", order: 0, action: "navigate", status: "passed", required: true, start_ms: 0, end_ms: 2000, duration_ms: 2000 },
  { step_id: "create", order: 1, action: "click", status: "passed", required: true, start_ms: 2000, end_ms: 5000, duration_ms: 3000 },
];

function shot(id: string, stepID: string, range: [number, number]): EditorShot {
  return { id, source_artifact_id: "raw", source_step_id: stepID, source_time_range_ms: range, purpose: id, operations: [{ type: "trim" }], overlays: [] };
}

function plan(shots: EditorShot[]): EditorPlan {
  return {
    plan_id: "plan",
    source_authority: "server_local_editor",
    model_role: "presentation_optimizer_only",
    source_material_policy: "existing_assets_only",
    script_order_policy: "preserve_required_step_order",
    locked_fields: [],
    model_editable_fields: [],
    shots,
  };
}

function session(editPlan: EditorPlan): EditorSession {
  return {
    schema_version: "demoops.editor_session.v1",
    session_id: "edit",
    name: "test",
    mode: "demo_safe",
    status: "editing",
    revision: 1,
    asset_catalog: {
      catalog_id: "catalog",
      timeline: { duration_ms: 5000, recording_artifact_id: "raw" },
      steps,
      artifacts: [{ id: "raw", kind: "raw_recording", uri: "file:///raw.mp4", sha256: "sha", duration_ms: 5000 }],
    },
    edit_plan: editPlan,
    preview_profile: { width: 1280, height: 720, fps: 30, format: "mp4" },
    final_profile: { width: 1920, height: 1080, fps: 30, format: "mp4" },
    preview: { status: "not_started" },
    final_render: { status: "not_started" },
    provider_capabilities: [],
  };
}

const validReport: EditorValidationReport = {
  schema_version: "demoops.demo_edit_plan_validation.v1",
  valid: true,
  checked_at: "2026-07-19T10:00:00Z",
  errors: [],
  warnings: [],
};

describe("editor workspace policy", () => {
  it("maps output time without changing source ranges", () => {
    const timeline = buildTimelineShots(plan([shot("one", "open", [1000, 2500]), shot("two", "create", [3000, 5000])]));
    expect(timeline.map(({ startMS, endMS }) => [startMS, endMS])).toEqual([[0, 1500], [1500, 3500]]);
    expect(formatTimecode(65_400)).toBe("01:05.4");
  });

  it("blocks missing and reordered required business evidence", () => {
    const missingPlan = plan([shot("one", "open", [0, 2000])]);
    const missingChecks = buildExportChecks(session(missingPlan), missingPlan, validReport);
    expect(missingChecks.find((check) => check.id === "required")?.tone).toBe("error");

    const reordered = [shot("two", "create", [2000, 5000]), shot("one", "open", [0, 2000])];
    expect(preservesRequiredStepOrder(reordered, steps)).toBe(false);
    const orderChecks = buildExportChecks(session(plan(reordered)), plan(reordered), validReport);
    expect(orderChecks.find((check) => check.id === "order")?.tone).toBe("error");
  });

  it("blocks unsupported pixels and generated candidates bound to business steps", () => {
    const unsafeShot = shot("generated", "open", [0, 2000]);
    unsafeShot.source_artifact_id = "generated";
    unsafeShot.operations = [{ type: "zoom_pan" }];
    const unsafePlan = plan([unsafeShot, shot("two", "create", [2000, 5000])]);
    const unsafeSession = session(unsafePlan);
    unsafeSession.asset_catalog.artifacts.push({ id: "generated", kind: "generated_video_candidate", uri: "file:///candidate.mp4", sha256: "generated-sha", duration_ms: 2000 });
    const checks = buildExportChecks(unsafeSession, unsafePlan, validReport);
    expect(checks.find((check) => check.id === "generated")?.tone).toBe("error");
    expect(checks.find((check) => check.id === "operations")?.tone).toBe("error");
  });
});
