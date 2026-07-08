import readline from "node:readline";
import { record } from "./recorder.js";
import { render } from "./renderer.js";

type JsonRpcRequest = {
  jsonrpc: "2.0";
  id: number | string;
  method: "record" | "render";
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
  if (request.method === "record") {
    return record(request.params as never);
  }
  if (request.method === "render") {
    return render(request.params as never);
  }
  throw new Error(`unknown method: ${request.method}`);
}
