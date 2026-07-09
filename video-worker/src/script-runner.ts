import { createHash } from "node:crypto";

type ScriptBundle = {
  script_manifest?: {
    entry_function?: string;
    language?: string;
    runtime?: string;
    dependency_allowlist?: string[];
    step_node_ids?: string[];
  };
  plan_json?: {
    steps?: Array<{ node_id?: string; order?: number; timing?: { duration_ms?: number } }>;
    recording_run_spec?: { allowed_domains?: string[] };
  };
  playwright_script?: { inline_source?: string; sha256?: string };
  security_policy?: {
    allowed_domains?: string[];
    forbidden_pages?: string[];
    forbidden_imports?: string[];
    forbidden_identifiers?: string[];
    allowed_context_apis?: string[];
    allowed_page_methods?: string[];
  };
  reproducibility?: { script_hash_sha256?: string };
};

export type ScriptValidationRequest = {
  bundle?: ScriptBundle;
};

export type ScriptValidationFinding = {
  id: string;
  severity: "info" | "warning" | "blocking";
  summary: string;
};

export type ScriptValidationResult = {
  valid: boolean;
  findings: ScriptValidationFinding[];
};

export type ExecuteScriptRequest = ScriptValidationRequest & {
  output_dir?: string;
  simulate_failure_node_id?: string;
};

export type ExecuteScriptResult = {
  ok: boolean;
  validation: ScriptValidationResult;
  recording_path?: string;
  screenshot_paths?: string[];
  trace_path?: string;
  step_results?: Array<{ node_id: string; status: string; duration_ms?: number }>;
  failure_diagnostic?: WorkerScriptFailureDiagnostic;
  error?: string;
};

export type WorkerScriptFailureDiagnostic = {
  schema_version: "demoops.script_failure_diagnostic.v1";
  failed_node_id: string;
  failed_step_order?: number;
  error: { code: string; message: string; retryable?: boolean };
  current_url?: string;
  page_title?: string;
  screenshot_refs: Array<{ role: string; kind: string; uri: string; mime_type: string; sha256: string; encrypted: boolean; sensitive: boolean }>;
  trace_refs: Array<{ role: string; kind: string; uri: string; mime_type: string; sha256: string; encrypted: boolean; sensitive: boolean }>;
  console_events: Array<{ level: string; message: string }>;
  network_events: Array<{ url: string; method?: string; status?: number; redacted?: boolean }>;
  redaction_report: { applied: boolean; stripped_headers: string[]; stripped_storage_keys: string[]; full_html_included: false };
  repair_hints: Array<{ kind: string; node_id: string; summary: string; suggested_action: string; confidence: number }>;
};

export async function validateScript(request: ScriptValidationRequest): Promise<ScriptValidationResult> {
  const bundle = request.bundle;
  const findings: ScriptValidationFinding[] = [];
  if (!bundle) {
    findings.push(blocking("bundle_missing", "缺少可执行脚本包。"));
    return { valid: false, findings };
  }
  const source = bundle.playwright_script?.inline_source ?? "";
  if (!source) {
    findings.push(blocking("script_missing", "TypeScript 脚本为空。"));
  }
  if (bundle.script_manifest?.entry_function !== "runCascadeRecording") {
    findings.push(blocking("entry_function_mismatch", "脚本入口函数必须是 runCascadeRecording。"));
  }
  if (bundle.script_manifest?.language !== "typescript") {
    findings.push(blocking("language_mismatch", "脚本语言必须是 TypeScript。"));
  }
  if (bundle.script_manifest?.runtime !== "playwright-restricted-sandbox") {
    findings.push(blocking("runtime_mismatch", "脚本必须声明受限 Playwright 沙箱运行时。"));
  }
  if ((bundle.script_manifest?.dependency_allowlist ?? []).length > 0) {
    findings.push(blocking("dependency_allowlist_not_empty", "v1 不允许脚本声明外部依赖。"));
  }
  if (!source.includes("runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult>")) {
    findings.push(blocking("entry_signature_missing", "脚本缺少固定导出函数签名。"));
  }

  for (const token of bundle.security_policy?.forbidden_identifiers ?? defaultForbiddenIdentifiers) {
    if (hasForbiddenIdentifier(source, token)) {
      findings.push(blocking(`forbidden_identifier_${safeID(token)}`, `脚本包含禁止标识符：${token}`));
    }
  }
  for (const token of bundle.security_policy?.forbidden_imports ?? defaultForbiddenImports) {
    if (source.includes(`"${token}"`) || source.includes(`'${token}'`)) {
      findings.push(blocking(`forbidden_import_${safeID(token)}`, `脚本包含禁止导入：${token}`));
    }
  }
  for (const finding of validateAllowedContextUsage(source, bundle)) {
    findings.push(finding);
  }
  for (const item of rawSecretPatterns) {
    if (item.pattern.test(source)) {
      findings.push(blocking(item.id, item.summary));
    }
  }

  const scriptHash = sha256(source);
  if (bundle.playwright_script?.sha256 && bundle.playwright_script.sha256 !== scriptHash) {
    findings.push(blocking("script_source_hash_mismatch", "脚本 sha256 与内联内容不一致。"));
  }
  if (bundle.reproducibility?.script_hash_sha256 && bundle.reproducibility.script_hash_sha256 !== scriptHash) {
    findings.push(blocking("script_hash_mismatch", "reproducibility.script_hash_sha256 与脚本内容不一致。"));
  }

  const planNodeIDs = new Set((bundle.plan_json?.steps ?? []).map((step) => step.node_id).filter((nodeID): nodeID is string => Boolean(nodeID)));
  for (const nodeID of planNodeIDs) {
    if (!source.includes(`"${nodeID}"`)) {
      findings.push(blocking(`node_missing_${safeID(nodeID)}`, `脚本缺少 JSON plan 中的 node_id：${nodeID}`));
    }
  }
  for (const nodeID of bundle.script_manifest?.step_node_ids ?? []) {
    if (!planNodeIDs.has(nodeID)) {
      findings.push(blocking(`manifest_node_unknown_${safeID(nodeID)}`, `manifest 中存在 plan 未声明的 node_id：${nodeID}`));
    }
  }

  const allowedDomains = bundle.security_policy?.allowed_domains ?? bundle.plan_json?.recording_run_spec?.allowed_domains ?? [];
  for (const literalURL of extractGotoURLs(source)) {
    const parsed = safeURL(literalURL);
    if (!parsed) {
      findings.push(blocking("url_parse_failed", `脚本中存在无法解析的跳转 URL：${literalURL}`));
      continue;
    }
    if (!hostAllowed(parsed.host, allowedDomains)) {
      findings.push(blocking("url_domain_forbidden", `脚本跳转 URL 不在 allowed domains 内：${literalURL}`));
    }
    for (const forbidden of bundle.security_policy?.forbidden_pages ?? []) {
      if (forbidden && parsed.pathname.includes(forbidden)) {
        findings.push(blocking("forbidden_page_navigation", `脚本尝试访问禁止页面：${literalURL}`));
      }
    }
  }

  return { valid: findings.every((finding) => finding.severity !== "blocking"), findings };
}

