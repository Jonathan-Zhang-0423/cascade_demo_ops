import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const root = resolve("contracts", "task-packs", "v1");
const executionRoot = resolve("contracts", "execution-port", "v1");
const schemaNames = [
  "product-spec.schema.json",
  "build-observation-plan.schema.json",
  "interaction-evidence-plan.schema.json",
  "experiment-run-report.schema.json",
  "temporal-visual-observation.schema.json",
];
const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);
ajv.addSchema(readJSON(resolve(executionRoot, "common.schema.json")));
const workflowTemplate = readJSON(resolve(executionRoot, "workflow-template.schema.json"));
ajv.addSchema(workflowTemplate);
for (const name of schemaNames) ajv.addSchema(readJSON(resolve(root, name)));

const manifestPath = resolve(root, "fixtures", "manifest.json");
const manifest = readJSON(manifestPath);
for (const item of manifest.valid) validateFixture(item, true);
for (const item of manifest.invalid) validateFixture(item, false);
validateExperimentDefinition(readJSON(resolve("experiments", "2048-v2", "definition.json")));
validateNoHarnessLeak(readJSON(resolve("experiments", "2048-v2", "product-spec.json")));
const taskPack = readJSON(resolve(root, "async-product-build-demo-v1.json"));
const validateTaskPack = ajv.getSchema(workflowTemplate.$id);
if (!validateTaskPack(taskPack)) throw new Error(`async-product-build-demo-v1 is invalid: ${ajv.errorsText(validateTaskPack.errors)}`);
validateSiteNeutralTaskPack(taskPack);

console.log(`task-pack contracts: ${schemaNames.length} schemas, ${manifest.valid.length + manifest.invalid.length} fixtures, experiment definition and prompt-isolation guards passed`);

function validateFixture(item, expectedValid) {
  const schema = readJSON(resolve(root, item.schema));
  const validate = ajv.getSchema(schema.$id);
  const fixture = readJSON(resolve(dirname(manifestPath), item.fixture));
  const valid = validate(fixture);
  if (valid !== expectedValid) throw new Error(`${item.fixture} expected valid=${expectedValid}: ${ajv.errorsText(validate.errors)}`);
}

function validateExperimentDefinition(value) {
  const expected = {
    schema_version: "demoops.experiment_definition.v1",
    workflow_template_id: "async-product-build-demo-v1",
    recovery_injection_phase: "once_effect_committed",
  };
  for (const [key, wanted] of Object.entries(expected)) if (value[key] !== wanted) throw new Error(`experiment definition ${key} must be ${wanted}`);
  if (value.authorization_budget?.target_submissions !== 2 || value.authorization_budget?.final_film_jobs !== 1 || value.authorization_budget?.provider_calls !== 8 || value.authorization_budget?.visual_calls_per_run !== 12) throw new Error("experiment definition budget is not frozen");
  if (value.main_target_duration_ms?.min !== 100000 || value.main_target_duration_ms?.max !== 110000) throw new Error("experiment target duration must be 100-110 seconds");
}

function validateNoHarnessLeak(value) {
  const targetText = [value.title, value.objective, ...(value.requirements ?? []).map((item) => item.statement), ...(value.interaction_requirements ?? []).map((item) => item.statement), ...(value.responsive_requirements ?? [])].join(" ").toLowerCase();
  for (const forbidden of ["demoops", "seedance", "ffmpeg", "h3", "视觉轮询", "审计", "selector", "data-testid"]) if (targetText.includes(forbidden)) throw new Error(`product target content leaks harness term ${forbidden}`);
}

function validateSiteNeutralTaskPack(value) {
  const text = JSON.stringify(value).toLowerCase();
  for (const forbidden of ["hostname", "selector", "fixed_route", "cascadeai.cn", "2048", "tetris", "俄罗斯方块", "/project/"]) if (text.includes(forbidden)) throw new Error(`task pack contains site-specific routing input ${forbidden}`);
}

function readJSON(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}
