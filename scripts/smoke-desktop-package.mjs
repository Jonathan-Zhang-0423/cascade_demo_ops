import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync } from "node:fs";
import { basename, resolve } from "node:path";
import { spawn, spawnSync } from "node:child_process";

const root = resolve(".");
const appPackage = JSON.parse(readFileSync(resolve("package.json"), "utf8"));
const targetGOOS = process.platform === "win32" ? "windows" : "darwin";
const releaseBaseName = `CascadeDemoOps-${appPackage.version || "0.0.0"}-${targetGOOS}-${process.arch}`;
const releaseRoot = resolve("dist", "release");
const zipPath = resolve(releaseRoot, `${releaseBaseName}.zip`);
const checksumPath = `${zipPath}.sha256`;
const releaseManifestPath = resolve(releaseRoot, `${releaseBaseName}.manifest.json`);
const smokeRoot = resolve("dist", "package-smoke", releaseBaseName);

assertFile(zipPath, "release zip");
assertFile(checksumPath, "release checksum");
assertFile(releaseManifestPath, "release manifest");

const manifest = JSON.parse(readFileSync(releaseManifestPath, "utf8"));
assert(manifest.schema_version === "demoops.desktop_package_manifest.v1", "unexpected manifest schema");
assert(manifest.package_kind === "portable_zip", "desktop package must be portable_zip");
assert(manifest.runtimes?.node?.path === "resources/runtimes/node/node.exe", "desktop package manifest must include bundled node runtime");
assert(manifest.server_connectivity?.required_for_local_generation === false, "server connectivity must be optional for local generation");
assertServerRecordingBoundary(manifest.server_connectivity, "desktop package manifest");
assert(Array.isArray(manifest.server_connectivity?.reserved_interfaces), "server reserved interfaces are required");
for (const name of ["ExchangeCapabilityResolver", "ExchangeIdentityStore", "ExchangeSessionManager", "CloudLifecycleClient"]) {
  assert(manifest.server_connectivity.reserved_interfaces.includes(name), `missing reserved interface ${name}`);
}
assertRequiredAppSurfaces(manifest);

const expectedZipHash = readFileHashLine(checksumPath, basename(zipPath));
const actualZipHash = sha256File(zipPath);
assert(actualZipHash === expectedZipHash, `zip checksum mismatch: ${actualZipHash} !== ${expectedZipHash}`);
assert(manifest.artifact?.sha256 === actualZipHash, "release manifest artifact hash mismatch");

const entries = listZipEntries(zipPath);
for (const required of [
  "cascade-demoops-desktop.exe",
  "package-manifest.json",
  "resources/desktop-runtime.json",
  "resources/runtimes/node/node.exe",
  "resources/web/index.html",
  "resources/sidecars/video-worker/dist/index.js",
]) {
  assert(entries.has(required) || entries.has(required.replaceAll("/", "\\")), `zip is missing ${required}`);
}

rmSync(smokeRoot, { recursive: true, force: true });
mkdirSync(smokeRoot, { recursive: true });
expandZip(zipPath, smokeRoot);

const entrypoint = resolve(smokeRoot, manifest.entrypoint || "cascade-demoops-desktop.exe");
assertFile(entrypoint, "desktop entrypoint");
assertFile(resolve(smokeRoot, manifest.resource_manifest || "resources/desktop-runtime.json"), "desktop runtime manifest");
assertPackagedWebSurfaces(resolve(smokeRoot, "resources", "web"));
const bundledNode = resolve(smokeRoot, "resources", "runtimes", "node", "node.exe");
const bundledWorker = resolve(smokeRoot, "resources", "sidecars", "video-worker", "dist", "index.js");
assertFile(bundledNode, "bundled node runtime");
assertFile(bundledWorker, "packaged video-worker entrypoint");
await assertVideoWorkerHealth(bundledNode, bundledWorker);

