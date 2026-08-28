import { launchOptionsWithProxy } from "./playwright-proxy.js";
import { configuredBrowserExecutable } from "./browser-executable.js";
import { createHash } from "node:crypto";

type VerifyInteractionRequest = {
  product_url?: string;
  timeout_ms?: number;
  headless?: boolean;
  allowed_domains?: string[];
  forbidden_path_prefixes?: string[];
  candidates?: InteractionCandidate[];
  intent_goals?: InteractionGoal[];
  safe_state_transitions?: InteractionCandidate[];
  demo_username?: string;
  demo_password?: string;
};

type InteractionGoal = {
  id?: string;
  label?: string;
  kind?: string;
  keywords?: string[];
  required?: boolean;
  business?: boolean;
  input_value?: string;
};

type InteractionCandidate = {
  id?: string;
  intent_goal_id?: string;
  label?: string;
  kind?: string;
  selector?: string;
  url?: string;
};

type VerificationMode = "playwright_readonly_scan" | "playwright_safe_state_scan";

type VerifyInteractionResult = {
  ok: boolean;
  verification_mode: VerificationMode;
  browser_scan_id: string;
  source_url?: string;
  results: VerifiedInteractionCandidate[];
  diagnostics?: InteractionVerifierDiagnostics;
  error?: { code: string; message: string };
};

type InteractionVerifierDiagnostics = {
  login_attempted?: boolean;
  login_status?: string;
  final_url?: string;
  page_title?: string;
  candidate_count?: number;
  verified_candidate_count?: number;
  discovered_business_control_count?: number;
  login_transitions?: string[];
  login_evidence?: VerifiedInteractionCandidate[];
  safe_state_transitions?: string[];
};

type VerifiedInteractionCandidate = InteractionCandidate & {
  status: "verified" | "not_visible" | "not_enabled" | "not_editable" | "selector_missing" | "error";
  visible?: boolean;
  enabled?: boolean;
  editable?: boolean;
  page_url?: string;
  page_title?: string;
  message?: string;
  verified_at?: string;
  evidence_id?: string;
  source_kind?: "page_scan";
  source_digest?: string;
  observed_role?: string;
  observed_accessible_name?: string;
  observed_url?: string;
  observed_route_template?: string;
  observed_page_role?: string;
  observed_form_role?: string;
  evidence_digest_sha256?: string;
  observed_at?: string;
};

const minimumEvidenceSettleMS = 1000;

export async function verifyInteractions(request: VerifyInteractionRequest): Promise<VerifyInteractionResult> {
  const productURL = request.product_url || request.candidates?.find((candidate) => candidate.url)?.url;
  const scanID = `browser_scan_${hashText(`${productURL || "missing"}|${Date.now()}`)}`;
  const results: VerifiedInteractionCandidate[] = [];
  if (!productURL) {
    return {
      ok: false,
      verification_mode: "playwright_readonly_scan",
      browser_scan_id: scanID,
      results,
      error: { code: "product_url_missing", message: "product_url is required for readonly interaction verification." },
    };
  }
  if (isURLForbiddenByScope(productURL, request)) {
    return {
      ok: false,
      verification_mode: "playwright_readonly_scan",
      browser_scan_id: scanID,
      source_url: productURL,
      results,
      error: { code: "control_plane_scope_violation", message: "product_url points to a forbidden control-plane path." },
    };
  }

  const playwright = await import("playwright");
  const launchOptions: Record<string, unknown> = { headless: request.headless ?? true };
  const executablePath = configuredBrowserExecutable();
  if (executablePath) launchOptions.executablePath = executablePath;
  const browser = await playwright.chromium.launch(launchOptionsWithProxy(launchOptions));
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });
  const page = await context.newPage();
  const timeout = request.timeout_ms || 20000;
  const diagnostics: InteractionVerifierDiagnostics = {
    candidate_count: request.candidates?.length || 0,
  };
  try {
    await page.goto(productURL, { waitUntil: "domcontentloaded", timeout });
    await waitForPageEvidenceReady(page, Math.min(timeout, 8000));
    const loginAttempt = await attemptLoginIfCredentialsProvided(page, {
      ...(request.demo_username ? { username: request.demo_username } : {}),
      ...(request.demo_password ? { password: request.demo_password } : {}),
      timeout,
      scanID,
      transitions: diagnostics.login_transitions = [],
    });
    diagnostics.login_attempted = Boolean(request.demo_username && request.demo_password);
    diagnostics.login_status = loginAttempt.status;
    diagnostics.login_evidence = loginAttempt.evidence;
    let pageTitle = await page.title().catch(() => "");
    let currentURL = page.url();
    let sourceDigest = await pageEvidenceDigest(page, currentURL);
    diagnostics.final_url = currentURL;
    diagnostics.page_title = pageTitle;
    for (const candidate of request.candidates || []) {
      const selector = candidate.selector?.trim();
      if (!selector) {
        results.push({ ...candidate, status: "selector_missing", page_url: currentURL, page_title: pageTitle, message: "selector is empty" });
        continue;
      }
      try {
        const locator = page.locator(selector).first();
        const visible = await locator.isVisible({ timeout: 2500 }).catch(() => false);
        if (!visible) {
          results.push({ ...candidate, status: "not_visible", visible, page_url: currentURL, page_title: pageTitle });
          continue;
        }
        const enabled = await locator.isEnabled({ timeout: 1000 }).catch(() => false);
        const editable = await locator.isEditable({ timeout: 1000 }).catch(() => false);
        const action = normalizeAction(candidate.kind);
        if ((action === "click" || action === "select" || action === "upload") && !enabled) {
          results.push({ ...candidate, status: "not_enabled", visible, enabled, editable, page_url: currentURL, page_title: pageTitle });
          continue;
        }
        if (action === "fill" && !editable) {
          results.push({ ...candidate, status: "not_editable", visible, enabled, editable, page_url: currentURL, page_title: pageTitle });
          continue;
        }
        results.push({
          ...candidate,
          status: "verified",
          visible,
          enabled,
          editable,
          page_url: currentURL,
          page_title: pageTitle,
          verified_at: new Date().toISOString(),
          ...await selectorProvenance(locator, candidate, currentURL, scanID, sourceDigest),
        });
      } catch (error) {
        results.push({
          ...candidate,
          status: "error",
          page_url: currentURL,
          page_title: pageTitle,
          message: error instanceof Error ? error.message : String(error),
        });
      }
    }
    const safeTransitions = await applySafeStateTransitions(page, request, timeout);
    diagnostics.safe_state_transitions = safeTransitions;
    const discoveredTransition = safeTransitions.some((value) => value.startsWith("applied:") && !/(login|auth|session)/i.test(value))
      ? ""
      : await applyDiscoveredGoalTransition(page, request.intent_goals || [], request, timeout);
    if (discoveredTransition) safeTransitions.push(discoveredTransition);
    const goalInputTransition = await applyDiscoveredGoalInput(page, request.intent_goals || [], request, timeout);
    if (goalInputTransition) safeTransitions.push(goalInputTransition);
    if (safeTransitions.some((value) => value.startsWith("applied:"))) {
      currentURL = page.url();
      pageTitle = await page.title().catch(() => "");
      diagnostics.final_url = currentURL;
      diagnostics.page_title = pageTitle;
	  sourceDigest = await pageEvidenceDigest(page, currentURL);
      for (let index = 0; index < results.length; index += 1) {
		const current = results[index];
		if (!current || current.status === "verified") continue;
		const rescanned = await verifyCandidateOnPage(page, current, currentURL, pageTitle, scanID, sourceDigest);
        if (rescanned.status === "verified") results[index] = rescanned;
      }
    }
    const discovered = await discoverBusinessActionsAcrossSafePages(page, request.intent_goals || [], request, currentURL, pageTitle, scanID);
    diagnostics.discovered_business_control_count = discovered.length;
    const seenSelectors = new Set(results.map((result) => normalizeSelectorText(result.selector)));
    for (const item of discovered) {
      if (!item.selector || seenSelectors.has(normalizeSelectorText(item.selector))) {
        continue;
      }
      seenSelectors.add(normalizeSelectorText(item.selector));
      results.push(item);
    }
    diagnostics.verified_candidate_count = results.filter((result) => result.status === "verified").length;
    const verificationMode: VerificationMode = safeTransitions.some((value) => value.startsWith("applied:")) ? "playwright_safe_state_scan" : "playwright_readonly_scan";
    return { ok: true, verification_mode: verificationMode, browser_scan_id: scanID, source_url: currentURL, results, diagnostics };
  } catch (error) {
    return {
      ok: false,
      verification_mode: "playwright_readonly_scan",
      browser_scan_id: scanID,
      source_url: productURL,
      results,
      diagnostics,
      error: { code: "page_unreachable", message: error instanceof Error ? error.message : String(error) },
    };
  } finally {
    await context.close().catch(() => undefined);
    await browser.close().catch(() => undefined);
  }
}

