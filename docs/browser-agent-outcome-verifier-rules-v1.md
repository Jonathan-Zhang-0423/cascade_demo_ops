# Browser Agent OutcomeVerifier Rules v1

日期：2026-07-29

## 范围

本文件记录 `browser-agent-outline-v1` 默认 `OutcomeVerifier` 首版规则、失败码和严重级别。它只消费 Server 传入的脱敏 DTO：`BrowserAgentValidationContext`、`StageExecutionEvent`、`RecordingResultPackage`。它不读取 Playwright/page 对象，也不修改脚本、selector、route、浏览器会话或 App 已审批包。

## 决策

| decision | 含义 |
| --- | --- |
| `continue` | 当前阶段或结果包可继续。 |
| `stop_and_report` | 证据、身份、required 校验或交付一致性不成立，必须停止或转失败包。 |
| `repair_allowed` | 仅运行时 verifier/proposer 可以提出，是否应用由 Server Repair Policy 和 `patch_ledger` 裁决。 |
| `reunderstanding_required` | 预留给外部 Validation Agent adapter；默认 verifier 不直接返回。 |

## 失败码

| code | phase | severity | 触发条件 |
| --- | --- | --- | --- |
| `approved_contract_missing` | `pre_execution` | `blocking` | `StageApprovalPlan`、`ScriptOutline` 或 `BrowserAgentContract` 缺失。 |
| `approved_hash_missing` | `pre_execution` | `blocking` | 批准包 hash 或 Browser Agent policy hash 缺失。 |
| `approved_stage_plan_empty` | `pre_execution` | `blocking` | 批准 stage plan 或 outline 没有可执行 stage。 |
| `runtime_event_identity_mismatch` | `runtime_stage` | `blocking` | 阶段事件的 `run_id`、`source_package_id`、bundle hash 或 policy hash 与批准上下文不一致。 |
| `observed_stage_completion_missing` | `post_execution` | `blocking` | step 未通过、stage 未完成，或没有真实 `outcome_observed`。 |
| `runtime_stage_report_missing` | `post_execution` | `blocking` | 交付 step 缺少成功的 `runtime_stage` 验证报告。 |
| `observed_state_not_runtime_derived` | `post_execution` | `blocking` | `StepResult.observed_state` 不是由 runtime observation 生成的 `source=...` 摘要。 |
| `evidence_refs_missing` | `post_execution` | `blocking` | step 无法追溯到 runtime event evidence 或产物。 |

通过的检查使用 `severity=info`；失败的 required 检查使用 `severity=blocking`。

## 适配器运行时失败码

以下失败码由 `BrowserAgentOutcomeVerifierAdapter` 在消费真实阶段事件与结果包时产出，覆盖《Validation-Agent 新架构对接任务说明》§6 的固定验收场景。

