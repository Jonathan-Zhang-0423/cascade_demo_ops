import { createHash, randomUUID } from "node:crypto";
import { existsSync } from "node:fs";
import { mkdir, readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { launchOptionsWithProxy } from "./playwright-proxy.js";

type BrowserAgentTargetContract = {
  semantic_id: string;
  allowed_roles?: string[];
  allowed_names?: string[];
  forbidden_names?: string[];
  component_ref?: string;
  destructive: boolean;
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
  forbidden_pages?: string[];
  forbidden_path_prefixes?: string[];
  forbidden_keywords?: string[];
  mask_selectors?: string[];
};

export type BrowserAgentStageRequest = {
  session_id: string;
  stage: BrowserAgentWorkerStage;
};

type RuntimeObservation = {
  source: "actual_browser_observation" | "browser_assertion";
  url?: string;
  title?: string;
  assertions?: Array<{ kind: string; passed: boolean; actual?: string }>;
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
  forbiddenPages: string[];
  forbiddenPathPrefixes: string[];
  forbiddenKeywords: string[];
  maskSelectors: string[];
};

type ResolvedTarget = { locator: any; strategy: string; approvedAlternative?: { kind: string; value: string } };

const sessions = new Map<string, BrowserAgentSession>();
const defaultViewport = { width: 1440, height: 900 };
const actionTimeoutMS = 10_000;
const screenshotTimeoutMS = 8_000;

export async function openBrowserAgentSession(request: BrowserAgentOpenRequest): Promise<{ session_id: string; runtime_versions: Record<string, string> }> {
  if (!request.output_dir?.trim()) throw new Error("browser_agent_output_dir_required");
  if (!Array.isArray(request.allowed_domains) || request.allowed_domains.length === 0) throw new Error("browser_agent_allowed_domains_required");
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
  const tracePath = path.join(outputDir, "browser-agent-trace.zip");
  await context.tracing.start({ screenshots: true, snapshots: true, sources: false });
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
    forbiddenPages: request.forbidden_pages || [],
    forbiddenPathPrefixes: request.forbidden_path_prefixes || [],
    forbiddenKeywords: request.forbidden_keywords || [],
    maskSelectors: request.mask_selectors || [],
  };
  await page.route("**/*", async (route: any) => {
    const error = urlPolicyError(route.request().url(), session, true);
    if (error) {
      await route.abort("blockedbyclient");
      return;
    }
    await route.continue();
  });
  sessions.set(sessionID, session);
  return { session_id: sessionID, runtime_versions: { runner: "playwright-browser-agent", browser: engineName } };
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
	let suggestedWaitCondition: string | undefined;
  let resolutionFailure = "";
  if (action && !["navigate", "wait"].includes(action.kind)) {
    try {
      resolved = await resolveTarget(session.page, request.stage, action, false);
    } catch (error) {
      if (!isTargetResolutionFailure(error)) throw error;
      resolutionFailure = safeResolutionFailure(error);
      approvedAlternative = await resolveSingleApprovedAlternative(session.page, request.stage);
		if (!approvedAlternative && await pageStillBusy(session.page)) {
			suggestedWaitCondition = suggestedEntryWaitCondition(request.stage);
		}
    }
  }
  const evidence = screenshotEvidence(artifact, request.stage, "执行前页面观察");
  const assertions = resolutionAssertions(resolved?.strategy, resolutionFailure, safeURL(session.page.url()));
  return {
    observation: await observation(session.page, "actual_browser_observation", assertions),
    evidence_refs: [evidence],
    artifacts: [artifact],
    target_resolved: Boolean(resolved) || action?.kind === "navigate",
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

export function resolutionAssertions(strategy: string | undefined, failure: string, currentURL: string): Array<{ kind: string; passed: boolean; actual?: string }> {
  if (strategy) return [{ kind: "target_resolved", passed: true, actual: strategy }];
  if (failure) return [{ kind: "target_resolved", passed: false, actual: failure }];
  return [{ kind: "page_observed", passed: true, actual: currentURL }];
}

export async function executeBrowserAgentStage(request: BrowserAgentStageRequest): Promise<BrowserAgentStageResult> {
  const session = requiredSession(request.session_id);
  validateStage(request.stage);
  const assertions: Array<{ kind: string; passed: boolean; actual?: string }> = [];
  for (const interaction of request.stage.interactions) {
    await executeInteraction(session, request.stage, interaction);
    assertions.push({ kind: `action_${interaction.kind}_completed`, passed: true, actual: request.stage.target_contract.semantic_id });
  }
  await waitForCaptureWindow(session.page, request.stage);
  assertions.push(...await evaluateRequiredValidations(session.page, request.stage));
  const artifact = await captureScreenshot(session, request.stage, "after");
  const evidence = screenshotEvidence(artifact, request.stage, "执行后结果证据");
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
  try {
    await session.context.tracing.stop({ path: session.tracePath });
  } finally {
    await session.context.close().catch(() => undefined);
    await session.browser.close().catch(() => undefined);
  }
  if (await fileExists(session.tracePath)) {
    artifacts.push(await artifactRef(`artifact_${session.id}_trace`, "browser_trace", session.tracePath, "application/zip", undefined, { include_in_demo: false }));
  }
  recordingPath = await session.video?.path().catch(() => undefined);
  if (recordingPath && await fileExists(recordingPath)) {
    artifacts.unshift(await artifactRef(`artifact_${session.id}_recording`, "raw_recording", recordingPath, "video/webm", undefined, { include_in_demo: true }));
  }
  // Failed observations may throw after their before-capture. Recover every
  // stage screenshot here so the Server can build a protocol failure package.
  for (const entry of await readdir(session.outputDir).catch(() => [] as string[])) {
    const match = /^stage-\d+-(.+)-(before|after)\.png$/i.exec(entry);
    if (!match) continue;
    const nodeID = match[1] || "unknown";
    const phase = (match[2] || "before").toLowerCase();
    artifacts.push(await artifactRef(
      `artifact_${safeName(session.id)}_${safeName(nodeID)}_${phase}`,
      "screenshot",
      path.join(session.outputDir, entry),
      "image/png",
      nodeID,
      { include_in_demo: phase === "after", capture_phase: phase, recovered_at_session_close: true },
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

async function executeInteraction(session: BrowserAgentSession, stage: BrowserAgentWorkerStage, interaction: BrowserAgentInteraction): Promise<void> {
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
  const resolved = await resolveTarget(session.page, stage, interaction, true);
  if (interaction.kind === "click") {
    await resolved.locator.click({ timeout });
  } else if (interaction.kind === "fill") {
    if (interaction.secret_ref) throw new Error("browser_agent_secret_ref_requires_broker");
    if (interaction.input_ref && interaction.value === undefined) throw new Error("browser_agent_input_ref_requires_resolver");
    await resolved.locator.fill(interaction.value || "", { timeout });
  } else if (interaction.kind === "select") {
    if (interaction.secret_ref) throw new Error("browser_agent_secret_ref_requires_broker");
    if (interaction.input_ref && interaction.value === undefined) throw new Error("browser_agent_input_ref_requires_resolver");
    await resolved.locator.selectOption(interaction.value || "", { timeout });
  } else if (interaction.kind === "assert" || interaction.kind === "inspect") {
    await resolved.locator.waitFor({ state: "visible", timeout });
  } else if (interaction.kind === "upload") {
    throw new Error("browser_agent_upload_requires_scoped_file_broker");
  } else {
    throw new Error(`browser_agent_action_not_supported: ${interaction.kind}`);
  }
  await waitForPageSettled(session.page, Math.min(timeout, 5_000));
}

async function resolveTarget(page: any, stage: BrowserAgentWorkerStage, interaction: BrowserAgentInteraction, allowSelectorAlternatives: boolean): Promise<ResolvedTarget> {
	const candidates: Array<{ strategy: string; locator: any }> = [];
	const contract = stage.target_contract;
	const preferred = stage.preferred_selector_alternative;
	if (preferred) {
		const locator = locatorFromAlternative(page, preferred.kind, preferred.value);
		if (!locator) throw new Error(`browser_agent_target_not_resolved: ${stage.node_id}; strategies=preferred_${preferred.kind}`);
		const resolved = await resolveUniqueVisibleContractTarget(locator, `approved_${preferred.kind}`, contract);
		if (!resolved) throw new Error(`browser_agent_target_not_resolved: ${stage.node_id}; strategies=preferred_${preferred.kind}`);
		return { ...resolved, approvedAlternative: { kind: preferred.kind, value: preferred.value } };
	}
  for (const role of contract.allowed_roles || []) {
    for (const name of contract.allowed_names || []) {
      candidates.push({ strategy: `role:${role}+approved_name`, locator: page.getByRole(role, { name, exact: true }) });
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
  if (interaction.target?.selector) candidates.push({ strategy: "interaction_css", locator: page.locator(interaction.target.selector) });

  const seen = new Set<string>();
  for (const candidate of candidates) {
    if (seen.has(candidate.strategy)) continue;
    seen.add(candidate.strategy);
		const resolved = await resolveUniqueVisibleContractTarget(candidate.locator, candidate.strategy, contract);
		if (resolved) return resolved;
	}
  throw new Error(`browser_agent_target_not_resolved: ${stage.node_id}; strategies=${[...seen].join(",") || "none"}`);
}

// This is the only discovery step allowed after the primary locator fails.
// It checks App-approved alternatives one by one and returns nothing unless
// exactly one live target is unique, visible, and contract-compatible.
async function resolveSingleApprovedAlternative(page: any, stage: BrowserAgentWorkerStage): Promise<{ kind: string; value: string } | undefined> {
  const matched: Array<{ kind: string; value: string }> = [];
  const components = (stage.components || []).filter((component) => !stage.target_contract.component_ref || component.component_ref === stage.target_contract.component_ref);
  for (const component of components) {
    for (const alternative of component.selector_alternatives || []) {
      const locator = locatorFromAlternative(page, alternative.kind, alternative.value);
      if (!locator) continue;
      const resolved = await resolveUniqueVisibleContractTarget(locator, `approved_${alternative.kind}`, stage.target_contract);
      if (resolved) matched.push({ kind: alternative.kind, value: alternative.value });
    }
  }
  return matched.length === 1 ? matched[0] : undefined;
}

async function resolveUniqueVisibleContractTarget(locator: any, strategy: string, contract: BrowserAgentTargetContract): Promise<ResolvedTarget | undefined> {
	const count = await locator.count().catch(() => 0);
	if (count !== 1) return undefined;
	const unique = locator.first();
	if (!await unique.isVisible({ timeout: 750 }).catch(() => false)) return undefined;
	const semantics = await compactElementSemantics(unique);
	if (!semanticsAllowed(semantics, contract)) return undefined;
	if (forbiddenName(semantics.name, contract.forbidden_names || [])) {
		throw new Error(`browser_agent_forbidden_target_name: ${contract.semantic_id}`);
	}
	return { locator: unique, strategy };
}

async function evaluateRequiredValidations(page: any, stage: BrowserAgentWorkerStage): Promise<Array<{ kind: string; passed: boolean; actual?: string }>> {
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
      } else if (validation.kind === "element_visible") {
        const locator = locatorForValidation(page, validation);
        passed = await locator.first().isVisible({ timeout }).catch(() => false);
        actual = passed ? "visible" : "not_visible";
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
    return actual.origin === expected.origin && actual.pathname === expected.pathname;
  } catch {
    return safeURL(actualValue).includes(expectedValue);
  }
}

function locatorFromAlternative(page: any, kind: string, value: string): any | undefined {
  if (!value) return undefined;
  if (kind === "testid") return page.getByTestId(value);
  if (kind === "label") return page.getByLabel(value, { exact: true });
  if (kind === "text") return page.getByText(value, { exact: true });
  if (kind === "css" || kind === "selector") return page.locator(value);
  return undefined;
}

async function captureScreenshot(session: BrowserAgentSession, stage: BrowserAgentWorkerStage, phase: "before" | "after"): Promise<ArtifactRef> {
  const fileName = `stage-${String(stage.order).padStart(3, "0")}-${safeName(stage.node_id)}-${phase}.png`;
  const filePath = path.join(session.outputDir, fileName);
  const masks = session.maskSelectors.filter(Boolean).map((selector) => session.page.locator(selector));
  await session.page.screenshot({ path: filePath, fullPage: false, mask: masks, timeout: screenshotTimeoutMS });
  return artifactRef(
    `artifact_${safeName(session.id)}_${safeName(stage.node_id)}_${phase}`,
    "screenshot",
    filePath,
    "image/png",
    stage.node_id,
    { include_in_demo: phase === "after", capture_phase: phase, target_semantic_id: stage.target_contract.semantic_id, current_url: safeURL(session.page.url()), page_title: redactText(await session.page.title().catch(() => "")) },
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

async function observation(page: any, source: RuntimeObservation["source"], assertions: NonNullable<RuntimeObservation["assertions"]>): Promise<RuntimeObservation> {
  return {
    source,
    url: safeURL(page.url()),
    title: redactText(await page.title().catch(() => "")),
    assertions,
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

function urlPolicyError(value: string, session: BrowserAgentSession, allowSubresource: boolean): string | undefined {
  if (allowSubresource && /^(about:|data:|blob:)/i.test(value)) return undefined;
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return "browser_agent_domain_not_allowed: invalid_url";
  }
  if (!allowSubresource && parsed.protocol !== "http:" && parsed.protocol !== "https:") return `browser_agent_domain_not_allowed: ${parsed.protocol}`;
  if (!hostAllowed(parsed.hostname, session.allowedDomains)) return `browser_agent_domain_not_allowed: ${parsed.hostname}`;
  const normalizedPath = `/${parsed.pathname.replace(/^\/+/, "")}`.toLowerCase();
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
    };
    let role = explicitRole || implicitRoles[tag] || "";
    if (tag === "a" && element.hasAttribute?.("href")) role = "link";
    if (tag === "input") role = ["button", "submit", "reset"].includes(type) ? "button" : type === "checkbox" ? "checkbox" : type === "radio" ? "radio" : "textbox";
    const labels = Array.from(element.labels || []).map((label: any) => label.textContent || "").join(" ");
    const name = String(element.getAttribute?.("aria-label") || labels || element.innerText || element.textContent || "").trim();
    return { role, name };
  }).catch(() => ({ role: "", name: "" }));
  return { role: String(result.role || "").toLowerCase(), name: redactText(String(result.name || "")) };
}

function semanticsAllowed(actual: { role: string; name: string }, contract: BrowserAgentTargetContract): boolean {
  const allowedRoles = (contract.allowed_roles || []).map((value) => value.trim().toLowerCase()).filter(Boolean);
  if (allowedRoles.length > 0 && !allowedRoles.includes(actual.role)) return false;
  const allowedNames = (contract.allowed_names || []).map(normalizeElementName).filter(Boolean);
  if (allowedNames.length > 0 && !allowedNames.includes(normalizeElementName(actual.name))) return false;
  return true;
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

function configuredBrowserExecutable(): string | undefined {
  const configured = process.env.CASCADE_BROWSER_EXECUTABLE_PATH?.trim();
  if (configured) return configured;
  if (process.platform !== "win32") return undefined;
  const candidates = [
    "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
    "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
    "C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe",
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
  ];
  return candidates.find((candidate) => existsSync(candidate));
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

async function fileExists(filePath: string): Promise<boolean> {
  const info = await stat(filePath).catch(() => undefined);
  return Boolean(info?.isFile());
}
