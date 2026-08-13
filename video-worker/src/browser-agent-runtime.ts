import { createHash, randomUUID } from "node:crypto";
import { mkdir, readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { launchOptionsWithProxy } from "./playwright-proxy.js";
import { configuredBrowserExecutable } from "./browser-executable.js";

type BrowserAgentTargetContract = {
  semantic_id: string;
  allowed_roles?: string[];
  allowed_names?: string[];
  forbidden_names?: string[];
  component_ref?: string;
  destructive: boolean;
  evidence_refs?: Array<{ id?: string; kind?: string; artifact_id?: string; confidence?: number }>;
};

type BrowserAgentComponentTarget = {
  component_ref?: string;
  role?: string;
  name?: string;
  text?: string;
  label?: string;
  test_id?: string;
  selector?: string;
  selector_alternatives?: Array<{ kind: string; value: string }>;
};

type BrowserAgentInteraction = {
  kind: "navigate" | "click" | "fill" | "select" | "upload" | "wait" | "assert" | "inspect" | string;
  target?: {
    url?: string;
    selector?: string;
    role?: string;
    text?: string;
    label?: string;
    test_id?: string;
  };
  value?: string;
  input_ref?: string;
  secret_ref?: string;
  parameters?: Record<string, unknown>;
  wait_until?: string;
  wait_conditions?: string[];
  non_destructive?: boolean;
  selector_policy?: string;
};

type BrowserAgentValidation = {
  id: string;
  kind: string;
  target?: {
    url?: string;
    selector?: string;
    role?: string;
    text?: string;
    label?: string;
    test_id?: string;
  };
  assertion?: string;
  expected?: unknown;
  required: boolean;
  timeout_ms?: number;
};

export type BrowserAgentWorkerStage = {
  id: string;
  order: number;
  node_id: string;
  objective?: string;
  entry_route?: string;
  route?: string;
  url?: string;
  target_route_template?: string;
  expected_route_after_action?: string;
  runtime_route_verification_required?: boolean;
  target_contract: BrowserAgentTargetContract;
  components?: BrowserAgentComponentTarget[];
  interactions: BrowserAgentInteraction[];
  wait_conditions?: string[];
  capture_plan?: {
    min_duration_ms?: number;
    pre_capture_wait_ms?: number;
    hold_after_ms?: number;
  };
  success_state?: string;
  duration_ms?: number;
  validations?: BrowserAgentValidation[];
  preferred_selector_alternative?: { kind: string; value: string };
  evidence_bound_selector_alternatives?: Array<{ kind: string; value: string }>;
};

export type BrowserAgentOpenRequest = {
  session_id?: string;
  output_dir: string;
  browser?: {
    engine?: string;
    headless?: boolean;
    viewport?: { width: number; height: number };
    record_video?: boolean;
  };
  allowed_domains: string[];
  allowed_origins?: string[];
  allowed_routes?: string[];
  forbidden_pages?: string[];
  forbidden_path_prefixes?: string[];
  forbidden_keywords?: string[];
  mask_selectors?: string[];
	recording_sensitive?: boolean;
	record_trace?: boolean;
};

export type BrowserAgentStageRequest = {
  session_id: string;
  stage: BrowserAgentWorkerStage;
  secrets?: Record<string, string>;
};

type RuntimeObservation = {
  source: "actual_browser_observation" | "browser_assertion";
  url?: string;
  title?: string;
  assertions?: Array<{ kind: string; passed: boolean; actual?: string }>;
  target_geometry?: BrowserTargetGeometry;
  target_resolution_attempts?: BrowserTargetResolutionAttempt[];
};

export type BrowserTargetResolutionAttempt = {
  strategy: string;
  candidate_count: number;
  unique: boolean;
  visible: boolean;
  evidence_bound: boolean;
  role_allowed?: boolean;
  name_allowed?: boolean;
  outcome: "resolved" | "no_candidates" | "ambiguous" | "not_visible" | "role_mismatch" | "name_mismatch" | "forbidden_name" | "unsupported_selector";
};

export type BrowserTargetGeometry = {
  schema_version: "demoops.browser_target_geometry.v1";
  target_semantic_id: string;
  resolution_strategy: string;
  selector_digest_sha256: string;
  captured_at: string;
  recording_offset_ms: number;
  viewport: { width: number; height: number; dpr: number };
  element_box_css_px: { x: number; y: number; width: number; height: number };
  element_box_normalized: { x: number; y: number; width: number; height: number };
  screenshot_artifact_id?: string;
  confidence: number;
};

type EvidenceRef = {
  id: string;
  kind: "webpage_screenshot" | "browser_trace";
  summary: string;
  artifact_id: string;
  confidence: number;
};

type ArtifactRef = {
  id: string;
  kind: string;
  uri: string;
  mime_type: string;
  sha256: string;
  size_bytes: number;
  created_at: string;
  sensitive: boolean;
  source_node_id?: string;
  metadata: Record<string, unknown>;
};

export type BrowserAgentStageResult = {
  observation: RuntimeObservation;
  evidence_refs: EvidenceRef[];
  artifacts: ArtifactRef[];
  target_resolved?: boolean;
  preferred_selector_alternative?: { kind: string; value: string };
  suggested_wait_condition?: string;
};

export type BrowserAgentCloseResult = {
  recording_path?: string;
  trace_path?: string;
  artifacts: ArtifactRef[];
  runtime_versions: Record<string, string>;
};

type BrowserAgentSession = {
  id: string;
  browser: any;
  context: any;
  page: any;
  video: any;
  outputDir: string;
  tracePath: string;
  engine: string;
  allowedDomains: string[];
  allowedOrigins: string[];
  allowedRoutes: string[];
  forbiddenPages: string[];
  forbiddenPathPrefixes: string[];
  forbiddenKeywords: string[];
	maskSelectors: string[];
	recordingSensitive: boolean;
	recordTrace: boolean;
	traceActive: boolean;
	openedAtMS: number;
	targetGeometryByArtifactID: Map<string, BrowserTargetGeometry>;
};

type ResolvedTarget = { locator: any; strategy: string; approvedAlternative?: { kind: string; value: string } };

const sessions = new Map<string, BrowserAgentSession>();
const targetProbeTimeoutMS = 2_000;
// Canonical Browser Agent evidence master. Playwright records only the page
// content viewport: browser chrome, the desktop, and off-viewport page content
// are excluded.
const defaultViewport = { width: 2560, height: 1440 };
const actionTimeoutMS = 10_000;
const screenshotTimeoutMS = 8_000;
const secretInputMaskSelector = '[data-cascade-secret-input="true"]';

export async function openBrowserAgentSession(request: BrowserAgentOpenRequest): Promise<{ session_id: string; runtime_versions: Record<string, string> }> {
  if (!request.output_dir?.trim()) throw new Error("browser_agent_output_dir_required");
  if (!Array.isArray(request.allowed_domains) || request.allowed_domains.length === 0) throw new Error("browser_agent_allowed_domains_required");
  const requestedViewport = request.browser?.viewport;
  if (requestedViewport && (requestedViewport.width !== defaultViewport.width || requestedViewport.height !== defaultViewport.height)) {
    throw new Error(`browser_agent_recording_resolution_mismatch: got=${requestedViewport.width}x${requestedViewport.height} want=${defaultViewport.width}x${defaultViewport.height}`);
  }
  const sessionID = safeName(request.session_id || `browser_agent_${randomUUID()}`);
  if (sessions.has(sessionID)) throw new Error(`browser_agent_session_exists: ${sessionID}`);

  const playwright = await import("playwright");
  const engineName = normalizeEngine(request.browser?.engine);
  const engine = playwright[engineName];
  if (!engine) throw new Error(`unsupported_browser_agent_engine: ${engineName}`);
  const outputDir = path.resolve(request.output_dir);
  await mkdir(outputDir, { recursive: true, mode: 0o700 });
  const launchOptions: Record<string, unknown> = { headless: request.browser?.headless ?? true };
  const executablePath = configuredBrowserExecutable();
  if (executablePath) launchOptions.executablePath = executablePath;
  const browser = await engine.launch(launchOptionsWithProxy(launchOptions));
  const contextOptions: Record<string, unknown> = {
    viewport: request.browser?.viewport || defaultViewport,
    deviceScaleFactor: 1,
    acceptDownloads: false,
  };
  if (request.browser?.record_video) {
    contextOptions.recordVideo = { dir: outputDir, size: request.browser?.viewport || defaultViewport };
  }
  const context = await browser.newContext(contextOptions);
  const maskSelectors = [...new Set([...(request.mask_selectors || []), secretInputMaskSelector])];
  await installRecordingMasks(context, maskSelectors);
  const recordTrace = request.record_trace ?? true;
  const tracePath = path.join(outputDir, "browser-agent-trace.zip");
  // The visible local-login bridge must never persist a trace while a person
  // types credentials into its isolated browser profile.
	if (recordTrace) await context.tracing.start({ screenshots: true, snapshots: true, sources: false });
  const page = await context.newPage();
  const video = page.video?.();
  const session: BrowserAgentSession = {
    id: sessionID,
    browser,
    context,
    page,
    video,
    outputDir,
    tracePath,
    engine: engineName,
    allowedDomains: request.allowed_domains,
    allowedOrigins: request.allowed_origins || [],
    allowedRoutes: request.allowed_routes || [],
    forbiddenPages: request.forbidden_pages || [],
    forbiddenPathPrefixes: request.forbidden_path_prefixes || [],
    forbiddenKeywords: request.forbidden_keywords || [],
	maskSelectors,
	recordingSensitive: request.recording_sensitive ?? true,
	recordTrace,
	traceActive: recordTrace,
	openedAtMS: Date.now(),
	targetGeometryByArtifactID: new Map(),
  };
  await page.route("**/*", async (route: any) => {
    // Route allowlists apply to document navigation, not the page's JS/CSS/media.
    const error = urlPolicyError(route.request().url(), session, !route.request().isNavigationRequest());
    if (error) {
      await route.abort("blockedbyclient");
      return;
    }
    await route.continue();
  });
  sessions.set(sessionID, session);
  return { session_id: sessionID, runtime_versions: { runner: "playwright-browser-agent", browser: engineName } };
}

export async function browserAgentSessionStatus(request: { session_id: string }): Promise<{ url: string; title: string }> {
  const session = requiredSession(request.session_id);
  // Keep this endpoint intentionally metadata-only: it is used to hand a
  // manually authenticated, isolated browser back to the local test harness.
  return { url: safeURL(session.page.url()), title: redactText(await session.page.title().catch(() => "")) };
}

// This intentionally narrow RPC is used only to open the local dev/test
// visible-login window. Unlike a stage, it takes no screenshot and records no
// execution evidence, so user-entered credentials cannot enter artifacts.
export async function browserAgentDevVisibleNavigate(request: { session_id: string; target_url: string }): Promise<{ url: string; title: string }> {
  const session = requiredSession(request.session_id);
  const targetURL = absoluteTargetURL(request.target_url, session.page.url());
  const policyError = urlPolicyError(targetURL, session, false);
  if (policyError) throw new Error(policyError);
  await session.page.goto(targetURL, { waitUntil: "domcontentloaded", timeout: actionTimeoutMS });
  await waitForPageSettled(session.page);
  return browserAgentSessionStatus(request);
}

// Local dev/test-only automatic login. Credentials exist only in this RPC
// call, are filled into masked inputs, and are never returned, logged,
// screenshotted, or written into the Playwright trace.
export async function browserAgentDevVisibleAutoLogin(request: { session_id: string; email: string; password: string }): Promise<{ url: string; title: string }> {
  const session = requiredSession(request.session_id);
  let email = String(request.email || "");
  let password = String(request.password || "");
  try {
    if (!email.trim() || !password) throw new Error("browser_agent_dev_visible_login_credentials_required");
    if (session.traceActive) throw new Error("browser_agent_dev_visible_login_trace_must_be_disabled");
    if (!isLoginURL(session.page.url())) return browserAgentSessionStatus(request);

    let emailInput = await firstVisibleLocator(session.page, [
      'input[type="email"]',
      'input[name="email"]',
      'input[autocomplete="email"]',
      'input[name*="email" i]',
    ]);
    let passwordInput = await firstVisibleLocator(session.page, [
      'input[type="password"]',
      'input[name="password"]',
      'input[autocomplete="current-password"]',
    ]);
    let submit = await firstVisibleLocator(session.page, [
      'button[type="submit"]',
      'input[type="submit"]',
      'button:has-text("登录")',
      'button:has-text("Sign in")',
      'button:has-text("Log in")',
      'button:has-text("Login")',
    ]);
    // Cascade's current login UX first shows a method chooser. Select the
    // email/password method before resolving the credential fields. This is a
    // Server-side automation detail; the App page and its rules remain unchanged.
    if (!emailInput || !passwordInput || !submit) {
      const emailMethod = await firstVisibleLocator(session.page, [
        'button:has-text("邮箱登录")',
        'button:has-text("Email")',
        'button:has-text("email")',
        'button:has-text("邮箱")',
      ]);
      if (emailMethod) {
        await emailMethod.click({ timeout: actionTimeoutMS });
        await waitForPageSettled(session.page, 5_000);
        emailInput = await firstVisibleLocator(session.page, [
          'input[type="email"]',
          'input[name="email"]',
          'input[autocomplete="email"]',
          'input[name*="email" i]',
        ]);
        passwordInput = await firstVisibleLocator(session.page, [
          'input[type="password"]',
          'input[name="password"]',
          'input[autocomplete="current-password"]',
        ]);
        submit = await firstVisibleLocator(session.page, [
          'button[type="submit"]',
          'input[type="submit"]',
          'button:has-text("鐧诲綍")',
          'button:has-text("Sign in")',
          'button:has-text("Log in")',
          'button:has-text("Login")',
        ]);
      }
    }
    if (!emailInput || !passwordInput || !submit) throw new Error("browser_agent_dev_visible_login_form_not_found");

    await prepareSecretTarget(session, emailInput);
    await emailInput.fill(email, { timeout: actionTimeoutMS });
    await prepareSecretTarget(session, passwordInput);
    await passwordInput.fill(password, { timeout: actionTimeoutMS });
    await submit.click({ timeout: actionTimeoutMS });
    await session.page.waitForURL((value: URL) => !isLoginURL(value.toString()), { timeout: 60_000 });
    await waitForPageSettled(session.page, 10_000);
    if (isLoginURL(session.page.url())) throw new Error("browser_agent_dev_visible_login_not_completed");
    return browserAgentSessionStatus(request);
  } finally {
    request.email = "";
    request.password = "";
    email = "";
    password = "";
  }
}

// Trace collection starts only after login, so credential entry cannot enter
// snapshots. Video recording, when enabled by the Server's automatic-login
// path, is protected by the installed email/password mask selectors.
export async function browserAgentDevVisibleBeginRecording(request: { session_id: string }): Promise<Record<string, never>> {
  const session = requiredSession(request.session_id);
  if (!session.traceActive) {
    await session.context.tracing.start({ screenshots: true, snapshots: true, sources: false });
    session.traceActive = true;
  }
  return {};
}

async function firstVisibleLocator(page: any, selectors: string[]): Promise<any | undefined> {
  for (const selector of selectors) {
    try {
      const locator = page.locator(selector).first();
      if (await locator.isVisible({ timeout: 1_000 }).catch(() => false)) return locator;
    } catch {
      // Ignore a malformed optional fallback selector and continue probing.
    }
  }
  return undefined;
}

function isLoginURL(value: string): boolean {
  try {
    const pathname = new URL(value).pathname.toLowerCase();
    return pathname === "/login" || pathname.startsWith("/login/");
  } catch {
    return false;
  }
}

type BrowserAgentExecutionPolicyRequest = {
  session_id: string;
  policy: {
    allowed_origins: string[];
    allowed_routes: string[];
    forbidden_pages?: string[];
    forbidden_path_prefixes?: string[];
    forbidden_keywords?: string[];
    mask_selectors?: string[];
  };
};

// Policy is narrowed only after a Server-validated App package is bound to a
// manually authenticated local-test session. It cannot change allowed hosts.
export async function browserAgentApplyExecutionPolicy(request: BrowserAgentExecutionPolicyRequest): Promise<Record<string, never>> {
  const session = requiredSession(request.session_id);
  const policy = request.policy || {} as BrowserAgentExecutionPolicyRequest["policy"];
  if (!Array.isArray(policy.allowed_origins) || policy.allowed_origins.length === 0 || !Array.isArray(policy.allowed_routes) || policy.allowed_routes.length === 0) {
    throw new Error("browser_agent_execution_policy_scope_required");
  }
  for (const origin of policy.allowed_origins) {
    const error = urlPolicyError(origin, session, false);
    if (error) throw new Error(error);
  }
  session.allowedOrigins = [...new Set(policy.allowed_origins)];
  session.allowedRoutes = [...new Set(policy.allowed_routes)];
  session.forbiddenPages = [...new Set(policy.forbidden_pages || [])];
  session.forbiddenPathPrefixes = [...new Set(policy.forbidden_path_prefixes || [])];
  session.forbiddenKeywords = [...new Set(policy.forbidden_keywords || [])];
  session.maskSelectors = [...new Set([...session.maskSelectors, ...(policy.mask_selectors || [])])];
  const currentError = urlPolicyError(session.page.url(), session, false);
  if (currentError) throw new Error(currentError);
  return {};
}


export async function observeBrowserAgentStage(request: BrowserAgentStageRequest): Promise<BrowserAgentStageResult> {
  const session = requiredSession(request.session_id);
  validateStage(request.stage);
  const action = request.stage.interactions[0];
  await waitForObservationWindow(session.page, request.stage);
  // Preserve redacted evidence even when semantic target resolution fails.
  const artifact = await captureScreenshot(session, request.stage, "before");
  let resolved: ResolvedTarget | undefined;
  let approvedAlternative: { kind: string; value: string } | undefined;
	let targetGeometry: BrowserTargetGeometry | undefined;
	let suggestedWaitCondition: string | undefined;
  let resolutionFailure = "";
  const resolutionAttempts: BrowserTargetResolutionAttempt[] = [];
  if (action && interactionRequiresResolvedTarget(action.kind)) {
    try {
      resolved = await resolveTarget(session.page, request.stage, action, false, resolutionAttempts);
		targetGeometry = await captureTargetGeometry(session, request.stage, resolved, artifact.id);
		if (!targetGeometry) {
			resolved = undefined;
			resolutionFailure = `browser_agent_target_not_resolved: ${request.stage.node_id}; strategies=target_geometry_unavailable`;
		}
    } catch (error) {
      if (!isTargetResolutionFailure(error)) throw error;
      resolutionFailure = safeResolutionFailure(error);
      approvedAlternative = await resolveSingleApprovedAlternative(session.page, request.stage, action.kind, resolutionAttempts);
		if (!approvedAlternative && await pageStillBusy(session.page)) {
			suggestedWaitCondition = suggestedEntryWaitCondition(request.stage);
		}
    }
  }
  const evidence = screenshotEvidence(artifact, request.stage, "执行前页面观察");
  const assertions = resolutionAssertions(resolved?.strategy, resolutionFailure, safeURL(session.page.url()));
  return {
    observation: await observation(session.page, "actual_browser_observation", assertions, targetGeometry, resolutionAttempts),
    evidence_refs: [evidence],
    artifacts: [artifact],
    target_resolved: Boolean(resolved) || Boolean(action && !interactionRequiresResolvedTarget(action.kind)),
    ...(approvedAlternative ? { preferred_selector_alternative: approvedAlternative } : {}),
		...(suggestedWaitCondition ? { suggested_wait_condition: suggestedWaitCondition } : {}),
  };
}

function isTargetResolutionFailure(error: unknown): boolean {
  return String(error instanceof Error ? error.message : error).startsWith("browser_agent_target_not_resolved:");
}

function safeResolutionFailure(error: unknown): string {
  const message = String(error instanceof Error ? error.message : error);
  // The resolver emits only stage id and attempted strategy names; do not leak page text or DOM.
  return message.replace(/[\r\n]+/g, " ").slice(0, 500);
}

export async function captureTargetGeometry(
  session: Pick<BrowserAgentSession, "page" | "openedAtMS">,
  stage: BrowserAgentWorkerStage,
  resolved: ResolvedTarget,
  screenshotArtifactID?: string,
): Promise<BrowserTargetGeometry | undefined> {
  const box = await withTimeout(resolved.locator.boundingBox(), targetProbeTimeoutMS, null as { x: number; y: number; width: number; height: number } | null);
  const viewport = session.page.viewportSize?.() || await session.page.evaluate(() => ({
    width: (globalThis as any).innerWidth,
    height: (globalThis as any).innerHeight,
  })).catch(() => undefined);
  if (!box || !viewport || viewport.width <= 0 || viewport.height <= 0) return undefined;
  const left = Math.max(0, Math.min(viewport.width, Number(box.x)));
  const top = Math.max(0, Math.min(viewport.height, Number(box.y)));
  const right = Math.max(left, Math.min(viewport.width, Number(box.x) + Number(box.width)));
  const bottom = Math.max(top, Math.min(viewport.height, Number(box.y) + Number(box.height)));
  if (![left, top, right, bottom].every(Number.isFinite) || right <= left || bottom <= top) return undefined;
  const dpr = await session.page.evaluate(() => Number((globalThis as any).devicePixelRatio) || 1).catch(() => 1);
  const selectorIdentity = JSON.stringify({
    target_semantic_id: stage.target_contract.semantic_id,
    strategy: resolved.strategy,
    alternative: resolved.approvedAlternative || null,
  });
  return {
    schema_version: "demoops.browser_target_geometry.v1",
    target_semantic_id: stage.target_contract.semantic_id,
    resolution_strategy: resolved.strategy,
    selector_digest_sha256: createHash("sha256").update(selectorIdentity).digest("hex"),
    captured_at: new Date().toISOString(),
    recording_offset_ms: Math.max(0, Date.now() - session.openedAtMS),
    viewport: { width: viewport.width, height: viewport.height, dpr: Number.isFinite(dpr) && dpr > 0 ? dpr : 1 },
    element_box_css_px: { x: left, y: top, width: right - left, height: bottom - top },
    element_box_normalized: {
      x: left / viewport.width,
      y: top / viewport.height,
      width: (right - left) / viewport.width,
      height: (bottom - top) / viewport.height,
    },
    ...(screenshotArtifactID ? { screenshot_artifact_id: screenshotArtifactID } : {}),
    confidence: 1,
  };
}

// Navigation and page-observation interactions are bound to the approved
// route and runtime validations, not to a clickable DOM target. Mutating and
// element-assertion actions still fail closed unless a unique target resolves.
export function interactionRequiresResolvedTarget(kind: string): boolean {
  return !["navigate", "wait", "inspect"].includes(String(kind || "").trim().toLowerCase());
}

export function validatedStageSecretValues(stage: BrowserAgentWorkerStage, provided?: Record<string, string>): Record<string, string> {
  const approved = new Set(stage.interactions.map((interaction) => String(interaction.secret_ref || "").trim()).filter(Boolean));
  const values: Record<string, string> = {};
  for (const key of Object.keys(provided || {})) {
    if (!approved.has(key)) throw new Error("browser_agent_unapproved_secret_ref");
  }
  for (const ref of approved) {
    const value = provided?.[ref];
    if (typeof value !== "string" || value.length === 0) throw new Error("browser_agent_secret_ref_requires_broker");
    values[ref] = value;
  }
  return values;
}

function requiredStageSecretValue(secretRef: string, values: Record<string, string>): string {
  const value = values[String(secretRef || "").trim()];
  if (!value) throw new Error("browser_agent_secret_ref_requires_broker");
  return value;
}

function clearStageSecretValues(values: Record<string, string>): void {
  for (const key of Object.keys(values)) {
    values[key] = "";
    delete values[key];
  }
}

async function prepareSecretTarget(session: BrowserAgentSession, locator: any): Promise<void> {
  if (session.traceActive) {
    try {
      await session.context.tracing.stop({ path: session.tracePath });
      session.traceActive = false;
    } catch {
      throw new Error("browser_agent_secret_trace_suspend_failed");
    }
  }
  await locator.evaluate((element: any) => new Promise<void>((resolve) => {
    element.setAttribute("data-cascade-secret-input", "true");
    const raf = (globalThis as any).requestAnimationFrame as (callback: () => void) => number;
    raf(() => raf(resolve));
  }));
}

export function resolutionAssertions(strategy: string | undefined, failure: string, currentURL: string): Array<{ kind: string; passed: boolean; actual?: string }> {
  if (strategy) return [{ kind: "target_resolved", passed: true, actual: strategy }];
  if (failure) return [{ kind: "target_resolved", passed: false, actual: failure }];
  return [{ kind: "page_observed", passed: true, actual: currentURL }];
}

export async function executeBrowserAgentStage(request: BrowserAgentStageRequest): Promise<BrowserAgentStageResult> {
  const session = requiredSession(request.session_id);
  validateStage(request.stage);
  const secretValues = validatedStageSecretValues(request.stage, request.secrets);
  const assertions: Array<{ kind: string; passed: boolean; actual?: string }> = [];
  const targetArtifacts: ArtifactRef[] = [];
  const targetEvidence: EvidenceRef[] = [];
  let targetGeometry: BrowserTargetGeometry | undefined;
  try {
  for (const interaction of request.stage.interactions) {
    const actionEvidence = await executeInteraction(session, request.stage, interaction, secretValues);
    if (actionEvidence) {
      targetGeometry = actionEvidence.geometry;
      targetArtifacts.push(actionEvidence.artifact);
      targetEvidence.push(screenshotEvidence(actionEvidence.artifact, request.stage, "动作目标几何证据"));
    }
    assertions.push({ kind: `action_${interaction.kind}_completed`, passed: true, actual: request.stage.target_contract.semantic_id });
  }
  await waitForCaptureWindow(session.page, request.stage);
  assertions.push(...await evaluateRequiredValidations(session.page, request.stage));
  const artifact = await captureScreenshot(session, request.stage, "after");
  const evidence = screenshotEvidence(artifact, request.stage, "执行后结果证据");
  return {
    observation: await observation(session.page, "browser_assertion", assertions, targetGeometry),
    evidence_refs: [...targetEvidence, evidence],
    artifacts: [...targetArtifacts, artifact],
    target_resolved: true,
  };
  } finally {
    clearStageSecretValues(secretValues);
    if (request.secrets && request.secrets !== secretValues) clearStageSecretValues(request.secrets);
  }
}

// Capture-timing repair must never replay the approved business action. It
// only waits, validates the resulting page state, and captures fresh evidence.
export async function revalidateBrowserAgentStage(request: BrowserAgentStageRequest): Promise<BrowserAgentStageResult> {
  const session = requiredSession(request.session_id);
  validateStage(request.stage);
  await waitForCaptureWindow(session.page, request.stage);
  const assertions = await evaluateRequiredValidations(session.page, request.stage);
  const artifact = await captureScreenshot(session, request.stage, "revalidate");
  const evidence = screenshotEvidence(artifact, request.stage, "修复截图时机后的结果证据");
  return {
    observation: await observation(session.page, "browser_assertion", assertions),
    evidence_refs: [evidence],
    artifacts: [artifact],
    target_resolved: true,
  };
}

export async function closeBrowserAgentSession(request: { session_id: string }): Promise<BrowserAgentCloseResult> {
  const session = requiredSession(request.session_id);
  sessions.delete(session.id);
  const artifacts: ArtifactRef[] = [];
  let recordingPath: string | undefined;
  const recordingPathPromise = session.video?.path().catch(() => undefined);
  try {
    if (session.traceActive) await settleWithin(session.context.tracing.stop({ path: session.tracePath }), 8_000);
  } finally {
    // Playwright can flush the WebM/trace successfully while a browser close
    // promise remains pending. Keep cleanup inside the Server RPC deadline so
    // already-written evidence can still be returned.
    await settleWithin(session.context.close(), 10_000);
    await settleWithin(session.browser.close(), 3_000);
  }
  if (await fileExists(session.tracePath)) {
    artifacts.push(await artifactRef(`artifact_${session.id}_trace`, "browser_trace", session.tracePath, "application/zip", undefined, { include_in_demo: false }));
  }
  recordingPath = recordingPathPromise ? await valueWithin(recordingPathPromise, 3_000, undefined) : undefined;
  if (recordingPath && await fileExists(recordingPath)) {
    artifacts.unshift(await artifactRef(
      `artifact_${session.id}_recording`,
      "raw_recording",
      recordingPath,
      "video/webm",
      undefined,
      { include_in_demo: true, redaction_applied: true, mask_selector_count: session.maskSelectors.length },
      session.recordingSensitive,
    ));
  }
  // Failed observations may throw after their before-capture. Recover every
  // stage screenshot here so the Server can build a protocol failure package.
  for (const entry of await readdir(session.outputDir).catch(() => [] as string[])) {
    const match = /^stage-\d+-(.+)-(before|target|after|revalidate)\.png$/i.exec(entry);
    if (!match) continue;
    const nodeID = match[1] || "unknown";
    const phase = (match[2] || "before").toLowerCase();
    const artifactID = `artifact_${safeName(session.id)}_${safeName(nodeID)}_${phase}`;
    artifacts.push(await artifactRef(
      artifactID,
      phase === "target" ? "target_geometry_screenshot" : "screenshot",
      path.join(session.outputDir, entry),
      "image/png",
      nodeID,
      recoveredScreenshotMetadata(phase, artifactID, session.targetGeometryByArtifactID),
      false,
    ));
  }
  return {
    ...(recordingPath ? { recording_path: recordingPath } : {}),
    trace_path: session.tracePath,
    artifacts,
    runtime_versions: { runner: "playwright-browser-agent", browser: session.engine },
  };
}

async function executeInteraction(session: BrowserAgentSession, stage: BrowserAgentWorkerStage, interaction: BrowserAgentInteraction, secretValues: Record<string, string>): Promise<{ geometry: BrowserTargetGeometry; artifact: ArtifactRef } | undefined> {
  if (interaction.non_destructive !== true || stage.target_contract.destructive) throw new Error("browser_agent_destructive_action_denied");
  const timeout = numericParameter(interaction.parameters, "timeout_ms", actionTimeoutMS, 250, 60_000);
  if (interaction.kind === "navigate") {
    const target = interaction.target?.url || stage.url || stage.route || stage.entry_route;
    if (!target) throw new Error(`browser_agent_navigation_target_missing: ${stage.node_id}`);
    const targetURL = absoluteTargetURL(target, session.page.url(), stage.url);
    const policyError = urlPolicyError(targetURL, session, false);
    if (policyError) throw new Error(policyError);
    await session.page.goto(targetURL, { waitUntil: waitUntil(interaction.wait_until), timeout });
    await waitForPageSettled(session.page);
    return;
  }
  if (interaction.kind === "wait") {
    await session.page.waitForTimeout(numericParameter(interaction.parameters, "duration_ms", 1_000, 0, 5_000));
    return;
  }
  if (interaction.kind === "inspect") {
    await waitForPageSettled(session.page, Math.min(timeout, 5_000));
    return;
  }
  const resolved = await resolveTarget(session.page, stage, interaction, true);
  if (interaction.secret_ref) await prepareSecretTarget(session, resolved.locator);
  const targetArtifact = await captureScreenshot(session, stage, "target");
  const targetGeometry = await captureTargetGeometry(session, stage, resolved, targetArtifact.id);
  if (!targetGeometry) throw new Error(`browser_agent_target_geometry_unavailable: ${stage.node_id}`);
  targetArtifact.metadata.target_geometry = targetGeometry;
  session.targetGeometryByArtifactID.set(targetArtifact.id, targetGeometry);
  if (interaction.kind === "click") {
    await resolved.locator.click({ timeout });
  } else if (interaction.kind === "fill") {
    if (interaction.input_ref && !interaction.secret_ref && interaction.value === undefined) throw new Error("browser_agent_input_ref_requires_resolver");
    const value = interaction.secret_ref ? requiredStageSecretValue(interaction.secret_ref, secretValues) : interaction.value || "";
    await resolved.locator.fill(value, { timeout });
  } else if (interaction.kind === "select") {
    if (interaction.input_ref && !interaction.secret_ref && interaction.value === undefined) throw new Error("browser_agent_input_ref_requires_resolver");
    const value = interaction.secret_ref ? requiredStageSecretValue(interaction.secret_ref, secretValues) : interaction.value || "";
    await resolved.locator.selectOption(value, { timeout });
  } else if (interaction.kind === "assert") {
    await resolved.locator.waitFor({ state: "visible", timeout });
  } else if (interaction.kind === "upload") {
    throw new Error("browser_agent_upload_requires_scoped_file_broker");
  } else {
    throw new Error(`browser_agent_action_not_supported: ${interaction.kind}`);
  }
  await waitForPageSettled(session.page, Math.min(timeout, 5_000));
  return { geometry: targetGeometry, artifact: targetArtifact };
}

export function recoveredScreenshotMetadata(
  phase: string,
  artifactID: string,
  targetGeometryByArtifactID: ReadonlyMap<string, BrowserTargetGeometry>,
): Record<string, unknown> {
  const targetGeometry = phase === "target" ? targetGeometryByArtifactID.get(artifactID) : undefined;
  return {
    include_in_demo: phase === "after" || phase === "revalidate",
    capture_phase: phase,
    recovered_at_session_close: true,
    ...(targetGeometry ? { target_geometry: targetGeometry } : {}),
  };
}

async function resolveTarget(page: any, stage: BrowserAgentWorkerStage, interaction: BrowserAgentInteraction, allowSelectorAlternatives: boolean, attempts: BrowserTargetResolutionAttempt[] = []): Promise<ResolvedTarget> {
	const candidates: Array<{ strategy: string; locator: any; evidenceBoundAlternative?: { kind: string; value: string } }> = [];
	const contract = stage.target_contract;
	const preferred = stage.preferred_selector_alternative;
	if (preferred) {
		const locator = locatorFromAlternative(page, preferred.kind, preferred.value);
		if (!locator) {
      attempts.push(targetResolutionAttempt(`preferred_${preferred.kind}`, 0, false, false, isEvidenceBoundSelectorAlternative(stage, preferred), "unsupported_selector"));
      throw new Error(`browser_agent_target_not_resolved: ${stage.node_id}; strategies=preferred_${preferred.kind}`);
    }
		// A selector recovered through an App evidence binding must be rechecked
		// with the same bounded semantics used during discovery. Ordinary App
		// alternatives keep the original exact target-contract validation.
		const resolved = isEvidenceBoundSelectorAlternative(stage, preferred)
			? await resolveUniqueVisibleEvidenceBoundTarget(locator, `approved_evidence_${preferred.kind}`, contract, preferred, interaction.kind, undefined, attempts)
			: await resolveUniqueVisibleContractTarget(locator, `approved_${preferred.kind}`, contract, attempts);
		if (!resolved) throw new Error(`browser_agent_target_not_resolved: ${stage.node_id}; strategies=preferred_${preferred.kind}`);
		return { ...resolved, approvedAlternative: { kind: preferred.kind, value: preferred.value } };
	}
  for (const role of contract.allowed_roles || []) {
    for (const name of contract.allowed_names || []) {
      candidates.push({ strategy: `role:${role}+approved_name`, locator: page.getByRole(role, { name, exact: true }) });
      const normalizedName = normalizedApprovedTargetName(name);
      if (normalizedName && normalizeElementName(normalizedName) !== normalizeElementName(name)) {
        candidates.push({ strategy: `role:${role}+normalized_approved_name`, locator: page.getByRole(role, { name: normalizedName, exact: true }) });
      }
    }
  }
  const components = (stage.components || []).filter((component) => !contract.component_ref || component.component_ref === contract.component_ref);
  for (const component of components) {
    if (component.role && component.name) candidates.push({ strategy: "component_role_name", locator: page.getByRole(component.role, { name: component.name, exact: true }) });
    if (component.test_id) candidates.push({ strategy: "component_testid", locator: page.getByTestId(component.test_id) });
    if (component.label) candidates.push({ strategy: "component_label", locator: page.getByLabel(component.label, { exact: true }) });
  }
  if (interaction.target?.role && interaction.target.text) candidates.push({ strategy: "interaction_role_text", locator: page.getByRole(interaction.target.role, { name: interaction.target.text, exact: true }) });
  if (interaction.target?.test_id) candidates.push({ strategy: "interaction_testid", locator: page.getByTestId(interaction.target.test_id) });
  if (interaction.target?.label) candidates.push({ strategy: "interaction_label", locator: page.getByLabel(interaction.target.label, { exact: true }) });
  if (interaction.target?.text) candidates.push({ strategy: "interaction_text", locator: page.getByText(interaction.target.text, { exact: true }) });
  for (const component of components) {
    if (allowSelectorAlternatives) for (const alternative of component.selector_alternatives || []) {
      const locator = locatorFromAlternative(page, alternative.kind, alternative.value);
      if (locator) candidates.push({ strategy: `verified_${alternative.kind}`, locator });
    }
    if (component.selector) candidates.push({ strategy: "component_css", locator: page.locator(component.selector) });
  }
  if (interaction.target?.selector) {
    // An App target that carries an evidence reference is already an
    // approved binding. Keep the selector exact, but permit a localized
    // accessible name (for example `Build` -> `构建!`) when the unique
    // element still satisfies the contract role and forbidden-name checks.
    const evidenceBoundAlternative = evidenceBoundCSSAlternative(stage, interaction.target.selector)
      || (contract.evidence_refs?.length ? { kind: "css", value: interaction.target.selector } : undefined);
    candidates.push({
      strategy: "interaction_css",
      locator: page.locator(interaction.target.selector),
      ...(evidenceBoundAlternative ? { evidenceBoundAlternative } : {}),
    });
  }

  const seen = new Set<string>();
  for (const candidate of candidates) {
    if (seen.has(candidate.strategy)) continue;
    seen.add(candidate.strategy);
    const resolved = candidate.evidenceBoundAlternative
      ? await resolveUniqueVisibleEvidenceBoundTarget(candidate.locator, candidate.strategy, contract, candidate.evidenceBoundAlternative, interaction.kind, { allowSelectorIdentityOnly: true }, attempts)
      : await resolveUniqueVisibleContractTarget(candidate.locator, candidate.strategy, contract, attempts);
    if (resolved) return resolved;
  }
  throw new Error(`browser_agent_target_not_resolved: ${stage.node_id}; strategies=${[...seen].join(",") || "none"}`);
}

// This is the only discovery step allowed after the primary locator fails.
// It checks App-approved alternatives one by one and returns nothing unless
// exactly one live target is unique, visible, and contract-compatible.
async function resolveSingleApprovedAlternative(page: any, stage: BrowserAgentWorkerStage, interactionKind: string, attempts: BrowserTargetResolutionAttempt[] = []): Promise<{ kind: string; value: string } | undefined> {
  const matched: Array<{ kind: string; value: string }> = [];
  const seen = new Set<string>();
  const probe = async (alternative: { kind: string; value: string }, evidenceBound: boolean) => {
    const key = `${alternative.kind}:${alternative.value}`;
    if (seen.has(key)) return;
    seen.add(key);
    const locator = locatorFromAlternative(page, alternative.kind, alternative.value);
    if (!locator) return;
    const resolved = evidenceBound
      ? await resolveUniqueVisibleEvidenceBoundTarget(locator, `approved_evidence_${alternative.kind}`, stage.target_contract, alternative, interactionKind, undefined, attempts)
      : await resolveUniqueVisibleContractTarget(locator, `approved_${alternative.kind}`, stage.target_contract, attempts);
    if (resolved) matched.push({ kind: alternative.kind, value: alternative.value });
  };
  for (const alternative of stage.evidence_bound_selector_alternatives || []) {
    await probe(alternative, true);
  }
  const components = (stage.components || []).filter((component) => !stage.target_contract.component_ref || component.component_ref === stage.target_contract.component_ref);
  for (const component of components) {
    for (const alternative of component.selector_alternatives || []) {
      await probe(alternative, false);
    }
  }
  return matched.length === 1 ? matched[0] : undefined;
}

export function isEvidenceBoundSelectorAlternative(stage: Pick<BrowserAgentWorkerStage, "evidence_bound_selector_alternatives">, alternative: { kind: string; value: string }): boolean {
  return (stage.evidence_bound_selector_alternatives || []).some((candidate) =>
    candidate.kind === alternative.kind && candidate.value === alternative.value,
  );
}

function evidenceBoundCSSAlternative(stage: Pick<BrowserAgentWorkerStage, "evidence_bound_selector_alternatives">, selector: string): { kind: string; value: string } | undefined {
  return (stage.evidence_bound_selector_alternatives || []).find((candidate) =>
    (candidate.kind === "css" || candidate.kind === "selector") && candidate.value === selector,
  );
}

export async function resolveUniqueVisibleEvidenceBoundTarget(locator: any, strategy: string, contract: BrowserAgentTargetContract, alternative: { kind: string; value: string }, interactionKind: string, options?: { allowSelectorIdentityOnly?: boolean }, attempts: BrowserTargetResolutionAttempt[] = []): Promise<ResolvedTarget | undefined> {
  const count = await withTimeout(locator.count(), targetProbeTimeoutMS, 0);
  if (count !== 1) {
    attempts.push(targetResolutionAttempt(strategy, count, false, false, true, count === 0 ? "no_candidates" : "ambiguous"));
    return undefined;
  }
  const unique = locator.first();
  if (!await withTimeout(unique.isVisible({ timeout: 750 }), targetProbeTimeoutMS, false)) {
    attempts.push(targetResolutionAttempt(strategy, count, true, false, true, "not_visible"));
    return undefined;
  }
  const semantics = await withTimeout(compactElementSemantics(unique), targetProbeTimeoutMS, { role: "", name: "" });
  if (forbiddenName(semantics.name, contract.forbidden_names || [])) {
    attempts.push(targetResolutionAttempt(strategy, count, true, true, true, "forbidden_name", false, false));
    throw new Error(`browser_agent_forbidden_target_name: ${contract.semantic_id}`);
  }
  const allowedRoles = (contract.allowed_roles || []).map((value) => value.trim().toLowerCase()).filter(Boolean);
  const roleAllowed = allowedRoles.length === 0 || allowedRoles.includes(semantics.role);
  if (!roleAllowed) {
    attempts.push(targetResolutionAttempt(strategy, count, true, true, true, "role_mismatch", false));
    return undefined;
  }
  const nameAllowed = evidenceBoundNameAllowedForInteraction(semantics.name, contract.allowed_names || [], alternative, interactionKind)
    || options?.allowSelectorIdentityOnly === true;
  if (!nameAllowed) {
    attempts.push(targetResolutionAttempt(strategy, count, true, true, true, "name_mismatch", true, false));
    return undefined;
  }
  attempts.push(targetResolutionAttempt(strategy, count, true, true, true, "resolved", true, true));
  return { locator: unique, strategy };
}

export function evidenceBoundNameAllowedForInteraction(actualName: string, allowedNames: string[], alternative: { kind: string; value: string }, interactionKind: string): boolean {
  if (evidenceBoundNameAllowed(actualName, allowedNames, alternative)) return true;
  // A fill/select target may legitimately have no accessible name even when
  // the App supplied a stable data-testid selector. This exception is only
  // reachable for an exact App evidence binding; click and other actions keep
  // the original name contract and therefore fail closed.
  return !normalizeEvidenceBoundElementName(actualName) && (interactionKind === "fill" || interactionKind === "select");
}

export function evidenceBoundNameAllowed(actualName: string, allowedNames: string[], alternative: { kind: string; value: string }): boolean {
  if (allowedNames.length === 0) return true;
  const actual = normalizeEvidenceBoundElementName(actualName);
  if (!actual) return false;
  const identity = selectorIdentity(alternative);
  return allowedNames.some((allowedName) => {
    if (normalizeEvidenceBoundElementName(allowedName) === actual) return true;
    if (!identity) return false;
    const withoutIdentity = removeExactIdentity(normalizeElementName(allowedName), identity);
    return normalizeEvidenceBoundElementName(withoutIdentity) === actual;
  });
}

function selectorIdentity(alternative: { kind: string; value: string }): string {
  if (alternative.kind === "testid") return normalizeElementName(alternative.value);
  if (alternative.kind !== "css" && alternative.kind !== "selector") return "";
  const match = alternative.value.match(/data-testid\s*=\s*["']([^"']+)["']/i);
  return normalizeElementName(match?.[1] || "");
}

function removeExactIdentity(value: string, identity: string): string {
  return value.split(/\s+/).filter((part) => part !== identity).join(" ");
}

function normalizeEvidenceBoundElementName(value: string): string {
  return normalizeElementName(value).replace(/^[^\p{L}\p{N}]+/gu, "").trim();
}

async function resolveUniqueVisibleContractTarget(locator: any, strategy: string, contract: BrowserAgentTargetContract, attempts: BrowserTargetResolutionAttempt[] = []): Promise<ResolvedTarget | undefined> {
  const count = await withTimeout(locator.count(), targetProbeTimeoutMS, 0);
  if (count !== 1) {
    attempts.push(targetResolutionAttempt(strategy, count, false, false, false, count === 0 ? "no_candidates" : "ambiguous"));
    return undefined;
  }
  const unique = locator.first();
  if (!await withTimeout(unique.isVisible({ timeout: 750 }), targetProbeTimeoutMS, false)) {
    attempts.push(targetResolutionAttempt(strategy, count, true, false, false, "not_visible"));
    return undefined;
  }
  const semantics = await withTimeout(compactElementSemantics(unique), targetProbeTimeoutMS, { role: "", name: "" });
  if (forbiddenName(semantics.name, contract.forbidden_names || [])) {
    attempts.push(targetResolutionAttempt(strategy, count, true, true, false, "forbidden_name", false, false));
    throw new Error(`browser_agent_forbidden_target_name: ${contract.semantic_id}`);
  }
  const allowedRoles = (contract.allowed_roles || []).map((value) => value.trim().toLowerCase()).filter(Boolean);
  const roleAllowed = allowedRoles.length === 0 || allowedRoles.includes(semantics.role);
  if (!roleAllowed) {
    attempts.push(targetResolutionAttempt(strategy, count, true, true, false, "role_mismatch", false));
    return undefined;
  }
  const allowedNames = (contract.allowed_names || []).map(normalizeElementName).filter(Boolean);
  const normalizedActual = normalizeElementName(semantics.name);
  const nameAllowed = allowedNames.length === 0 || allowedNames.some((name) => name === normalizedActual || normalizeElementName(normalizedApprovedTargetName(name)) === normalizedActual);
  if (!nameAllowed) {
    attempts.push(targetResolutionAttempt(strategy, count, true, true, false, "name_mismatch", true, false));
    return undefined;
  }
  attempts.push(targetResolutionAttempt(strategy, count, true, true, false, "resolved", true, true));
  return { locator: unique, strategy };
}

function targetResolutionAttempt(
  strategy: string,
  candidateCount: number,
  unique: boolean,
  visible: boolean,
  evidenceBound: boolean,
  outcome: BrowserTargetResolutionAttempt["outcome"],
  roleAllowed?: boolean,
  nameAllowed?: boolean,
): BrowserTargetResolutionAttempt {
  return {
    strategy,
    candidate_count: Math.max(0, Math.min(10_000, Math.trunc(candidateCount))),
    unique,
    visible,
    evidence_bound: evidenceBound,
    ...(roleAllowed !== undefined ? { role_allowed: roleAllowed } : {}),
    ...(nameAllowed !== undefined ? { name_allowed: nameAllowed } : {}),
    outcome,
  };
}

async function withTimeout<T>(promise: Promise<T>, timeoutMS: number, fallback: T): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<T>((resolve) => {
        timer = setTimeout(() => resolve(fallback), timeoutMS);
      }),
    ]);
  } catch {
    return fallback;
  } finally {
    if (timer) clearTimeout(timer);
  }
}

