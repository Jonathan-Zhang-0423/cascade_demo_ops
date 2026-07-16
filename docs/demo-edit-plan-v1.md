# Demo Edit Plan v1

`demoops.demo_edit_plan.v1` is the cloud-side video planning contract used
after a customer-side recording run has produced UI recordings, screenshots,
execution traces, and artifact descriptors.

The model designs editing decisions only. It must not create new product UI
actions, images, video clips, or synthetic screenshots. Final composition must
use existing artifacts referenced by the catalog.

The collaboration boundary is part of the protocol:

```text
User explicit requirements > Customer-side execution script > Cloud-side model suggestions.
```

User explicit requirements include manual prompts, must-show/must-not-show
items, target audience, target duration, caption/voiceover expectations, and
style requirements carried by the cloud package metadata or already normalized
by the customer-side package. The customer-side agent remains the authority for
product interaction facts, required UI steps, execution order, and captured
source material. Cloud-side AIGC may only optimize how the verified material is
presented.

## Objects

- `AssetTimelineCatalog` (`demoops.asset_timeline_catalog.v1`) describes the
  available source material: execution steps, artifacts, timing, and constraints.
- `DemoEditPlan` (`demoops.demo_edit_plan.v1`) describes shots, trims, camera
  moves, overlays, captions, callouts, and visual treatment.
- `DemoEditPlanValidationReport`
  (`demoops.demo_edit_plan_validation.v1`) records whether the plan is safe to
  render.
- `ArkMediaGenerationResult`
  (`demoops.ark_media_generation_result.v1`) records optional Ark provider task
  status, poll attempts, non-authoritative generated candidate URLs, and
  downloaded candidate artifacts.
- `CandidateAssetReview`
  (`demoops.candidate_asset_review.v1`) records whether downloaded generated
  candidates are approved for future presentation-only edit-plan references.
- `CandidateAssetEditPlanPatch`
  (`demoops.candidate_asset_edit_plan_patch.v1`) records optional
  presentation-only shots that could be added from approved generated
  candidates after explicit opt-in and renderer validation.

The Go DTOs live in `backend/internal/model/demo_edit_plan.go`. The current
worker implementation writes these JSON files from `video-worker/src/renderer.ts`.
The model-facing source bundle lives in `backend/internal/model/director_input.go`
as `demoops.director_input.v1`. Director/model suggestions use
`demoops.director_edit_suggestion.v1` and are validated by
`demoops.director_edit_suggestion_validation.v1` before any later conversion
into `DemoEditPlan` changes.
`demoops.director_edit_plan_patch.v1` is the controlled bridge from a
`DirectorEditSuggestion` to renderable `DemoEditPlan` fields. It may propose
shot purpose text, caption overlays, safe operation hints, and global style
changes, but it copies locked source fields only for audit and must validate
that they still match the base plan. It is not auto-applied to the current final
video.
`demoops.director_edit_plan_patch_apply_result.v1` records whether the validated
patch was actually merged into a new `DemoEditPlan` and whether the worker
re-rendered the final video from existing captured assets. This apply step is
deterministic renderer work, not model rendering: the model proposes, the
renderer executes, and product UI footage still comes only from captured source
material.

`director_input.workflow.nodes[]` preserves customer-side interaction context
from the latest workflow graph and recording spec: narrative cues, capture
instructions, selector alternatives, evidence refs, capture windows, timing
hints, and verification metadata. A node with
`verification.authority=verified_product_fact` may be treated as customer-side
verified product evidence. A node with
`verification.authority=runtime_adaptive_executable_intent` is executable
intent only; it must not be presented as fully verified product proof unless the
cloud recording result later captures successful artifacts for that step.
Director suggestions must preserve this distinction: verified steps may be
emphasized as source-backed product facts, while runtime-adaptive steps should
be described as runtime-resolved interactions to capture and validate from the
recording result. Captions, shot purposes, and model prompts must not claim that
a runtime-adaptive outcome is complete, successful, or proven before recorded
artifacts confirm it.

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

The director input must also persist a decision priority block equivalent to:

```text
1. user_explicit_requirements
2. client_execution_script
3. cloud_model_suggestion
```

If an explicit user requirement conflicts with the script or model suggestion,
the renderer/model adapter must either satisfy the user requirement from
approved captured material or report it as unsatisfied. It must not silently
downgrade user intent into an optional model hint.

## Field Ownership

The customer-side agent is the authority for product facts, required UI steps,
business meaning, safety boundaries, and the interaction story. The cloud-side
AIGC role is limited to presentation optimization.

User explicit requirements are the delivery intent authority. They may come
from direct cloud-side metadata such as `metadata.user_demo_intent` or from
customer-side normalized fields such as `workflow_graph.intent`,
`workflow_graph.requirements`, and `project_context_summary.goals`.

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

Generated provider candidates are not normal demo material. A
`generated_video_candidate` may be referenced only after
`CandidateAssetReview` or an equivalent explicit review has marked the
downloaded artifact with `metadata.approved_for_demo=true`,
`metadata.non_authoritative=true`, and
`metadata.source_material_policy=non_authoritative_generated_candidate`. Such
shots must not set `source_step_id`; they are presentation-only and cannot count
as evidence that a product workflow step was shown.

`CandidateAssetEditPlanPatch` is not the final edit plan. It is a safe proposal
layer for future intro, divider, or outro shots. It must declare
`auto_apply=false`, require explicit opt-in, preserve required step order, and
require renderer validation before any proposed shot is merged into a real
`DemoEditPlan`.

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
3. Build `DirectorInput` for model-facing source context.
4. Build Ark media request previews and asset publication preflight artifacts.
5. Ask the configured `DirectorAdapter` for a `DirectorEditSuggestion`; its
   `provider_gate` must say whether a real provider call is disabled, blocked,
   dry-run only, ready for a later controlled call, submitted, or failed. If a
   provider call is submitted, `provider_call` records the non-authoritative
   Ark task trace.
6. Convert the suggestion into a `DirectorEditPlanPatch` and validate that it
   only touches allowed presentation fields while preserving locked source
   fields. The patch remains a proposal until a controlled apply/re-render step
   is explicitly enabled.
7. Apply the validated patch through the deterministic renderer path when it
   contains safe shot-level updates. The apply result must record whether a new
   plan was produced and whether the final video was re-rendered. If validation
   fails, source fields do not match, or no renderable shot patch exists, keep
   the base render and report the reason.
8. Normalize provider output into `ArkMediaGenerationResult`. Returned URLs may
   become candidate artifacts only; pending async task ids do not become media.
   If polling returns downloadable URLs, persist them as local
   `downloaded_artifacts`.
9. Create `CandidateAssetReview` for downloaded provider candidates. Approved
   artifacts may be referenced by later presentation-only edit-plan shots, but
   are still not auto-included in the final video.
10. Create `CandidateAssetEditPlanPatch` as an optional proposal from approved
   candidates. The patch is not applied to the current final video by default.
11. Validate the suggestion for priority order, source tracing, provider gate,
   and source-only policy.
12. Create or accept a `DemoEditPlan`.
13. Validate that the plan uses existing material only and preserves required
   step order.
14. Render/composite the final video from referenced artifacts only.

This keeps multimodal models in the role of editor/director while preserving the
recorded product interaction as the factual source of truth.
