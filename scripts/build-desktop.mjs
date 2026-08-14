import { mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { spawnSync } from "node:child_process";

const target = process.argv[2] || process.platform;
const targets = {
  win: { goos: "windows", ext: ".exe" },
  windows: { goos: "windows", ext: ".exe" },
  mac: { goos: "darwin", ext: "" },
  darwin: { goos: "darwin", ext: "" },
};

const spec = targets[target];
if (!spec) {
  console.error(`Unsupported desktop target: ${target}`);
  process.exit(1);
}

const out = resolve("dist", "desktop", spec.goos, `cascade-demoops-desktop${spec.ext}`);
mkdirSync(dirname(out), { recursive: true });

if (spec.goos === "windows") {
  const result = spawnSync("node", ["scripts/build-wails-desktop.mjs"], { cwd: resolve("."), stdio: "inherit" });
  if (result.status !== 0) process.exit(result.status ?? 1);
  process.exit(0);
}

const buildArgs = ["build", "-o", out];
if (spec.goos === "windows") {
  buildArgs.push("-ldflags", "-H=windowsgui");
}
buildArgs.push("./cmd/desktop");

const result = spawnSync("go", buildArgs, {
  cwd: resolve("backend"),
  env: { ...process.env, GOOS: spec.goos, GOARCH: process.env.GOARCH || "amd64" },
  stdio: "inherit",
});

if (result.status !== 0) {
  process.exit(result.status ?? 1);
}

console.log(`Built desktop runtime: ${out}`);
