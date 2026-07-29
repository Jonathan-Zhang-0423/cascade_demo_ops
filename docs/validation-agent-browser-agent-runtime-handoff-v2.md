# Validation Agent 与 Browser Agent Runtime 对接说明（Server 侧）

日期：2026-07-29
状态：Server 新主路径、三段验证和 `OutcomeVerifier` 注入点均已实现；本地真实浏览器执行、阶段审计、结果包与 MP4 渲染已验证。**但尚未完成“真实 App 产出包 -> Exchange -> Server -> 结果回传”的端到端验收；未拿到真实 App 包前，不得把 Server 内部固定包或 mock 报告当作联调通过证明。**

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

## 8. 2026-07-29 对接更新：真实包与真实结果的硬门槛

本节覆盖此前仅靠固定 fixture、单元测试或演示报告无法证明的部分。它不改变 `docs/app-server-browser-agent-outline-protocol.md` 的 App -> Server 包格式，也不向 App 增加字段；它只规定何时可以声称 Validation Agent 已完成新主路径对接。

### 8.1 唯一有效的联调输入

有效联调只能使用 App 正式任务入口生成、并由 App 按正式 Exchange 流程投递的 `browser-agent-outline-v1` `ClientExecutionPackage`。测试自动化可以代替用户点击“提交”，但必须调用与页面完全相同的请求 DTO、校验、默认值、任务状态机、审批记录、包生成和投递逻辑。

以下数据不得作为联调输入或成功证据：

- Server 内部固定测试包；
- 手工编辑后再上传的 JSON 包；
- `LegacyValidationReports`、mock `ValidationReport` 或由计划推导的全通过 `StepResult`；
- 只包含单元测试日志、Claude/其他模型对日志的分析文本、或缺少 `workflow_graph` 的测试对象。

Server 的 `dev-visible-browser-agent` 固定包入口仅用于本地开发回归：它可验证登录后的真实网页操作、脱敏截图与 MP4 编码，但不经过 App 包生成和 Exchange 投递，永远不得写入生产结果、不得作为 App/Server 联调验收结论。

### 8.2 一次真实联调必须具备的证据链

同一个 `source_package_id`、`source_bundle_hash_sha256` 和 `policy_hash_sha256` 必须贯穿下列对象，且相互一致：

1. App 投递回执：`package_id`、运行时为 `browser-agent-outline-v1`、包哈希、投递时间；
2. Server Intake/Runtime Router 记录：通过协议校验、编译出的只读 Stage Plan、拒绝或接受原因；
3. `browser-agent-stage-events.jsonl`：每个 Stage 的单调 `sequence`、真实 `outcome_observed`、脱敏证据引用；
4. `RecordingResultPackage.step_results`：`observed_state` 必须来自真实运行时 observation，而不是 `expected_outcome`/`success_state`；
5. `RecordingResultPackage.validation_reports`：固定顺序为 1 份 `pre_execution`、每个已执行 Stage 1 份 `runtime_stage`、1 份 `post_execution`；
6. 产物与交付：raw recording、trace、截图、最终 MP4、checksum/引用，以及最终 `completed` 或 `failed` 裁决。

任何一项缺失时，结果只能标记为“联调证据不完整”，不能标记为通过。对手动登录的本地可见测试，登录阶段可不采集录屏/trace，但结果包必须明确该豁免；它不能替代正式包的录屏与 trace 要求。

### 8.3 Validation Agent 当前接入职责

明达的 Validation Agent 应以现有 `OutcomeVerifier` 接口接入，而不是再向 Server 提供独立 REST API 或向 App 要求新上传字段：

```go
type OutcomeVerifier interface {
    ValidateBeforeExecution(context.Context, BrowserAgentValidationContext) (model.ValidationReport, error)
    ValidateStageEvents(context.Context, BrowserAgentValidationContext, []model.StageExecutionEvent) (model.ValidationReport, error)
    ValidatePostExecution(context.Context, BrowserAgentValidationContext, model.RecordingResultPackage, []model.StageExecutionEvent) (model.ValidationReport, error)
}
```

Server 现有注入点为 `Service.SetBrowserAgentOutcomeVerifier(verifier)`。每个任务开始时 Server 会获取 verifier 快照；Validation Agent 只接收不可变、脱敏的 `BrowserAgentValidationContext`、`StageExecutionEvent` 和 `RecordingResultPackage`，绝不接触 Playwright `page`、Cookie、密码、Token、完整 DOM/HTML 或源码。

### 8.4 判定与权限边界（本次必须确认）

| Validation Agent 决策 | Server 行为 | 明达不得做的事 |
| --- | --- | --- |
| `continue` | 仅当 runtime/post 阶段具有真实浏览器或产物证据时继续。 | 用计划文案、mock 数据或 `derived_from_plan` 伪造成功。 |
| `repair_allowed` | Server 继续交给 Repair Policy 审批；仅批准后由 Action Executor 执行并写入 `patch_ledger`。 | 直接修改脚本、selector、浏览器会话或原始包。 |
| `stop_and_report` / `reunderstanding_required` | Stage Orchestrator 停止后续阶段；Result Packager 生成失败结果与 `repair_request`。 | 把失败报告当成“仅提示、不影响交付”，或继续执行后续业务动作。 |

