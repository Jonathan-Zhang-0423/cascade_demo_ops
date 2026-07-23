# Validation Agent 与 Server Browser Agent 新架构对接说明 v1

> 日期：2026-07-22<br>
> 对接范围：Server 侧 Validation Agent 与 `browser-agent-outline-v1` 运行时<br>
> 规范依据：`docs/app-server-browser-agent-outline-protocol.md`<br>
> 关联清单：`validation-agent-completeness-and-mengyang-handoff-zh.md`<br>
> 文档性质：Server 内部实现对接说明，不新增 App 侧协议要求，不替代或修改既有 App ↔ Server 协议

## 1. 结论

Validation Agent 不以旧的 `playwright-restricted-sandbox` 固定 TypeScript 执行路径作为新接入目标。它直接面向未来的 `browser-agent-outline-v1` 事件化运行时，定位为：

- 执行前风险诊断的提供者；
- 每个业务阶段的 `Outcome Verifier`（结果验证器）；
- 执行结束后的质量报告提供者；
- `RuntimeRepairProposal`（运行时修复建议）的提供者。

Validation Agent 不负责浏览器执行，不直接应用补丁，不修改 App 已审批的执行包，不改变业务目标、阶段顺序、输入语义或安全边界，也不覆盖 Result Packager 生成的失败诊断。

当前同事清单中已经实现的预检、执行后五维分析、反馈分类、安全受限的修复提示和阈值决策可以保留；其最终集成点应从旧执行函数中的 pre/post 注入，演进为新 Browser Agent 的阶段事件接口。

## 2. 唯一协议基线

本对接必须遵守 `docs/app-server-browser-agent-outline-protocol.md`，特别是以下既定规则：

1. App 输出 `approval_markdown`、`stage_approval_plan`、`script_outline`、`agent_prompt_policy`、`browser_agent_contract`、`security_policy` 和对应 hash。
2. `script_outline` 是受约束路线图，不是最终 TypeScript 脚本；Server 可以修正 selector、等待策略、截图时机和同域非破坏性探索路径。
3. Server 不得修改用户需求、阶段顺序、阶段目标、业务意图、动作语义、输入语义值、`secret_ref`、允许域名、禁止页面、破坏性标记和脱敏策略。
4. `browser-agent-outline-v1` 允许 `playwright_script.inline_source` 为空，不得因缺少 TypeScript 而拒绝。
5. `stage_approval_plan` 与 `script_outline` 必须通过 `node_id` 对齐，并与 `plan_json`、Workflow Graph 对齐。
6. 页面事实与 Stage 或 `target_contract` 冲突时必须 `stop_and_report`，不得通过调整验证阈值继续执行。
7. Intake 必须校验 schema、身份、hash、域名和安全策略；Validation Agent 不重复定义或放宽这些规则。
8. 所有诊断和验证数据只引用脱敏 `evidence_refs`，不得包含完整 DOM、HTML、源码、Cookie、Authorization、Storage secret 或输入框敏感值。

本文件定义的 `StageExecutionEvent`、`ValidationReport` 和 `RuntimeRepairProposal` 是 Server 内部运行时对象及结果包扩展。它们不是 App 上传三合一包的新必填字段。

## 3. 新架构位置

```text
App ClientExecutionPackage
  -> Exchange Intake
  -> Outline Package Validator
  -> Runtime Router
  -> Browser Agent Outline Runner
       -> Policy Guard
       -> Stage Orchestrator
       -> Observation / Locator Resolver
       -> Action Executor
       -> Outcome Verifier                 <- Validation Agent 主接入点
       -> Repair Policy
       -> Patch Ledger
       -> Recorder / Trace Collector
  -> Result Packager
  -> Editor Session Materializer
```

旧 `playwright-restricted-sandbox` Runner 只作为历史包兼容路径保留，不作为本轮 Validation Agent 新接口的设计基准。两条 Runtime 可以在未来共享通用的结果包、录屏、产物存储和脱敏组件，但不得通过把 Outline 包送入旧 TypeScript Worker 来实现兼容。

## 4. 组件责任边界