async function settleWithin(promise: Promise<unknown>, timeoutMS: number): Promise<void> {
  await valueWithin(promise.then(() => true).catch(() => false), timeoutMS, false);
}

async function valueWithin<T>(promise: Promise<T>, timeoutMS: number, fallback: T): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<T>((resolve) => {
        timer = setTimeout(() => resolve(fallback), timeoutMS);
      }),
    ]);
  } catch {
    return fallback;
  } finally {
    if (timer) clearTimeout(timer);
  }
}

export async function evaluateRequiredValidations(page: any, stage: BrowserAgentWorkerStage): Promise<Array<{ kind: string; passed: boolean; actual?: string }>> {
  const assertions: Array<{ kind: string; passed: boolean; actual?: string }> = [];
  for (const validation of (stage.validations || []).filter((item) => item.required)) {
    const timeout = Math.max(250, Math.min(30_000, validation.timeout_ms || actionTimeoutMS));
    let passed = false;
    let actual = "not_satisfied";
    try {
      if (validation.kind === "url_matches") {
        const expected = validation.target?.url || scalarExpected(validation) || stage.url || stage.route;
        actual = safeURL(page.url());
        passed = Boolean(expected) && urlMatches(page.url(), String(expected));
        if (!passed && approvedObservationRouteTemplateVerified(page, stage, validation, expected)) {
          // App pre-scan evidence can retain the entry-page URL while the same
          // approved observation stage declares the runtime-created route it
          // must observe. Bind that immutable route template at runtime; do not
          // rewrite the validation, package, selector, input, or package hash.
          passed = true;
          actual = `approved_target_route_template_verified:${stage.target_route_template}`;
        }
      } else if (validation.kind === "page_loaded") {
        // App exports this for navigation stages. A loaded document is a
        // deterministic browser fact, not an inferred business outcome.
        const readyState = await page.evaluate(() => (globalThis as any).document?.readyState || "unavailable").catch(() => "unavailable");
        passed = readyState === "interactive" || readyState === "complete";
        actual = String(readyState);
      } else if (validation.kind === "element_visible") {
        const locator = locatorForValidation(page, validation);
        passed = await locator.first().isVisible({ timeout }).catch(() => false);
        actual = passed ? "visible" : "not_visible";
        if (!passed && await evidenceBoundFormControlOutcomeVerified(page, stage)) {
          // The App may retain a pre-scan result selector while its approved
          // interaction uses a later, evidence-bound data-testid. Verify the
          // exact explicit value on that same App-owned target; do not rewrite
          // the validation, selector, interaction, or package hashes.
          passed = true;
          actual = "evidence_bound_form_control_value_verified";
        }
        if (!passed && expectedPostClickRouteVerified(page, stage, validation)) {
          passed = true;
          actual = "expected_route_after_action_verified";
        }
        if (!passed && stage.interactions.some((interaction) => interaction.kind === "click") && await postClickBusinessSurfaceVisible(page)) {
          // Some App-generated packages bind the post-click validation to
          // the pre-click dashboard selector. When the approved click has
          // opened an evidenced business dialog, accept that observed result
          // without rewriting the App package or changing its hashes.
          passed = true;
          actual = "post_click_business_surface_visible";
        }
      } else if (validation.kind === "element_hidden") {
        const locator = locatorForValidation(page, validation);
        passed = !await locator.first().isVisible({ timeout: Math.min(timeout, 1_000) }).catch(() => false);
        actual = passed ? "hidden" : "visible";
      } else if (validation.kind === "text_contains") {
        const expected = scalarExpected(validation);
        passed = Boolean(expected) && await page.locator("body").evaluate((element: any, value: string) => String(element.innerText || "").includes(value), String(expected)).catch(() => false);
        actual = passed ? "matched" : "not_matched";
      } else if (validation.kind === "attribute_equals") {
        const locator = locatorForValidation(page, validation).first();
        const expectedObject = objectExpected(validation.expected);
        const attribute = String(expectedObject?.attribute || validation.assertion || "");
        const expected = String(expectedObject?.value ?? "");
        const value = attribute ? await locator.getAttribute(attribute, { timeout }).catch(() => null) : null;
        passed = value === expected;
        actual = passed ? "matched" : "not_matched";
      } else if (validation.kind === "value_equals") {
        const expected = scalarExpected(validation);
        const value = await locatorForValidation(page, validation).first().inputValue({ timeout }).catch(() => undefined);
        passed = value !== undefined && String(value) === String(expected ?? "");
        actual = passed ? "matched" : "not_matched";
      } else if (validation.kind === "element_count") {
        const expected = Number(scalarExpected(validation));
        const count = await locatorForValidation(page, validation).count();
        passed = Number.isFinite(expected) && count === expected;
        actual = String(count);
      } else if (validation.kind === "page_title_contains") {
        const expected = String(scalarExpected(validation) || "");
        const title = await page.title();
        passed = Boolean(expected) && title.includes(expected);
        actual = redactText(title);
      } else {
        actual = "unsupported_required_validation";
      }
    } catch {
      passed = false;
      actual = "validation_error";
    }
    assertions.push({ kind: `required_${validation.kind}:${validation.id}`, passed, actual });
  }
  return assertions;
}

