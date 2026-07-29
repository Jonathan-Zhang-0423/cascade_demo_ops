// Prepares a local-only compatibility copy of an App package. It deliberately
// changes only stage semantics newly required by the Server protocol.
import { createHash, randomBytes } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';

const inputPath = process.argv[2] ?? 'C:\\Users\\15193\\Desktop\\测试包\\client_execution_package.json';
const outputPath = process.argv[3] ?? path.resolve('artifacts/local-package-repair/browser-agent-outline-v1-repaired-upload.json');

const semanticsByNode = new Map([
  ['start', ['session_setup', 'unauthenticated']],
  ['verified_intent_login_observe', ['session_setup', 'workspace']],
  ['verified_intent_new_project', ['business_action', 'creation_flow']],
  ['verified_intent_project_name', ['business_input', 'creation_flow']],
  ['verified_intent_build_mode', ['mode_selection', 'creation_flow']],
  ['verified_intent_start_agent_build', ['business_submit', 'build_running']],
  ['verified_intent_agent_build_wait', ['observe_progress', 'build_running']],
  ['close', ['final_observe', 'project_detail']],
]);

// These arrays mirror the Go struct declaration order used by json.Marshal.
const approvalStageKeys = [
  'id', 'order', 'node_id', 'business_stage_id', 'stage_kind', 'route_state',
  'title', 'objective', 'business_intent', 'duration_ms', 'entry_route',
  'target_route', 'target_route_template', 'expected_route_after_action',
  'runtime_route_verification_required', 'candidate_routes', 'target_url',
  'component_refs', 'api_refs', 'style_refs', 'data_model_refs', 'input_content',
  'interaction', 'target_contract', 'success_state', 'wait_conditions',
  'capture_points', 'capture_plan', 'risk_notes', 'investigation_question_refs',
  'evidence_refs', 'confidence',
];
const outlineStageKeys = [
  'id', 'stage_id', 'order', 'node_id', 'business_stage_id', 'stage_kind',
  'route_state', 'objective', 'entry_route', 'route', 'target_route_template',
  'expected_route_after_action', 'runtime_route_verification_required',
  'candidate_routes', 'url', 'components', 'interactions', 'target_contract',
  'wait_conditions', 'capture_points', 'capture_plan', 'success_state',
  'duration_ms', 'can_modify', 'must_preserve', 'investigation_question_refs',
  'evidence_refs', 'confidence',
];
const interactionKeys = [
  'kind', 'target', 'value', 'input_ref', 'secret_ref', 'parameters', 'wait_until',
  'wait_conditions', 'non_destructive', 'selector_policy', 'evidence_refs',
];

function orderObject(source, orderedKeys) {
  const result = {};
  for (const key of orderedKeys) {
    if (Object.prototype.hasOwnProperty.call(source, key)) result[key] = source[key];
  }
  for (const [key, value] of Object.entries(source)) {
    if (!Object.prototype.hasOwnProperty.call(result, key)) result[key] = value;
  }
  return result;
}

// encoding/json escapes these characters by default; Node's JSON.stringify
// does not. Preserve Go's byte-level canonical JSON behavior for SHA-256.
function goCanonicalJson(value) {
  return JSON.stringify(value)
    .replaceAll('<', '\\u003c')
    .replaceAll('>', '\\u003e')
    .replaceAll('&', '\\u0026')
    .replaceAll('\u2028', '\\u2028')
    .replaceAll('\u2029', '\\u2029');
}

function digest(value) {
  return createHash('sha256').update(goCanonicalJson(value), 'utf8').digest('hex');
}

function repairStage(stage, keys) {
  const semantics = semanticsByNode.get(stage.node_id);
  if (!semantics) throw new Error(`No approved semantic mapping for node ${JSON.stringify(stage.node_id)}.`);
  const repaired = { ...stage, stage_kind: semantics[0], route_state: semantics[1] };
  return orderObject(repaired, keys);
}

function markApprovedNonDestructive(stage) {
  // The App target contract already marks these actions non-destructive. The
  // legacy export omitted the matching interaction flag required by Runtime.
  if (stage?.target_contract?.destructive === false && stage?.interaction && stage.interaction.non_destructive !== true) {
    stage.interaction.non_destructive = true;
  }
  if (stage?.interaction) stage.interaction = orderObject(stage.interaction, interactionKeys);
  return stage;
}

function markOutlineApprovedNonDestructive(stage) {
  if (stage?.target_contract?.destructive === false && Array.isArray(stage.interactions)) {
    stage.interactions = stage.interactions.map((interaction) => orderObject({ ...interaction, non_destructive: true }, interactionKeys));
  }
  return stage;
}