const result = spawnSync(entrypoint, ["--check"], {
  cwd: smokeRoot,
  env: {
    ...process.env,
    CASCADE_PROFILE: "desktop",
    CASCADE_DATA_ROOT: resolve(smokeRoot, "user-data"),
  },
  encoding: "utf8",
});
if (result.status !== 0) {
  process.stdout.write(result.stdout || "");
  process.stderr.write(result.stderr || "");
  throw new Error(`desktop entrypoint exited with status ${result.status}`);
}
const payload = JSON.parse(result.stdout);
assert(payload.ready === true, "desktop entrypoint did not report ready=true");
assert(payload.profile === "desktop", "desktop entrypoint did not use desktop profile");
assert(payload.mode === "desktop", "desktop entrypoint did not use desktop mode");

const host = spawn(entrypoint, ["--open=false", "--addr", "127.0.0.1:0"], {
  cwd: smokeRoot,
  env: {
    ...process.env,
    CASCADE_PROFILE: "desktop",
    CASCADE_DATA_ROOT: resolve(smokeRoot, "host-user-data"),
  },
  stdio: ["ignore", "pipe", "pipe"],
});
try {
  const hostPayload = await waitForReadyPayload(host);
  assert(hostPayload.ready === true, "desktop host did not report ready=true");
  assert(typeof hostPayload.url === "string" && hostPayload.url.startsWith("http://127.0.0.1:"), "desktop host did not report a local URL");
  const index = httpGet(hostPayload.url);
  assert(index.includes("<!doctype html>") || index.includes("<div id=\"root\"></div>"), "desktop host did not serve packaged web index");
  const health = JSON.parse(httpGet(`${hostPayload.url}/v1/desktop/runtime-health`));
  assert(health.ok === true, "desktop host runtime-health failed");
  assert(health.data?.node_runtime_configured === true, "desktop host did not load bundled node runtime");
  assert(health.data?.sidecars?.["video-worker"] === true, "desktop host did not load packaged video-worker");
  assertRuntimeCapabilities(health.data, "desktop host runtime-health");
} finally {
  await terminateChild(host);
}

console.log(`Desktop package smoke passed: ${zipPath}`);

function assertFile(path, label) {
  assert(existsSync(path), `missing ${label}: ${path}`);
}

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

function assertServerRecordingBoundary(connectivity, label) {
  assert(connectivity?.server_recording_required === true, `${label} must require server-side production recording`);
  assert(connectivity?.local_recording_execution === false, `${label} must not advertise local production recording`);
  assert(connectivity.server_responsibilities?.includes("browser_execution"), `${label} must assign browser execution to server`);
  assert(connectivity.app_responsibilities?.includes("approved_package_upload"), `${label} must assign approved package upload to app`);
  assert(connectivity.app_responsibilities?.includes("result_video_download"), `${label} must assign result video download to app`);
}

function assertRuntimeCapabilities(data, label) {
  const capabilities = data?.app_capabilities;
  assert(capabilities?.demo_asset_generation_console === true, `${label} must expose demo console capability`);
  assert(capabilities?.video_editor === true, `${label} must expose video editor capability`);
  assert(capabilities?.local_package_generation === true, `${label} must allow local package generation`);
  assert(capabilities?.approved_package_upload === true, `${label} must allow approved package upload`);
  assert(capabilities?.result_video_download === true, `${label} must allow result video download`);
  assert(capabilities?.error_report_download === true, `${label} must allow error report download`);
  assert(capabilities?.server_recording_required === true, `${label} must require server-side production recording`);
  assert(capabilities?.local_recording_execution === false, `${label} must not enable local production recording`);
  assert(capabilities?.video_worker_role === "editor_media_helper_and_dev_compatibility_runtime", `${label} must classify video-worker as non-production-recorder`);
}

function assertRequiredAppSurfaces(manifest) {
  const surfaces = manifest.app_surfaces ?? [];
  assert(Array.isArray(surfaces), "desktop manifest must declare app surfaces");
  const byID = new Map(surfaces.map((surface) => [surface.id, surface]));
  const demoConsole = byID.get("demo_asset_generation_console");
  assert(demoConsole?.required === true, "desktop manifest must require demo asset generation console");
  for (const capability of ["approved_package_upload", "result_video_download", "error_report_download"]) {
    assert(demoConsole.capabilities?.includes(capability), `desktop manifest demo console is missing capability ${capability}`);
  }
  assert(byID.get("video_editor")?.required === true, "desktop manifest must require video editor");
}