| 组件 | 负责 | 不负责 |
| --- | --- | --- |
| Exchange Intake | envelope、payload、schema、hash、身份、域名、安全策略和 runtime 准入 | 不执行浏览器动作，不做页面结果判断 |
| Runtime Router | 按 `script_manifest.runtime` 选择 Runner | 不把 Outline 包降级为旧 TypeScript 包 |
| Policy Guard | 在每个动作和修复前执行协议权限检查 | 不根据模型建议放宽 App/Server 安全基线 |
| Stage Orchestrator | 按已审批顺序推进 Stage、发出事件、控制停止和重试 | 不重排、跳过或插入业务 Stage |
| Observation / Locator Resolver | 采集脱敏页面观察，解析语义等价控件 | 不修改业务目标和输入语义 |
| Action Executor | 执行通过 Policy Guard 的白名单动作 | 不接受 Validation Agent 直接操作浏览器 |
| Validation Agent / Outcome Verifier | 验证真实结果、评估证据质量、输出决策和修复建议 | 不直接应用补丁，不改原包，不生成第二份最终失败诊断 |
| Repair Policy | 根据合同和置信度批准或拒绝修复建议 | 不修改不可变字段 |
| Patch Ledger | 记录修复前后值、证据、审批、应用和验证结果 | 不反向改写 App 包及其 hash |
| Result Packager | 汇总最终状态、失败诊断、验证报告、Patch Ledger 和产物 | 不让不同生产者相互覆盖诊断字段 |

### 4.1 两人个人责任边界

#### 孟洋负责（Server Browser Agent 主链）

- 维护 Exchange Intake、Outline Package Validator 和 Runtime Router，保证不同 runtime 进入正确执行器；
- 建设 Browser Agent Outline Runner、Stage Orchestrator、Observation / Locator Resolver 和 Action Executor；
- 建设 Policy Guard、Repair Policy 与 Patch Ledger，决定修复提案能否应用并记录完整过程；
- 维护 Recorder / Trace Collector、Result Packager、素材落库和 Editor Session Materializer；
- 负责 Server 运行状态、错误码、生产隔离、凭据代理、产物脱敏、接口联调和最终合并；
- 不替 Validation Agent 编造验证结论，也不修改 App 已审批的业务事实和 hash。

#### Validation Agent 同事负责（Outcome Verifier）

- 实现执行前、阶段运行中、执行后三个阶段的验证逻辑；
- 消费本文件约定的 `StageExecutionEvent`，输出 `ValidationReport` 和可选的 `RuntimeRepairProposal`；
- 维护 pass rate、confidence、evidence quality、反馈分类和阈值计算，并覆盖缺证、乱序、重复事件等测试；
- 保证计划派生信息不被当成真实成功证据，所有结论仅引用脱敏 `evidence_refs`；
- 不直接操作浏览器、不应用补丁、不修改原包、不决定最终失败诊断。

#### 双方共同确认

- 共同冻结 DTO 字段、枚举、错误语义、fixture 和版本升级规则；
- 共同确认 `fine_tune` 到新决策枚举的映射以及 required validation 的最低证据要求；
- 共同完成一条 Outline fixture 的端到端联调，但各自只修改本人所有的模块；
- 协议存在歧义时先更新本说明并评审，任何一方都不能在代码中私自扩大 App 或另一模块的责任。

### 4.2 文件与模块所有权

| 文件或模块 | 主负责人 | 变更规则 |
| --- | --- | --- |
| `docs/app-server-browser-agent-outline-protocol.md` | App / Server 协议共同基线 | 本次对接只遵守，不由任一 Server 子模块单方面改写 |
| `backend/internal/model/browser_agent_runtime.go` | 双方共同合同，孟洋负责合并 | 字段和枚举变更必须双方确认并补 JSON fixture 测试 |
| `backend/internal/app/exchange_runtime_router.go` | 孟洋 | Validation Agent 不直接修改路由或旧 Runner 兼容规则 |
| `backend/internal/app/browser_agent_outline_runner.go` 及 Stage 执行链 | 孟洋 | 对外只发布合同事件，不暴露 Playwright 对象和凭据 |
| Validation Agent 的 verifier 实现与单元测试 | Validation Agent 同事 | 只通过合同接口接入，不反向依赖 `exchange_runner.go` 或 TypeScript Worker |
| Repair Policy、Patch Ledger、Result Packager | 孟洋 | Validation Agent 只能提交 Proposal 或 Report，不能直接写最终结果 |
| 跨模块联调 fixture | 双方共同维护 | fixture 不新增 App 上传必填字段，不包含真实凭据或完整页面源码 |

