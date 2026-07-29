import { describe, expect, it } from "vitest";

import { evaluateRequiredValidations, resolutionAssertions, urlPolicyError } from "../src/browser-agent-runtime.js";

describe("browser agent target resolution feedback", () => {
  it("keeps an unresolved target as a failed structured assertion", () => {
    expect(resolutionAssertions(undefined, "browser_agent_target_not_resolved: create; strategies=component_testid", "https://app.example.com/dashboard")).toEqual([
      { kind: "target_resolved", passed: false, actual: "browser_agent_target_not_resolved: create; strategies=component_testid" },
    ]);
  });

  it("does not mislabel an unresolved target as page success", () => {
    expect(resolutionAssertions(undefined, "", "https://app.example.com/dashboard")).toEqual([
      { kind: "page_observed", passed: true, actual: "https://app.example.com/dashboard" },
    ]);
  });
});

describe("browser agent navigation policy", () => {
  const session = {
    allowedDomains: ["app.example.com"],
    allowedOrigins: ["https://app.example.com"],
    allowedRoutes: ["/dashboard"],
    forbiddenPages: ["/v1"],
    forbiddenPathPrefixes: ["/aigc"],
    forbiddenKeywords: ["delete"],
  } as any;

  it("allows only an approved origin and route subtree for navigations", () => {
    expect(urlPolicyError("https://app.example.com/dashboard/projects/1", session, false)).toBeUndefined();
    expect(urlPolicyError("http://app.example.com/dashboard", session, false)).toContain("origin_not_allowed");
    expect(urlPolicyError("https://app.example.com/settings", session, false)).toContain("route_not_allowed");
  });
});

describe("browser agent required validations", () => {
  it("accepts the App page_loaded validation for an interactive document", async () => {
    const assertions = await evaluateRequiredValidations({
      evaluate: async () => "interactive",
    }, {
      id: "stage_open",
      order: 1,
      node_id: "open",
      target_contract: { semantic_id: "target_open", destructive: false },
      interactions: [{ kind: "navigate", non_destructive: true }],
      validations: [{ id: "page_ready", kind: "page_loaded", required: true }],
    });
    expect(assertions).toEqual([{ kind: "required_page_loaded:page_ready", passed: true, actual: "interactive" }]);
  });
});
