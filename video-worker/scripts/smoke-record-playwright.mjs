import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, rmSync, statSync } from "node:fs";
import { createServer } from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawn } from "node:child_process";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const workerRoot = path.resolve(scriptDir, "..");
const repoRoot = path.resolve(workerRoot, "..");
const outputDir = resolveOutputDir();
const keepOutput = process.env.CASCADE_RECORD_SMOKE_KEEP === "1" || Boolean(process.env.CASCADE_RECORD_SMOKE_OUTPUT_DIR);
const targetURL = process.env.CASCADE_RECORD_SMOKE_TARGET_URL?.trim();
const forceFailure = process.env.CASCADE_RECORD_SMOKE_FAIL === "1";

const pageHTML = `<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <title>Cascade Recording Smoke</title>
    <style>
      body { font-family: Arial, sans-serif; margin: 48px; }
      main { max-width: 720px; }
      label, input, button { display: block; margin-top: 16px; }
      input { width: 320px; padding: 10px; }
      button { padding: 10px 14px; }
      #status { margin-top: 20px; color: #555; }
      #status.done { color: #0a7a3f; font-weight: 700; }
    </style>
  </head>
  <body>
    <main>
      <h1>Demo signup</h1>
      <label for="email">Work email</label>
      <input id="email" />
      <button id="start">Start demo</button>
      <div id="status">Waiting</div>
    </main>
    <script>
      document.querySelector("#start").addEventListener("click", () => {
        const status = document.querySelector("#status");
        status.textContent = "Demo is ready";
        status.className = "done";
      });
    </script>
  </body>
</html>`;

const server = createServer((_, response) => {
  response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  response.end(pageHTML);
});

try {
  const baseURL = targetURL || (await listen(server));
  const request = buildRecordRequest(baseURL, { interactiveLocalPage: !targetURL });
  const result = await callWorker(request);
  if (forceFailure) {
    assertFailedRecordingResult(result);
  } else {
    assertRealRecordingResult(result);
  }
  console.log(
    JSON.stringify({
      ok: true,
      expected_failure: forceFailure,
      output_dir: outputDir,
      target_url: baseURL,
      recording: path.basename(result.recording_path),
      screenshots: result.screenshot_paths.length,
      screenshot_paths: result.screenshot_paths,
      trace: path.basename(result.trace_path),
      manifest: path.basename(result.artifact_manifest_path),
      generated_assets: result.generated_assets.length,
    }),
  );
} finally {
  if (!targetURL) {
    await close(server);
  }
  if (!keepOutput) {
    rmSync(outputDir, { recursive: true, force: true });
  }
}

function resolveOutputDir() {
  const configured = process.env.CASCADE_RECORD_SMOKE_OUTPUT_DIR;
  if (!configured) {
    return mkdtempSync(path.join(repoRoot, ".playwright-record-smoke-"));
  }
  const dir = path.isAbsolute(configured) ? configured : path.resolve(repoRoot, configured);
  mkdirSync(dir, { recursive: true });
  return dir;
}

function buildRecordRequest(baseURL, options) {
  const host = new URL(baseURL).host;
  const interactiveLocalPage = options.interactiveLocalPage;
  const source = interactiveLocalPage ? buildInteractiveScript(baseURL) : buildRealPageScript(baseURL);
  const steps = interactiveLocalPage ? buildInteractiveSteps(baseURL) : buildRealPageSteps(baseURL);
  const stepNodeIDs = steps.map((step) => step.node_id);
  const allowedPageMethods = interactiveLocalPage ? ["goto", "fill", "click"] : ["goto", "waitForLoadState"];
  const sourceHash = createHash("sha256").update(source).digest("hex");
  return {
    jsonrpc: "2.0",
    id: "smoke_record_playwright",
    method: "record",
    params: {
      output_dir: outputDir,
      recording_mode: "playwright",
      viewport: { width: 960, height: 640 },
      headless: true,
      recording_run_spec: {
        browser: { engine: "chromium", headless: true },
        redactions: { mask_selectors: [] },
      },
      executable_script_bundle: {
        script_manifest: {
          entry_function: "runCascadeRecording",
          language: "typescript",
          runtime: "playwright-restricted-sandbox",
          dependency_allowlist: [],
          step_node_ids: stepNodeIDs,
        },
        plan_json: {
          recording_run_spec: { allowed_domains: [host] },
          steps,
        },
        playwright_script: { inline_source: source, sha256: sourceHash },
        security_policy: {
          allowed_domains: [host],
          allowed_context_apis: ["ctx.page", "ctx.log"],
          allowed_page_methods: allowedPageMethods,
        },
        reproducibility: { script_hash_sha256: sourceHash },
      },
    },
  };
}

function buildInteractiveScript(baseURL) {
  const failureBlock = forceFailure ? `  ctx.log("node_missing_selector");
  await ctx.page.click("#does-not-exist", { timeout: 500 });
` : "";
  const source = `type CascadeRecordingContext = { page: any; secrets: any; capture: any; assert: any; log: any };
type CascadeRecordingResult = { ok: boolean };
export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> {
  await ctx.page.goto("${baseURL}");
  ctx.log("node_open");
  await ctx.page.fill("#email", "demo@example.com");
  ctx.log("node_fill");
${failureBlock}  await ctx.page.click("#start");
  ctx.log("node_click");
  ctx.log("node_assert");
  return { ok: true };
}`;
  return source;
}

function buildRealPageScript(baseURL) {
  return `type CascadeRecordingContext = { page: any; secrets: any; capture: any; assert: any; log: any };
type CascadeRecordingResult = { ok: boolean };
export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> {
  await ctx.page.goto("${baseURL}");
  ctx.log("node_open");
  await ctx.page.waitForLoadState("domcontentloaded");
  ctx.log("node_wait");
  ctx.log("node_assert");
  return { ok: true };
}`;
}