### 4.3 联调交付顺序

1. 孟洋先交付 DTO、Runtime Router、Outline Runner 接口和一份脱敏 fixture；
2. Validation Agent 同事基于 DTO 完成适配器，不把旧 `playwright-restricted-sandbox` 注入点带入新主链；
3. 双方用同一 fixture 校验事件输入、报告输出、修复提案和停止决策；
4. 孟洋接入 Repair Policy、Patch Ledger 与 Result Packager，完成最终端到端测试；
5. 未完成某一环节时返回稳定的结构化平台错误，不降级到旧 Worker，也不伪造成功结果。

## 5. Validation Agent 的输入

### 5.1 执行前输入

Validation Agent 读取 Intake 已验收的不可变快照：

- `workflow_graph`；
- `plan_json`；
- `stage_approval_plan`；
- `script_outline`；
- `agent_prompt_policy`；
- `browser_agent_contract`；
- `security_policy`；
- `recording_run_spec`；
- `reproducibility.bundle_hash_sha256` 及各子对象 hash。

执行前检查只产生补充风险诊断。schema 缺失、hash 不匹配、越域、禁止页面或合同字段非法仍由 Intake 结构化拒绝，Validation Agent 不得把协议硬错误降级为 warning。

对 `browser-agent-outline-v1`，`StageApprovalPlan` 为空或 `stages=[]` 必须由 Intake 拒绝，不允许沿用旧兼容逻辑“静默 no-op”。

### 5.2 运行时输入

Validation Agent 消费由 Stage Orchestrator 发布的真实 `StageExecutionEvent`。它不直接读取 Playwright 对象，也不依赖固定 TypeScript 源码。

事件最小类型：

```text
stage_started
observation_collected
target_resolved
action_started
action_completed
outcome_observed
repair_proposed
repair_applied
stage_completed
stage_failed
```

Validation Agent 至少消费：

- `observation_collected`；
- `action_completed`；
- `outcome_observed`；
- `repair_applied`；
- `stage_completed`；
- `stage_failed`。

### 5.3 执行后输入

- 完整的 Stage 事件序列；
- 真实 `StepResults`；
- `ExecutionTrace`；
- 截图、trace、录屏及其他脱敏 `evidence_refs`；
- Patch Ledger；
- Renderer/Result Packager 的产物清单和 checksum。

## 6. Server 内部事件格式

建议新增内部 schema：

```text
demoops.stage_execution_event.v1
```

示例：

```json
{
  "schema_version": "demoops.stage_execution_event.v1",
  "event_id": "event_01J...",
  "run_id": "run_01J...",
  "source_package_id": "pkg_01J...",
  "source_bundle_hash_sha256": "<sha256>",
  "policy_hash_sha256": "<sha256>",
  "node_id": "node_create_project",
  "stage_id": "stage_create_project",
  "attempt": 1,
  "sequence": 12,
  "event_type": "outcome_observed",
  "occurred_at": "2026-07-22T12:00:00Z",
  "action": {
    "kind": "click",
    "target_semantic_id": "create_project_submit"
  },
  "observation": {
    "source": "actual_browser_observation",
    "url": "https://product.example.com/projects/123",
    "title": "项目详情",
    "assertions": [
      {
        "kind": "url_matches",
        "passed": true,
        "actual": "/projects/123"
      }
    ]
  },
  "evidence_refs": [
    {
      "id": "artifact_stage_3_screenshot",
      "kind": "screenshot"
    }
  ]
}
```

### 6.1 必填关联字段

每个事件必须携带：

- `schema_version`；
- `event_id`；
- `run_id`；
- `source_package_id`；
- `source_bundle_hash_sha256`；
- `policy_hash_sha256`；
- `node_id`；
- `stage_id`；
- `attempt`；
- `sequence`；
- `event_type`；
- `occurred_at`。

`node_id` 必须来自已验收 `plan_json`、`stage_approval_plan` 和 `script_outline` 的交集；Validation Agent 不接受运行时新造的业务节点。

### 6.2 观察来源

所有结果判断必须声明 `observation.source`：

```text
actual_browser_observation
browser_assertion
artifact_observation
derived_from_plan
insufficient_evidence
```

