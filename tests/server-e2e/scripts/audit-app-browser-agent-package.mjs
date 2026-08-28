import fs from "node:fs";
import path from "node:path";

function usage() {
  console.error("TEST ONLY: usage: node tests/server-e2e/scripts/audit-app-browser-agent-package.mjs <package.json> [expected-input]");
  process.exit(64);
}

const packagePath = process.argv[2];
const expectedInput = process.argv[3] ?? "贪吃蛇游戏";
if (!packagePath) usage();

const absolutePath = path.resolve(packagePath);
const raw = fs.readFileSync(absolutePath, "utf8");
const pkg = JSON.parse(raw);
const bundle = pkg.executable_script_bundle ?? {};
const outline = bundle.script_outline ?? {};
const stages = Array.isArray(outline.stages) ? outline.stages : [];
const interactions = [];

for (const stage of stages) {
  for (const interaction of Array.isArray(stage.interactions) ? stage.interactions : []) {
    interactions.push({
      node_id: stage.node_id ?? "",
      stage_id: stage.stage_id ?? stage.id ?? "",
      stage_kind: stage.stage_kind ?? "",
      objective: stage.objective ?? "",
      kind: interaction.kind ?? "",
      selector: interaction.target?.selector ?? "",
      selector_alternatives: (interaction.target?.selector_alternatives ?? []).map((item) => item?.value ?? "").filter(Boolean),
      label: interaction.target?.label ?? interaction.target?.text ?? "",
      value: interaction.value ?? interaction.input_value ?? interaction.input_data ?? "",
      input_ref: interaction.input_ref ?? "",
      secret_ref_present: Boolean(interaction.secret_ref),
    });
  }
}

const normalized = (value) => String(value ?? "").trim();
const selectorMatches = (interaction, marker) =>
  interaction.selector.includes(marker) || interaction.selector_alternatives.some((selector) => selector.includes(marker));

const projectIdeaFills = interactions.filter((interaction) =>
  interaction.kind === "fill" &&
  (selectorMatches(interaction, "input-project-idea") || /project idea|snake|贪吃蛇|今天你想做什么/i.test(interaction.label))
);
const exactProjectIdeaFills = projectIdeaFills.filter((interaction) => normalized(interaction.value) === normalized(expectedInput));
const buildClicks = interactions.filter((interaction) =>
  interaction.kind === "click" &&
  (selectorMatches(interaction, "button-create-project") || /build|构建/i.test(interaction.label))
);

const checks = {
  runtime_browser_agent_outline_v1: bundle.script_manifest?.runtime === "browser-agent-outline-v1",
  package_id_present: Boolean(pkg.package_id),
  bundle_hash_present: Boolean(bundle.reproducibility?.bundle_hash_sha256),
  plan_hash_present: Boolean(bundle.reproducibility?.plan_hash_sha256),
  stages_present: stages.length > 0,
  project_idea_fill_present: projectIdeaFills.length > 0,
  project_idea_exact_value_present: exactProjectIdeaFills.length > 0,
  build_click_present: buildClicks.length > 0,
};
const readyForServerExecution = Object.values(checks).every(Boolean);

const report = {
  schema_version: "cascade.app_package_audit.v1",
  package_path: absolutePath,
  package_id: pkg.package_id ?? "",
  package_digest_sha256: pkg.reproducibility?.script_hash_sha256 ?? "",
  bundle_hash_sha256: bundle.reproducibility?.bundle_hash_sha256 ?? "",
  plan_hash_sha256: bundle.reproducibility?.plan_hash_sha256 ?? "",
  confidence_readiness: pkg.confidence_summary?.readiness ?? "",
  expected_input: expectedInput,
  stage_count: stages.length,
  checks,
  ready_for_server_execution: readyForServerExecution,
  project_idea_fill_candidates: projectIdeaFills,
  build_click_candidates: buildClicks,
  interactions,
};

console.log(JSON.stringify(report, null, 2));
process.exit(readyForServerExecution ? 0 : 2);
