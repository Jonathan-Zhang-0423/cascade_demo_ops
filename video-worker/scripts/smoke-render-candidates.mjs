import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

import { render } from "../dist/renderer.js";

const root = await mkdtemp(path.join(tmpdir(), "cascade-render-candidates-"));
const previousFFmpeg = process.env.CASCADE_FFMPEG_PATH;

try {
  process.env.CASCADE_FFMPEG_PATH = "definitely-not-installed-ffmpeg-for-candidate-smoke";
  const candidatePath = path.join(root, "candidate.mp4");
  await writeFile(candidatePath, Buffer.from("candidate video bytes"));

  const baseRequest = {
    output_dir: path.join(root, "render"),
    duration_sec: 2,
    graph: {
      id: "graph_candidates_smoke",
      version: 1,
      nodes: [{ id: "node_open", action: "navigate", expected_outcome: "Product page loads", duration_hint_ms: 1000 }],
    },
    generated_assets: [
      {
        id: "artifact_generated_candidate_001",
        kind: "generated_video_candidate",
        uri: candidatePath,
        mime_type: "video/mp4",
        metadata: {
          asset_role: "director_preview_candidate",
          include_in_demo: false,
          source_material_policy: "non_authoritative_generated_candidate",
          non_authoritative: true,
        },
      },
    ],
    edit_plan: {
      schema_version: "demoops.demo_edit_plan.v1",
      plan_id: "edit_plan_candidate_smoke",
      source_authority: "customer_side_agent",
      model_role: "presentation_optimizer_only",
      source_material_policy: "existing_assets_only",
      script_order_policy: "preserve_required_step_order",
      locked_fields: [
        "source_authority",
        "model_role",
        "source_material_policy",
        "script_order_policy",
        "source_artifact_id",
        "source_step_id",
        "source_time_range_ms",
        "required_step_order",
      ],
      model_editable_fields: [
        "purpose",
        "overlays.text",
        "global_style.color_grade",
        "global_style.pacing",
        "global_style.transition_style",
        "operations.zoom",
        "operations.speed",
        "operations.style",
      ],
      target_duration_ms: 1000,
      shots: [
        {
          id: "shot_candidate_preview",
          source_artifact_id: "artifact_generated_candidate_001",
          source_time_range_ms: [0, 1000],
          purpose: "Use an approved non-authoritative generated preview as a presentation-only divider.",
          operations: [{ type: "trim", start_ms: 0, end_ms: 1000 }],
        },
      ],
    },
  };

  await assert.rejects(
    () => render(baseRequest),
    /generated_candidate_not_approved/,
  );

  const approvedRequest = structuredClone(baseRequest);
  approvedRequest.output_dir = path.join(root, "render-approved");
  Object.assign(approvedRequest.generated_assets[0].metadata, {
    approved_for_demo: true,
    artifact_variant: "normalized",
    normalization_status: "ok",
    media_probe_status: "ok",
    normalization_profile: "editor_mp4_h264_yuv420p_1920x1080_cfr30_v1",
  });

  const result = await render(approvedRequest);
  const manifest = JSON.parse(await readFile(result.render_manifest_path, "utf8"));
  const report = JSON.parse(await readFile(result.requirement_satisfaction_report_path, "utf8"));

  assert.equal(result.validation_report.valid, true);
  assert.equal(manifest.compositor.source_artifact_id, "artifact_generated_candidate_001");
  assert.equal(manifest.compositor.method, "copy_source_recording");
  assert.deepEqual(report.actual.represented_step_ids, []);
  assert.ok(report.step_checks.some((check) => check.step_id === "node_open" && check.represented_in_edit_plan === false));

  const finalReviewPendingRequest = structuredClone(baseRequest);
  finalReviewPendingRequest.output_dir = path.join(root, "render-final-review-pending");
  Object.assign(finalReviewPendingRequest.generated_assets[0].metadata, {
    approval_mode: "final_output_review_pending",
    review_scope: "final_output",
    automated_quality_gate_passed: true,
    artifact_variant: "normalized",
    normalization_status: "ok",
    media_probe_status: "ok",
    normalization_profile: "editor_mp4_h264_yuv420p_1920x1080_cfr30_v1",
  });
  const pendingResult = await render(finalReviewPendingRequest);
  assert.equal(pendingResult.validation_report.valid, true);
} finally {
  if (previousFFmpeg === undefined) {
    delete process.env.CASCADE_FFMPEG_PATH;
  } else {
    process.env.CASCADE_FFMPEG_PATH = previousFFmpeg;
  }
  await rm(root, { recursive: true, force: true });
}