- `actual_browser_observation`、`browser_assertion`、`artifact_observation` 可以参与成功判断。
- `derived_from_plan` 只能解释预期，不能证明成功。
- `insufficient_evidence` 必须降低置信度，并且 required validation 缺少真实证据时不能输出 `continue`。

严禁使用 `expected_outcome` 回填 `observed_state` 后将其当作独立成功证据。计划回退生成的全 `passed` StepResults 必须标记为 `derived_from_plan`，不能进入高置信验证统计。

### 6.3 事件持久化

第一版可以使用进程内事件总线，但必须同时追加写入每次运行独立的 JSONL 审计文件或等价持久化存储。事件必须按 `sequence` 单调递增；重复投递通过 `event_id` 幂等去重。

## 7. Validation Agent 输出格式

### 7.1 ValidationReport

建议使用：

```text
demoops.validation_report.v1
```

```json
{
  "schema_version": "demoops.validation_report.v1",
  "report_id": "validation_01J...",
  "run_id": "run_01J...",
  "source_package_id": "pkg_01J...",
  "source_bundle_hash_sha256": "<sha256>",
  "policy_hash_sha256": "<sha256>",
  "phase": "runtime_stage",
  "node_id": "node_create_project",
  "stage_id": "stage_create_project",
  "decision": "continue",
  "pass_rate": 1,
  "overall_confidence": 0.93,
  "evidence_quality": "actual_browser_observation",
  "checks": [],
  "evidence_refs": [],
  "repair_proposal_refs": [],
  "created_at": "2026-07-22T12:00:01Z"
}
```

`phase` 枚举：

```text
pre_execution
runtime_stage
post_execution
```

新架构运行决策使用：

```text
continue
repair_allowed
stop_and_report
reunderstanding_required
```

决策语义：

| decision | 含义 | Stage Orchestrator 行为 |
| --- | --- | --- |
| `continue` | required validation 有真实证据通过 | 进入下一动作或下一 Stage |
| `repair_allowed` | 只存在合同允许的机械问题 | 交给 Repair Policy；不得直接应用 |
| `stop_and_report` | 安全、权限、合同、证据或不可恢复执行失败 | 立即停止并生成失败结果 |
| `reunderstanding_required` | 运行时事实证明 App 的业务理解或路线需要重新生成 | 当前运行停止；Result Packager 产生 `repair_request` 供 App 后续重新理解和审批 |

同事现有的 `continue`、`fine_tune`、`reunderstanding_required` 可保留为内部诊断分类，但在接入 Stage Orchestrator 前必须做稳定映射：

```text
continue                 -> continue
fine_tune + 有合法提案   -> repair_allowed
fine_tune + 无合法提案   -> stop_and_report
reunderstanding_required -> reunderstanding_required（当前运行停止）
```

该映射不修改 App-Server 上传协议，只规范 Server 内部运行行为。

### 7.2 RuntimeRepairProposal

Validation Agent 只提出建议：

```text
demoops.runtime_repair_proposal.v1
```

```json
{
  "schema_version": "demoops.runtime_repair_proposal.v1",
  "proposal_id": "repair_01J...",
  "run_id": "run_01J...",
  "node_id": "node_create_project",
  "stage_id": "stage_create_project",
  "base_bundle_hash_sha256": "<sha256>",
  "policy_hash_sha256": "<sha256>",
  "repair_kind": "selector_alternative",
  "field": "script_outline.stages[2].components[0].selector",
  "before": "[data-testid='create-project']",
  "after": "button:has-text('新建项目')",
  "confidence": 0.91,
  "evidence_refs": [],
  "requires_approval": false,
  "created_at": "2026-07-22T12:00:01Z"
}
```

允许提出的种类只能取自 `browser_agent_contract.repair_policy.allowed_repair_kinds`，并受以下条件约束：

- 同 `node_id`；
- 同 action type；
- 同域；
- 非破坏性；
- 不超过 `max_repair_attempts`；
- 不超过 `max_patch_operations`；
- 达到 `min_auto_apply_confidence` 才可进入自动审批；
- 只涉及协议允许的 selector、selector alternatives、wait strategy、capture timing、same-domain non-destructive exploration path 或 frame resolution。

Validation Agent 不得输出改变 route 业务含义、success state、业务输入、`secret_ref`、Stage 顺序或 destructive 标记的提案。

