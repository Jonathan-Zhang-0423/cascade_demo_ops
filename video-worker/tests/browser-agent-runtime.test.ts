import { describe, expect, it } from "vitest";

import { approvedKeyboardKeys, captureTargetGeometry, evidenceBoundNameAllowed, evidenceBoundNameAllowedForInteraction, evaluateRequiredValidations, interactionRequiresResolvedTarget, isEvidenceBoundSelectorAlternative, normalizedApprovedTargetName, recoveredScreenshotMetadata, resolutionAssertions, resolveTarget, resolveUniqueVisibleEvidenceBoundTarget, routeTemplateMatches, stageExecutionTargetURL, urlPolicyError, validatedStageSecretValues, validationTimeoutMilliseconds, type BrowserTargetResolutionAttempt } from "../src/browser-agent-runtime.js";

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

describe("browser agent recording profile", () => {
  it("rejects a non-canonical recording viewport before opening Chromium", async () => {
    await expect(import("../src/browser-agent-runtime.js").then(({ openBrowserAgentSession }) => openBrowserAgentSession({
      output_dir: "artifacts/test-only",
      allowed_domains: ["app.example.com"],
      browser: { viewport: { width: 1440, height: 900 } },
    }))).rejects.toThrow("browser_agent_recording_resolution_mismatch");
  });
});

describe("browser agent bounded target-name redundancy", () => {
  it("normalizes only approved action/control wording and keeps exact business identity", () => {
    expect(normalizedApprovedTargetName("点击新建项目入口")).toBe("新建项目");
    expect(normalizedApprovedTargetName("请选择构建按钮")).toBe("构建");
    expect(normalizedApprovedTargetName("Please click the New Project button")).toBe("New Project");
    expect(normalizedApprovedTargetName("删除项目")).toBe("删除项目");
  });
});

