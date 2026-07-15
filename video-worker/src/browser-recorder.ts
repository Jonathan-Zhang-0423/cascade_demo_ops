import { createHash } from "node:crypto";
import { readFile, stat, unlink } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { launchOptionsWithProxy } from "./playwright-proxy.js";

type BrowserEngineName = "chromium" | "firefox" | "webkit";

type BrowserRecordRequest = {
  output_dir: string;
  viewport?: { width: number; height: number };
  headless?: boolean;
  recording_run_spec?: {
    allowed_domains?: string[];
    browser?: { engine?: string; headless?: boolean };
    redactions?: { mask_selectors?: string[] };
  };
  sandbox_policy?: BrowserSandboxPolicy;
  executable_script_bundle?: {
    plan_json?: {
      recording_run_spec?: { allowed_domains?: string[] };
      steps?: BrowserScriptStep[];
    };
    security_policy?: {
      allowed_domains?: string[];
      forbidden_pages?: string[];
    };
  };
};

type BrowserSandboxPolicy = {
  profile?: string;
  isolation_mode?: string;
  policy_hash_sha256?: string;
  network_policy?: {
    mode?: "deny_all" | "allowed_domains_only" | "no_customer_network" | string;
    allowed_domains?: string[];
  };
};

type BrowserScriptStep = {
  node_id?: string;
  page_target?: { url?: string; selector?: string };
  action?: {
    type?: string;
    target?: {
      url?: string;
      selector?: string;
      test_id?: string;
      text?: string;
      label?: string;
      role?: string;
    };
    value?: string;
    timeout_ms?: number;
    wait_until?: string;
  };
  expected_outcome?: string;
  capture?: {
    screenshot?: boolean;
    scope?: "viewport" | "full_page" | "element";
    selector?: string;
    focus_selector?: string;
    full_page?: boolean;
    dedupe?: boolean;
    asset_role?: string;
    include_in_demo?: boolean;
    mask_selectors?: string[];
  };
  timing?: { duration_ms?: number };
  blocking?: boolean;
};

export type BrowserRecordResult = {
  recording_path?: string;
  screenshot_paths?: string[];
  trace_path?: string;
  step_results?: Array<{ node_id: string; status: string; duration_ms?: number; observed_state?: string }>;
  generated_assets?: BrowserArtifactRef[];
  failure_diagnostic?: BrowserFailureDiagnostic;
  sandbox_metadata?: BrowserSandboxMetadata;
  runtime_versions?: Record<string, string>;
};

type BrowserSandboxMetadata = {
  policy_hash_sha256?: string;
  profile?: string;
  isolation_mode?: string;
  network_mode?: string;
  worker_id?: string;
  runtime_versions?: Record<string, string>;
};

type BrowserFailureDiagnostic = {
  id: string;
  schema_version: "demoops.script_failure_diagnostic.v1";
  failed_node_id: string;
  failed_step_order?: number;
  attempt?: number;
  error: { code: string; message: string; retryable?: boolean };
  current_url?: string;
  page_title?: string;
  screenshot_refs?: BrowserPackageArtifactDescriptor[];
  trace_refs?: BrowserPackageArtifactDescriptor[];
  redaction_report: {
    applied: boolean;
    policy_ref?: string;
    masked_selectors?: string[];
    full_html_included: boolean;
  };
  captured_at: string;
};

type BrowserPackageArtifactDescriptor = {
  id: string;
  role: string;
  kind: string;
  uri: string;
  mime_type?: string;
  sha256?: string;
  size_bytes?: number;
  encrypted: boolean;
  sensitive?: boolean;
  metadata?: Record<string, unknown>;
};

type BrowserArtifactRef = {
  id: string;
  kind: string;
  uri: string;
  mime_type?: string;
  sha256?: string;
  size_bytes?: number;
  created_at?: string;
  sensitive?: boolean;
  source_node_id?: string;
  metadata?: Record<string, unknown>;
};

type ScreenshotCaptureResult = {
  path?: string;
  warning?: string;
};

const screenshotTimeoutMS = 8000;
const defaultViewport = { width: 1920, height: 1080 };
const pageSettleTimeoutMS = 5000;
const pageSettleFrameMS = 250;

