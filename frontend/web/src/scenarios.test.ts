import { describe, expect, it } from "vitest";
import { getScenarioTemplate, scenarioTemplates } from "./scenarios";

describe("scenario templates", () => {
  it("defines the three first-class app scenarios", () => {
    expect(scenarioTemplates.map((template) => template.id)).toEqual([
      "product_demo",
      "ai_customer_service_demo",
      "internal_onboarding_tutorial",
    ]);
  });

  it("defaults every scenario to video and step docs", () => {
    for (const template of scenarioTemplates) {
      expect(template.requiredAssets).toContain("demo_video");
      expect(template.requiredAssets).toContain("step_by_step_docs");
      expect(template.targetDurationSec).toBeGreaterThanOrEqual(60);
    }
  });

  it("captures AI customer service scenario workflow intent", () => {
    const template = getScenarioTemplate("ai_customer_service_demo");
    expect(template.defaultChecklist).toContain("知识检索");
    expect(template.defaultChecklist).toContain("转人工边界");
  });
});