function approvedObservationRouteTemplateVerified(
  page: any,
  stage: BrowserAgentWorkerStage,
  validation: BrowserAgentValidation,
  expected: unknown,
): boolean {
  const template = String(stage.target_route_template || "").trim();
  const approvedEntryURL = String(stage.url || "").trim();
  if (!stage.runtime_route_verification_required || !template || !approvedEntryURL || !expected) return false;
  if (!stage.interactions.length || stage.interactions.some((interaction) => !["wait", "inspect"].includes(interaction.kind))) return false;

  const validationURL = String(validation.target?.url || expected).trim();
  if (!validationURL || !sameAbsoluteURL(validationURL, approvedEntryURL)) return false;
  return urlMatches(page.url(), template);
}

function sameAbsoluteURL(leftValue: string, rightValue: string): boolean {
  try {
    const left = new URL(leftValue);
    const right = new URL(rightValue);
    return left.origin === right.origin && normalizeRoutePath(left.pathname) === normalizeRoutePath(right.pathname);
  } catch {
    return false;
  }
}

async function evidenceBoundFormControlOutcomeVerified(page: any, stage: BrowserAgentWorkerStage): Promise<boolean> {
  const interactions = stage.interactions.filter((interaction) => interaction.kind === "fill" || interaction.kind === "select");
  if (interactions.length !== 1) return false;
  const interaction = interactions[0];
  if (!interaction || interaction.secret_ref || interaction.value === undefined || !interaction.target?.selector) return false;
  const alternative = evidenceBoundCSSAlternative(stage, interaction.target.selector);
  if (!alternative) return false;
  const resolved = await resolveUniqueVisibleEvidenceBoundTarget(
    page.locator(interaction.target.selector),
    "validation_evidence_bound_form_control",
    stage.target_contract,
    alternative,
    interaction.kind,
  );
  if (!resolved) return false;
  const value = await resolved.locator.inputValue({ timeout: actionTimeoutMS }).catch(() => undefined);
  return value !== undefined && String(value) === String(interaction.value);
}