export async function recordWithPlaywright(request: BrowserRecordRequest): Promise<BrowserRecordResult> {
  const playwright = await import("playwright");
  const engineName = normalizeEngine(request.recording_run_spec?.browser?.engine);
  const engine = playwright[engineName];
  if (!engine) {
    throw new Error(`unsupported Playwright browser engine: ${engineName}`);
  }

  const outputDir = request.output_dir;
  const viewport = request.viewport || defaultViewport;
  const browser = await engine.launch(launchOptionsWithProxy({ headless: request.recording_run_spec?.browser?.headless ?? request.headless ?? true }));
  const context = await browser.newContext({
    viewport,
    deviceScaleFactor: 1,
    recordVideo: { dir: outputDir, size: viewport },
  });
  const tracePath = path.join(outputDir, "trace.zip");
  await context.tracing.start({ screenshots: true, snapshots: true, sources: false });

  const page = await context.newPage();
  const allowedDomains = effectiveAllowedDomains(request);
  const forbiddenPages = request.executable_script_bundle?.security_policy?.forbidden_pages || [];
  await page.route("**/*", async (route: any) => {
    const requestURL = route.request().url();
    const policyError = urlPolicyError(requestURL, allowedDomains, forbiddenPages, request.sandbox_policy?.network_policy?.mode);
    if (policyError) {
      await route.abort("blockedbyclient");
      return;
    }
    await route.continue();
  });
  const steps = request.executable_script_bundle?.plan_json?.steps || [];
  const screenshotPaths: string[] = [];
  const stepResults: NonNullable<BrowserRecordResult["step_results"]> = [];
  const generatedAssets: NonNullable<BrowserRecordResult["generated_assets"]> = [];
  const video = page.video?.();
  let previousScreenshotSHA256: string | undefined;
  let failureDiagnostic: BrowserFailureDiagnostic | undefined;

  try {
    for (const [index, step] of steps.entries()) {
      const started = Date.now();
      try {
        await runStep(page, step, allowedDomains, forbiddenPages, request.sandbox_policy?.network_policy?.mode);
        await waitForStepDuration(page, step, started);
        const screenshotCapture = await captureStepScreenshot(page, outputDir, step, index, request.recording_run_spec?.redactions?.mask_selectors || []);
        const screenshotPath = screenshotCapture.path;
        if (screenshotPath) {
          const screenshotAsset = await artifactRef(
            `artifact_screenshot_${String(index + 1).padStart(3, "0")}`,
            "screenshot",
            screenshotPath,
            "image/png",
            step.node_id,
            screenshotMetadata(step),
          );
          if (shouldKeepScreenshot(step, screenshotAsset, previousScreenshotSHA256)) {
            screenshotPaths.push(screenshotPath);
            generatedAssets.push(screenshotAsset);
            previousScreenshotSHA256 = screenshotAsset.sha256;
          } else {
            await unlink(screenshotPath).catch(() => undefined);
          }
        }
        const stepResult: NonNullable<BrowserRecordResult["step_results"]>[number] = {
          node_id: step.node_id || `step_${index + 1}`,
          status: "passed",
          duration_ms: Date.now() - started,
        };
        const observedState = [step.expected_outcome, screenshotCapture.warning ? `screenshot_warning: ${screenshotCapture.warning}` : ""]
          .filter(Boolean)
          .join(" | ");
        if (observedState) {
          stepResult.observed_state = observedState;
        }
        stepResults.push(stepResult);
      } catch (error) {
        const failedNodeID = step.node_id || `step_${index + 1}`;
        const message = error instanceof Error ? error.message : String(error);
        const failureScreenshotPath = await captureFailureScreenshot(page, outputDir, index, request.recording_run_spec?.redactions?.mask_selectors || []);
        let failureScreenshotAsset: BrowserArtifactRef | undefined;
        if (failureScreenshotPath) {
          failureScreenshotAsset = await artifactRef(
            `artifact_failure_screenshot_${String(index + 1).padStart(3, "0")}`,
            "failure_screenshot",
            failureScreenshotPath,
            "image/png",
            failedNodeID,
            {
              asset_role: "failure_screenshot",
              include_in_demo: false,
              capture_scope: "viewport",
              action_type: step.action?.type || "inspect",
              sensitive: true,
            },
          );
          screenshotPaths.push(failureScreenshotPath);
          generatedAssets.push(failureScreenshotAsset);
        }
        stepResults.push({
          node_id: failedNodeID,
          status: "failed",
          duration_ms: Date.now() - started,
          observed_state: message,
        });
        failureDiagnostic = {
          id: `diag_${failedNodeID}`,
          schema_version: "demoops.script_failure_diagnostic.v1",
          failed_node_id: failedNodeID,
          failed_step_order: index + 1,
          attempt: 1,
          error: { code: classifyPlaywrightError(message), message: redactDiagnosticText(message), retryable: true },
          current_url: redactDiagnosticText(page.url?.() || ""),
          page_title: redactDiagnosticText(await page.title().catch(() => "")),
          redaction_report: {
            applied: true,
            policy_ref: "recording_run_spec.redactions",
            masked_selectors: request.recording_run_spec?.redactions?.mask_selectors || [],
            full_html_included: false,
          },
          captured_at: new Date().toISOString(),
        };
        if (failureScreenshotAsset) {
          failureDiagnostic.screenshot_refs = [diagnosticDescriptor(failureScreenshotAsset, "failure_screenshot")];
        }
        if (step.blocking !== false) {
          break;
        }
      }
    }
  } finally {
    await context.tracing.stop({ path: tracePath }).catch(() => undefined);
    await context.close().catch(() => undefined);
    await browser.close().catch(() => undefined);
  }

  const recordingPath = await video?.path().catch(() => undefined);
  if (recordingPath) {
    generatedAssets.unshift(
      await artifactRef("artifact_raw_recording", "raw_recording", recordingPath, "video/webm", undefined, {
        asset_role: "raw_recording",
        include_in_demo: true,
        capture_scope: "browser_context_video",
      }),
    );
  }
  generatedAssets.push(
    await artifactRef("artifact_browser_trace", "browser_trace", tracePath, "application/zip", undefined, {
      asset_role: "debug_trace",
      include_in_demo: false,
    }),
  );

  const result: BrowserRecordResult = {
    screenshot_paths: screenshotPaths,
    trace_path: tracePath,
    step_results: stepResults,
    generated_assets: generatedAssets,
    runtime_versions: { runner: "playwright", browser: engineName },
  };
  const sandboxMetadata = sandboxMetadataFromRequest(request, engineName);
  if (sandboxMetadata) {
    result.sandbox_metadata = sandboxMetadata;
  }
  if (failureDiagnostic) {
    const traceAsset = generatedAssets.find((asset) => asset.kind === "browser_trace");
    if (traceAsset) {
      failureDiagnostic.trace_refs = [diagnosticDescriptor(traceAsset, "failure_trace")];
    }
    result.failure_diagnostic = failureDiagnostic;
  }
  if (recordingPath) {
    result.recording_path = recordingPath;
  }
  return result;
}

