import { cpSync, existsSync, mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

const root = resolve(".");
if (process.env.CASCADE_SKIP_WEB_BUILD !== "1") {
  const command = process.platform === "win32" ? "cmd.exe" : "pnpm";
  const args = process.platform === "win32"
    ? ["/d", "/s", "/c", "pnpm", "--filter", "@cascade/web", "build"]
    : ["--filter", "@cascade/web", "build"];
  run(command, args, root, { VITE_CASCADE_BRIDGE: "local", VITE_CASCADE_BRIDGE_URL: "" });
}

const wailsRoot = resolve(root, "backend", "cmd", "wails-desktop");
const wailsToolRoot = resolve(root, "backend", ".cache", "wails", "v2.10.2");
const wailsTool = process.env.CASCADE_WAILS_PATH || resolve(wailsToolRoot, process.platform === "win32" ? "wails.exe" : "wails");
const builtBinary = resolve(wailsRoot, "build", "bin", "cascade-demoops-desktop.exe");
const output = resolve(root, "dist", "desktop", "windows", "cascade-demoops-desktop.exe");
if (!existsSync(wailsTool)) {
  mkdirSync(wailsToolRoot, { recursive: true });
  run("go", ["install", "github.com/wailsapp/wails/v2/cmd/wails@v2.10.2"], wailsRoot, { GOBIN: wailsToolRoot });
}
run(wailsTool, ["build", "-clean", "-platform", "windows/amd64"], wailsRoot);
if (!existsSync(builtBinary)) throw new Error(`Wails did not produce ${builtBinary}`);
mkdirSync(resolve(output, ".."), { recursive: true });
cpSync(builtBinary, output);
console.log(`Built Wails desktop runtime: ${output}`);

function run(commandName, commandArgs, cwd, extraEnv = {}) {
  const result = spawnSync(commandName, commandArgs, {
    cwd,
    env: { ...process.env, ...extraEnv },
    stdio: "inherit",
  });
  if (result.error?.code === "ENOENT") {
    throw new Error(`${commandName} is required to build the Wails desktop shell`);
  }
  if (result.status !== 0) process.exit(result.status ?? 1);
}
