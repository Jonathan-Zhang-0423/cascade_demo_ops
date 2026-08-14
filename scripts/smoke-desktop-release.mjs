import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(".");
const packageManifestPath = resolve(root, "dist", "package", "package-manifest.json");
const runtimeManifestPath = resolve(root, "dist", "package", "resources", "desktop-runtime.json");
assertFile(packageManifestPath, "desktop package manifest");
assertFile(runtimeManifestPath, "desktop runtime manifest");
const packageManifest = JSON.parse(readFileSync(packageManifestPath, "utf8"));
const runtimeManifest = JSON.parse(readFileSync(runtimeManifestPath, "utf8"));
assert(packageManifest.desktop_ui?.primary === "wails_webview2", "Windows package must use Wails/WebView2");
assert(packageManifest.updater?.path === "cascade-demoops-updater.exe", "isolated updater is required");
assert(runtimeManifest.runtimes?.node, "bundled Node runtime is required");
const channel = packageManifest.release_channel || "internal";
if (channel !== "internal") {
  assert(runtimeManifest.runtimes?.ffmpeg, `${channel} package must bundle FFmpeg`);
  assert(runtimeManifest.runtimes?.ffprobe, `${channel} package must bundle ffprobe`);
  assert(packageManifest.updater?.public_key === "resources/updates/release-public-key.pem", `${channel} package must bundle the Ed25519 update public key`);
  const version = packageManifest.version;
  const installer = resolve(root, "dist", "release", `CascadeDemoOps-${version}-windows-x64-setup.exe`);
  assertFile(installer, "Inno Setup installer");
  assert(runtimeManifest.browser_agent_direct?.protocol_version === "browser-agent-direct-v1", `${channel} package must declare Browser Agent direct transport`);
  assert(runtimeManifest.browser_agent_direct?.control_url_embedded === false, `${channel} package must require user-managed Browser Agent server configuration`);
  assert(runtimeManifest.browser_agent_direct?.access_token_embedded === false, `${channel} package must not embed Browser Agent credentials`);
}
console.log(`Desktop release smoke passed for ${channel} channel.`);

function assertFile(path, label) {
  assert(existsSync(path), `Missing ${label}: ${path}`);
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}