const raw = (await readFile(inputPath, 'utf8')).replace(/^\uFEFF/, '');
const pkg = JSON.parse(raw);
const bundle = pkg.executable_script_bundle;
if (!bundle?.stage_approval_plan?.stages || !bundle?.script_outline?.stages) {
  throw new Error('Expected executable_script_bundle.stage_approval_plan and script_outline.');
}
if (bundle.stage_approval_plan.stages.length !== semanticsByNode.size || bundle.script_outline.stages.length !== semanticsByNode.size) {
  throw new Error('Refusing to repair: the package stage count does not match the approved local mapping.');
}

bundle.stage_approval_plan.stages = bundle.stage_approval_plan.stages
  .map(markApprovedNonDestructive)
  .map((stage) => repairStage(stage, approvalStageKeys));
bundle.script_outline.stages = bundle.script_outline.stages
  .map(markOutlineApprovedNonDestructive)
  .map((stage) => repairStage(stage, outlineStageKeys));
bundle.reproducibility.stage_plan_hash_sha256 = digest(bundle.stage_approval_plan);
bundle.reproducibility.outline_hash_sha256 = digest(bundle.script_outline);

// ComputeBundleHash copies the bundle, blanks this field, and omits validation
// and repair_lineage before Go canonical JSON serialization.
const bundleForHash = structuredClone(bundle);
// Go tags bundle_hash_sha256 with omitempty, so an empty value is omitted
// from canonical JSON rather than serialized as an empty string.
delete bundleForHash.reproducibility.bundle_hash_sha256;
delete bundleForHash.validation;
delete bundleForHash.repair_lineage;
bundle.reproducibility.bundle_hash_sha256 = digest(bundleForHash);

const payloadDigest = digest(pkg);
const canonicalPayload = goCanonicalJson(pkg);
const seed = randomBytes(12).toString('hex');
const payloadRef = {
  kind: 'inline',
  inline_ciphertext: 'dev-plaintext-payload',
  mime_type: 'application/json',
  sha256: payloadDigest,
  size_bytes: Buffer.byteLength(canonicalPayload),
  encrypted: true,
  sensitive: true,
  compression_alg: 'none',
  dev_plaintext: true,
};
const now = new Date();
const envelope = {
  envelope_id: `env_local_outline_repair_${seed}`,
  org_id: pkg.org_id,
  project_id: pkg.project_id,
  package_kind: 'client_execution',
  schema_version: 'demoops.exchange_envelope.v1',
  payload_schema_version: 'demoops.client_execution_package.v1',
  idempotency_key: `idem_local_outline_repair_${seed}`,
  created_at: now.toISOString(),
  expires_at: new Date(now.getTime() + 30 * 60 * 1000).toISOString(),
  producer: {
    app_version: 'local-package-repair',
    install_id: 'desktop-dev-install',
    runtime_profile: 'local-browser-agent-test',
  },
  crypto: {
    crypto_suite: 'aes-256-gcm',
    key_wrapping_mode: 'server_public_key',
    server_key_id: 'local-dev/server-public-key',
    key_encryption_alg: 'aes-256-gcm-dev-inline',
    content_encryption_alg: 'aes-256-gcm',
    compression_alg: 'none',
    payload_digest_alg: 'sha256',
    payload_digest_sha256: payloadDigest,
    ciphertext_digest_sha256: payloadDigest,
    signature_alg: 'dev-static',
    signature_key_id: 'desktop-dev-install',
    signature: 'desktop-dev-token-compatible-signature',
    nonce: `nonce_local_outline_repair_${seed}`,
    encrypted_content_key: 'wrapped-for-local-dev/server-public-key',
  },
  payload_ref: payloadRef,
  policy: {
    replay_protection: true,
    max_execution_window_sec: 900,
    delete_payload_after_run: true,
    human_approval_required: true,
    structure_summary_only: true,
    required_ip_allowlist_ack: false,
  },
};

await mkdir(path.dirname(outputPath), { recursive: true });
await writeFile(outputPath, `${JSON.stringify({ envelope, payload_ref: payloadRef, payload: pkg }, null, 2)}\n`, { encoding: 'utf8', mode: 0o600 });
console.log(`Prepared local-only repair: ${outputPath}`);
console.log(`Package: ${pkg.package_id}; repaired stages: ${semanticsByNode.size}; payload SHA-256: ${payloadDigest}`);
