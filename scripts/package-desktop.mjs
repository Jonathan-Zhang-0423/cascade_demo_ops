import { createHash, createPublicKey } from "node:crypto";
import { mkdirSync, cpSync, existsSync, readFileSync, realpathSync, rmSync, statSync, writeFileSync, readdirSync } from "node:fs";
import { basename, relative, resolve } from "node:path";
import { spawnSync } from "node:child_process";

const root = resolve(".");
const appPackage = JSON.parse(readFileSync(resolve("package.json"), "utf8"));
const packageRoot = resolve("dist", "package");
const resourceRoot = resolve(packageRoot, "resources");
const releaseRoot = resolve("dist", "release");
const videoWorkerDist = resolve("video-worker", "dist");
const webDist = resolve("frontend", "web", "dist");
const targetGOOS = process.platform === "win32" ? "windows" : "darwin";
const desktopExt = targetGOOS === "windows" ? ".exe" : "";
const nodeBinaryName = targetGOOS === "windows" ? "node.exe" : "node";
const bundledNodePath = resolve(resourceRoot, "runtimes", "node", nodeBinaryName);
const bundledFFmpegPath = resolve(resourceRoot, "runtimes", "ffmpeg", targetGOOS === "windows" ? "ffmpeg.exe" : "ffmpeg");
const bundledFFprobePath = resolve(resourceRoot, "runtimes", "ffmpeg", targetGOOS === "windows" ? "ffprobe.exe" : "ffprobe");
const sourceFFmpegPath = String(process.env.CASCADE_PACKAGE_FFMPEG_PATH || "").trim();
const sourceFFprobePath = String(process.env.CASCADE_PACKAGE_FFPROBE_PATH || "").trim();
const sourceUpdatePublicKeyPath = String(process.env.DEMOOPS_UPDATE_PUBLIC_KEY_PATH || "").trim();
const updateManifestURL = String(process.env.DEMOOPS_UPDATE_MANIFEST_URL || "").trim();
const releaseChannel = String(process.env.CASCADE_RELEASE_CHANNEL || "internal").trim();
const desktopBinary = resolve("dist", "desktop", targetGOOS, `cascade-demoops-desktop${desktopExt}`);
const updaterBinary = resolve("dist", "desktop", targetGOOS, `cascade-demoops-updater${desktopExt}`);
const directConfigurerBinary = resolve("dist", "desktop", targetGOOS, `browser-agent-direct-configure${desktopExt}`);
const packagedDirectConfigurer = resolve(packageRoot, "tools", basename(directConfigurerBinary));
const releaseBaseName = `CascadeDemoOps-${appPackage.version || "0.0.0"}-${targetGOOS}-${process.arch}`;
const releaseZip = resolve(releaseRoot, `${releaseBaseName}.zip`);
const releaseChecksum = `${releaseZip}.sha256`;

if (!new Set(["internal", "beta", "stable"]).has(releaseChannel)) {
  throw new Error(`Unsupported CASCADE_RELEASE_CHANNEL: ${releaseChannel}`);
}
validateMediaBuildInput(sourceFFmpegPath, "ffmpeg");
validateMediaBuildInput(sourceFFprobePath, "ffprobe");
validateFileBuildInput(sourceUpdatePublicKeyPath, "update public key");
validateUpdatePublicKey(sourceUpdatePublicKeyPath);
if (releaseChannel !== "internal" && (!sourceFFmpegPath || !sourceFFprobePath)) {
  throw new Error("beta/stable packages require CASCADE_PACKAGE_FFMPEG_PATH and CASCADE_PACKAGE_FFPROBE_PATH");
}
if (releaseChannel !== "internal" && !sourceUpdatePublicKeyPath) {
  throw new Error("beta/stable packages require DEMOOPS_UPDATE_PUBLIC_KEY_PATH");
}
if (releaseChannel !== "internal" && !updateManifestURL.startsWith("https://")) {
  throw new Error("beta/stable packages require DEMOOPS_UPDATE_MANIFEST_URL on a DemoOps HTTPS release origin");
}
if (!fallbackWebBuild()) {
  runWithFallback("pnpm", ["--filter", "@cascade/web", "build"], fallbackWebBuild);
}
if (!fallbackWorkerBuild()) {
  runWithFallback("pnpm", ["--filter", "@cascade/video-worker", "build"], fallbackWorkerBuild);
}
if (targetGOOS === "windows") {
  run("node", ["scripts/build-wails-desktop.mjs"], { env: { CASCADE_SKIP_WEB_BUILD: "1" } });
  run("go", ["build", "-trimpath", "-ldflags", "-s -w", "-o", updaterBinary, "./cmd/desktop-updater"], { cwd: resolve(root, "backend") });
  run("go", ["build", "-trimpath", "-ldflags", "-s -w", "-o", directConfigurerBinary, "./cmd/browser-agent-direct-configure"], { cwd: resolve(root, "backend") });
} else {
  run("node", ["scripts/build-desktop.mjs", targetGOOS]);
}

