import { launchOptionsWithProxy } from "./playwright-proxy.js";

type VerifyInteractionRequest = {
  product_url?: string;
  timeout_ms?: number;
  headless?: boolean;
  allowed_domains?: string[];
  candidates?: InteractionCandidate[];
  intent_goals?: InteractionGoal[];
  demo_username?: string;
  demo_password?: string;
};

type InteractionGoal = {
  id?: string;
  label?: string;
  kind?: string;
  keywords?: string[];
  required?: boolean;
  business?: boolean;
};

type InteractionCandidate = {
  id?: string;
  intent_goal_id?: string;
  label?: string;
  kind?: string;
  selector?: string;
  url?: string;
};

type VerifyInteractionResult = {
  ok: boolean;
  verification_mode: "playwright_readonly_scan";
  browser_scan_id: string;
  source_url?: string;
  results: VerifiedInteractionCandidate[];
  diagnostics?: InteractionVerifierDiagnostics;
  error?: { code: string; message: string };
};

type InteractionVerifierDiagnostics = {
  login_attempted?: boolean;
  login_status?: string;
  final_url?: string;
  page_title?: string;
  candidate_count?: number;
  verified_candidate_count?: number;
  discovered_business_control_count?: number;
};

type VerifiedInteractionCandidate = InteractionCandidate & {
  status: "verified" | "not_visible" | "not_enabled" | "not_editable" | "selector_missing" | "error";
  visible?: boolean;
  enabled?: boolean;
  editable?: boolean;
  page_url?: string;
  page_title?: string;
  message?: string;
  verified_at?: string;
};

export async function verifyInteractions(request: VerifyInteractionRequest): Promise<VerifyInteractionResult> {
  const productURL = request.product_url || request.candidates?.find((candidate) => candidate.url)?.url;
  const scanID = `browser_scan_${hashText(`${productURL || "missing"}|${Date.now()}`)}`;
  const results: VerifiedInteractionCandidate[] = [];
  if (!productURL) {
    return {
      ok: false,
      verification_mode: "playwright_readonly_scan",
      browser_scan_id: scanID,
      results,
      error: { code: "product_url_missing", message: "product_url is required for readonly interaction verification." },
    };
  }

  const playwright = await import("playwright");
  const browser = await playwright.chromium.launch(launchOptionsWithProxy({ headless: request.headless ?? true }));
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });
  const page = await context.newPage();
  const timeout = request.timeout_ms || 20000;
  const diagnostics: InteractionVerifierDiagnostics = {
    candidate_count: request.candidates?.length || 0,
  };
  try {
    await page.goto(productURL, { waitUntil: "domcontentloaded", timeout });
    await page.waitForLoadState("networkidle", { timeout: Math.min(timeout, 8000) }).catch(() => undefined);
    const loginStatus = await attemptLoginIfCredentialsProvided(page, {
      ...(request.demo_username ? { username: request.demo_username } : {}),
      ...(request.demo_password ? { password: request.demo_password } : {}),
      timeout,
    });
    diagnostics.login_attempted = Boolean(request.demo_username && request.demo_password);
    diagnostics.login_status = loginStatus;
    const pageTitle = await page.title().catch(() => "");
    const currentURL = page.url();
    diagnostics.final_url = currentURL;
    diagnostics.page_title = pageTitle;
    for (const candidate of request.candidates || []) {
      const selector = candidate.selector?.trim();
      if (!selector) {
        results.push({ ...candidate, status: "selector_missing", page_url: currentURL, page_title: pageTitle, message: "selector is empty" });
        continue;
      }
      try {
        const locator = page.locator(selector).first();
        const visible = await locator.isVisible({ timeout: 2500 }).catch(() => false);
        if (!visible) {
          results.push({ ...candidate, status: "not_visible", visible, page_url: currentURL, page_title: pageTitle });
          continue;
        }
        const enabled = await locator.isEnabled({ timeout: 1000 }).catch(() => false);
        const editable = await locator.isEditable({ timeout: 1000 }).catch(() => false);
        const action = normalizeAction(candidate.kind);
        if ((action === "click" || action === "select" || action === "upload") && !enabled) {
          results.push({ ...candidate, status: "not_enabled", visible, enabled, editable, page_url: currentURL, page_title: pageTitle });
          continue;
        }
        if (action === "fill" && !editable) {
          results.push({ ...candidate, status: "not_editable", visible, enabled, editable, page_url: currentURL, page_title: pageTitle });
          continue;
        }
        results.push({
          ...candidate,
          status: "verified",
          visible,
          enabled,
          editable,
          page_url: currentURL,
          page_title: pageTitle,
          verified_at: new Date().toISOString(),
        });
      } catch (error) {
        results.push({
          ...candidate,
          status: "error",
          page_url: currentURL,
          page_title: pageTitle,
          message: error instanceof Error ? error.message : String(error),
        });
      }
    }
    const discovered = await discoverBusinessActions(page, request.intent_goals || [], currentURL, pageTitle);
    diagnostics.discovered_business_control_count = discovered.length;
    const seenSelectors = new Set(results.map((result) => normalizeSelectorText(result.selector)));
    for (const item of discovered) {
      if (!item.selector || seenSelectors.has(normalizeSelectorText(item.selector))) {
        continue;
      }
      seenSelectors.add(normalizeSelectorText(item.selector));
      results.push(item);
    }
    diagnostics.verified_candidate_count = results.filter((result) => result.status === "verified").length;
    return { ok: true, verification_mode: "playwright_readonly_scan", browser_scan_id: scanID, source_url: currentURL, results, diagnostics };
  } catch (error) {
    return {
      ok: false,
      verification_mode: "playwright_readonly_scan",
      browser_scan_id: scanID,
      source_url: productURL,
      results,
      diagnostics,
      error: { code: "page_unreachable", message: error instanceof Error ? error.message : String(error) },
    };
  } finally {
    await context.close().catch(() => undefined);
    await browser.close().catch(() => undefined);
  }
}

