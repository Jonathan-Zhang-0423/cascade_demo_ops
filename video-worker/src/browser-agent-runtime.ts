import { createHash, randomUUID } from "node:crypto";
import { spawn } from "node:child_process";
import { mkdir, readdir, readFile, rename, stat, writeFile } from "node:fs/promises";
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
  selector_alternatives?: BrowserAgentSelectorCandidate[];
};

type BrowserAgentSelectorCandidate = {
  kind: string;
  value: string;
  observed_role?: string;
  observed_accessible_name?: string;
  observed_url?: string;
  observed_route_template?: string;
  observed_page_role?: string;
  observed_form_role?: string;
  evidence_digest_sha256?: string;
};

type BrowserAgentTaskSecret = {
  username: string;
  password: string;
  expires_at: string;
  allowed_domains: string[];
  allowed_operations: string[];
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

type InteractionPredicate = {
  id: string;
  kind: string;
  target?: BrowserAgentInteraction["target"];
  expected?: unknown;
  required: boolean;
  timeout_ms?: number;
};

type InteractionContract = {
  schema_version: "demoops.interaction_contract.v1";
  contract_id: string;
  semantic_goal: string;
  archetype?: string;
  action_kind: string;
  replay_policy: "observe_only" | "idempotent_write" | "once_effect";
  target_semantic_id: string;
  parameters?: Record<string, unknown>;
  preconditions?: InteractionPredicate[];
  expected_transitions: InteractionPredicate[];
  non_destructive: boolean;
};

export type BrowserAgentWorkerStage = {
  id: string;
  order: number;
  node_id: string;
  stage_kind?: string;
  objective?: string;
  entry_route?: string;
  route?: string;
  url?: string;
  target_route_template?: string;
  expected_route_after_action?: string;
  runtime_route_verification_required?: boolean;
  target_contract: BrowserAgentTargetContract;
  interaction_contract?: InteractionContract;
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
  preferred_selector_alternative?: BrowserAgentSelectorCandidate;
  evidence_bound_selector_alternatives?: BrowserAgentSelectorCandidate[];
  checkpoint_restore?: boolean;
};

export type BrowserAgentOpenRequest = {
  session_id?: string;
  output_dir: string;
  initial_url?: string;
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
  visual_max_calls?: number;
  task_secrets?: Record<string, BrowserAgentTaskSecret>;
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
  state_fingerprint?: BrowserStateFingerprint;
};

type BrowserStateFingerprint = {
  origin?: string;
  route_template?: string;
  document_digest?: string;
  aria_digest?: string;
  frame_digests?: Record<string, string>;
  observed_at: string;
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
  recording_segment_paths?: string[];
  recording_segment_manifest_path?: string;
  trace_path?: string;
  artifacts: ArtifactRef[];
  runtime_versions: Record<string, string>;
};

type OutcomeChangeEvidence = {
  url: boolean;
  visual: boolean;
  dom: boolean;
  aria: boolean;
  frame: boolean;
  networkSettled: boolean;
  distinctActions?: number;
  numericIncreased?: boolean;
  restoreSimilarity?: number;
  inputModality?: string;
  stateVariants?: number;
};

type InteractionProofEvidence = Pick<OutcomeChangeEvidence, "distinctActions" | "numericIncreased" | "restoreSimilarity" | "inputModality" | "stateVariants">;

type OutcomeSnapshot = {
  url: string;
  visualDigest: string;
  domDigest: string;
  ariaDigest: string;
  frameDigest: string;
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
	visualChangeByNodeID: Map<string, boolean>;
	outcomeChangesByNodeID: Map<string, OutcomeChangeEvidence>;
	interactionProofByNodeID: Map<string, InteractionProofEvidence>;
	interactionProofBySessionID: Map<string, InteractionProofEvidence>;
	interactionStateHistory: string[];
	latestNumericIncrease: boolean;
	latestFrameChange: boolean;
	visualMaxCalls: number;
	temporalSnapshotsByScopeID: Map<string, OutcomeSnapshot>;
	visionPollArtifactsByNodeID: Map<string, ArtifactRef[]>;
	visionPollEvidenceByNodeID: Map<string, EvidenceRef[]>;
	visionVerdictsByNodeID: Map<string, BrowserVisualObservation[]>;
	resultSurfaceBaselineDigest?: string;
	runtimeContinuationEffectsCommitted: number;
  // A verified action may move an application from its entry route to a
  // newly-created result route. Subsequent non-navigation stages must keep
  // that live route instead of treating their planning-time entry route as a
  // command to navigate backwards.
  continuationURL?: string;
  taskSecrets: Record<string, BrowserAgentTaskSecret>;
};

export type BrowserAgentTemporalCaptureRequest = {
  session_id: string;
  scope_id: string;
  sequence: number;
  phase: string;
  reason: "submitted" | "heartbeat" | "material_change" | "manual_probe";
};

export type BrowserAgentTemporalCaptureResult = {
  artifact: ArtifactRef;
  fingerprint: BrowserStateFingerprint;
  material_change: boolean;
  changed_channels: Array<"url" | "visual" | "dom" | "aria" | "frame">;
  current_url: string;
  page_title: string;
};

export type BrowserVisualObservation = {
	schema_version: "demoops.browser_visual_observation.v1";
	decision: "in_progress" | "succeeded" | "failed" | "unknown";
	confidence: number;
	summary: string;
	visible_evidence?: string[];
	blocking_reason?: string;
	model_trace?: Record<string, unknown>;
	provider_calls_used?: number;
	observed_at: string;
};

type ResolvedTarget = { locator: any; strategy: string; approvedAlternative?: { kind: string; value: string } };

export type AdaptiveTargetCandidateScore = {
  role_state: number;
  semantic: number;
  container_context: number;
  uniqueness: number;
  transition_feasibility: number;
};

export function adaptiveTargetCandidateScore(value: AdaptiveTargetCandidateScore): number {
  const unit = (input: number) => Math.max(0, Math.min(1, Number.isFinite(input) ? input : 0));
  return unit(value.role_state) * .30 + unit(value.semantic) * .25 + unit(value.container_context) * .20 + unit(value.uniqueness) * .15 + unit(value.transition_feasibility) * .10;
}

export function adaptiveTargetCandidateExecutable(best: number, secondBest?: number): boolean {
  return best >= .85 && (secondBest === undefined || best - secondBest >= .20);
}

const sessions = new Map<string, BrowserAgentSession>();
const targetProbeTimeoutMS = 2_000;
// Canonical Browser Agent evidence master. Playwright records only the page
// content viewport: browser chrome, the desktop, and off-viewport page content
// are excluded.
const defaultViewport = { width: 2560, height: 1440 };
const actionTimeoutMS = 10_000;
const screenshotTimeoutMS = 8_000;
const standardValidationTimeoutMS = 30_000;
const finalCompletionValidationTimeoutMS = 35 * 60 * 1000;
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
  // Playwright begins page video capture with this browser session. Keep this
  // origin so every later target geometry can be mapped back to the raw WebM.
  const openedAtMS = Date.now();
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
	openedAtMS,
	targetGeometryByArtifactID: new Map(),
	visualChangeByNodeID: new Map(),
	outcomeChangesByNodeID: new Map(),
	interactionProofByNodeID: new Map(),
	interactionProofBySessionID: new Map(),
	interactionStateHistory: [],
	latestNumericIncrease: false,
	latestFrameChange: false,
	visualMaxCalls: Math.max(1, Math.min(12, Math.trunc(Number(request.visual_max_calls) || 12))),
		temporalSnapshotsByScopeID: new Map(),
		visionPollArtifactsByNodeID: new Map(),
		visionPollEvidenceByNodeID: new Map(),
		visionVerdictsByNodeID: new Map(),
		runtimeContinuationEffectsCommitted: 0,
    taskSecrets: validatedTaskSecrets(request.task_secrets),
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
  try {
    if (request.initial_url?.trim()) {
      await navigateWithinSessionPolicy(session, request.initial_url, "domcontentloaded", actionTimeoutMS);
    }
  } catch (error) {
    sessions.delete(sessionID);
    await settleWithin(context.close(), 5_000);
    await settleWithin(browser.close(), 3_000);
    throw error;
  }
  return { session_id: sessionID, runtime_versions: { runner: "playwright-browser-agent", browser: engineName } };
}

export async function browserAgentSessionStatus(request: { session_id: string }): Promise<{ url: string; title: string }> {
  const session = requiredSession(request.session_id);
  // Keep this endpoint intentionally metadata-only: it is used to hand a
  // manually authenticated, isolated browser back to the local test harness.
  return { url: safeURL(session.page.url()), title: redactText(await session.page.title().catch(() => "")) };
}