### 7.3 Patch Ledger

修复获准并应用后，由 Browser Agent Runtime 写入：

```text
demoops.patch_ledger_entry.v1
```

至少记录：

- `proposal_id`；
- 原始 bundle hash 与 policy hash；
- `run_id`、`node_id`、`stage_id`、attempt；
- patch 前后值；
- Policy Guard/Repair Policy 决策；
- 应用时间；
- 应用后的真实 Outcome Verifier 结果；
- evidence refs；
- 是否回滚。

Patch Ledger 是运行时派生记录，不改写原 `ClientExecutionPackage`，不重算 App 审批 hash，也不自动成为后续包的永久业务逻辑。

## 8. 与现有结果包的兼容方式

在不改变 `demoops.recording_result_package.v1` 既有字段语义的前提下，可增加可选字段：

```json
{
  "validation_reports": [],
  "patch_ledger": []
}
```

要求：

1. 两字段使用 `omitempty`，旧消费者忽略时不受影响。
2. 每个子对象必须有独立 `schema_version`。
3. `ValidationReport` 不覆盖 `verification_report`；前者表达阶段级业务结果诊断，后者继续表达结果包输出与复现校验。
4. `PatchLedgerEntry` 不替代 `graph_patch_suggestions`；前者记录单次运行的机械修复，后者是需要重新审批的业务图建议。
5. `failure_diagnostic` 只有一个最终聚合生产者：Result Packager。
6. Validation Agent 的 findings、决策和提案通过引用交给 Result Packager 合并，不能直接覆盖 Worker/Executor 已产生的错误、截图、trace、current URL/title 或 redaction report。
7. `repair_request` 仍使用既有字段；`stop_and_report` 或 `reunderstanding_required` 时由 Result Packager 根据最终诊断生成。

是否把验证摘要增加到 `ExecutionResultSummary` 属于后续 UI 优化，不是第一版 wire 合同的必要条件。

## 9. 进度与状态约定

为兼容现有 Exchange 状态机，第一阶段无需新增顶层 `ExchangePackageStatus` 枚举。验证通过 Stage History 表达：

```text
accepted: 10
validated: 25
preparing_worker: 35
validating_pre_execution: 36
running_browser_agent: 45..64
recording / packaging_recording: 65..70
validating_runtime_stage: 71..84
rendering: 85
validating_post_execution: 95
completed: 100
```

具体数值可由 Server 统一调整，但必须单调不回退。同事清单中 `MarkExecutionStage(..., progress=0)` 的描述不得进入最终实现。

前端需要把新 Stage 名映射为中文：

- `validating_pre_execution`：执行前校验；
- `running_browser_agent`：浏览器智能执行；
- `validating_runtime_stage`：步骤结果校验；
- `validating_post_execution`：执行后复核。

若 `MarkExecutionStage` 写入失败：

- 不得丢弃 ValidationReport 和事件审计文件；
- 进度展示失败本身不应改写业务验证决策；
- 持久化/审计存储失败应进入 Server 日志并由 Result Packager 标记为审计不完整；生产配置可以将“无法持久化审计”设为阻断性平台错误。

## 10. 对同事现有实现的保留与调整

### 10.1 可以直接保留

- 三类现有诊断反馈的计算逻辑；
- 执行前 domain/selector/evidence/blocking uncertainty 检查；
- 执行后的 status/state/route/artifact/duration 五维分析；
- pass rate 与 confidence 计算；
- selector/wait/timing 安全边界测试；
- 回放验证禁止伪造成功的原则；
- nil 安全、证据去重、阈值链和全流程测试。

### 10.2 必须面向新架构调整

1. 不把 `exchange_runner.go` 中旧 pipeline 的 pre/post 调用视为最终集成缝。
2. `ValidateRealTimeBatch` 改为由 Stage Orchestrator 的事件流触发。
3. Validation Agent 接口接受不可变 `ValidationContext` 和 `StageExecutionEvent`，不依赖 Playwright TypeScript。
4. `RuntimeRepairPatch` 改为 `RuntimeRepairProposal`；实际应用由 Repair Policy 和 Action Executor 完成。
5. `reunderstanding_required` 在新运行时中必须停止当前运行，不能继续后续 Stage。
6. `StageApprovalPlan` 缺失在 Outline runtime 中是 Intake 错误，不是静默 no-op。
7. 任何 `derived_from_plan` StepResult 不得作为成功证明。
8. Validation Agent 不直接生产最终 `ScriptFailureDiagnostic`，防止与 Worker/Executor 双生产者冲突。

