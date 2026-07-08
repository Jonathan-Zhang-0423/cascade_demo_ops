import { mkdirSync, cpSync, existsSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

const root = resolve(".");
const resourceRoot = resolve("dist", "package", "resources");
const videoWorkerDist = resolve("video-worker", "dist");
const webDist = resolve("frontend", "web", "dist");

if (!fallbackWebBuild()) {
  runWithFallback("pnpm", ["--filter", "@cascade/web", "build"], fallbackWebBuild);
}
if (!fallbackWorkerBuild()) {
  runWithFallback("pnpm", ["--filter", "@cascade/video-worker", "build"], fallbackWorkerBuild);
}
run("node", ["scripts/build-desktop.mjs", process.platform === "win32" ? "windows" : "darwin"]);

mkdirSync(resourceRoot, { recursive: true });
copyIfExists(videoWorkerDist, resolve(resourceRoot, "sidecars", "video-worker", "dist"));
copyIfExists(webDist, resolve(resourceRoot, "web"));
writeFileSync(
  resolve(resourceRoot, "desktop-runtime.json"),
  JSON.stringify(
    {
      app: "Cascade DemoOps",
      resource_contract_version: 1,
      sidecars: {
        "video-worker": "sidecars/video-worker/dist/index.js",
      },
      web: "web",
    },
    null,
    2,
  ),
);

console.log(`Prepared desktop package resources under ${resourceRoot}`);

function run(command, args) {
  const result = spawn(command, args);
  if (!result.ok) {
    process.exit(result.status);
  }
}

function runWithFallback(command, args, fallback) {
  const result = spawn(command, args);
  if (result.ok) {
    return;
  }
  if (fallback && fallback(result)) {
    return;
  }
  process.exit(result.status);
}

function spawn(command, args) {
  const executable = process.platform === "win32" && command === "pnpm" ? "cmd.exe" : command;
  const finalArgs = process.platform === "win32" && command === "pnpm" ? ["/d", "/s", "/c", command, ...args] : args;
  const result = spawnSync(executable, finalArgs, {
    cwd: root,
    stdio: "inherit",
  });
  if (result.error) {
    console.error(`Failed to start ${command}: ${result.error.message}`);
    return { ok: false, status: 1, error: result.error };
  }
  return { ok: result.status === 0, status: result.status ?? 1 };
}

function fallbackWebBuild() {
  const pkgPath = resolve("frontend", "web", "package.json");
  if (!existsSync(pkgPath)) {
    return false;
  }
  const pkg = JSON.parse(readFileSync(pkgPath, "utf8"));
  if (pkg.scripts?.build !== "echo web-ui-not-implemented-yet") {
    return false;
  }
  console.warn("Skipping frontend/web build: build script is still a placeholder.");
  return true;
}

function fallbackWorkerBuild() {
  const tsc = process.platform === "win32" ? resolve("node_modules", ".bin", "tsc.cmd") : resolve("node_modules", ".bin", "tsc");
  if (!existsSync(tsc)) {
    return false;
  }
  console.warn("Building video-worker with local TypeScript compiler.");
  const executable = process.platform === "win32" ? "cmd.exe" : tsc;
  const args = process.platform === "win32" ? ["/d", "/s", "/c", tsc, "-p", resolve("video-worker", "tsconfig.json")] : ["-p", resolve("video-worker", "tsconfig.json")];
  const result = spawnSync(executable, args, {
    cwd: root,
    stdio: "inherit",
  });
  return result.status === 0;
}

function copyIfExists(from, to) {
  if (!existsSync(from)) {
    console.warn(`Skipping missing package resource: ${from}`);
    return;
  }
  mkdirSync(to, { recursive: true });
  cpSync(from, to, { recursive: true });
}