async function verifyCandidateOnPage(page: any, candidate: InteractionCandidate, pageURL: string, pageTitle: string, scanID: string, sourceDigest: string): Promise<VerifiedInteractionCandidate> {
  const selector = candidate.selector?.trim();
  if (!selector) return { ...candidate, status: "selector_missing", page_url: pageURL, page_title: pageTitle, message: "selector is empty" };
  try {
    const locator = page.locator(selector).first();
    const visible = await locator.isVisible({ timeout: 2500 }).catch(() => false);
    if (!visible) return { ...candidate, status: "not_visible", visible, page_url: pageURL, page_title: pageTitle };
    const enabled = await locator.isEnabled({ timeout: 1000 }).catch(() => false);
    const editable = await locator.isEditable({ timeout: 1000 }).catch(() => false);
    const action = normalizeAction(candidate.kind);
    if ((action === "click" || action === "select" || action === "upload") && !enabled) {
      return { ...candidate, status: "not_enabled", visible, enabled, editable, page_url: pageURL, page_title: pageTitle };
    }
    if (action === "fill" && !editable) {
      return { ...candidate, status: "not_editable", visible, enabled, editable, page_url: pageURL, page_title: pageTitle };
    }
    return { ...candidate, status: "verified", visible, enabled, editable, page_url: pageURL, page_title: pageTitle, verified_at: new Date().toISOString(), ...await selectorProvenance(locator, candidate, pageURL, scanID, sourceDigest) };
  } catch (error) {
    return { ...candidate, status: "error", page_url: pageURL, page_title: pageTitle, message: error instanceof Error ? error.message : String(error) };
  }
}

async function applySafeStateTransitions(page: any, request: VerifyInteractionRequest, timeout: number): Promise<string[]> {
  const transitions: string[] = [];
  for (const transition of (request.safe_state_transitions || []).slice(0, 1)) {
    const action = normalizeAction(transition.kind);
    const semantic = `${transition.id || ""} ${transition.label || ""} ${transition.selector || ""}`;
    if (action !== "click" || !transition.selector || looksLikeDestructiveControl(semantic) || looksLikeControlPlaneSignal(semantic)) {
      transitions.push(`rejected:${transition.id || "unknown"}`);
      continue;
    }
    const currentURL = page.url();
    if (isURLForbiddenByScope(currentURL, request) || isURLForbiddenByScope(transition.url, request)) {
      transitions.push(`scope_rejected:${transition.id || "unknown"}`);
      continue;
    }
    const locator = page.locator(transition.selector).first();
    const visible = await locator.isVisible({ timeout: 2500 }).catch(() => false);
    const enabled = visible && await locator.isEnabled({ timeout: 1000 }).catch(() => false);
    if (!visible || !enabled) {
      transitions.push(`unavailable:${transition.id || "unknown"}`);
      continue;
    }
    const clicked = await locator.click({ timeout: Math.min(timeout, 4000) }).then(() => true).catch(() => false);
    if (!clicked) {
      transitions.push(`failed:${transition.id || "unknown"}`);
      continue;
    }
    await waitForPageEvidenceReady(page, Math.min(timeout, 5000));
    if (isURLForbiddenByScope(page.url(), request)) {
      transitions.push(`result_scope_rejected:${transition.id || "unknown"}`);
      await page.goto(currentURL, { waitUntil: "domcontentloaded", timeout: Math.min(timeout, 6000) }).catch(() => undefined);
      await waitForPageEvidenceReady(page, Math.min(timeout, 5000));
      continue;
    }
    transitions.push(`applied:${transition.id || "unknown"}`);
  }
  return transitions;
}

async function applyDiscoveredGoalTransition(
  page: any,
  goals: InteractionGoal[],
  request: VerifyInteractionRequest,
  timeout: number,
): Promise<string> {
  const projectCreationRequested = goals.some((candidate) => {
    if (!candidate.required || !candidate.business || normalizeAction(candidate.kind) !== "click") return false;
    const semantic = normalizeSelectorText(`${candidate.label || ""} ${(candidate.keywords || []).join(" ")}`);
    return isNewProjectSemantic(semantic);
  });
  const goal = goals.find((candidate) => {
    if (!candidate.required || !candidate.business || normalizeAction(candidate.kind) !== "click") return false;
    if (!projectCreationRequested) return true;
    const semantic = normalizeSelectorText(`${candidate.label || ""} ${(candidate.keywords || []).join(" ")}`);
    return isNewProjectSemantic(semantic);
  });
  if (!goal || isURLForbiddenByScope(page.url(), request)) return "";

  const creationInputVisible = projectCreationRequested && await page.locator("input, textarea").evaluateAll((elements: any[]) => elements.some((element) => {
    const html = element as any;
    const rect = html.getBoundingClientRect();
    const style = (globalThis as any).getComputedStyle(html);
    const semantic = String([
      element.getAttribute("data-testid"), element.getAttribute("data-test"), element.getAttribute("data-cy"),
      element.getAttribute("name"), element.getAttribute("aria-label"), element.getAttribute("placeholder"),
    ].filter(Boolean).join(" ")).toLowerCase();
    return rect.width > 0 && rect.height > 0 && style.visibility !== "hidden" && style.display !== "none" &&
      /(project[-_ ]?(idea|name|prompt)|(idea|name|prompt)[-_ ]?project|项目.{0,4}(需求|名称|描述))/.test(semantic);
  })).catch(() => false);
  if (creationInputVisible) return "already_visible:new_project_creation";

  const controls = await page.locator("button, [role='button']").evaluateAll((elements: any[]) => elements.slice(0, 120).map((element) => {
    const html = element as any;
    const rect = html.getBoundingClientRect();
    const style = (globalThis as any).getComputedStyle(html);
    return {
      text: String(html.innerText || html.textContent || "").trim().replace(/\s+/g, " ").slice(0, 120),
      testid: element.getAttribute("data-testid") || element.getAttribute("data-test") || element.getAttribute("data-cy") || "",
      aria: element.getAttribute("aria-label") || "",
      id: element.getAttribute("id") || "",
      visible: rect.width > 0 && rect.height > 0 && style.visibility !== "hidden" && style.display !== "none",
      disabled: Boolean((html as any).disabled) || element.getAttribute("aria-disabled") === "true",
    };
  })).catch(() => []);

  const candidates = controls
    .filter((control: any) => control.visible && !control.disabled)
    .map((control: any) => {
      const semantic = normalizeSelectorText(`${control.text} ${control.aria} ${control.testid} ${control.id}`);
      let selector = "";
      if (control.testid) selector = `[data-testid="${escapeCSSString(control.testid)}"]`;
      else if (control.aria) selector = `[aria-label="${escapeCSSString(control.aria)}"]`;
      else if (control.id && /^[A-Za-z][\w-]*$/.test(control.id)) selector = `#${control.id}`;
      const requested = projectCreationRequested ? isNewProjectSemantic(semantic) : businessControlScore(semantic, selector, [goal], "click") > 0;
      const staleEntity = projectCreationRequested &&
        /(?:card|row|tile|list|menu)[-_ ]*(?:project|entity|item)|(?:project|entity|item)[-_ ]*(?:card|row|tile|list|menu|name)/i.test(semantic) &&
        !isNewProjectSemantic(semantic);
      const score = businessControlScore(semantic, selector, [goal], "click") +
        (control.testid ? 100 : control.aria ? 70 : control.id ? 50 : 0) +
        (/button-new-project|new-project-button|create-project-button/i.test(semantic) ? 80 : 0);
      return { selector, semantic, requested, staleEntity, score };
    })
    .filter((candidate: any) => candidate.selector && candidate.requested && !candidate.staleEntity && candidate.score > 0 && !looksLikeDestructiveControl(candidate.semantic) && !looksLikeControlPlaneSignal(candidate.semantic))
    .sort((left: any, right: any) => right.score - left.score);
  if (candidates.length === 0 || (candidates.length > 1 && candidates[0].score === candidates[1].score)) return "";

  const clicked = await page.locator(candidates[0].selector).first().click({ timeout: Math.min(timeout, 4000) }).then(() => true).catch(() => false);
  if (!clicked) return `discovered_failed:${goal.id || "business_transition"}`;
  await waitForPageEvidenceReady(page, Math.min(timeout, 5000));
  if (isURLForbiddenByScope(page.url(), request)) return `discovered_result_scope_rejected:${goal.id || "business_transition"}`;
  return `applied:discovered_${goal.id || "business_transition"}`;
}