### 10.3 暂不实施

- 为旧 Playwright Runner 单独定制 Validation Agent 接口；
- 为 Validation Agent 增加独立对外 REST API；
- 新增 App 侧上传字段；
- 允许 Agent 自动修改 App 已审批包；
- 在没有 Browser Agent Outline Runner 和真实事件时虚拟逐阶段成功数据；
- 把回放 Sidecar 作为 v1 上线前置条件。

## 11. 建议的 Go 内部接口

以下是 Server 内部建议，不是 wire schema：

```go
type ValidationContext struct {
    SourcePackageID       string
    SourceBundleHash      string
    EffectivePolicyHash   string
    WorkflowGraph         *model.DemoWorkflowGraph
    Plan                  *model.ExecutionScriptDocument
    StageApprovalPlan     *model.StageApprovalPlan
    ScriptOutline         *model.BrowserAgentScriptOutline
    BrowserAgentContract  *model.BrowserAgentContract
}

type OutcomeVerifier interface {
    ValidateBeforeExecution(context.Context, ValidationContext) (model.ValidationReport, error)
    ValidateStageEvents(context.Context, ValidationContext, []model.StageExecutionEvent) (model.ValidationReport, error)
    ValidatePostExecution(context.Context, ValidationContext, model.RecordingResultPackage, []model.StageExecutionEvent) (model.ValidationReport, error)
}
```

实现要求：

- 输入对象在一次 run 中只读；
- context 取消必须停止计算；
- 输出稳定、可序列化、无明文敏感数据；
- 相同输入和事件产生确定性决策；
- 模型辅助只能补充解释，不得绕过规则判定；
- 事件乱序、缺失或重复时返回证据不足或结构化平台错误，不伪造成功。

## 12. 分批开发计划

### P0：先冻结合同

- 双方确认本文件的数据对象、枚举和责任边界；
- 同事推送 `feat/validation-agent` commit/PR；
- 对 `ValidationReport` 现有模型做字段映射，不直接合并旧 `exchange_runner.go` 注入；
- 为事件、报告和提案增加 JSON round-trip 与反敏感信息测试。

验收：fixture 能稳定序列化；现有 Outline 包无需增加字段；任何不可变字段修复均被拒绝。

### P1：Browser Agent Runner 骨架与真实事件

- 实现 Runtime Router；
- 为 `browser-agent-outline-v1` 建立 Outline Runner、Stage Orchestrator 和事件总线；
- Action Executor 对每个 Stage 产生真实事件和 evidence refs；
- Validation Agent 接入 `Outcome Verifier`。

验收：一个 Outline fixture 能走到真实浏览器步骤事件；空 TS 不进入旧 Worker；缺少真实证据不能判定 Stage 成功。

### P2：受控修复闭环

- Validation Agent 输出 `RuntimeRepairProposal`；
- Policy Guard/Repair Policy 审批；
- Action Executor 应用；
- Patch Ledger 留痕；
- 应用后重新触发 Outcome Verifier。

验收：selector/wait/capture timing 小修可在同节点内闭环；route、业务输入、Stage 顺序和安全边界修改全部阻断。

### P3：结果包与 App 重新理解

- Result Packager 汇总 validation reports 与 patch ledger；
- `reunderstanding_required` 生成既有 `repair_request`；
- App 按既有 Result/Status API 获取结果并决定重新理解和重新审批；
- 前端增加验证摘要与阶段中文映射。

验收：失败结果包含脱敏截图/trace、current URL/title、redaction report、验证依据和 repair request；原包及其 hash 保持不变。

### P4：回放与生产隔离增强

- 接入 Browser Agent 回放 Sidecar；
- 将同一事件/验证接口用于回放验证；
- 在 per-job container/MicroVM、强制 egress 和 Secret Broker 环境中复验；
- 审计清理、凭据撤销与 artifact checksum 纳入最终报告。

验收：回放不能伪造成功；任务间无凭据、浏览器状态或临时文件串用。

## 13. 双方需要确认的事项

### Server Browser Agent 侧确认

