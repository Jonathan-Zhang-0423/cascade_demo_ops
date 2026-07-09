// Cascade DemoOps fixture script.
// Generated from plan.json shape for review/demo only.
// The cloud worker must validate this file before sandbox execution.

type CascadeStepResult = {
  nodeId: string;
  status: "passed" | "failed";
  durationMs?: number;
  error?: string;
};

type CascadeRecordingResult = {
  ok: boolean;
  planHash: string;
  stepResults: CascadeStepResult[];
};

type CascadeRecordingContext = {
  page: {
    goto(url: string, options?: Record<string, unknown>): Promise<void>;
    click(selector: string, options?: Record<string, unknown>): Promise<void>;
    fill(selector: string, value: string, options?: Record<string, unknown>): Promise<void>;
    waitForLoadState(state?: string, options?: Record<string, unknown>): Promise<void>;
    locator(selector: string): { waitFor(options?: Record<string, unknown>): Promise<void> };
  };
  secrets: {
    get(secretRef: string): Promise<string>;
    getInput(inputRef: string): Promise<string>;
  };
  capture: {
    start(metadata?: Record<string, unknown>): Promise<void>;
    stop(): Promise<void>;
    mark(nodeId: string, metadata?: Record<string, unknown>): Promise<void>;
    screenshot(options?: Record<string, unknown>): Promise<void>;
    applyRedactions(selectors: string[]): Promise<void>;
  };
  assert: {
    step(nodeId: string, expected: string, options?: Record<string, unknown>): Promise<void>;
    visible(selector: string, options?: Record<string, unknown>): Promise<void>;
    url(url: string, options?: Record<string, unknown>): Promise<void>;
  };
  log: {
    step(nodeId: string, title: string): Promise<void>;
    info(message: string, metadata?: Record<string, unknown>): Promise<void>;
  };
};

const cascadePlanHash = "example_sha256_plan_basic_three_in_one";
const cascadeNodeIds = ["open_dashboard", "open_team", "invite_member", "verify_success"];
const cascadeAllowedDomains = ["demo.example.cn"];
const cascadeForbiddenPages = ["/billing", "/settings/api-keys"];
const cascadeRedactionSelectors = ["[data-sensitive]", "[data-testid='customer-email']"];

export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult> {
  const stepResults: CascadeStepResult[] = [];
  await ctx.capture.start({
    planHash: cascadePlanHash,
    nodeIds: cascadeNodeIds,
    allowedDomains: cascadeAllowedDomains,
    forbiddenPages: cascadeForbiddenPages,
  });
  await ctx.capture.applyRedactions(cascadeRedactionSelectors);

  await ctx.log.step("open_dashboard", "打开产品工作台");
  await ctx.capture.mark("open_dashboard", { order: 1, action: "navigate" });
  await ctx.page.goto("https://demo.example.cn/dashboard", { waitUntil: "networkidle", timeout: 30000 });
  await ctx.page.waitForLoadState("networkidle", { timeout: 30000 });
  await ctx.assert.url("https://demo.example.cn/dashboard", { nodeId: "open_dashboard", timeout: 30000 });
  await ctx.assert.visible("[data-testid='dashboard-shell']", { nodeId: "open_dashboard", timeout: 15000 });
  await ctx.capture.screenshot({ nodeId: "open_dashboard", maskSelectors: cascadeRedactionSelectors });
  stepResults.push({ nodeId: "open_dashboard", status: "passed", durationMs: 8000 });

  await ctx.log.step("open_team", "进入团队协作页面");
  await ctx.capture.mark("open_team", { order: 2, action: "click" });
  await ctx.page.click("[data-testid='team-nav']", { timeout: 15000 });
  await ctx.assert.visible("[data-testid='team-page']", { nodeId: "open_team", timeout: 15000 });
  await ctx.capture.screenshot({ nodeId: "open_team", maskSelectors: cascadeRedactionSelectors });
  stepResults.push({ nodeId: "open_team", status: "passed", durationMs: 7000 });

  await ctx.log.step("invite_member", "邀请一名团队成员");
  await ctx.capture.mark("invite_member", { order: 3, action: "fill_and_click" });
  const inviteEmail = await ctx.secrets.getInput("input_ref:demo_invite_email");
  await ctx.page.fill("[data-testid='invite-email-input']", inviteEmail, { timeout: 15000 });
  await ctx.page.click("[data-testid='send-invite-button']", { timeout: 15000 });
  await ctx.assert.step("invite_member", "邀请请求已提交", { selector: "[data-testid='invite-pending']", timeout: 15000 });
  await ctx.capture.screenshot({ nodeId: "invite_member", maskSelectors: cascadeRedactionSelectors, callout: true });
  stepResults.push({ nodeId: "invite_member", status: "passed", durationMs: 12000 });

  await ctx.log.step("verify_success", "验证邀请成功状态");
  await ctx.capture.mark("verify_success", { order: 4, action: "assert" });
  await ctx.page.locator("[data-testid='invite-success']").waitFor({ state: "visible", timeout: 20000 });
  await ctx.assert.visible("[data-testid='invite-success']", { nodeId: "verify_success", timeout: 20000 });
  await ctx.capture.screenshot({ nodeId: "verify_success", maskSelectors: cascadeRedactionSelectors, zoom: true, callout: true });
  stepResults.push({ nodeId: "verify_success", status: "passed", durationMs: 10000 });

  await ctx.capture.stop();
  return { ok: true, planHash: cascadePlanHash, stepResults };
}
