import { createHash, createPrivateKey, createPublicKey, sign } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { basename, resolve } from "node:path";

const channel = process.env.CASCADE_RELEASE_CHANNEL || "internal";
if (!new Set(["internal", "beta", "stable"]).has(channel)) throw new Error(`Unsupported update channel: ${channel}`);
const artifactArg = process.argv[2];
if (!artifactArg) throw new Error("Usage: node scripts/generate-update-manifest.mjs <artifact>");
const artifact = resolve(artifactArg);
if (!existsSync(artifact)) throw new Error(`Update artifact is missing: ${artifact}`);
if (channel !== "internal" && !artifact.toLowerCase().endsWith(".exe")) {
  throw new Error("beta/stable update manifests must point to the signed Inno Setup .exe");
}
const pkg = JSON.parse(readFileSync(resolve("package.json"), "utf8"));
const baseURL = String(process.env.DEMOOPS_UPDATE_BASE_URL || "").replace(/\/$/, "");
if (!baseURL.startsWith("https://")) throw new Error("DEMOOPS_UPDATE_BASE_URL must be a DemoOps HTTPS release origin");
const privateKeyPath = String(process.env.DEMOOPS_UPDATE_SIGNING_KEY_PATH || "").trim();
if (!privateKeyPath || !existsSync(privateKeyPath)) throw new Error("DEMOOPS_UPDATE_SIGNING_KEY_PATH is required");

const bytes = readFileSync(artifact);
const manifest = {
  schema_version: "demoops.desktop_update.v1",
  app: "Cascade DemoOps",
  channel,
  version: pkg.version || "0.0.0",
  minimum_version: process.env.DEMOOPS_MINIMUM_APP_VERSION || "0.1.0",
  published_at: new Date().toISOString(),
  artifact: {
    url: `${baseURL}/${channel}/${basename(artifact)}`,
    file_name: basename(artifact),
    size_bytes: statSync(artifact).size,
    sha256: createHash("sha256").update(bytes).digest("hex"),
  },
  release_notes: process.env.DEMOOPS_RELEASE_NOTES || "",
  signature: { algorithm: "Ed25519", key_id: process.env.DEMOOPS_UPDATE_KEY_ID || "release-1" },
};
const canonical = Buffer.from(`${JSON.stringify(manifest)}\n`, "utf8");
const privateKey = createPrivateKey(readFileSync(privateKeyPath));
const signature = sign(null, canonical, privateKey).toString("base64");
const output = resolve("dist", "release", `CascadeDemoOps-${channel}.update.json`);
mkdirSync(resolve(output, ".."), { recursive: true });
writeFileSync(output, `${JSON.stringify(manifest, null, 2)}\n`);
writeFileSync(`${output}.sig`, `${signature}\n`);
if (process.env.DEMOOPS_EXPORT_UPDATE_PUBLIC_KEY === "1") {
  writeFileSync(`${output}.pub.pem`, createPublicKey(privateKey).export({ type: "spki", format: "pem" }));
}
console.log(`Created signed update manifest: ${output}`);
