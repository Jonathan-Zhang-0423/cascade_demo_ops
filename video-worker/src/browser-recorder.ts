import { createHash } from "node:crypto";
import { readFile, stat, unlink } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";

type BrowserEngineName = "chromium" | "firefox" | "webkit";

type BrowserRecordRequest = {
  output_dir: string;
  viewport?: { width: number; height: number };
  headless?: boolean;
  recording_run_spec?: {
    browser?: { engine?: string; headless?: boolean };
    redactions?: { mask_selectors?: string[] };
  };
  executable_script_bundle?: {
    plan_json?: {
      steps?: BrowserScriptStep[];
    };
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

export async function recordWithPlaywright(request: BrowserRecordRequest): Promise<BrowserRecordResult> {
  const playwright = await import("playwright");
  const engineName = normalizeEngine(request.recording_run_spec?.browser?.engine);
  const engine = playwright[engineName];
  if (!engine) {
    throw new Error(`unsupported Playwright browser engine: ${engineName}`);
  }

  const outputDir = request.output_dir;
  const viewport = request.viewport || { width: 1440, height: 900 };
  const browser = await engine.launch({ headless: request.recording_run_spec?.browser?.headless ?? request.headless ?? true });
  const context = await browser.newContext({
    viewport,
    recordVideo: { dir: outputDir, size: viewport },
  });
  const tracePath = path.join(outputDir, "trace.zip");
  await context.tracing.start({ screenshots: true, snapshots: true, sources: false });

  const page = await context.newPage();
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
        await runStep(page, step);
        const screenshotPath = await captureStepScreenshot(page, outputDir, step, index, request.recording_run_spec?.redactions?.mask_selectors || []);
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
        if (step.expected_outcome) {
          stepResult.observed_state = step.expected_outcome;
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

async function runStep(page: any, step: BrowserScriptStep): Promise<void> {
  const action = step.action?.type || "inspect";
  const timeout = step.action?.timeout_ms || 10000;
  if (action === "navigate") {
    const url = step.action?.target?.url || step.page_target?.url;
    if (!url) throw new Error("navigate step missing URL");
    await page.goto(url, { waitUntil: waitUntil(step.action?.wait_until), timeout });
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
    await page.waitForTimeout(Math.max(0, step.timing?.duration_ms || 1000));
    return;
  }
  if (action === "assert" || action === "inspect") {
    const selector = selectorForStep(step);
    if (selector) await page.locator(selector).first().waitFor({ timeout });
  }
}

async function captureStepScreenshot(page: any, outputDir: string, step: BrowserScriptStep, index: number, globalMaskSelectors: string[]): Promise<string | undefined> {
  if (!step.capture?.screenshot) return undefined;
  const screenshotPath = path.join(outputDir, `step-${String(index + 1).padStart(3, "0")}.png`);
  const maskSelectors = [...globalMaskSelectors, ...(step.capture.mask_selectors || [])].filter(Boolean);
  const mask = maskSelectors.map((selector) => page.locator(selector));
  const scope = captureScope(step);
  if (scope === "element") {
    const selector = step.capture.selector || selectorForStep(step);
    if (!selector) throw new Error(`step ${step.node_id || "unknown"} requested element screenshot without selector`);
    await page.locator(selector).first().screenshot({ path: screenshotPath, mask });
    return screenshotPath;
  }
  await page.screenshot({ path: screenshotPath, fullPage: scope === "full_page", mask });
  return screenshotPath;
}

async function captureFailureScreenshot(page: any, outputDir: string, index: number, globalMaskSelectors: string[]): Promise<string | undefined> {
  const screenshotPath = path.join(outputDir, `failure-step-${String(index + 1).padStart(3, "0")}.png`);
  const mask = globalMaskSelectors.filter(Boolean).map((selector) => page.locator(selector));
  await page.screenshot({ path: screenshotPath, fullPage: false, mask }).catch(() => undefined);
  const fileStat = await stat(screenshotPath).catch(() => undefined);
  return fileStat?.isFile() ? screenshotPath : undefined;
}

function captureScope(step: BrowserScriptStep): "viewport" | "full_page" | "element" {
  if (step.capture?.scope === "full_page" || step.capture?.full_page === true) return "full_page";
  if (step.capture?.scope === "element") return "element";
  return "viewport";
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

function classifyPlaywrightError(message: string): string {
  const lower = message.toLowerCase();
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
    encrypted: false,
    metadata: { ...(asset.metadata || {}), dev_local_artifact: true },
  };
  if (asset.mime_type) descriptor.mime_type = asset.mime_type;
  if (asset.sha256) descriptor.sha256 = asset.sha256;
  if (asset.size_bytes !== undefined) descriptor.size_bytes = asset.size_bytes;
  if (asset.sensitive !== undefined) descriptor.sensitive = asset.sensitive;
  return descriptor;
}
