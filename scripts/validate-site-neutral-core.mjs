import { readFileSync, readdirSync, statSync } from "node:fs";
import { extname, resolve } from "node:path";

const roots = [
  resolve("backend", "internal", "model", "interaction_contract.go"),
	resolve("backend", "internal", "model", "browser_agent_runtime.go"),
	resolve("backend", "internal", "agents", "business_stage_planner.go"),
	resolve("backend", "internal", "agents", "code_reader.go"),
	resolve("backend", "internal", "agents", "graph_builder.go"),
	resolve("backend", "internal", "agents", "page_interaction_verifier.go"),
	resolve("backend", "internal", "agents", "script_packager.go"),
	resolve("backend", "internal", "app", "direct_reunderstanding.go"),
	resolve("backend", "internal", "app", "browser_agent_stage_plan.go"),
	resolve("backend", "internal", "app", "browser_agent_event_log.go"),
	resolve("backend", "internal", "app", "experiment_execution_adapter.go"),
	resolve("backend", "internal", "app", "experiment_service.go"),
	resolve("backend", "internal", "driver", "browser_agent.go"),
  resolve("backend", "internal", "finalfilm"),
  resolve("backend", "internal", "experiment"),
  resolve("video-worker", "src", "browser-agent-runtime.ts"),
  resolve("video-worker", "src", "temporal-visual-observer.ts"),
  resolve("video-worker", "src", "interaction-verifier.ts"),
  resolve("skills", "final-film"),
  resolve("contracts", "task-packs", "v1", "async-product-build-demo-v1.json"),
];
const forbidden = [
  "cascadeai.cn", "2048", "tetris", "俄罗斯方块", "dialog-new-project", "preview-iframe", "card-project-",
  "build-result-card", "playable_preview", "verify_playable_controls", "/project/:id",
];
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
