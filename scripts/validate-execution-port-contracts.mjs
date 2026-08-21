import { readFile, readdir } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const contractRoot = join(repositoryRoot, "contracts", "execution-port", "v1");
const fixtureRoot = join(contractRoot, "fixtures");
const maxEventBytes = 65_536;

const parseJSON = async (path) => JSON.parse(await readFile(path, "utf8"));
const schemaFiles = (await readdir(contractRoot))
  .filter((name) => name.endsWith(".schema.json"))
  .sort();

const ajv = new Ajv2020({ allErrors: true, strict: true, strictRequired: false, allowUnionTypes: true });
addFormats(ajv);
for (const file of schemaFiles) {
  ajv.addSchema(await parseJSON(join(contractRoot, file)));
}

const manifest = await parseJSON(join(fixtureRoot, "manifest.json"));
if (manifest.schema_version !== "demoops.execution_port_fixture_manifest.v1") {
  throw new Error("execution-port fixture manifest version is unsupported");
}

let checked = 0;
for (const fixture of manifest.fixtures) {
  const schema = await parseJSON(join(contractRoot, fixture.schema));
  const validate = ajv.getSchema(schema.$id);
  if (!validate) throw new Error(`schema was not registered: ${fixture.schema}`);
  const value = await parseJSON(join(fixtureRoot, fixture.file));
  const actual = validate(value);
  if (actual !== fixture.valid) {
    const details = ajv.errorsText(validate.errors, { separator: "\n" });
    throw new Error(`${fixture.file} expected valid=${fixture.valid}, received ${actual}\n${details}`);
  }
  checked += 1;
}

const workflowFixture = await parseJSON(join(fixtureRoot, "valid", "workflow-template.json"));
validateWorkflowDAG(workflowFixture);
validateSiteNeutralWorkflow(workflowFixture);

const oversizedEvent = await parseJSON(join(fixtureRoot, "valid", "execution-event.json"));
oversizedEvent.summary = "x".repeat(maxEventBytes);
if (eventWithinLimit(oversizedEvent)) {
  throw new Error("oversized execution event was not rejected");
}
const normalEvent = await parseJSON(join(fixtureRoot, "valid", "execution-event.json"));
if (!eventWithinLimit(normalEvent)) {
  throw new Error("valid execution event exceeds the protocol byte limit");
}
validateEventSequence([
  { ...normalEvent, event_id: "event-sequence-001", sequence: 1 },
  { ...normalEvent, event_id: "event-sequence-002", sequence: 2 },
]);
assertStateTransition("created", "admitted", true);
assertStateTransition("running", "waiting_external", true);
assertStateTransition("waiting_external", "queued", true);
assertStateTransition("succeeded", "running", false);
assertStateTransition("waiting_input", "succeeded", false);

const forbiddenFixture = await parseJSON(join(fixtureRoot, "invalid", "workflow-template-hostname-router.json"));
if (!containsForbiddenRouterKey(forbiddenFixture)) {
  throw new Error("site-specific workflow fixture did not exercise the router-key guard");
}

console.log(`execution-port contracts: ${schemaFiles.length} schemas, ${checked} fixtures, DAG/site-neutral/lifecycle/event guards passed`);

function eventWithinLimit(value) {
  return Buffer.byteLength(JSON.stringify(value), "utf8") <= maxEventBytes;
}

function validateWorkflowDAG(template) {
  const nodes = new Map(template.nodes.map((node) => [node.node_id, node]));
  if (nodes.size !== template.nodes.length) throw new Error("workflow template contains duplicate node_id values");
  const visiting = new Set();
  const visited = new Set();
  const visit = (nodeID) => {
    if (visiting.has(nodeID)) throw new Error(`workflow template contains a dependency cycle at ${nodeID}`);
    if (visited.has(nodeID)) return;
    const node = nodes.get(nodeID);
    if (!node) throw new Error(`workflow template references unknown node ${nodeID}`);
    visiting.add(nodeID);
    for (const dependency of node.depends_on) visit(dependency);
    visiting.delete(nodeID);
    visited.add(nodeID);
  };
  for (const nodeID of nodes.keys()) visit(nodeID);
}

function validateEventSequence(events) {
  const seen = new Set();
  let previous = 0;
  for (const event of events) {
    if (seen.has(event.event_id) || event.sequence !== previous + 1) {
      throw new Error("execution event stream identity or sequence is not contiguous");
    }
    seen.add(event.event_id);
    previous = event.sequence;
  }
}

function assertStateTransition(before, after, expected) {
  const transitions = {
    created: new Set(["admitted", "failed", "canceled", "expired"]),
    admitted: new Set(["queued", "canceled", "expired"]),
    queued: new Set(["running", "canceled", "expired"]),
    running: new Set(["waiting_input", "waiting_external", "succeeded", "failed", "canceled"]),
    waiting_input: new Set(["queued", "canceled", "expired"]),
    waiting_external: new Set(["queued", "canceled", "expired"]),
    succeeded: new Set(),
    failed: new Set(),
    canceled: new Set(),
    expired: new Set(),
  };
  const actual = transitions[before]?.has(after) === true;
  if (actual !== expected) throw new Error(`unexpected lifecycle transition result: ${before} -> ${after}`);
}

function validateSiteNeutralWorkflow(value) {
  if (containsForbiddenRouterKey(value)) {
    throw new Error("workflow template router contains a site-specific key");
  }
}

function containsForbiddenRouterKey(value) {
  const forbidden = new Set(["hostname", "host", "domain", "fixed_route", "route", "selector", "test_id", "product_name"]);
  const walk = (current) => {
    if (Array.isArray(current)) return current.some(walk);
    if (!current || typeof current !== "object") return false;
    return Object.entries(current).some(([key, child]) => forbidden.has(key.toLowerCase()) || walk(child));
  };
  return walk(value);
}
