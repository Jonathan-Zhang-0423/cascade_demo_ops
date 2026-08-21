import { existsSync, readFileSync, readdirSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve("skills", "final-film");
const expected = new Set([
  "director-evidence-story",
  "director-generated-shots",
  "director-timeline-compose",
  "director-quality-gate",
  "final-film-director-harness",
]);
const seen = new Set();
for (const entry of readdirSync(root, { withFileTypes: true })) {
  if (!entry.isDirectory()) continue;
  const skillID = entry.name;
  if (!expected.has(skillID)) throw new Error(`Unexpected final-film skill: ${skillID}`);
  const directory = resolve(root, skillID);
  const skillPath = resolve(directory, "SKILL.md");
  const yamlPath = resolve(directory, "agents", "openai.yaml");
  const runtimePath = resolve(directory, "agents", "runtime.json");
  for (const path of [skillPath, yamlPath, runtimePath]) if (!existsSync(path)) throw new Error(`Missing ${path}`);
  const skill = readFileSync(skillPath, "utf8");
  if (!skill.startsWith(`---\nname: ${skillID}\n`) && !skill.startsWith(`---\r\nname: ${skillID}\r\n`)) throw new Error(`${skillID} has invalid frontmatter`);
  if (/\bTODO\b/.test(skill)) throw new Error(`${skillID} still contains TODO content`);
  const runtime = JSON.parse(readFileSync(runtimePath, "utf8"));
  if (runtime.schema_version !== "demoops.director_skill_runtime.v1" || runtime.skill_id !== skillID || runtime.version !== "1.1.0") throw new Error(`${skillID} runtime identity is invalid`);
  if (!Array.isArray(runtime.inputs) || runtime.inputs.length === 0 || !Array.isArray(runtime.outputs) || runtime.outputs.length === 0) throw new Error(`${skillID} runtime IO is incomplete`);
  const machineText = JSON.stringify(runtime).toLowerCase();
  for (const forbidden of ["cascadeai.cn", "2048", "tetris", "俄罗斯方块", "data-testid", "preview-iframe"]) if (machineText.includes(forbidden)) throw new Error(`${skillID} runtime contains site-specific token ${forbidden}`);
  for (const reference of runtime.references ?? []) if (!existsSync(resolve(directory, reference))) throw new Error(`${skillID} missing runtime reference ${reference}`);
  seen.add(skillID);
}
for (const skillID of expected) if (!seen.has(skillID)) throw new Error(`Missing final-film skill ${skillID}`);
console.log(`Validated ${seen.size} versioned final-film Director skills.`);