| code | phase | severity | required | 触发条件 | 对应场景 |
| --- | --- | --- | --- | --- | --- |
| `MISSING_BUNDLE_HASH` | `pre_execution` | `blocking` | 是 | 批准上下文缺少 `SourceBundleHashSHA256`。 | 1 |
| `MISSING_POLICY_HASH` | `pre_execution` | `blocking` | 是 | 批准上下文缺少 `EffectivePolicyHashSHA256`。 | 1 |
| `EMPTY_STAGE_APPROVAL_PLAN` | `pre_execution` | `blocking` | 是 | `StageApprovalPlan` 为空或没有 stage。 | 1 |
| `MISSING_SCRIPT_OUTLINE` | `pre_execution` | `blocking` | 是 | `ScriptOutline` 缺失。 | 1 |
| `OUT_OF_ORDER_EVENTS` | `runtime_stage` | `blocking` | 是 | `stage_completed`/`stage_failed` 出现在 `stage_started` 之前。 | 8 |
| `DUPLICATE_STAGE_STARTED` | `runtime_stage` | `warning` | 否 | 同一 stage 出现多个 `stage_started`。 | 8 |
| `MISSING_OUTCOME_OBSERVED` | `runtime_stage` | `blocking` | 是 | stage 标记 completed 但缺少 `outcome_observed`。 | 8 |
| `DERIVED_FROM_PLAN_EVIDENCE` | `runtime_stage` | `blocking` | 是 | 观察结果来源为 `derived_from_plan`，非真实浏览器观察。 | 7 |
| `NO_OBSERVATION_EVIDENCE` | `runtime_stage` | `blocking` | 是 | 关键事件缺少 `observation` 字段。 | 9 |
| `REQUIRED_ASSERTION_FAILED` | `runtime_stage` | `blocking` | 是 | required 断言未通过。 | 3 |
| `STAGE_FAILED` | `runtime_stage` | `blocking` | 是 | 出现 `stage_failed` 事件。 | 2、4 |
| `CROSS_DOMAIN_ACCESS` | `runtime_stage` | `blocking` | 是 | 观察到的 URL host 不在 `vctx.AllowedDomains` 内。 | 6 |
| `FORBIDDEN_PAGE_ACCESS` | `runtime_stage` | `blocking` | 是 | 观察到的 URL 路径命中 `ScriptOutline.AllowedExplorationScope.ForbiddenPathPrefixes`。 | 6 |
| `MISSING_RESULT_PACKAGE` | `post_execution` | `blocking` | 是 | `RecordingResultPackage.ResultID` 为空。 | 10 |
| `RESULT_PACKAGE_MISMATCH` | `post_execution` | `blocking` | 是 | 结果包 `source_package_id` 与批准运行的包不一致。 | 11 |
| `RESULT_HASH_MISMATCH` | `post_execution` | `blocking` | 是 | `AuditTrail.SourcePackageDigest` 与批准包 `SourceBundleHashSHA256` 不一致。 | 11 |
| `REQUIRED_STAGE_NOT_COMPLETED` | `post_execution` | `blocking` | 是 | `StageApprovalPlan` 中的必需 stage 没有完成事件。 | 4 |
| `MISSING_EVIDENCE_REFS` | `post_execution` | `blocking` | 是 | required `outcome_observed` 事件缺少 `evidence_refs`；附加 `observation_collected` 缺少引用仍为 warning。 | 9 |
| `MISSING_TRACE_ARTIFACT` | `post_execution` | `warning` | 否 | 结果包缺少 trace 类产物。 | 10 |
| `MISSING_SCREENSHOTS` | `post_execution` | `warning` | 否 | 结果包缺少截图证据。 | 10 |
| `MISSING_MP4_VIDEO` | `post_execution` | `warning` | 否 | 结果包缺少 MP4 视频。 | 10 |
| `MISSING_STAGE_EVENT_LOG` | `post_execution` | `warning` | 否 | 结果包缺少 `stage_event_log_ref`。 | 10 |
| `STAGE_VALIDATION_FAILURE_THRESHOLD` | `post_execution` | `blocking` | 是 | 50% 以上 stage 验证失败，决策转为 `reunderstanding_required`。 | — |

跨域与禁止页面匹配采用与 `app` 包一致的语义：host 比较忽略大小写、去端口、支持子域；路径前缀按路径段边界匹配，`/billing` 不会命中 `/billing-faq`。

### 受限修复提案

场景 5（已批准 selector alternative 恢复）与场景 12（修复越权拒绝）由 `RepairProposalGenerator` 承担，不产出上表的失败码：

- 每个 `RuntimeRepairProposal` 必须携带 `run_id`、`node_id`、`stage_id`、源包哈希、策略哈希、修改前后内容、置信度与 `evidence_refs`；缺少 `node_id`/`stage_id` 的检查不生成提案。
- `IsRepairAllowed` 是最终闸门：仅当 repair kind 在 `allowed_repair_kinds` 内、字段不在 `immutable_fields` 与绝对禁止字段列表内时才放行。
- 绝对禁止字段包括业务输入、路由、成功判定、stage 顺序、权限边界、凭据与 `secret_ref`；命中即拒绝（场景 12）。

## 验收覆盖

首版规则由以下定向测试覆盖：

```bash
cd backend
go test ./internal/app -run 'Test(LocalBrowserAgentOutlineRunner|DeterministicBrowserAgentStageVerifier|BrowserAgentStepResults|OutlineRuntimeFullPath)' -count=1
go test ./internal/model -run 'Test(PreExecutionValidationAllowsOnlyApprovedPackageEvidence|ValidationCheckCarriesVersionedFailureCodeAndSeverity)' -count=1
```

