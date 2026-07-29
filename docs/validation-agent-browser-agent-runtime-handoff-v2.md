# Validation Agent 与 Browser Agent Runtime 对接说明（Server 侧）

日期：2026-07-27  
状态：Server 新主路径、三段验证和 `OutcomeVerifier` 注入点均已实现；定向与全量后端回归已通过。

## 1. 对接结论

Server 不会把 Validation Agent v1 的 `LegacyValidationReport` 或自动修复逻辑接入新主路径。新主路径固定为 `browser-agent-outline-v1`，验证器通过 `OutcomeVerifier` 接收脱敏的阶段事件和结果包，并由 Server 的 Repair Policy 决定是否允许任何运行时补丁。

这保证 Validation Agent 只能报告和提出建议，不能改写 App 已审批的业务目标、阶段顺序、输入语义、安全边界或原执行包。

## 2. 已接通的三段验证

| 阶段 | 触发点 | 输入 | 输出和行为 |
| --- | --- | --- | --- |
| `pre_execution` | 打开浏览器前 | hash 绑定的 `StageApprovalPlan`、`ScriptOutline`、`BrowserAgentContract` | 生成 `ValidationReport`；结构或策略不成立则阻断启动。 |
| `runtime_stage` | 每个真实 `outcome_observed` 事件后 | 当前 Stage 的真实观察、断言、证据引用、`run_id/node_id/stage_id/attempt` | 生成阶段报告；required 验证失败立即停止后续 Stage。 |
| `post_execution` | 全部阶段执行并打包后、交付前 | 全量真实阶段事件、`StepResults`、产物和执行结果包 | 生成汇总报告；任何阶段没有真实观察或报告为停止时，结果包改为失败包并附带诊断与 `repair_request`。 |

`RecordingResultPackage.validation_reports` 的顺序固定为：1 份执行前报告、每 Stage 1 份运行时报告、1 份执行后报告。

## 3. 明达可依赖的真实数据

每个 `StageExecutionEvent` 由 Server 在执行过程中写入 JSONL 审计文件，并包含：

- `run_id`、`node_id`、`stage_id`、`attempt`、单调递增 `sequence`、真实时间；
- 事件类型：开始、观察、目标解析、动作开始/完成、结果观察、修复提议/应用、完成/失败；
- 脱敏的 `RuntimeObservation`：来源、URL/标题是否观察到、断言种类与通过状态；
- 截图/trace 等 `evidence_refs`。

`StepResult.observed_state` 现在只由真实 `outcome_observed` 事件生成，例如 `source=browser_assertion; assertion:required_text_contains:save=passed`。它不再回填 App 的 `success_state` 文案。

## 4. 严格边界

- `required` 断言不通过、缺失真实证据、审计日志写入失败，均不能继续交付成功结果。
- Validation Agent 若希望提出修复，只能生成 `RuntimeRepairProposal`；是否应用由 Server Repair Policy 判定，并写入 `patch_ledger`。
- 未被 App 批准的 selector、跨 origin、跨 route、禁止页面和破坏性操作，Server 一律拒绝。
- `legacy_validation_reports` 只保留给明达 v1 的旧兼容诊断，不参与新 Browser Agent 的放行和修复决策。

## 5. 当前验收事实

已完成 Worker 的真实 Chromium 固定验收：

1. 导航、目标识别、点击和 required 成功校验；
2. 目标语义不匹配时点击前停止；
3. locator 缺失时停止；
4. 仅使用 App 已批准的 selector alternative；
5. 页面繁忙时仅做有上限的等待修复；
6. required validation 失败时停止；
7. 录屏、截图和 trace 产物留存。

验收产物目录：`artifacts/browser-agent-acceptance/strict-runtime/`。

本机已通过可写 Go 缓存完成以下定向回归：

```powershell
cd D:\Engine-7-8\backend
go test ./internal/app -run 'Test(LocalBrowserAgentOutlineRunner|DeterministicBrowserAgentStageVerifier|BrowserAgentStepResults|OutlineRuntimeFullPath)' -count=1
go test ./internal/model -run TestPreExecutionValidationAllowsOnlyApprovedPackageEvidence -count=1
```

另外，Worker 侧已实际执行并通过以下检查：