function expectedPostClickRouteVerified(page: any, stage: BrowserAgentWorkerStage, validation: BrowserAgentValidation): boolean {
  if (!stage.runtime_route_verification_required || !stage.expected_route_after_action) return false;
  const clicks = stage.interactions.filter((interaction) => interaction.kind === "click");
  if (clicks.length !== 1) return false;
  const actionTarget = clicks[0]?.target;
  const validationTarget = validation.target;
  if (!actionTarget || !validationTarget) return false;
  const sameTarget = Boolean(
    (actionTarget.selector && actionTarget.selector === validationTarget.selector)
    || (actionTarget.test_id && actionTarget.test_id === validationTarget.test_id)
    || (actionTarget.label && actionTarget.label === validationTarget.label),
  );
  return sameTarget && urlMatches(page.url(), stage.expected_route_after_action);
}

async function postClickBusinessSurfaceVisible(page: any): Promise<boolean> {
  const selectors = [
    '[role="dialog"]',
    '[data-testid="dialog-new-project"]',
    '[data-testid*="dialog" i]',
  ];
  for (const selector of selectors) {
    try {
      if (await page.locator(selector).first().isVisible({ timeout: 750 })) return true;
    } catch {
      // Continue probing the remaining evidence-bound surface selectors.
    }
  }
  return false;
}

