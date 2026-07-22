import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { basename, resolve } from "node:path";
import { spawn, spawnSync } from "node:child_process";

const root = resolve(".");
const appPackage = JSON.parse(readFileSync(resolve("package.json"), "utf8"));
const targetGOOS = process.platform === "win32" ? "windows" : "darwin";
if (targetGOOS !== "windows") {
  console.error("Desktop installer smoke currently supports Windows only.");
  process.exit(1);
}

const releaseBaseName = `CascadeDemoOps-${appPackage.version || "0.0.0"}-${targetGOOS}-${process.arch}`;
const releaseRoot = resolve("dist", "release");
const setupPath = resolve(releaseRoot, `${releaseBaseName}-installer.exe`);
const setupSidecarManifestPath = `${setupPath}.manifest`;
const checksumPath = `${setupPath}.sha256`;
const manifestPath = resolve(releaseRoot, `${releaseBaseName}-installer.manifest.json`);
const releaseChannelManifestPath = resolve(releaseRoot, "CascadeDemoOps-desktop-latest.json");
const portableZipPath = resolve(releaseRoot, `${releaseBaseName}.zip`);
const smokeRoot = resolve("dist", "installer-smoke", releaseBaseName);
const installDir = resolve(smokeRoot, "CascadeDemoOps");
const appDataRoot = resolve(smokeRoot, "appdata");

assertFile(setupPath, "desktop setup exe");
assertFile(setupSidecarManifestPath, "desktop setup Windows manifest");
assertFile(checksumPath, "desktop setup checksum");
assertFile(manifestPath, "desktop setup manifest");
assertFile(releaseChannelManifestPath, "desktop release channel manifest");

const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
assert(manifest.schema_version === "demoops.desktop_installer_manifest.v1", "unexpected installer manifest schema");
assert(manifest.package_kind === "self_extracting_setup_exe", "installer must be self_extracting_setup_exe");
assert(manifest.windows_manifest?.requested_execution_level === "asInvoker", "installer must declare asInvoker execution level");
assert(manifest.server_connectivity?.required_for_local_generation === false, "server connectivity must be optional for local generation");
assert(manifest.install_behavior?.supports_silent_install === true, "installer must support silent install");
assertRequiredAppSurfaces(manifest);

const expectedHash = readFileHashLine(checksumPath, basename(setupPath));
const actualHash = sha256File(setupPath);
assert(actualHash === expectedHash, `setup checksum mismatch: ${actualHash} !== ${expectedHash}`);
assert(manifest.artifact?.sha256 === actualHash, "installer manifest artifact hash mismatch");
const releaseChannel = JSON.parse(readFileSync(releaseChannelManifestPath, "utf8"));
assertReleaseChannelManifest(releaseChannel, {
  installerName: basename(setupPath),
  installerHash: actualHash,
  installerSize: manifest.artifact?.size_bytes,
  installerManifestName: basename(manifestPath),
  portableName: basename(portableZipPath),
});

rmSync(smokeRoot, { recursive: true, force: true });
mkdirSync(appDataRoot, { recursive: true });

const check = spawnSync(setupPath, ["--check"], {
  cwd: root,
  env: { ...process.env, APPDATA: appDataRoot, LOCALAPPDATA: resolve(smokeRoot, "localappdata") },
  encoding: "utf8",
});
if (check.status !== 0) {
  process.stdout.write(check.stdout || "");
  process.stderr.write(check.stderr || "");
  throw new Error(`setup --check exited with status ${check.status}`);
}
const checkPayload = JSON.parse(check.stdout);
assert(checkPayload.ready === true, "setup --check did not report ready=true");
assert(checkPayload.payload_embedded === true, "setup payload is not embedded");
assert(checkPayload.payload_sha256 === manifest.payload?.sha256, "setup payload hash does not match manifest");

const install = spawnSync(setupPath, ["--quiet", "--launch=false", "--install-dir", installDir], {
  cwd: root,
  env: { ...process.env, APPDATA: appDataRoot, LOCALAPPDATA: resolve(smokeRoot, "localappdata") },
  encoding: "utf8",
});
if (install.status !== 0) {
  process.stdout.write(install.stdout || "");
  process.stderr.write(install.stderr || "");
  throw new Error(`setup install exited with status ${install.status}`);
}
const installPayload = JSON.parse(install.stdout);
assert(installPayload.installed === true, "setup did not report installed=true");
assertFile(resolve(installDir, "cascade-demoops-desktop.exe"), "installed desktop entrypoint");
assertFile(resolve(installDir, "resources", "web", "index.html"), "installed web index");
assertPackagedWebSurfaces(resolve(installDir, "resources", "web"));
assertFile(resolve(installDir, "resources", "desktop-runtime.json"), "installed runtime manifest");
const bundledNode = resolve(installDir, "resources", "runtimes", "node", "node.exe");
const bundledWorker = resolve(installDir, "resources", "sidecars", "video-worker", "dist", "index.js");
assertFile(bundledNode, "installed bundled node runtime");
assertFile(bundledWorker, "installed video-worker entrypoint");
await assertVideoWorkerHealth(bundledNode, bundledWorker);
assertFile(resolve(installDir, "install-manifest.json"), "install manifest");
const uninstallScript = resolve(installDir, "Uninstall-CascadeDemoOps.ps1");
assertFile(uninstallScript, "uninstall script");