rmSync(packageRoot, { recursive: true, force: true });
mkdirSync(resourceRoot, { recursive: true });
copyIfExists(desktopBinary, resolve(packageRoot, basename(desktopBinary)));
copyIfExists(updaterBinary, resolve(packageRoot, basename(updaterBinary)));
if (targetGOOS === "windows") copyIfExists(directConfigurerBinary, packagedDirectConfigurer);
copyIfExists(videoWorkerDist, resolve(resourceRoot, "sidecars", "video-worker", "dist"));
copyVideoWorkerRuntimeDependencies(resolve(resourceRoot, "sidecars", "video-worker", "node_modules"));
copyIfExists(webDist, resolve(resourceRoot, "web"));
copyIfExists(resolve("skills", "final-film"), resolve(resourceRoot, "skills", "final-film"));
copyIfExists(process.execPath, bundledNodePath);
copyMediaRuntime(sourceFFmpegPath, bundledFFmpegPath, "ffmpeg");
copyMediaRuntime(sourceFFprobePath, bundledFFprobePath, "ffprobe");
if (sourceUpdatePublicKeyPath) copyIfExists(sourceUpdatePublicKeyPath, resolve(resourceRoot, "updates", "release-public-key.pem"));
if (releaseChannel !== "internal" && (!existsSync(bundledFFmpegPath) || !existsSync(bundledFFprobePath))) {
  throw new Error("beta/stable packages require CASCADE_PACKAGE_FFMPEG_PATH and CASCADE_PACKAGE_FFPROBE_PATH");
}
if (releaseChannel !== "internal") {
  run("node", ["scripts/sign-windows.mjs", resolve(packageRoot, basename(desktopBinary)), resolve(packageRoot, basename(updaterBinary)), packagedDirectConfigurer]);
}
const runtimes = {};
if (existsSync(bundledNodePath)) runtimes.node = `runtimes/node/${nodeBinaryName}`;
if (existsSync(bundledFFmpegPath)) runtimes.ffmpeg = `runtimes/ffmpeg/${basename(bundledFFmpegPath)}`;
if (existsSync(bundledFFprobePath)) runtimes.ffprobe = `runtimes/ffmpeg/${basename(bundledFFprobePath)}`;
const runtimeManifest = {
  app: "Cascade DemoOps",
  version: appPackage.version || "0.0.0",
  resource_contract_version: 1,
    browser_agent_direct: {
    protocol_version: "browser-agent-direct-v1",
    crypto_suite: "AES-256-GCM+HKDF-SHA256",
    control_url_embedded: false,
    access_token_embedded: false,
    configuration: "user_managed_windows_credential_manager",
    provisioning_tool: targetGOOS === "windows" ? `../tools/${basename(directConfigurerBinary)}` : "",
    provisioning_tool_token_argument_supported: false,
    provisioning_tool_strict_known_hosts: true,
    provisioning_tool_ssh_host_key_algorithm: "ssh-ed25519",
  },
  sidecars: {
    "video-worker": "sidecars/video-worker/dist/index.js",
  },
  runtimes,
  web: "web",
  director_skills: "skills/final-film",
  updates: {
    channel: releaseChannel,
    manifest_url: updateManifestURL,
    public_key: sourceUpdatePublicKeyPath ? "updates/release-public-key.pem" : "",
    updater: `../${basename(updaterBinary)}`,
    previous_installer: "../previous-installer.exe",
    app_executable: `../${basename(desktopBinary)}`,
  },
};
writeJSON(resolve(resourceRoot, "desktop-runtime.json"), runtimeManifest);

const packageManifest = buildPackageManifest();
writeJSON(resolve(packageRoot, "package-manifest.json"), packageManifest);

mkdirSync(releaseRoot, { recursive: true });
rmSync(releaseZip, { force: true });
rmSync(releaseChecksum, { force: true });
createZip(packageRoot, releaseZip);
const zipSHA256 = sha256File(releaseZip);
writeFileSync(releaseChecksum, `${zipSHA256}  ${basename(releaseZip)}\n`);

writeJSON(resolve(releaseRoot, `${releaseBaseName}.manifest.json`), {
  ...packageManifest,
  artifact: {
    file_name: basename(releaseZip),
    sha256: zipSHA256,
    size_bytes: statSync(releaseZip).size,
  },
});

console.log(`Prepared desktop package resources under ${resourceRoot}`);
console.log(`Created desktop release archive: ${releaseZip}`);
console.log(`Created checksum: ${releaseChecksum}`);

