import { readFileSync, readdirSync, statSync } from "node:fs";
import { extname, resolve } from "node:path";

const roots = [
  resolve("backend", "internal", "model", "interaction_contract.go"),
	resolve("backend", "internal", "model", "browser_agent_runtime.go"),
	resolve("backend", "internal", "agents", "script_packager.go"),
	resolve("backend", "internal", "app", "browser_agent_stage_plan.go"),
	resolve("backend", "internal", "app", "browser_agent_event_log.go"),
  resolve("backend", "internal", "finalfilm"),
  resolve("video-worker", "src", "browser-agent-runtime.ts"),
  resolve("skills", "final-film"),
];
const forbidden = ["cascadeai.cn", "tetris", "俄罗斯方块", "dialog-new-project", "preview-iframe", "card-project-"];
const files = [];
for (const root of roots) collect(root, files);
for (const file of files) {
  if (/(_test\.go|\.test\.ts)$/i.test(file)) continue;
  if (![".go", ".ts", ".md", ".json"].includes(extname(file).toLowerCase())) continue;
  const content = readFileSync(file, "utf8").toLowerCase();
  for (const token of forbidden) if (content.includes(token)) throw new Error(`Site-neutral core contains forbidden token ${token}: ${file}`);
}
console.log(`Validated ${files.length} site-neutral core and Director skill files.`);

function collect(path, output) {
  const info = statSync(path);
  if (info.isFile()) { output.push(path); return; }
  for (const entry of readdirSync(path, { withFileTypes: true })) collect(resolve(path, entry.name), output);
}