async function applyDiscoveredGoalInput(
  page: any,
  goals: InteractionGoal[],
  request: VerifyInteractionRequest,
  timeout: number,
): Promise<string> {
  const projectCreationRequested = goals.some((candidate) => {
    const semantic = normalizeSelectorText(`${candidate.label || ""} ${(candidate.keywords || []).join(" ")}`);
    return candidate.required && candidate.business && normalizeAction(candidate.kind) === "click" && isNewProjectSemantic(semantic);
  });
  const goal = goals.find((candidate) => {
    const semantic = normalizeSelectorText(`${candidate.label || ""} ${(candidate.keywords || []).join(" ")}`);
    return candidate.required && candidate.business && normalizeAction(candidate.kind) === "fill" &&
      typeof candidate.input_value === "string" && candidate.input_value.trim().length > 0 && candidate.input_value.length <= 512 &&
      (!projectCreationRequested || isProjectIdeaSemantic(semantic)) &&
      !/(password|passwd|secret|token|api key|密码|口令|密钥|令牌)/i.test(candidate.input_value);
  });
  if (!goal || isURLForbiddenByScope(page.url(), request)) return "";

  const controls = await page.locator("input, textarea").evaluateAll((elements: any[]) => elements.slice(0, 80).map((element) => {
    const html = element as any;
    const rect = html.getBoundingClientRect();
    const style = (globalThis as any).getComputedStyle(html);
    const semantic = String([
      element.getAttribute("data-testid"), element.getAttribute("data-test"), element.getAttribute("data-cy"),
      element.getAttribute("name"), element.getAttribute("aria-label"), element.getAttribute("placeholder"),
    ].filter(Boolean).join(" ")).toLowerCase();
    const testid = element.getAttribute("data-testid") || element.getAttribute("data-test") || element.getAttribute("data-cy") || "";
    const name = element.getAttribute("name") || "";
    return {
      testid,
      name,
      tag: String(html.tagName || "input").toLowerCase(),
      semantic,
      eligible: rect.width > 0 && rect.height > 0 && style.visibility !== "hidden" && style.display !== "none" &&
        !html.disabled && !html.readOnly && !["password", "hidden", "file"].includes(String(html.type || "").toLowerCase()),
      score: 0,
    };
  })).catch(() => []);
  const resolvedControls = controls.map((control: any) => ({
    ...control,
    selector: control.testid ? `[data-testid="${escapeCSSString(control.testid)}"]` : control.name ? `${control.tag}[name="${escapeCSSString(control.name)}"]` : "",
  })).map((control: any) => ({ ...control, score: businessControlScore(control.semantic, control.selector, [goal], "fill") + (control.testid ? 100 : 40) }));
  const candidates = resolvedControls.filter((control: any) => control.eligible && control.selector && control.score > 40).sort((left: any, right: any) => right.score - left.score);
  if (candidates.length === 0) {
    return `discovered_unavailable:${goal.id || "business_input"}`;
  }
  if (candidates.length > 1 && candidates[0].score === candidates[1].score) return `discovered_ambiguous:${goal.id || "business_input"}`;
  const filled = await page.locator(candidates[0].selector).first().fill(goal.input_value!, { timeout: Math.min(timeout, 4000) }).then(() => true).catch(() => false);
  if (!filled) return `discovered_failed:${goal.id || "business_input"}`;
  await page.waitForTimeout(250);
  return `applied:discovered_${goal.id || "business_input"}`;
}

async function discoverBusinessActions(
  page: any,
  goals: InteractionGoal[],
  request: VerifyInteractionRequest,
  pageURL: string,
  pageTitle: string,
  scanID: string,
): Promise<VerifiedInteractionCandidate[]> {
  const visibleControls = await page.locator("button, a[href], input, textarea, select, [role='button'], [role='link'], [data-testid], [data-test], [data-cy], [aria-label]").evaluateAll((elements: any[]) => {
    return elements.slice(0, 250).map((element, index) => {
      const html = element as any;
      const rect = html.getBoundingClientRect();
      const tag = html.tagName.toLowerCase();
      const input = element as any;
      const style = (globalThis as any).getComputedStyle(html);
      const text = (html.innerText || html.textContent || "").trim().replace(/\s+/g, " ").slice(0, 120);
      const attrs = {
        testid: element.getAttribute("data-testid") || element.getAttribute("data-test") || element.getAttribute("data-cy") || "",
        id: element.getAttribute("id") || "",
        aria: element.getAttribute("aria-label") || "",
        name: element.getAttribute("name") || "",
        placeholder: element.getAttribute("placeholder") || "",
        href: element.getAttribute("href") || "",
        role: element.getAttribute("role") || "",
        type: element.getAttribute("type") || "",
        title: element.getAttribute("title") || "",
      };
      return {
        index,
        tag,
        text,
        attrs,
        visible: rect.width > 0 && rect.height > 0 && style.visibility !== "hidden" && style.display !== "none",
        disabled: Boolean((input as any).disabled) || element.getAttribute("aria-disabled") === "true",
      };
    });
  }).catch(() => []);

  const scored = visibleControls
    .filter((control: any) => control.visible && !control.disabled)
    .filter((control: any) => isSemanticallyInteractiveControl(control))
    .filter((control: any) => !isURLForbiddenByScope(control.attrs?.href, request))
    .map((control: any) => {
      const label = accessibleControlName(control);
      const selector = selectorForControl(control, label);
      const kind = actionKindForControl(control);
      const score = businessControlScore(label, selector, goals, kind);
      const goal = bestGoalForControl(label, selector, goals, kind);
      return { control, label, selector, kind, score, goal };
    })
    .filter((item: any) => item.selector && item.label && item.goal && item.score >= 35 && !looksLikeChromeControl(`${item.label} ${item.selector}`) && !looksLikeControlPlaneSignal(`${item.label} ${item.selector}`))
    .sort((a: any, b: any) => b.score - a.score)
    .slice(0, 8);

  const now = new Date().toISOString();
  const sourceDigest = await pageEvidenceDigest(page, pageURL);
  const actions = scored.map((item: any, index: number) => {
    const id = `browser_discovered_${index + 1}_${hashText(`${item.selector}|${item.label}`)}`;
    const result: VerifiedInteractionCandidate = {
      id,
      label: item.label,
      kind: item.kind,
      selector: item.selector,
      url: pageURL,
      status: "verified",
      visible: true,
      enabled: true,
      editable: item.kind === "fill",
      page_url: pageURL,
      page_title: pageTitle,
      message: "login-aware readonly DOM scan discovered a visible business control",
      verified_at: now,
      evidence_id: selectorEvidenceID(scanID, id, item.selector),
      source_kind: "page_scan",
      source_digest: sourceDigest,
      observed_role: observedControlRole(item.control, item.kind),
      observed_accessible_name: item.label,
      observed_at: now,
    };
    if (item.goal?.id) {
      result.intent_goal_id = item.goal.id;
    }
    return result;
  });
  const resultStates = await discoverObservedResultStates(page, goals, pageURL, pageTitle, scanID, sourceDigest, now);
  return [...actions, ...resultStates];
}

