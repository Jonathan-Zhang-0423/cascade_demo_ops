import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawn } from "node:child_process";
import readline from "node:readline";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const workerRoot = path.resolve(scriptDir, "..");
const repoRoot = path.resolve(workerRoot, "..");
const outputDir = path.resolve(readOption("--output-dir") || path.join(repoRoot, "artifacts", "browser-agent-acceptance", "latest"));
mkdirSync(outputDir, { recursive: true });

const pageHTML = `<!doctype html><html><head><title>Acceptance Dashboard</title></head><body>
<main aria-label="Dashboard"><h1>Dashboard</h1><button data-testid="invite-member">Invite teammate</button><p id="status">Waiting</p></main>
<script>document.querySelector('[data-testid="invite-member"]').addEventListener('click',()=>document.querySelector('#status').textContent='Invite flow starts')</script>
</body></html>`;
const delayedHTML = `<!doctype html><html><head><title>Delayed Acceptance</title></head><body>
<main aria-label="Delayed Dashboard"><h1>Delayed Dashboard</h1><section id="delayed-area" aria-busy="true"></section></main>
<script>setTimeout(()=>{document.querySelector('#delayed-area').innerHTML='<button data-testid="delayed-action">Delayed action</button>';document.querySelector('#delayed-area').setAttribute('aria-busy','false')},1500)</script>
</body></html>`;
const server = createServer((request, response) => { response.writeHead(200, { "content-type": "text/html; charset=utf-8" }); response.end(request.url === "/delayed" ? delayedHTML : pageHTML); });
let worker;
const report = { schema_version: "cascade.browser_agent_acceptance.v1", generated_at: new Date().toISOString(), runtime: "browser-agent-outline-v1", strict_gate: "passed", scenarios: [] };