允许提议的修复仅限已批准策略中的 selector alternative、等待策略、capture timing、同域且非破坏性的探索路径或 frame resolution。不得改变业务输入、路由目标、成功定义、Stage 顺序、权限边界、`secret_ref` 或任何破坏性标记。

### 8.5 `LegacyValidationReports` 的最终定位

明达当前 `feat/validation-agent` 中的 pre/post 诊断、阈值计算、风险分级、selector/wait/timing 建议和测试可以复用为规则实现素材；但其 `LegacyValidationReports` 输出只保留为兼容诊断/历史查看：

- 不参与新主路径的通过、停止或修复授权；
- 不得替代 `RecordingResultPackage.validation_reports`；
- 不得写入或覆盖真实 `StepResults`、Stage JSONL、Worker 证据、`failure_diagnostic`；
- 不能以“测试 4/4 通过”或 mock 演示报告宣称真实 App/Server 联调完成。

### 8.6 请明达下一步交付的内容（按优先级）

**P0：把现有 Validation Agent 适配为 `OutcomeVerifier`。**

1. 用 `BrowserAgentValidationContext` 替代旧 pipeline 的可变包对象；输入缺少 `StageApprovalPlan`、`ScriptOutline`、`BrowserAgentContract` 或 hash 时，返回结构化 `stop_and_report`，不可静默 no-op。
2. `ValidateStageEvents` 只消费 Server 事件流；要求 `outcome_observed`、真实 observation source、required validation 的断言和证据引用都存在。事件乱序、重复、缺失、无证据或 `derived_from_plan` 必须不能形成 `continue`。
3. `ValidatePostExecution` 对 `StepResults`、事件、产物和报告做交叉核验：必需 Stage 完成、观察来自 runtime、证据可追溯、raw recording/trace/截图/最终视频满足包中要求。返回报告必须通过 `model.ValidationReport.Validate()`。
4. 给每个检查项补齐稳定的 `code`、`severity`、`required`、`evidence_refs` 和中文可读 `summary`；不得只输出自然语言建议。

**P1：输出受限修复提案，不负责应用。**

1. 将旧的 selector/wait/timing 建议映射为 `RuntimeRepairProposal`；每个提案带 `run_id`、`node_id`、`stage_id`、两类 hash、前后值、置信度和证据引用。
2. 仅在当前 `BrowserAgentContract.repair_policy` 明确允许时返回 `repair_allowed`；否则返回 `stop_and_report` 或 `reunderstanding_required`。
3. 对任何试图跨域、改业务输入、改路由、改顺序、接触凭据或进行破坏性操作的提案，显式拒绝并输出阻塞错误码。

**P2：与 Server 共同建立真实包验收。**

1. 由 App 正式任务入口产生 1 个成功包、至少 3 个失败包（locator 缺失、required 验证失败、等待超时/构建未完成）；每个包通过 Exchange 进入 Server。
2. 明达为每个包给出预期 `ValidationReport` 断言；Server 负责执行、保存 JSONL/产物并比对结果包。
3. 只有成功包具备第 8.2 节完整证据链，且失败包被正确停止并保留诊断，双方才可宣布新路径 Validation Agent 联调通过。

### 8.7 Server 侧承诺与待办

Server 侧负责维持 `OutcomeVerifier` 注入点、提供脱敏 DTO、执行 Policy Guard/Repair Policy、写入事件审计与 Patch Ledger、生成最终 `failure_diagnostic` 和结果包，并为 P2 的真实 App 包运行端到端验收。

Server 当前待办是接收并运行真实 App 的 `browser-agent-outline-v1` 包；在此之前，Server 不会再用固定内部包替代 App 包，也不会把固定包的 MP4 当作完整项目链路的验收结论。

### 8.8 App 查询结果时的验证摘要

Server 会在现有 `ExecutionPackageStatusResponse.result_summary.validation` 中返回脱敏验证摘要，供 App 的状态页或验收页展示。该字段不增加 App 上传字段，也不包含页面正文、完整 DOM、Cookie、Token 或凭据：

```json
{
  "runtime": "browser-agent-outline-v1",
  "status": "complete",
  "validation_report_count": 5,
  "pre_execution_report_count": 1,
  "runtime_stage_report_count": 3,
  "post_execution_report_count": 1,
  "latest_decision": "continue",
  "real_observed_step_count": 3,
  "stage_event_log_available": true
}
```

`status=complete` 仅表示验证报告、真实观察步骤和阶段审计日志齐全；最终是否可以交付仍须结合既有 `result_status`、raw recording/trace/截图/MP4 的 `deliverables` 与 checksum 判断。任何 `derived_from_plan` 的 `observed_state` 都不计入 `real_observed_step_count`。