async function discoverBusinessActions(
  page: any,
  goals: InteractionGoal[],
  pageURL: string,
  pageTitle: string,
): Promise<VerifiedInteractionCandidate[]> {
  const visibleControls = await page.locator("button, a[href], input, textarea, select, [role='button'], [role='link'], [data-testid], [data-test], [data-cy], [aria-label]").evaluateAll((elements: any[]) => {
    return elements.slice(0, 250).map((element, index) => {
      const html = element as any;
      const rect = html.getBoundingClientRect();
      const tag = html.tagName.toLowerCase();
      const input = element as any;
      const style = (globalThis as any).getComputedStyle(html);
      const text = (html.innerText || html.textContent || "").trim().replace(/\s+/g, " ").slice(0, 120);
      const attrs = {
        testid: element.getAttribute("data-testid") || element.getAttribute("data-test") || element.getAttribute("data-cy") || "",
        id: element.getAttribute("id") || "",
        aria: element.getAttribute("aria-label") || "",
        name: element.getAttribute("name") || "",
        placeholder: element.getAttribute("placeholder") || "",
        href: element.getAttribute("href") || "",
        role: element.getAttribute("role") || "",
        type: element.getAttribute("type") || "",
        title: element.getAttribute("title") || "",
      };
      return {
        index,
        tag,
        text,
        attrs,
        visible: rect.width > 0 && rect.height > 0 && style.visibility !== "hidden" && style.display !== "none",
        disabled: Boolean((input as any).disabled) || element.getAttribute("aria-disabled") === "true",
      };
    });
  }).catch(() => []);

  const scored = visibleControls
    .filter((control: any) => control.visible && !control.disabled)
    .map((control: any) => {
      const label = controlLabel(control);
      const selector = selectorForControl(control, label);
      const kind = actionKindForControl(control);
      const score = businessControlScore(label, selector, goals, kind);
      const goal = bestGoalForControl(label, selector, goals);
      return { control, label, selector, kind, score, goal };
    })
    .filter((item: any) => item.selector && item.score >= 35 && !looksLikeChromeControl(`${item.label} ${item.selector}`))
    .sort((a: any, b: any) => b.score - a.score)
    .slice(0, 8);

  const now = new Date().toISOString();
  return scored.map((item: any, index: number) => {
    const result: VerifiedInteractionCandidate = {
      id: `browser_discovered_${index + 1}_${hashText(`${item.selector}|${item.label}`)}`,
      label: item.label,
      kind: item.kind,
      selector: item.selector,
      url: pageURL,
      status: "verified",
      visible: true,
      enabled: true,
      editable: item.kind === "fill",
      page_url: pageURL,
      page_title: pageTitle,
      message: "login-aware readonly DOM scan discovered a visible business control",
      verified_at: now,
    };
    if (item.goal?.id) {
      result.intent_goal_id = item.goal.id;
    }
    return result;
  });
}

