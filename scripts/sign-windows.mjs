import { existsSync } from "node:fs";
import { spawnSync } from "node:child_process";

const files = process.argv.slice(2);
if (files.length === 0) throw new Error("Usage: node scripts/sign-windows.mjs <file> [...]");
const signtool = process.env.CASCADE_SIGNTOOL_PATH || "signtool.exe";
const thumbprint = String(process.env.CASCADE_SIGN_CERT_SHA1 || "").replaceAll(" ", "");
const timestampURL = String(process.env.CASCADE_SIGN_TIMESTAMP_URL || "").trim();
if (!thumbprint || !timestampURL) {
  throw new Error("CASCADE_SIGN_CERT_SHA1 and CASCADE_SIGN_TIMESTAMP_URL are required");
}
for (const file of files) {
  if (!existsSync(file)) throw new Error(`Signing target does not exist: ${file}`);
  run(signtool, ["sign", "/sha1", thumbprint, "/fd", "SHA256", "/tr", timestampURL, "/td", "SHA256", file]);
  run(signtool, ["verify", "/pa", "/all", "/v", file]);
}

function run(command, args) {
  const result = spawnSync(command, args, { stdio: "inherit" });
  if (result.error?.code === "ENOENT") throw new Error(`${command} was not found`);
  if (result.status !== 0) process.exit(result.status ?? 1);
}