// Captures a bounded, redacted keyframe for the temporal visual harness. The
// PNG is returned only as an ArtifactRef; callers must never inline it in an
// execution event or model prompt. Change detection is intentionally semantic
// and site-neutral: it compares visual, DOM, ARIA, and frame surfaces without
// interpreting page copy or approving an action.
export async function captureBrowserAgentTemporalObservation(request: BrowserAgentTemporalCaptureRequest): Promise<BrowserAgentTemporalCaptureResult> {
  const session = requiredSession(request.session_id);
  const scopeID = safeName(request.scope_id);
  if (!scopeID || !Number.isInteger(request.sequence) || request.sequence < 1) throw new Error("browser_agent_temporal_capture_identity_invalid");
  if (!request.phase?.trim() || !["submitted", "heartbeat", "material_change", "manual_probe"].includes(request.reason)) {
    throw new Error("browser_agent_temporal_capture_context_invalid");
  }
  const policyError = urlPolicyError(session.page.url(), session, false);
  if (policyError) throw new Error(policyError);

  const snapshot = await captureOutcomeSnapshot(session.page);
  const previous = session.temporalSnapshotsByScopeID.get(scopeID);
  const change = previous ? compareOutcomeSnapshots(previous, snapshot, false) : { url: true, visual: true, dom: true, aria: true, frame: Boolean(snapshot.frameDigest), networkSettled: false };
  const changedChannels = (["url", "visual", "dom", "aria", "frame"] as const).filter((channel) => change[channel]);
  session.temporalSnapshotsByScopeID.set(scopeID, snapshot);

  const fileName = `temporal-${scopeID}-${String(request.sequence).padStart(3, "0")}-${safeName(request.reason)}.png`;
  const filePath = path.join(session.outputDir, fileName);
  const masks = session.maskSelectors.filter(Boolean).map((selector) => session.page.locator(selector));
  await session.page.screenshot({ path: filePath, fullPage: false, mask: masks, animations: "disabled", caret: "hide", timeout: screenshotTimeoutMS });
  const currentURL = safeURL(session.page.url());
  const title = redactText(await session.page.title().catch(() => ""));
  const artifact = await artifactRef(
    `artifact_${safeName(session.id)}_${scopeID}_temporal_${request.sequence}`,
    "temporal_observation_keyframe",
    filePath,
    "image/png",
    undefined,
    {
      include_in_demo: request.reason !== "heartbeat" || changedChannels.length > 0,
      presentation_only: true,
      observation_scope_id: scopeID,
      observation_sequence: request.sequence,
      observation_phase: request.phase,
      capture_reason: request.reason,
      changed_channels: changedChannels,
      current_url: currentURL,
      page_title: title,
    },
    false,
  );
  return {
    artifact,
    fingerprint: await browserStateFingerprint(session.page),
    material_change: previous === undefined || changedChannels.length > 0,
    changed_channels: changedChannels,
    current_url: currentURL,
    page_title: title,
  };
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
  await ensureStageExecutionRoute(session, request.stage);
	const action = request.stage.interactions[0];
	await waitForObservationWindow(session.page, request.stage);
	if (action && String(action.parameters?.action_recipe || "") === "continue_execution") {
		if (booleanParameter(action.parameters, "capture_result_surface_baseline", false)) {
			const existingSurface = await interactiveSurfaceTargetOnce(session.page);
			if (existingSurface) session.resultSurfaceBaselineDigest = await visualDigest(session.page, existingSurface.digestTarget);
			else delete session.resultSurfaceBaselineDigest;
		}
		await waitForRuntimeExecutionContinuation(session, request.stage, action);
	}
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
  return !["navigate", "wait", "inspect", "press", "gesture"].includes(String(kind || "").trim().toLowerCase());
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
  await ensureStageExecutionRoute(session, request.stage);
  const taskSecretRef = formalAuthenticationTaskSecretRef(session, request.stage);
  const secretValues = taskSecretRef ? {} : validatedStageSecretValues(request.stage, request.secrets);
  const assertions: Array<{ kind: string; passed: boolean; actual?: string }> = [];
  const targetArtifacts: ArtifactRef[] = [];
  const targetEvidence: EvidenceRef[] = [];
  const resolutionAttempts: BrowserTargetResolutionAttempt[] = [];
  let targetGeometry: BrowserTargetGeometry | undefined;
  try {
  const outcomeBefore = await captureOutcomeSnapshot(session.page);
  if (taskSecretRef) await executeFormalAuthentication(session, request.stage, taskSecretRef);
  for (const interaction of request.stage.interactions) {
    const actionEvidence = await executeInteraction(session, request.stage, interaction, secretValues, resolutionAttempts);
    if (actionEvidence) {
      targetGeometry = actionEvidence.geometry;
      targetArtifacts.push(actionEvidence.artifact);
      targetEvidence.push(screenshotEvidence(actionEvidence.artifact, request.stage, "动作目标几何证据"));
    }
    assertions.push({ kind: `action_${interaction.kind}_completed`, passed: true, actual: request.stage.target_contract.semantic_id });
  }
  await waitForCaptureWindow(session.page, request.stage);
  const networkSettled = await session.page.waitForLoadState?.("networkidle", { timeout: 2_000 }).then(() => true).catch(() => false) ?? false;
  const outcomeAfter = await captureOutcomeSnapshot(session.page);
  const changes = compareOutcomeSnapshots(outcomeBefore, outcomeAfter, networkSettled);
  if (changes.url) session.continuationURL = outcomeAfter.url;
  const proofSession = stringParameter(request.stage.interactions[0]?.parameters, "proof_session_id");
  const proof = {
    ...(proofSession ? session.interactionProofBySessionID.get(proofSession) : undefined),
    ...(session.interactionProofByNodeID.get(request.stage.node_id) || {}),
  };
  const recipe = String(request.stage.interactions[0]?.parameters?.action_recipe || "");
  if (recipe === "observe" && (request.stage.validations || []).some((validation) => validation.kind === "numeric_increased")) {
    proof.numericIncreased = session.latestNumericIncrease;
    if (session.latestFrameChange) changes.frame = true;
  }
  if (recipe === "observe" && proofSession && session.latestFrameChange) changes.frame = true;
  Object.assign(changes, proof);
  session.outcomeChangesByNodeID.set(request.stage.node_id, changes);
  if (changes.visual) session.visualChangeByNodeID.set(request.stage.node_id, true);
  assertions.push(...await evaluateRequiredValidations(session.page, request.stage, session.visualChangeByNodeID, session.outcomeChangesByNodeID, session.continuationURL, session));
  const artifact = await captureScreenshot(session, request.stage, "after");
  const evidence = screenshotEvidence(artifact, request.stage, "执行后结果证据");
  const visualPoll = drainVisionPollEvidence(session, request.stage.node_id);
  return {
    observation: await observation(session.page, "browser_assertion", assertions, targetGeometry, resolutionAttempts),
    evidence_refs: [...targetEvidence, ...visualPoll.evidence, evidence],
    artifacts: [...targetArtifacts, ...visualPoll.artifacts, artifact],
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
  await ensureStageExecutionRoute(session, request.stage);
  await waitForCaptureWindow(session.page, request.stage);
  const assertions = await evaluateRequiredValidations(session.page, request.stage, session.visualChangeByNodeID, session.outcomeChangesByNodeID, session.continuationURL, session);
  const artifact = await captureScreenshot(session, request.stage, "revalidate");
  const evidence = screenshotEvidence(artifact, request.stage, "修复截图时机后的结果证据");
  const visualPoll = drainVisionPollEvidence(session, request.stage.node_id);
  return {
    observation: await observation(session.page, "browser_assertion", assertions),
    evidence_refs: [...visualPoll.evidence, evidence],
    artifacts: [...visualPoll.artifacts, artifact],
    target_resolved: true,
  };
}

export async function closeBrowserAgentSession(request: { session_id: string }): Promise<BrowserAgentCloseResult> {
  const session = requiredSession(request.session_id);
  sessions.delete(session.id);
  clearTaskSecrets(session.taskSecrets);
  const artifacts: ArtifactRef[] = [];
  let recordingPath: string | undefined;
  const recordingPathPromise = session.video?.path().catch(() => undefined);
  try {
    if (session.traceActive) await settleWithin(session.context.tracing.stop({ path: session.tracePath }), 8_000);
  } finally {
    // Chromium finalizes a long Playwright recording only while closing its
    // context. Never hash or upload the provisional WebM while ffmpeg is still
    // appending bytes; keep the outer RPC bounded by the Go cleanup deadline.
    const contextClosed = await valueWithin(session.context.close().then(() => true).catch(() => false), 8 * 60_000, false);
    if (!contextClosed) throw new Error("browser_agent_recording_finalize_timeout");
    const browserClosed = await valueWithin(session.browser.close().then(() => true).catch(() => false), 60_000, false);
    if (!browserClosed) throw new Error("browser_agent_browser_close_timeout");
  }
  if (await fileExists(session.tracePath)) {
    artifacts.push(await artifactRef(`artifact_${session.id}_trace`, "browser_trace", session.tracePath, "application/zip", undefined, { include_in_demo: false }));
  }
  recordingPath = recordingPathPromise ? await valueWithin(recordingPathPromise, 3_000, undefined) : undefined;
  if (recordingPath && await fileExists(recordingPath)) {
    if (!await waitForFileStable(recordingPath, 3, 750)) throw new Error("browser_agent_recording_not_stable");
    const segmentEntries = (await readdir(session.outputDir).catch(() => [] as string[])).filter((entry) => /^recording-segment-\d+\.webm$/i.test(entry));
    const segmentPath = path.join(session.outputDir, `recording-segment-${String(segmentEntries.length + 1).padStart(3, "0")}.webm`);
    if (path.resolve(recordingPath) !== path.resolve(segmentPath)) await rename(recordingPath, segmentPath);
    recordingPath = segmentPath;
  }
  const recordingSegmentPaths = (await readdir(session.outputDir).catch(() => [] as string[]))
    .filter((entry) => /^recording-segment-\d+\.webm$/i.test(entry)).sort().map((entry) => path.join(session.outputDir, entry));
  let segmentManifestPath: string | undefined;
  if (recordingSegmentPaths.length > 0) {
    segmentManifestPath = path.join(session.outputDir, "recording-segments.json");
    await writeFile(segmentManifestPath, JSON.stringify({
      schema_version: "demoops.browser_recording_segments.v1",
      segment_count: recordingSegmentPaths.length,
      segments: recordingSegmentPaths.map((segmentPath, index) => ({ order: index + 1, file_name: path.basename(segmentPath) })),
    }, null, 2) + "\n", { mode: 0o600 });
    if (recordingSegmentPaths.length > 1) {
      const stitchedPath = path.join(session.outputDir, "recording-stitched.webm");
      if (await stitchRecordingSegments(recordingSegmentPaths, stitchedPath, session.outputDir)) recordingPath = stitchedPath;
    }
  }
  if (recordingPath && await fileExists(recordingPath)) {
    artifacts.unshift(await artifactRef(
      `artifact_${session.id}_recording`,
      "raw_recording",
      recordingPath,
      "video/webm",
      undefined,
      {
        include_in_demo: true,
        redaction_applied: true,
        mask_selector_count: session.maskSelectors.length,
        recording_timebase_schema_version: "demoops.browser_recording_timebase.v1",
        recording_started_at_unix_ms: session.openedAtMS,
      },
      session.recordingSensitive,
    ));
  }
  for (let index = 0; index < recordingSegmentPaths.length; index++) {
    const segmentPath = recordingSegmentPaths[index]!;
    artifacts.push(await artifactRef(
      `artifact_${session.id}_recording_segment_${index + 1}`,
      "recording_segment",
      segmentPath,
      "video/webm",
      undefined,
      { include_in_demo: true, segment_order: index + 1, segment_count: recordingSegmentPaths.length, redaction_applied: true },
      session.recordingSensitive,
    ));
  }
  if (segmentManifestPath) artifacts.push(await artifactRef(`artifact_${session.id}_recording_segments`, "recording_segment_manifest", segmentManifestPath, "application/json", undefined, { include_in_demo: false }, false));
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
    ...(recordingSegmentPaths.length > 0 ? { recording_segment_paths: recordingSegmentPaths } : {}),
    ...(segmentManifestPath ? { recording_segment_manifest_path: segmentManifestPath } : {}),
    trace_path: session.tracePath,
    artifacts,
    runtime_versions: { runner: "playwright-browser-agent", browser: session.engine },
  };
}

export function runtimeContinuationPollDecision(targetResolved: boolean, baselineDigest: string, currentDigest: string, deadlineReached: boolean, busy: boolean, idleResumeWithoutPriorEffect = false): "act" | "advance" | "observe" {
	if (targetResolved) return "act";
	if (!busy && baselineDigest && currentDigest && baselineDigest !== currentDigest) return "advance";
	if (deadlineReached) return "advance";
	if (!busy && idleResumeWithoutPriorEffect) return "advance";
	return "observe";
}

async function waitForRuntimeExecutionContinuation(session: BrowserAgentSession, stage: BrowserAgentWorkerStage, interaction: BrowserAgentInteraction): Promise<void> {
	const timeoutMS = Math.max(0, Math.min(300_000, Math.trunc(Number(interaction.parameters?.target_wait_timeout_ms) || 0)));
	if (timeoutMS <= 0) return;
	const pollMS = Math.max(1_000, Math.min(15_000, Math.trunc(Number(interaction.parameters?.target_poll_interval_ms) || 5_000)));
	const requiredCommittedEffects = booleanParameter(interaction.parameters, "requires_prior_continuation_effect", false)
		? Math.max(1, numericParameter(interaction.parameters, "continuation_chain_index", 2, 1, 8) - 1)
		: 0;
	const resumeGraceMS = numericParameter(interaction.parameters, "continuation_resume_grace_ms", 15_000, 0, 60_000);
	const startedAt = Date.now();
	const deadline = Date.now() + timeoutMS;
	while (true) {
		const attempts: BrowserTargetResolutionAttempt[] = [];
		const target = await firstRuntimeExecutionContinuationLocator(session.page, stage, attempts);
		const surface = session.resultSurfaceBaselineDigest ? await interactiveSurfaceTargetOnce(session.page) : undefined;
		const currentDigest = surface ? await visualDigest(session.page, surface.digestTarget) : "";
		const priorEffectMissing = session.runtimeContinuationEffectsCommitted < requiredCommittedEffects;
		const idleResumeWithoutPriorEffect = priorEffectMissing && Date.now() - startedAt >= resumeGraceMS;
		const decision = runtimeContinuationPollDecision(Boolean(target), session.resultSurfaceBaselineDigest || "", currentDigest, Date.now() >= deadline, await pageStillBusy(session.page), idleResumeWithoutPriorEffect);
		if (decision !== "observe") return;
		await session.page.waitForTimeout(Math.min(pollMS, Math.max(100, deadline - Date.now())));
	}
}

async function waitForFileStable(filePath: string, requiredStableChecks: number, intervalMS: number): Promise<boolean> {
  let previousSize = -1;
  let stableChecks = 0;
  for (let attempt = 0; attempt < requiredStableChecks + 8; attempt++) {
    const size = await stat(filePath).then((value) => value.size).catch(() => -1);
    if (size > 0 && size === previousSize) {
      stableChecks++;
      if (stableChecks >= requiredStableChecks) return true;
    } else {
      stableChecks = 0;
    }
    previousSize = size;
    await new Promise((resolve) => setTimeout(resolve, intervalMS));
  }
  return false;
}

async function stitchRecordingSegments(segmentPaths: string[], outputPath: string, outputDir: string): Promise<boolean> {
  const executable = String(process.env.CASCADE_FFMPEG_PATH || "").trim();
  if (!executable || segmentPaths.length < 2) return false;
  const concatPath = path.join(outputDir, "recording-segments.ffconcat");
  const quote = (value: string) => value.replace(/'/g, "'\\''");
  await writeFile(concatPath, "ffconcat version 1.0\n" + segmentPaths.map((value) => `file '${quote(path.resolve(value).replace(/\\/g, "/"))}'`).join("\n") + "\n", { mode: 0o600 });
  return new Promise<boolean>((resolve) => {
    const child = spawn(executable, ["-hide_banner", "-loglevel", "error", "-y", "-safe", "0", "-f", "concat", "-i", concatPath, "-map", "0:v:0", "-map", "0:a?", "-c", "copy", outputPath], { windowsHide: true, stdio: "ignore" });
    const timer = setTimeout(() => { child.kill(); resolve(false); }, 30_000);
    child.once("error", () => { clearTimeout(timer); resolve(false); });
    child.once("exit", (code) => { clearTimeout(timer); resolve(code === 0); });
  });
}

async function executeInteraction(
  session: BrowserAgentSession,
  stage: BrowserAgentWorkerStage,
  interaction: BrowserAgentInteraction,
  secretValues: Record<string, string>,
  resolutionAttempts: BrowserTargetResolutionAttempt[] = [],
): Promise<{ geometry: BrowserTargetGeometry; artifact: ArtifactRef } | undefined> {
  if (interaction.non_destructive !== true || stage.target_contract.destructive) throw new Error("browser_agent_destructive_action_denied");
  const timeout = numericParameter(interaction.parameters, "timeout_ms", actionTimeoutMS, 250, 60_000);
  if (interaction.kind === "navigate") {
    const target = interaction.target?.url || stage.url || stage.route || stage.entry_route;
    if (!target) throw new Error(`browser_agent_navigation_target_missing: ${stage.node_id}`);
    const targetURL = absoluteTargetURL(target, session.page.url(), stage.url);
    await navigateWithinSessionPolicy(session, targetURL, waitUntil(interaction.wait_until), timeout);
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
  if (interaction.kind === "press") {
    const keys = approvedKeyboardKeys(interaction.parameters);
    if (keys.length === 0) throw new Error(`browser_agent_keyboard_keys_not_approved: ${stage.node_id}`);
    let playableTarget: PlayableSurfaceTarget | undefined;
    if (booleanParameter(interaction.parameters, "focus_preview", false)) {
		  playableTarget = await focusLargestPlayableSurface(session.page, Math.min(timeout, 5_000), interaction.target);
    }
    const before = await visualDigest(session.page, playableTarget?.digestTarget);
    const numericBefore = await visibleNumericValues(session.page);
    const stateBefore = await interactiveStateDigest(session.page, playableTarget?.digestTarget);
    if (stateBefore) session.interactionStateHistory.push(stateBefore);
    const delayMS = numericParameter(interaction.parameters, "inter_key_delay_ms", 350, 100, 1_000);
    const digests = new Set<string>([before]);
    let currentDigest = before;
    const successfulKeys = new Set<string>();
    const maxAttempts = numericParameter(interaction.parameters, "max_attempts", keys.length, keys.length, 12);
    for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
      const key = keys[attempt % keys.length]!;
      const priorDigest = currentDigest;
      if (playableTarget?.keyboardTarget?.press) {
        await playableTarget.keyboardTarget.press(key, { timeout }).catch(async () => session.page.keyboard.press(key));
      } else {
        await session.page.keyboard.press(key);
      }
      await session.page.waitForTimeout(delayMS);
      const nextDigest = await visualDigest(session.page, playableTarget?.digestTarget);
      digests.add(nextDigest);
      if (nextDigest !== priorDigest) successfulKeys.add(key);
      currentDigest = nextDigest;
      const state = await interactiveStateDigest(session.page, playableTarget?.digestTarget);
      if (state) session.interactionStateHistory.push(state);
      const numericNow = await visibleNumericValues(session.page);
      if (successfulKeys.size >= 2 && numericSeriesIncreased(numericBefore, numericNow)) break;
    }
    const numericAfter = await visibleNumericValues(session.page);
    const numericIncreased = numericSeriesIncreased(numericBefore, numericAfter);
    const changed = digests.size > 1;
    session.latestNumericIncrease = numericIncreased;
    session.latestFrameChange = changed;
    const interactionProof = { distinctActions: successfulKeys.size, numericIncreased };
    session.interactionProofByNodeID.set(stage.node_id, interactionProof);
    const proofSession = stringParameter(interaction.parameters, "proof_session_id");
    if (proofSession) session.interactionProofBySessionID.set(proofSession, interactionProof);
    session.visualChangeByNodeID.set(stage.node_id, changed);
    return;
  }
  if (interaction.kind === "gesture") {
    if (String(interaction.parameters?.viewport || "") === "mobile") await session.page.setViewportSize({ width: 390, height: 844 });
    const target = await focusLargestPlayableSurface(session.page, Math.min(timeout, 5_000), interaction.target);
    const box = await target?.digestTarget?.boundingBox?.().catch(() => undefined);
    if (!target || !box) throw new Error(`browser_agent_gesture_surface_not_resolved: ${stage.node_id}`);
    const direction = String(interaction.parameters?.swipe_direction || "");
    const start = { x: box.x + box.width * .65, y: box.y + box.height * .55 };
    const delta = Math.max(60, Math.min(box.width, box.height) * .35);
    const end = { x: start.x + (direction === "right" ? delta : direction === "left" ? -delta : 0), y: start.y + (direction === "down" ? delta : direction === "up" ? -delta : 0) };
    if (!["left", "right", "up", "down"].includes(direction)) throw new Error(`browser_agent_gesture_direction_not_approved: ${stage.node_id}`);
    const before = await visualDigest(session.page, target.digestTarget);
    const cdp = await session.context.newCDPSession?.(session.page).catch(() => undefined);
    if (cdp) {
      await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: start.x, y: start.y }] });
      await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: end.x, y: end.y }] });
      await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
      await cdp.detach?.().catch(() => undefined);
    } else {
      await target.digestTarget.dispatchEvent("pointerdown", { pointerType: "touch", clientX: start.x, clientY: start.y });
      await target.digestTarget.dispatchEvent("pointermove", { pointerType: "touch", clientX: end.x, clientY: end.y });
      await target.digestTarget.dispatchEvent("pointerup", { pointerType: "touch", clientX: end.x, clientY: end.y });
    }
    await session.page.waitForTimeout(500);
    const changed = before !== await visualDigest(session.page, target.digestTarget);
    session.latestFrameChange = changed;
    const gestureProof = { inputModality: "touch" };
    session.interactionProofByNodeID.set(stage.node_id, gestureProof);
    const gestureSession = stringParameter(interaction.parameters, "proof_session_id");
    if (gestureSession) session.interactionProofBySessionID.set(gestureSession, { ...(session.interactionProofBySessionID.get(gestureSession) || {}), ...gestureProof });
    session.visualChangeByNodeID.set(stage.node_id, changed);
    return;
  }
  if (interaction.kind === "click" && String(interaction.parameters?.action_recipe || "") === "activate_state_variants") {
    const names = stringArrayParameter(interaction.parameters, "allowed_names", 8);
    const roles = stringArrayParameter(interaction.parameters, "allowed_roles", 8);
    const observed = new Set<string>();
    for (const name of names) {
      const locator = await firstVisibleSemanticLocator(session.page, roles, name, timeout);
      if (!locator) continue;
      await locator.click({ timeout });
      await session.page.waitForTimeout(400);
      observed.add(await interactiveStateDigest(session.page));
    }
    const variantProof = { stateVariants: [...observed].filter(Boolean).length };
    session.interactionProofByNodeID.set(stage.node_id, variantProof);
    const variantSession = stringParameter(interaction.parameters, "proof_session_id");
    if (variantSession) session.interactionProofBySessionID.set(variantSession, { ...(session.interactionProofBySessionID.get(variantSession) || {}), ...variantProof });
    session.latestFrameChange = observed.size > 0;
    session.visualChangeByNodeID.set(stage.node_id, observed.size > 0);
    return;
  }
  const resolved = await resolveTarget(session.page, stage, interaction, true, resolutionAttempts);
	if (booleanParameter(interaction.parameters, "capture_result_surface_baseline", false)) {
		const existingSurface = await interactiveSurfaceTargetOnce(session.page);
		if (existingSurface) session.resultSurfaceBaselineDigest = await visualDigest(session.page, existingSurface.digestTarget);
		else delete session.resultSurfaceBaselineDigest;
	}
  if (interaction.secret_ref) await prepareSecretTarget(session, resolved.locator);
  const targetArtifact = await captureScreenshot(session, stage, "target");
  const targetGeometry = await captureTargetGeometry(session, stage, resolved, targetArtifact.id);
  if (!targetGeometry) throw new Error(`browser_agent_target_geometry_unavailable: ${stage.node_id}`);
  targetArtifact.metadata.target_geometry = targetGeometry;
  session.targetGeometryByArtifactID.set(targetArtifact.id, targetGeometry);
	const trackVisualChange = interactionRequiresVisualChangeEvidence(stage, interaction.kind);
	const visualDigestBefore = trackVisualChange ? await pageVisualDigest(session.page) : "";
  if (interaction.kind === "click") {
    await resolved.locator.click({ timeout });
	if (String(interaction.parameters?.action_recipe || "") === "continue_execution") {
		session.runtimeContinuationEffectsCommitted += 1;
	}
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
	if (interaction.kind === "click" && String(interaction.parameters?.action_recipe || "") === "activate_control") {
		const restored = await interactiveStateDigest(session.page);
		const history = session.interactionStateHistory.slice(0, -1);
		const restoreProof = { restoreSimilarity: restored && history.includes(restored) ? 1 : 0 };
		session.interactionProofByNodeID.set(stage.node_id, restoreProof);
		const restoreSession = stringParameter(interaction.parameters, "proof_session_id");
		if (restoreSession) session.interactionProofBySessionID.set(restoreSession, { ...(session.interactionProofBySessionID.get(restoreSession) || {}), ...restoreProof });
	}
	if (trackVisualChange) {
		session.visualChangeByNodeID.set(stage.node_id, visualDigestBefore !== await pageVisualDigest(session.page));
	}
  return { geometry: targetGeometry, artifact: targetArtifact };
}