function controlLabel(control: any): string {
  return [
    control.text,
    control.attrs?.aria,
    control.attrs?.placeholder,
    control.attrs?.title,
    control.attrs?.testid,
    control.attrs?.name,
    control.attrs?.id,
    control.attrs?.href,
  ].filter(Boolean).join(" ").trim().replace(/\s+/g, " ").slice(0, 140);
}

function selectorForControl(control: any, label: string): string {
  const attrs = control.attrs || {};
  if (attrs.testid) return `[data-testid="${escapeCSSString(attrs.testid)}"]`;
  if (attrs.aria) return `[aria-label="${escapeCSSString(attrs.aria)}"]`;
  if (attrs.id && /^[A-Za-z][\w-]*$/.test(attrs.id)) return `#${attrs.id}`;
  if (control.tag === "input" && attrs.name) return `input[name="${escapeCSSString(attrs.name)}"]`;
  if (control.tag === "textarea" && attrs.name) return `textarea[name="${escapeCSSString(attrs.name)}"]`;
  if (control.tag === "select" && attrs.name) return `select[name="${escapeCSSString(attrs.name)}"]`;
  if ((control.tag === "input" || control.tag === "textarea") && attrs.placeholder) return `${control.tag}[placeholder="${escapeCSSString(attrs.placeholder)}"]`;
  const shortLabel = visibleTextSelectorLabel(label);
  if (shortLabel && control.tag === "button") return `button:has-text("${escapeCSSString(shortLabel)}")`;
  if (shortLabel && control.tag === "a") return `a:has-text("${escapeCSSString(shortLabel)}")`;
  if (shortLabel && attrs.role === "button") return `[role="button"]:has-text("${escapeCSSString(shortLabel)}")`;
  if (shortLabel && attrs.role === "link") return `[role="link"]:has-text("${escapeCSSString(shortLabel)}")`;
  return "";
}

function actionKindForControl(control: any): string {
  const tag = control.tag;
  const type = String(control.attrs?.type || "").toLowerCase();
  if (tag === "input" && !["button", "submit", "checkbox", "radio"].includes(type)) return "fill";
  if (tag === "textarea") return "fill";
  if (tag === "select") return "select";
  return "click";
}

function businessControlScore(label: string, selector: string, goals: InteractionGoal[], kind: string): number {
  const normalized = normalizeSelectorText(`${label} ${selector}`);
  if (!normalized || looksLikeChromeControl(normalized) || looksLikeLoginControl(normalized)) return 0;
  let score = 10;
  let hasBusinessSignal = false;
  if (selector.includes("data-testid") || selector.includes("data-test") || selector.includes("data-cy")) score += 35;
  if (selector.includes("aria-label") || selector.includes(":has-text")) score += 25;
  if (selector.startsWith("#")) score += 18;
  if (["click", "fill", "select"].includes(kind)) score += 12;
  for (const keyword of businessKeywords()) {
    if (normalized.includes(keyword)) {
      score += 15;
      hasBusinessSignal = true;
    }
  }
  for (const goal of goals) {
    const keywords = goalKeywords(goal);
    const matched = keywords.filter((keyword) => keyword && normalized.includes(keyword)).length;
    if (matched > 0) {
      score += matched * 20;
      if (goal.business) score += 12;
      hasBusinessSignal = true;
    }
  }
  if (!hasBusinessSignal) return 0;
  return score;
}

function bestGoalForControl(label: string, selector: string, goals: InteractionGoal[]): InteractionGoal | undefined {
  const normalized = normalizeSelectorText(`${label} ${selector}`);
  let best: { goal: InteractionGoal; score: number } | undefined;
  for (const goal of goals) {
    const score = goalKeywords(goal).filter((keyword) => keyword && normalized.includes(keyword)).length;
    if (!best || score > best.score) {
      best = { goal, score };
    }
  }
  return best && best.score > 0 ? best.goal : goals.find((goal) => goal.business) || goals[0];
}