try {
  const baseURL = await listen(server);
  worker = startWorker();
  const open = await worker.call("browser_agent_open", { session_id: "browser_agent_acceptance", output_dir: outputDir, browser: { engine: "chromium", headless: true, viewport: { width: 960, height: 640 }, record_video: true }, allowed_domains: ["127.0.0.1"], forbidden_pages: ["/billing"], forbidden_path_prefixes: ["/v1"], forbidden_keywords: ["delete"], mask_selectors: [] });
  const navigate = stageNavigate(baseURL);
  const navBefore = await worker.call("browser_agent_observe", { session_id: open.session_id, stage: navigate });
  const navAfter = await worker.call("browser_agent_execute", { session_id: open.session_id, stage: navigate });
  const invite = stageInvite();
  const inviteBefore = await worker.call("browser_agent_observe", { session_id: open.session_id, stage: invite });
  const inviteAfter = await worker.call("browser_agent_execute", { session_id: open.session_id, stage: invite });
  const evidence = [...navBefore.artifacts, ...navAfter.artifacts, ...inviteBefore.artifacts, ...inviteAfter.artifacts];
  add("success_navigation_click", "导航、识别经批准的按钮、点击并验证页面结果", "通过；含截图证据", navAfter.observation.url === baseURL && inviteBefore.target_resolved && inviteAfter.observation.assertions.every((item) => item.passed) && evidence.every(artifactExists), { action_executed: true, evidence: evidence.map(artifactView), assertions: [...navAfter.observation.assertions, ...inviteAfter.observation.assertions] });
  const semanticBlocked = await targetIsBlocked(worker, open.session_id, { ...invite, target_contract: { ...invite.target_contract, allowed_names: ["Delete workspace"] } });
  add("semantic_target_contract_conflict", "页面存在按钮但批准名称不同", "点击前拦截", semanticBlocked, { action_executed: false, evidence: [], assertions: [{ kind: "target_contract", passed: semanticBlocked, actual: semanticBlocked ? "blocked" : "unexpectedly_accepted" }] });
  const locatorBlocked = await targetIsBlocked(worker, open.session_id, { ...invite, node_id: "node_missing", target_contract: { ...invite.target_contract, allowed_names: ["Missing action"] } });
  add("locator_missing", "批准目标不在页面上", "点击前拦截", locatorBlocked, { action_executed: false, evidence: [], assertions: [{ kind: "locator_resolution", passed: locatorBlocked, actual: locatorBlocked ? "blocked" : "unexpectedly_accepted" }] });
  const fallback = stageApprovedFallback();
  const fallbackBefore = await worker.call("browser_agent_observe", { session_id: open.session_id, stage: fallback });
  const fallbackCandidate = fallbackBefore.preferred_selector_alternative;
  const fallbackResolved = await worker.call("browser_agent_observe", { session_id: open.session_id, stage: { ...fallback, preferred_selector_alternative: fallbackCandidate } });
  const fallbackAfter = await worker.call("browser_agent_execute", { session_id: open.session_id, stage: { ...fallback, preferred_selector_alternative: fallbackCandidate } });
  const fallbackPassed = fallbackBefore.target_resolved === false && fallbackCandidate?.kind === "testid" && fallbackCandidate.value === "invite-member" && fallbackResolved.target_resolved === true && fallbackAfter.observation.assertions.every((item) => item.passed);
  add("approved_selector_alternative_repair", "Primary selector fails and only an App-approved alternative may be retried", "A unique visible approved alternative resolves and executes", fallbackPassed, { action_executed: fallbackPassed, evidence: [...fallbackBefore.artifacts, ...fallbackResolved.artifacts, ...fallbackAfter.artifacts].map(artifactView), assertions: [...fallbackBefore.observation.assertions, ...fallbackResolved.observation.assertions, ...fallbackAfter.observation.assertions] });
  const delayedURL = new URL("/delayed", baseURL).toString();
  await worker.call("browser_agent_execute", { session_id: open.session_id, stage: stageNavigate(delayedURL) });
  const delayed = stageDelayedAction();
  const delayedBefore = await worker.call("browser_agent_observe", { session_id: open.session_id, stage: delayed });
  const delayedWait = delayedBefore.suggested_wait_condition;
  const delayedResolved = await worker.call("browser_agent_observe", { session_id: open.session_id, stage: { ...delayed, wait_conditions: [delayedWait] } });
  const delayedAfter = await worker.call("browser_agent_execute", { session_id: open.session_id, stage: { ...delayed, wait_conditions: [delayedWait] } });
  const delayedPassed = delayedBefore.target_resolved === false && delayedWait === "wait_after_entry_at_least_1100ms" && delayedResolved.target_resolved === true && delayedAfter.observation.assertions.every((item) => item.passed);
  add("busy_page_wait_repair", "A busy page with an unresolved target may propose one bounded entry wait", "Wait grows from 100ms to 1100ms and the target resolves", delayedPassed, { action_executed: delayedPassed, evidence: [...delayedBefore.artifacts, ...delayedResolved.artifacts, ...delayedAfter.artifacts].map(artifactView), assertions: [...delayedBefore.observation.assertions, ...delayedResolved.observation.assertions, ...delayedAfter.observation.assertions] });
  await worker.call("browser_agent_execute", { session_id: open.session_id, stage: stageNavigate(baseURL) });
  const validationResult = await worker.call("browser_agent_execute", { session_id: open.session_id, stage: { ...invite, id: "stage_failed_validation", node_id: "node_failed_validation", validations: [{ id: "validation_missing_text", kind: "text_contains", expected: "This text is intentionally absent", required: true }] } });
  const invalid = validationResult.observation.assertions.some((item) => item.kind === "required_text_contains:validation_missing_text" && !item.passed);
  add("required_validation_failure", "动作已完成但要求的业务结果不存在", "停止，不进入后续阶段", invalid, { action_executed: true, evidence: validationResult.artifacts.map(artifactView), assertions: validationResult.observation.assertions, stop_reason: invalid ? "required_validation_failed" : "required_validation_not_reported" });
  const close = await worker.call("browser_agent_close", { session_id: open.session_id });
  await worker.close(); worker = undefined;
  const retained = close.artifacts.some((item) => item.kind === "raw_recording" && artifactExists(item)) && close.artifacts.some((item) => item.kind === "browser_trace" && artifactExists(item));
  add("recording_and_trace_delivery", "关闭会话后保留可回放证据", "录屏和 trace 均存在", retained, { action_executed: false, evidence: close.artifacts.map(artifactView), assertions: [{ kind: "recording_and_trace", passed: retained, actual: retained ? "retained" : "missing" }] });
} catch (error) {
  report.strict_gate = "failed";
  add("acceptance_harness", "启动受控验收环境", "通过", false, { action_executed: false, evidence: [], assertions: [{ kind: "harness", passed: false, actual: String(error) }] });
} finally {
  if (worker) await worker.close().catch(() => worker.kill());
  await closeServer(server);
  if (report.scenarios.some((item) => item.verdict !== "passed")) report.strict_gate = "failed";
  writeFileSync(path.join(outputDir, "acceptance-report.json"), `${JSON.stringify(report, null, 2)}\n`, "utf8");
  console.log(JSON.stringify(report));
  if (report.strict_gate !== "passed") process.exitCode = 1;
}