async function runStep(page: any, step: BrowserScriptStep, allowedDomains: string[], forbiddenPages: string[], networkMode?: string): Promise<void> {
  const action = step.action?.type || "inspect";
  const timeout = step.action?.timeout_ms || 10000;
  if (action === "navigate") {
    const url = step.action?.target?.url || step.page_target?.url;
    if (!url) throw new Error("navigate step missing URL");
    const policyError = urlPolicyError(url, allowedDomains, forbiddenPages, networkMode);
    if (policyError) throw new Error(policyError);
    await page.goto(url, { waitUntil: waitUntil(step.action?.wait_until), timeout });
    await waitForPageSettled(page);
    return;
  }
  if (action === "click") {
    await locatorForStep(page, step).click({ timeout });
    return;
  }
  if (action === "fill") {
    await locatorForStep(page, step).fill(step.action?.value || "", { timeout });
    return;
  }
  if (action === "select") {
    await locatorForStep(page, step).selectOption(step.action?.value || "", { timeout });
    return;
  }
  if (action === "wait") {
    const selector = selectorForStep(step);
    if (selector) {
      await page
        .locator(selector)
        .first()
        .waitFor({ timeout: Math.min(timeout, 3000) })
        .catch(() => undefined);
    }
    await page.waitForTimeout(Math.max(0, step.timing?.duration_ms || 1000));
    return;
  }
  if (action === "inspect") {
    const selector = selectorForStep(step);
    if (selector) {
      await page
        .locator(selector)
        .first()
        .waitFor({ timeout: Math.min(timeout, 3000) })
        .catch(() => undefined);
    }
    await waitForPageSettled(page, Math.min(timeout, pageSettleTimeoutMS));
    return;
  }
  if (action === "assert") {
    const selector = selectorForStep(step);
    if (selector) await page.locator(selector).first().waitFor({ timeout });
    await waitForPageSettled(page, Math.min(timeout, pageSettleTimeoutMS));
  }
}

async function waitForStepDuration(page: any, step: BrowserScriptStep, startedAtMS: number): Promise<void> {
  const durationMS = step.timing?.duration_ms || 0;
  if (durationMS <= 0) return;
  const remainingMS = durationMS - (Date.now() - startedAtMS);
  if (remainingMS > 0) {
    await page.waitForTimeout(remainingMS);
  }
}

