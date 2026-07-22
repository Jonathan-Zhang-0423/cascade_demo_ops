import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { basename, resolve } from "node:path";
import { spawnSync } from "node:child_process";

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
assert(manifest.server_connectivity?.required_for_local_generation === false, "server connectivity must be optional for local generation");
assert(Array.isArray(manifest.server_connectivity?.reserved_interfaces), "server reserved interfaces are required");
for (const name of ["ExchangeCapabilityResolver", "ExchangeIdentityStore", "ExchangeSessionManager", "CloudLifecycleClient"]) {
  assert(manifest.server_connectivity.reserved_interfaces.includes(name), `missing reserved interface ${name}`);
}

const expectedZipHash = readFileHashLine(checksumPath, basename(zipPath));
const actualZipHash = sha256File(zipPath);
assert(actualZipHash === expectedZipHash, `zip checksum mismatch: ${actualZipHash} !== ${expectedZipHash}`);
assert(manifest.artifact?.sha256 === actualZipHash, "release manifest artifact hash mismatch");

const entries = listZipEntries(zipPath);
for (const required of [
  "cascade-demoops-desktop.exe",
  "package-manifest.json",
  "resources/desktop-runtime.json",
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

const result = spawnSync(entrypoint, {
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

console.log(`Desktop package smoke passed: ${zipPath}`);

function assertFile(path, label) {
  assert(existsSync(path), `missing ${label}: ${path}`);
}

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
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