- Runtime Router、Stage Orchestrator 和事件总线由 Server Browser Agent 侧实现；
- 事件必须包含真实观测来源和 evidence refs；
- Repair Policy、Patch Ledger 和最终 Result Packager 归 Server Browser Agent 侧；
- `failure_diagnostic` 由 Result Packager 单点聚合；
- 新进度 Stage 的持久化和前端映射由 Server 侧处理。

### Validation Agent 侧确认

- 同意以 `browser-agent-outline-v1` 为新主路径，不以旧 TS Runner 作为目标接口；
- 同意现有 `fine_tune` 在接入层映射为 `repair_allowed` 或 `stop_and_report`；
- 同意 `reunderstanding_required` 停止当前运行；
- 同意不直接应用 patch、不修改原包、不覆盖失败诊断；
- 同意把 plan-derived StepResults 排除出真实成功证据；
- 提供已完成本地分支的 commit/PR 和现有模型字段清单，供 P0 映射。

## 14. 对接验收清单

- [ ] 不新增或改写 App 上传三合一包的必填字段。
- [ ] Outline 包继续按现有 schema 和 hash 规则通过 Intake。
- [ ] 空 `playwright_script.inline_source` 的 Outline 包不进入旧 TypeScript Worker。
- [ ] 每个事件绑定原始 package、bundle hash、policy hash、`run_id`、`node_id` 和 `stage_id`。
- [ ] Stage 顺序、业务目标、输入语义和安全边界始终不可修改。
- [ ] required validation 没有真实证据时不能 `continue`。
- [ ] Validation Agent 只输出报告和提案，不直接操作浏览器或应用 patch。
- [ ] Repair Policy 拒绝越权、跨节点、跨 action type、跨域和破坏性修复。
- [ ] Patch Ledger 不改写原包及其审批 hash。
- [ ] 最终失败诊断只有 Result Packager 一个聚合生产者。
- [ ] 验证报告和证据引用不含凭据、Cookie、Authorization、Storage secret、完整 DOM/HTML 或源码。
- [ ] 结果包扩展对旧消费者保持可选和向后兼容。
- [ ] 进度单调递增，前端能显示新验证阶段。
- [ ] 回放或计划派生数据不能伪造真实执行成功。

## 15. 给对接同事的简短结论

Validation Agent 已完成的规则验证、执行后分析和安全修复提示可以保留，但新的正式接入目标是 `browser-agent-outline-v1` 的事件化 Browser Agent Runner。双方先冻结 `StageExecutionEvent -> ValidationReport / RuntimeRepairProposal` 合同；Validation Agent 只验证和提出建议，Server Browser Agent 负责执行、权限判断、补丁应用、Patch Ledger 与最终结果打包。所有设计继续服从现有 App ↔ Server Outline 协议，不修改 App 已审批业务事实，也不要求 App 增加新的上传字段。

## 16. 2026-07-22 Server 底座落地状态

本轮已经完成：

- `StageExecutionEvent`、`ValidationReport`、`RuntimeRepairProposal`、`RuntimePatchLedgerEntry` 正式 Go DTO；
- JSON 往返、枚举、置信度、真实证据、敏感信息和补丁状态校验；
- Runtime Router 已接入上传包的实际运行入口，Outline 包不会再进入旧 TypeScript Worker；
- Runtime Router 会先把 `stage_approval_plan`、`script_outline` 和 `browser_agent_contract` 编译为只读运行计划，并在调用任何 Outline Runner 前强制执行 Policy Guard；
- Stage 编译会阻断阶段缺失、顺序变化、目标或成功条件变化、业务动作插入、输入值变化和目标合同冲突；
- Policy Guard 会阻断域外地址、控制面/禁止页面、破坏性动作、未审批节点和不符合目标合同的动作，因此后续替换执行器也不能绕过准入检查；
- Outline Runner 未接入时返回固定错误码 `browser_agent_outline_runner_unavailable`，不降级、不伪造成功；
- Stage 事件以单次运行独立 JSONL 追加写入，支持 `event_id` 幂等去重并拒绝乱序；
- Stage Orchestrator 已冻结 Observer、Action Executor 和 Outcome Verifier 接口，并能按阶段输出开始、观察、动作开始、动作完成和阶段完成/失败事件；
- 阶段观察和动作完成必须引用真实浏览器断言、浏览器观察或产物证据；只从计划推导的数据不能冒充执行成功；
- Validation Agent 返回 `stop_and_report` 或 `reunderstanding_required` 时，Stage Orchestrator 会停止后续阶段；`repair_allowed` 只表示可进入 Repair Policy，不代表补丁已自动应用；
- Outcome Verifier 接口和只读审批上下文已经冻结，可供 Validation Agent 同事直接实现；
- 最新 Outline 协议 fixture 已包含 `browser_agent_contract`、各阶段 `target_contract`、required validations 和对应真实 hash，可用于双方联调；
- 结果包可以携带执行 runtime、验证报告、Patch Ledger 和事件审计引用；
- Editor Session 会把上述信息转成中文智能执行摘要，视频编辑页面可查看验证、修复与审计状态。