async function discoverObservedResultStates(
  page: any,
  goals: InteractionGoal[],
  pageURL: string,
  pageTitle: string,
  scanID: string,
  sourceDigest: string,
  observedAt: string,
): Promise<VerifiedInteractionCandidate[]> {
  const projectCreationRequested = goals.some((candidate) => {
    const semantic = normalizeSelectorText(`${candidate.label || ""} ${(candidate.keywords || []).join(" ")}`);
    return candidate.required && candidate.business && normalizeAction(candidate.kind) === "click" && isNewProjectSemantic(semantic);
  });
  const eligibleGoals = goals.filter((candidate) => {
    const semantic = normalizeSelectorText(`${candidate.label || ""} ${(candidate.keywords || []).join(" ")}`);
    return candidate.required && candidate.business && normalizeAction(candidate.kind) === "click" && (!projectCreationRequested || isNewProjectSemantic(semantic));
  });
  if (eligibleGoals.length === 0) return [];
  const states = await page.locator("dialog, [role='dialog'], [role='region'][aria-label], [data-testid*='dialog' i], [data-testid*='modal' i]").evaluateAll((elements: any[]) => elements.slice(0, 40).map((element) => {
    const html = element as any;
    const rect = html.getBoundingClientRect();
    const style = (globalThis as any).getComputedStyle(html);
    return {
      text: String(html.innerText || html.textContent || "").trim().replace(/\s+/g, " ").slice(0, 160),
      testid: element.getAttribute("data-testid") || element.getAttribute("data-test") || element.getAttribute("data-cy") || "",
      aria: element.getAttribute("aria-label") || "",
      id: element.getAttribute("id") || "",
      role: element.getAttribute("role") || "dialog",
      visible: rect.width > 0 && rect.height > 0 && style.visibility !== "hidden" && style.display !== "none",
    };
  })).catch(() => []);
  return states.flatMap((state: any, index: number) => {
    const semantic = normalizeSelectorText(`${state.text} ${state.testid} ${state.aria} ${state.id}`);
    if (!state.visible || !semantic) return [];
    const goal = bestGoalForControl(semantic, "", eligibleGoals, "click") || (eligibleGoals.length === 1 && state.role === "dialog" ? eligibleGoals[0] : undefined);
    if (!goal) return [];
    const selector = state.testid ? `[data-testid="${escapeCSSString(state.testid)}"]` : state.aria ? `[aria-label="${escapeCSSString(state.aria)}"]` : state.id && /^[A-Za-z][\w-]*$/.test(state.id) ? `#${state.id}` : "";
    if (!selector) return [];
    const id = `browser_state_${index + 1}_${hashText(`${selector}|${state.text}`)}`;
    return [{
      id,
      intent_goal_id: goal.id,
      label: state.aria || state.text || "Observed result surface",
      kind: "inspect",
      selector,
      url: pageURL,
      status: "verified" as const,
      visible: true,
      enabled: true,
      editable: false,
      page_url: pageURL,
      page_title: pageTitle,
      message: "safe-state scan verified the visible result container independently from action controls",
      verified_at: observedAt,
      evidence_id: selectorEvidenceID(scanID, id, selector),
      source_kind: "page_scan" as const,
      source_digest: sourceDigest,
      observed_role: state.role || "dialog",
      observed_accessible_name: state.aria || state.text || "Observed result surface",
      observed_url: pageURL,
      observed_route_template: new URL(pageURL).pathname,
      observed_page_role: "product",
      observed_form_role: state.role || "generic",
      evidence_digest_sha256: sourceDigest,
      observed_at: observedAt,
    }];
  });
}

function isSemanticallyInteractiveControl(control: any): boolean {
  const tag = String(control?.tag || "").toLowerCase();
  const role = String(control?.attrs?.role || "").toLowerCase();
  return ["button", "a", "input", "textarea", "select"].includes(tag) || ["button", "link", "textbox", "combobox", "checkbox", "radio"].includes(role);
}

async function discoverBusinessActionsAcrossSafePages(
  page: any,
  goals: InteractionGoal[],
  request: VerifyInteractionRequest,
  pageURL: string,
  pageTitle: string,
  scanID: string,
): Promise<VerifiedInteractionCandidate[]> {
  const out: VerifiedInteractionCandidate[] = [];
  const seen = new Set<string>();
  const add = (items: VerifiedInteractionCandidate[]) => {
    for (const item of items) {
      const key = normalizeSelectorText(`${item.selector}|${item.label}|${item.page_url}`);
      if (!key || seen.has(key)) continue;
      seen.add(key);
      out.push(item);
    }
  };
  add(await discoverBusinessActions(page, goals, request, pageURL, pageTitle, scanID));
  if (out.length >= 3) return out;

  const links = await safeExplorationLinks(page, goals, request);
  const originalURL = page.url();
  for (const link of links.slice(0, 4)) {
    if (out.length >= 6) break;
    await page.goto(link.href, { waitUntil: "domcontentloaded", timeout: Math.min(request.timeout_ms || 20000, 8000) }).catch(() => undefined);
    await waitForPageEvidenceReady(page, Math.min(request.timeout_ms || 20000, 5000));
    const title = await page.title().catch(() => "");
    add(await discoverBusinessActions(page, goals, request, page.url(), title, scanID));
  }
  if (page.url() !== originalURL) {
    await page.goto(originalURL, { waitUntil: "domcontentloaded", timeout: Math.min(request.timeout_ms || 20000, 6000) }).catch(() => undefined);
    await waitForPageEvidenceReady(page, Math.min(request.timeout_ms || 20000, 5000));
  }
  return out;
}

async function safeExplorationLinks(page: any, goals: InteractionGoal[], request: VerifyInteractionRequest): Promise<Array<{ href: string; label: string; score: number }>> {
  const links = await page.locator("a[href], [role='link'][href]").evaluateAll((elements: any[]) => {
    return elements.slice(0, 160).map((element) => {
      const html = element as any;
      const text = (html.innerText || html.textContent || "").trim().replace(/\s+/g, " ").slice(0, 120);
      return {
        href: html.href || element.getAttribute("href") || "",
        label: [
          text,
          element.getAttribute("aria-label") || "",
          element.getAttribute("title") || "",
          element.getAttribute("data-testid") || "",
        ].filter(Boolean).join(" "),
      };
    });
  }).catch(() => []);
  return links
    .map((link: any) => ({ ...link, score: safeLinkScore(link.label, link.href, goals) }))
    .filter((link: any) => link.href && link.score > 0 && !isURLForbiddenByScope(link.href, request))
    .sort((a: any, b: any) => b.score - a.score);
}

function accessibleControlName(control: any): string {
  return [
    control.text,
    control.attrs?.aria,
    control.attrs?.placeholder,
    control.attrs?.title,
  ].filter(Boolean).join(" ").trim().replace(/\s+/g, " ").slice(0, 140);
}