function buildPackageManifest() {
  const files = listFiles(packageRoot)
    .filter((file) => relative(packageRoot, file) !== "package-manifest.json")
    .map((file) => ({
      path: slash(relative(packageRoot, file)),
      size_bytes: statSync(file).size,
      sha256: sha256File(file),
    }));
  return {
    schema_version: "demoops.desktop_package_manifest.v1",
    app: "Cascade DemoOps",
    package_name: "Cascade DemoOps Desktop",
    version: appPackage.version || "0.0.0",
    target_os: targetGOOS,
    target_arch: process.arch,
    package_kind: "portable_zip",
    release_channel: releaseChannel,
    created_at: new Date().toISOString(),
    entrypoint: slash(basename(desktopBinary)),
    resource_manifest: "resources/desktop-runtime.json",
    updater: existsSync(updaterBinary)
      ? {
          path: slash(basename(updaterBinary)),
          public_key: sourceUpdatePublicKeyPath ? "resources/updates/release-public-key.pem" : "",
          channels: ["internal", "beta", "stable"],
          verifies: ["https", "ed25519", "sha256", "authenticode"],
          rollback_requires_previous_installer: true,
        }
      : undefined,
    desktop_ui: {
      primary: targetGOOS === "windows" ? "wails_webview2" : "legacy_native",
      uses_browser_shell: targetGOOS === "windows",
      legacy_fallback_entry: "",
      legacy_fallback_disabled: true,
      native_capabilities: [
        "input_collection",
        "native_input_readiness_summary",
        "native_input_preflight_detail",
        "native_generate_readiness_gate",
        "non_sensitive_input_draft_persistence",
        "non_sensitive_input_draft_clear",
        "native_menu_bar",
        "native_keyboard_shortcuts",
        "native_confirmation_dialogs",
        "native_folder_picker",
        "requirement_document_import",
        "local_package_generation",
        "native_lifecycle_phase_bar",
        "native_server_connection_status",
        "approval_health_summary",
        "native_package_gate_summary",
        "native_approval_review_summary",
        "native_approval_checklist",
        "native_local_approval_record",
        "native_server_handoff_summary",
        "native_approved_package_upload_init",
        "native_server_handoff_record",
        "native_server_status_query",
        "native_server_result_package_fetch",
        "native_server_result_ack",
        "native_server_artifact_download",
        "native_server_artifact_checksum_verify",
        "native_server_artifact_open_folder",
        "native_server_primary_artifact_open",
        "native_server_delivery_health_summary",
        "native_stage_explorer",
        "stage_plan_review",
        "script_outline_review",
        "native_approval_clipboard_copy",
        "native_approval_file_open",
        "three_in_one_package_save",
        "native_package_folder_import",
        "native_package_folder_export",
        "native_export_overwrite_protection",
        "native_recent_package_history",
        "native_recent_package_summary",
        "native_recent_package_open",
        "artifact_folder_open",
        "diagnostic_log_open",
        "native_environment_status_bar",
        "native_visual_hierarchy",
      ],
    },
    runtimes: {
      node: existsSync(bundledNodePath)
        ? {
            path: slash(relative(packageRoot, bundledNodePath)),
            source: "bundled",
            required_for: ["video-worker"],
          }
        : {
            path: "",
            source: "system",
            required_for: ["video-worker"],
          },
      ffmpeg: runtimeEntry(bundledFFmpegPath, "video-worker rendering and media validation"),
      ffprobe: runtimeEntry(bundledFFprobePath, "video-worker media inspection"),
    },
    server_connectivity: {
      primary_transport: "browser_agent_direct_v1",
      demoops_exchange_primary: false,
      fixed_tls_control_port: true,
      dedicated_data_port_per_lease: true,
      timestamp_token_authenticated_encryption: true,
      required_for_local_generation: false,
      server_recording_required: true,
      local_recording_execution: false,
      server_responsibilities: [
        "browser_execution",
        "adaptive_recording",
        "failure_diagnosis",
      ],
      app_responsibilities: [
        "input_collection",
        "local_package_generation",
        "stage_plan_review",
        "approved_package_upload",
        "result_video_download",
        "error_report_download",
        "video_editing",
      ],
      reserved_interfaces: [
        "BrowserAgentDirectClient",
        "DirectInstallationIdentityStore",
        "DirectLeaseManager",
        "DirectArtifactVerifier",
      ],
      provisioning_tool: targetGOOS === "windows"
        ? {
            path: slash(relative(packageRoot, packagedDirectConfigurer)),
            token_argument_supported: false,
            token_stdout_supported: false,
            strict_known_hosts: true,
            ssh_authentication: "ed25519_public_key_only",
            ssh_host_key_algorithm: "ssh-ed25519",
            credential_store: "windows_credential_manager",
          }
        : undefined,
    },
    app_surfaces: [
      {
        id: "demo_asset_generation_console",
        label: "演示资产生成控制台",
        required: true,
        entry_nav_label: "项目",
        capabilities: [
          "input_collection",
          "local_package_generation",
          "stage_plan_review",
          "execution_package_approval",
          "approved_package_upload",
          "result_video_download",
          "error_report_download",
        ],
      },
      {
        id: "video_editor",
        label: "视频编辑器",
        required: true,
        entry_nav_label: "视频编辑",
        capabilities: [
          "result_package_import",
          "timeline_editing",
          "caption_and_callout_editing",
          "preview_and_export",
        ],
      },
    ],
    files,
  };
}

