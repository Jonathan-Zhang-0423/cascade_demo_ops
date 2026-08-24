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

## APP-017: formal package loses the required project-idea interaction and falls back to page-only source binding

Run/package: `unattended-app-server-e2e/20260821-122438/client_execution_package.json`

Stage/node: `business_stage_project_name_input` and `business_stage_start_agent_build`

Finding code: `app_package_contract.critical_requirement_not_mapped_to_executable_stage`

Severity / decision: blocking; formal App-to-Server execution must not start.

Why the App package is insufficient:

- The requested project-idea input is a required business action, but the emitted `fill` interaction has an empty selector and no selector alternatives. The audit therefore reports `project_idea_fill_present=false` and `project_idea_exact_value_present=false`.
- The corresponding Build stage is retained but also has an empty selector. The page annotation evidence contains the intended textarea and Build test IDs, yet the final executable outline does not carry those bindings.
- The package reports `requirement_coverage=0.75` and the unscoped blocking reason “critical requirement is not mapped to a stage”. An unscoped blocker cannot be waived as a node-level local test exception and must not be converted by Server into an executable action.
- The local repository was read into code snapshots, but source/page identity remains `status=unverified`, `effective_mode=page_only`, with conflicting product-name signals. This does not satisfy a test that claims the final plan is bound to the current local App source.

Evidence IDs or artifact IDs:

- Project: `proj_1787315078315412800`
- Package: `pkg_bundle_script_graph_1787315293651325500`
- Raw package: `artifacts/dev-test-only/unattended-app-server-e2e/20260821-122438/client_execution_package.json`
- Audit: `artifacts/dev-test-only/unattended-app-server-e2e/20260821-122438/app-package-outline-audit.json`
- Existing page evidence: `evidence_new_project_dialog_after`, `annotation_project_idea_input`, `annotation_project_create_button`
- Source-binding assessment: `a30e794606150f052dafcfbd7a0f382c30bf0a46f96856b594d72d8edac33dc2`

Reproduction conditions: generate a formal package for `http://127.0.0.1:5000/app` from `D:\备份Cascade-main\Cascade-main` with the required flow New Project → fill project idea → Build. The result is deterministically blocked before Browser Agent execution.

Minimum App fix:

1. Preserve the App-approved page-evidence bindings for the project-idea textarea and Build control when compiling `stage_approval_plan`, `script_outline`, and executable interactions; include the exact non-secret project-idea input reference/value binding.
2. Emit complete selector provenance for both controls, including a pure lower-case 64-character `source_digest` (see APP-016), evidence ID/ref binding, observed role/name, and timestamp.
3. Resolve source/page identity or require an explicit App-side mixed-source confirmation before generation, then regenerate all digest-bound package fields and require fresh human approval.

Reapproval required: yes

Formal App-to-Server acceptance: not complete until a regenerated package is `readiness=ready`, `requirement_coverage=1`, source-bound as approved, and passes Direct v1 intake unchanged.