export async function executeScript(request: ExecuteScriptRequest): Promise<ExecuteScriptResult> {
  const validation = await validateScript(request);
  if (!validation.valid) {
    const failedNodeID = request.bundle?.plan_json?.steps?.[0]?.node_id ?? "validation";
    return {
      ok: false,
      validation,
      error: "script validation failed",
      failure_diagnostic: buildFailureDiagnostic(request, failedNodeID, "script_validation_failed", "脚本校验失败，未进入浏览器执行。"),
    };
  }
  const outputDir = request.output_dir || "artifacts/script-execution";
  const steps = request.bundle?.plan_json?.steps ?? [];
  const simulatedFailureNodeID = request.simulate_failure_node_id;
  if (simulatedFailureNodeID) {
    return {
      ok: false,
      validation,
      trace_path: `${outputDir}/trace.zip`,
      screenshot_paths: [`${outputDir}/failure-${safeID(simulatedFailureNodeID)}.png`],
      step_results: steps.map((step) => ({
        node_id: step.node_id ?? "unknown",
        status: step.node_id === simulatedFailureNodeID ? "failed" : "passed",
        ...(step.timing?.duration_ms !== undefined ? { duration_ms: step.timing.duration_ms } : {}),
      })),
      failure_diagnostic: buildFailureDiagnostic(request, simulatedFailureNodeID, "selector_timeout", `等待节点 ${simulatedFailureNodeID} 的 selector 超时。`),
      error: `script failed at node ${simulatedFailureNodeID}`,
    };
  }
  return {
    ok: true,
    validation,
    recording_path: `${outputDir}/recording.webm`,
    screenshot_paths: steps.map((step, index) => `${outputDir}/step-${String(index + 1).padStart(3, "0")}.png`),
    trace_path: `${outputDir}/trace.zip`,
    step_results: steps.map((step) => {
      const result: { node_id: string; status: string; duration_ms?: number } = {
        node_id: step.node_id ?? "unknown",
        status: "passed",
      };
      if (step.timing?.duration_ms !== undefined) {
        result.duration_ms = step.timing.duration_ms;
      }
      return result;
    }),
  };
}

const defaultForbiddenImports = ["fs", "node:fs", "child_process", "node:child_process", "http", "https", "net", "tls"];
const defaultForbiddenIdentifiers = ["import", "require", "eval", "Function", "process", "global", "globalThis", "window", "document", "fetch", "XMLHttpRequest", "WebSocket"];
const rawSecretPatterns = [
  { id: "private_key_literal", pattern: /BEGIN\s+(RSA\s+|EC\s+|OPENSSH\s+)?PRIVATE\s+KEY/i, summary: "脚本中疑似出现私钥内容。" },
  { id: "bearer_token_literal", pattern: /bearer\s+[a-z0-9._-]{12,}/i, summary: "脚本中疑似出现 Bearer token。" },
  { id: "api_key_assignment", pattern: /(password|passwd|api[_-]?key|private[_-]?key|token)\s*[:=]\s*["'][^"']{6,}["']/i, summary: "脚本中疑似出现明文密码、token 或 API key。" },
  { id: "secret_key_literal", pattern: /sk-[A-Za-z0-9]{12,}/, summary: "脚本中疑似出现 API secret key。" },
];