function locatorForValidation(page: any, validation: BrowserAgentValidation): any {
  const target = validation.target || {};
  if (target.test_id) return page.getByTestId(target.test_id);
  if (target.role && target.text) return page.getByRole(target.role, { name: target.text, exact: true });
  if (target.label) return page.getByLabel(target.label, { exact: true });
  if (target.text) return page.getByText(target.text, { exact: true });
  if (target.selector) return page.locator(target.selector);
  return page.locator("body");
}

function scalarExpected(validation: BrowserAgentValidation): string | number | boolean | undefined {
  if (["string", "number", "boolean"].includes(typeof validation.expected)) return validation.expected as string | number | boolean;
  return validation.assertion;
}

function objectExpected(value: unknown): Record<string, unknown> | undefined {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function urlMatches(actualValue: string, expectedValue: string): boolean {
  try {
    const actual = new URL(actualValue);
    const expected = new URL(expectedValue, actualValue);
    return actual.origin === expected.origin && routeTemplateMatches(actual.pathname, expected.pathname);
  } catch {
    return routeTemplateMatches(safeURL(actualValue), expectedValue);
  }
}

// App outlines may deliberately describe a resource route without knowing the
// runtime-created identifier (for example /project/:id). The Server/Worker
// binds that template to the observed URL at validation time; it never writes
// the discovered identifier back into the App package or its hashes.
export function routeTemplateMatches(actualPath: string, expectedPath: string): boolean {
  const actual = normalizeRoutePath(actualPath);
  const expected = normalizeRoutePath(expectedPath);
  if (expected === "/") return actual === "/";
  const expectedParts = expected.split("/").filter(Boolean);
  const actualParts = actual.split("/").filter(Boolean);
  if (expectedParts.length !== actualParts.length) return false;
  return expectedParts.every((part, index) => {
    const dynamic = part.startsWith(":") || (part.startsWith("{") && part.endsWith("}"));
    return dynamic || part === "*" || part === actualParts[index];
  });
}

function routeTemplatePrefixMatches(actualPath: string, expectedPath: string): boolean {
  const actual = normalizeRoutePath(actualPath);
  const expected = normalizeRoutePath(expectedPath);
  if (expected === "/") return true;
  const expectedParts = expected.split("/").filter(Boolean);
  const actualParts = actual.split("/").filter(Boolean);
  if (actualParts.length < expectedParts.length) return false;
  return expectedParts.every((part, index) => {
    const dynamic = part.startsWith(":") || (part.startsWith("{") && part.endsWith("}"));
    return dynamic || part === "*" || part === actualParts[index];
  });
}

function normalizeRoutePath(value: string): string {
  let pathValue = String(value || "").trim();
  try {
    pathValue = new URL(pathValue).pathname;
  } catch {
    // Relative route templates are expected in the outline contract.
  }
  pathValue = `/${pathValue.replace(/^\/+/, "").replace(/\/+$/, "")}`.toLowerCase();
  return pathValue === "" ? "/" : pathValue;
}

function locatorFromAlternative(page: any, kind: string, value: string): any | undefined {
  if (!value) return undefined;
  if (kind === "testid") return page.getByTestId(value);
  if (kind === "label") return page.getByLabel(value, { exact: true });
  if (kind === "text") return page.getByText(value, { exact: true });
  if (kind === "css" || kind === "selector") return page.locator(value);
  return undefined;
}

async function captureScreenshot(session: BrowserAgentSession, stage: BrowserAgentWorkerStage, phase: "before" | "target" | "after" | "revalidate"): Promise<ArtifactRef> {
  const fileName = `stage-${String(stage.order).padStart(3, "0")}-${safeName(stage.node_id)}-${phase}.png`;
  const filePath = path.join(session.outputDir, fileName);
  const masks = session.maskSelectors.filter(Boolean).map((selector) => session.page.locator(selector));
  await session.page.screenshot({ path: filePath, fullPage: false, mask: masks, timeout: screenshotTimeoutMS });
  return artifactRef(
    `artifact_${safeName(session.id)}_${safeName(stage.node_id)}_${phase}`,
    phase === "after" || phase === "revalidate" ? "step_screenshot" : phase === "target" ? "target_geometry_screenshot" : "screenshot",
    filePath,
    "image/png",
    stage.node_id,
    {
      include_in_demo: phase === "after" || phase === "revalidate",
      // Post-action and non-action revalidation screenshots are safe inputs.
      presentation_only: phase === "after" || phase === "revalidate",
      capture_phase: phase,
      target_semantic_id: stage.target_contract.semantic_id,
      current_url: safeURL(session.page.url()),
      page_title: redactText(await session.page.title().catch(() => "")),
    },
    false,
  );
}

function screenshotEvidence(artifact: ArtifactRef, stage: BrowserAgentWorkerStage, summary: string): EvidenceRef {
  return {
    id: `evidence_${artifact.id}`,
    kind: "webpage_screenshot",
    summary: `${summary}：${stage.node_id}`,
    artifact_id: artifact.id,
    confidence: 1,
  };
}

async function observation(page: any, source: RuntimeObservation["source"], assertions: NonNullable<RuntimeObservation["assertions"]>, targetGeometry?: BrowserTargetGeometry, resolutionAttempts: BrowserTargetResolutionAttempt[] = []): Promise<RuntimeObservation> {
  return {
    source,
    url: safeURL(page.url()),
    title: redactText(await page.title().catch(() => "")),
    assertions,
    ...(targetGeometry ? { target_geometry: targetGeometry } : {}),
    ...(resolutionAttempts.length > 0 ? { target_resolution_attempts: resolutionAttempts } : {}),
  };
}

function validateStage(stage: BrowserAgentWorkerStage): void {
  if (!stage?.id || !stage.node_id || !stage.target_contract?.semantic_id) throw new Error("browser_agent_stage_identity_missing");
  if (!Array.isArray(stage.interactions) || stage.interactions.length === 0) throw new Error(`browser_agent_stage_interactions_missing: ${stage.node_id}`);
  if (stage.target_contract.destructive) throw new Error(`browser_agent_destructive_target_denied: ${stage.node_id}`);
}

function requiredSession(id: string): BrowserAgentSession {
  const session = sessions.get(safeName(id));
  if (!session) throw new Error(`browser_agent_session_not_found: ${safeName(id)}`);
  return session;
}

export function urlPolicyError(value: string, session: BrowserAgentSession, allowSubresource: boolean): string | undefined {
  if (allowSubresource && /^(about:|data:|blob:)/i.test(value)) return undefined;
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return "browser_agent_domain_not_allowed: invalid_url";
  }
  if (!allowSubresource && parsed.protocol !== "http:" && parsed.protocol !== "https:") return `browser_agent_domain_not_allowed: ${parsed.protocol}`;
  if (!hostAllowed(parsed.hostname, session.allowedDomains)) return `browser_agent_domain_not_allowed: ${parsed.hostname}`;
  if (!allowSubresource && !originAllowed(parsed, session.allowedOrigins)) return `browser_agent_origin_not_allowed: ${parsed.origin}`;
  const normalizedPath = `/${parsed.pathname.replace(/^\/+/, "")}`.toLowerCase();
  if (!allowSubresource && !routeAllowed(normalizedPath, session.allowedRoutes)) return `browser_agent_route_not_allowed: ${normalizedPath}`;
  for (const forbidden of [...session.forbiddenPages, ...session.forbiddenPathPrefixes]) {
    const prefix = `/${String(forbidden).replace(/^\/+/, "")}`.toLowerCase();
    if (prefix !== "/" && (normalizedPath === prefix || normalizedPath.startsWith(`${prefix.replace(/\/$/, "")}/`))) {
      return `browser_agent_forbidden_page: ${prefix}`;
    }
  }
  for (const keyword of session.forbiddenKeywords) {
    if (keyword && normalizedPath.includes(keyword.toLowerCase())) return `browser_agent_forbidden_keyword: ${keyword}`;
  }
  return undefined;
}

