import { afterEach, describe, expect, it, vi } from "vitest";

import { createExperimentRunClient, experimentRunStatusCopy, type ExperimentRun } from "./experimentRun";

afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs(); });

describe("experiment run client", () => {
  it("binds startup authorization and final review to revisions", async () => {
	vi.stubEnv("VITE_CASCADE_BRIDGE", "local");
    const calls: Array<{ url: string; body: string | undefined }> = [];
    vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), body: init?.body ? String(init.body) : undefined });
      return new Response(JSON.stringify({ ok: true, data: runFixture() }), { status: 200, headers: { "content-type": "application/json" } });
    });
    const client = createExperimentRunClient();
    await client.start({ definition_ref: "interactive-v2", target_url: "https://target.example/app", credential_ref: "credential://demo/ref", authorization_ref: "approval://run/start", idempotency_key: "experiment-idempotency-001" });
    await client.finalReview("run-1", 7, 19, "accept", "package-1", "reviewer://local/user");
    expect(calls[0]!.url).toContain("/v1/experiment-runs");
    expect(calls[0]!.body).toContain("approval://run/start");
    expect(calls[1]!.body).toBe(JSON.stringify({ expected_revision: 7, final_film_revision: 19, decision: "accept", package_id: "package-1", reviewer_ref: "reviewer://local/user" }));
  });

  it("explains waiting ownership instead of inventing a lifecycle state", () => {
    const run = runFixture();
    run.state = "waiting_input";
    run.phase = "once_effect_uncertain";
    run.waiting = { reason: "External result could not be confirmed", responsibility: "user", next_action: "inspect evidence" };
    expect(experimentRunStatusCopy(run)).toEqual({ title: "实验等待处理", detail: "External result could not be confirmed", blocked: true });
  });
});

function runFixture(): ExperimentRun {
  return {
    schema_version: "demoops.experiment_run.v1", run_id: "run-1", definition_id: "interactive-v2", workflow_template_id: "async-product-build-demo-v1",
    state: "queued", phase: "product_spec_frozen", revision: 1,
    budget: { target_submissions: 2, final_film_jobs: 1, provider_calls: 8, visual_calls_per_run: 12 }, provider_calls_used: 0, legs: [],
  };
}