export function interactionRequiresVisualChangeEvidence(stage: BrowserAgentWorkerStage, interactionKind: string): boolean {
	if (!["click", "press", "fill", "select"].includes(interactionKind)) return false;
	const changeKinds = new Set(["page_changed", "state_changed", "dom_changed", "aria_changed", "visual_region_changed", "frame_surface_changed"]);
	return (stage.validations || []).some((validation) => validation.required && changeKinds.has(validation.kind))
		|| (stage.interaction_contract?.expected_transitions || []).some((predicate) => predicate.required && changeKinds.has(predicate.kind));
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

export async function resolveTarget(page: any, stage: BrowserAgentWorkerStage, interaction: BrowserAgentInteraction, allowSelectorAlternatives: boolean, attempts: BrowserTargetResolutionAttempt[] = []): Promise<ResolvedTarget> {
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
  const structuralInput = structuralInputLocator(page, stage, interaction);
  if (structuralInput) {
    seen.add(structuralInput.strategy);
    const resolved = await resolveUniqueVisibleStructuralInputTarget(
      structuralInput.locator,
      structuralInput.strategy,
      contract,
      attempts,
    );
    if (resolved) return resolved;
  }
  const structuralSubmit = structuralModalSubmitLocator(page, stage, interaction);
  if (structuralSubmit) {
    seen.add(structuralSubmit.strategy);
    const resolved = await resolveUniqueVisibleStructuralSubmitTarget(
      structuralSubmit.locator,
      structuralSubmit.strategy,
      contract,
      attempts,
    );
    if (resolved) return resolved;
  }
	if (String(interaction.parameters?.action_recipe || "") === "continue_execution") {
		const resolved = await firstRuntimeExecutionContinuationLocator(page, stage, attempts);
		if (resolved) return resolved;
		seen.add("runtime_execution_continuation");
	}
  throw new Error(`browser_agent_target_not_resolved: ${stage.node_id}; strategies=${[...seen].join(",") || "none"}`);
}

export function runtimeExecutionContinuationScore(label: string, role: string, inPrimaryContainer: boolean, uniqueSemanticCandidate: boolean): number {
	const normalized = String(label || "").trim().toLowerCase();
	const allowedRole = role === "button" || role === "link";
	const continuation = runtimeExecutionContinuationLabel(normalized);
	const abort = /cancel|close|dismiss|back|stop|delete|remove|取消|关闭|返回|停止|删除|移除/.test(normalized);
	return adaptiveTargetCandidateScore({
		role_state: allowedRole && !abort ? 1 : 0,
		semantic: continuation && !abort ? 1 : 0,
		container_context: inPrimaryContainer ? 1 : .4,
		uniqueness: uniqueSemanticCandidate ? 1 : 0,
		transition_feasibility: allowedRole && continuation && !abort ? 1 : 0,
	});
}

function runtimeExecutionContinuationLabel(value: string): boolean {
	const normalized = String(value || "").trim().toLowerCase();
	if (/continue|confirm|proceed|execute|build|generate|继续|确认|执行|构建|生成/.test(normalized)) return true;
	return /(?:run|start).*(?:task|job|build|generation|execution)|(?:task|job|build|generation|execution).*(?:run|start)|(?:开始|启动).*(?:任务|构建|生成|执行)|(?:任务|构建|生成|执行).*(?:开始|启动)/.test(normalized);
}

async function firstRuntimeExecutionContinuationLocator(page: any, stage: BrowserAgentWorkerStage, attempts: BrowserTargetResolutionAttempt[]): Promise<ResolvedTarget | undefined> {
	if (stage.target_contract.destructive || stage.interactions.some((item) => item.non_destructive !== true)) return undefined;
	const locator = page.locator('button, [role="button"], a[href]');
	const rawCount = Math.min(await locator.count().catch(() => 0), 64);
	const values: Array<{ locator: any; score: number }> = [];
	for (let index = 0; index < rawCount; index += 1) {
		const item = locator.nth(index);
		if (!await item.isVisible().catch(() => false) || !await item.isEnabled().catch(() => false)) continue;
		const metadata = await item.evaluate((element: any) => ({
			role: String(element.getAttribute?.("role") || (String(element.tagName || "").toLowerCase() === "a" ? "link" : "button")).toLowerCase(),
			label: String(element.getAttribute?.("aria-label") || element.innerText || element.textContent || ""),
			inPrimaryContainer: Boolean(element.closest("main, article, section, [role='main'], [role='region']")),
		})).catch(() => ({ role: "", label: "", inPrimaryContainer: false }));
		if (forbiddenName(metadata.label, stage.target_contract.forbidden_names || [])) continue;
		if (!runtimeExecutionContinuationLabel(metadata.label)) continue;
		values.push({ locator: item, score: runtimeExecutionContinuationScore(metadata.label, metadata.role, metadata.inPrimaryContainer, true) });
	}
	values.sort((left, right) => right.score - left.score);
	const best = values[0];
	const second = values[1];
	if (!best || !adaptiveTargetCandidateExecutable(best.score, second?.score)) {
		attempts.push(targetResolutionAttempt("runtime_execution_continuation", values.length, values.length === 1, Boolean(best), false, values.length === 0 ? "no_candidates" : "ambiguous"));
		return undefined;
	}
	attempts.push(targetResolutionAttempt("runtime_execution_continuation", values.length, true, true, false, "resolved", true, true));
	return { locator: best.locator, strategy: "runtime_execution_continuation" };
}

// A newly opened modal often contains an input that could not have appeared in
// the pre-action page scan. For non-destructive fill/select actions, permit the
// unique editable control inside the active modal as a structural binding. The
// fallback is deliberately role- and cardinality-bound: it cannot select page
// content, buttons, destructive controls, or one of several ambiguous fields.
function structuralInputLocator(page: any, stage: BrowserAgentWorkerStage, interaction: BrowserAgentInteraction): { strategy: string; locator: any } | undefined {
  if (stage.target_contract.destructive || interaction.non_destructive === false) return undefined;
  const roles = new Set((stage.target_contract.allowed_roles || []).map((value) => value.trim().toLowerCase()));
  if (interaction.kind === "fill" && roles.has("textbox")) {
    return {
      strategy: "active_modal_unique_textbox",
      locator: page.locator('[role="dialog"] textarea, dialog textarea, [aria-modal="true"] textarea, [role="dialog"] input:not([type="hidden"]):not([type="checkbox"]):not([type="radio"]):not([type="button"]):not([type="submit"]), dialog input:not([type="hidden"]):not([type="checkbox"]):not([type="radio"]):not([type="button"]):not([type="submit"]), [aria-modal="true"] input:not([type="hidden"]):not([type="checkbox"]):not([type="radio"]):not([type="button"]):not([type="submit"])'),
    };
  }
  if (interaction.kind === "select" && roles.has("combobox")) {
    return {
      strategy: "active_modal_unique_combobox",
      locator: page.locator('[role="dialog"] select, dialog select, [aria-modal="true"] select, [role="dialog"] [role="combobox"], dialog [role="combobox"], [aria-modal="true"] [role="combobox"]'),
    };
  }
  return undefined;
}

function structuralModalSubmitLocator(page: any, stage: BrowserAgentWorkerStage, interaction: BrowserAgentInteraction): { strategy: string; locator: any } | undefined {
	if (stage.stage_kind !== "business_submit") return undefined;
  if (interaction.kind !== "click" || stage.interaction_contract?.replay_policy !== "once_effect") return undefined;
  if (stage.target_contract.destructive) return undefined;
  const roles = new Set((stage.target_contract.allowed_roles || []).map((value) => value.trim().toLowerCase()));
  if (!roles.has("button")) return undefined;
  return {
    strategy: "active_modal_unique_primary_action",
    locator: page.locator('[role="dialog"] button, dialog button, [aria-modal="true"] button'),
  };
}

async function resolveUniqueVisibleStructuralSubmitTarget(locator: any, strategy: string, contract: BrowserAgentTargetContract, attempts: BrowserTargetResolutionAttempt[] = []): Promise<ResolvedTarget | undefined> {
  const allowedRoles = (contract.allowed_roles || []).map((value) => value.trim().toLowerCase()).filter(Boolean);
  const rawCount = await withTimeout(locator.count(), targetProbeTimeoutMS, 0);
  const eligible: Array<{ locator: any; semantics: { role: string; name: string } }> = [];
  for (let index = 0; index < Math.min(rawCount, 32); index += 1) {
    const candidate = locator.nth(index);
    if (!await withTimeout(candidate.isVisible({ timeout: 750 }), targetProbeTimeoutMS, false)) continue;
    if (await withTimeout(candidate.isDisabled(), targetProbeTimeoutMS, true)) continue;
    const semantics = await withTimeout(compactElementSemantics(candidate), targetProbeTimeoutMS, { role: "", name: "", siblingButtonCount: 0 });
    if (!allowedRoles.includes(semantics.role)) continue;
    if ((semantics.siblingButtonCount || 0) < 2) continue;
    if (!normalizeElementName(semantics.name) || structuralAbortActionName(semantics.name)) continue;
    if (forbiddenName(semantics.name, contract.forbidden_names || [])) continue;
    eligible.push({ locator: candidate, semantics });
  }
  if (eligible.length !== 1) {
    attempts.push(targetResolutionAttempt(strategy, eligible.length, false, false, false, eligible.length === 0 ? "no_candidates" : "ambiguous"));
    return undefined;
  }
	const score = adaptiveTargetCandidateScore({ role_state: 1, semantic: .8, container_context: 1, uniqueness: 1, transition_feasibility: 1 });
	if (!adaptiveTargetCandidateExecutable(score)) {
		attempts.push(targetResolutionAttempt(strategy, 1, true, true, false, "name_mismatch", true, false));
		return undefined;
	}
  attempts.push(targetResolutionAttempt(strategy, 1, true, true, false, "resolved", true, true));
  return { locator: eligible[0]!.locator, strategy };
}

function structuralAbortActionName(value: string): boolean {
  const normalized = normalizeElementName(value);
  if (!/[\p{L}\p{N}]/u.test(normalized)) return true;
  if (["取消", "关闭", "返回", "放弃"].some((token) => normalized.includes(token))) return true;
  if (/(?:^|\s)(?:cancel|close|dismiss|back|abort)(?:\s|$)/i.test(normalized)) return true;
  return normalized === "x" || (/^(?:x|×|✕)\s+/u.test(normalized) && normalized.length <= 32);
}

async function resolveUniqueVisibleStructuralInputTarget(locator: any, strategy: string, contract: BrowserAgentTargetContract, attempts: BrowserTargetResolutionAttempt[] = []): Promise<ResolvedTarget | undefined> {
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
  const roleAllowed = allowedRoles.includes(semantics.role);
  if (!roleAllowed) {
    attempts.push(targetResolutionAttempt(strategy, count, true, true, false, "role_mismatch", false));
    return undefined;
  }
  attempts.push(targetResolutionAttempt(strategy, count, true, true, false, "resolved", true, true));
  return { locator: unique, strategy };
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
  const score = adaptiveTargetCandidateScore({ role_state: 1, semantic: 1, container_context: 1, uniqueness: 1, transition_feasibility: 1 });
  if (!adaptiveTargetCandidateExecutable(score)) return undefined;
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

export async function evaluateRequiredValidations(
  page: any,
  stage: BrowserAgentWorkerStage,
  visualChangeByNodeID?: ReadonlyMap<string, boolean>,
  outcomeChangesByNodeID?: ReadonlyMap<string, OutcomeChangeEvidence>,
  continuationURL?: string,
  session?: BrowserAgentSession,
): Promise<Array<{ kind: string; passed: boolean; actual?: string }>> {
  const assertions: Array<{ kind: string; passed: boolean; actual?: string }> = [];
  for (const validation of (stage.validations || []).filter((item) => item.required)) {
    const timeout = validationTimeoutMilliseconds(stage, validation);
    let passed = false;
    let actual = "not_satisfied";
    try {
      if (validation.kind === "url_matches") {
        const expected = validation.target?.url || scalarExpected(validation) || stage.url || stage.route;
        actual = safeURL(page.url());
        passed = Boolean(expected) && urlMatches(page.url(), String(expected));
        if (!passed && approvedContinuationRouteVerified(page.url(), stage, expected, continuationURL)) {
          passed = true;
          actual = `verified_route_continuity:${safeURL(page.url())}`;
        }
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
        passed = await waitForLocatorVisible(locator, timeout);
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
      } else if (validation.kind === "element_hidden") {
        const locator = locatorForValidation(page, validation);
        passed = !await locator.first().isVisible({ timeout: Math.min(timeout, 1_000) }).catch(() => false);
        actual = passed ? "hidden" : "visible";
      } else if (validation.kind === "text_contains") {
        const expected = scalarExpected(validation);
        passed = Boolean(expected) && await waitForPageText(page, String(expected), timeout);
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
        if (!passed && await structuralInputValueEquals(page, stage, expected, timeout)) {
          passed = true;
          actual = "structural_input_value_verified";
        }
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
      } else if (validation.kind === "page_changed" || validation.kind === "state_changed") {
        const changes = outcomeChangesByNodeID?.get(stage.node_id);
        passed = Boolean(changes?.visual || changes?.dom || changes?.aria || changes?.frame || visualChangeByNodeID?.get(stage.node_id));
		actual = passed ? "observed_state_changed_after_approved_action" : "no_verified_state_change";
      } else if (validation.kind === "dom_changed") {
        passed = outcomeChangesByNodeID?.get(stage.node_id)?.dom === true;
        actual = passed ? "dom_changed" : "dom_unchanged";
      } else if (validation.kind === "aria_changed") {
        passed = outcomeChangesByNodeID?.get(stage.node_id)?.aria === true;
        actual = passed ? "aria_changed" : "aria_unchanged";
      } else if (validation.kind === "visual_region_changed") {
        passed = outcomeChangesByNodeID?.get(stage.node_id)?.visual === true;
        actual = passed ? "visual_region_changed" : "visual_region_unchanged";
      } else if (validation.kind === "frame_surface_changed") {
        passed = outcomeChangesByNodeID?.get(stage.node_id)?.frame === true;
        actual = passed ? "frame_surface_changed" : "frame_surface_unchanged";
      } else if (validation.kind === "network_settled") {
        passed = outcomeChangesByNodeID?.get(stage.node_id)?.networkSettled === true;
        actual = passed ? "network_settled" : "network_not_settled";
      } else if (validation.kind === "distinct_actions_observed") {
        const count = outcomeChangesByNodeID?.get(stage.node_id)?.distinctActions || 0;
        passed = count >= Number(scalarExpected(validation) || 1); actual = String(count);
      } else if (validation.kind === "numeric_increased") {
        passed = outcomeChangesByNodeID?.get(stage.node_id)?.numericIncreased === true; actual = passed ? "numeric_increased" : "numeric_not_increased";
      } else if (validation.kind === "approximate_state_restored") {
        const similarity = outcomeChangesByNodeID?.get(stage.node_id)?.restoreSimilarity || 0;
        passed = similarity >= Number(scalarExpected(validation) || 1); actual = String(similarity);
      } else if (validation.kind === "input_modality_used") {
        actual = String(outcomeChangesByNodeID?.get(stage.node_id)?.inputModality || ""); passed = actual === String(scalarExpected(validation) || "");
      } else if (validation.kind === "state_variants_observed") {
        const count = outcomeChangesByNodeID?.get(stage.node_id)?.stateVariants || 0;
        passed = count >= Number(scalarExpected(validation) || 1); actual = String(count);
      } else if (validation.kind === "playable_surface_visible" || validation.kind === "interactive_surface_visible") {
		const target = validation.target || {};
		const hasBoundTarget = Boolean(target.test_id || target.selector || target.role || target.label || target.text);
		const boundTargetVisible = !hasBoundTarget || await waitForLocatorVisible(locatorForValidation(page, validation), timeout);
		const result = session
			? await waitForPlayableSurfaceWithVisualObservation(session, stage, timeout)
			: await waitForPlayableSurface(page, timeout);
		passed = boundTargetVisible && result.surface;
		actual = `bound_target=${boundTargetVisible};interactive_surface=${result.surface};stateful=${result.score};focusable=${result.controls}`;
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

export function validationTimeoutMilliseconds(stage: BrowserAgentWorkerStage, validation: BrowserAgentValidation): number {
  const requested = Math.max(250, Math.trunc(Number(validation.timeout_ms) || actionTimeoutMS));
  if (requested <= standardValidationTimeoutMS) return requested;
  if (!longCompletionValidationAllowed(stage, validation)) return standardValidationTimeoutMS;
  return Math.min(requested, finalCompletionValidationTimeoutMS);
}

function longCompletionValidationAllowed(stage: BrowserAgentWorkerStage, validation: BrowserAgentValidation): boolean {
  if (stage.stage_kind !== "final_observe" || stage.target_contract?.destructive) return false;
  if (!stage.interactions.length || stage.interactions.some((interaction) => !["wait", "inspect"].includes(String(interaction.kind || "").toLowerCase()))) return false;
  return validation.kind === "element_visible" || validation.kind === "text_contains" || validation.kind === "interactive_surface_visible" || validation.kind === "playable_surface_visible";
}

async function waitForLocatorVisible(locator: any, timeout: number): Promise<boolean> {
  const first = locator.first();
  if (typeof first.waitFor === "function") {
    try {
      await first.waitFor({ state: "visible", timeout });
      return true;
    } catch {
      return false;
    }
  }
  return first.isVisible({ timeout }).catch(() => false);
}

async function waitForPageText(page: any, expected: string, timeout: number): Promise<boolean> {
  if (typeof page.waitForFunction === "function") {
    try {
      await page.waitForFunction((value: string) => String((globalThis as any).document?.body?.innerText || "").includes(value), expected, { timeout });
      return true;
    } catch {
      return false;
    }
  }
  return page.locator("body").evaluate((element: any, value: string) => String(element.innerText || "").includes(value), expected).catch(() => false);
}

type BrowserVisionObserverConfig = { url: string; token: string; intervalMS: number; maxCalls: number };

function browserVisionObserverConfig(): BrowserVisionObserverConfig | undefined {
	const value = String(process.env.CASCADE_BROWSER_VISION_OBSERVER_URL || "").trim();
	const token = String(process.env.CASCADE_BROWSER_VISION_OBSERVER_TOKEN || "").trim();
	if (!value || token.length < 32) return undefined;
	try {
		const url = new URL(value);
		if (url.protocol !== "http:" || !["127.0.0.1", "localhost", "::1"].includes(url.hostname) || url.username || url.password) return undefined;
		const requestedInterval = Math.trunc(Number(process.env.CASCADE_BROWSER_VISION_INTERVAL_MS) || 60_000);
		const requestedMaxCalls = Math.trunc(Number(process.env.CASCADE_BROWSER_VISION_MAX_CALLS) || 12);
		return {
			url: url.toString(), token,
			intervalMS: Math.max(30_000, Math.min(requestedInterval, 90_000)),
			maxCalls: Math.max(1, Math.min(requestedMaxCalls, 12)),
		};
	} catch {
		return undefined;
	}
}

export function confirmedBrowserVisualTerminalDecision(observations: BrowserVisualObservation[], minimumConfidence = 0.9): "succeeded" | "failed" | undefined {
	if (observations.length < 2) return undefined;
	const latest = observations[observations.length - 1]!;
	const previous = observations[observations.length - 2]!;
	if ((latest.decision !== "succeeded" && latest.decision !== "failed") || latest.decision !== previous.decision) return undefined;
	if (latest.confidence < minimumConfidence || previous.confidence < minimumConfidence) return undefined;
	return latest.decision;
}

export function browserVisualTerminalWithStructuralEvidence(observations: BrowserVisualObservation[], maxCalls: number, minimumConfidence = 0.9): "succeeded" | "failed" | undefined {
	const latest = observations.at(-1);
	if (Math.trunc(maxCalls) <= 2 && latest && (latest.decision === "succeeded" || latest.decision === "failed") && latest.confidence >= minimumConfidence) {
		return latest.decision;
	}
	return confirmedBrowserVisualTerminalDecision(observations, minimumConfidence);
}

export function browserVisualObservationAllocation(maxCalls: number, requireVisualTerminal: boolean): { heartbeatLimit: number; terminalReserve: number } {
	const bounded = Math.max(1, Math.min(12, Math.trunc(Number(maxCalls) || 1)));
	const terminalReserve = requireVisualTerminal ? Math.max(0, bounded - 1) : 0;
	return { heartbeatLimit: Math.max(1, bounded - terminalReserve), terminalReserve };
}

export function browserVisualNextDelayMultiplier(decision: BrowserVisualObservation["decision"]): number {
	return decision === "succeeded" || decision === "failed" ? 1 : 3;
}

export function browserVisualRefreshDue(startedAtMS: number, nowMS: number, refreshAfterMS: number, refreshed: boolean): boolean {
	return !refreshed && refreshAfterMS > 0 && nowMS - startedAtMS >= refreshAfterMS;
}

export function browserVisualTerminalPolicy(stage: BrowserAgentWorkerStage): { requireVisualTerminal: boolean; refreshAfterMS: number } {
	// Compatible RPC/package paths may carry the approved parameters on the
	// immutable interaction even when the duplicated stage contract is omitted.
	// Both representations express the same approved policy.
	const parameters = stage.interaction_contract?.parameters
		|| stage.interactions.find((interaction) => interaction.parameters)?.parameters;
	return {
		requireVisualTerminal: parameters?.require_visual_terminal_confirmation === true,
		refreshAfterMS: Math.max(0, Math.trunc(Number(parameters?.refresh_after_ms) || 0)),
	};
}

export function browserVisualFinalObservationDue(input: {
	existingCount: number;
	maxCalls: number;
	nowMS: number;
	nextCaptureAtMS: number;
	startedAtMS: number;
	refreshAfterMS: number;
	deadlineMS: number;
	sawBusy: boolean;
	busyNow: boolean;
}): boolean {
	if (input.existingCount >= input.maxCalls || input.nowMS < input.nextCaptureAtMS) return false;
	if (input.existingCount === 0) return true;
	const nearDeadline = input.deadlineMS - input.nowMS <= 30_000;
	const busyTransitionCompleted = input.sawBusy && !input.busyNow;
	const scheduledRefresh = !input.busyNow && (input.refreshAfterMS <= 0 || input.nowMS - input.startedAtMS >= input.refreshAfterMS);
	return busyTransitionCompleted || scheduledRefresh || nearDeadline;
}

async function waitForPlayableSurfaceWithVisualObservation(
	session: BrowserAgentSession,
	stage: BrowserAgentWorkerStage,
	timeout: number,
): Promise<{ surface: boolean; score: boolean; controls: boolean }> {
	const baseConfig = browserVisionObserverConfig();
	if (!baseConfig) return waitForPlayableSurface(session.page, timeout);
	const config = { ...baseConfig, maxCalls: Math.min(baseConfig.maxCalls, session.visualMaxCalls) };
	const deadline = Date.now() + interactiveSurfacePollTimeout(timeout);
	const { requireVisualTerminal, refreshAfterMS } = browserVisualTerminalPolicy(stage);
	const { heartbeatLimit } = browserVisualObservationAllocation(config.maxCalls, requireVisualTerminal);
	const startedAtMS = Date.now();
	let refreshed = false;
	let sawBusy = false;
	let nextCaptureAt = Date.now();
	while (Date.now() < deadline) {
		if (requireVisualTerminal && browserVisualRefreshDue(startedAtMS, Date.now(), refreshAfterMS, refreshed)) {
			refreshed = true;
			if (!await refreshAndRestoreObservedEntry(session)) {
				return { surface: false, score: false, controls: false };
			}
			nextCaptureAt = Date.now();
		}
		const target = await interactiveSurfaceTargetOnce(session.page);
		if (target) {
			const busyNow = await pageStillBusy(session.page);
			if (busyNow) sawBusy = true;
			const surfaceDigest = session.resultSurfaceBaselineDigest ? await visualDigest(session.page, target.digestTarget) : "";
			const surfaceChanged = Boolean(session.resultSurfaceBaselineDigest && surfaceDigest && surfaceDigest !== session.resultSurfaceBaselineDigest);
			if (session.resultSurfaceBaselineDigest && !surfaceChanged) {
				await session.page.waitForTimeout(Math.min(1_000, Math.max(100, deadline - Date.now())));
				continue;
			}
			const existing = session.visionVerdictsByNodeID.get(stage.node_id) || [];
			if (!requireVisualTerminal) {
				// Compatibility mode: the visual result is supporting evidence and
				// the deterministic surface remains the admitting channel.
				if (existing.length < config.maxCalls) {
					await captureAndUnderstandVisionPoll(session, stage, config, Math.max(0, Date.now() - session.openedAtMS));
				}
				return { surface: true, score: target.stateful, controls: target.focusable };
			}
			const terminal = browserVisualTerminalWithStructuralEvidence(existing, config.maxCalls);
			if (terminal === "succeeded") return { surface: true, score: target.stateful, controls: target.focusable };
			if (terminal === "failed") return { surface: false, score: false, controls: false };
			if (browserVisualFinalObservationDue({
				existingCount: existing.length, maxCalls: config.maxCalls, nowMS: Date.now(), nextCaptureAtMS: nextCaptureAt,
				startedAtMS, refreshAfterMS, deadlineMS: deadline, sawBusy, busyNow,
			})) {
				const observed = await captureAndUnderstandVisionPoll(session, stage, config, Math.max(0, Date.now() - session.openedAtMS));
				// Sparse in-progress polling preserves enough calls for a late result
				// plus the mandatory independent terminal confirmation.
				nextCaptureAt = Date.now() + config.intervalMS * browserVisualNextDelayMultiplier(observed.decision);
				const updatedTerminal = browserVisualTerminalWithStructuralEvidence(session.visionVerdictsByNodeID.get(stage.node_id) || [], config.maxCalls);
				if (updatedTerminal === "succeeded") return { surface: true, score: target.stateful, controls: target.focusable };
				if (updatedTerminal === "failed") return { surface: false, score: false, controls: false };
				if (observed.decision === "unknown" && surfaceChanged && target.stateful && target.focusable) {
					return { surface: true, score: true, controls: true };
				}
			}
			if ((session.visionVerdictsByNodeID.get(stage.node_id)?.length || 0) >= config.maxCalls) {
				return { surface: false, score: false, controls: false };
			}
		}
		if (!target && Date.now() >= nextCaptureAt && (session.visionVerdictsByNodeID.get(stage.node_id)?.length || 0) < heartbeatLimit) {
			const observed = await captureAndUnderstandVisionPoll(session, stage, config, Math.max(0, Date.now() - session.openedAtMS));
			nextCaptureAt = Date.now() + config.intervalMS * browserVisualNextDelayMultiplier(observed.decision);
		}
		await session.page.waitForTimeout(Math.min(1_000, Math.max(100, deadline - Date.now())));
	}
	return { surface: false, score: false, controls: false };
}

export function browserVisualRefreshRecoveryRequired(observedEntryURL: string, currentURL: string): boolean {
	if (!String(observedEntryURL || "").trim()) return false;
	try {
		const observed = new URL(observedEntryURL);
		const current = new URL(currentURL);
		return observed.origin === current.origin && !sameAbsoluteURL(observed.href, current.href);
	} catch {
		return false;
	}
}

async function refreshAndRestoreObservedEntry(session: BrowserAgentSession): Promise<boolean> {
	const observedEntryURL = session.page.url();
	await session.page.reload({ waitUntil: "domcontentloaded", timeout: 30_000 }).catch(() => undefined);
	await waitForPageSettled(session.page, 5_000);
	if (!browserVisualRefreshRecoveryRequired(observedEntryURL, session.page.url())) return sameAbsoluteURL(observedEntryURL, session.page.url());
	if (urlPolicyError(observedEntryURL, session, false)) return false;

	// A deep SPA route may reload to its workspace. Recover through the exact
	// runtime-observed entity entry when it is present; this is a navigation-only
	// observation recovery and never guesses from names, hosts, or route shapes.
	const links = session.page.locator('a[href]');
	const count = Math.min(await links.count().catch(() => 0), 256);
	const exactVisible: any[] = [];
	for (let index = 0; index < count; index += 1) {
		const link = links.nth(index);
		if (!await link.isVisible().catch(() => false)) continue;
		const href = await link.evaluate((element: any) => String(element.href || element.getAttribute?.("href") || "")).catch(() => "");
		if (sameAbsoluteURL(href, observedEntryURL)) exactVisible.push(link);
	}
	if (exactVisible.length === 1) {
		await exactVisible[0].click({ timeout: 10_000 }).catch(() => undefined);
		await waitForPageSettled(session.page, 5_000);
		if (sameAbsoluteURL(observedEntryURL, session.page.url())) return true;
	}

	// History recovery preserves client-side router state on applications whose
	// server entry point redirects deep links back to the workspace.
	await session.page.goBack({ waitUntil: "domcontentloaded", timeout: 15_000 }).catch(() => undefined);
	await waitForPageSettled(session.page, 3_000);
	if (sameAbsoluteURL(observedEntryURL, session.page.url())) return true;
	await session.page.goto(observedEntryURL, { waitUntil: "domcontentloaded", timeout: 20_000 }).catch(() => undefined);
	await waitForPageSettled(session.page, 3_000);
	return sameAbsoluteURL(observedEntryURL, session.page.url());
}

async function captureAndUnderstandVisionPoll(
	session: BrowserAgentSession,
	stage: BrowserAgentWorkerStage,
	config: BrowserVisionObserverConfig,
	elapsedMS: number,
): Promise<BrowserVisualObservation> {
	const sequence = (session.visionVerdictsByNodeID.get(stage.node_id)?.length || 0) + 1;
	const suffix = `vision-poll-${String(sequence).padStart(3, "0")}`;
	const screenshotPath = path.join(session.outputDir, `stage-${String(stage.order).padStart(3, "0")}-${safeName(stage.node_id)}-${suffix}.png`);
	const masks = session.maskSelectors.filter(Boolean).map((selector) => session.page.locator(selector));
	await session.page.screenshot({ path: screenshotPath, fullPage: false, mask: masks, timeout: screenshotTimeoutMS });
	const screenshotBytes = await readFile(screenshotPath);
	const response = await requestBrowserVisualObservation(config, {
		schema_version: "demoops.browser_visual_observation.v1",
		observation_kind: "task_terminal",
		run_id: session.id,
		stage_id: stage.id,
		node_id: stage.node_id,
		stage_order: stage.order,
		sequence,
		elapsed_ms: elapsedMS,
		semantic_goal: stage.interaction_contract?.semantic_goal || stage.objective || stage.target_contract.semantic_id,
		expected_state: stage.success_state || stage.objective || stage.target_contract.semantic_id,
		current_url: safeURL(session.page.url()),
		page_title: redactText(await session.page.title().catch(() => "")),
		screenshot_data_uri: `data:image/png;base64,${screenshotBytes.toString("base64")}`,
	});
	const observationPath = path.join(session.outputDir, `stage-${String(stage.order).padStart(3, "0")}-${safeName(stage.node_id)}-${suffix}.json`);
	await writeFile(observationPath, JSON.stringify(response, null, 2) + "\n", { encoding: "utf8", mode: 0o600 });
	const screenshotArtifact = await artifactRef(
		`artifact_${safeName(session.id)}_${safeName(stage.node_id)}_${suffix}`,
		"browser_visual_poll_screenshot", screenshotPath, "image/png", stage.node_id,
		{ include_in_demo: false, presentation_only: false, capture_phase: "vision_poll", sequence, decision: response.decision, confidence: response.confidence },
		session.recordingSensitive,
	);
	const observationArtifact = await artifactRef(
		`artifact_${safeName(session.id)}_${safeName(stage.node_id)}_${suffix}_observation`,
		"browser_visual_observation", observationPath, "application/json", stage.node_id,
		{ include_in_demo: false, presentation_only: false, sequence, screenshot_artifact_id: screenshotArtifact.id },
		session.recordingSensitive,
	);
	const artifacts = session.visionPollArtifactsByNodeID.get(stage.node_id) || [];
	artifacts.push(screenshotArtifact, observationArtifact);
	session.visionPollArtifactsByNodeID.set(stage.node_id, artifacts);
	const evidence = session.visionPollEvidenceByNodeID.get(stage.node_id) || [];
	evidence.push({
		id: `evidence_${screenshotArtifact.id}`, kind: "webpage_screenshot",
		summary: `时序视觉观察 ${sequence}：${redactText(response.summary)}`,
		artifact_id: screenshotArtifact.id, confidence: response.confidence,
	});
	session.visionPollEvidenceByNodeID.set(stage.node_id, evidence);
	const history = session.visionVerdictsByNodeID.get(stage.node_id) || [];
	history.push(response);
	session.visionVerdictsByNodeID.set(stage.node_id, history);
	return response;
}

async function requestBrowserVisualObservation(config: BrowserVisionObserverConfig, body: Record<string, unknown>): Promise<BrowserVisualObservation> {
	try {
		const controller = new AbortController();
		const timer = setTimeout(() => controller.abort(), 50_000);
		try {
			const result = await fetch(config.url, {
				method: "POST",
				headers: { "Authorization": `Bearer ${config.token}`, "Content-Type": "application/json" },
				body: JSON.stringify(body), signal: controller.signal,
			});
			if (!result.ok) {
				const detail = await result.json().catch(() => ({})) as { error?: unknown; provider_calls_used?: unknown };
				const safeCode = String(detail.error || "").trim();
				return {
					schema_version: "demoops.browser_visual_observation.v1", decision: "unknown", confidence: 0,
					summary: "视觉观察服务暂不可用；继续使用确定性页面验证。",
					blocking_reason: /^[a-z0-9_-]{1,128}$/.test(safeCode) ? safeCode : `observer_http_${result.status}`,
					provider_calls_used: Math.max(1, Math.min(2, Number(detail.provider_calls_used) || 1)),
					observed_at: new Date().toISOString(),
				};
			}
			const response = await result.json() as BrowserVisualObservation;
			if (!validBrowserVisualObservation(response)) throw new Error("observer_response_invalid");
			return response;
		} finally {
			clearTimeout(timer);
		}
	} catch (error) {
		return {
			schema_version: "demoops.browser_visual_observation.v1", decision: "unknown", confidence: 0,
			summary: "视觉观察服务暂不可用；继续使用确定性页面验证。",
			blocking_reason: redactText(error instanceof Error ? error.message : String(error)), provider_calls_used: 1, observed_at: new Date().toISOString(),
		};
	}
}

function validBrowserVisualObservation(value: BrowserVisualObservation): boolean {
	return value?.schema_version === "demoops.browser_visual_observation.v1"
		&& ["in_progress", "succeeded", "failed", "unknown"].includes(value.decision)
		&& Number.isFinite(value.confidence) && value.confidence >= 0 && value.confidence <= 1
		&& Boolean(String(value.summary || "").trim()) && Boolean(String(value.observed_at || "").trim());
}

function drainVisionPollEvidence(session: BrowserAgentSession, nodeID: string): { artifacts: ArtifactRef[]; evidence: EvidenceRef[] } {
	const artifacts = session.visionPollArtifactsByNodeID.get(nodeID) || [];
	const evidence = session.visionPollEvidenceByNodeID.get(nodeID) || [];
	session.visionPollArtifactsByNodeID.delete(nodeID);
	session.visionPollEvidenceByNodeID.delete(nodeID);
	return { artifacts, evidence };
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
// runtime-created identifier in an observed route template. The Server/Worker
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
  const stateFingerprint = await browserStateFingerprint(page);
  return {
    source,
    url: safeURL(page.url()),
    title: redactText(await page.title().catch(() => "")),
    assertions,
    ...(targetGeometry ? { target_geometry: targetGeometry } : {}),
    ...(resolutionAttempts.length > 0 ? { target_resolution_attempts: resolutionAttempts } : {}),
    state_fingerprint: stateFingerprint,
  };
}

async function browserStateFingerprint(page: any): Promise<BrowserStateFingerprint> {
  const url = new URL(safeURL(page.url()));
  const digests = await page.evaluate(() => {
    const hash = (value: string) => {
      let state = 2166136261;
      for (let index = 0; index < value.length; index++) state = Math.imul(state ^ value.charCodeAt(index), 16777619);
      return (state >>> 0).toString(16).padStart(8, "0");
    };
    const documentValue = String((globalThis as any).document?.documentElement?.outerHTML || "");
    const ariaValue = Array.from((globalThis as any).document?.querySelectorAll?.("[role],[aria-label],[aria-labelledby]") || [])
      .map((element: any) => `${element.getAttribute("role") || ""}|${element.getAttribute("aria-label") || ""}|${element.getAttribute("aria-labelledby") || ""}`)
      .join("\n");
    const frames = Array.from((globalThis as any).document?.querySelectorAll?.("iframe") || [])
      .map((frame: any, index: number) => [`frame_${index}`, hash(String(frame.getAttribute("src") || "inline"))]);
    return { documentDigest: hash(documentValue), ariaDigest: hash(ariaValue), frameDigests: Object.fromEntries(frames) };
  }).catch(() => ({ documentDigest: "unavailable", ariaDigest: "unavailable", frameDigests: {} }));
  return {
    origin: url.origin,
    route_template: normalizeRoutePath(url.pathname).replace(/\b[0-9a-f]{8,}\b/gi, ":id").replace(/\/\d+(?=\/|$)/g, "/:id"),
    document_digest: digests.documentDigest,
    aria_digest: digests.ariaDigest,
    frame_digests: digests.frameDigests,
    observed_at: new Date().toISOString(),
  };
}

function validateStage(stage: BrowserAgentWorkerStage): void {
  if (!stage?.id || !stage.node_id || !stage.target_contract?.semantic_id) throw new Error("browser_agent_stage_identity_missing");
  if (!Array.isArray(stage.interactions) || stage.interactions.length === 0) throw new Error(`browser_agent_stage_interactions_missing: ${stage.node_id}`);
  if (stage.target_contract.destructive) throw new Error(`browser_agent_destructive_target_denied: ${stage.node_id}`);
  const contract = stage.interaction_contract;
  if (contract) {
    if (contract.schema_version !== "demoops.interaction_contract.v1" || !contract.contract_id || !contract.semantic_goal || contract.target_semantic_id !== stage.target_contract.semantic_id) {
      throw new Error(`browser_agent_interaction_contract_invalid: ${stage.node_id}`);
    }
    if (!contract.non_destructive || !["observe_only", "idempotent_write", "once_effect"].includes(contract.replay_policy)) {
      throw new Error(`browser_agent_interaction_contract_replay_policy_invalid: ${stage.node_id}`);
    }
    if (!Array.isArray(contract.expected_transitions) || contract.expected_transitions.length === 0 || contract.expected_transitions.some((predicate) => !predicate.required)) {
      throw new Error(`browser_agent_interaction_contract_outcome_missing: ${stage.node_id}`);
    }
  }
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

function validatedTaskSecrets(provided?: Record<string, BrowserAgentTaskSecret>): Record<string, BrowserAgentTaskSecret> {
  const values: Record<string, BrowserAgentTaskSecret> = {};
  for (const [ref, value] of Object.entries(provided || {})) {
    if (!ref.trim() || !value || !String(value.username || "").trim() || !String(value.password || "")) {
      throw new Error("browser_agent_task_secret_invalid");
    }
    const expiresAt = Date.parse(String(value.expires_at || ""));
    if (!Number.isFinite(expiresAt) || expiresAt <= Date.now()) throw new Error("browser_agent_task_secret_expired");
    if (!Array.isArray(value.allowed_domains) || value.allowed_domains.length === 0 || !Array.isArray(value.allowed_operations)) {
      throw new Error("browser_agent_task_secret_scope_invalid");
    }
    values[ref] = value;
  }
  return values;
}

function formalAuthenticationTaskSecretRef(session: BrowserAgentSession, stage: BrowserAgentWorkerStage): string | undefined {
  const refs = [...new Set(stage.interactions.map((interaction) => String(interaction.secret_ref || "").trim()).filter(Boolean))];
  if (refs.length === 0) return undefined;
  if (refs.length !== 1 || stage.stage_kind !== "session_setup") throw new Error("browser_agent_task_secret_stage_not_approved");
  const ref = refs[0]!;
  const secret = session.taskSecrets[ref];
  if (!secret) return undefined;
  const required = ["fill_username", "fill_password", "submit_login"];
  if (!required.every((operation) => secret.allowed_operations.includes(operation))) throw new Error("browser_agent_task_secret_operation_not_approved");
  const current = new URL(session.page.url());
  if (!hostAllowed(current.hostname, secret.allowed_domains)) throw new Error("browser_agent_task_secret_domain_not_approved");
  if (Date.parse(secret.expires_at) <= Date.now()) throw new Error("browser_agent_task_secret_expired");
  if (!stageHasAuthenticationProvenance(stage, session.page.url())) throw new Error("authentication_context_unverified");
  return ref;
}

async function executeFormalAuthentication(session: BrowserAgentSession, stage: BrowserAgentWorkerStage, secretRef: string): Promise<void> {
  const secret = session.taskSecrets[secretRef];
  if (!secret) throw new Error("browser_agent_task_secret_unavailable");
  const entryURL = session.page.url();
  const runtimeBootstrap = runtimeAdaptiveAuthenticationBootstrap(stage, entryURL);
  try {
    let passwordInput = await firstVisibleLocator(session.page, passwordInputSelectors());
    if (!passwordInput) {
      const entry = await firstApprovedAuthenticationLocator(session.page, stage, false)
        || (runtimeBootstrap ? await firstRuntimeAuthenticationEntryLocator(session.page, secret.username) : undefined);
      if (!entry) throw new Error("login_entry_evidence_missing");
      await entry.click({ timeout: actionTimeoutMS });
      await waitForPageSettled(session.page, 5_000);
      passwordInput = await firstVisibleLocator(session.page, passwordInputSelectors());
    }
    const usernameInput = await firstVisibleLocator(session.page, usernameInputSelectors());
    const submit = await firstApprovedAuthenticationLocator(session.page, stage, true)
      || (runtimeBootstrap && passwordInput ? await firstRuntimeAuthenticationSubmitLocator(session.page, passwordInput) : undefined);
    if (!usernameInput || !passwordInput || !submit) throw new Error("authentication_context_unverified");
    await prepareSecretTarget(session, usernameInput);
    await prepareSecretTarget(session, passwordInput);
    await suspendTraceForSecretInput(session);
    await usernameInput.fill(secret.username, { timeout: actionTimeoutMS });
    await passwordInput.fill(secret.password, { timeout: actionTimeoutMS });
    await submit.click({ timeout: actionTimeoutMS });
    await session.page.waitForURL((value: URL) => !urlMatches(value.toString(), entryURL), { timeout: 60_000 });
    await waitForPageSettled(session.page, 10_000);
    const policyError = urlPolicyError(session.page.url(), session, false);
    if (policyError) throw new Error(policyError);
    if (urlMatches(session.page.url(), entryURL)) throw new Error("login_success_validation_missing");
    if (stage.expected_route_after_action && !urlMatches(session.page.url(), stage.expected_route_after_action)) {
      throw new Error("login_success_validation_missing");
    }
  } finally {
    secret.username = "";
    secret.password = "";
    delete session.taskSecrets[secretRef];
    await resumeTraceAfterSecretInput(session);
  }
}

function usernameInputSelectors(): string[] {
  return ['input[type="email"]', 'input[name="email"]', 'input[autocomplete="email"]', 'input[autocomplete="username"]', 'input[name*="user" i]'];
}

function passwordInputSelectors(): string[] {
  return ['input[type="password"]', 'input[name="password"]', 'input[autocomplete="current-password"]'];
}

function authenticationCandidates(stage: BrowserAgentWorkerStage): BrowserAgentSelectorCandidate[] {
  return [...(stage.components || []).flatMap((component) => component.selector_alternatives || []), ...(stage.evidence_bound_selector_alternatives || [])]
    .filter((candidate, index, all) => candidate?.value && all.findIndex((item) => item.kind === candidate.kind && item.value === candidate.value) === index);
}

function stageHasAuthenticationProvenance(stage: BrowserAgentWorkerStage, currentURL: string): boolean {
  const candidates = authenticationCandidates(stage);
  if (candidates.length === 0 && runtimeAdaptiveAuthenticationBootstrap(stage, currentURL)) return true;
  return candidates.some((candidate) =>
    candidate.observed_page_role === "authentication"
    && candidate.observed_form_role === "authentication"
    && Boolean(candidate.evidence_digest_sha256)
    && Boolean(candidate.observed_url)
    && urlMatches(candidate.observed_url!, stage.url || stage.route || stage.entry_route || currentURL));
}

// Selector-free session setup is admitted only when the approved package
// binds one credential, a non-destructive runtime stage, and an explicit
// successor route. The concrete controls are then scored from the live page.
export function runtimeAdaptiveAuthenticationBootstrap(stage: BrowserAgentWorkerStage, currentURL: string): boolean {
  if (stage.stage_kind !== "session_setup" || stage.target_contract?.destructive !== false) return false;
  if (!stage.expected_route_after_action || !stage.interactions?.length) return false;
  if (authenticationCandidates(stage).length > 0) return false;
  const secretRefs = [...new Set(stage.interactions.map((item) => String(item.secret_ref || "").trim()).filter(Boolean))];
  if (secretRefs.length !== 1 || stage.interactions.some((item) => item.non_destructive !== true || Boolean(item.target?.selector || item.target?.test_id))) return false;
  const entry = stage.url || stage.route || stage.entry_route || currentURL;
  if (!entry || !urlMatches(currentURL, entry)) return false;
  const successorURL = absoluteTargetURL(stage.expected_route_after_action, currentURL, stage.url);
  const entryURL = absoluteTargetURL(entry, currentURL, stage.url);
  return !urlMatches(successorURL, entryURL);
}

export function runtimeAuthenticationChoiceScore(label: string, usernameLooksLikeEmail: boolean, inPrimaryContainer: boolean): number {
  const value = label.trim().toLowerCase();
  const email = /(^|\s)(e-?mail|mail)(\s|$)|邮箱|邮件/.test(value);
  const phone = /phone|mobile|sms|手机号|手机|短信/.test(value);
  const external = /github|google|wechat|weixin|微信|oauth/.test(value);
  const accountCreation = /register|sign\s*up|create\s+account|注册|创建账户/.test(value);
  const login = /log\s*in|sign\s*in|login|登录|登陆|登入/.test(value);
  const semantic = accountCreation ? 0 : usernameLooksLikeEmail ? (email ? 1 : login ? 0.55 : 0.1) : (login ? 1 : 0.25);
  const context = inPrimaryContainer ? 1 : 0.5;
  const uniqueness = email && usernameLooksLikeEmail ? 1 : login ? 0.65 : 0.25;
  const transition = accountCreation || external || (usernameLooksLikeEmail && phone) ? 0 : email || login ? 1 : 0.2;
  return 0.3 + 0.25 * semantic + 0.2 * context + 0.15 * uniqueness + 0.1 * transition;
}

async function firstRuntimeAuthenticationEntryLocator(page: any, username: string): Promise<any | undefined> {
  const candidates: Array<{ locator: any; score: number }> = [];
  const locator = page.locator('button, [role="button"], a[href]');
  const count = Math.min(await locator.count().catch(() => 0), 48);
  const usernameLooksLikeEmail = username.includes("@");
  for (let index = 0; index < count; index += 1) {
    const item = locator.nth(index);
    if (!await item.isVisible().catch(() => false) || !await item.isEnabled().catch(() => false)) continue;
    const metadata = await item.evaluate((element: any) => ({
      label: String(element.getAttribute("aria-label") || element.innerText || element.textContent || ""),
      inPrimaryContainer: Boolean(element.closest("main, form, [role='dialog'], [role='main']")),
    })).catch(() => ({ label: "", inPrimaryContainer: false }));
    candidates.push({ locator: item, score: runtimeAuthenticationChoiceScore(metadata.label, usernameLooksLikeEmail, metadata.inPrimaryContainer) });
  }
  candidates.sort((left, right) => right.score - left.score);
  if (!candidates[0] || candidates[0].score < 0.85) return undefined;
  if (candidates[1] && candidates[0].score - candidates[1].score < 0.2) return undefined;
  return candidates[0].locator;
}

async function firstRuntimeAuthenticationSubmitLocator(page: any, passwordInput: any): Promise<any | undefined> {
  const structural = await firstVisibleLocator(page, ['form button[type="submit"]', 'form input[type="submit"]', 'button[type="submit"]', 'input[type="submit"]']);
  if (structural) return structural;
  const container = passwordInput.locator("xpath=ancestor::*[self::form or @role='dialog'][1]");
  if (await container.count().catch(() => 0) !== 1) return undefined;
  const buttons = container.locator('button, [role="button"]');
  const visible: any[] = [];
  const count = Math.min(await buttons.count().catch(() => 0), 12);
  for (let index = 0; index < count; index += 1) {
    const item = buttons.nth(index);
    if (await item.isVisible().catch(() => false) && await item.isEnabled().catch(() => false)) visible.push(item);
  }
  if (visible.length === 1) return visible[0];
  const scored: Array<{ locator: any; score: number }> = [];
  for (const item of visible) {
    const label = await item.evaluate((element: any) => String(element.getAttribute("aria-label") || element.innerText || element.textContent || "")).catch(() => "");
    scored.push({ locator: item, score: runtimeAuthenticationChoiceScore(label, false, true) });
  }
  scored.sort((left, right) => right.score - left.score);
  if (!scored[0] || scored[0].score < 0.85 || (scored[1] && scored[0].score - scored[1].score < 0.2)) return undefined;
  return scored[0].locator;
}

async function firstApprovedAuthenticationLocator(page: any, stage: BrowserAgentWorkerStage, formSubmit: boolean): Promise<any | undefined> {
  for (const candidate of authenticationCandidates(stage)) {
    const isForm = candidate.observed_page_role === "authentication" && candidate.observed_form_role === "authentication";
    if (formSubmit !== isForm || !candidate.observed_url || !urlMatches(candidate.observed_url, page.url())) continue;
    const locator = locatorFromAlternative(page, candidate.kind, candidate.value)?.first();
    if (locator && await locator.isVisible({ timeout: 1_000 }).catch(() => false)) return locator;
  }
  return undefined;
}

async function suspendTraceForSecretInput(session: BrowserAgentSession): Promise<void> {
  if (!session.traceActive) return;
  await session.context.tracing.stop({ path: session.tracePath });
  session.traceActive = false;
}

async function resumeTraceAfterSecretInput(session: BrowserAgentSession): Promise<void> {
  if (!session.recordTrace || session.traceActive) return;
  await session.context.tracing.start({ screenshots: true, snapshots: true, sources: false });
  session.traceActive = true;
}

function clearTaskSecrets(values: Record<string, BrowserAgentTaskSecret>): void {
  for (const ref of Object.keys(values)) {
    values[ref]!.username = "";
    values[ref]!.password = "";
    delete values[ref];
  }
}

export function stageExecutionTargetURL(stage: BrowserAgentWorkerStage, currentURL: string, continuationURL?: string): string | undefined {
  const explicitNavigation = stage.interactions.some((interaction) => interaction.kind === "navigate");
  if (!explicitNavigation && continuationURL && currentURL !== "about:blank") {
    try {
      if (new URL(currentURL).origin === new URL(continuationURL).origin) return undefined;
    } catch {
      // Invalid URLs are handled by the normal target and policy validation.
    }
  }
  const target = String(stage.url || stage.route || stage.entry_route || "").trim();
  if (!target) return undefined;
  return absoluteTargetURL(target, currentURL, stage.url);
}

async function ensureStageExecutionRoute(session: BrowserAgentSession, stage: BrowserAgentWorkerStage): Promise<void> {
  const targetURL = stageExecutionTargetURL(stage, session.page.url(), session.continuationURL);
  if (!targetURL) return;
  if (urlMatches(session.page.url(), targetURL)) {
    const currentError = urlPolicyError(session.page.url(), session, false);
    if (currentError) throw new Error(currentError);
    if (stage.checkpoint_restore) session.continuationURL = session.page.url();
    return;
  }
  await navigateWithinSessionPolicy(session, targetURL, "domcontentloaded", actionTimeoutMS);
  if (!urlMatches(session.page.url(), targetURL)) {
    throw new Error(`browser_agent_stage_route_not_reached: ${stage.node_id}`);
  }
  if (stage.checkpoint_restore) session.continuationURL = session.page.url();
}

async function navigateWithinSessionPolicy(
  session: BrowserAgentSession,
  targetURL: string,
  loadState: "load" | "domcontentloaded" | "networkidle",
  timeout: number,
): Promise<void> {
  const policyError = urlPolicyError(targetURL, session, false);
  if (policyError) throw new Error(policyError);
  await session.page.goto(targetURL, { waitUntil: loadState, timeout });
  await waitForPageSettled(session.page);
  let finalPolicyError = urlPolicyError(session.page.url(), session, false);
  if (finalPolicyError) throw new Error(finalPolicyError);
  if (!urlMatches(session.page.url(), targetURL)) {
    await recoverRedirectedResultEntry(session.page, targetURL, Math.min(timeout, 8_000));
    finalPolicyError = urlPolicyError(session.page.url(), session, false);
    if (finalPolicyError) throw new Error(finalPolicyError);
  }
}

type RedirectedResultEntryCandidate = {
  index: number;
  tag: string;
  role: string;
  href: string;
  attributes: string[];
  cursor: string;
  area: number;
};

/**
 * Select a result entry after a SPA has rejected a valid deep link and sent the
 * browser back to a same-origin collection page. Selection is based only on
 * the opaque identity already present in the approved target URL. It does not
 * know a product name, hostname, route template, selector, or business label.
 */
export function selectRedirectedResultEntryCandidate(
  currentURL: string,
  targetURL: string,
  candidates: RedirectedResultEntryCandidate[],
): number | undefined {
  let current: URL;
  let target: URL;
  try {
    current = new URL(currentURL);
    target = new URL(targetURL);
  } catch {
    return undefined;
  }
  if (current.origin !== target.origin || current.href === target.href) return undefined;
  const token = target.pathname.split("/").filter(Boolean).at(-1) || "";
  if (token.length < 8 || !/^[a-z0-9_-]+$/i.test(token)) return undefined;
  const destructive = /(?:delete|remove|destroy|trash|rename|edit|删除|移除|销毁|重命名|编辑)/i;
  const scored = candidates.flatMap((candidate) => {
    if (!Number.isFinite(candidate.area) || candidate.area < 64) return [];
    const identity = [candidate.tag, candidate.role, ...candidate.attributes].join(" ");
    if (destructive.test(identity)) return [];
    const values = candidate.attributes.map((value) => String(value || ""));
    let score = 0;
    if (candidate.href) {
      try {
        const href = new URL(candidate.href, current);
        if (href.origin === target.origin && href.pathname === target.pathname) score = 1_000;
      } catch {
        // A malformed live href is simply not identity evidence.
      }
    }
    if (values.some((value) => value === target.href || value === target.pathname)) score = Math.max(score, 900);
    if (values.some((value) => value.includes(target.pathname))) score = Math.max(score, 800);
    if (values.some((value) => value === token)) score = Math.max(score, 700);
    if (values.some((value) => value.includes(token))) score = Math.max(score, 600);
    if (score === 0) return [];
    if (candidate.cursor === "pointer") score += 100;
    if (candidate.tag === "a") score += 80;
    if (candidate.role === "link" || candidate.role === "button") score += 40;
    score += Math.min(20, Math.round(Math.log2(Math.max(1, candidate.area))));
    return [{ index: candidate.index, score }];
  }).sort((left, right) => right.score - left.score || left.index - right.index);
  if (scored.length === 0 || (scored[1] && scored[1].score === scored[0]!.score)) return undefined;
  return scored[0]!.index;
}

export async function recoverRedirectedResultEntry(page: any, targetURL: string, timeoutMS = 8_000): Promise<boolean> {
  if (urlMatches(page.url(), targetURL)) return true;
  const locator = page.locator?.("body *");
  if (!locator?.evaluateAll) return false;
  const candidates = await locator.evaluateAll((elements: any[]) => elements.map((element, index) => {
    const style = (globalThis as any).getComputedStyle?.(element);
    const rect = element.getBoundingClientRect?.();
    const visible = Boolean(rect && rect.width > 0 && rect.height > 0 && style?.display !== "none" && style?.visibility !== "hidden" && Number(style?.opacity ?? 1) > 0);
    if (!visible) return undefined;
    return {
      index,
      tag: String(element.tagName || "").toLowerCase(),
      role: String(element.getAttribute?.("role") || "").toLowerCase(),
      href: String(element.href || element.getAttribute?.("href") || ""),
      attributes: Array.from(element.attributes || []).map((attribute: any) => String(attribute.value || "")),
      cursor: String(style?.cursor || ""),
      area: Number(rect.width) * Number(rect.height),
    };
  }).filter(Boolean));
  const index = selectRedirectedResultEntryCandidate(page.url(), targetURL, candidates);
  if (index === undefined) return false;
  const candidate = locator.nth(index);
  await candidate.scrollIntoViewIfNeeded?.().catch(() => undefined);
  await candidate.click({ timeout: timeoutMS });
  await waitForPageSettled(page, Math.min(timeoutMS, 5_000));
  return urlMatches(page.url(), targetURL);
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
    const pageDocument = (globalThis as any).document;
    const documentBusy = pageDocument.readyState !== "complete";
    const ariaBusy = Boolean(pageDocument.querySelector?.('[aria-busy="true"]'));
    const visibleText = (element: any): string => {
      const style = (globalThis as any).getComputedStyle?.(element);
      const rect = element.getBoundingClientRect?.();
      if (style?.display === "none" || style?.visibility === "hidden" || Number(style?.opacity) === 0 || !rect || rect.width <= 0 || rect.height <= 0) return "";
      return String(element.getAttribute?.("aria-label") || element.innerText || element.textContent || "").trim().toLowerCase();
    };
    const lifecycleNodes = Array.from(pageDocument.querySelectorAll?.('progress,[role="progressbar"],[role="status"],[aria-live],button,[role="button"]') || []).slice(0, 160) as any[];
    const activeLifecycleSignal = lifecycleNodes.some((element) => {
      const text = visibleText(element);
      if (!text) return element.matches?.('progress:not([value]),[role="progressbar"]') || false;
      return /\b(running|in progress|generating|building|executing)\b|执行中|生成中|构建中|处理中|正在执行|正在生成|正在构建/.test(text);
    });
    return documentBusy || ariaBusy || activeLifecycleSignal;
  }).catch(() => false);
}

function numericParameter(parameters: Record<string, unknown> | undefined, key: string, fallback: number, min: number, max: number): number {
  const value = Number(parameters?.[key]);
  if (!Number.isFinite(value)) return fallback;
  return Math.max(min, Math.min(max, Math.round(value)));
}

function booleanParameter(parameters: Record<string, unknown> | undefined, key: string, fallback: boolean): boolean {
  const value = parameters?.[key];
  if (typeof value === "boolean") return value;
  if (typeof value === "string") {
    if (value.trim().toLowerCase() === "true") return true;
    if (value.trim().toLowerCase() === "false") return false;
  }
  return fallback;
}

function stringParameter(parameters: Record<string, unknown> | undefined, key: string): string {
  const value = parameters?.[key];
  return typeof value === "string" ? value.trim().slice(0, 128) : "";
}

function approvedContinuationRouteVerified(
  currentURL: string,
  stage: BrowserAgentWorkerStage,
  expected: unknown,
  continuationURL?: string,
): boolean {
  if (!continuationURL || !expected || stage.interactions.some((interaction) => interaction.kind === "navigate")) return false;
  try {
    const current = new URL(currentURL);
    const continuation = new URL(continuationURL);
    if (current.origin !== continuation.origin) return false;
    const planningAnchor = String(stage.url || stage.route || stage.entry_route || "").trim();
    if (!planningAnchor) return false;
    const anchorURL = absoluteTargetURL(planningAnchor, currentURL, stage.url);
    return Boolean(anchorURL) && urlMatches(anchorURL!, String(expected));
  } catch {
    return false;
  }
}

async function structuralInputValueEquals(page: any, stage: BrowserAgentWorkerStage, expected: unknown, timeout: number): Promise<boolean> {
  const interaction = (stage.interactions || []).find((candidate) => candidate.kind === "fill" || candidate.kind === "select");
  if (!interaction || String(interaction.value ?? "") !== String(expected ?? "")) return false;
  const structural = structuralInputLocator(page, stage, interaction);
  if (!structural) return false;
  const count = await withTimeout(structural.locator.count(), targetProbeTimeoutMS, 0);
  if (count !== 1) return false;
  const unique = structural.locator.first();
  if (!await withTimeout(unique.isVisible({ timeout: Math.min(timeout, 750) }), targetProbeTimeoutMS, false)) return false;
  const semantics = await withTimeout(compactElementSemantics(unique), targetProbeTimeoutMS, { role: "", name: "" });
  if (forbiddenName(semantics.name, stage.target_contract.forbidden_names || [])) return false;
  const allowedRoles = (stage.target_contract.allowed_roles || []).map((value) => value.trim().toLowerCase()).filter(Boolean);
  if (!allowedRoles.includes(semantics.role)) return false;
  const value = await unique.inputValue({ timeout }).catch(() => undefined);
  return value !== undefined && String(value) === String(expected ?? "");
}

function stringArrayParameter(parameters: Record<string, unknown> | undefined, key: string, max: number): string[] {
  const raw = parameters?.[key];
  const values = Array.isArray(raw) ? raw : typeof raw === "string" ? raw.split(/[;,]+/) : [];
  return values.map((value) => typeof value === "string" ? value.trim() : "").filter(Boolean).slice(0, max);
}

async function firstVisibleSemanticLocator(page: any, roles: string[], name: string, timeout: number): Promise<any | undefined> {
  for (const frame of (typeof page.frames === "function" ? page.frames() : [page])) {
    for (const role of (roles.length ? roles : ["button"])) {
      const locator = frame.getByRole?.(role, { name, exact: false })?.first();
      if (locator && await locator.isVisible({ timeout: Math.min(timeout, 1_500) }).catch(() => false)) return locator;
    }
    const locator = frame.getByText?.(name, { exact: false })?.first();
    if (locator && await locator.isVisible({ timeout: Math.min(timeout, 1_500) }).catch(() => false)) return locator;
  }
  return undefined;
}

async function visibleNumericValues(page: any): Promise<number[]> {
  const values: number[] = [];
  for (const frame of (typeof page.frames === "function" ? page.frames() : [page])) {
    const frameValues = await frame.evaluate(() => {
      const doc = (globalThis as any).document;
      if (!doc) return [];
      const nodes = Array.from(doc.querySelectorAll('[role="status"], [aria-live], [role="application"], main')).slice(0, 64) as any[];
      return nodes.flatMap((node) => String(node.innerText || node.textContent || "").match(/\b\d{1,9}\b/g) || []).map(Number).filter(Number.isFinite).slice(0, 256);
    }).catch(() => [] as number[]);
    values.push(...frameValues);
  }
  return values;
}

function numericSeriesIncreased(before: number[], after: number[]): boolean {
  if (!before.length || !after.length) return false;
  const beforeSorted = [...before].sort((a, b) => a - b);
  const afterSorted = [...after].sort((a, b) => a - b);
  return Math.max(...afterSorted) > Math.max(...beforeSorted) || afterSorted.reduce((sum, value) => sum + value, 0) > beforeSorted.reduce((sum, value) => sum + value, 0);
}

async function interactiveStateDigest(page: any, locator?: any): Promise<string> {
  let value = "";
  if (locator?.evaluate) {
    value = await locator.evaluate((element: any) => `${element.innerText || element.textContent || ""}|${element.getAttribute?.("aria-label") || ""}`).catch(() => "");
  }
  if (!value) {
    const target = await interactiveSurfaceTargetOnce(page);
    value = await target?.digestTarget?.evaluate?.((element: any) => `${element.innerText || element.textContent || ""}|${element.getAttribute?.("aria-label") || ""}`).catch(() => "") || "";
  }
  return value ? createHash("sha256").update(String(value).replace(/\s+/g, " ").trim()).digest("hex") : "";
}

export function approvedKeyboardKeys(parameters: Record<string, unknown> | undefined): string[] {
  const raw = parameters?.keys;
  const values = Array.isArray(raw)
    ? raw.map((value) => typeof value === "string" ? value.trim() : "")
    : typeof raw === "string" ? raw.split(/[;,\s]+/).map((value) => value.trim()) : [];
  const keys = values.filter(Boolean);
  const allowed = new Set(["ArrowLeft", "ArrowRight", "ArrowDown", "ArrowUp"]);
  if (keys.length === 0 || keys.length > 8 || keys.some((key) => !allowed.has(key))) return [];
  return keys;
}

async function pageVisualDigest(page: any): Promise<string> {
  const bytes = await page.screenshot({ type: "png", animations: "disabled", caret: "hide", timeout: screenshotTimeoutMS });
  return createHash("sha256").update(bytes).digest("hex");
}

async function captureOutcomeSnapshot(page: any): Promise<OutcomeSnapshot> {
  const fullVisualDigest = await pageVisualDigest(page);
  const frames = typeof page.frames === "function" ? page.frames() : [page];
  const domParts: string[] = [];
  const ariaParts: string[] = [];
  for (const frame of frames) {
    const snapshot = await frame.evaluate(() => {
      const doc = (globalThis as any).document;
      if (!doc) return { dom: "", aria: "" };
      // Include the body itself: srcdoc/embedded surfaces commonly publish
      // their semantic lifecycle on body[role]/body[aria-*], while their
      // descendant structure remains unchanged.
      const elements = Array.from(doc.querySelectorAll("body,body *")).slice(0, 800) as any[];
      const dom = elements.map((element) => [
        String(element.tagName || "").toLowerCase(),
        String(element.getAttribute?.("role") || ""),
        String(element.getAttribute?.("data-testid") || ""),
        String(element.id || ""),
        element.hidden ? "hidden" : "visible",
      ].join(":" )).join("|");
      const aria = elements.map((element) => Array.from(element.attributes || [])
        .filter((attribute: any) => String(attribute.name || "").startsWith("aria-"))
        .map((attribute: any) => `${attribute.name}=${attribute.value}`)
        .sort().join(","))
        .filter(Boolean).join("|");
      return { dom, aria };
    }).catch(() => ({ dom: "", aria: "" }));
    domParts.push(String(snapshot.dom || ""));
    ariaParts.push(String(snapshot.aria || ""));
  }
  const surface = await interactiveSurfaceTargetOnce(page);
  const frameDigest = surface ? await visualDigest(page, surface.digestTarget) : "";
  return {
    url: safeURL(page.url()),
    visualDigest: fullVisualDigest,
    domDigest: createHash("sha256").update(domParts.join("\n")).digest("hex"),
    ariaDigest: createHash("sha256").update(ariaParts.join("\n")).digest("hex"),
    frameDigest,
  };
}

function compareOutcomeSnapshots(before: OutcomeSnapshot, after: OutcomeSnapshot, networkSettled: boolean): OutcomeChangeEvidence {
  return {
    url: before.url !== after.url,
    visual: before.visualDigest !== after.visualDigest,
    dom: before.domDigest !== after.domDigest,
    aria: before.ariaDigest !== after.ariaDigest,
    frame: Boolean(before.frameDigest || after.frameDigest) && before.frameDigest !== after.frameDigest,
    networkSettled,
  };
}

async function visualDigest(page: any, locator?: any): Promise<string> {
  if (locator?.screenshot) {
    const bytes = await locator.screenshot({ type: "png", animations: "disabled", caret: "hide", timeout: screenshotTimeoutMS }).catch(() => undefined);
    if (bytes) return createHash("sha256").update(bytes).digest("hex");
  }
  return pageVisualDigest(page);
}

type PlayableSurfaceEvidence = { surface: boolean; score: boolean; controls: boolean };
type InteractiveSurfaceEvidence = { surface: boolean; stateful: boolean; focusable: boolean };
type PlayableSurfaceTarget = InteractiveSurfaceEvidence & { digestTarget: any; keyboardTarget: any };

export function classifyInteractiveSurfaceFrame(surface: boolean, stateful: boolean, focusable: boolean): InteractiveSurfaceEvidence {
  return { surface, stateful: surface && stateful, focusable: surface && focusable };
}

export function classifyDOMInteractiveSurface(interactiveCount: number, width: number, height: number): InteractiveSurfaceEvidence {
  const surface = Number.isFinite(interactiveCount) && interactiveCount >= 2 && width >= 120 && height >= 80;
  return classifyInteractiveSurfaceFrame(surface, surface, surface);
}

export function classifyPlayableSurfaceFrame(text: string, surface: boolean): PlayableSurfaceEvidence {
  void text;
  // Compatibility alias: old packages used this name, but the runtime no
  // longer interprets product copy such as scores or control instructions.
  return { surface, score: surface, controls: surface };
}

async function interactiveSurfaceTargetOnce(page: any): Promise<PlayableSurfaceTarget | undefined> {
  const frames = typeof page.frames === "function" ? page.frames() : [page];
	const candidates: Array<PlayableSurfaceTarget & { area: number }> = [];
  const surfaceSelectors = [
    '[role="application"]:visible', "canvas:visible",
    '[contenteditable="true"]:visible', '[role="grid"]:visible', '[tabindex]:visible',
  ];
  for (let frameIndex = 0; frameIndex < frames.length; frameIndex++) {
    const frame = frames[frameIndex];
    const body = frame.locator?.("body");
    for (const selector of surfaceSelectors) {
      const locator = frame.locator?.(selector)?.first();
      const surfaceVisible = await locator?.isVisible({ timeout: 500 }).catch(() => false) || false;
      if (!surfaceVisible) continue;
      const box = await locator.boundingBox?.().catch(() => undefined);
      const stateful = Boolean(box && Number(box.width) >= 120 && Number(box.height) >= 80);
      const focusable = selector.includes("tabindex") || selector.includes("application") || selector.includes("contenteditable") || selector.includes("canvas") || selector.includes("iframe") || selector.includes("grid");
      const evidence = classifyInteractiveSurfaceFrame(surfaceVisible, stateful, focusable);
      if (evidence.surface && evidence.stateful) {
		candidates.push({ ...evidence, digestTarget: locator, keyboardTarget: body || locator, area: Number(box?.width || 0) * Number(box?.height || 0) });
      }
    }
    const embeddedFrame = typeof page.mainFrame === "function" ? frame !== page.mainFrame() : frames.length > 1 && frameIndex > 0;
    if (embeddedFrame && body?.evaluate) {
      const profile = await body.evaluate(() => {
        const doc = (globalThis as any).document;
        const win = (globalThis as any).window;
        const candidates = Array.from(doc?.querySelectorAll?.('button,input,select,textarea,a[href],[role="button"],[role="switch"],[role="slider"],[role="textbox"],[tabindex]') || []) as any[];
        const visible = candidates.filter((element) => {
          const style = win?.getComputedStyle?.(element);
          const rect = element.getBoundingClientRect?.();
          return !element.disabled && style?.display !== "none" && style?.visibility !== "hidden" && Number(rect?.width || 0) > 1 && Number(rect?.height || 0) > 1;
        }).length;
        const rect = doc?.body?.getBoundingClientRect?.();
        return { interactiveCount: visible, width: Number(rect?.width || 0), height: Number(rect?.height || 0) };
      }).catch(() => ({ interactiveCount: 0, width: 0, height: 0 }));
      const evidence = classifyDOMInteractiveSurface(profile.interactiveCount, profile.width, profile.height);
		if (evidence.surface) candidates.push({ ...evidence, digestTarget: body, keyboardTarget: body, area: profile.width * profile.height });
    }
  }
	candidates.sort((left, right) => right.area - left.area);
	return candidates[0];
}

async function waitForPlayableSurfaceTarget(page: any, timeout: number): Promise<PlayableSurfaceTarget | undefined> {
  const deadline = Date.now() + interactiveSurfacePollTimeout(timeout);
  do {
    const target = await interactiveSurfaceTargetOnce(page);
    if (target) return target;
    await page.waitForTimeout?.(250);
  } while (Date.now() < deadline);
  return undefined;
}

async function focusLargestPlayableSurface(page: any, timeout: number, target?: BrowserAgentInteraction["target"]): Promise<PlayableSurfaceTarget | undefined> {
	if (target?.test_id) {
		const approved = page.getByTestId(target.test_id).first();
		if (await approved.isVisible({ timeout: Math.min(timeout, 1_500) }).catch(() => false)) {
			await approved.click({ timeout }).catch(() => undefined);
			await page.waitForTimeout(150);
		}
	}
  const playable = await waitForPlayableSurfaceTarget(page, timeout);
  if (!playable) return undefined;
  await playable.digestTarget.click({ timeout }).catch(() => undefined);
  await page.waitForTimeout(150);
  return playable;
}

async function waitForPlayableSurface(page: any, timeout: number): Promise<{ surface: boolean; score: boolean; controls: boolean }> {
  const target = await waitForPlayableSurfaceTarget(page, timeout);
  return { surface: Boolean(target?.surface), score: Boolean(target?.stateful), controls: Boolean(target?.focusable) };
}

function forbiddenName(actual: string, forbidden: string[]): boolean {
  const normalized = actual.trim().toLowerCase();
  return Boolean(normalized) && forbidden.some((value) => normalized.includes(value.trim().toLowerCase()));
}

async function compactElementSemantics(locator: any): Promise<{ role: string; name: string; siblingButtonCount?: number }> {
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
    const siblingButtonCount = Array.from(element.parentElement?.children || []).filter((candidate: any) => String(candidate.tagName || "").toLowerCase() === "button").length;
    return { role, name, siblingButtonCount };
  }).catch(() => ({ role: "", name: "", siblingButtonCount: 0 }));
  return { role: String(result.role || "").toLowerCase(), name: redactText(String(result.name || "")), siblingButtonCount: Number(result.siblingButtonCount || 0) };
}

export function interactiveSurfacePollTimeout(timeout: number): number {
  // The caller has already applied the stage-kind safety policy in
  // validationTimeoutMilliseconds. Preserve an admitted long final-observe
  // budget here instead of silently truncating it back to the standard probe.
  return Math.max(250, Math.min(timeout, finalCompletionValidationTimeoutMS));
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