function observedControlRole(control: any, actionKind: string): string {
  const explicit = String(control.attrs?.role || "").trim().toLowerCase();
  if (explicit) return explicit;
  const tag = String(control.tag || "").toLowerCase();
  const type = String(control.attrs?.type || "").toLowerCase();
  if (tag === "a") return "link";
  if (tag === "button" || ["button", "submit", "reset"].includes(type)) return "button";
  if (tag === "select") return "combobox";
  if (tag === "textarea" || tag === "input" && !["checkbox", "radio", "file"].includes(type)) return "textbox";
  if (type === "checkbox") return "checkbox";
  if (type === "radio") return "radio";
  if (type === "file") return "button";
  return normalizeAction(actionKind) === "fill" ? "textbox" : "button";
}

async function selectorProvenance(locator: any, candidate: InteractionCandidate, pageURL: string, scanID: string, sourceDigest: string): Promise<Pick<VerifiedInteractionCandidate, "evidence_id" | "source_kind" | "source_digest" | "observed_role" | "observed_accessible_name" | "observed_url" | "observed_route_template" | "observed_page_role" | "observed_form_role" | "evidence_digest_sha256" | "observed_at">> {
  const semantics = await locator.evaluate((element: any) => {
    const tag = String(element.tagName || "").toLowerCase();
    const type = String(element.getAttribute?.("type") || "").toLowerCase();
    const explicitRole = String(element.getAttribute?.("role") || "").trim().toLowerCase();
    const labelledBy = String(element.getAttribute?.("aria-labelledby") || "").trim().split(/\s+/).filter(Boolean)
      .map((id: string) => element.ownerDocument?.getElementById(id)?.textContent || "").join(" ");
    const labels = Array.from(element.labels || []).map((label: any) => label.textContent || "").join(" ");
    const text = String(element.innerText || element.textContent || "");
    const name = [element.getAttribute?.("aria-label") || "", labelledBy, labels, text, element.getAttribute?.("placeholder") || "", element.getAttribute?.("title") || "", element.getAttribute?.("alt") || ""]
      .map((value) => String(value).trim().replace(/\s+/g, " ")).find(Boolean) || "";
    let role = explicitRole;
    if (!role && tag === "a") role = "link";
    if (!role && (tag === "button" || ["button", "submit", "reset"].includes(type))) role = "button";
    if (!role && tag === "select") role = "combobox";
    if (!role && (tag === "textarea" || tag === "input" && !["checkbox", "radio", "file"].includes(type))) role = "textbox";
    if (!role && type === "checkbox") role = "checkbox";
    if (!role && type === "radio") role = "radio";
    if (!role && type === "file") role = "button";
    const visiblePassword = (container: any) => Array.from(container?.querySelectorAll?.('input[type="password"]') || []).some((input: any) => {
      if (input.hidden || input.disabled || input.getAttribute?.("aria-hidden") === "true") return false;
      const style = input.ownerDocument?.defaultView?.getComputedStyle?.(input);
      if (style && (style.display === "none" || style.visibility === "hidden" || style.opacity === "0")) return false;
      const rect = input.getBoundingClientRect?.();
      return !rect || rect.width > 0 && rect.height > 0;
    });
    const form = element.closest?.("form");
    let authContainer = form && visiblePassword(form) ? form : null;
    for (let ancestor = element.parentElement, depth = 0; !authContainer && ancestor && ancestor !== element.ownerDocument?.body && depth < 10; ancestor = ancestor.parentElement, depth += 1) {
      if (visiblePassword(ancestor)) authContainer = ancestor;
    }
    const semanticContainer = authContainer || form;
    const formText = String([
      semanticContainer?.getAttribute?.("aria-label"), semanticContainer?.getAttribute?.("name"), semanticContainer?.getAttribute?.("id"),
      semanticContainer?.getAttribute?.("role"), semanticContainer?.getAttribute?.("data-testid"),
    ].filter(Boolean).join(" ")).toLowerCase();
    const hasPassword = Boolean(authContainer);
    const formRole = hasPassword || /login|sign[ -]?in|auth|登录/.test(formText) ? "authentication" : form ? "generic" : "none";
    const pageRole = formRole === "authentication" ? "authentication" : "product";
    return { role, name: name.slice(0, 160), formRole, pageRole };
  }).catch(() => ({ role: "", name: "", formRole: "none", pageRole: "unknown" }));
  const observedAt = new Date().toISOString();
  return {
    evidence_id: selectorEvidenceID(scanID, candidate.id || "candidate", candidate.selector || ""),
    source_kind: "page_scan",
    source_digest: sourceDigest || sha256Text(`page|${pageURL}`),
    observed_role: semantics.role || (normalizeAction(candidate.kind) === "fill" ? "textbox" : "button"),
    observed_accessible_name: semantics.name,
    observed_url: pageURL,
    observed_route_template: new URL(pageURL).pathname,
    observed_page_role: semantics.pageRole,
    observed_form_role: semantics.formRole,
    evidence_digest_sha256: sourceDigest || sha256Text(`page|${pageURL}`),
    observed_at: observedAt,
  };
}

async function pageEvidenceDigest(page: any, pageURL: string): Promise<string> {
  const evidence = await page.locator("button, a[href], input, textarea, select, [role], [aria-label]").evaluateAll((elements: any[]) => elements.slice(0, 200).map((element) => ({
    tag: String(element.tagName || "").toLowerCase(),
    role: element.getAttribute?.("role") || "",
    aria: element.getAttribute?.("aria-label") || "",
    placeholder: element.getAttribute?.("placeholder") || "",
    title: element.getAttribute?.("title") || "",
    text: String(element.innerText || element.textContent || "").trim().replace(/\s+/g, " ").slice(0, 120),
  }))).catch(() => []);
  return sha256Text(JSON.stringify({ page_url: pageURL, controls: evidence }));
}

function selectorEvidenceID(scanID: string, candidateID: string, selector: string): string {
  return `ev_browser_scan_${hashText(`${scanID}|${candidateID}|${selector}`)}`;
}

// Direct API v1 uses a bare lowercase SHA-256 value. Adding an algorithm
// prefix makes otherwise valid selector provenance fail formal intake.
function sha256Text(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}

function isNewProjectSemantic(value: string): boolean {
  return /(新建项目|创建项目|新增项目|new project|create project)/i.test(value);
}

function isProjectIdeaSemantic(value: string): boolean {
  return /(项目需求|项目名称|今天你想做什么|project idea|project prompt|project name|input-project-idea)/i.test(value);
}

function selectorForControl(control: any, label: string): string {
  const attrs = control.attrs || {};
  if (attrs.testid) return `[data-testid="${escapeCSSString(attrs.testid)}"]`;
  if (attrs.aria) return `[aria-label="${escapeCSSString(attrs.aria)}"]`;
  if (attrs.id && /^[A-Za-z][\w-]*$/.test(attrs.id)) return `#${attrs.id}`;
  if (control.tag === "input" && attrs.name) return `input[name="${escapeCSSString(attrs.name)}"]`;
  if (control.tag === "textarea" && attrs.name) return `textarea[name="${escapeCSSString(attrs.name)}"]`;
  if (control.tag === "select" && attrs.name) return `select[name="${escapeCSSString(attrs.name)}"]`;
  if ((control.tag === "input" || control.tag === "textarea") && attrs.placeholder) return `${control.tag}[placeholder="${escapeCSSString(attrs.placeholder)}"]`;
  const shortLabel = visibleTextSelectorLabel(label);
  if (shortLabel && control.tag === "button") return `button:has-text("${escapeCSSString(shortLabel)}")`;
  if (shortLabel && control.tag === "a") return `a:has-text("${escapeCSSString(shortLabel)}")`;
  if (shortLabel && attrs.role === "button") return `[role="button"]:has-text("${escapeCSSString(shortLabel)}")`;
  if (shortLabel && attrs.role === "link") return `[role="link"]:has-text("${escapeCSSString(shortLabel)}")`;
  return "";
}

function actionKindForControl(control: any): string {
  const tag = control.tag;
  const type = String(control.attrs?.type || "").toLowerCase();
  if (tag === "input" && type === "file") return "upload";
  if (tag === "input" && !["button", "submit", "checkbox", "radio"].includes(type)) return "fill";
  if (tag === "textarea") return "fill";
  if (tag === "select") return "select";
  return "click";
}