function assertPackagedWebSurfaces(webRoot) {
  assertFile(resolve(webRoot, "index.html"), "packaged web index");
  const bundleText = readPackagedText(webRoot);
  assertSurfaceText(bundleText, "demo asset generation console", [
    "开始实战流程",
    "输入材料配置",
    "演示账号",
    "执行包审批",
    "Stage JSON",
    "Browser Agent 大纲",
  ]);
  assertSurfaceText(bundleText, "video editor", [
    "视频编辑",
    "导入素材",
    "生成预览",
    "导出 MP4",
    "时间线",
    "字幕",
  ]);
}

function readPackagedText(webRoot) {
  const files = listFiles(webRoot).filter((file) => /\.(html|js|css)$/i.test(file));
  assert(files.length > 0, `packaged web surface files are missing: ${webRoot}`);
  return files.map((file) => readFileSync(file, "utf8")).join("\n");
}

function assertSurfaceText(text, label, requiredTexts) {
  for (const required of requiredTexts) {
    assert(text.includes(required), `packaged web is missing ${label} text: ${required}`);
  }
}

function sha256File(file) {
  return createHash("sha256").update(readFileSync(file)).digest("hex");
}

function readFileHashLine(file, expectedName) {
  const line = readFileSync(file, "utf8").trim();
  const [hash, ...rest] = line.split(/\s+/);
  assert(hash && /^[a-f0-9]{64}$/i.test(hash), `invalid checksum file: ${file}`);
  assert(rest.join(" ").includes(expectedName), `checksum file does not reference ${expectedName}`);
  return hash.toLowerCase();
}

function listZipEntries(file) {
  if (process.platform === "win32") {
    const command = [
      "Add-Type -AssemblyName System.IO.Compression.FileSystem;",
      `[IO.Compression.ZipFile]::OpenRead('${escapePowerShell(file)}').Entries | ForEach-Object { $_.FullName }`,
    ].join(" ");
    const result = spawnSync("powershell", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command], {
      cwd: root,
      encoding: "utf8",
    });
    if (result.status !== 0) {
      process.stderr.write(result.stderr || "");
      throw new Error("failed to inspect release zip");
    }
    return new Set(result.stdout.split(/\r?\n/).map((line) => line.trim()).filter(Boolean));
  }
  const result = spawnSync("zipinfo", ["-1", file], { cwd: root, encoding: "utf8" });
  if (result.status !== 0) {
    process.stderr.write(result.stderr || "");
    throw new Error("failed to inspect release zip");
  }
  return new Set(result.stdout.split(/\r?\n/).map((line) => line.trim()).filter(Boolean));
}

function listFiles(dir) {
  const result = [];
  const stack = [dir];
  while (stack.length > 0) {
    const current = stack.pop();
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const path = resolve(current, entry.name);
      if (entry.isDirectory()) {
        stack.push(path);
      } else if (entry.isFile()) {
        result.push(path);
      }
    }
  }
  return result;
}

function expandZip(file, destination) {
  if (process.platform === "win32") {
    const result = spawnSync("powershell", [
      "-NoProfile",
      "-ExecutionPolicy",
      "Bypass",
      "-Command",
      `Expand-Archive -LiteralPath '${escapePowerShell(file)}' -DestinationPath '${escapePowerShell(destination)}' -Force`,
    ], {
      cwd: root,
      stdio: "inherit",
    });
    if (result.status !== 0) {
      throw new Error(`failed to expand release zip: ${file}`);
    }
    return;
  }
  const result = spawnSync("unzip", ["-q", file, "-d", destination], {
    cwd: root,
    stdio: "inherit",
  });
  if (result.status !== 0) {
    throw new Error(`failed to expand release zip: ${file}`);
  }
}

function escapePowerShell(value) {
  return value.replaceAll("'", "''");
}

