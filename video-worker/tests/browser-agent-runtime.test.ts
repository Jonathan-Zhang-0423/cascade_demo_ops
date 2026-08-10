import { describe, expect, it } from "vitest";

import { evidenceBoundNameAllowed, evaluateRequiredValidations, isEvidenceBoundSelectorAlternative, resolutionAssertions, routeTemplateMatches, urlPolicyError } from "../src/browser-agent-runtime.js";

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

describe("browser agent App-evidence-bound selector semantics", () => {
  const candidate = { kind: "css", value: `[data-testid="button-new-project"]` };

  it("accepts the live accessible name when the App scanner appended the same test id", () => {
    expect(evidenceBoundNameAllowed("新建项目", ["新建项目 button-new-project"], candidate)).toBe(true);
    expect(evidenceBoundNameAllowed("+ 新建项目", ["新建项目 button-new-project"], candidate)).toBe(true);
  });

  it("rejects a different business target even when its selector was App-declared", () => {
    expect(evidenceBoundNameAllowed("删除项目", ["新建项目 button-new-project"], candidate)).toBe(false);
  });

  it("reuses evidence-bound semantics only for the exact App candidate during re-observation and execution", () => {
    const stage = { evidence_bound_selector_alternatives: [candidate] };
    expect(isEvidenceBoundSelectorAlternative(stage, candidate)).toBe(true);
    expect(isEvidenceBoundSelectorAlternative(stage, { kind: "css", value: `[data-testid="project-list"]` })).toBe(false);
    expect(isEvidenceBoundSelectorAlternative(stage, { kind: "testid", value: "button-new-project" })).toBe(false);
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

describe("browser agent runtime route templates", () => {
  it("matches a runtime-created resource id without changing the template", () => {
    expect(routeTemplateMatches("/project/proj_42", "/project/:id")).toBe(true);
    expect(routeTemplateMatches("https://app.example.com/project/proj_42", "https://app.example.com/project/:id")).toBe(true);
    expect(routeTemplateMatches("/project/proj_42/logs", "/project/:id")).toBe(false);
    expect(routeTemplateMatches("/workspace/proj_42", "/project/:id")).toBe(false);
  });

  it("keeps dynamic route policy inside the approved template and blocks control paths", () => {
    const session = {
      allowedDomains: ["app.example.com"],
      allowedOrigins: ["https://app.example.com"],
      allowedRoutes: ["/project/:id"],
      forbiddenPages: ["/v1"],
      forbiddenPathPrefixes: [],
      forbiddenKeywords: [],
    } as any;
    expect(urlPolicyError("https://app.example.com/project/proj_42", session, false)).toBeUndefined();
    expect(urlPolicyError("https://app.example.com/project/proj_42/logs", session, false)).toBeUndefined();
    expect(urlPolicyError("https://app.example.com/settings", session, false)).toContain("route_not_allowed");
    expect(urlPolicyError("https://app.example.com/v1/execution-packages", { ...session, allowedRoutes: ["/"] }, false)).toContain("forbidden_page");
  });
});
