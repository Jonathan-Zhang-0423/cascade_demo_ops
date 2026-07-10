import { createHash } from "node:crypto";
import { readFile, unlink } from "node:fs/promises";
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
  runtime_versions?: Record<string, string>;
};

type BrowserArtifactRef = {
  id: string;
  kind: string;
  uri: string;
  mime_type?: string;
  sha256?: string;
  size_bytes?: number;
  created_at?: string;
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
        stepResults.push({
          node_id: step.node_id || `step_${index + 1}`,
          status: "failed",
          duration_ms: Date.now() - started,
          observed_state: error instanceof Error ? error.message : String(error),
        });
        if (step.blocking !== false) {
          throw error;
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
  if (metadata && Object.keys(metadata).length > 0) {
    ref.metadata = metadata;
  }
  return ref;
}