function waitForReadyPayload(child) {
  return new Promise((resolveReady, rejectReady) => {
    let stdout = "";
    let stderr = "";
    const timer = setTimeout(() => {
      rejectReady(new Error(`desktop host did not print ready payload before timeout. stderr=${stderr}`));
    }, 10000);
    child.stdout?.on("data", (chunk) => {
      stdout += chunk.toString("utf8");
      const parsed = tryParseFirstJSON(stdout);
      if (parsed) {
        clearTimeout(timer);
        resolveReady(parsed);
      }
    });
    child.stderr?.on("data", (chunk) => {
      stderr += chunk.toString("utf8");
    });
    child.on("error", (err) => {
      clearTimeout(timer);
      rejectReady(err);
    });
    child.on("exit", (code) => {
      const parsed = tryParseFirstJSON(stdout);
      if (parsed) {
        clearTimeout(timer);
        resolveReady(parsed);
        return;
      }
      clearTimeout(timer);
      rejectReady(new Error(`desktop host exited before ready payload, code=${code}, stderr=${stderr}`));
    });
  });
}

function tryParseFirstJSON(value) {
  const start = value.indexOf("{");
  const end = value.lastIndexOf("}");
  if (start < 0 || end <= start) {
    return null;
  }
  try {
    return JSON.parse(value.slice(start, end + 1));
  } catch {
    return null;
  }
}

async function terminateChild(child) {
  if (child.exitCode !== null) {
    return;
  }
  child.kill();
  const exited = await waitForExit(child, 3000);
  if (!exited && process.platform === "win32" && child.pid) {
    spawnSync("taskkill", ["/PID", String(child.pid), "/T", "/F"], { stdio: "ignore" });
    await waitForExit(child, 3000);
  }
}

function waitForExit(child, timeoutMS) {
  return new Promise((resolveExit) => {
    if (child.exitCode !== null) {
      resolveExit(true);
      return;
    }
    const timer = setTimeout(() => resolveExit(false), timeoutMS);
    child.on("exit", () => {
      clearTimeout(timer);
      resolveExit(true);
    });
  });
}

function httpGet(url) {
  const ps = [
    "$ProgressPreference = 'SilentlyContinue';",
    `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8;`,
    `(Invoke-WebRequest -UseBasicParsing -TimeoutSec 5 -Uri '${escapePowerShell(url)}').Content`,
  ].join(" ");
  const result = spawnSync("powershell", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", ps], {
    cwd: root,
    encoding: "utf8",
  });
  if (result.status !== 0) {
    process.stderr.write(result.stderr || "");
    throw new Error(`HTTP GET failed: ${url}`);
  }
  return result.stdout;
}

function assertVideoWorkerHealth(nodePath, workerPath) {
  return new Promise((resolveHealth, rejectHealth) => {
    const child = spawn(nodePath, [workerPath], {
      cwd: resolve(workerPath, ".."),
      stdio: ["pipe", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    const timer = setTimeout(() => {
      child.kill();
      rejectHealth(new Error(`video-worker health timed out. stderr=${stderr}`));
    }, 5000);
    child.stdout?.on("data", (chunk) => {
      stdout += chunk.toString("utf8");
      const line = stdout.split(/\r?\n/).find((item) => item.trim().startsWith("{"));
      if (!line) {
        return;
      }
      try {
        const response = JSON.parse(line);
        if (response.result?.ok === true && response.result?.service === "video-worker") {
          clearTimeout(timer);
          child.kill();
          resolveHealth();
          return;
        }
        clearTimeout(timer);
        child.kill();
        rejectHealth(new Error(`unexpected video-worker health response: ${line}`));
      } catch {
        // Wait for a complete JSON line.
      }
    });
    child.stderr?.on("data", (chunk) => {
      stderr += chunk.toString("utf8");
    });
    child.on("error", (err) => {
      clearTimeout(timer);
      rejectHealth(err);
    });
    child.on("exit", (code) => {
      const response = tryParseFirstJSON(stdout);
      if (response?.result?.ok === true && response.result?.service === "video-worker") {
        clearTimeout(timer);
        resolveHealth();
        return;
      }
      clearTimeout(timer);
      rejectHealth(new Error(`video-worker exited before healthy response, code=${code}, stderr=${stderr}`));
    });
    child.stdin?.end(`${JSON.stringify({ jsonrpc: "2.0", id: 1, method: "health" })}\n`);
  });
}
