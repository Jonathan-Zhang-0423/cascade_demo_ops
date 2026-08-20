# App Feedback

Use this template only for findings whose `responsibility_domain` is `app` or whose normalized category is `app_package_contract`.

```text
Run/package:
Stage/node:
Finding code:
Severity / decision:
Why the App package is insufficient:
Evidence IDs or artifact IDs:
Reproduction conditions:
Minimum App fix:
Reapproval required: yes/no
Formal App-to-Server acceptance: not complete until rerun with the regenerated package
```

Rules:

- Do not ask Server to edit or fill the approved package.
- Do not include credentials, cookies, tokens, raw HTML, or raw form values.
- Use the exact finding code and evidence IDs from `ValidationRunReport`.

## APP-016：`source_digest` 格式不符合 Direct API v1 协议

Run/package: `unattended-app-server-e2e/20260820-122811/client_execution_package.json`

Stage/node: `package.executable_script_bundle.plan_json.steps[0].action.target.selector_alternatives[0]`

Finding code: `app_package_contract.selector_provenance.source_digest_format_invalid`

Severity / decision: 阻断；禁止正式 App→Server 验收，禁止修改原始包后继续执行。

Why the App package is insufficient: 包内 `source_digest` 使用了 `sha256:` 前缀（例如 `sha256:95ba...`），而 Direct API v1 要求该字段为纯 64 位小写十六进制摘要（例如 `95ba...`）。Server 不能在接收后剥离前缀，因为这会改变原包字节、包哈希及审批绑定。

Evidence IDs or artifact IDs: project `proj_1787228892086920800`; package `D:\Engine-7-8\artifacts\dev-test-only\unattended-app-server-e2e\20260820-122811\client_execution_package.json`; Server gate error `is missing required selector provenance`。

Reproduction conditions: 使用该不可变原始包进入 Direct API v1 正式门禁时稳定复现；Gateway、Worker、TLS、登录和模型就绪状态均不是本次阻断原因。

Minimum App fix: App 重新生成原始包时，将所有 selector provenance 的 `source_digest` 输出为不带 `sha256:` 前缀的 64 位小写十六进制字符串，并重新计算包哈希、重新审批。不得由 Server 修补原包。

Reapproval required: yes

Formal App-to-Server acceptance: not complete until rerun with the regenerated package
