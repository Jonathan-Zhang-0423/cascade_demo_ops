import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { basename, dirname, resolve } from "node:path";
import { spawnSync } from "node:child_process";

const payloadMarker = Buffer.from("CASCADE_DEMOOPS_INSTALLER_PAYLOAD_V1", "utf8");
const root = resolve(".");
const appPackage = JSON.parse(readFileSync(resolve("package.json"), "utf8"));
const targetGOOS = process.platform === "win32" ? "windows" : "darwin";
if (targetGOOS !== "windows") {
  console.error("Desktop installer packaging currently supports Windows only.");
  process.exit(1);
}

const targetArch = process.arch;
const version = appPackage.version || "0.0.0";
const releaseBaseName = `CascadeDemoOps-${version}-${targetGOOS}-${targetArch}`;
const releaseRoot = resolve("dist", "release");
const portableZip = resolve(releaseRoot, `${releaseBaseName}.zip`);
const installerDir = resolve("dist", "installer", releaseBaseName);
const installerExe = resolve(installerDir, "CascadeDemoOpsInstaller.exe");
const releaseInstaller = resolve(releaseRoot, `${releaseBaseName}-installer.exe`);
const releaseInstallerSidecarManifest = `${releaseInstaller}.manifest`;
const releaseChecksum = `${releaseInstaller}.sha256`;
const releaseManifest = resolve(releaseRoot, `${releaseBaseName}-installer.manifest.json`);
const releaseChannelManifest = resolve(releaseRoot, "CascadeDemoOps-desktop-latest.json");
const appSurfaces = [
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
];
const serverConnectivity = {
  required_for_local_generation: false,
  reserved_interfaces: [
    "ExchangeCapabilityResolver",
    "ExchangeIdentityStore",
    "ExchangeSessionManager",
    "CloudLifecycleClient",
  ],
};

run("node", ["scripts/package-desktop.mjs"]);
assertFile(portableZip, "portable desktop zip");

rmSync(installerDir, { recursive: true, force: true });
mkdirSync(dirname(installerExe), { recursive: true });
run("go", ["build", "-o", installerExe, "./cmd/desktop-installer"], {
  cwd: resolve("backend"),
  env: { ...process.env, GOOS: "windows", GOARCH: process.env.GOARCH || "amd64" },
});

const payload = readFileSync(portableZip);
const payloadSHA256 = sha256(payload);
const trailer = {
  schema_version: "demoops.desktop_installer_payload.v1",
  app: "Cascade DemoOps",
  version,
  target_os: targetGOOS,
  target_arch: targetArch,
  package_kind: "portable_zip",
  payload_name: basename(portableZip),
  payload_size: payload.length,
  payload_sha256: payloadSHA256,
};
appendPayload(installerExe, payload, trailer);

