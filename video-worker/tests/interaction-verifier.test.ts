import { afterEach, describe, expect, it } from "vitest";
import { createServer, type Server } from "node:http";
import { once } from "node:events";
import { verifyInteractions } from "../src/interaction-verifier.js";

let server: Server | undefined;

afterEach(async () => {
  if (!server) return;
  server.close();
  await once(server, "close");
  server = undefined;
});

describe("interaction verifier login state machine", () => {
  it("handles a login-method chooser before the credential form", async () => {
    server = createServer((request, response) => {
      response.setHeader("content-type", "text/html; charset=utf-8");
      if (request.url === "/workspace") {
        response.end("<!doctype html><title>Workspace</title><button data-testid='new-project'>New Project</button>");
        return;
      }
      response.end(`<!doctype html>
        <title>Login</title>
        <button id="email-login" type="button" onclick="document.querySelector('#form').hidden=false">邮箱登录</button>
        <form id="form" hidden onsubmit="event.preventDefault(); location.href='/workspace'">
          <input type="email" autocomplete="username" />
          <input type="password" autocomplete="current-password" />
          <button type="submit">登录</button>
        </form>`);
    });
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("fixture server did not bind");

    const result = await verifyInteractions({
      product_url: `http://127.0.0.1:${address.port}/login`,
      allowed_domains: ["127.0.0.1"],
      timeout_ms: 15_000,
      demo_username: "demo@example.test",
      demo_password: "fixture-only",
      intent_goals: [{ id: "new-project", label: "New Project", kind: "click", keywords: ["new", "project"], required: true, business: true }],
    });

    expect(result.ok).toBe(true);
    expect(result.diagnostics?.login_status).toBe("submitted_navigation_observed");
    expect(result.diagnostics?.final_url).toContain("/workspace");
    expect(result.diagnostics?.login_transitions).toContain("login_form_ready");
    expect(result.diagnostics?.login_transitions).toContain("authenticated_navigation_observed");
  }, 30_000);
});