function blocking(id: string, summary: string): ScriptValidationFinding {
  return { id, severity: "blocking", summary };
}

function hasForbiddenIdentifier(source: string, token: string): boolean {
  if (token === "import") {
    return /^\s*import\s/m.test(source);
  }
  return new RegExp(`\\b${escapeRegExp(token)}\\b`).test(source);
}

function validateAllowedContextUsage(source: string, bundle: ScriptBundle): ScriptValidationFinding[] {
  const findings: ScriptValidationFinding[] = [];
  const allowedRoots = new Set(
    (bundle.security_policy?.allowed_context_apis ?? ["ctx.page", "ctx.secrets", "ctx.capture", "ctx.assert", "ctx.log"])
      .map((api) => api.replace(/^ctx\./, "").trim())
      .filter(Boolean),
  );
  for (const match of source.matchAll(/ctx\.([A-Za-z_][A-Za-z0-9_]*)/g)) {
    const root = match[1];
    if (root && !allowedRoots.has(root)) {
      findings.push(blocking(`ctx_api_forbidden_${safeID(root)}`, `脚本调用了未授权的 ctx API：${root}`));
    }
  }
  const allowedPageMethods = new Set(
    (bundle.security_policy?.allowed_page_methods ?? ["goto", "click", "fill", "selectOption", "setInputFiles", "waitForTimeout", "waitForLoadState", "locator"])
      .map((method) => method.trim())
      .filter(Boolean),
  );
  for (const match of source.matchAll(/ctx\.page\.([A-Za-z_][A-Za-z0-9_]*)/g)) {
    const method = match[1];
    if (method && !allowedPageMethods.has(method)) {
      findings.push(blocking(`page_method_forbidden_${safeID(method)}`, `脚本调用了未授权的 ctx.page 方法：${method}`));
    }
  }
  return findings;
}

function extractGotoURLs(source: string): string[] {
  return [...source.matchAll(/ctx\.page\.goto\("([^"]+)"/g)]
    .map((match) => match[1])
    .filter((value): value is string => Boolean(value));
}

function hostAllowed(host: string, allowedDomains: string[]): boolean {
  const normalized = host.toLowerCase();
  return allowedDomains.some((domain) => {
    const allowed = domain.trim().toLowerCase();
    return Boolean(allowed) && (normalized === allowed || normalized.endsWith(`.${allowed}`));
  });
}

function safeURL(value: string): URL | undefined {
  try {
    return new URL(value);
  } catch {
    return undefined;
  }
}

function sha256(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}

function buildFailureDiagnostic(request: ExecuteScriptRequest, failedNodeID: string, code: string, message: string): WorkerScriptFailureDiagnostic {
  const outputDir = request.output_dir || "artifacts/script-execution";
  const step = request.bundle?.plan_json?.steps?.find((candidate) => candidate.node_id === failedNodeID);
  return {
    schema_version: "demoops.script_failure_diagnostic.v1",
    failed_node_id: failedNodeID,
    ...(step?.order !== undefined ? { failed_step_order: step.order } : {}),
    error: { code, message, retryable: true },
    screenshot_refs: [
      {
        role: "failure_screenshot",
        kind: "webpage_screenshot",
        uri: `${outputDir}/failure-${safeID(failedNodeID)}.png.enc`,
        mime_type: "image/png",
        sha256: sha256(`failure_screenshot:${failedNodeID}`),
        encrypted: true,
        sensitive: true,
      },
    ],
    trace_refs: [
      {
        role: "failure_trace",
        kind: "browser_trace",
        uri: `${outputDir}/trace.zip.enc`,
        mime_type: "application/zip",
        sha256: sha256(`failure_trace:${failedNodeID}`),
        encrypted: true,
        sensitive: true,
      },
    ],
    console_events: [{ level: "error", message }],
    network_events: [],
    redaction_report: {
      applied: true,
      stripped_headers: ["authorization", "cookie"],
      stripped_storage_keys: ["localStorage", "sessionStorage"],
      full_html_included: false,
    },
    repair_hints: [
      {
        kind: "selector_repair",
        node_id: failedNodeID,
        summary: "使用失败截图、DOM 摘要和可访问性摘要修复 selector 或等待条件。",
        suggested_action: "在 App 端基于本地代码上下文重新生成脚本，并重新审批后上传。",
        confidence: 0.7,
      },
    ],
  };
}

function safeID(value: string): string {
  return value.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "") || "token";
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