mkdirSync(releaseRoot, { recursive: true });
rmSync(releaseInstaller, { force: true });
rmSync(releaseInstallerSidecarManifest, { force: true });
rmSync(releaseChecksum, { force: true });
for (const suffix of ["setup", "bootstrap"]) {
  rmSync(resolve(releaseRoot, `${releaseBaseName}-${suffix}.exe`), { force: true });
  rmSync(resolve(releaseRoot, `${releaseBaseName}-${suffix}.exe.manifest`), { force: true });
  rmSync(resolve(releaseRoot, `${releaseBaseName}-${suffix}.exe.sha256`), { force: true });
  rmSync(resolve(releaseRoot, `${releaseBaseName}-${suffix}.manifest.json`), { force: true });
}
copyFile(installerExe, releaseInstaller);
writeFileSync(releaseInstallerSidecarManifest, windowsAsInvokerManifest());
const installerSHA256 = sha256File(releaseInstaller);
writeFileSync(releaseChecksum, `${installerSHA256}  ${basename(releaseInstaller)}\n`);
const portableManifestPath = resolve(releaseRoot, `${releaseBaseName}.manifest.json`);
const portableManifest = JSON.parse(readFileSync(portableManifestPath, "utf8"));
writeJSON(releaseManifest, {
  schema_version: "demoops.desktop_installer_manifest.v1",
  app: "Cascade DemoOps",
  package_name: "Cascade DemoOps Desktop Installer",
  version,
  target_os: targetGOOS,
  target_arch: targetArch,
  package_kind: "self_extracting_setup_exe",
  created_at: new Date().toISOString(),
  entrypoint: basename(releaseInstaller),
  windows_manifest: {
    file_name: basename(releaseInstallerSidecarManifest),
    requested_execution_level: "asInvoker",
    ui_access: false,
    embedded: false,
  },
  artifact: {
    file_name: basename(releaseInstaller),
    sha256: installerSHA256,
    size_bytes: statSync(releaseInstaller).size,
  },
  payload: {
    file_name: basename(portableZip),
    sha256: payloadSHA256,
    size_bytes: payload.length,
  },
  runtimes: {
    node: {
      path: "resources/runtimes/node/node.exe",
      source: "bundled",
      required_for: ["video-worker"],
    },
  },
  install_behavior: {
    default_scope: "per_user",
    default_install_dir: "%LOCALAPPDATA%/Programs/CascadeDemoOps",
    writes_user_data_dir: "%APPDATA%/CascadeDemoOps",
    creates_start_menu_launcher: true,
    supports_custom_install_dir: true,
    supports_silent_install: true,
  },
  server_connectivity: {
    ...serverConnectivity,
  },
  app_surfaces: appSurfaces,
});
writeJSON(releaseChannelManifest, {
  schema_version: "demoops.desktop_release_channel.v1",
  channel: "latest",
  app: "Cascade DemoOps",
  package_name: "Cascade DemoOps Desktop",
  version,
  target_os: targetGOOS,
  target_arch: targetArch,
  generated_at: new Date().toISOString(),
  recommended_artifact: "installer",
  artifacts: {
    installer: {
      file_name: basename(releaseInstaller),
      manifest_file_name: basename(releaseManifest),
      sha256: installerSHA256,
      size_bytes: statSync(releaseInstaller).size,
      windows_manifest_file_name: basename(releaseInstallerSidecarManifest),
      checksum_file_name: basename(releaseChecksum),
    },
    portable_zip: {
      file_name: basename(portableZip),
      manifest_file_name: basename(portableManifestPath),
      sha256: portableManifest.artifact?.sha256 ?? payloadSHA256,
      size_bytes: statSync(portableZip).size,
      checksum_file_name: `${basename(portableZip)}.sha256`,
    },
  },
  install_behavior: {
    default_scope: "per_user",
    default_install_dir: "%LOCALAPPDATA%/Programs/CascadeDemoOps",
    writes_user_data_dir: "%APPDATA%/CascadeDemoOps",
    creates_start_menu_launcher: true,
    supports_custom_install_dir: true,
    supports_silent_install: true,
  },
  server_connectivity: serverConnectivity,
  app_surfaces: appSurfaces,
});

console.log(`Created desktop installer: ${releaseInstaller}`);
console.log(`Created checksum: ${releaseChecksum}`);
console.log(`Created release channel manifest: ${releaseChannelManifest}`);

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    cwd: options.cwd || root,
    env: {
      ...process.env,
      VITE_CASCADE_BRIDGE: process.env.VITE_CASCADE_BRIDGE || "local",
      VITE_CASCADE_BRIDGE_URL: process.env.VITE_CASCADE_BRIDGE_URL || "",
      ...(options.env || {}),
    },
    stdio: "inherit",
  });
  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}

function appendPayload(exePath, payload, trailer) {
  const trailerBytes = Buffer.from(`${JSON.stringify(trailer)}\n`, "utf8");
  const lengthBytes = Buffer.alloc(8);
  lengthBytes.writeBigUInt64BE(BigInt(trailerBytes.length));
  const exe = readFileSync(exePath);
  writeFileSync(exePath, Buffer.concat([exe, payload, trailerBytes, lengthBytes, payloadMarker]));
}

function assertFile(path, label) {
  if (!existsSync(path)) {
    throw new Error(`missing ${label}: ${path}`);
  }
}

function copyFile(from, to) {
  writeFileSync(to, readFileSync(from));
}

function sha256(buffer) {
  return createHash("sha256").update(buffer).digest("hex");
}

function sha256File(path) {
  return sha256(readFileSync(path));
}

function writeJSON(path, value) {
  writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`);
}

function windowsAsInvokerManifest() {
  return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity version="1.0.0.0" processorArchitecture="amd64" name="CascadeDemoOps.DesktopInstaller" type="win32"/>
  <description>Cascade DemoOps Desktop Installer</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
</assembly>
`;
}
