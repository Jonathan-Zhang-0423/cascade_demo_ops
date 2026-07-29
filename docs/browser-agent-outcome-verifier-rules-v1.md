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

## 验收覆盖

首版规则由以下定向测试覆盖：

```bash
cd backend
go test ./internal/app -run 'Test(LocalBrowserAgentOutlineRunner|DeterministicBrowserAgentStageVerifier|BrowserAgentStepResults|OutlineRuntimeFullPath)' -count=1
go test ./internal/model -run 'Test(PreExecutionValidationAllowsOnlyApprovedPackageEvidence|ValidationCheckCarriesVersionedFailureCodeAndSeverity)' -count=1
```
