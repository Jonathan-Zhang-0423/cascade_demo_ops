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
  it("uses the login entry instead of filling a homepage waitlist email field", async () => {
    server = createServer((request, response) => {
      response.setHeader("content-type", "text/html; charset=utf-8");
      if (request.url === "/workspace") {
        response.end("<!doctype html><title>Workspace</title><button data-testid='new-project'>New Project</button>");
        return;
      }
      if (request.url === "/login") {
        response.end(`<!doctype html><title>Login</title>
          <form onsubmit="event.preventDefault(); location.href='/workspace'">
            <input type="email" autocomplete="username" />
            <input type="password" autocomplete="current-password" />
            <button type="submit">Sign in</button>
          </form>`);
        return;
      }
      response.end(`<!doctype html><title>Marketing</title>
        <input data-testid="input-email" type="email" placeholder="Enter your email address" />
        <button type="button">Join waitlist</button>
        <a href="/login">Try it now</a>`);
    });
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("fixture server did not bind");

    const result = await verifyInteractions({
      product_url: `http://127.0.0.1:${address.port}/`, allowed_domains: ["127.0.0.1"], timeout_ms: 15_000,
      demo_username: "demo@example.test", demo_password: "fixture-only",
      intent_goals: [{ id: "new-project", label: "New Project", kind: "click", keywords: ["new", "project"], required: true, business: true }],
    });

    expect(result.diagnostics?.login_status).toBe("submitted_navigation_observed");
    expect(result.diagnostics?.login_transitions).toContain("login_trigger_clicked:1");
    expect(result.diagnostics?.final_url).toContain("/workspace");
  }, 30_000);

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

  it("handles an email-first login before the password step", async () => {
    server = createServer((request, response) => {
      response.setHeader("content-type", "text/html; charset=utf-8");
      if (request.url === "/workspace") {
        response.end("<!doctype html><title>Workspace</title><button data-testid='new-project'>New Project</button>");
        return;
      }
      response.end(`<!doctype html><title>Login</title>
        <form id="email-step" onsubmit="event.preventDefault(); this.hidden=true; document.querySelector('#password-step').hidden=false">
          <input data-testid="input-email" type="email" placeholder="Enter your email address" />
          <button type="submit">Continue</button>
        </form>
        <form id="password-step" hidden onsubmit="event.preventDefault(); location.href='/workspace'">
          <input data-testid="input-password" type="password" autocomplete="current-password" />
          <button type="submit">Sign in</button>
        </form>`);
    });
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("fixture server did not bind");

    const result = await verifyInteractions({
      product_url: `http://127.0.0.1:${address.port}/`, allowed_domains: ["127.0.0.1"], timeout_ms: 15_000,
      demo_username: "demo@example.test", demo_password: "fixture-only",
      intent_goals: [{ id: "new-project", label: "New Project", kind: "click", keywords: ["new", "project"], required: true, business: true }],
    });

    expect(result.diagnostics?.login_status).toBe("submitted_navigation_observed");
    expect(result.diagnostics?.login_transitions).toContain("username_step_submitted");
    expect(result.diagnostics?.final_url).toContain("/workspace");
    expect(result.results.some((item) => item.intent_goal_id === "new-project" && item.status === "verified")).toBe(true);
  }, 30_000);

  it("does not report success when the password form disappears without authenticated evidence", async () => {
    server = createServer((_request, response) => {
      response.setHeader("content-type", "text/html; charset=utf-8");
      response.end(`<!doctype html><title>Login</title>
        <form onsubmit="event.preventDefault(); this.hidden=true; document.querySelector('#error').hidden=false">
          <input type="email" /><input type="password" /><button type="submit">Sign in</button>
        </form><p id="error" hidden>Invalid credentials</p>`);
    });
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("fixture server did not bind");

    const result = await verifyInteractions({
      product_url: `http://127.0.0.1:${address.port}/`, allowed_domains: ["127.0.0.1"], timeout_ms: 15_000,
      demo_username: "demo@example.test", demo_password: "fixture-only",
    });

    expect(result.diagnostics?.login_status).toBe("submitted_still_on_login_url");
    expect(result.diagnostics?.login_transitions).not.toContain("authenticated_navigation_observed");
  }, 30_000);

  it("returns a redacted category for invalid credentials", async () => {
    server = createServer((_request, response) => {
      response.setHeader("content-type", "text/html; charset=utf-8");
      response.end(`<!doctype html><title>Login</title>
        <form onsubmit="event.preventDefault(); document.querySelector('#error').hidden=false">
          <input type="email" /><input type="password" /><button type="submit">Sign in</button>
        </form><p id="error" hidden>Invalid credentials</p>`);
    });
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("fixture server did not bind");

    const result = await verifyInteractions({
      product_url: `http://127.0.0.1:${address.port}/login`, allowed_domains: ["127.0.0.1"], timeout_ms: 15_000,
      demo_username: "demo@example.test", demo_password: "fixture-only",
    });

    expect(result.diagnostics?.login_status).toBe("submitted_invalid_credentials");
    expect(result.diagnostics?.login_transitions).toContain("login_failure:invalid_credentials");
  }, 30_000);

  it("never binds credential goals to forgot-password or back controls", async () => {
    server = createServer((_request, response) => {
      response.setHeader("content-type", "text/html; charset=utf-8");
      response.end(`<!doctype html><title>Login</title>
        <input name="email" placeholder="邮箱" />
        <input name="password" type="password" placeholder="密码" />
        <button type="button">忘记密码？</button>
        <a href="/">返回</a>`);
    });
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("fixture server did not bind");

    const result = await verifyInteractions({
      product_url: `http://127.0.0.1:${address.port}/login`,
      allowed_domains: ["127.0.0.1"],
      intent_goals: [{ id: "login-credentials", label: "邮箱密码登录", kind: "fill", keywords: ["邮箱", "密码"], required: true }],
    });

    const discovered = result.results.filter((item) => item.id?.startsWith("browser_discovered_"));
    expect(discovered.every((item) => item.kind !== "fill" || item.editable === true)).toBe(true);
    expect(discovered.some((item) => /忘记密码|返回/.test(`${item.label} ${item.selector}`))).toBe(false);
    expect(discovered.some((item) => item.intent_goal_id === "login-credentials" && item.kind === "fill")).toBe(true);
  }, 30_000);
});