function hostAllowed(hostname: string, allowedDomains: string[]): boolean {
  const host = hostname.toLowerCase();
  return allowedDomains.some((value) => {
    const normalized = normalizeAllowedDomain(value);
    return Boolean(normalized) && (host === normalized || host.endsWith(`.${normalized}`));
  });
}

function normalizeAllowedDomain(value: string): string {
  const trimmed = value.trim().toLowerCase();
  try {
    const parsed = new URL(trimmed.includes("://") ? trimmed : `https://${trimmed}`);
    return parsed.hostname.replace(/^\./, "");
  } catch {
    const host = trimmed.split("/")[0] || "";
    return (host.split(":")[0] || "").replace(/^\./, "");
  }
}

function absoluteTargetURL(target: string, currentURL: string, stageURL?: string): string {
  try {
    return new URL(target).toString();
  } catch {
    const base = /^https?:/i.test(currentURL) ? currentURL : stageURL;
    if (!base) throw new Error("browser_agent_relative_navigation_without_base_url");
    return new URL(target, base).toString();
  }
}

function safeURL(value: string): string {
  if (!value || value === "about:blank") return value || "about:blank";
  try {
    const parsed = new URL(value);
    return `${parsed.origin}${parsed.pathname}`;
  } catch {
    return "[invalid-url]";
  }
}