async function captureStepScreenshot(page: any, outputDir: string, step: BrowserScriptStep, index: number, globalMaskSelectors: string[]): Promise<ScreenshotCaptureResult> {
  if (!step.capture?.screenshot) return {};
  const screenshotPath = path.join(outputDir, `step-${String(index + 1).padStart(3, "0")}.png`);
  const maskSelectors = [...globalMaskSelectors, ...(step.capture.mask_selectors || [])].filter(Boolean);
  const mask = maskSelectors.map((selector) => page.locator(selector));
  const scope = captureScope(step);
  try {
    await waitForPageSettled(page);
    if (scope === "element") {
      const selector = step.capture.selector || step.capture.focus_selector || selectorForStep(step);
      if (!selector) return { warning: `step ${step.node_id || "unknown"} requested element screenshot without selector` };
      await page.locator(selector).first().screenshot({ path: screenshotPath, mask, timeout: screenshotTimeoutMS });
      return { path: screenshotPath };
    }
    await page.screenshot({ path: screenshotPath, fullPage: scope === "full_page", mask, timeout: screenshotTimeoutMS });
    return { path: screenshotPath };
  } catch (error) {
    await unlink(screenshotPath).catch(() => undefined);
    return { warning: redactDiagnosticText(error instanceof Error ? error.message : String(error)) };
  }
}

async function captureFailureScreenshot(page: any, outputDir: string, index: number, globalMaskSelectors: string[]): Promise<string | undefined> {
  const screenshotPath = path.join(outputDir, `failure-step-${String(index + 1).padStart(3, "0")}.png`);
  const mask = globalMaskSelectors.filter(Boolean).map((selector) => page.locator(selector));
  await page.screenshot({ path: screenshotPath, fullPage: false, mask }).catch(() => undefined);
  const fileStat = await stat(screenshotPath).catch(() => undefined);
  return fileStat?.isFile() ? screenshotPath : undefined;
}

function captureScope(step: BrowserScriptStep): "viewport" | "full_page" | "element" {
  if (step.capture?.scope === "element") return "element";
  if (step.capture?.scope === "viewport" || step.capture?.full_page === false) return "viewport";
  if (step.capture?.scope === "full_page" || step.capture?.full_page === true) return "full_page";
  return "full_page";
}

function shouldKeepScreenshot(step: BrowserScriptStep, asset: BrowserArtifactRef, previousSHA256?: string): boolean {
  if (step.capture?.dedupe === false) return true;
  if (!asset.sha256 || !previousSHA256) return true;
  return asset.sha256 !== previousSHA256;
}

function screenshotMetadata(step: BrowserScriptStep): Record<string, unknown> {
  return {
    asset_role: step.capture?.asset_role || "primary",
    include_in_demo: step.capture?.include_in_demo ?? true,
    capture_scope: captureScope(step),
    dedupe_policy: step.capture?.dedupe === false ? "disabled" : "adjacent_sha256",
    action_type: step.action?.type || "inspect",
  };
}

function locatorForStep(page: any, step: BrowserScriptStep): any {
  const selector = selectorForStep(step);
  if (selector) return page.locator(selector).first();
  if (step.action?.target?.test_id) return page.getByTestId(step.action.target.test_id);
  if (step.action?.target?.label) return page.getByLabel(step.action.target.label);
  if (step.action?.target?.text) return page.getByText(step.action.target.text);
  if (step.action?.target?.role && step.action?.target?.text) return page.getByRole(step.action.target.role, { name: step.action.target.text });
  throw new Error(`step ${step.node_id || "unknown"} is missing a selector target`);
}

function selectorForStep(step: BrowserScriptStep): string | undefined {
  return step.action?.target?.selector || step.page_target?.selector;
}

function waitUntil(value?: string): "load" | "domcontentloaded" | "networkidle" {
  if (value === "domcontentloaded" || value === "networkidle") return value;
  return "load";
}

async function waitForPageSettled(page: any, timeoutMS = pageSettleTimeoutMS): Promise<void> {
  await page.waitForLoadState("networkidle", { timeout: timeoutMS }).catch(() => undefined);
  await page.waitForTimeout(pageSettleFrameMS).catch(() => undefined);
}

function classifyPlaywrightError(message: string): string {
  const lower = message.toLowerCase();
  if (lower.includes("domain_not_allowed")) return "domain_not_allowed";
  if (lower.includes("timeout")) return "selector_timeout";
  if (lower.includes("net::") || lower.includes("navigation")) return "navigation_failed";
  if (lower.includes("missing selector") || lower.includes("selector target")) return "selector_missing";
  return "playwright_step_failed";
}

