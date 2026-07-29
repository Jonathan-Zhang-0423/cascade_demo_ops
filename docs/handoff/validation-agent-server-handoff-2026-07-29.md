# Validation Agent 与 Server Browser Agent 对接协议

日期：2026-07-29  
面向：明达（Validation Agent）与 Server Browser Agent 负责人  
协议基线：`docs/app-server-browser-agent-outline-protocol.md`  
运行时：`browser-agent-outline-v1`

## 1. 结论与目标

新主路径固定为：

```text
用户在 App 输入任务
-> App 按正式逻辑生成 ClientExecutionPackage
-> Exchange
-> Server Intake / Runtime Router
-> Browser Agent Outline Runner
-> Policy Guard / Stage Orchestrator / Outcome Verifier
-> Result Packager
-> StepResults、证据、Validation Reports、MP4
```

Validation Agent 是 Server 的受限验证器：它检查已批准的计划和真实运行证据，并可提出受限修复建议；它不执行浏览器动作，不修改 App 审批包，不决定越权操作，也不直接生成最终失败诊断或视频。

当前 Server 已具备新主路径的运行时接口、阶段事件、结果包字段、验证器注入点和本地真实浏览器执行能力。尚未完成的关键验收是：使用 App 正式入口生成的真实执行包，完整跑通 App -> Exchange -> Server -> 结果回传。

## 2. 联调唯一有效输入

联调只能使用 App 正式任务入口生成、并通过正式 Exchange 流程投递的：

```text
ClientExecutionPackage(runtime = browser-agent-outline-v1)
```

测试脚本可以代替用户点击“提交”，但必须调用与页面相同的请求 DTO、校验、默认值、任务状态机、审批记录、包生成和投递逻辑。

以下内容不得作为 App/Server 联调成功证明：

- Server 内部固定测试包；
- 手工修改后上传的 JSON；
- mock `ValidationReport`、`LegacyValidationReports` 或单元测试日志；
- 根据计划文本生成的 `derived_from_plan` StepResults；
- 仅由模型分析日志得到的结论。

Server 的 `dev-visible-browser-agent` 固定包入口仅用于本地回归：它可验证真实页面操作、脱敏截图和 MP4 编码，但绕过了 App 包生成与 Exchange，不得作为完整项目链路验收。

## 3. 双方责任边界

| 范围 | Server Browser Agent 负责 | Validation Agent 负责 |
| --- | --- | --- |
| App 包 | 接收、协议校验、hash 校验、编译只读运行计划。 | 不修改、重签或补全 App 包。 |
| 浏览器执行 | Policy Guard、目标解析、动作执行、截图/录屏/trace、阶段事件。 | 不读取 Playwright page、Cookie、Token、完整 DOM、密码或源码。 |
| 阶段判断 | 执行 required validation，并提供真实 observation/证据。 | 基于脱敏事件和结果作独立验证结论。 |
| 修复 | Repair Policy 审批、Action Executor 应用、Patch Ledger 审计。 | 只输出受限 RuntimeRepairProposal。 |
| 最终交付 | 结果包、failure_diagnostic、repair_request、素材与 MP4。 | 输出 ValidationReport；不能覆盖 Server 的最终产物或失败诊断。 |

## 4. 已冻结的 Server 接口

Server 已提供以下 Go 内部接口；明达应直接实现，不新增独立 REST API：

```go
type OutcomeVerifier interface {
    ValidateBeforeExecution(
        context.Context,
        BrowserAgentValidationContext,
    ) (model.ValidationReport, error)

    ValidateStageEvents(
        context.Context,
        BrowserAgentValidationContext,
        []model.StageExecutionEvent,
    ) (model.ValidationReport, error)

    ValidatePostExecution(
        context.Context,
        BrowserAgentValidationContext,
        model.RecordingResultPackage,
        []model.StageExecutionEvent,
    ) (model.ValidationReport, error)
}
```

注入点：

```go
Service.SetBrowserAgentOutcomeVerifier(verifier)
```

Server 在每次任务开始时取得 verifier 快照，防止任务中途被替换。

## 5. Validation Agent 的允许输入

### 5.1 执行前

`BrowserAgentValidationContext`：

- `run_id`、`source_package_id`；
- `source_bundle_hash_sha256`、`effective_policy_hash_sha256`；
- 只读 `workflow_graph`、`plan`、`stage_approval_plan`；
- 只读 `script_outline`、`browser_agent_contract`。

执行前只能用已审批、hash 绑定的包事实作为证据；此时还没有浏览器成功证据。

### 5.2 每个阶段

`StageExecutionEvent` 由 Server 追加写入 JSONL，包含：

- 身份关联：`run_id`、`node_id`、`stage_id`、`attempt`、单调递增 `sequence`；
- 绑定关系：原包 ID、bundle hash、policy hash；
- 事件：`stage_started`、`observation_collected`、`target_resolved`、`action_started`、`action_completed`、`outcome_observed`、`repair_proposed`、`repair_applied`、`stage_completed`、`stage_failed`；
- 脱敏 `RuntimeObservation`：来源、URL/标题、断言及通过状态；
- 脱敏 `evidence_refs`：截图、trace、产物等引用。

### 5.3 执行后

`RecordingResultPackage` 与全部阶段事件，包括：

- 真实 `step_results`；
- `validation_reports`；
- `patch_ledger`；
- 阶段 JSONL 引用；
- 录屏、截图、trace、最终 MP4 等产物引用；
- `failure_diagnostic` 与 `repair_request`（如失败）。