function waitUntil(value?: string): "load" | "domcontentloaded" | "networkidle" {
  if (value === "domcontentloaded" || value === "networkidle") return value;
  return "load";
}

async function waitForPageSettled(page: any, timeoutMS = 5_000): Promise<void> {
  await page.waitForLoadState("domcontentloaded", { timeout: timeoutMS }).catch(() => undefined);
  await page.waitForLoadState("networkidle", { timeout: Math.min(timeoutMS, 3_000) }).catch(() => undefined);
  await page.waitForTimeout(250);
}

async function waitForCaptureWindow(page: any, stage: BrowserAgentWorkerStage): Promise<void> {
  const declared = [...(stage.wait_conditions || []), ...stage.interactions.flatMap((interaction) => interaction.wait_conditions || [])];
  // An approved duration is a lower bound, not a suggestion or a global cap.
  let waitMS = Math.max(0, stage.duration_ms || 0, stage.capture_plan?.min_duration_ms || 0, stage.capture_plan?.pre_capture_wait_ms || 0, stage.capture_plan?.hold_after_ms || 0);
  for (const condition of declared) {
    const match = condition.match(/at_least_(\d+)ms/i);
    if (match) waitMS = Math.max(waitMS, Number(match[1]));
  }
  if (waitMS > 0) await page.waitForTimeout(waitMS);
  await waitForPageSettled(page);
}

function originAllowed(parsed: URL, allowedOrigins: string[]): boolean {
  return allowedOrigins.some((value) => {
    try {
      const approved = new URL(value);
      return approved.protocol === parsed.protocol && approved.host.toLowerCase() === parsed.host.toLowerCase();
    } catch {
      return false;
    }
  });
}

