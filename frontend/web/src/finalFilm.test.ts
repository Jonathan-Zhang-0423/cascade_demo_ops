import { describe, expect, it } from "vitest";
import { buildFinalFilmIntent } from "./finalFilm";

describe("final film presentation intents", () => {
  it("keeps provider/model choices server-owned and generation optional", () => {
    const intent = buildFinalFilmIntent("section_divider", 7, 42);
    expect(intent).toEqual(expect.objectContaining({
      intent_id: "presentation_section_divider_42",
      capability: "presentation_video_candidate",
      purpose: "section_divider",
      required: false,
      failure_policy: "continue_without_generated_candidate",
    }));
    expect(intent.requested_slot).toEqual({ preferred_duration_sec: 7, aspect_ratio: "16:9" });
    expect(intent.content_policy).toEqual({
      presentation_only: true,
      may_represent_business_step: false,
      may_replace_captured_ui: false,
      requires_explicit_review: true,
    });
    expect(intent).not.toHaveProperty("provider");
    expect(intent).not.toHaveProperty("model");
    expect(intent).not.toHaveProperty("source_step_id");
  });

  it("clamps UI duration input to the server contract", () => {
    expect(buildFinalFilmIntent("intro", 1, 1).requested_slot.preferred_duration_sec).toBe(4);
    expect(buildFinalFilmIntent("outro", 99, 2).requested_slot.preferred_duration_sec).toBe(15);
  });
});
