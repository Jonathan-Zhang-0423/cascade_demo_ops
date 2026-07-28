import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

const root = resolve(".");
const pkg = JSON.parse(readFileSync(resolve(root, "package.json"), "utf8"));
const version = pkg.version || "0.0.0";
const channel = process.env.CASCADE_RELEASE_CHANNEL || "internal";
const appExe = resolve(root, "dist", "package", "cascade-demoops-desktop.exe");
const updaterExe = resolve(root, "dist", "package", "cascade-demoops-updater.exe");
const webViewBootstrapper = String(process.env.CASCADE_WEBVIEW2_BOOTSTRAPPER || "").trim();
if (channel !== "internal" && (!webViewBootstrapper || !existsSync(webViewBootstrapper))) {
  throw new Error("beta/stable installers require CASCADE_WEBVIEW2_BOOTSTRAPPER");
}
run("node", ["scripts/package-desktop.mjs"]);
if (!existsSync(appExe)) throw new Error(`Packaged app is missing: ${appExe}`);
if (!existsSync(updaterExe)) throw new Error(`Packaged updater is missing: ${updaterExe}`);

const iscc = process.env.CASCADE_ISCC_PATH || "ISCC.exe";
const isccArgs = [];
if (channel !== "internal") {
  const signTool = process.env.CASCADE_SIGNTOOL_PATH || "signtool.exe";
  const thumbprint = String(process.env.CASCADE_SIGN_CERT_SHA1 || "").replaceAll(" ", "");
  const timestampURL = String(process.env.CASCADE_SIGN_TIMESTAMP_URL || "").trim();
  if (!thumbprint || !timestampURL) throw new Error("CASCADE_SIGN_CERT_SHA1 and CASCADE_SIGN_TIMESTAMP_URL are required");
  isccArgs.push(`/Sdemoops=${signTool} sign /sha1 ${thumbprint} /fd SHA256 /tr ${timestampURL} /td SHA256 $f`);
}
isccArgs.push(resolve(root, "packaging", "windows", "CascadeDemoOps.iss"));
run(iscc, isccArgs, {
  CASCADE_APP_VERSION: version,
  CASCADE_RELEASE_CHANNEL: channel,
});
const setup = resolve(root, "dist", "release", `CascadeDemoOps-${version}-windows-x64-setup.exe`);
if (!existsSync(setup)) throw new Error(`Inno Setup output is missing: ${setup}`);
if (channel !== "internal") run("node", ["scripts/sign-windows.mjs", setup]);
console.log(`Created Inno Setup installer: ${setup}`);

function run(command, args, extraEnv = {}) {
  const result = spawnSync(command, args, {
    cwd: root,
    env: { ...process.env, ...extraEnv },
    stdio: "inherit",
  });
  if (result.error?.code === "ENOENT") throw new Error(`${command} was not found`);
  if (result.status !== 0) process.exit(result.status ?? 1);
}
