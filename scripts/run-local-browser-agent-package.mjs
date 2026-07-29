// Uploads a previously preflighted local package and collects its Server-side
// runtime result. Credentials are never read, written, or printed here.
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';

const baseUrl = process.env.CASCADE_DEV_EXCHANGE_URL ?? 'http://127.0.0.1:4317';
const token = process.env.CASCADE_DEV_EXCHANGE_TOKEN;
const inputPath = process.argv[2] ?? path.resolve('artifacts/local-package-repair/browser-agent-outline-v1-repaired-upload.json');
const outputDir = process.argv[3] ?? path.resolve('artifacts/local-package-repair/runtime-result');
if (!token) throw new Error('CASCADE_DEV_EXCHANGE_TOKEN is required.');

const body = JSON.parse(await readFile(inputPath, 'utf8'));
const headers = {
  Authorization: `Bearer ${token}`,
  'X-Cascade-Org-ID': body.payload.org_id,
  'Content-Type': 'application/json',
};

async function request(method, pathname, value) {
  const response = await fetch(`${baseUrl}${pathname}`, {
    method,
    headers,
    body: value === undefined ? undefined : JSON.stringify(value),
  });
  const text = await response.text();
  let parsed;
  try { parsed = text ? JSON.parse(text) : {}; } catch { parsed = { raw: text }; }
  if (!response.ok) throw new Error(`${method} ${pathname} failed (${response.status}): ${JSON.stringify(parsed)}`);
  return parsed;
}

const init = await request('POST', '/aigc/v1/execution-packages/init', {
  org_id: body.payload.org_id,
  project_id: body.payload.project_id,
  package_kind: 'client_execution',
  producer: body.envelope.producer,
});
body.upload_id = init.upload_id;
const uploaded = await request('POST', '/aigc/v1/execution-packages', body);
const id = uploaded.exchange_package_id;
console.log(`Accepted package: ${id}; starting Browser Agent.`);
await request('POST', `/aigc/v1/dev/execution-packages/${encodeURIComponent(id)}/run`, {});

let status;
const deadline = Date.now() + 6 * 60 * 1000;
while (Date.now() < deadline) {
  await new Promise((resolve) => setTimeout(resolve, 3000));
  status = await request('GET', `/aigc/v1/execution-packages/${encodeURIComponent(id)}/status`);
  console.log(`Status: ${status.status}; stage: ${status.stage ?? 'unknown'}; progress: ${status.progress_percent ?? 0}%`);
  if (['completed', 'failed', 'canceled', 'expired'].includes(status.status)) break;
}
if (!status || !['completed', 'failed', 'canceled', 'expired'].includes(status.status)) {
  throw new Error(`Timed out waiting for ${id}.`);
}
const debug = await request('GET', `/aigc/v1/dev/execution-packages/${encodeURIComponent(id)}/debug`);
await mkdir(outputDir, { recursive: true });
await writeFile(path.join(outputDir, `${id}.status.json`), `${JSON.stringify(status, null, 2)}\n`);
await writeFile(path.join(outputDir, `${id}.debug.json`), `${JSON.stringify(debug, null, 2)}\n`);
console.log(`Terminal status: ${status.status}; diagnostics saved in ${outputDir}`);