function add(id, description, expected, passed, details) { report.scenarios.push({ id, description, expected, actual: passed ? "pass" : "fail", verdict: passed ? "passed" : "failed", ...details }); }
function stageNavigate(baseURL) { return { id: "stage_open_dashboard", order: 1, node_id: "node_open_dashboard", objective: "Dashboard loads", url: baseURL, target_contract: { semantic_id: "target_dashboard", allowed_roles: ["main"], allowed_names: ["Dashboard"], destructive: false }, components: [], interactions: [{ kind: "navigate", target: { url: baseURL }, wait_until: "domcontentloaded", non_destructive: true }], validations: [{ id: "validation_dashboard_url", kind: "url_matches", target: { url: baseURL }, required: true }], success_state: "Dashboard loads" }; }
function stageInvite() { return { id: "stage_invite_member", order: 2, node_id: "node_invite_member", objective: "Invite flow starts", route: "/", target_contract: { semantic_id: "target_invite_member", allowed_roles: ["button"], allowed_names: ["Invite teammate"], component_ref: "component:invite-button", destructive: false }, components: [{ component_ref: "component:invite-button", role: "button", name: "Invite teammate", test_id: "invite-member" }], interactions: [{ kind: "click", target: { test_id: "invite-member" }, non_destructive: true }], validations: [{ id: "validation_invite_visible", kind: "element_visible", target: { test_id: "invite-member" }, required: true }], success_state: "Invite flow starts" }; }
function stageApprovedFallback() { return { id: "stage_selector_fallback", order: 3, node_id: "node_selector_fallback", objective: "Use approved fallback", route: "/", target_contract: { semantic_id: "target_invite_fallback", component_ref: "component:invite-fallback", destructive: false }, components: [{ component_ref: "component:invite-fallback", selector: "[data-testid='stale-invite-member']", selector_alternatives: [{ kind: "testid", value: "invite-member" }] }], interactions: [{ kind: "click", target: { selector: "[data-testid='stale-invite-member']" }, non_destructive: true }], validations: [{ id: "validation_fallback_visible", kind: "element_visible", target: { test_id: "invite-member" }, required: true }], success_state: "Approved fallback clicked" }; }
function stageDelayedAction() { return { id: "stage_delayed_action", order: 4, node_id: "node_delayed_action", objective: "Wait for delayed action", route: "/", target_contract: { semantic_id: "target_delayed_action", allowed_roles: ["button"], allowed_names: ["Delayed action"], component_ref: "component:delayed-action", destructive: false }, components: [{ component_ref: "component:delayed-action", role: "button", name: "Delayed action", test_id: "delayed-action" }], interactions: [{ kind: "click", target: { test_id: "delayed-action" }, non_destructive: true }], wait_conditions: ["wait_after_entry_at_least_100ms"], validations: [{ id: "validation_delayed_visible", kind: "element_visible", target: { test_id: "delayed-action" }, required: true }], success_state: "Delayed action clicked" }; }
async function targetIsBlocked(worker, sessionID, stage) { const result = await worker.call("browser_agent_observe", { session_id: sessionID, stage }); return result.target_resolved === false && result.observation.assertions.some((item) => item.kind === "target_resolved" && item.passed === false); }
function artifactExists(artifact) { return artifact?.uri && existsSync(new URL(artifact.uri)) && artifact.sha256 && artifact.size_bytes > 0; }
function artifactView(artifact) { return { id: artifact.id, kind: artifact.kind, uri: artifact.uri, mime_type: artifact.mime_type, sha256: artifact.sha256, size_bytes: artifact.size_bytes }; }
function readOption(name) { const index = process.argv.indexOf(name); return index >= 0 ? process.argv[index + 1] : undefined; }
function startWorker() { const child = spawn(process.execPath, [path.join(workerRoot, "dist", "index.js")], { cwd: repoRoot, windowsHide: true }); const lines = readline.createInterface({ input: child.stdout, crlfDelay: Infinity }); const pending = []; let nextID = 0; let stderr = ""; child.stderr.on("data", (chunk) => { stderr += String(chunk); }); lines.on("line", (line) => { const request = pending.shift(); if (!request) return; try { const response = JSON.parse(line); response.error ? request.reject(new Error(response.error.message)) : request.resolve(response.result); } catch (error) { request.reject(error); } }); child.on("close", (code) => { while (pending.length) pending.shift().reject(new Error(`video-worker exited ${code}: ${stderr}`)); }); return { call(method, params) { nextID += 1; return new Promise((resolve, reject) => { pending.push({ resolve, reject }); child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id: nextID, method, params })}\n`); }); }, close() { return new Promise((resolve, reject) => { child.once("error", reject); child.once("close", (code) => code === 0 ? resolve() : reject(new Error(`video-worker exited ${code}: ${stderr}`))); child.stdin.end(); }); }, kill() { child.kill(); } }; }
function listen(target) { return new Promise((resolve, reject) => { target.once("error", reject); target.listen(0, "127.0.0.1", () => resolve(`http://127.0.0.1:${target.address().port}/`)); }); }
function closeServer(target) { return new Promise((resolve) => target.close(() => resolve())); }