function businessControlScore(label: string, selector: string, goals: InteractionGoal[], kind: string): number {
  const normalized = normalizeSelectorText(`${label} ${selector}`);
  if (!normalized || looksLikeChromeControl(normalized) || looksLikeLoginControl(normalized) || looksLikeNegativeAuthControl(normalized) || looksLikeControlPlaneSignal(normalized)) return 0;
  let score = 10;
  let hasBusinessSignal = false;
  if (selector.includes("data-testid") || selector.includes("data-test") || selector.includes("data-cy")) score += 35;
  if (selector.includes("aria-label") || selector.includes(":has-text")) score += 25;
  if (selector.startsWith("#")) score += 18;
  if (["click", "fill", "select"].includes(kind)) score += 12;
  for (const keyword of businessKeywords()) {
    if (normalized.includes(keyword)) {
      score += 15;
      hasBusinessSignal = true;
    }
  }
  for (const goal of goals) {
    if (!goalActionCompatible(goal, kind)) continue;
    const keywords = goalKeywords(goal);
    const matched = keywords.filter((keyword) => keyword && normalized.includes(keyword)).length;
    if (matched > 0) {
      score += matched * 20;
      if (goal.business) score += 12;
      hasBusinessSignal = true;
    }
  }
  if (!hasBusinessSignal) return 0;
  return score;
}

function safeLinkScore(label: string, href: string, goals: InteractionGoal[]): number {
  const normalized = normalizeSelectorText(`${label} ${href}`);
  if (!normalized || looksLikeControlPlaneSignal(normalized) || looksLikeDestructiveControl(normalized) || looksLikeLoginControl(normalized)) return 0;
  let score = 0;
  for (const keyword of ["dashboard", "workspace", "project", "demo", "asset", "workflow", "工作台", "项目", "演示", "资产", "工作流", "开始", "创建", "生成"]) {
    if (normalized.includes(keyword)) score += 18;
  }
  for (const goal of goals) {
    score += goalKeywords(goal).filter((keyword) => keyword && normalized.includes(keyword)).length * 25;
  }
  return score;
}

function bestGoalForControl(label: string, selector: string, goals: InteractionGoal[], kind: string): InteractionGoal | undefined {
  const normalized = normalizeSelectorText(`${label} ${selector}`);
  if (looksLikeNegativeAuthControl(normalized)) return undefined;
  let best: { goal: InteractionGoal; score: number } | undefined;
  for (const goal of goals) {
    if (!goalActionCompatible(goal, kind)) continue;
    const score = goalKeywords(goal).filter((keyword) => keyword && normalized.includes(keyword)).length;
    if (!best || score > best.score) {
      best = { goal, score };
    }
  }
  return best && best.score > 0 ? best.goal : undefined;
}

function goalActionCompatible(goal: InteractionGoal, actionKind: string): boolean {
  const goalKind = normalizeAction(goal.kind);
  const action = normalizeAction(actionKind);
  if (!goalKind || goalKind === "business_action") return ["click", "fill", "select", "upload"].includes(action);
  if (goalKind === "auth") return action === "fill" || action === "click";
  if (goalKind === "inspect" || goalKind === "observe" || goalKind === "wait") return false;
  return goalKind === action;
}

function goalKeywords(goal: InteractionGoal): string[] {
  return uniqueWords([...(goal.keywords || []), ...(goal.label || "").split(/[\s,，、/|·:：-]+/)]);
}

function businessKeywords(): string[] {
  return [
    "create", "new", "add", "generate", "run", "start", "build", "upload", "search", "submit", "save", "confirm", "open",
    "创建", "新建", "新增", "添加", "生成", "开始", "运行", "上传", "搜索", "提交", "保存", "确认", "打开", "进入",
    "项目", "工作台", "资产", "工作流", "内容", "视频", "脚本", "图谱", "演示", "模板", "审批", "录制",
  ];
}

function looksLikeChromeControl(value: string): boolean {
  const normalized = normalizeSelectorText(value);
  if (!/(sidebar|side-bar|toggle|collapse|expand|hamburger|theme|avatar|profile|account-menu|breadcrumb|drawer|layout|shell|chrome|close|cancel|dismiss|侧边栏|折叠|展开|主题|头像|个人资料|导航|菜单|通知|关闭|取消)/i.test(normalized)) {
    return false;
  }
  return !/(create|new|project|generate|run|start|submit|save|confirm|创建|新建|项目|生成|开始|运行|提交|保存|确认)/i.test(normalized);
}

function looksLikeLoginControl(value: string): boolean {
  return /(登录|登陆|登入|sign\s*in|log\s*in|login|logout|退出|注册|创建账户|create\s*account|sign\s*up|register)/i.test(value);
}

function looksLikeNegativeAuthControl(value: string): boolean {
  return /(忘记密码|找回密码|重置密码|forgot\s*(your\s*)?password|reset\s*password|返回|返回登录|back\s*(to\s*)?(login|sign\s*in)?|注册|创建账户|create\s*account|sign\s*up|register)/i.test(value);
}

function looksLikeControlPlaneSignal(value: string): boolean {
  return /(\/aigc\b|\.well-known|\/v1\/|app-installations|execution-packages|result-packages|cascade-exchange|exchange envelope|cloud exchange|authorization bearer)/i.test(value);
}

function looksLikeDestructiveControl(value: string): boolean {
  return /(delete|remove|destroy|drop|pay|payment|billing|invoice|apikey|api key|secret|删除|移除|销毁|付款|支付|账单|密钥|令牌)/i.test(value);
}

function isURLForbiddenByScope(value: string | undefined, request: VerifyInteractionRequest): boolean {
  if (!value) return false;
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return looksLikeControlPlaneSignal(value);
  }
  const hostname = parsed.hostname.toLowerCase();
  const allowed = request.allowed_domains || [];
  if (allowed.length > 0) {
    const matched = allowed.some((domain) => {
      const normalized = normalizedAllowedHostname(domain);
      return normalized === hostname || (!isIPAddress(hostname) && !isIPAddress(normalized) && hostname.endsWith(`.${normalized}`));
    });
    if (!matched) return true;
  }
  const path = parsed.pathname.toLowerCase();
  for (const prefix of request.forbidden_path_prefixes || []) {
    const normalized = `/${prefix.replace(/^\/+/, "").toLowerCase()}`;
    if (normalized !== "/" && (path === normalized || path.startsWith(`${normalized.replace(/\/$/, "")}/`))) {
      return true;
    }
  }
  return looksLikeControlPlaneSignal(parsed.toString());
}

