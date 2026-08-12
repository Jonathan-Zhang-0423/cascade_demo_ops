import { describe, expect, it } from "vitest";

import { evidenceBoundNameAllowed, evidenceBoundNameAllowedForInteraction, evaluateRequiredValidations, interactionRequiresResolvedTarget, isEvidenceBoundSelectorAlternative, resolutionAssertions, resolveUniqueVisibleEvidenceBoundTarget, routeTemplateMatches, urlPolicyError, validatedStageSecretValues } from "../src/browser-agent-runtime.js";

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

describe("browser agent credential broker boundary", () => {
  const stage = {
    id: "stage_login",
    order: 1,
    node_id: "node_login",
    target_contract: { semantic_id: "login_password", destructive: false },
    interactions: [{ kind: "fill", secret_ref: "vault://approved/login", non_destructive: true }],
  };

  it("accepts only the exact secret_ref declared by the approved stage", () => {
    expect(validatedStageSecretValues(stage, { "vault://approved/login": "test-secret" })).toEqual({
      "vault://approved/login": "test-secret",
    });
    expect(() => validatedStageSecretValues(stage, { "vault://other": "test-secret" })).toThrow("browser_agent_unapproved_secret_ref");
  });

  it("fails closed when the broker did not provide the approved secret", () => {
    expect(() => validatedStageSecretValues(stage, {})).toThrow("browser_agent_secret_ref_requires_broker");
  });

  it("does not require a DOM target for approved route/page observation actions", () => {
    expect(interactionRequiresResolvedTarget("navigate")).toBe(false);
    expect(interactionRequiresResolvedTarget("wait")).toBe(false);
    expect(interactionRequiresResolvedTarget("inspect")).toBe(false);
    expect(interactionRequiresResolvedTarget("click")).toBe(true);
    expect(interactionRequiresResolvedTarget("fill")).toBe(true);
    expect(interactionRequiresResolvedTarget("assert")).toBe(true);
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

  it("allows an unnamed App-evidence-bound form control only for fill/select", () => {
    expect(evidenceBoundNameAllowedForInteraction("", ["Project idea input"], candidate, "fill")).toBe(true);
    expect(evidenceBoundNameAllowedForInteraction("", ["Project idea input"], candidate, "select")).toBe(true);
    expect(evidenceBoundNameAllowedForInteraction("", ["Project idea input"], candidate, "click")).toBe(false);
    expect(evidenceBoundNameAllowedForInteraction("Delete project", ["Project idea input"], candidate, "fill")).toBe(false);
  });

  it("resolves one visible unnamed textarea for fill and still enforces role, uniqueness, and forbidden names", async () => {
    const locator = (count: number, role: string, name: string) => ({
      count: async () => count,
      first() {
        return {
          isVisible: async () => true,
          evaluate: async () => ({ role, name }),
        };
      },
    });
    const contract = {
      semantic_id: "project_idea_input",
      allowed_roles: ["textbox"],
      allowed_names: ["Project idea input"],
      forbidden_names: ["Delete"],
      destructive: false,
    };

    await expect(resolveUniqueVisibleEvidenceBoundTarget(locator(1, "textbox", ""), "interaction_css", contract, candidate, "fill")).resolves.toBeDefined();
    await expect(resolveUniqueVisibleEvidenceBoundTarget(locator(1, "textbox", ""), "interaction_css", contract, candidate, "click")).resolves.toBeUndefined();
    await expect(resolveUniqueVisibleEvidenceBoundTarget(locator(1, "button", ""), "interaction_css", contract, candidate, "fill")).resolves.toBeUndefined();
    await expect(resolveUniqueVisibleEvidenceBoundTarget(locator(2, "textbox", ""), "interaction_css", contract, candidate, "fill")).resolves.toBeUndefined();
    await expect(resolveUniqueVisibleEvidenceBoundTarget(locator(1, "textbox", "Delete project"), "interaction_css", contract, candidate, "fill")).rejects.toThrow("browser_agent_forbidden_target_name");
  });

  it("does not treat a textarea's current value as its accessible name", async () => {
    const element = {
      tagName: "TEXTAREA",
      labels: [],
      innerText: "Tetris",
      textContent: "Tetris",
      getAttribute: () => null,
      hasAttribute: () => false,
    };
    const locator = {
      count: async () => 1,
      first() {
        return {
          isVisible: async () => true,
          evaluate: async (callback: (value: typeof element) => unknown) => callback(element),
        };
      },
    };
    const contract = {
      semantic_id: "project_idea_input",
      allowed_roles: ["textbox"],
      allowed_names: ["Project idea input"],
      destructive: false,
    };
    await expect(resolveUniqueVisibleEvidenceBoundTarget(locator, "interaction_css", contract, candidate, "fill")).resolves.toBeDefined();
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

  it("verifies an explicit fill value on the exact App evidence-bound selector when the pre-scan result selector is stale", async () => {
    const stale = {
      first: () => ({ isVisible: async () => false }),
    };
    const current = {
      count: async () => 1,
      first: () => ({
        isVisible: async () => true,
        evaluate: async () => ({ role: "textbox", name: "" }),
        inputValue: async () => "Tetris",
      }),
    };
    const assertions = await evaluateRequiredValidations({
      locator: (selector: string) => selector === "[data-testid='input-project-idea']" ? current : stale,
    }, {
      id: "stage_fill",
      order: 1,
      node_id: "fill_project_idea",
      target_contract: {
        semantic_id: "project_idea_input",
        allowed_roles: ["textbox"],
        allowed_names: ["Project idea input"],
        destructive: false,
      },
      interactions: [{
        kind: "fill",
        target: { selector: "[data-testid='input-project-idea']" },
        value: "Tetris",
        non_destructive: true,
      }],
      evidence_bound_selector_alternatives: [{ kind: "css", value: "[data-testid='input-project-idea']" }],
      validations: [{
        id: "stale_pre_scan_target",
        kind: "element_visible",
        target: { selector: "[data-testid='old-generated-target']" },
        required: true,
      }],
    });
    expect(assertions).toEqual([{
      kind: "required_element_visible:stale_pre_scan_target",
      passed: true,
      actual: "evidence_bound_form_control_value_verified",
    }]);
  });

  it("does not accept a mismatched value or a non-evidence-bound fill selector", async () => {
    const current = {
      count: async () => 1,
      first: () => ({
        isVisible: async () => true,
        evaluate: async () => ({ role: "textbox", name: "" }),
        inputValue: async () => "Different value",
      }),
    };
    const stale = { first: () => ({ isVisible: async () => false }) };
    const page = {
      locator: (selector: string) => selector === "[data-testid='input-project-idea']" ? current : stale,
    };
    const baseStage = {
      id: "stage_fill",
      order: 1,
      node_id: "fill_project_idea",
      target_contract: {
        semantic_id: "project_idea_input",
        allowed_roles: ["textbox"],
        allowed_names: ["Project idea input"],
        destructive: false,
      },
      interactions: [{
        kind: "fill",
        target: { selector: "[data-testid='input-project-idea']" },
        value: "Tetris",
        non_destructive: true,
      }],
      validations: [{ id: "stale", kind: "element_visible", target: { selector: "#stale" }, required: true }],
    };
    const mismatched = await evaluateRequiredValidations(page, {
      ...baseStage,
      evidence_bound_selector_alternatives: [{ kind: "css", value: "[data-testid='input-project-idea']" }],
    });
    expect(mismatched[0]?.passed).toBe(false);
    const unbound = await evaluateRequiredValidations(page, baseStage);
    expect(unbound[0]?.passed).toBe(false);
  });

  it("accepts the App-declared post-click dynamic route when the reused pre-click selector disappears", async () => {
    const assertions = await evaluateRequiredValidations({
      url: () => "http://127.0.0.1:5000/project/project_42",
      locator: () => ({ first: () => ({ isVisible: async () => false }) }),
    }, {
      id: "stage_build",
      order: 4,
      node_id: "start_build",
      url: "http://127.0.0.1:5000/app",
      target_route_template: "/project/:id",
      expected_route_after_action: "/project/:id",
      runtime_route_verification_required: true,
      target_contract: { semantic_id: "build", destructive: false },
      interactions: [{ kind: "click", target: { selector: "[data-testid='button-create-project']" }, non_destructive: true }],
      validations: [{
        id: "reused_build_button",
        kind: "element_visible",
        target: { selector: "[data-testid='button-create-project']" },
        required: true,
      }],
    });
    expect(assertions).toEqual([{
      kind: "required_element_visible:reused_build_button",
      passed: true,
      actual: "expected_route_after_action_verified",
    }]);
  });

  it("does not accept a different route or a validation for a different target", async () => {
    const page = {
      url: () => "http://127.0.0.1:5000/settings",
      locator: () => ({ first: () => ({ isVisible: async () => false }) }),
    };
    const stage = {
      id: "stage_build",
      order: 4,
      node_id: "start_build",
      expected_route_after_action: "/project/:id",
      runtime_route_verification_required: true,
      target_contract: { semantic_id: "build", destructive: false },
      interactions: [{ kind: "click", target: { selector: "#build" }, non_destructive: true }],
      validations: [{ id: "result", kind: "element_visible", target: { selector: "#build" }, required: true }],
    };
    expect((await evaluateRequiredValidations(page, stage))[0]?.passed).toBe(false);
    expect((await evaluateRequiredValidations({ ...page, url: () => "http://127.0.0.1:5000/project/42" }, {
      ...stage,
      validations: [{ id: "other", kind: "element_visible", target: { selector: "#other" }, required: true }],
    }))[0]?.passed).toBe(false);
  });

  it("binds an App-approved observation route template when pre-scan URL evidence is stale", async () => {
    const stage = {
      id: "stage_observe",
      order: 5,
      node_id: "observe_build",
      url: "http://127.0.0.1:5000/app",
      target_route_template: "/project/:id",
      expected_route_after_action: "/project/:id",
      runtime_route_verification_required: true,
      target_contract: { semantic_id: "observe_build", destructive: false },
      interactions: [{ kind: "wait", non_destructive: true }],
      validations: [{
        id: "stale_pre_scan_url",
        kind: "url_matches",
        target: { url: "http://127.0.0.1:5000/app" },
        required: true,
      }],
    };

    expect(await evaluateRequiredValidations({ url: () => "http://127.0.0.1:5000/project/runtime_42" }, stage)).toEqual([{
      kind: "required_url_matches:stale_pre_scan_url",
      passed: true,
      actual: "approved_target_route_template_verified:/project/:id",
    }]);
  });

  it("does not use observation route binding for wrong routes, changed validation URLs, or mutating actions", async () => {
    const stage = {
      id: "stage_observe",
      order: 5,
      node_id: "observe_build",
      url: "http://127.0.0.1:5000/app",
      target_route_template: "/project/:id",
      runtime_route_verification_required: true,
      target_contract: { semantic_id: "observe_build", destructive: false },
      interactions: [{ kind: "wait", non_destructive: true }],
      validations: [{ id: "result_url", kind: "url_matches", target: { url: "http://127.0.0.1:5000/app" }, required: true }],
    };

    expect((await evaluateRequiredValidations({ url: () => "http://127.0.0.1:5000/settings" }, stage))[0]?.passed).toBe(false);
    expect((await evaluateRequiredValidations({ url: () => "http://127.0.0.1:5000/project/42" }, {
      ...stage,
      validations: [{ id: "changed", kind: "url_matches", target: { url: "http://127.0.0.1:5000/dashboard" }, required: true }],
    }))[0]?.passed).toBe(false);
    expect((await evaluateRequiredValidations({ url: () => "http://127.0.0.1:5000/project/42" }, {
      ...stage,
      interactions: [{ kind: "click", non_destructive: true }],
    }))[0]?.passed).toBe(false);
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
