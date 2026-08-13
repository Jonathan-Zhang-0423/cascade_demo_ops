import { describe, expect, it } from "vitest";
import { createLocalBridgeClient, createMockBridgeClient } from "./bridge";

describe("account bridge", () => {
  it("persists profile updates, exposes credits, and gates after logout", async () => {
    const bridge = createMockBridgeClient();
    const session = await bridge.accountSession();
    expect(session.data?.authenticated).toBe(true);
    const updated = await bridge.updateAccountProfile({ displayName: "Cascade Operator" });
    expect(updated.data?.initials).toBe("CO");
	const verification = await bridge.startAccountVerification("email", "operator@example.com");
	expect(verification.data?.developmentCode).toBe("123456");
	expect((await bridge.accountVerificationState("email")).data?.status).toBe("pending");
	expect((await bridge.confirmAccountVerification("email", "123456")).data?.emailVerification).toBe("verified");
	expect((await bridge.accountVerificationState("email")).data?.status).toBe("verified");
    expect((await bridge.accountPlan()).data?.creditsRemaining).toBe(640);
    expect((await bridge.accountLogout()).data?.authenticated).toBe(false);
    expect((await bridge.accountProfile()).ok).toBe(false);
    expect((await bridge.devAccountLogin()).data?.user?.displayName).toBe("Cascade Operator");
  });

  it("completes the deterministic GitHub device flow without exposing a token", async () => {
    const bridge = createMockBridgeClient();
    const started = await bridge.startGitHubDeviceFlow();
    expect(started.data?.status).toBe("pending");
    const completed = await bridge.pollGitHubDeviceFlow(started.data!.id);
    expect(completed.data?.status).toBe("authorized");
    expect(completed.data?.github?.login).toBe("jonathan-zhang");
    expect(JSON.stringify(completed.data)).not.toContain("access_token");
  });

  it("keeps mock password setup, login, and recovery contract-compatible", async () => {
    const bridge = createMockBridgeClient();
    const created = await bridge.setAccountPassword("a sufficiently long password");
    expect(created.data).toMatchObject({ authenticated: false, passwordSetupRequired: false });
    expect((await bridge.passwordAccountLogin("jonathan zhang", "wrong password")).ok).toBe(false);
    expect((await bridge.passwordAccountLogin("jonathan zhang", "a sufficiently long password")).data?.authenticated).toBe(true);
    const reset = await bridge.startAccountPasswordReset("email", "jonathan@cascade.ai");
    expect(reset.data?.developmentCode).toBe("654321");
    expect((await bridge.confirmAccountPasswordReset("email", "jonathan@cascade.ai", "654321", "a recovered long password")).data?.authenticated).toBe(false);
    expect((await bridge.passwordAccountLogin("jonathan@cascade.ai", "a recovered long password")).data?.authenticated).toBe(true);
  });

  it("maps local account DTOs and sends only editable profile fields", async () => {
    const originalFetch = globalThis.fetch;
    let requestBody = "";
    globalThis.fetch = async (input, init) => {
      const path = String(input);
      if (path.endsWith("/v1/desktop/account/profile") && init?.method === "PUT") {
        requestBody = String(init.body);
        return new Response(JSON.stringify({ ok: true, data: { id: "user_1", display_name: "Jonathan Zhang", email: "jonathan@example.com", initials: "JZ", email_verification: "unverified", has_password: false, updated_at: "2026-08-01T00:00:00Z" } }), { status: 200 });
      }
	  if (path.endsWith("/v1/desktop/account/verification/email/start")) {
		return new Response(JSON.stringify({ ok: true, data: { channel: "email", masked_destination: "j•••@example.com", status: "pending", expires_at: "2026-08-01T00:10:00Z", resend_at: "2026-08-01T00:01:00Z" } }), { status: 200 });
	  }
	  if (path.endsWith("/v1/desktop/account/verification/email/confirm")) {
		return new Response(JSON.stringify({ ok: true, data: { id: "user_1", display_name: "Jonathan Zhang", email: "jonathan@example.com", initials: "JZ", email_verification: "verified", has_password: false, updated_at: "2026-08-01T00:00:00Z" } }), { status: 200 });
	  }
	  if (path.endsWith("/v1/desktop/account/verification/email") && (!init?.method || init.method === "GET")) {
		return new Response(JSON.stringify({ ok: true, data: { channel: "email", status: "pending", masked_destination: "j•••@example.com", expires_at: "2026-08-01T00:10:00Z", resend_at: "2026-08-01T00:01:00Z" } }), { status: 200 });
	  }
      return new Response(JSON.stringify({ ok: true, data: { authenticated: true, dev_login_available: true, password_setup_required: true, user: { id: "user_1", display_name: "Jonathan Zhang", initials: "JZ", email_verification: "unverified", has_password: false, updated_at: "2026-08-01T00:00:00Z" } } }), { status: 200 });
    };
    try {
      const bridge = createLocalBridgeClient("http://127.0.0.1:4317");
      expect((await bridge.accountSession()).data?.user?.displayName).toBe("Jonathan Zhang");
      expect((await bridge.updateAccountProfile({ displayName: "Jonathan Zhang" })).data?.email).toBe("jonathan@example.com");
      expect(JSON.parse(requestBody)).toEqual({ display_name: "Jonathan Zhang" });
	  expect((await bridge.startAccountVerification("email", "jonathan@example.com")).data?.maskedDestination).toBe("j•••@example.com");
	  expect((await bridge.accountVerificationState("email")).data?.status).toBe("pending");
	  expect((await bridge.confirmAccountVerification("email", "123456")).data?.emailVerification).toBe("verified");
      expect(requestBody).not.toContain("token");
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});