function goalKeywords(goal: InteractionGoal): string[] {
  return uniqueWords([...(goal.keywords || []), ...(goal.label || "").split(/[\s,，、/|·:：-]+/)]);
}

function businessKeywords(): string[] {
  return [
    "create", "new", "add", "generate", "run", "start", "build", "upload", "search", "submit", "save", "confirm", "open",
    "创建", "新建", "新增", "添加", "生成", "开始", "运行", "上传", "搜索", "提交", "保存", "确认", "打开", "进入",
    "项目", "工作台", "资产", "工作流", "内容", "视频", "脚本", "图谱", "演示", "模板", "审批", "录制",
  ];
}

function looksLikeChromeControl(value: string): boolean {
  const normalized = normalizeSelectorText(value);
  if (!/(sidebar|side-bar|toggle|collapse|expand|hamburger|theme|avatar|profile|account-menu|breadcrumb|drawer|layout|shell|chrome|侧边栏|折叠|展开|主题|头像|个人资料|导航|菜单|通知)/i.test(normalized)) {
    return false;
  }
  return !/(create|new|project|generate|run|start|submit|save|confirm|创建|新建|项目|生成|开始|运行|提交|保存|确认)/i.test(normalized);
}

function looksLikeLoginControl(value: string): boolean {
  return /(登录|登陆|登入|sign\s*in|log\s*in|login|logout|退出|注册|创建账户|create\s*account|sign\s*up|register)/i.test(value);
}

