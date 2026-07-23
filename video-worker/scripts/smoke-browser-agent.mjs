import { existsSync, mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawn } from "node:child_process";
import readline from "node:readline";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const workerRoot = path.resolve(scriptDir, "..");
const repoRoot = path.resolve(workerRoot, "..");
const outputDir = mkdtempSync(path.join(repoRoot, ".browser-agent-smoke-"));
const keepOutput = process.env.CASCADE_BROWSER_AGENT_SMOKE_KEEP === "1";
const pageHTML = `<!doctype html>
<html lang="en">
  <head><meta charset="utf-8"><title>Browser Agent Smoke</title></head>
  <body>
    <main aria-label="Dashboard">
      <h1>Dashboard</h1>
      <button type="button" data-testid="invite-member">Invite teammate</button>
      <p id="status">Waiting</p>
    </main>
    <script>
      document.querySelector('[data-testid="invite-member"]').addEventListener('click', () => {
        document.querySelector('#status').textContent = 'Invite flow starts';
      });
    </script>
  </body>
</html>`;

const server = createServer((_, response) => {
  response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  response.end(pageHTML);
});

let worker;
try {
  const baseURL = await listen(server);
  worker = startWorker();
  const open = await worker.call("browser_agent_open", {
    session_id: "smoke_browser_agent",
    output_dir: outputDir,
    browser: { engine: "chromium", headless: true, viewport: { width: 960, height: 640 }, record_video: true },
    allowed_domains: ["127.0.0.1"],
    forbidden_pages: ["/billing"],
    forbidden_path_prefixes: ["/v1"],
    forbidden_keywords: ["delete"],
    mask_selectors: [],
  });
  assert(open.session_id === "smoke_browser_agent", "worker did not preserve session identity");

  const navigateStage = {
    id: "stage_open_dashboard",
    order: 1,
    node_id: "node_open_dashboard",
    objective: "Dashboard loads",
    url: baseURL,
    target_contract: { semantic_id: "target_dashboard", allowed_roles: ["main"], allowed_names: ["Dashboard"], destructive: false },
    components: [],
    interactions: [{ kind: "navigate", target: { url: baseURL }, wait_until: "domcontentloaded", non_destructive: true }],
    validations: [{ id: "validation_dashboard_url", kind: "url_matches", target: { url: baseURL }, required: true }],
    wait_conditions: ["wait_after_entry_at_least_100ms"],
    success_state: "Dashboard loads",
  };
  const beforeNavigate = await worker.call("browser_agent_observe", { session_id: open.session_id, stage: navigateStage });
  const afterNavigate = await worker.call("browser_agent_execute", { session_id: open.session_id, stage: navigateStage });
  assert(beforeNavigate.evidence_refs.length === 1, "navigate observation evidence missing");
  assert(afterNavigate.observation.url === baseURL, `navigate result URL mismatch: ${afterNavigate.observation.url}`);

  const clickStage = {
    id: "stage_invite_member",
    order: 2,
    node_id: "node_invite_member",
    objective: "Invite flow starts",
    route: "/",
    target_contract: {
      semantic_id: "target_invite_member",
      allowed_roles: ["button"],
      allowed_names: ["Invite teammate"],
      component_ref: "component:invite-button",
      destructive: false,
    },
    components: [{ component_ref: "component:invite-button", role: "button", name: "Invite teammate", test_id: "invite-member" }],
    interactions: [{ kind: "click", target: { test_id: "invite-member" }, non_destructive: true }],
    validations: [{ id: "validation_invite_visible", kind: "element_visible", target: { test_id: "invite-member" }, required: true }],
    success_state: "Invite flow starts",
  };
  const beforeClick = await worker.call("browser_agent_observe", { session_id: open.session_id, stage: clickStage });
  let semanticMismatchBlocked = false;
  try {
    await worker.call("browser_agent_observe", {
      session_id: open.session_id,
      stage: { ...clickStage, target_contract: { ...clickStage.target_contract, allowed_names: ["Delete workspace"] } },
    });
  } catch (error) {
    semanticMismatchBlocked = String(error).includes("browser_agent_target_not_resolved");
  }
  assert(semanticMismatchBlocked, "target contract name mismatch was not blocked");
  const afterClick = await worker.call("browser_agent_execute", { session_id: open.session_id, stage: clickStage });
  assert(beforeClick.target_resolved === true, "semantic target was not resolved");
  assert(afterClick.observation.assertions.every((item) => item.passed), "click completion assertion failed");
  assert(afterClick.observation.assertions.some((item) => item.kind === "required_element_visible:validation_invite_visible"), "required validation result missing");

  const close = await worker.call("browser_agent_close", { session_id: open.session_id });
  await worker.close();
  worker = undefined;
  assert(close.artifacts.some((artifact) => artifact.kind === "raw_recording"), "raw recording artifact missing");
  assert(close.artifacts.some((artifact) => artifact.kind === "browser_trace"), "browser trace artifact missing");
  for (const result of [beforeNavigate, afterNavigate, beforeClick, afterClick]) {
    for (const artifact of result.artifacts) {
      assert(existsSync(new URL(artifact.uri)), `screenshot artifact missing: ${artifact.uri}`);
      assert(artifact.sha256 && artifact.size_bytes > 0, `screenshot checksum missing: ${artifact.id}`);
    }
  }
  assert(existsSync(close.recording_path), "recording file does not exist");
  assert(existsSync(close.trace_path), "trace file does not exist");
  console.log(JSON.stringify({
    ok: true,
    stages: 2,
    semantic_target_resolved: true,
    semantic_mismatch_blocked: true,
    screenshots: 4,
    recording: path.basename(close.recording_path),
    trace: path.basename(close.trace_path),
  }));
} finally {
  if (worker) await worker.close().catch(() => worker.kill());
  await closeServer(server);
  if (!keepOutput) rmSync(outputDir, { recursive: true, force: true });
}

function startWorker() {
  const child = spawn(process.execPath, [path.join(workerRoot, "dist", "index.js")], { cwd: repoRoot, windowsHide: true });
  const lines = readline.createInterface({ input: child.stdout, crlfDelay: Infinity });
  const pending = [];
  let nextID = 0;
  let stderr = "";
  child.stderr.on("data", (chunk) => { stderr += String(chunk); });
  lines.on("line", (line) => {
    const request = pending.shift();
    if (!request) return;
    try {
      const response = JSON.parse(line);
      if (response.error) request.reject(new Error(response.error.message));
      else request.resolve(response.result);
    } catch (error) {
      request.reject(error);
    }
  });
  child.on("close", (code) => {
    while (pending.length > 0) pending.shift().reject(new Error(`video-worker exited ${code}: ${stderr}`));
  });
  return {
    call(method, params) {
      nextID += 1;
      return new Promise((resolve, reject) => {
        pending.push({ resolve, reject });
        child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: nextID, method, params })}\n`);
      });
    },
    close() {
      return new Promise((resolve, reject) => {
        child.once("error", reject);
        child.once("close", (code) => code === 0 ? resolve() : reject(new Error(`video-worker exited ${code}: ${stderr}`)));
        child.stdin.end();
      });
    },
    kill() { child.kill(); },
  };
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function listen(target) {
  return new Promise((resolve, reject) => {
    target.once("error", reject);
    target.listen(0, "127.0.0.1", () => {
      const address = target.address();
      resolve(`http://127.0.0.1:${address.port}/`);
    });
  });
}

function closeServer(target) {
  return new Promise((resolve) => target.close(() => resolve()));
}
