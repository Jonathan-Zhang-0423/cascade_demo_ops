import readline from "node:readline";
import { record } from "./recorder.js";
import { render } from "./renderer.js";
import { executeScript, validateScript } from "./script-runner.js";
import { verifyInteractions } from "./interaction-verifier.js";
import { probeMediaFile } from "./media-probe.js";
import { validateEditPlan } from "./renderer.js";

type JsonRpcRequest = {
  jsonrpc: "2.0";
  id: number | string;
  method: "health" | "record" | "render" | "probe_media" | "validate_edit_plan" | "validate_script" | "execute_script" | "verify_interactions";
  params?: unknown;
};

const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });

rl.on("line", async (line) => {
  try {
    const request = JSON.parse(line) as JsonRpcRequest;
    const result = await dispatch(request);
    process.stdout.write(JSON.stringify({ jsonrpc: "2.0", id: request.id, result }) + "\n");
  } catch (error) {
    process.stdout.write(
      JSON.stringify({
        jsonrpc: "2.0",
        id: null,
        error: { code: -32000, message: error instanceof Error ? error.message : String(error) },
      }) + "\n",
    );
  }
});

async function dispatch(request: JsonRpcRequest): Promise<unknown> {
  if (request.jsonrpc !== "2.0") {
    throw new Error("invalid JSON-RPC version");
  }
  if (request.method === "health") {
    return { ok: true, service: "video-worker" };
  }
  if (request.method === "record") {
    return record(request.params as never);
  }
  if (request.method === "render") {
    return render(request.params as never);
  }
  if (request.method === "probe_media") {
    return probeMediaFile(request.params as never);
  }
  if (request.method === "validate_edit_plan") {
    return validateEditPlan(request.params as never);
  }
  if (request.method === "validate_script") {
    return validateScript(request.params as never);
  }
  if (request.method === "execute_script") {
    return executeScript(request.params as never);
  }
  if (request.method === "verify_interactions") {
    return verifyInteractions(request.params as never);
  }
  throw new Error(`unknown method: ${request.method}`);
}
