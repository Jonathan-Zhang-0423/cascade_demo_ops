import { existsSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { basename, resolve } from "node:path";
import { spawn, spawnSync } from "node:child_process";
import playwright from "../video-worker/node_modules/playwright/index.js";

const { chromium } = playwright;

const root = resolve(".");
const appPackage = JSON.parse(readFileSync(resolve("package.json"), "utf8"));
const targetGOOS = process.platform === "win32" ? "windows" : "darwin";
const releaseBaseName = `CascadeDemoOps-${appPackage.version || "0.0.0"}-${targetGOOS}-${process.arch}`;
const releaseRoot = resolve("dist", "release");
const zipPath = resolve(releaseRoot, `${releaseBaseName}.zip`);
const manifestPath = resolve(releaseRoot, `${releaseBaseName}.manifest.json`);
const smokeRoot = resolve("dist", "desktop-ui-smoke", releaseBaseName);

assertFile(zipPath, "release zip");
assertFile(manifestPath, "release manifest");

const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
assertRequiredAppSurfaces(manifest);

rmSync(smokeRoot, { recursive: true, force: true });
mkdirSync(smokeRoot, { recursive: true });
expandZip(zipPath, smokeRoot);

const entrypoint = resolve(smokeRoot, manifest.entrypoint || "cascade-demoops-desktop.exe");
assertFile(entrypoint, "desktop entrypoint");
const host = spawn(entrypoint, ["--open=false", "--addr", "127.0.0.1:0"], {
  cwd: smokeRoot,
  env: {
    ...process.env,
    CASCADE_PROFILE: "desktop",
    CASCADE_DATA_ROOT: resolve(smokeRoot, "user-data"),
  },
  stdio: ["ignore", "pipe", "pipe"],
});

let browser;
try {
  const hostPayload = await waitForReadyPayload(host);
  assert(hostPayload.ready === true, "desktop host did not report ready=true");
  browser = await launchBrowser();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  await page.goto(hostPayload.url, { waitUntil: "networkidle" });
  await expectVisibleText(page, "开始实战流程");
  await expectVisibleText(page, "基础设置");
  await clickVisibleButton(page, "输入材料");
  await expectVisibleText(page, "输入材料配置");
  await expectVisibleText(page, "项目根目录");
  await expectVisibleText(page, "本次演示需求");
  await clickVisibleButton(page, "方案审批");
  await expectVisibleText(page, "执行方案");
  await clickVisibleButton(page, "执行包审批");
  await expectVisibleText(page, "执行包审批入口");
  await expectVisibleText(page, "视频编辑");

  await clickVisibleButton(page, /^执行包$/);
  await expectVisibleText(page, "执行包审批");
  await expectVisibleText(page, "Runtime");
  await expectVisibleText(page, "上传模式");
  await expectVisibleText(page, "服务器执行生命周期");

  await clickVisibleButton(page, "视频编辑");
  await page.getByRole("combobox", { name: "编辑项目" }).waitFor({ state: "visible", timeout: 10000 });
  await expectVisibleText(page, "新建或导入");
  await clickVisibleButton(page, "新建或导入");
  await expectVisibleText(page, "导入素材或结果包");
  await expectVisibleText(page, "结果包");
  await expectVisibleText(page, "空项目");
  await expectVisibleText(page, "原始素材保持只读");

  console.log(`Desktop UI smoke passed: ${hostPayload.url}`);
} finally {
  if (browser) {
    await browser.close();
  }
  await terminateChild(host);
}

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
  assert(Array.isArray(surfaces), "desktop manifest must declare app surfaces");
  const byID = new Map(surfaces.map((surface) => [surface.id, surface]));
  assert(byID.get("demo_asset_generation_console")?.required === true, "desktop manifest must require demo asset generation console");
  assert(byID.get("video_editor")?.required === true, "desktop manifest must require video editor");
}

async function launchBrowser() {
  const executablePath = process.env.CASCADE_DESKTOP_UI_SMOKE_BROWSER || findSystemBrowser();
  if (!executablePath) {
    throw new Error("desktop UI smoke needs Chrome or Edge. Set CASCADE_DESKTOP_UI_SMOKE_BROWSER to a browser executable.");
  }
  return chromium.launch({ headless: true, executablePath });
}

function findSystemBrowser() {
  if (process.platform !== "win32") {
    return "";
  }
  const candidates = [
    "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
    "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
    "C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe",
    "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
  ];
  return candidates.find((candidate) => existsSync(candidate)) || "";
}

async function expectVisibleText(page, text) {
  await page.getByText(text, { exact: false }).first().waitFor({ state: "visible", timeout: 10000 });
}

async function clickVisibleButton(page, name) {
  const button = page.getByRole("button", { name }).first();
  await button.waitFor({ state: "visible", timeout: 10000 });
  await button.evaluate((element) => element.click());
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

function escapePowerShell(value) {
  return value.replaceAll("'", "''");
}
