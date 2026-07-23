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
const localAppDataRoot = resolve(smokeRoot, "localappdata");
const installerLogPath = resolve(localAppDataRoot, "CascadeDemoOps", "logs", "CascadeDemoOpsInstaller.log");

assertFile(setupPath, "desktop setup exe");
assertWindowsGuiSubsystem(setupPath, "desktop setup exe");
assertFile(setupSidecarManifestPath, "desktop setup Windows manifest");
assertFile(checksumPath, "desktop setup checksum");
assertFile(manifestPath, "desktop setup manifest");
assertFile(releaseChannelManifestPath, "desktop release channel manifest");

const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
assert(manifest.schema_version === "demoops.desktop_installer_manifest.v1", "unexpected installer manifest schema");
assert(manifest.package_kind === "self_extracting_setup_exe", "installer must be self_extracting_setup_exe");
assert(manifest.desktop_ui?.primary === "native_win32", "installer must declare native Win32 primary UI");
assert(manifest.desktop_ui?.uses_browser_shell === false, "installer must not declare a browser shell as primary UI");
assertNativeCapabilities(manifest.desktop_ui, "installer manifest");
assert(manifest.windows_manifest?.requested_execution_level === "asInvoker", "installer must declare asInvoker execution level");
assert(manifest.server_connectivity?.required_for_local_generation === false, "server connectivity must be optional for local generation");
assertServerRecordingBoundary(manifest.server_connectivity, "installer manifest");
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
  env: { ...process.env, APPDATA: appDataRoot, LOCALAPPDATA: localAppDataRoot },
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
  env: { ...process.env, APPDATA: appDataRoot, LOCALAPPDATA: localAppDataRoot },
  encoding: "utf8",
});
if (install.status !== 0) {
  process.stdout.write(install.stdout || "");
  process.stderr.write(install.stderr || "");
  throw new Error(`setup install exited with status ${install.status}`);
}
const installPayload = JSON.parse(install.stdout);
assert(installPayload.installed === true, "setup did not report installed=true");
assertFile(installerLogPath, "installer diagnostic log");
assertFile(resolve(installDir, "cascade-demoops-desktop.exe"), "installed desktop entrypoint");
assertWindowsGuiSubsystem(resolve(installDir, "cascade-demoops-desktop.exe"), "installed desktop entrypoint");
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
assertServerRecordingBoundary(installedManifest.server_connectivity, "installed manifest");
assertRequiredAppSurfaces(installedManifest);
assert(Array.isArray(installedManifest.shortcuts) && installedManifest.shortcuts.length === 1, "installer did not record Start Menu launcher");
const launcherPath = installedManifest.shortcuts[0];
assertFile(launcherPath, "Start Menu launcher");
assert(launcherPath.endsWith(".lnk"), "Start Menu launcher must be a Windows shortcut");