function buildInteractiveSteps(baseURL) {
  const steps = [
    {
      node_id: "node_open",
      action: { type: "navigate", target: { url: baseURL }, wait_until: "domcontentloaded", timeout_ms: 15000 },
      capture: { screenshot: true, scope: "viewport" },
      expected_outcome: "Landing page loads",
    },
    {
      node_id: "node_fill",
      action: { type: "fill", target: { selector: "#email" }, value: "demo@example.com", timeout_ms: 10000 },
      capture: { screenshot: true, scope: "viewport" },
      expected_outcome: "Email is entered",
    },
    {
      node_id: "node_click",
      action: { type: "click", target: { selector: "#start" }, timeout_ms: 10000 },
      capture: { screenshot: true, scope: "viewport" },
      expected_outcome: "Primary action is clicked",
    },
    {
      node_id: "node_assert",
      action: { type: "assert", target: { selector: "#status.done" }, timeout_ms: 10000 },
      capture: { screenshot: true, scope: "viewport" },
      expected_outcome: "Confirmation is visible",
    },
  ];
  if (forceFailure) {
    steps.splice(2, 0, {
      node_id: "node_missing_selector",
      action: { type: "click", target: { selector: "#does-not-exist" }, timeout_ms: 500 },
      capture: { screenshot: true, scope: "viewport" },
      expected_outcome: "This step intentionally fails for diagnostic smoke coverage",
    });
  }
  return steps;
}

function buildRealPageSteps(baseURL) {
  return [
    {
      node_id: "node_open",
      action: { type: "navigate", target: { url: baseURL }, wait_until: "domcontentloaded", timeout_ms: 45000 },
      capture: { screenshot: true, scope: "viewport" },
      expected_outcome: "Target page loads",
    },
    {
      node_id: "node_wait",
      action: { type: "wait" },
      timing: { duration_ms: 1000 },
      expected_outcome: "Page has settled after initial load",
    },
    {
      node_id: "node_assert",
      action: { type: "assert", target: { selector: "body" }, timeout_ms: 10000 },
      expected_outcome: "Page body is visible",
    },
  ];
}

function callWorker(request) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [path.join(workerRoot, "dist", "index.js")], { cwd: repoRoot });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk;
    });
    child.on("error", reject);
    child.on("close", (code) => {
      if (code !== 0) {
        reject(new Error(`video-worker exited ${code}: ${stderr || stdout}`));
        return;
      }
      try {
        const response = JSON.parse(stdout.trim());
        if (response.error) {
          reject(new Error(response.error.message));
          return;
        }
        resolve(response.result);
      } catch (error) {
        reject(new Error(`invalid worker response: ${stdout}\n${error instanceof Error ? error.message : String(error)}`));
      }
    });
    child.stdin.write(`${JSON.stringify(request)}\n`);
    child.stdin.end();
  });
}

function assertRealRecordingResult(result) {
  if (!result.recording_path || !existsSync(result.recording_path)) {
    throw new Error("recording_path is missing or does not exist");
  }
  if (!Array.isArray(result.screenshot_paths) || result.screenshot_paths.length < 1) {
    throw new Error("expected at least one non-duplicate step screenshot");
  }
  for (const screenshotPath of result.screenshot_paths) {
    if (!existsSync(screenshotPath)) {
      throw new Error(`screenshot is missing: ${screenshotPath}`);
    }
  }
  if (!result.trace_path || !existsSync(result.trace_path)) {
    throw new Error("trace_path is missing or does not exist");
  }
  if (!result.artifact_manifest_path || !existsSync(result.artifact_manifest_path)) {
    throw new Error("artifact_manifest_path is missing or does not exist");
  }
  const assets = result.generated_assets || [];
  for (const kind of ["raw_recording", "screenshot", "browser_trace"]) {
    if (!assets.some((asset) => asset.kind === kind)) {
      throw new Error(`generated_assets missing ${kind}`);
    }
  }
  for (const asset of assets) {
    if (!asset.sha256 || !asset.size_bytes || asset.size_bytes <= 0) {
      throw new Error(`asset is missing checksum or size: ${asset.id}`);
    }
  }
  if (statSync(result.recording_path).size <= 0) {
    throw new Error("recording file is empty");
  }
}

function assertFailedRecordingResult(result) {
  assertRealRecordingResult({ ...result, screenshot_paths: result.screenshot_paths || [] });
  const diagnostic = result.failure_diagnostic;
  if (!diagnostic) {
    throw new Error("expected failure_diagnostic");
  }
  if (diagnostic.failed_node_id !== "node_missing_selector") {
    throw new Error(`unexpected failed node: ${diagnostic.failed_node_id}`);
  }
  if (!diagnostic.current_url || !diagnostic.page_title) {
    throw new Error("failure diagnostic is missing current_url or page_title");
  }
  if (!Array.isArray(diagnostic.screenshot_refs) || diagnostic.screenshot_refs.length < 1) {
    throw new Error("failure diagnostic is missing screenshot_refs");
  }
  if (!Array.isArray(diagnostic.trace_refs) || diagnostic.trace_refs.length < 1) {
    throw new Error("failure diagnostic is missing trace_refs");
  }
  const failureScreenshot = result.generated_assets?.find((asset) => asset.kind === "failure_screenshot");
  if (!failureScreenshot || !existsSync(new URL(failureScreenshot.uri))) {
    throw new Error("failure screenshot asset is missing");
  }
}

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      resolve(`http://127.0.0.1:${address.port}/`);
    });
  });
}

function close(server) {
  return new Promise((resolve, reject) => {
    server.close((error) => {
      if (error) {
        reject(error);
        return;
      }
      resolve();
    });
  });
}