| 检查项 | 结果 | 说明 |
| --- | --- | --- |
| `pnpm --filter @cascade/video-worker typecheck` | 通过 | TypeScript 类型检查通过。 |
| `pnpm --filter @cascade/video-worker test` | 通过 | 14 个测试通过，2 个按环境条件跳过。 |
| 真实 Chromium 固定验收 | 通过 | 7 个场景全部通过；报告为 `artifacts/browser-agent-acceptance/strict-runtime/acceptance-report.json`。 |

因此，当前结论是：Worker 的真实浏览器闭环、Go 运行时三段验证及 Server 受控协议全链路均已有验收事实。它们仍不等同于真实客户网站、生产密文解密或生产隔离环境已验收。

## 6. 给明达的下一步接口约定

若明达继续开发 Validation Agent，应实现新 `OutcomeVerifier` 的三个方法：

```go
ValidateBeforeExecution(ctx, approvedContext) (ValidationReport, error)
ValidateStageEvents(ctx, approvedContext, stageEvents) (ValidationReport, error)
ValidatePostExecution(ctx, approvedContext, resultPackage, allEvents) (ValidationReport, error)
```

约束：输入只使用上述脱敏 DTO；返回 `continue`、`repair_allowed`、`stop_and_report` 或 `reunderstanding_required`。`repair_allowed` 必须另行提供受限 `RuntimeRepairProposal`，不能直接改执行脚本或浏览器对象。

## 7. 建议明达负责的下一步开发（新主路径）

本节是给明达的明确工作边界。目标是把 Validation Agent 接入已实现的 `browser-agent-outline-v1` 新主路径，而不是继续扩展旧的 `LegacyValidationReport` 自动修复路径。

### 7.1 第一优先级：实现 `OutcomeVerifier` 首版

明达负责按第 6 节的三个方法实现一个可注入的 `OutcomeVerifier`，并完成以下判断规则：

1. `pre_execution`：确认批准包、阶段计划和运行约束的 hash/引用一致；发现缺失批准、越权域名/路径、必需证据缺失时返回 `stop_and_report`。
2. `runtime_stage`：基于 Server 写入的真实 `StageExecutionEvent` 与 `RuntimeObservation` 判断当前 Stage 是否达到批准的成功条件；`required` 失败必须停止。
3. `post_execution`：检查每个必需 Stage 是否有真实观察、`StepResults.observed_state` 是否来自运行时、证据引用是否可追溯，并产出汇总 `ValidationReport`。

明达不得从 App 的计划文本或 `success_state` 推断“已成功”，也不得把自身推断写回为真实浏览器事实。

### 7.2 第二优先级：规则、失败码与修复建议

明达负责定义并版本化验证规则、失败码、严重级别和面向开发者的诊断文案；对可恢复问题可生成 `RuntimeRepairProposal`。提案必须说明：触发的 Stage、观察到的证据、建议的受限修复类型、风险与预期验证方式。

Server 负责对提案进行权限判断、实际执行、`patch_ledger` 审计和最终放行/失败裁决。明达不能直接修改脚本、selector、导航目标或浏览器会话。

### 7.3 第三优先级：共同固定验收包

双方共同维护脱敏的 `browser-agent-outline-v1` fixture、每个场景的预期 `ValidationReport`、失败码及结果包断言。明达重点补齐以下验收用例：正常通过、语义冲突、locator 缺失、允许的 selector alternative、等待超限/修复、required 验证失败，以及交付前汇总失败。

验收通过的定义是：验证报告、真实 `StepResults`、JSONL 事件、截图/trace 引用及结果包结论相互一致；不是仅凭 Validation Agent 返回“通过”。

### 7.4 交付物与协作顺序

明达首版交付应包括：`OutcomeVerifier` 实现、规则/失败码说明、固定 fixture 的预期结果、自动化测试及对异常输入的处理。Server 已提供 `Service.SetBrowserAgentOutcomeVerifier(verifier)` 注入点：每个 Outline 任务开始时获取一份固定快照，避免任务中途替换验证器。Server 随后负责注入实现、运行端到端固定验收、审计事件与结果包字段核对。

建议顺序为：先对齐 fixture 和预期结果，再实现 `pre_execution`，然后接入 `runtime_stage`，最后完成 `post_execution` 与端到端验收。任何旧 `legacy_validation_reports` 输出都只能保留为诊断对照，不能成为新主路径的放行依据。