const entrypoint = resolve(installDir, "cascade-demoops-desktop.exe");
const desktopCheck = spawnSync(entrypoint, ["--check"], {
  cwd: installDir,
  env: {
    ...process.env,
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
assert(desktopPayload.ui === "native", "installed desktop must default to native UI");

const native = spawn(entrypoint, ["--addr", "127.0.0.1:0"], {
  cwd: installDir,
  env: {
    ...process.env,
    CASCADE_DATA_ROOT: resolve(smokeRoot, "native-user-data"),
  },
  stdio: ["ignore", "pipe", "pipe"],
});
try {
  await assertNativeWindowLaunch(native, resolve(smokeRoot, "native-user-data", "logs", "desktop-launcher.log"));
} finally {
  await terminateChild(native);
}

const host = spawn(entrypoint, ["--native=false", "--open=false", "--addr", "127.0.0.1:0"], {
  cwd: installDir,
  env: {
    ...process.env,
    CASCADE_DATA_ROOT: resolve(smokeRoot, "host-user-data"),
    CASCADE_DEV_EXCHANGE_HTTP: "1",
    CASCADE_DEV_EXCHANGE_TOKEN: "desktop-smoke-token",
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
  assertRuntimeCapabilities(health.data, "installed desktop runtime-health");
  assertDevExchangeRunUnavailable(hostPayload.url);
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

function assertWindowsGuiSubsystem(exePath, label) {
  if (process.platform !== "win32") {
    return;
  }
  const data = readFileSync(exePath);
  assert(data.readUInt16LE(0) === 0x5a4d, `${label} is not a PE executable`);
  const peOffset = data.readUInt32LE(0x3c);
  const optionalHeaderOffset = peOffset + 24;
  const subsystemOffset = optionalHeaderOffset + 68;
  const subsystem = data.readUInt16LE(subsystemOffset);
  assert(subsystem === 2, `${label} must use Windows GUI subsystem, got ${subsystem}`);
}

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

function assertNativeCapabilities(desktopUI, label) {
  const capabilities = desktopUI?.native_capabilities || [];
  for (const capability of ["native_menu_bar", "native_folder_picker", "requirement_document_import", "native_lifecycle_phase_bar", "approval_health_summary", "artifact_folder_open", "diagnostic_log_open", "native_visual_hierarchy", "three_in_one_package_save"]) {
    assert(capabilities.includes(capability), `${label} missing native capability ${capability}`);
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

function assertDevExchangeRunUnavailable(baseURL) {
  const status = httpStatus(`${baseURL}/v1/dev/execution-packages/xpkg_smoke/run?org_id=org_smoke`, {
    method: "POST",
    authorization: "Bearer desktop-smoke-token",
  });
  assert(status === 404, `installed desktop host must not expose local dev recording run route, got HTTP ${status}`);
}

function assertRequiredAppSurfaces(manifest) {
  const surfaces = manifest.app_surfaces ?? [];
  assert(Array.isArray(surfaces), "installer manifest must declare app surfaces");
  const byID = new Map(surfaces.map((surface) => [surface.id, surface]));
  const demoConsole = byID.get("demo_asset_generation_console");
  assert(demoConsole?.required === true, "installer manifest must require demo asset generation console");
  for (const capability of ["approved_package_upload", "result_video_download", "error_report_download"]) {
    assert(demoConsole.capabilities?.includes(capability), `installer manifest demo console is missing capability ${capability}`);
  }
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
  assert(channel.desktop_ui?.primary === "native_win32", "release channel must declare native Win32 primary UI");
  assert(channel.desktop_ui?.uses_browser_shell === false, "release channel must not declare a browser shell as primary UI");
  assertNativeCapabilities(channel.desktop_ui, "release channel");
  assert(channel.server_connectivity?.required_for_local_generation === false, "release channel must keep local generation server-optional");
  assertServerRecordingBoundary(channel.server_connectivity, "release channel");
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

async function assertNativeWindowLaunch(child, logPath) {
  let stdout = "";
  let stderr = "";
  let launchError = null;
  child.stdout?.on("data", (chunk) => {
    stdout += chunk.toString("utf8");
  });
  child.stderr?.on("data", (chunk) => {
    stderr += chunk.toString("utf8");
  });
  child.on("error", (err) => {
    launchError = err;
  });

  const deadline = Date.now() + 10000;
  let lastLog = "";
  while (Date.now() < deadline) {
    if (launchError) {
      throw launchError;
    }
    if (stdout.includes("Desktop host is serving packaged web assets") || stdout.includes("\"url\":\"http://")) {
      throw new Error(`native launch unexpectedly started compatibility web host: ${stdout}`);
    }
    if (child.exitCode !== null) {
      throw new Error(`native desktop exited before smoke window interval. stdout=${stdout} stderr=${stderr}`);
    }
    if (existsSync(logPath)) {
      lastLog = readFileSync(logPath, "utf8");
      if (lastLog.includes("starting native desktop ui") && lastLog.includes("本地原生应用已启动")) {
        return;
      }
    }
    await delay(250);
  }
  throw new Error(`native desktop log did not prove window initialization before timeout. log=${lastLog} stdout=${stdout} stderr=${stderr}`);
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

function delay(timeoutMS) {
  return new Promise((resolveDelay) => setTimeout(resolveDelay, timeoutMS));
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

function httpStatus(url, options = {}) {
  const method = options.method || "GET";
  const headers = options.authorization ? `-Headers @{Authorization='${escapePowerShell(options.authorization)}'}` : "";
  const ps = [
    "$ProgressPreference = 'SilentlyContinue';",
    `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8;`,
    "$ErrorActionPreference = 'Stop';",
    "try {",
    `$response = Invoke-WebRequest -UseBasicParsing -TimeoutSec 5 -Method ${method} -Uri '${escapePowerShell(url)}' ${headers};`,
    "[int]$response.StatusCode",
    "} catch {",
    "if ($_.Exception.Response -and $_.Exception.Response.StatusCode) { [int]$_.Exception.Response.StatusCode } else { throw }",
    "}",
  ].join(" ");
  const result = spawnSync("powershell", ["-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", ps], {
    cwd: root,
    encoding: "utf8",
  });
  if (result.status !== 0) {
    process.stderr.write(result.stderr || "");
    throw new Error(`HTTP status check failed: ${url}`);
  }
  return Number.parseInt(result.stdout.trim(), 10);
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