function redactDiagnosticText(value: string): string {
  return value
    .replace(/authorization[:=]\s*[^\s,;]+/gi, "authorization=[redacted]")
    .replace(/cookie[:=]\s*[^\s,;]+/gi, "cookie=[redacted]")
    .replace(/bearer\s+[^\s,;]+/gi, "bearer [redacted]")
    .replace(/sk-[A-Za-z0-9_-]+/g, "sk-[redacted]");
}

function normalizeEngine(value?: string): BrowserEngineName {
  if (value === "firefox" || value === "webkit") return value;
  return "chromium";
}

function fileURI(filePath: string): string {
  return pathToFileURL(path.resolve(filePath)).toString();
}

async function artifactRef(
  id: string,
  kind: string,
  filePath: string,
  mimeType: string,
  sourceNodeID?: string,
  metadata?: Record<string, unknown>,
): Promise<BrowserArtifactRef> {
  const data = await readFile(filePath);
  const ref: BrowserArtifactRef = {
    id,
    kind,
    uri: fileURI(filePath),
    mime_type: mimeType,
    sha256: createHash("sha256").update(data).digest("hex"),
    size_bytes: data.length,
    created_at: new Date().toISOString(),
  };
  if (sourceNodeID) {
    ref.source_node_id = sourceNodeID;
  }
  if (metadata?.sensitive === true) {
    ref.sensitive = true;
  }
  if (metadata && Object.keys(metadata).length > 0) {
    ref.metadata = metadata;
  }
  return ref;
}

function diagnosticDescriptor(asset: BrowserArtifactRef, role: string): BrowserPackageArtifactDescriptor {
  const descriptor: BrowserPackageArtifactDescriptor = {
    id: asset.id,
    role,
    kind: asset.kind,
    uri: asset.uri,
    encrypted: true,
    sensitive: true,
    metadata: { ...(asset.metadata || {}), dev_local_artifact: true },
  };
  if (asset.mime_type) descriptor.mime_type = asset.mime_type;
  if (asset.sha256) descriptor.sha256 = asset.sha256;
  if (asset.size_bytes !== undefined) descriptor.size_bytes = asset.size_bytes;
  return descriptor;
}

function effectiveAllowedDomains(request: BrowserRecordRequest): string[] {
  return (
    request.sandbox_policy?.network_policy?.allowed_domains ||
    request.executable_script_bundle?.security_policy?.allowed_domains ||
    request.recording_run_spec?.allowed_domains ||
    request.executable_script_bundle?.plan_json?.recording_run_spec?.allowed_domains ||
    []
  );
}

function urlPolicyError(value: string, allowedDomains: string[], forbiddenPages: string[], networkMode?: string): string | undefined {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return `domain_not_allowed: invalid URL ${value}`;
  }
  if (networkMode === "deny_all" || networkMode === "no_customer_network") {
    return `domain_not_allowed: sandbox network policy blocks customer navigation to ${parsed.host}`;
  }
  if (allowedDomains.length > 0 && !hostAllowed(parsed.host, allowedDomains)) {
    return `domain_not_allowed: ${parsed.host} is not in allowed_domains`;
  }
  for (const forbidden of forbiddenPages) {
    if (forbidden && parsed.pathname.includes(forbidden)) {
      return `domain_not_allowed: forbidden page ${forbidden}`;
    }
  }
  return undefined;
}

function hostAllowed(host: string, allowedDomains: string[]): boolean {
  const normalized = host.toLowerCase();
  return allowedDomains.some((domain) => {
    const allowed = domain.trim().toLowerCase();
    return Boolean(allowed) && (normalized === allowed || normalized.endsWith(`.${allowed}`));
  });
}

function sandboxMetadataFromRequest(request: BrowserRecordRequest, browser: string): BrowserSandboxMetadata | undefined {
  const policy = request.sandbox_policy;
  if (!policy) return undefined;
  const metadata: BrowserSandboxMetadata = {
    worker_id: "video-worker-local",
    runtime_versions: { runner: "playwright", browser },
  };
  if (policy.policy_hash_sha256) metadata.policy_hash_sha256 = policy.policy_hash_sha256;
  if (policy.profile) metadata.profile = policy.profile;
  if (policy.isolation_mode) metadata.isolation_mode = policy.isolation_mode;
  if (policy.network_policy?.mode) metadata.network_mode = policy.network_policy.mode;
  return metadata;
}
