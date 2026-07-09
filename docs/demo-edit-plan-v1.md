# Demo Edit Plan v1

`demoops.demo_edit_plan.v1` is the cloud-side video planning contract used
after a customer-side recording run has produced UI recordings, screenshots,
execution traces, and artifact descriptors.

The model designs editing decisions only. It must not create new product UI
actions, images, video clips, or synthetic screenshots. Final composition must
use existing artifacts referenced by the catalog.

The collaboration boundary is part of the protocol:

```text
Customer-side agent owns what must be demonstrated.
Cloud-side AIGC may only optimize how the verified material is presented.
```

## Objects

- `AssetTimelineCatalog` (`demoops.asset_timeline_catalog.v1`) describes the
  available source material: execution steps, artifacts, timing, and constraints.
- `DemoEditPlan` (`demoops.demo_edit_plan.v1`) describes shots, trims, camera
  moves, overlays, captions, callouts, and visual treatment.
- `DemoEditPlanValidationReport`
  (`demoops.demo_edit_plan_validation.v1`) records whether the plan is safe to
  render.

The Go DTOs live in `backend/internal/model/demo_edit_plan.go`. The current
worker implementation writes these JSON files from `video-worker/src/renderer.ts`.

## Required Policies

Every persisted edit plan must use:

```json
{
  "source_authority": "customer_side_agent",
  "model_role": "presentation_optimizer_only",
  "source_material_policy": "existing_assets_only",
  "script_order_policy": "preserve_required_step_order",
  "locked_fields": [
    "source_authority",
    "model_role",
    "source_material_policy",
    "script_order_policy",
    "source_artifact_id",
    "source_step_id",
    "source_time_range_ms",
    "required_step_order"
  ],
  "model_editable_fields": [
    "purpose",
    "overlays.text",
    "global_style.color_grade",
    "global_style.pacing",
    "global_style.transition_style",
    "operations.zoom",
    "operations.speed",
    "operations.style"
  ]
}
```

The interaction script remains the primary storyline. A planner may trim,
emphasize, caption, crop, zoom, pan, hold, change speed, apply transitions, blur
regions, and color grade existing material, but required script steps must keep
their original order.

## Field Ownership

The customer-side agent is the authority for product facts, required UI steps,
business meaning, safety boundaries, and the interaction story. The cloud-side
AIGC role is limited to presentation optimization.

Locked fields cannot be treated as model-editable:

- source authority and model role
- source material and script order policies
- `source_artifact_id`
- `source_step_id`
- `source_time_range_ms`
- required step order

Model-editable fields are intentionally narrow:

- shot `purpose`
- overlay text such as captions and callouts
- global color, pacing, and transition style
- zoom, speed, and operation style parameters

If a plan declares locked fields as editable, omits required locked fields, or
adds unsupported model-editable fields, validation must reject it before render.

## Source References

Each shot must reference existing material:

- `source_artifact_id` must match an artifact in the catalog.
- `source_step_id` should match the step that produced or explains the shot.
- `source_time_range_ms` is required for raw recording clips and must be inside
  the referenced source duration.

Sensitive artifacts must not be used in renderable shots. Redaction and blur
operations should be represented as edit operations or overlays that reference
the existing source, not as generated replacement media.

## Prohibited Instructions

Plans must reject fields that ask a model to generate new media or browser
actions, including:

- `image_prompt`
- `video_prompt`
- `generate_asset`
- `text_to_image`
- `text_to_video`
- `create_image`
- `create_video`
- `new_ui_action`
- `browser_action`
- `action_spec`

The validation report should mark these as
`prohibited_generation_instruction`.

## Pipeline

1. Load `RecordingResultPackage`, execution trace, artifact manifest, and raw
   recording paths.
2. Build `AssetTimelineCatalog`.
3. Create or accept a `DemoEditPlan`.
4. Validate that the plan uses existing material only and preserves required
   step order.
5. Render/composite the final video from referenced artifacts only.

This keeps multimodal models in the role of editor/director while preserving the
recorded product interaction as the factual source of truth.