function normalizedAllowedHostname(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return "";
  try {
    return new URL(/^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`).hostname.toLowerCase();
  } catch {
    return trimmed.replace(/^\[|\]$/g, "").replace(/:\d+$/, "").toLowerCase();
  }
}

function isIPAddress(value: string): boolean {
  return /^\d{1,3}(?:\.\d{1,3}){3}$/.test(value) || value.includes(":");
}

function looksLikeLoginURL(value: string): boolean {
  return /\/(login|signin|sign-in|auth\/login|app\/login)([/?#]|$)/i.test(value);
}

function visibleTextSelectorLabel(label: string): string {
  const cleaned = label.replace(/\s+/g, " ").trim();
  if (!cleaned || cleaned.length > 36) return "";
  return cleaned;
}

function escapeCSSString(value: string): string {
  return value.replace(/\\/g, "\\\\").replace(/"/g, "\\\"");
}

function normalizeSelectorText(value?: string): string {
  return (value || "").toLowerCase().trim().replace(/\s+/g, " ");
}

function uniqueWords(values: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of values) {
    const normalized = normalizeSelectorText(value);
    if (!normalized || seen.has(normalized)) continue;
    seen.add(normalized);
    out.push(normalized);
  }
  return out;
}

async function attemptLoginIfCredentialsProvided(
  page: any,
  credentials: { username?: string; password?: string; timeout: number; scanID: string; transitions?: string[] },
): Promise<{ status: string; evidence: VerifiedInteractionCandidate[] }> {
  const username = credentials.username?.trim();
  const password = credentials.password;
  const evidence: VerifiedInteractionCandidate[] = [];
  if (!username || !password) {
    return { status: "credentials_missing", evidence };
  }

  const transitions = credentials.transitions || [];
  transitions.push("credentials_available");
  let usernameFilled = false;
  let passwordInput = await firstVisibleLocatorAcrossFrames(page, passwordInputSelectors(), 1800);
  if (!passwordInput && await isLikelyLoginContext(page)) {
    const usernameInput = await firstVisibleLocatorAcrossFrames(page, usernameInputSelectors(), 2200);
    if (usernameInput) {
      await usernameInput.fill(username, { timeout: 3500 }).catch(() => undefined);
      usernameFilled = true;
      transitions.push("username_step_filled");
      const next = await firstVisibleLoginSubmit(page);
      const advanced = next
        ? await next.click({ timeout: 3000 }).then(() => true).catch(() => false)
        : await usernameInput.press("Enter", { timeout: 2000 }).then(() => true).catch(() => false);
      transitions.push(advanced ? "username_step_submitted" : "username_step_submit_failed");
      if (advanced) {
        await waitForPageEvidenceReady(page, Math.min(credentials.timeout, 8000));
        passwordInput = await firstVisibleLocatorAcrossFrames(page, passwordInputSelectors(), 3500);
      }
    }
  }
  for (let attempt = 1; !passwordInput && attempt <= 3; attempt += 1) {
    const trigger = await firstVisibleLoginTrigger(page, Math.min(2200 + attempt * 600, 4000));
    if (!trigger) {
      transitions.push(`login_trigger_not_found:${attempt}`);
      break;
    }
    await appendLoginEvidence(evidence, trigger, "login_entry", "click", page, credentials.scanID);
    transitions.push(`login_trigger_found:${attempt}`);
    const clicked = await trigger.click({ timeout: 4000 }).then(() => true).catch(() => false);
    transitions.push(clicked ? `login_trigger_clicked:${attempt}` : `login_trigger_click_failed:${attempt}`);
    if (!clicked) continue;
    await waitForPageEvidenceReady(page, Math.min(credentials.timeout, 8000));
    passwordInput = await firstVisibleLocatorAcrossFrames(page, passwordInputSelectors(), 3500);
  }

  passwordInput ||= await firstVisibleLocatorAcrossFrames(page, passwordInputSelectors(), 3000);
  if (!passwordInput) {
    transitions.push("trying_known_login_paths");
    passwordInput = await navigateToLikelyLoginPath(page, credentials.timeout);
  }
  if (!passwordInput) {
    transitions.push("login_form_not_found");
    return { status: "login_form_not_found", evidence };
  }
  transitions.push("password_input_visible");
  const formSubmit = await firstVisibleLoginSubmit(page);
  if (formSubmit) {
    await appendLoginEvidence(evidence, formSubmit, "login_form_submit", "click", page, credentials.scanID);
  }
  if (!usernameFilled) {
    const usernameInput = await firstVisibleLocatorAcrossFrames(page, usernameInputSelectors(), 3000);
    if (!usernameInput) {
      transitions.push("username_input_not_found");
      return { status: "username_input_not_found", evidence };
    }
    await usernameInput.fill(username, { timeout: 3500 }).catch(() => undefined);
    usernameFilled = true;
  }

  transitions.push("login_form_ready");
  await passwordInput.fill(password, { timeout: 3500 }).catch(() => undefined);
  const preSubmitURL = page.url();
  const submit = await firstVisibleLoginSubmit(page);
  if (submit) {
    await submit.click({ timeout: 3000 }).catch(() => undefined);
  } else {
    await passwordInput.press("Enter", { timeout: 2000 }).catch(() => undefined);
  }
  transitions.push("login_submitted");
  await waitForPageEvidenceReady(page, Math.min(credentials.timeout, 8000));
  const passwordStillVisible = await firstVisibleLocatorAcrossFrames(page, passwordInputSelectors(), 1200);
  if (passwordStillVisible) {
    const failure = await classifyVisibleLoginFailure(page);
    if (failure) {
      transitions.push(`login_failure:${failure}`);
      return { status: `submitted_${failure}`, evidence };
    }
    transitions.push("submitted_login_form_still_visible");
    return { status: "submitted_login_form_still_visible", evidence };
  }
  const finalURL = page.url();
  const authenticatedContextVisible = await hasAuthenticatedContextMarker(page);
  if (looksLikeLoginURL(finalURL) || (finalURL === preSubmitURL && !authenticatedContextVisible)) {
    transitions.push("submitted_still_on_login_url");
    return { status: "submitted_still_on_login_url", evidence };
  }
  transitions.push("authenticated_navigation_observed");
  return { status: "submitted_navigation_observed", evidence };
}

async function appendLoginEvidence(
  evidence: VerifiedInteractionCandidate[],
  locator: any,
  id: string,
  kind: string,
  page: any,
  scanID: string,
): Promise<void> {
  const selector = await stableSelectorForLoginEvidence(locator);
  if (!selector || evidence.some((item) => item.selector === selector)) return;
  const pageURL = page.url();
  const candidate: InteractionCandidate = { id, kind, selector };
  const sourceDigest = await pageEvidenceDigest(page, pageURL);
  evidence.push({
    ...candidate,
    status: "verified",
    visible: true,
    enabled: true,
    page_url: pageURL,
    page_title: await page.title().catch(() => ""),
    verified_at: new Date().toISOString(),
    ...await selectorProvenance(locator, candidate, pageURL, scanID, sourceDigest),
  });
}

async function stableSelectorForLoginEvidence(locator: any): Promise<string> {
  return locator.evaluate((element: any) => {
    const escape = (value: string) => value.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
    const tag = String(element.tagName || "").toLowerCase();
    const testID = String(element.getAttribute?.("data-testid") || "").trim();
    if (testID) return `[data-testid="${escape(testID)}"]`;
    const id = String(element.id || "").trim();
    if (id && /^[A-Za-z][\w-]*$/.test(id)) return `#${id}`;
    const name = String(element.getAttribute?.("name") || "").trim();
    if (tag && name) return `${tag}[name="${escape(name)}"]`;
    const type = String(element.getAttribute?.("type") || "").trim().toLowerCase();
    if (tag && type) return `${tag}[type="${escape(type)}"]`;
    return "";
  }).catch(() => "");
}

async function classifyVisibleLoginFailure(page: any): Promise<string> {
  const text = await page.locator("body").innerText({ timeout: 1500 }).catch(() => "");
  if (/(invalid credentials|incorrect username|incorrect password|用户名或密码错误|账号或密码错误|邮箱或密码错误)/i.test(text)) return "invalid_credentials";
  if (/(temporarily locked|too many failed attempts|账户.*锁定|账号.*锁定|尝试次数过多)/i.test(text)) return "account_locked";
  if (/(captcha|human verification|人机验证|安全验证)/i.test(text)) return "captcha_required";
  if (/(network error|request failed|网络错误|请求失败|服务不可用)/i.test(text)) return "network_error";
  return "";
}

async function hasAuthenticatedContextMarker(page: any): Promise<boolean> {
  const selectors = [
    "[data-testid*='new-project' i]", "[data-testid*='create-project' i]", "[data-testid*='workspace' i]",
    "[data-testid*='user-menu' i]", "[data-testid*='avatar' i]", "a[href*='/projects']", "a[href*='/workspace']",
  ];
  if (await firstVisibleLocatorAcrossFrames(page, selectors, 1200)) return true;
  const authenticatedName = /新建项目|创建项目|项目工作台|退出登录|工作台|new\s*project|create\s*project|workspace|log\s*out|sign\s*out/i;
  const frames = typeof page.frames === "function" ? page.frames() : [page];
  for (const frame of frames) {
    for (const locator of [frame.getByRole("button", { name: authenticatedName }).first(), frame.getByRole("link", { name: authenticatedName }).first()]) {
      if (await locator.isVisible({ timeout: 800 }).catch(() => false)) return true;
    }
  }
  return false;
}