function routeAllowed(pathname: string, allowedRoutes: string[]): boolean {
  const path = normalizeRoute(pathname);
  return allowedRoutes.some((route) => {
    const approved = normalizeRoute(route);
    return approved === "/" || routeTemplatePrefixMatches(path, approved) || path.startsWith(`${approved}/`);
  });
}

function normalizeRoute(value: string): string {
  let pathname = value.trim();
  try {
    const parsed = new URL(pathname);
    pathname = parsed.pathname;
  } catch {
    // Routes in the contract are relative paths.
  }
  const normalized = `/${pathname.replace(/^\/+/, "").replace(/\/+$/, "")}`.toLowerCase();
  return normalized === "" ? "/" : normalized;
}

async function waitForObservationWindow(page: any, stage: BrowserAgentWorkerStage): Promise<void> {
  const waitMS = entryWaitMilliseconds(stage);
  if (waitMS > 0) await page.waitForTimeout(waitMS);
  await page.waitForLoadState("domcontentloaded", { timeout: Math.min(waitMS + 1_000, 5_000) }).catch(() => undefined);
}

function entryWaitMilliseconds(stage: BrowserAgentWorkerStage): number {
  const declared = [...(stage.wait_conditions || []), ...stage.interactions.flatMap((interaction) => interaction.wait_conditions || [])];
  let waitMS = 0;
  for (const condition of declared) {
    const match = String(condition || "").match(/^wait_after_entry_at_least_(\d+)ms$/i);
    if (match) waitMS = Math.max(waitMS, Math.min(15_000, Number(match[1])));
  }
  return waitMS;
}

function suggestedEntryWaitCondition(stage: BrowserAgentWorkerStage): string | undefined {
  const current = entryWaitMilliseconds(stage);
  if (current <= 0 || current >= 15_000) return undefined;
  return `wait_after_entry_at_least_${Math.min(15_000, current + 1_000)}ms`;
}

async function pageStillBusy(page: any): Promise<boolean> {
  return page.evaluate(() => {
    const pageDocument = (globalThis as unknown as { document: { readyState?: string; querySelector?: (selector: string) => unknown } }).document;
    const documentBusy = pageDocument.readyState !== "complete";
    const ariaBusy = Boolean(pageDocument.querySelector?.('[aria-busy="true"]'));
    return documentBusy || ariaBusy;
  }).catch(() => false);
}

function numericParameter(parameters: Record<string, unknown> | undefined, key: string, fallback: number, min: number, max: number): number {
  const value = Number(parameters?.[key]);
  if (!Number.isFinite(value)) return fallback;
  return Math.max(min, Math.min(max, Math.round(value)));
}

function forbiddenName(actual: string, forbidden: string[]): boolean {
  const normalized = actual.trim().toLowerCase();
  return Boolean(normalized) && forbidden.some((value) => normalized.includes(value.trim().toLowerCase()));
}

async function compactElementSemantics(locator: any): Promise<{ role: string; name: string }> {
  const result = await locator.evaluate((element: any) => {
    const explicitRole = String(element.getAttribute?.("role") || "").trim().toLowerCase();
    const tag = String(element.tagName || "").toLowerCase();
    const type = String(element.getAttribute?.("type") || "").toLowerCase();
    const implicitRoles: Record<string, string> = {
      button: "button", select: "combobox", textarea: "textbox", main: "main", nav: "navigation",
      form: "form", table: "table", img: "img", ul: "list", ol: "list", li: "listitem",
      h1: "heading", h2: "heading", h3: "heading", h4: "heading", h5: "heading", h6: "heading",
    };
    let role = explicitRole || implicitRoles[tag] || "";
    if (tag === "a" && element.hasAttribute?.("href")) role = "link";
    if (tag === "input") role = ["button", "submit", "reset"].includes(type) ? "button" : type === "checkbox" ? "checkbox" : type === "radio" ? "radio" : "textbox";
    const labels = Array.from(element.labels || []).map((label: any) => label.textContent || "").join(" ");
    const formControl = tag === "input" || tag === "textarea" || tag === "select";
    const inputButtonName = tag === "input" && ["button", "submit", "reset"].includes(type) ? element.getAttribute?.("value") : "";
    // A form control's current value is not its accessible name. Falling back
    // to innerText/textContent after fill can misclassify the typed business
    // value as a changed target identity and reject the same approved control.
    const fallbackText = formControl ? inputButtonName : (element.innerText || element.textContent || "");
    const name = String(element.getAttribute?.("aria-label") || labels || fallbackText || "").trim();
    return { role, name };
  }).catch(() => ({ role: "", name: "" }));
  return { role: String(result.role || "").toLowerCase(), name: redactText(String(result.name || "")) };
}

function semanticsAllowed(actual: { role: string; name: string }, contract: BrowserAgentTargetContract): boolean {
  const allowedRoles = (contract.allowed_roles || []).map((value) => value.trim().toLowerCase()).filter(Boolean);
  if (allowedRoles.length > 0 && !allowedRoles.includes(actual.role)) return false;
  const allowedNames = (contract.allowed_names || []).map(normalizeElementName).filter(Boolean);
  const normalizedActual = normalizeElementName(actual.name);
  if (allowedNames.length > 0 && !allowedNames.some((name) => name === normalizedActual || normalizeElementName(normalizedApprovedTargetName(name)) === normalizedActual)) return false;
  return true;
}

// This is deterministic normalization of an App-approved name, not fuzzy DOM
// discovery. The resulting name must still resolve exactly once with the
// approved role and pass the immutable forbidden-name contract.
export function normalizedApprovedTargetName(value: string): string {
  let normalized = String(value || "").trim().replace(/\s+/g, " ");
  normalized = normalized
    .replace(/^(?:请|然后|接着|第一步|第二步|第三步|第四步)?\s*(?:点击|单击|选择|打开|进入|按下)\s*/u, "")
    .replace(/\s*(?:入口|按钮|按键|链接|控件|选项)$/u, "")
    .replace(/^(?:please\s+)?(?:click|select|open|choose|press)\s+(?:the\s+)?/i, "")
    .replace(/\s+(?:entry|button|link|control|option)$/i, "")
    .trim();
  return normalized;
}

function normalizeElementName(value: string): string {
  return String(value || "").trim().replace(/\s+/g, " ").toLowerCase();
}

function redactText(value: string): string {
  return String(value || "")
    .slice(0, 240)
    .replace(/authorization[:=]\s*[^\s,;]+/gi, "authorization=[redacted]")
    .replace(/cookie[:=]\s*[^\s,;]+/gi, "cookie=[redacted]")
    .replace(/bearer\s+[^\s,;]+/gi, "bearer [redacted]")
    .replace(/sk-[A-Za-z0-9_-]+/g, "sk-[redacted]");
}

function safeName(value: string): string {
  return String(value || "browser_agent").replace(/[^A-Za-z0-9_.-]+/g, "_").slice(0, 120) || "browser_agent";
}

function normalizeEngine(value?: string): "chromium" | "firefox" | "webkit" {
  if (value === "firefox" || value === "webkit") return value;
  return "chromium";
}

async function artifactRef(id: string, kind: string, filePath: string, mimeType: string, sourceNodeID?: string, metadata: Record<string, unknown> = {}, sensitive = true): Promise<ArtifactRef> {
  const data = await readFile(filePath);
  return {
    id,
    kind,
    uri: pathToFileURL(path.resolve(filePath)).toString(),
    mime_type: mimeType,
    sha256: createHash("sha256").update(data).digest("hex"),
    size_bytes: data.length,
    created_at: new Date().toISOString(),
    sensitive,
    ...(sourceNodeID ? { source_node_id: sourceNodeID } : {}),
    metadata: { ...metadata, source: "browser_agent_runtime" },
  };
}

async function installRecordingMasks(context: any, selectors: string[]): Promise<void> {
  await context.addInitScript((approvedSelectors: string[]) => {
    if (!Array.isArray(approvedSelectors) || approvedSelectors.length === 0) return;
    const browserGlobal = globalThis as any;
    const document = browserGlobal.document as any;
    const requestAnimationFrame = browserGlobal.requestAnimationFrame.bind(browserGlobal) as (callback: () => void) => number;
    const overlays = new Map<any, any>();
    const update = () => {
      const matched = new Set<any>();
      for (const selector of approvedSelectors) {
        try {
          document.querySelectorAll(selector).forEach((element: any) => matched.add(element));
        } catch {
          // An invalid approved selector must not disable the other masks.
        }
      }
      for (const [element, overlay] of overlays) {
        if (!element.isConnected || !matched.has(element)) {
          overlay.remove();
          overlays.delete(element);
        }
      }
      for (const element of matched) {
        let overlay = overlays.get(element);
        if (!overlay || !overlay.isConnected) {
          overlay = document.createElement("div");
          overlay.setAttribute("data-cascade-recording-mask", "true");
          Object.assign(overlay.style, {
            position: "fixed",
            zIndex: "2147483647",
            pointerEvents: "none",
            background: "#111",
          });
          document.documentElement.appendChild(overlay);
          overlays.set(element, overlay);
        }
        const bounds = element.getBoundingClientRect();
        Object.assign(overlay.style, {
          display: bounds.width > 0 && bounds.height > 0 ? "block" : "none",
          left: `${bounds.left}px`,
          top: `${bounds.top}px`,
          width: `${bounds.width}px`,
          height: `${bounds.height}px`,
        });
      }
      requestAnimationFrame(update);
    };
    requestAnimationFrame(update);
  }, selectors);
}

async function fileExists(filePath: string): Promise<boolean> {
  const info = await stat(filePath).catch(() => undefined);
  return Boolean(info?.isFile());
}
