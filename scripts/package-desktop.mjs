import { createHash } from "node:crypto";
import { mkdirSync, cpSync, existsSync, readFileSync, rmSync, statSync, writeFileSync, readdirSync } from "node:fs";
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
const desktopBinary = resolve("dist", "desktop", targetGOOS, `cascade-demoops-desktop${desktopExt}`);
const releaseBaseName = `CascadeDemoOps-${appPackage.version || "0.0.0"}-${targetGOOS}-${process.arch}`;
const releaseZip = resolve(releaseRoot, `${releaseBaseName}.zip`);
const releaseChecksum = `${releaseZip}.sha256`;

if (!fallbackWebBuild()) {
  runWithFallback("pnpm", ["--filter", "@cascade/web", "build"], fallbackWebBuild);
}
if (!fallbackWorkerBuild()) {
  runWithFallback("pnpm", ["--filter", "@cascade/video-worker", "build"], fallbackWorkerBuild);
}
run("node", ["scripts/build-desktop.mjs", targetGOOS]);

rmSync(packageRoot, { recursive: true, force: true });
mkdirSync(resourceRoot, { recursive: true });
copyIfExists(desktopBinary, resolve(packageRoot, basename(desktopBinary)));
copyIfExists(videoWorkerDist, resolve(resourceRoot, "sidecars", "video-worker", "dist"));
copyIfExists(webDist, resolve(resourceRoot, "web"));
copyIfExists(process.execPath, bundledNodePath);
const runtimeManifest = {
  app: "Cascade DemoOps",
  resource_contract_version: 1,
  sidecars: {
    "video-worker": "sidecars/video-worker/dist/index.js",
  },
  runtimes: existsSync(bundledNodePath)
    ? {
        node: `runtimes/node/${nodeBinaryName}`,
      }
    : {},
  web: "web",
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
    created_at: new Date().toISOString(),
    entrypoint: slash(basename(desktopBinary)),
    resource_manifest: "resources/desktop-runtime.json",
    desktop_ui: {
      primary: "native_win32",
      uses_browser_shell: false,
      compatibility_web_host_flag: "--native=false",
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
        "native_local_approval_record",
        "native_server_handoff_summary",
        "native_approved_package_upload_init",
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
    },
    server_connectivity: {
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
        "ExchangeCapabilityResolver",
        "ExchangeIdentityStore",
        "ExchangeSessionManager",
        "CloudLifecycleClient",
      ],
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