describe("browser agent target geometry evidence", () => {
  it("clips a live DOM box to the content viewport and stores only a selector digest", async () => {
    const locator = { boundingBox: async () => ({ x: -10, y: 100, width: 210, height: 80 }) };
    const geometry = await captureTargetGeometry({
      openedAtMS: Date.now() - 500,
      page: {
        viewportSize: () => ({ width: 1000, height: 500 }),
        evaluate: async () => 2,
      },
    } as any, {
      id: "stage_create", order: 1, node_id: "create",
      target_contract: { semantic_id: "new_project", destructive: false },
      interactions: [{ kind: "click", non_destructive: true }],
    }, {
      locator,
      strategy: "approved_evidence_css",
      approvedAlternative: { kind: "css", value: "[data-testid='button-new-project']" },
    }, "artifact_target");
    expect(geometry).toMatchObject({
      schema_version: "demoops.browser_target_geometry.v1",
      target_semantic_id: "new_project",
      resolution_strategy: "approved_evidence_css",
      viewport: { width: 1000, height: 500, dpr: 2 },
      element_box_css_px: { x: 0, y: 100, width: 200, height: 80 },
      element_box_normalized: { x: 0, y: 0.2, width: 0.2, height: 0.16 },
      screenshot_artifact_id: "artifact_target",
      confidence: 1,
    });
    expect(geometry?.selector_digest_sha256).toMatch(/^[a-f0-9]{64}$/);
    expect(JSON.stringify(geometry)).not.toContain("button-new-project");
  });

  it("fails closed when the target has no visible in-viewport geometry", async () => {
    const geometry = await captureTargetGeometry({
      openedAtMS: Date.now(),
      page: { viewportSize: () => ({ width: 1000, height: 500 }), evaluate: async () => 1 },
    } as any, {
      id: "stage_create", order: 1, node_id: "create",
      target_contract: { semantic_id: "new_project", destructive: false },
      interactions: [{ kind: "click", non_destructive: true }],
    }, { locator: { boundingBox: async () => null }, strategy: "component_testid" });
    expect(geometry).toBeUndefined();
  });

  it("retains verified target geometry when close recovers stage screenshots", () => {
    const geometry = {
      schema_version: "demoops.browser_target_geometry.v1" as const,
      target_semantic_id: "new_project",
      resolution_strategy: "approved_evidence_css",
      selector_digest_sha256: "a".repeat(64),
      captured_at: "2026-08-13T00:00:00Z",
      recording_offset_ms: 1200,
      viewport: { width: 2560, height: 1440, dpr: 1 },
      element_box_css_px: { x: 100, y: 80, width: 200, height: 60 },
      element_box_normalized: { x: 0.0390625, y: 0.0555555556, width: 0.078125, height: 0.0416666667 },
      screenshot_artifact_id: "artifact_session_create_target",
      confidence: 1,
    };
    const evidence = new Map([["artifact_session_create_target", geometry]]);

    expect(recoveredScreenshotMetadata("target", "artifact_session_create_target", evidence)).toMatchObject({
      capture_phase: "target",
      recovered_at_session_close: true,
      target_geometry: geometry,
    });
    expect(recoveredScreenshotMetadata("before", "artifact_session_create_target", evidence)).not.toHaveProperty("target_geometry");
    expect(recoveredScreenshotMetadata("target", "artifact_session_other_target", evidence)).not.toHaveProperty("target_geometry");
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
		expect(interactionRequiresResolvedTarget("press")).toBe(false);
    expect(interactionRequiresResolvedTarget("click")).toBe(true);
    expect(interactionRequiresResolvedTarget("fill")).toBe(true);
    expect(interactionRequiresResolvedTarget("assert")).toBe(true);
  });

	it("accepts only the four approved gameplay keys", () => {
		expect(approvedKeyboardKeys({ keys: "ArrowLeft,ArrowRight,ArrowDown,ArrowUp" })).toEqual(["ArrowLeft", "ArrowRight", "ArrowDown", "ArrowUp"]);
		expect(approvedKeyboardKeys({ keys: "Control+L" })).toEqual([]);
		expect(approvedKeyboardKeys({ keys: ["ArrowLeft", "Delete"] })).toEqual([]);
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

  it("resolves a stage execution route from about:blank before target lookup", () => {
    expect(stageExecutionTargetURL({
      id: "stage-login", order: 1, node_id: "login", url: "https://app.example.com/login", entry_route: "/login",
      target_contract: { semantic_id: "login", destructive: false }, interactions: [{ kind: "fill", non_destructive: true }],
    }, "about:blank")).toBe("https://app.example.com/login");
  });

  it("uses the approved absolute stage URL as the base for a relative entry route", () => {
    expect(stageExecutionTargetURL({
      id: "stage-project", order: 2, node_id: "project", entry_route: "/dashboard",
      url: "https://app.example.com/dashboard", target_contract: { semantic_id: "project", destructive: false },
      interactions: [{ kind: "click", non_destructive: true }],
    }, "about:blank")).toBe("https://app.example.com/dashboard");
  });

  it("keeps disallowed stage targets subject to the existing policy", () => {
    const target = stageExecutionTargetURL({
      id: "stage-foreign", order: 1, node_id: "foreign", url: "https://evil.example/login",
      target_contract: { semantic_id: "foreign", destructive: false }, interactions: [{ kind: "click", non_destructive: true }],
    }, "about:blank");
    expect(urlPolicyError(target!, session, false)).toContain("domain_not_allowed");
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

  it("accepts a localized name only for one exact evidence-bound selector and keeps safety checks", async () => {
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
      semantic_id: "start_build",
      allowed_roles: ["button"],
      allowed_names: ["Build"],
      forbidden_names: ["Delete", "删除"],
      destructive: false,
    };
    const exactEvidenceSelector = { kind: "css", value: `[data-testid="button-create-project"]` };

    await expect(resolveUniqueVisibleEvidenceBoundTarget(locator(1, "button", "构建!"), "interaction_css", contract, exactEvidenceSelector, "click")).resolves.toBeUndefined();
    await expect(resolveUniqueVisibleEvidenceBoundTarget(locator(1, "button", "构建!"), "interaction_css", contract, exactEvidenceSelector, "click", { allowSelectorIdentityOnly: true })).resolves.toBeDefined();
    await expect(resolveUniqueVisibleEvidenceBoundTarget(locator(2, "button", "构建!"), "interaction_css", contract, exactEvidenceSelector, "click", { allowSelectorIdentityOnly: true })).resolves.toBeUndefined();
    await expect(resolveUniqueVisibleEvidenceBoundTarget(locator(1, "link", "构建!"), "interaction_css", contract, exactEvidenceSelector, "click", { allowSelectorIdentityOnly: true })).resolves.toBeUndefined();
    await expect(resolveUniqueVisibleEvidenceBoundTarget(locator(1, "button", "删除项目"), "interaction_css", contract, exactEvidenceSelector, "click", { allowSelectorIdentityOnly: true })).rejects.toThrow("browser_agent_forbidden_target_name");
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

  it("records every evidence-bound resolution outcome without selector or page text", async () => {
    const locator = (count: number, role: string, name: string, visible = true) => ({
      count: async () => count,
      first: () => ({
        isVisible: async () => visible,
        evaluate: async () => ({ role, name }),
      }),
    });
    const contract = {
      semantic_id: "project_idea_input",
      allowed_roles: ["textbox"],
      allowed_names: ["Project idea input"],
      forbidden_names: ["Delete"],
      destructive: false,
    };
    const cases = [
      { target: locator(0, "textbox", "Project idea input"), outcome: "no_candidates" },
      { target: locator(2, "textbox", "Project idea input"), outcome: "ambiguous" },
      { target: locator(1, "textbox", "Project idea input", false), outcome: "not_visible" },
      { target: locator(1, "button", "Project idea input"), outcome: "role_mismatch" },
      { target: locator(1, "textbox", "Different private page text"), outcome: "name_mismatch" },
      { target: locator(1, "textbox", "Project idea input"), outcome: "resolved" },
    ] as const;

    for (const entry of cases) {
      const attempts: BrowserTargetResolutionAttempt[] = [];
      await resolveUniqueVisibleEvidenceBoundTarget(entry.target, "approved_evidence_css", contract, candidate, "click", undefined, attempts);
      expect(attempts).toHaveLength(1);
      expect(attempts[0]).toMatchObject({
        strategy: "approved_evidence_css",
        evidence_bound: true,
        outcome: entry.outcome,
      });
      const audit = JSON.stringify(attempts);
      expect(audit).not.toContain(candidate.value);
      expect(audit).not.toContain("Project idea input");
      expect(audit).not.toContain("Different private page text");
    }

    const forbiddenAttempts: BrowserTargetResolutionAttempt[] = [];
    await expect(resolveUniqueVisibleEvidenceBoundTarget(
      locator(1, "textbox", "Delete project"),
      "approved_evidence_css",
      contract,
      candidate,
      "click",
      undefined,
      forbiddenAttempts,
    )).rejects.toThrow("browser_agent_forbidden_target_name");
    expect(forbiddenAttempts).toEqual([expect.objectContaining({ outcome: "forbidden_name", evidence_bound: true })]);
    expect(JSON.stringify(forbiddenAttempts)).not.toContain("Delete project");
  });

  it("audits role/name, testid, and exact App CSS resolution paths in order", async () => {
    const absent = {
      count: async () => 0,
      first: () => ({ isVisible: async () => false }),
    };
    const live = (role: string, name: string) => ({
      count: async () => 1,
      first: () => ({ isVisible: async () => true, evaluate: async () => ({ role, name }) }),
    });
    const targetByTestID = live("button", "New Project");
    const page = {
      getByRole: () => absent,
      getByTestId: (value: string) => value === "button-new-project" ? targetByTestID : absent,
      getByLabel: () => absent,
      getByText: () => absent,
      locator: () => absent,
    };
    const attempts: BrowserTargetResolutionAttempt[] = [];
    const stage = {
      id: "stage_new_project",
      order: 1,
      node_id: "new_project",
      target_contract: {
        semantic_id: "new_project",
        allowed_roles: ["button"],
        allowed_names: ["New Project"],
        destructive: false,
      },
      components: [{ component_ref: "new-project", test_id: "button-new-project" }],
      interactions: [{ kind: "click", target: { selector: "[data-testid='button-new-project']" }, non_destructive: true }],
    };

    const resolved = await resolveTarget(page, stage, stage.interactions[0], false, attempts);
    expect(resolved.strategy).toBe("component_testid");
    expect(attempts.map((attempt) => [attempt.strategy, attempt.outcome])).toEqual([
      ["role:button+approved_name", "no_candidates"],
      ["component_testid", "resolved"],
    ]);
    expect(JSON.stringify(attempts)).not.toContain("button-new-project");
    expect(JSON.stringify(attempts)).not.toContain("New Project");
  });

  it("fails closed on an ambiguous exact App CSS target and records the bounded attempt", async () => {
    const ambiguous = {
      count: async () => 2,
      first: () => ({ isVisible: async () => true, evaluate: async () => ({ role: "button", name: "Build" }) }),
    };
    const absent = { count: async () => 0, first: () => ({ isVisible: async () => false }) };
    const page = {
      getByRole: () => absent,
      getByTestId: () => absent,
      getByLabel: () => absent,
      getByText: () => absent,
      locator: (selector: string) => selector === "[data-testid='build']" ? ambiguous : absent,
    };
    const attempts: BrowserTargetResolutionAttempt[] = [];
    const stage = {
      id: "stage_build",
      order: 1,
      node_id: "build",
      target_contract: {
        semantic_id: "build",
        allowed_roles: ["button"],
        allowed_names: ["Build"],
        destructive: false,
        evidence_refs: [{ id: "evidence-build" }],
      },
      evidence_bound_selector_alternatives: [{ kind: "css", value: "[data-testid='build']" }],
      interactions: [{ kind: "click", target: { selector: "[data-testid='build']" }, non_destructive: true }],
    };

    await expect(resolveTarget(page, stage, stage.interactions[0], false, attempts)).rejects.toThrow("browser_agent_target_not_resolved");
    expect(attempts).toContainEqual(expect.objectContaining({
      strategy: "interaction_css",
      candidate_count: 2,
      evidence_bound: true,
      outcome: "ambiguous",
    }));
    expect(JSON.stringify(attempts)).not.toContain("[data-testid='build']");
  });
});

describe("browser agent required validations", () => {
	it("requires an execution-recorded visual change for keyboard playability", async () => {
		const stage = {
			id: "stage_play", order: 8, node_id: "verify_playable_controls", stage_kind: "final_observe",
			target_contract: { semantic_id: "tetris_keyboard", destructive: false },
			interactions: [{ kind: "press", non_destructive: true }],
			validations: [{ id: "changed", kind: "page_changed", expected: true, required: true }],
		};
		expect(await evaluateRequiredValidations({}, stage, new Map([[stage.node_id, true]]))).toEqual([{
			kind: "required_page_changed:changed", passed: true, actual: "visual_changed_after_approved_keys",
		}]);
		expect((await evaluateRequiredValidations({}, stage, new Map()))[0]?.passed).toBe(false);
	});

  it("allows a bounded long poll only for a non-destructive final completion observation", async () => {
    const finalStage = {
      id: "stage_final", order: 7, node_id: "final_observe", stage_kind: "final_observe",
      target_contract: { semantic_id: "build_complete", destructive: false },
      interactions: [{ kind: "inspect", non_destructive: true }],
    };
    const validation = { id: "build_complete", kind: "element_visible", target: { test_id: "build-result-card" }, required: true, timeout_ms: 1_200_000 };
    expect(validationTimeoutMilliseconds(finalStage, validation)).toBe(1_200_000);
    expect(validationTimeoutMilliseconds({ ...finalStage, stage_kind: "business_submit", interactions: [{ kind: "click", non_destructive: true }] }, validation)).toBe(30_000);
    expect(validationTimeoutMilliseconds(finalStage, { ...validation, timeout_ms: 9_999_999 })).toBe(1_200_000);

    let waitOptions: unknown;
    const assertions = await evaluateRequiredValidations({
      getByTestId: () => ({ first: () => ({ waitFor: async (options: unknown) => { waitOptions = options; } }) }),
    }, { ...finalStage, validations: [validation] });
    expect(waitOptions).toEqual({ state: "visible", timeout: 1_200_000 });
    expect(assertions).toEqual([{ kind: "required_element_visible:build_complete", passed: true, actual: "visible" }]);
  });

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
