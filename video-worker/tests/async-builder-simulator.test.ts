import { createServer, type Server } from "node:http";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";

import { captureBrowserAgentTemporalObservation, closeBrowserAgentSession, openBrowserAgentSession } from "../src/browser-agent-runtime.js";

const servers: Server[] = [];
const roots: string[] = [];

afterEach(async () => {
  await Promise.all(servers.splice(0).map((server) => new Promise<void>((resolve) => server.close(() => resolve()))));
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

describe("site-neutral asynchronous builder simulator", () => {
  for (const variant of ["dom", "iframe", "canvas"] as const) {
    it(`detects material lifecycle change for ${variant} result surfaces`, async () => {
      const { url } = await serveVariant(variant);
      const root = await mkdtemp(join(tmpdir(), "demoops-async-builder-"));
      roots.push(root);
      const session = await openBrowserAgentSession({
        output_dir: root, initial_url: url, allowed_domains: ["127.0.0.1"], allowed_origins: [new URL(url).origin], allowed_routes: ["/workspace"],
        record_trace: false, recording_sensitive: false,
      });
      try {
        const first = await captureBrowserAgentTemporalObservation({ session_id: session.session_id, scope_id: `leg-${variant}`, sequence: 1, phase: "request_submitted", reason: "submitted" });
        await new Promise((resolve) => setTimeout(resolve, 1_400));
        const second = await captureBrowserAgentTemporalObservation({ session_id: session.session_id, scope_id: `leg-${variant}`, sequence: 2, phase: "execution_active", reason: "material_change" });
        expect(first.artifact.uri).toMatch(/^file:/);
        expect(JSON.stringify(first)).not.toContain("data:image");
        expect(second.material_change).toBe(true);
        expect(second.changed_channels).toContain("visual");
        expect(second.changed_channels.some((channel) => ["dom", "aria", "frame"].includes(channel))).toBe(true);
      } finally {
        await closeBrowserAgentSession({ session_id: session.session_id });
      }
    }, 20_000);
  }

  it("preserves recording segments across a restarted browser session", async () => {
    const { url } = await serveVariant("dom");
    const root = await mkdtemp(join(tmpdir(), "demoops-segment-resume-"));
    roots.push(root);
    const open = (sessionID: string) => openBrowserAgentSession({ session_id: sessionID, output_dir: root, initial_url: url, allowed_domains: ["127.0.0.1"], allowed_origins: [new URL(url).origin], allowed_routes: ["/workspace"], browser: { record_video: true }, record_trace: false });
    const first = await open("segment-run-one");
    const firstClose = await closeBrowserAgentSession({ session_id: first.session_id });
    const second = await open("segment-run-two");
    const secondClose = await closeBrowserAgentSession({ session_id: second.session_id });
    expect(firstClose.recording_segment_paths).toHaveLength(1);
    expect(secondClose.recording_segment_paths).toHaveLength(2);
    expect(secondClose.recording_segment_manifest_path).toMatch(/recording-segments\.json$/);
    expect(secondClose.artifacts.filter((artifact) => artifact.kind === "recording_segment")).toHaveLength(2);
  }, 20_000);
});

async function serveVariant(variant: "dom" | "iframe" | "canvas"): Promise<{ url: string }> {
  const server = createServer((_request, response) => {
    response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
    response.end(pageForVariant(variant));
  });
  servers.push(server);
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("simulator address unavailable");
  return { url: `http://127.0.0.1:${address.port}/workspace` };
}

function pageForVariant(variant: "dom" | "iframe" | "canvas"): string {
  const common = `<style>body{margin:0;background:#071428;color:white;font:24px sans-serif}main{padding:40px}section{height:700px;border:4px solid #30d5ff;border-radius:24px;padding:24px}</style>`;
  if (variant === "dom") return `<!doctype html><html><head>${common}</head><body><main><section id="surface" role="status" aria-busy="true">Preparing</section></main><script>setTimeout(()=>{surface.textContent='Interactive result ready';surface.setAttribute('aria-busy','false');surface.setAttribute('aria-label','Result ready')},1200)</script></body></html>`;
  if (variant === "iframe") return `<!doctype html><html><head>${common}</head><body><main><iframe title="Result surface" style="width:90%;height:700px"></iframe></main><script>const frame=document.querySelector('iframe');frame.srcdoc='<body style="background:#17213d;color:white;font:32px sans-serif">Preparing</body>';setTimeout(()=>{frame.srcdoc='<body role="main" aria-label="Ready result" style="background:#123d51;color:white;font:32px sans-serif">Interactive result ready</body>'},1200)</script></body></html>`;
  return `<!doctype html><html><head>${common}</head><body><main><canvas width="1200" height="700" aria-label="Interactive result surface"></canvas></main><script>const c=document.querySelector('canvas'),x=c.getContext('2d');x.fillStyle='#17213d';x.fillRect(0,0,c.width,c.height);setTimeout(()=>{x.fillStyle='#14d9c4';x.fillRect(80,80,900,500);c.setAttribute('aria-label','Ready interactive result')},1200)</script></body></html>`;
}