describe("interaction verifier safe state exploration", () => {
  it("applies only an explicitly authorized project-entry click before rescanning", async () => {
    server = createServer((_request, response) => {
      response.setHeader("content-type", "text/html; charset=utf-8");
      response.end(`<!doctype html><title>Workspace</title>
        <button data-testid="new-project" onclick="document.querySelector('#form').hidden=false">New Project</button>
        <section id="form" hidden><label>Project name<input data-testid="project-name"></label></section>`);
    });
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("fixture server did not bind");
    const productURL = `http://127.0.0.1:${address.port}/workspace`;

    const result = await verifyInteractions({
      product_url: productURL,
      allowed_domains: ["127.0.0.1"],
      timeout_ms: 15_000,
      candidates: [{ id: "project-name", kind: "fill", selector: "[data-testid='project-name']", url: productURL }],
      safe_state_transitions: [{ id: "business_stage_new_project_entry", label: "New Project", kind: "click", selector: "[data-testid='new-project']", url: productURL }],
      intent_goals: [{ id: "project-name", label: "Project name", kind: "fill", keywords: ["project", "name"], required: true, business: true }],
    });

    expect(result.ok).toBe(true);
    expect(result.verification_mode).toBe("playwright_safe_state_scan");
    expect(result.diagnostics?.safe_state_transitions).toContain("applied:business_stage_new_project_entry");
    const projectName = result.results.find((item) => item.id === "project-name");
    expect(projectName?.status).toBe("verified");
    expect(projectName).toMatchObject({ source_kind: "page_scan", observed_role: "textbox", observed_accessible_name: "Project name" });
    expect(projectName?.source_digest).toMatch(/^sha256:[a-f0-9]{64}$/);
    expect(projectName?.evidence_id).toMatch(/^ev_browser_scan_/);
    expect(projectName?.observed_accessible_name).not.toContain("project-name");
  }, 30_000);

  it("binds a component login dialog without a native form to authentication provenance", async () => {
    server = createServer((request, response) => {
      response.setHeader("content-type", "text/html; charset=utf-8");
      if (request.url === "/workspace") {
        response.end("<!doctype html><title>Workspace</title><button data-testid='new-project'>New Project</button>");
        return;
      }
      response.end(`<!doctype html><title>Login</title>
        <div role="dialog" aria-label="Sign in">
          <input name="email" type="email" autocomplete="username" />
          <input name="password" type="password" autocomplete="current-password" />
          <button data-testid="login-submit" type="button" onclick="location.href='/workspace'">Sign in</button>
        </div>`);
    });
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("fixture server did not bind");

    const result = await verifyInteractions({
      product_url: `http://127.0.0.1:${address.port}/login`, allowed_domains: ["127.0.0.1"], timeout_ms: 15_000,
      demo_username: "demo@example.test", demo_password: "fixture-only",
      intent_goals: [{ id: "new-project", label: "New Project", kind: "click", keywords: ["new", "project"], required: true, business: true }],
    });

    expect(result.diagnostics?.login_status).toBe("submitted_navigation_observed");
    const submitEvidence = result.diagnostics?.login_evidence?.find((item) => item.id === "login_form_submit");
    expect(submitEvidence).toMatchObject({
      source_kind: "page_scan",
      observed_page_role: "authentication",
      observed_form_role: "authentication",
    });
    expect(submitEvidence?.evidence_digest_sha256).toMatch(/^sha256:[a-f0-9]{64}$/);
  }, 30_000);

  it("rejects destructive transition semantics", async () => {
    server = createServer((_request, response) => {
      response.setHeader("content-type", "text/html; charset=utf-8");
      response.end('<!doctype html><title>Workspace</title><button data-testid="delete-project">Delete project</button>');
    });
    server.listen(0, "127.0.0.1");
    await once(server, "listening");
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("fixture server did not bind");
    const productURL = `http://127.0.0.1:${address.port}/workspace`;

    const result = await verifyInteractions({
      product_url: productURL,
      allowed_domains: ["127.0.0.1"],
      safe_state_transitions: [{ id: "delete-project", label: "Delete project", kind: "click", selector: "[data-testid='delete-project']", url: productURL }],
    });

    expect(result.verification_mode).toBe("playwright_readonly_scan");
    expect(result.diagnostics?.safe_state_transitions).toContain("rejected:delete-project");
  }, 30_000);
});
