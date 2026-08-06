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
| `MISSING_EVIDENCE_REFS` | `post_execution` | `warning` | 否 | `outcome_observed` 事件缺少 `evidence_refs`。 | 9 |
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