const installedManifest = JSON.parse(readFileSync(resolve(installDir, "install-manifest.json"), "utf8"));
assert(installedManifest.schema_version === "demoops.desktop_install_manifest.v1", "unexpected installed manifest schema");
assert(installedManifest.server_connectivity?.required_for_local_generation === false, "installed app must keep local generation server-optional");
assertRequiredAppSurfaces(installedManifest);
assert(Array.isArray(installedManifest.shortcuts) && installedManifest.shortcuts.length === 1, "installer did not record Start Menu launcher");
const launcherPath = installedManifest.shortcuts[0];
assertFile(launcherPath, "Start Menu launcher");
assert(readFileSync(launcherPath, "utf8").includes("cascade-demoops-desktop.exe"), "Start Menu launcher does not point at desktop entrypoint");

const entrypoint = resolve(installDir, "cascade-demoops-desktop.exe");
const desktopCheck = spawnSync(entrypoint, ["--check"], {
  cwd: installDir,
  env: {
    ...process.env,
    CASCADE_PROFILE: "desktop",
    CASCADE_DATA_ROOT: resolve(smokeRoot, "user-data"),
  },
  encoding: "utf8",
});
if (desktopCheck.status !== 0) {
  process.stdout.write(desktopCheck.stdout || "");
  process.stderr.write(desktopCheck.stderr || "");
  throw new Error(`installed desktop --check exited with status ${desktopCheck.status}`);
}
const desktopPayload = JSON.parse(desktopCheck.stdout);
assert(desktopPayload.ready === true, "installed desktop did not report ready=true");
assert(desktopPayload.profile === "desktop", "installed desktop did not use desktop profile");

const host = spawn(entrypoint, ["--open=false", "--addr", "127.0.0.1:0"], {
  cwd: installDir,
  env: {
    ...process.env,
    CASCADE_PROFILE: "desktop",
    CASCADE_DATA_ROOT: resolve(smokeRoot, "host-user-data"),
  },
  stdio: ["ignore", "pipe", "pipe"],
});
try {
  const hostPayload = await waitForReadyPayload(host);
  assert(hostPayload.ready === true, "installed desktop host did not report ready=true");
  const index = httpGet(hostPayload.url);
  assert(index.includes("<!doctype html>") || index.includes("<div id=\"root\"></div>"), "installed desktop host did not serve web index");
  const health = JSON.parse(httpGet(`${hostPayload.url}/v1/desktop/runtime-health`));
  assert(health.ok === true, "installed desktop runtime-health failed");
  assert(health.data?.node_runtime_configured === true, "installed desktop did not load bundled node runtime");
  assert(health.data?.sidecars?.["video-worker"] === true, "installed desktop did not load packaged video-worker");
} finally {
  await terminateChild(host);
}

const userDataSentinel = resolve(smokeRoot, "user-data", "preserve-me.txt");
mkdirSync(resolve(userDataSentinel, ".."), { recursive: true });
writeFileSync(userDataSentinel, "user data must survive uninstall\n");
const uninstall = spawnSync("powershell", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", uninstallScript], {
  cwd: root,
  encoding: "utf8",
});
if (uninstall.status !== 0) {
  process.stdout.write(uninstall.stdout || "");
  process.stderr.write(uninstall.stderr || "");
  throw new Error(`uninstall script exited with status ${uninstall.status}`);
}
assert(!existsSync(installDir), "uninstall script did not remove install dir");
assert(!existsSync(launcherPath), "uninstall script did not remove Start Menu launcher");
assertFile(userDataSentinel, "user data sentinel after uninstall");

console.log(`Desktop installer smoke passed: ${setupPath}`);

function assertFile(path, label) {
  assert(existsSync(path), `missing ${label}: ${path}`);
}

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

function assertRequiredAppSurfaces(manifest) {
  const surfaces = manifest.app_surfaces ?? [];
  assert(Array.isArray(surfaces), "installer manifest must declare app surfaces");
  const byID = new Map(surfaces.map((surface) => [surface.id, surface]));
  assert(byID.get("demo_asset_generation_console")?.required === true, "installer manifest must require demo asset generation console");
  assert(byID.get("video_editor")?.required === true, "installer manifest must require video editor");
}

function assertReleaseChannelManifest(channel, expected) {
  assert(channel.schema_version === "demoops.desktop_release_channel.v1", "unexpected release channel manifest schema");
  assert(channel.channel === "latest", "release channel must be latest");
  assert(channel.recommended_artifact === "installer", "release channel must recommend installer");
  assert(channel.version === appPackage.version, "release channel version mismatch");
  assert(channel.target_os === targetGOOS, "release channel target_os mismatch");
  assert(channel.target_arch === process.arch, "release channel target_arch mismatch");
  assert(channel.artifacts?.installer?.file_name === expected.installerName, "release channel installer file mismatch");
  assert(channel.artifacts?.installer?.manifest_file_name === expected.installerManifestName, "release channel installer manifest mismatch");
  assert(channel.artifacts?.installer?.sha256 === expected.installerHash, "release channel installer hash mismatch");
  assert(channel.artifacts?.installer?.size_bytes === expected.installerSize, "release channel installer size mismatch");
  assert(channel.artifacts?.portable_zip?.file_name === expected.portableName, "release channel portable zip file mismatch");
  assert(channel.server_connectivity?.required_for_local_generation === false, "release channel must keep local generation server-optional");
  assertRequiredAppSurfaces(channel);
}

function assertPackagedWebSurfaces(webRoot) {
  assertFile(resolve(webRoot, "index.html"), "installed packaged web index");
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
  assert(files.length > 0, `installed web surface files are missing: ${webRoot}`);
  return files.map((file) => readFileSync(file, "utf8")).join("\n");
}

function assertSurfaceText(text, label, requiredTexts) {
  for (const required of requiredTexts) {
    assert(text.includes(required), `installed web is missing ${label} text: ${required}`);
  }
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

function escapePowerShell(value) {
  return value.replaceAll("'", "''");
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
