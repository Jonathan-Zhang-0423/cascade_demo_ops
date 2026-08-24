import { mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

// Production Server build allowlist. Test-only commands under backend/cmd and
// tests/server-e2e are intentionally not compiled into the deployable output.
const goos = process.env.GOOS || "linux";
const goarch = process.env.GOARCH || "amd64";
const root = resolve(".");
const backend = resolve(root, "backend");
const outputDir = resolve(root, "dist", "server", `${goos}-${goarch}`);
mkdirSync(outputDir, { recursive: true });

const goEnv = { ...process.env, CGO_ENABLED: "0", GOOS: goos, GOARCH: goarch };
const targets = [
  ["browser-agent-gateway", "./cmd/browser-agent-gateway"],
  ["browser-agent-direct-worker", "./cmd/browser-agent-direct-worker"],
];

for (const [name, target] of targets) {
  run("go", ["build", "-trimpath", "-ldflags", "-s -w", "-o", resolve(outputDir, name), target], backend, goEnv);
}

run("npm", ["run", "build", "--prefix", resolve(root, "video-worker")], root, process.env);
console.log(`Built production Server binaries in ${outputDir} and formal video-worker in video-worker/dist.`);

function run(command, args, cwd, env) {
  // npm is a Windows command shim and needs shell dispatch; keep Go direct so
  // ldflags containing spaces ("-s -w") remain a single argument.
  const result = spawnSync(command, args, {
    cwd,
    env,
    stdio: "inherit",
    shell: process.platform === "win32" && command === "npm",
  });
  if (result.error?.code === "ENOENT") throw new Error(`${command} is required for the production Server build`);
  if (result.status !== 0) process.exit(result.status ?? 1);
}
