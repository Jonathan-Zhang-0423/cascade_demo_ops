import { describe, expect, it } from "vitest";

import {
  evidenceBoundNameAllowed,
  evaluateRequiredValidations,
  isEvidenceBoundSelectorAlternative,
  resolutionAssertions,
  urlPolicyError,
  usesPassiveRouteResolution,
  waitObservationRouteResolution,
} from "../src/browser-agent-runtime.js";

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

  it("prefers an approved stable selector when visible text changes after a click", async () => {
	const calls: string[] = [];
	const page = {
	  locator: (selector: string) => ({ first: () => ({ isVisible: async () => { calls.push(`selector:${selector}`); return true; } }) }),
	  getByLabel: (label: string) => ({ first: () => ({ isVisible: async () => { calls.push(`label:${label}`); return false; } }) }),
	};
	const assertions = await evaluateRequiredValidations(page, {
	  id: "stage_mode",
	  order: 1,
	  node_id: "mode",
	  target_contract: { semantic_id: "target_mode", destructive: false },
	  interactions: [{ kind: "click", non_destructive: true }],
	  validations: [{ id: "mode_visible", kind: "element_visible", required: true, target: { selector: "[data-testid='build-mode']", label: "Select build mode" } }],
	});
	expect(assertions[0]?.passed).toBe(true);
	expect(calls).toEqual(["selector:[data-testid='build-mode']"]);
  });
});

describe("browser agent route validation", () => {
  it("matches an approved dynamic route template after submit", async () => {
	const assertions = await evaluateRequiredValidations({ url: () => "https://app.example.com/project/demo-tetris" }, {
	  id: "stage_submit",
	  order: 1,
	  node_id: "submit",
	  url: "https://app.example.com/app",
	  target_contract: { semantic_id: "target_submit", destructive: false },
	  interactions: [{ kind: "click", non_destructive: true }],
	  validations: [{ id: "project_route", kind: "url_matches", required: true, target: { url: "/project/:id" } }],
	});
	expect(assertions).toEqual([{ kind: "required_url_matches:project_route", passed: true, actual: "https://app.example.com/project/demo-tetris" }]);
  });

  it("binds a passive wait stage to its approved dynamic route", () => {
	const stage = {
	  id: "stage_observe",
	  order: 5,
	  node_id: "observe_progress",
	  entry_route: "/project/:id",
	  route: "/project/:id",
	  url: "https://app.example.com/project/:id",
	  target_contract: { semantic_id: "target_progress", destructive: false },
	  interactions: [{ kind: "wait" as const, target: { url: "https://app.example.com/project/:id" }, non_destructive: true }],
	};
	expect(waitObservationRouteResolution("https://app.example.com/project/demo-tetris", stage, stage.interactions[0])).toEqual({
	  strategy: "approved_route",
	  failure: "",
	});
  });

  it("does not resolve a passive wait stage on a different route", () => {
	const stage = {
	  id: "stage_observe",
	  order: 5,
	  node_id: "observe_progress",
	  route: "/project/:id",
	  target_contract: { semantic_id: "target_progress", destructive: false },
	  interactions: [{ kind: "wait" as const, target: { url: "/project/:id" }, non_destructive: true }],
	};
	expect(waitObservationRouteResolution("https://app.example.com/app", stage, stage.interactions[0])).toEqual({
	  failure: "browser_agent_target_not_resolved: observe_progress; strategies=approved_route",
	});
  });

  it("uses route evidence for a final inspect whose approved result is route-only", () => {
	const interaction = { kind: "inspect" as const, target: { text: "Final state" }, non_destructive: true };
	const stage = {
	  id: "stage_final",
	  order: 6,
	  node_id: "final_observe",
	  stage_kind: "final_observe",
	  route: "/project/:id",
	  target_contract: { semantic_id: "target_final", destructive: false },
	  interactions: [interaction],
	  validations: [{ id: "final_route", kind: "url_matches", target: { url: "/project/:id" }, required: true }],
	};
	expect(usesPassiveRouteResolution(stage, interaction)).toBe(true);
	expect(waitObservationRouteResolution("https://app.example.com/project/demo-tetris", stage, interaction)).toEqual({
	  strategy: "approved_route",
	  failure: "",
	});
  });

  it("keeps a final inspect with an element result bound to its live target", () => {
	const interaction = { kind: "inspect" as const, target: { role: "heading", text: "Build complete" }, non_destructive: true };
	const stage = {
	  id: "stage_final",
	  order: 6,
	  node_id: "final_observe",
	  stage_kind: "final_observe",
	  target_contract: { semantic_id: "target_final", destructive: false },
	  interactions: [interaction],
	  validations: [{ id: "final_heading", kind: "text_contains", target: { role: "heading", text: "Build complete" }, required: true }],
	};
	expect(usesPassiveRouteResolution(stage, interaction)).toBe(false);
  });
});