function runtimeEntry(path, requiredFor) {
  if (!existsSync(path)) return { path: "", source: "not_bundled", required_for: [requiredFor] };
  return {
    path: slash(relative(packageRoot, path)),
    source: "build_input",
    required_for: [requiredFor],
    sha256: sha256File(path),
    size_bytes: statSync(path).size,
  };
}

function copyMediaRuntime(source, destination, label) {
  if (!source) {
    console.warn(`Skipping ${label}: set CASCADE_PACKAGE_${label.toUpperCase()}_PATH to bundle it.`);
    return;
  }
  if (!existsSync(source) || statSync(source).isDirectory()) {
    throw new Error(`Invalid ${label} build input: ${source}`);
  }
  copyIfExists(source, destination);
}

function validateMediaBuildInput(source, label) {
  if (!source) return;
  if (!existsSync(source) || statSync(source).isDirectory()) {
    throw new Error(`Invalid ${label} build input: ${source}`);
  }
}

function validateFileBuildInput(source, label) {
  if (!source) return;
  if (!existsSync(source) || statSync(source).isDirectory()) {
    throw new Error(`Invalid ${label} build input: ${source}`);
  }
}

function validateUpdatePublicKey(source) {
  if (!source) return;
  let key;
  try {
    key = createPublicKey(readFileSync(source));
  } catch (error) {
    throw new Error(`Invalid update public key PEM: ${error instanceof Error ? error.message : String(error)}`);
  }
  if (key.asymmetricKeyType !== "ed25519") {
    throw new Error("DEMOOPS_UPDATE_PUBLIC_KEY_PATH must contain an Ed25519 public key");
  }
}

function run(command, args, options = {}) {
  const result = spawn(command, args, options);
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

function spawn(command, args, options = {}) {
  const executable = process.platform === "win32" && command === "pnpm" ? "cmd.exe" : command;
  const finalArgs = process.platform === "win32" && command === "pnpm" ? ["/d", "/s", "/c", command, ...args] : args;
  const result = spawnSync(executable, finalArgs, {
    cwd: options.cwd || root,
    env: {
      ...process.env,
      ...(options.env ?? {}),
      VITE_CASCADE_BRIDGE: process.env.VITE_CASCADE_BRIDGE || "local",
      VITE_CASCADE_BRIDGE_URL: process.env.VITE_CASCADE_BRIDGE_URL || "",
    },
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
  const stat = statSync(from);
  mkdirSync(stat.isDirectory() ? to : resolve(to, ".."), { recursive: true });
  cpSync(from, to, { recursive: true });
}

function copyVideoWorkerRuntimeDependencies(destination) {
  const linkedPlaywright = resolve("video-worker", "node_modules", "playwright");
  if (!existsSync(linkedPlaywright)) {
    throw new Error("Missing required video-worker runtime dependency: playwright");
  }
  const playwright = realpathSync(linkedPlaywright);
  const linkedCore = resolve(playwright, "..", "playwright-core");
  if (!existsSync(linkedCore)) {
    throw new Error("Missing required video-worker runtime dependency: playwright-core");
  }
  copyIfExists(playwright, resolve(destination, "playwright"));
  copyIfExists(realpathSync(linkedCore), resolve(destination, "playwright-core"));
}

function createZip(fromDir, toFile) {
  if (process.platform === "win32") {
    run("powershell", [
      "-NoProfile",
      "-ExecutionPolicy",
      "Bypass",
      "-Command",
      `Compress-Archive -Path '${escapePowerShell(fromDir)}\\*' -DestinationPath '${escapePowerShell(toFile)}' -Force`,
    ]);
    return;
  }
  run("zip", ["-qr", toFile, "."], { cwd: fromDir });
}

function listFiles(dir) {
  const out = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const fullPath = resolve(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...listFiles(fullPath));
    } else if (entry.isFile()) {
      out.push(fullPath);
    }
  }
  return out;
}

function sha256File(file) {
  return createHash("sha256").update(readFileSync(file)).digest("hex");
}

function writeJSON(path, value) {
  writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`);
}

function slash(path) {
  return path.replaceAll("\\", "/");
}

function escapePowerShell(value) {
  return value.replaceAll("'", "''");
}