## 6. 输出合同与判定规则

### 6.1 ValidationReport

明达必须返回可通过 `model.ValidationReport.Validate()` 的：

```text
schema_version = demoops.validation_report.v1
phase          = pre_execution | runtime_stage | post_execution
decision       = continue | repair_allowed | stop_and_report | reunderstanding_required
```

每份报告必须携带包/运行/hashes、通过率、置信度、证据质量、结构化 `checks` 和 `evidence_refs`。

运行时与执行后阶段若返回 `continue`，证据质量必须来自：

```text
actual_browser_observation | browser_assertion | artifact_observation
```

不得把 `expected_outcome`、`success_state` 或 `derived_from_plan` 当作浏览器成功证据。

### 6.2 决策映射

| 决策 | Server 的固定行为 |
| --- | --- |
| `continue` | 仅在真实证据充分时继续。 |
| `repair_allowed` | 交给 Server Repair Policy 审批；批准后才执行并写入 Patch Ledger。 |
| `stop_and_report` | 停止当前及后续阶段，打包失败结果与诊断。 |
| `reunderstanding_required` | 停止当前及后续阶段，生成可供 App 重新理解/重新审批的 repair_request。 |

旧 Validation Agent 的“诊断不阻断”语义不适用于新主路径的 required 验证失败、越权或真实证据缺失场景。

### 6.3 RuntimeRepairProposal

明达可提出但不得应用修复。提案必须带：运行/阶段身份、两类 hash、修复类型、字段前后值、置信度、证据、是否需审批。

仅允许在 `browser_agent_contract.repair_policy` 已批准范围中提议：selector alternative、等待策略、capture timing、同域非破坏性探索路径、frame resolution。

绝对禁止提议：改业务输入、改路由、改成功定义、改阶段顺序、改权限边界、读写凭据、改 `secret_ref`、破坏性操作。

## 7. 结果包顺序与证据链

`RecordingResultPackage.validation_reports` 的固定顺序：

```text
1 份 pre_execution
-> 每个已执行 Stage 1 份 runtime_stage
-> 1 份 post_execution
```

一次真实联调必须能用同一 `source_package_id`、`source_bundle_hash_sha256`、`policy_hash_sha256` 串起：

1. App 投递回执；
2. Server Intake / Runtime Router 接受记录；
3. 阶段 JSONL；
4. 真实 StepResults；
5. Validation Reports；
6. raw recording、trace、截图、MP4 和最终结果裁决。

任何一项缺失，状态只能是“联调证据不完整”，不能称为通过。

## 8. LegacyValidationReports 的定位

明达现有 `feat/validation-agent` 的规则、风险分级、阈值、selector/wait/timing 建议可以作为规则实现的基础。

但 `LegacyValidationReports` 只用于旧兼容诊断或历史查看：

- 不参与新路径的通过、停止或修复授权；
- 不替代 `RecordingResultPackage.validation_reports`；
- 不得覆盖真实 StepResults、阶段 JSONL、Worker 证据或最终 failure_diagnostic；
- 不能以 mock 报告或 4/4 单元测试通过宣称 App/Server 实际联调完成。

## 9. 明达下一步工作建议

### P0：适配 OutcomeVerifier（首要）

1. 将现有 pre/post 诊断接到第 4 节的三个方法，不再依赖旧 pipeline 的可变包对象。
2. `ValidateBeforeExecution`：校验审批计划、outline、contract、hash、允许域/路由、required validation 是否完整；缺失时返回结构化 `stop_and_report`，不可静默 no-op。
3. `ValidateStageEvents`：只基于真实事件；必须识别乱序、重复、缺失 `outcome_observed`、required 断言失败、无证据、`derived_from_plan` 等情况，并禁止形成 `continue`。
4. `ValidatePostExecution`：交叉核验阶段事件、StepResults、验证报告与结果产物；确认必需 Stage 完成、observed_state 真实、证据可追溯、所要求的录屏/trace/截图/MP4 均存在。
5. 每个检查项输出稳定 `code`、`severity`、`required`、`evidence_refs` 与中文 `summary`。

### P1：受限修复提案

1. 将已有 selector/wait/timing 建议映射为 `RuntimeRepairProposal`。
2. 仅当审批包 repair policy 允许时，返回 `repair_allowed`。
3. 对跨域、修改业务输入/路由/顺序、读写凭据、破坏性操作，必须输出阻塞性拒绝结果。

### P2：共同真实包验收

App 侧须通过正式入口生成并投递以下包：

- 1 个成功包；
- 1 个 locator 缺失包；
- 1 个 required 验证失败包；
- 1 个等待超时或“构建未完成”包。

明达负责每包的预期 ValidationReport 断言；Server 负责执行、事件审计、结果打包与产物保存。只有成功包证据链完整、失败包正确停止且保留诊断，才可宣布 Validation Agent 已对接 Server 新主路径。

## 10. 给明达的简短说明

请将现有 Validation Agent 从“旧 pipeline 的诊断报告”迁移为 Server 新 Browser Agent Runtime 的 `OutcomeVerifier`。你只读取脱敏的审批上下文、真实阶段事件和结果包，输出结构化验证报告与受限修复建议；不操作浏览器、不改原始包、不直接修复。我们的共同目标不是让单元测试通过，而是让真实 App 生成的 `browser-agent-outline-v1` 包经过 Exchange 后，在 Server 上得到真实 StepResults、Validation Reports、审计证据和可验收的最终视频。
