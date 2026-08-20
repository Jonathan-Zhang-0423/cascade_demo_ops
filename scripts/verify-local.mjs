import { mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const backendDir = resolve(root, "backend");
const goCache = resolve(root, ".gocache");
const goTmp = resolve(root, ".gotmp");
const isWindows = process.platform === "win32";
const rawArgs = process.argv.slice(2).filter((arg) => arg !== "--");
const args = new Set(rawArgs);
const useWSLGo = isWindows && args.has("--wsl-go");
const wslProxyPort = optionValue("--wsl-proxy") || process.env.CASCADE_WSL_PROXY_PORT || "";

mkdirSync(goCache, { recursive: true });
mkdirSync(goTmp, { recursive: true });

const steps = [
  {
    name: "Director skill validation",
    command: process.execPath,
    args: [resolve(root, "scripts", "validate-director-skills.mjs")],
    cwd: root,
    group: "node",
  },
  {
    name: "Site-neutral core gate",
    command: process.execPath,
    args: [resolve(root, "scripts", "validate-site-neutral-core.mjs")],
    cwd: root,
    group: "node",
  },
  {
    name: "Go version",
    ...goStep("version"),
    cwd: backendDir,
    group: "go",
  },
  {
    name: "Backend build",
    ...goStep("build", "./..."),
    cwd: backendDir,
    group: "go",
  },
  {
    name: "Backend tests",
    ...goStep("test", "./..."),
    cwd: backendDir,
    group: "go",
  },
  {
    name: "Video worker typecheck",
    ...pnpmStep("--filter", "@cascade/video-worker", "typecheck"),
    cwd: root,
    group: "node",
  },
  {
    name: "Video worker build",
    ...pnpmStep("--filter", "@cascade/video-worker", "build"),
    cwd: root,
    group: "node",
  },
  {
    name: "Video worker requirement report smoke",
    ...pnpmStep("--filter", "@cascade/video-worker", "smoke:render:requirements"),
    cwd: root,
    group: "node",
  },
  {
    name: "Video worker generated candidate smoke",
    ...pnpmStep("--filter", "@cascade/video-worker", "smoke:render:candidates"),
    cwd: root,
    group: "node",
  },
  {
    name: "Video worker format fallback smoke",
    ...pnpmStep("--filter", "@cascade/video-worker", "smoke:render:format-fallback"),
    cwd: root,
    group: "node",
  },
  {
    name: "Server render validation fixture smoke",
    command: process.execPath,
    args: [resolve(root, "scripts", "smoke-validate-server-render.mjs")],
    cwd: root,
    group: "node",
  },
  {
    name: "Web tests",
    ...pnpmStep("--filter", "@cascade/web", "test"),
    cwd: root,
    group: "node",
  },
  {
    name: "Web build",
    ...pnpmStep("--filter", "@cascade/web", "build"),
    cwd: root,
    group: "node",
  },
  {
    name: "Diff whitespace check",
    command: "git",
    args: ["diff", "--check"],
    cwd: root,
    group: "git",
  },
];

const selectedSteps = steps.filter((step) => {
  if (args.has("--skip-go") && step.group === "go") return false;
  if (args.has("--go-only") && step.group !== "go") return false;
  if (args.has("--node-only") && step.group !== "node") return false;
  return true;
});

if (args.has("--help")) {
  printHelp();
  process.exit(0);
}

for (const step of selectedSteps) {
  runStep(step);
}

console.log("\nLocal verification passed.");

function runStep(step) {
  console.log(`\n==> ${step.name}`);
  console.log(`$ ${[step.command, ...step.args].join(" ")}`);
  const result = spawnSync(step.command, step.args, {
    cwd: step.cwd,
    env: { ...process.env, ...(step.env ?? {}) },
    stdio: "inherit",
    shell: false,
  });
  if (result.error) {
    console.error(`\n${step.name} failed to start: ${result.error.message}`);
    if (step.group === "go") {
      printGoApplicationControlHelp();
    }
    process.exit(result.status || 1);
  }
  if (result.status !== 0) {
    if (step.group === "go") {
      printGoApplicationControlHelp();
    }
    process.exit(result.status ?? 1);
  }
}

function goEnv() {
  return {
    GOCACHE: goCache,
    GOTMPDIR: goTmp,
  };
}

function goStep(...goArgs) {
  if (useWSLGo) {
    const wslRoot = toWSLPath(root);
    const wslBackend = toWSLPath(backendDir);
    const wslGoCache = toWSLPath(goCache);
    const wslGoTmp = toWSLPath(goTmp);
    const command = [
      wslProxyExportCommand(wslProxyPort),
      `cd ${shellQuote(wslBackend)}`,
      `GOCACHE=${shellQuote(wslGoCache)} GOTMPDIR=${shellQuote(wslGoTmp)} go ${goArgs.map(shellQuote).join(" ")}`,
    ].filter(Boolean).join(" && ");
    return {
      command: "wsl.exe",
      args: ["--distribution", "Ubuntu", "--user", "root", "--exec", "bash", "-lc", command],
      cwd: wslRoot,
    };
  }
  return {
    command: "go",
    args: goArgs,
    env: goEnv(),
  };
}

function optionValue(name) {
  for (let index = 0; index < rawArgs.length; index += 1) {
    const arg = rawArgs[index];
    if (arg === name) return rawArgs[index + 1] ?? "";
    if (arg.startsWith(`${name}=`)) return arg.slice(name.length + 1);
  }
  return "";
}

function wslProxyExportCommand(port) {
  port = String(port).trim();
  if (!port) return "";
  if (!/^\d+$/.test(port)) {
    throw new Error(`Invalid --wsl-proxy port: ${port}`);
  }
  const proxyURL = `"http://$wsl_host:${port}"`;
  return [
    `wsl_host=$(awk '/nameserver/ {print $2; exit}' /etc/resolv.conf)`,
    `test -n "$wsl_host"`,
    `export HTTP_PROXY=${proxyURL}`,
    `export HTTPS_PROXY=${proxyURL}`,
    `export ALL_PROXY=${proxyURL}`,
    `export NO_PROXY='localhost,127.0.0.1,::1'`,
    `export http_proxy="$HTTP_PROXY"`,
    `export https_proxy="$HTTPS_PROXY"`,
    `export all_proxy="$ALL_PROXY"`,
    `export no_proxy="$NO_PROXY"`,
  ].join(" && ");
}

function pnpmStep(...pnpmArgs) {
  if (isWindows) {
    return {
      command: "cmd.exe",
      args: ["/d", "/s", "/c", "corepack.cmd", "pnpm", ...pnpmArgs],
    };
  }
  return {
    command: "corepack",
    args: ["pnpm", ...pnpmArgs],
  };
}

function toWSLPath(value) {
  const normalized = resolve(value).replaceAll("\\", "/");
  const match = normalized.match(/^([A-Za-z]):\/(.*)$/);
  if (!match) return normalized;
  return `/mnt/${match[1].toLowerCase()}/${match[2]}`;
}

function shellQuote(value) {
  return "'" + String(value).replaceAll("'", "'\\''") + "'";
}

function printHelp() {
  console.log(`Usage: pnpm verify [-- --skip-go|--go-only|--node-only|--wsl-go|--wsl-proxy=7897]

Runs the strict local validation suite:
  - go version
  - go build ./...
  - go test ./...
  - video-worker typecheck/build
  - video-worker render smoke checks
  - server render validation fixture smoke
  - web tests/build
  - git diff --check

Options:
  --skip-go   Run Node/web/worker and diff checks only.
  --go-only   Run Go checks only.
  --node-only Run video-worker and web checks only.
  --wsl-go    On Windows, run Go checks inside the Ubuntu WSL distro.
  --wsl-proxy Set WSL HTTP(S) proxy to the Windows gateway on this port.
`);
}

function printGoApplicationControlHelp() {
  if (!isWindows) return;
  console.error(`
Go validation did not complete. On Windows, Application Control / Device Guard
may block unsigned Go binaries or Go-generated *.test.exe files.

Preferred fixes:
  1. Run validation inside WSL/Linux or on the server.
  2. Ask the security policy owner to allow:
     - C:\\Program Files\\Go\\bin\\go.exe
     - ${goTmp}\\**\\*.exe

The script already pins:
  GOCACHE=${goCache}
  GOTMPDIR=${goTmp}
`);
}