本轮尚未完成：

- Browser Agent 的真实 Observation / Locator Resolver 和 Action Executor；
- Validation Agent 同事实现与 `OutcomeVerifier` 的真实注入；
- Repair Policy 自动审批与补丁实际应用；
- 生产级 per-job container / MicroVM、Secret Broker 和强制网络出口隔离。

因此当前状态是“公共合同、分流、防误执行、审计和 UI 结果展示已具备”，不是“Browser Agent 已可真实完成业务操作”。

## 17. 2026-07-23 第一条真实浏览器执行链

本轮在上一节底座之上新增了第一版真实执行能力：

- Server 默认 Outline Runner 已从“不可用占位”切换为本地 Playwright Browser Agent Runner；
- Go 与 `video-worker` 使用同一持久 JSON-RPC 进程维持浏览器会话，Stage 之间不会丢失页面状态和登录态；
- Go 仍负责只读运行计划、Policy Guard、Stage 顺序、Outcome Verifier、审计事件和结果包，Node Worker 只负责浏览器观察、语义定位和白名单动作；
- 第一版语义定位按照审批合同中的 role/name、组件 test id/label 和已验证 selector 逐级解析，并拒绝歧义目标与禁止名称；
- 第一版动作执行支持 `navigate`、`click`、字面量 `fill`、字面量 `select`、`wait`、`assert` 和 `inspect`，每个动作仍需同时满足同域、非破坏性和目标合同约束；
- Worker 网络层会再次执行 allowed domains、禁止页面、禁止路径和禁止关键词检查，不能只依赖 Go 侧预检；
- 每个 Stage 会生成执行前、执行后脱敏截图，运行结束生成可选 WebM 录屏和 Playwright trace；URL 证据移除查询参数和 fragment，不回传完整 DOM、Cookie、Authorization 或 Storage；
- 确定性 Outcome Verifier 已接入：只有真实页面断言和证据均通过才允许进入下一 Stage；失败断言会写入 `stage_failed` 并停止；
- `plan_json.steps[].validations` 中的 required validation 会被编译进只读 Stage；Worker 必须返回每一项对应的真实检查结果，缺失任何一项都会停止，不允许用“动作已完成”代替“业务结果已验证”；
- Server 现有生命周期页面会收到中文的“正在观察、正在执行、正在验证、正在整理证据”等进度，不新增 App 上传字段；
- 已通过 Node Worker 两阶段真实浏览器冒烟测试，以及 Go → Node 持久 RPC 的真实浏览器集成测试。

第一版明确未完成：

- `secret_ref` 仍等待生产 Secret Broker，`input_ref` 仍等待只读输入解析器；二者缺失时停止，不回退到环境变量或模型猜测；
- `upload` 仍等待限定目录和文件授权的 File Broker，当前拒绝直接读取任意 Server 文件路径；
- Stagehand 智能定位尚未接入；目前只有审批合同约束内的确定性 Playwright 定位；
- Repair Policy、补丁实际应用和 Patch Ledger 闭环尚未完成；
- 当前真实验收使用本地受控测试页面，仍需拿一份可访问的真实业务测试环境执行最新 Outline fixture 联调；
- 生产级容器或 MicroVM、Secret Broker 和强制网络出口隔离仍未完成。

因此 2026-07-23 的准确状态是：“Browser Agent 已能在受控页面完成真实 Playwright 阶段执行和证据回传”，但还不是“已具备生产凭据、智能修复和任意业务页面适配能力”。