function looksLikeLoginURL(value: string): boolean {
  return /\/(login|signin|sign-in|auth\/login|app\/login)([/?#]|$)/i.test(value);
}

function visibleTextSelectorLabel(label: string): string {
  const cleaned = label.replace(/\s+/g, " ").trim();
  if (!cleaned || cleaned.length > 36) return "";
  return cleaned;
}

function escapeCSSString(value: string): string {
  return value.replace(/\\/g, "\\\\").replace(/"/g, "\\\"");
}

function normalizeSelectorText(value?: string): string {
  return (value || "").toLowerCase().trim().replace(/\s+/g, " ");
}

function uniqueWords(values: string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of values) {
    const normalized = normalizeSelectorText(value);
    if (!normalized || seen.has(normalized)) continue;
    seen.add(normalized);
    out.push(normalized);
  }
  return out;
}

async function attemptLoginIfCredentialsProvided(
  page: any,
  credentials: { username?: string; password?: string; timeout: number },
): Promise<string> {
  const username = credentials.username?.trim();
  const password = credentials.password;
  if (!username || !password) {
    return "credentials_missing";
  }

  let passwordInput = await firstVisibleLocator(page, passwordInputSelectors(), 1200);
  if (!passwordInput) {
    const trigger = await firstVisibleLoginTrigger(page);
    if (trigger) {
      await trigger.click({ timeout: 2500 }).catch(() => undefined);
      await page.waitForLoadState("domcontentloaded", { timeout: Math.min(credentials.timeout, 6000) }).catch(() => undefined);
      await page.waitForLoadState("networkidle", { timeout: Math.min(credentials.timeout, 6000) }).catch(() => undefined);
    }
  }

  passwordInput = await firstVisibleLocator(page, passwordInputSelectors(), 2500);
  if (!passwordInput) {
    passwordInput = await navigateToLikelyLoginPath(page, credentials.timeout);
  }
  if (!passwordInput) {
    return "login_form_not_found";
  }
  const usernameInput = await firstVisibleLocator(page, usernameInputSelectors(), 2500);
  if (!usernameInput) {
    return "username_input_not_found";
  }

  await usernameInput.fill(username, { timeout: 2500 }).catch(() => undefined);
  await passwordInput.fill(password, { timeout: 2500 }).catch(() => undefined);
  const submit = await firstVisibleLoginSubmit(page);
  if (submit) {
    await submit.click({ timeout: 3000 }).catch(() => undefined);
  } else {
    await passwordInput.press("Enter", { timeout: 2000 }).catch(() => undefined);
  }
  await page.waitForLoadState("domcontentloaded", { timeout: Math.min(credentials.timeout, 8000) }).catch(() => undefined);
  await page.waitForLoadState("networkidle", { timeout: Math.min(credentials.timeout, 8000) }).catch(() => undefined);
  await page.waitForTimeout(1000).catch(() => undefined);
  const passwordStillVisible = await firstVisibleLocator(page, passwordInputSelectors(), 900);
  if (passwordStillVisible) {
    return "submitted_login_form_still_visible";
  }
  if (looksLikeLoginURL(page.url())) {
    return "submitted_still_on_login_url";
  }
  return "submitted_navigation_observed";
}

async function navigateToLikelyLoginPath(page: any, timeout: number): Promise<any | undefined> {
  const current = new URL(page.url());
  const paths = ["/login", "/signin", "/sign-in", "/auth/login", "/app/login"];
  for (const path of paths) {
    const target = new URL(path, current.origin).toString();
    await page.goto(target, { waitUntil: "domcontentloaded", timeout: Math.min(timeout, 6000) }).catch(() => undefined);
    await page.waitForLoadState("networkidle", { timeout: Math.min(timeout, 5000) }).catch(() => undefined);
    const passwordInput = await firstVisibleLocator(page, passwordInputSelectors(), 1500);
    if (passwordInput) {
      return passwordInput;
    }
  }
  await page.goto(current.toString(), { waitUntil: "domcontentloaded", timeout: Math.min(timeout, 6000) }).catch(() => undefined);
  await page.waitForLoadState("networkidle", { timeout: Math.min(timeout, 5000) }).catch(() => undefined);
  return undefined;
}

async function firstVisibleLoginTrigger(page: any): Promise<any | undefined> {
  const loginName = /登录|登陆|登入|sign\s*in|log\s*in|login|控制台|console|dashboard|进入/i;
  const roleCandidates = [
    page.getByRole("link", { name: loginName }).first(),
    page.getByRole("button", { name: loginName }).first(),
    page.getByText(loginName).first(),
  ];
  for (const locator of roleCandidates) {
    if (await locator.isVisible({ timeout: 900 }).catch(() => false)) {
      return locator;
    }
  }
  return firstVisibleLocator(page, [
    "a[href*='login']",
    "a[href*='signin']",
    "a[href*='sign-in']",
    "button[data-testid*='login' i]",
    "[data-testid*='login' i]",
  ], 900);
}

async function firstVisibleLoginSubmit(page: any): Promise<any | undefined> {
  const loginName = /登录|登陆|登入|sign\s*in|log\s*in|login|继续|continue|提交|submit/i;
  const roleCandidates = [
    page.getByRole("button", { name: loginName }).first(),
    page.getByRole("link", { name: loginName }).first(),
  ];
  for (const locator of roleCandidates) {
    if (await locator.isVisible({ timeout: 900 }).catch(() => false)) {
      return locator;
    }
  }
  return firstVisibleLocator(page, [
    "button[type='submit']",
    "input[type='submit']",
    "[data-testid*='submit' i]",
    "[data-testid*='login' i]",
  ], 900);
}

async function firstVisibleLocator(page: any, selectors: string[], timeout: number): Promise<any | undefined> {
  for (const selector of selectors) {
    const locator = page.locator(selector).first();
    if (await locator.isVisible({ timeout }).catch(() => false)) {
      return locator;
    }
  }
  return undefined;
}

function usernameInputSelectors(): string[] {
  return [
    "input[type='email']",
    "input[name='email']",
    "input[name='username']",
    "input[name='account']",
    "input[id*='email' i]",
    "input[id*='user' i]",
    "input[autocomplete='username']",
    "input[autocomplete='email']",
    "input[type='text']",
  ];
}

function passwordInputSelectors(): string[] {
  return [
    "input[type='password']",
    "input[name='password']",
    "input[id*='password' i]",
    "input[autocomplete='current-password']",
  ];
}

function normalizeAction(value?: string): string {
  const normalized = (value || "").toLowerCase().trim();
  if (["button", "cta"].includes(normalized)) return "click";
  if (["input", "type"].includes(normalized)) return "fill";
  return normalized;
}

function hashText(value: string): string {
  let hash = 2166136261;
  for (let i = 0; i < value.length; i += 1) {
    hash ^= value.charCodeAt(i);
    hash += (hash << 1) + (hash << 4) + (hash << 7) + (hash << 8) + (hash << 24);
  }
  return (hash >>> 0).toString(16);
}