async function navigateToLikelyLoginPath(page: any, timeout: number): Promise<any | undefined> {
  const current = new URL(page.url());
  const paths = ["/login", "/signin", "/sign-in", "/auth/login", "/app/login"];
  for (const path of paths) {
    const target = new URL(path, current.origin).toString();
    await page.goto(target, { waitUntil: "domcontentloaded", timeout: Math.min(timeout, 6000) }).catch(() => undefined);
    await waitForPageEvidenceReady(page, Math.min(timeout, 5000));
    const passwordInput = await firstVisibleLocatorAcrossFrames(page, passwordInputSelectors(), 2200);
    if (passwordInput) {
      return passwordInput;
    }
    const trigger = await firstVisibleLoginTrigger(page, 2500);
    if (trigger && await trigger.click({ timeout: 3000 }).then(() => true).catch(() => false)) {
      await waitForPageEvidenceReady(page, Math.min(timeout, 5000));
      const revealedPassword = await firstVisibleLocatorAcrossFrames(page, passwordInputSelectors(), 3000);
      if (revealedPassword) return revealedPassword;
    }
  }
  await page.goto(current.toString(), { waitUntil: "domcontentloaded", timeout: Math.min(timeout, 6000) }).catch(() => undefined);
  await waitForPageEvidenceReady(page, Math.min(timeout, 5000));
  return undefined;
}

async function isLikelyLoginContext(page: any): Promise<boolean> {
  if (looksLikeLoginURL(page.url())) return true;
  const title = await page.title().catch(() => "");
  if (/(login|log\s*in|sign\s*in|登录|登入|登陆)/i.test(title)) return true;
  const authMethod = /邮箱登录|账号登录|密码登录|手机号登录|email\s*(login|sign\s*in)|password\s*(login|sign\s*in)/i;
  for (const locator of [page.getByRole("button", { name: authMethod }).first(), page.getByRole("link", { name: authMethod }).first()]) {
    if (await locator.isVisible({ timeout: 800 }).catch(() => false)) return true;
  }
  return false;
}

async function waitForPageEvidenceReady(page: any, timeoutMS = 8000): Promise<void> {
  await page.waitForLoadState("domcontentloaded", { timeout: timeoutMS }).catch(() => undefined);
  await page.waitForLoadState("networkidle", { timeout: timeoutMS }).catch(() => undefined);
  await page.waitForTimeout(minimumEvidenceSettleMS).catch(() => undefined);
  await page.evaluate(() => {
    const doc = (globalThis as any).document;
    return doc?.fonts?.ready;
  }).catch(() => undefined);
  await page.evaluate(() => new Promise<void>((resolve) => {
    const raf = (globalThis as any).requestAnimationFrame;
    if (typeof raf !== "function") {
      resolve();
      return;
    }
    raf(() => raf(() => resolve()));
  })).catch(() => undefined);
  await page.waitForFunction(
    () => {
      const state = globalThis as any;
      const doc = state.document;
      if (!doc) return true;
      const controls = doc.querySelectorAll("button,a[href],input,textarea,select,[role='button'],[role='link'],[data-testid],[data-test],[data-cy]").length;
      const bodyTextLength = (doc.body?.innerText || "").trim().length;
      const signature = `${doc.readyState}|${bodyTextLength}|${controls}|${Math.round(doc.body?.getBoundingClientRect().height || 0)}`;
      const stable = state.__cascadeEvidenceRenderSignature === signature;
      state.__cascadeEvidenceRenderSignature = signature;
      return stable && doc.readyState !== "loading";
    },
    undefined,
    { timeout: Math.min(timeoutMS, 2500), polling: 250 },
  ).catch(() => undefined);
}

async function firstVisibleLoginTrigger(page: any, timeout = 1800): Promise<any | undefined> {
  const loginName = /邮箱登录|邮件登录|账号登录|密码登录|登录|登陆|登入|sign\s*in|log\s*in|login|try\s*it\s*now|get\s*started|start\s*building|控制台|console|dashboard|进入/i;
  const roleCandidates = [
    page.getByRole("link", { name: loginName }).first(),
    page.getByRole("button", { name: loginName }).first(),
    page.getByText(loginName).first(),
  ];
  for (const locator of roleCandidates) {
    if (await locator.isVisible({ timeout }).catch(() => false)) {
      return locator;
    }
  }
  return firstVisibleLocatorAcrossFrames(page, [
    "a[href*='login']",
    "a[href*='signin']",
    "a[href*='sign-in']",
    "button[data-testid*='login' i]",
    "[data-testid*='login' i]",
    "button:has-text('邮箱登录')",
    "button:has-text('账号登录')",
    "button:has-text('Try it now')",
    "a:has-text('Try it now')",
    "button:has-text('Get started')",
    "a:has-text('Get started')",
    "button:has-text('Start building')",
    "a:has-text('Start building')",
  ], timeout);
}

async function firstVisibleLoginSubmit(page: any): Promise<any | undefined> {
  const semanticSubmit = await firstVisibleLocatorAcrossFrames(page, [
    "button[type='submit']",
    "input[type='submit']",
    "[data-testid*='submit' i]",
    "[data-testid*='login-submit' i]",
  ], 1800);
  if (semanticSubmit) return semanticSubmit;
  const loginName = /登录|登陆|登入|sign\s*in|log\s*in|login|继续|continue|提交|submit/i;
  const frames = typeof page.frames === "function" ? page.frames() : [page];
  for (const frame of frames) {
    const roleCandidates = [
      frame.getByRole("button", { name: loginName }).first(),
      frame.getByRole("link", { name: loginName }).first(),
    ];
    for (const locator of roleCandidates) {
      if (await locator.isVisible({ timeout: 1200 }).catch(() => false)) {
        return locator;
      }
    }
  }
  return undefined;
}

async function firstVisibleLocator(page: any, selectors: string[], timeout: number): Promise<any | undefined> {
  for (const selector of selectors) {
    const locator = page.locator(selector).first();
    if (await locator.isVisible({ timeout }).catch(() => false)) {
      return locator;
    }
  }
  return undefined;
}

async function firstVisibleLocatorAcrossFrames(page: any, selectors: string[], timeout: number): Promise<any | undefined> {
  const frames = typeof page.frames === "function" ? page.frames() : [page];
  for (const frame of frames) {
    const found = await firstVisibleLocator(frame, selectors, timeout);
    if (found) return found;
  }
  return undefined;
}

function usernameInputSelectors(): string[] {
  return [
    "input[type='email']",
    "input[name='email']",
    "input[name='username']",
    "input[name='account']",
    "input[id*='email' i]",
    "input[id*='user' i]",
    "input[autocomplete='username']",
    "input[autocomplete='email']",
    "input[type='text']",
  ];
}

function passwordInputSelectors(): string[] {
  return [
    "input[type='password']",
    "input[name='password']",
    "input[id*='password' i]",
    "input[autocomplete='current-password']",
  ];
}

function normalizeAction(value?: string): string {
  const normalized = (value || "").toLowerCase().trim();
  if (["button", "cta"].includes(normalized)) return "click";
  if (["input", "type"].includes(normalized)) return "fill";
  return normalized;
}

function hashText(value: string): string {
  let hash = 2166136261;
  for (let i = 0; i < value.length; i += 1) {
    hash ^= value.charCodeAt(i);
    hash += (hash << 1) + (hash << 4) + (hash << 7) + (hash << 8) + (hash << 24);
  }
  return (hash >>> 0).toString(16);
}