适配器失败码与受限修复提案由以下测试覆盖：

```bash
cd backend
go test ./internal/orchestrator -count=1
go test ./internal/app -run 'TestControlledOutlineScenarioPackages' -count=1
```

其中 `internal/orchestrator` 覆盖三阶段适配器的全部失败码与修复提案门禁；`TestControlledOutlineScenarioPackages` 覆盖四个受控 outline 场景包（成功、locator 缺失、required 验证失败、构建未完成）的结构性与端到端验收。

新增的注解字段与 Replay Manifest 测试：

```bash
cd backend
go test ./internal/model -run 'TestAnnotateValidation|TestReplayManifest' -count=1
go test ./internal/app -run 'TestBuild.*ReplayManifest|TestBuildAndWrite' -count=1
```

## §10.3 三阶段 ValidationReport 示例

下面是一次失败执行中三个阶段报告的精简结构示例，展示每个阶段的 `phase`、`decision`、`checks` 结构和结构化反馈字段。

### 执行前报告（pre_execution）

```json
{
  "schema_version": "demoops.validation_report.v1",
  "report_id": "pre_critical_1786048000000000000",
  "run_id": "run_controlled_business",
  "source_package_id": "pkg_bundle_script_graph_...",
  "source_bundle_hash_sha256": "2e96309...",
  "policy_hash_sha256": "4459a29...",
  "phase": "pre_execution",
  "decision": "continue",
  "pass_rate": 1.0,
  "overall_confidence": 0.95,
  "evidence_quality": "browser_assertion",
  "checks": [],
  "created_at": "2026-08-07T12:00:00Z"
}
```

> 执行前全部通过时 `checks` 为空，`decision = continue`。若发现问题（如 `MISSING_BUNDLE_HASH`）则 `decision = stop_and_report`，`checks` 携带对应 `code`、`impact`、`suggestion`、`responsibility_domain`。

### 运行时阶段报告（runtime_stage）—— 失败场景

```json
{
  "report_id": "runtime_critical_1786048100000000000",
  "phase": "runtime_stage",
  "node_id": "business_stage_new_project_entry",
  "stage_id": "stage_step_01_business_stage_new_project_entry",
  "decision": "stop_and_report",
  "pass_rate": 0.0,
  "checks": [
    {
      "id": "runtime_stage_failed_stage_step_01_business_stage_new_project_entry_2",
      "kind": "stage_failure",
      "code": "STAGE_FAILED",
      "node_id": "business_stage_new_project_entry",
      "stage_id": "stage_step_01_business_stage_new_project_entry",
      "severity": "blocking",
      "passed": false,
      "required": true,
      "summary": "阶段 stage_step_01_business_stage_new_project_entry 执行失败: ...",
      "impact": "阶段执行失败，后续依赖阶段无法继续，执行停止。",
      "suggestion": "查看 failure_diagnostic 中的 error.code 和截图/trace 定位具体原因。",
      "next_step": "根据 failure_diagnostic.error.code 和 blocked_reasons 定位责任方，修复后重试。",
      "responsibility_domain": "server"
    }
  ]
}
```

### 执行后报告（post_execution）—— 正常场景

```json
{
  "report_id": "post_1786048200000000000",
  "phase": "post_execution",
  "decision": "continue",
  "pass_rate": 1.0,
  "overall_confidence": 0.97,
  "checks": [
    {
      "id": "post_no_stage_event_log",
      "kind": "artifact_completeness",
      "code": "MISSING_STAGE_EVENT_LOG",
      "severity": "warning",
      "passed": false,
      "required": false,
      "summary": "缺少 stage_event_log_ref，无法追溯阶段执行 JSONL",
      "impact": "缺少 stage_event_log，阶段时序无法重放，可追溯性受损。",
      "suggestion": "确认 Browser Agent 事件日志写入器（BrowserAgentEventLog）正常落盘 JSONL 文件。",
      "next_step": "由 Server Runtime 侧排查事件日志落盘逻辑。",
      "responsibility_domain": "server"
    }
  ]
}
```

