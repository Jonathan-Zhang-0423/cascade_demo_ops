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
  capture?: { screenshot?: boolean; mask_selectors?: string[] };
  timing?: { duration_ms?: number };
  blocking?: boolean;
};

export type BrowserRecordResult = {
  recording_path?: string;
  screenshot_paths?: string[];
  trace_path?: string;
  step_results?: Array<{ node_id: string; status: string; duration_ms?: number; observed_state?: string }>;
  generated_assets?: Array<{
    id: string;
    kind: string;
    uri: string;
    mime_type?: string;
    source_node_id?: string;
  }>;
  runtime_versions?: Record<string, string>;
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

  try {
    for (const [index, step] of steps.entries()) {
      const started = Date.now();
      try {
        await runStep(page, step);
        const screenshotPath = await captureStepScreenshot(page, outputDir, step, index, request.recording_run_spec?.redactions?.mask_selectors || []);
        if (screenshotPath) {
          screenshotPaths.push(screenshotPath);
          const screenshotAsset: NonNullable<BrowserRecordResult["generated_assets"]>[number] = {
            id: `artifact_screenshot_${String(index + 1).padStart(3, "0")}`,
            kind: "screenshot",
            uri: fileURI(screenshotPath),
            mime_type: "image/png",
          };
          if (step.node_id) {
            screenshotAsset.source_node_id = step.node_id;
          }
          generatedAssets.push(screenshotAsset);
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
    generatedAssets.unshift({
      id: "artifact_raw_recording",
      kind: "raw_recording",
      uri: fileURI(recordingPath),
      mime_type: "video/webm",
    });
  }
  generatedAssets.push({
    id: "artifact_browser_trace",
    kind: "browser_trace",
    uri: fileURI(tracePath),
    mime_type: "application/zip",
  });

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
  await page.screenshot({ path: screenshotPath, fullPage: true, mask });
  return screenshotPath;
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