> `warning` 级别的 check 不影响 `decision`，不阻断交付；`blocking` 级别的 check 将 `decision` 升为 `stop_and_report`。

## §10.4 可重放清单（Replay Manifest）示例

Replay Manifest 在每次执行结束时写入 `{EventDir}/replay-manifest.json`，并以 `kind="replay_manifest"`、真实 SHA-256/size 和 delivery descriptor 注册为产物。正式 Direct Worker 上传前把清单中的本地 URI 终结为当前 job 的认证 `direct://` artifact URI 并重新计算摘要；Gateway 会解析清单与 StageEventLog，而不是只相信 artifact kind/hash，复核 run/package/bundle/policy、stage、ValidationReports、事件序列和真实 outcome evidence。正式 Direct 路径在渲染与 StageEventLog 绑定完成后生成，因此清单引用的 StepResults、ValidationReports、原始录屏、trace、最终视频和阶段日志来自同一 job/package。以下是一次旁路测试失败后的精简示例：

```json
{
  "schema_version": "demoops.replay_manifest.v1",
  "manifest_id": "manifest_run_controlled_business",
  "created_at": "2026-08-06T20:17:10Z",

  "run_id": "run_controlled_business",
  "package_id": "pkg_bundle_script_graph_1785933490880486000",
  "bundle_hash_sha256": "2e96309596eed577440f977706d0d83faa838110a552150f6f9fa9fc54b0c051",
  "policy_hash_sha256": "4459a29cb7a6ab370cf883eb6326ce63f903f916ba23a8cb3240745fbf5465df",

  "dev_test_only": true,
  "waiver_id": "test_waiver_1786050844694253000",
  "waiver_allowed_node_ids": [
    "business_stage_new_project_entry",
    "business_stage_start_agent_build"
  ],
  "waiver_blocked_reasons": [
    "business_stage_new_project_entry: 安全域、来源或非破坏性约束不完整",
    "business_stage_start_agent_build: 安全域、来源或非破坏性约束不完整",
    "关键需求未完整映射到 stage 和证据"
  ],

  "status": "failed",
  "failed_node_id": "business_stage_new_project_entry",
  "final_decision": "stop_and_report",

  "stages": [
    {
      "node_id": "business_stage_new_project_entry",
      "stage_id": "stage_step_01_business_stage_new_project_entry",
      "order": 1,
      "status": "failed",
      "waived": true,
      "validation_decision": "stop_and_report",
      "observed_url": "http://127.0.0.1:5100/app",
      "observed_title": "Cascade AI — Build Apps with AI",
      "failure_code": "browser_agent_target_not_resolved",
      "failure_domain": "app",
      "evidence_artifact_ids": []
    }
  ],

  "validation_reports": [
    { "report_id": "pre_...", "phase": "pre_execution",  "decision": "continue",         "check_count": 0, "fail_count": 0 },
    { "report_id": "rt_...",  "phase": "runtime_stage",  "decision": "stop_and_report",  "check_count": 1, "fail_count": 1 }
  ],

  "raw_recording_uri": "file:///.../.cascade-dev/artifacts/.../recording/page@e5162cfa.webm",
  "browser_trace_uri":  "file:///.../.cascade-dev/artifacts/.../recording/browser-agent-trace.zip",
  "stage_event_log_uri": "file:///.../.cascade-dev/artifacts/.../execution/browser-agent-stage-events.jsonl",
  "manifest_uri":       "file:///.../.cascade-dev/artifacts/.../execution/replay-manifest.json",

  "execution_bundle_runtime": "browser-agent-outline-v1",
  "browser_runtime_version":  "chromium-1228"
}
```

**还原失败现场的步骤**（无需凭据）：

1. 定位 `stage_event_log_uri` 文件，按 `sequence` 排序重建事件时序。
2. 对应 `stages[].evidence_artifact_ids` 找截图与 trace。
3. 对照 `waiver_blocked_reasons` 与 `stages[].failure_code` 确认根因。
4. 使用 `bundle_hash_sha256` 验证原始包未被修改。
5. 查阅 `validation_reports[]` 的 `fail_count` 定位是哪个阶段的验证最先停止。
