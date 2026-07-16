# Browser Agent 设计路线与双方协议对齐建议

> 日期：2026-07-16  
> 适用范围：同事侧（用户服务器/App Agent）与我们侧（云端执行、录制、诊断和结果交付）  
> 文档目的：统一 Browser Agent 的职责、三合一数据包协议、运行时修复权限、验证标准和双方后续开发边界。

> 状态说明：本文是面向下一版协议和实现的设计提案，不代表当前 v1 已经具备云端自动修复能力。当前 v1 仍以“我们侧返回失败诊断、同事侧修复并重新审批上传”为准；文中自动修复能力应按阶段实施并经过双方协议确认。

---

## 1. 结论摘要

双方后续建议采用以下组合，而不是从零实现一个可以任意浏览和修改业务流程的通用 Browser Agent：

```text
Playwright
    做确定性浏览器执行

Stagehand
    做当前业务步骤内的智能目标识别

Outcome Verifier
    执行同事侧提供的结构化结果验证

Repair Policy
    控制 Browser Agent 可以修改什么、何时可以自动应用、何时必须停止或重新审批
```

核心原则：

1. 业务步骤由同事侧根据完整源码生成并写死，我们侧 Browser Agent 不修改业务步骤。
2. `Graph + PlanJSON + validations` 是业务意图和成功标准的权威来源。
3. Stagehand 只允许在当前步骤内重新识别目标元素，不允许生成新的业务流程。
4. Playwright 负责真实浏览器执行、录制、Trace、截图和结构化验证，不负责自行推断业务意图。
5. 原始三合一数据包保持不可变；运行时修复以独立 Patch Overlay 形式应用和审计。
6. 低风险修复可由同事侧在上传包中预授权；超出授权范围仍返回同事侧重新生成并审批数据包。
7. 页面运行时状态是客观证据，但不能覆盖同事侧已经审批的业务目标。
8. Browser Agent 无法证明页面目标与业务目标一致时，必须停止，不能自行猜测。

---

## 2. 当前协议与代码事实

### 2.1 当前三合一数据包

同事侧在 `ClientExecutionPackage.executable_script_bundle` 中上传：

1. `plan_json`：机器可审计的 `ExecutionScriptDocument`，是 JSON 执行计划和审计源。
2. `playwright_script`：从 `plan_json` 确定性生成的受限 TypeScript Playwright 脚本。
3. `approval_markdown`：展示给用户审核的中文说明和审批文档。

三部分通过以下摘要绑定：

```text
plan_hash_sha256
script_hash_sha256
markdown_hash_sha256
bundle_hash_sha256
```

因此，上传并审批后的原始三合一包不能由我们侧直接覆盖或原地修改。

### 2.2 当前 v1 失败修复流程

当前协议定义的是同事侧修复：

```text
我们侧执行失败
    ↓
我们侧返回失败截图、Trace、URL、DOM/a11y引用和脱敏诊断
    ↓
同事侧 App Agent 结合本地完整源码修复 PlanJSON 和受限 TS
    ↓
用户重新审核 approval markdown
    ↓
同事侧上传带 RepairContext 和 ScriptRepairLineage 的新包
```

当前协议中的 `approval_required=true` 表示正式脚本修复仍需要用户侧审批。

### 2.3 当前实际执行方式

当前我们侧 Go 后端通过内部 JSON-RPC 调用 Node.js `video-worker`：

```text
Go 后端
    ↓ record JSON-RPC
video-worker/src/index.ts
    ↓
recorder.ts
    ↓
browser-recorder.ts
    ↓
Playwright 浏览器
```

当前代码会校验受限 TypeScript，但真实网页操作主要按照 `plan_json.steps` 执行。因此双方必须把 `PlanJSON` 视为运行步骤的主要机器来源，并保证 TS 与 PlanJSON 确定性一致。

### 2.4 当前结果判断缺口

当前 Playwright 可以发现：

- selector 找不到；
- 元素无法点击；
- 输入框不可编辑；
- 页面导航或等待超时；
- 浏览器动作抛出异常。

但当前执行成功后，`expected_outcome` 主要被直接写入 `observed_state`，没有完整执行同事侧提供的 `validations`。因此暂时不能可靠发现：

- 点击成功但点击了同名的错误按钮；
- 点击后打开了错误功能；
- 数据输入成功但业务提交失败；
- 页面发生变化但没有达到同事侧定义的成功状态。

所以接入 Stagehand 之前，必须先补齐结构化 Outcome Verifier。

---

## 3. 双方职责边界

## 3.1 同事侧职责

同事侧能够看到完整源码，应负责提供准确且经过审批的业务逻辑：

- 生成 DemoWorkflowGraph；
- 确定业务步骤、步骤顺序和 action type；
- 生成 PlanJSON；
- 从 PlanJSON 确定性生成受限 TypeScript；
- 为每个关键步骤提供结构化 validations；
- 提供目标元素的业务语义 `target_contract`；
- 声明哪些运行时修复可以自动执行；
- 声明哪些字段永远不可由云端 Agent 修改；
- 生成包含 Browser Agent 权限说明的 approval markdown；
- 对超出预授权范围的修复重新生成、审核和上传数据包；
- 接收我们侧返回的成功 Patch，并决定是否吸收到下一版正式脚本。

## 3.2 我们侧职责

我们侧负责受控执行，而不是重新定义业务：

- 校验包身份、签名、Hash和版本；
- 校验 Graph、PlanJSON、TS和审批文档之间的绑定关系；
- 校验 Browser Agent 授权范围；
- 使用 Playwright 执行确定性动作；
- 执行 allowed domains、forbidden pages、secret、redaction和SandboxPolicy；
- 使用 Stagehand 在当前步骤内识别目标候选；
- 使用 Repair Policy 判断候选是否有权执行；
- 使用 Outcome Verifier 执行同事侧定义的 validations；
- 保存Observation、候选、Patch、验证和失败原因；
- 生成视频、截图、Trace和结果包；
- 冲突或越权时停止并返回诊断。

## 3.3 我们侧不能做的事情

默认禁止我们侧 Browser Agent：

- 修改 Graph 业务目标；
- 修改步骤顺序；
- 插入或删除业务步骤；
- 修改 action type；
- 修改用户输入数据；
- 修改 `input_ref` 或 `secret_ref`；
- 修改 `expected_outcome`；
- 修改 `validations`；
- 扩大 allowed domains；
- 修改 forbidden pages、redactions或SandboxPolicy；
- 页面不存在目标能力时自行选择另一个业务功能。

---

## 4. 四个核心组件的定位

## 4.1 Playwright：确定性执行器

Playwright 负责：

- 启动 Chromium/Firefox/WebKit；
- 创建隔离Browser Context；
- 执行 `goto/click/fill/select/wait/assert`；
- 使用自身 actionability 机制判断元素是否存在、可见、稳定、可点击或可编辑；
- 处理页面跳转、iframe和弹窗；
- 执行同事侧提供的结构化 validations；
- 限制页面网络请求范围；
- 录屏、截图、Trace和失败取证。

Playwright 不负责决定：

- 应该执行哪个业务步骤；
- 两个不同业务功能中应该选择哪个；
- 是否应该修改业务目标；
- 是否可以降低成功标准。

源代码可以提高脚本生成准确度，但不能消除以下运行时变量：

- 部署版本与源码快照漂移；
- 用户角色和权限差异；
- 租户数据和Feature Flag差异；
- 页面异步加载、网络延迟和遮罩；
- 登录状态失效；
- 响应式布局和iframe变化。

因此 Playwright 的运行时检查仍然必须保留，但不需要在业务步骤前重复实现大量手工 `isVisible/isEnabled` 检查，应优先使用 Playwright 自带的等待和actionability机制。

## 4.2 Stagehand：智能目标识别器

Stagehand 只处理当前步骤内的目标定位问题，例如：

- 原CSS selector失效；
- 页面改用新的data-testid；
- 按钮位置或DOM层级变化；
- 元素进入iframe；
- 页面存在多个候选，需要根据同事侧业务语义筛选。

Stagehand 输入中必须包含同事侧提供的 `target_contract`，而不能只提供一句自然语言任务。

Stagehand 输出应为目标候选，不应直接输出完整新脚本：

```text
候选role/name/testid/selector
匹配的semantic_id
候选证据
置信度
是否存在歧义
```

第一阶段建议 Stagehand 运行在 `suggest_only/shadow` 模式，只生成建议；协议、验证和数据流稳定后，再开放低风险自动应用。

## 4.3 Outcome Verifier：结果验证器

业务成功标准必须由同事侧提供，我们侧只机械执行。

建议第一版支持以下Validation白名单：

```text
url_matches
element_visible
element_hidden
text_contains
attribute_equals
value_equals
element_count
page_title_contains
```

关键 `click/navigate/submit` 步骤至少包含一个 `required=true` 的结构化Validation。

示例：

```json
{
  "expected_outcome": "创建项目表单打开",
  "validations": [
    {
      "id": "project_dialog_visible",
      "kind": "element_visible",
      "target": {
        "role": "dialog",
        "test_id": "create-project-dialog"
      },
      "required": true,
      "timeout_ms": 5000
    }
  ]
}
```

Stagehand 和我们侧都不能修改这些Validation。

## 4.4 Repair Policy：业务权限控制器

Repair Policy 不负责判断页面元素，而负责判断Stagehand提出的Patch是否有权执行。

建议默认允许：

- 使用已有selector alternative；
- CSS改成testid；
- CSS改成role/name；
- 修正frame范围；
- 在上限内调整timeout/wait condition；
- 在同一个 `target_contract.semantic_id` 内重新定位目标。

建议默认禁止：

- 修改action type；
- 修改value/input_ref/secret_ref；
- 修改URL或扩大域名；
- 修改expected outcome和validations；
- 插入、删除、跳过和重排步骤；
- 跨node修复；
- 选择与target contract冲突的元素。

---

## 5. 修改后的运行流程

```text
同事侧生成Graph + PlanJSON + TS + approval markdown
        ↓
用户审批业务步骤、Validation和Browser Agent授权范围
        ↓
上传Hash绑定的三合一包
        ↓
我们侧校验签名、Hash、SandboxPolicy和Browser Agent Contract
        ↓
生成本次运行的Effective Execution Plan
初始内容与原PlanJSON一致
        ↓
Playwright执行原始步骤
        ↓
Outcome Verifier执行同事侧validations
        │
        ├── 通过
        │      → 继续下一步
        │
        └── 执行失败或Validation失败
               ↓
        我们侧生成脱敏Runtime Observation
               ↓
        Stagehand根据target_contract识别候选目标
               ↓
        Repair Policy检查候选和Patch
               │
               ├── 越权或业务冲突
               │      → 停止并返回同事侧
               │
               ├── 协议要求重新审批
               │      → 返回Repair Proposal
               │
               └── 属于预授权低风险修改
                      ↓
              生成Runtime Repair Patch Overlay
                      ↓
              Playwright在同一浏览器会话内执行修复候选
                      ↓
              Outcome Verifier再次执行原Validation
                      │
                      ├── 通过 → passed_repaired
                      └── 失败 → 回退、换候选或终止
```

Browser Agent循环必须在同一个TypeScript/Node浏览器执行worker内完成，否则多次RPC会丢失当前页面、登录状态、弹窗和Browser Context。

---

## 6. 建议同事侧新增的协议结构

## 6.1 全局 Browser Agent Contract

建议在 `ExecutionScriptDocument` 或 `ClientExecutionPackage` 中加入：

```json
{
  "browser_agent_contract": {
    "schema_version": "demoops.browser_agent_contract.v1",
    "mode": "suggest_only",
    "business_authority": {
      "source": "workflow_graph_and_validations",
      "script_may_change_business_intent": false,
      "runtime_page_may_change_business_intent": false,
      "agent_may_change_business_intent": false
    },
    "repair_policy": {
      "allowed_repair_kinds": [
        "selector_strategy",
        "selector_alternative",
        "semantic_target",
        "wait_condition",
        "frame_target"
      ],
      "editable_fields": [
        "action.target.selector",
        "action.target.selector_alternatives",
        "action.target.test_id",
        "action.target.role",
        "action.target.text",
        "action.target.label",
        "action.target.frame",
        "action.timeout_ms",
        "action.wait_until"
      ],
      "immutable_fields": [
        "node_id",
        "action.type",
        "action.value",
        "action.input_ref",
        "action.secret_ref",
        "page_target.url",
        "expected_outcome",
        "validations",
        "blocking"
      ],
      "max_repair_attempts": 2,
      "max_patch_operations": 3,
      "max_agent_runtime_ms": 30000,
      "min_auto_apply_confidence": 0.9,
      "same_node_only": true,
      "same_action_type_only": true,
      "same_domain_only": true,
      "step_insert_allowed": false,
      "step_delete_allowed": false,
      "step_reorder_allowed": false,
      "step_skip_allowed": false
    },
    "conflict_policy": {
      "on_graph_plan_conflict": "reject_package",
      "on_target_contract_conflict": "stop_and_report",
      "on_runtime_intent_conflict": "stop_and_report",
      "on_ambiguous_target": "require_approval",
      "on_outcome_verification_failure": "retry_within_limit",
      "on_repair_limit_exceeded": "return_to_customer_app"
    }
  }
}
```

## 6.2 每个步骤增加 target contract

```json
{
  "node_id": "node_create_project",
  "action": {
    "type": "click",
    "target": {
      "selector": "button.create",
      "selector_alternatives": [
        {
          "kind": "test_id",
          "value": "create-project",
          "confidence": 0.95,
          "source": "customer_source"
        }
      ]
    }
  },
  "target_contract": {
    "semantic_id": "workspace.create_project_button",
    "purpose": "打开创建项目表单",
    "allowed_roles": ["button"],
    "allowed_names": ["创建项目", "新建项目"],
    "forbidden_names": ["删除项目", "创建文件夹"],
    "component_ref": "workspace.toolbar",
    "destructive": false
  }
}
```

`target_contract` 由同事侧根据源码和产品逻辑生成，是Stagehand判断目标的业务边界。

## 6.3 Observation Policy

```json
{
  "observation_policy": {
    "allowed_fields": [
      "url_without_query",
      "page_title",
      "accessibility_snapshot",
      "interactive_elements",
      "element_attributes",
      "redacted_screenshot",
      "redacted_console_summary",
      "redacted_network_summary"
    ],
    "full_html_allowed": false,
    "cookies_allowed": false,
    "authorization_headers_allowed": false,
    "storage_content_allowed": false,
    "input_values_allowed": false,
    "source_code_allowed": false,
    "max_interactive_elements": 200,
    "max_text_length": 12000,
    "mask_selectors": [
      "input[type=password]",
      "[data-sensitive=true]"
    ]
  }
}
```

## 6.4 Model Policy

Stagehand可能需要把页面Observation发送给模型，因此模型数据边界必须进入协议和用户审批：

```json
{
  "model_policy": {
    "provider_ref": "approved_browser_agent_model",
    "deployment_mode": "approved_cloud",
    "customer_data_export_allowed": false,
    "training_usage_allowed": false,
    "retention_allowed": false,
    "data_residency": "CN",
    "request_timeout_ms": 15000
  }
}
```

如果协议禁止客户页面数据外发，则我们侧必须使用私有模型、同区域模型，或者只发送经过严格脱敏的结构化Observation。

---

## 7. Browser Agent 输入与输出

## 7.1 Browser Agent 输入

Browser Agent输入由两部分组成。

### A. 同事侧提供的权威静态上下文

- package、graph、plan和bundle Hash；
- 当前node和step；
- action type；
- target contract；
- expected outcome；
- structured validations；
- allowed domains和安全策略；
- repair policy；
- conflict policy。

### B. 我们侧运行时生成的Observation

- 当前URL和页面标题；
- 失败类型；
- 脱敏Accessibility Snapshot；
- 可交互元素摘要；
- 原目标命中数量；
- 截图引用；
- 脱敏console/network摘要；
- 原Validation失败结果；
- 已尝试候选和修复历史。

运行时Observation无法预先放入三合一包，因为它只能在真实执行过程中生成；但Observation允许包含的字段和数据处理方式必须由三合一包预先授权。

## 7.2 Browser Agent 输出

Stagehand/Browser Agent只返回候选或Repair Proposal：

```json
{
  "proposal_id": "repair_001",
  "source_package_id": "pkg_001",
  "base_graph_hash_sha256": "sha256:graph",
  "base_plan_hash_sha256": "sha256:plan",
  "failed_node_id": "node_create_project",
  "repair_kind": "semantic_target",
  "target_alignment": {
    "semantic_id": "workspace.create_project_button",
    "matched_role": "button",
    "matched_allowed_name": "创建项目",
    "forbidden_match": false
  },
  "operations": [
    {
      "op": "replace",
      "node_id": "node_create_project",
      "field": "action.target",
      "old_value": {
        "selector": "button.create"
      },
      "new_value": {
        "role": "button",
        "text": "创建项目",
        "test_id": "create-project"
      }
    }
  ],
  "confidence": 0.94,
  "ambiguous": false
}
```

---

## 8. Runtime Repair Patch 与Hash关系

我们侧不能修改原始：

```text
plan_hash_sha256
script_hash_sha256
bundle_hash_sha256
approval digest
```

运行时采用：

```text
Original Plan
    +
Authorized Runtime Repair Patch
    =
Effective Execution Plan
```

建议结果中记录：

```json
{
  "base_plan_hash_sha256": "sha256:original",
  "repair_patch_hash_sha256": "sha256:patch",
  "effective_plan_hash_sha256": "sha256:effective",
  "repair_authorized_by": "browser_agent_contract.repair_policy"
}
```

成功Patch返回同事侧后，由同事侧决定是否吸收到下一版正式PlanJSON，并重新生成TS、approval markdown和全部Hash。

---

## 9. 冲突判断规则

信息权威顺序如下：

```text
SandboxPolicy / AllowedDomains
    最高安全约束

Graph + structured validations
    业务权威

PlanJSON / TypeScript
    已审批的执行方案

运行时页面Observation
    客观事实

Stagehand推理结果
    候选建议，不是业务权威
```

### 9.1 Graph与Plan冲突

上传阶段拒绝：

```text
GRAPH_PLAN_SEMANTIC_CONFLICT
```

### 9.2 原selector与页面冲突

允许在Repair Policy范围内生成selector/target候选。

### 9.3 页面不存在符合target contract的元素

停止并返回：

```text
RUNTIME_BUSINESS_CAPABILITY_MISSING
```

### 9.4 存在多个无法区分的候选

停止自动执行并返回：

```text
AMBIGUOUS_SEMANTIC_TARGET
```

### 9.5 修复动作执行成功但Validation失败

该候选视为无效，不得标记成功；应回退、尝试其他候选或终止。

---

## 10. 用户审批边界

### 10.1 可以预授权的修改

- selector策略替换；
- 使用已有selector alternative；
- 同semantic_id内的role/name/testid重新定位；
- frame修正；
- 有上限的wait/timeout修正；
- 最大修复次数和最大运行时间。

### 10.2 必须重新审批的修改

- action type变化；
- URL或域名变化；
- 输入数据、input_ref或secret_ref变化；
- expected outcome或validations变化；
- 步骤插入、删除、跳过或重排；
- 跨node修复；
- destructive action目标变化；
- 无法确认与target contract一致的候选。

### 10.3 approval markdown新增内容

同事侧审批文档中必须展示：

- 是否启用Browser Agent；
- 使用的模型或模型策略；
- Agent可以观察哪些页面信息；
- 页面信息是否会发送到外部模型；
- 允许自动修改哪些字段；
- 禁止修改哪些字段；
- 是否允许semantic target repair；
- 最大尝试次数、最长运行时间；
- 哪些情况会停止并返回同事侧。

这些内容必须进入审批摘要和Hash绑定，不能审批后由我们侧扩大。

---

## 11. 建议双方对齐的接口状态

建议增加运行状态：

```text
running_script
verifying_outcome
observing_failure
planning_repair
validating_repair_policy
executing_repair
verifying_repair_outcome
completed
completed_with_repair
repair_approval_required
repair_rejected_by_policy
repair_attempts_exhausted
runtime_business_conflict
```

建议步骤结果状态：

```text
passed_original
passed_repaired
failed_execution
failed_outcome_verification
repair_rejected_by_policy
repair_requires_approval
repair_attempts_exhausted
```

建议 `RecordingResultPackage` 增加：

```text
repair_summary
repair_attempts[]
applied_repair_patches[]
effective_plan_hash_sha256
outcome_verification_results[]
browser_agent_trace_ref
```

---

## 12. 双方近期开发建议

## 12.1 同事侧优先事项

1. 明确 `PlanJSON` 是业务执行和审计的机器权威。
2. 为关键click/navigate/submit步骤补齐required validations。
3. 为可修复步骤增加target contract。
4. 定义Browser Agent Contract、Observation Policy和Model Policy。
5. 将Repair Policy从简单布尔值升级为字段级权限。
6. 修改approval markdown，展示Browser Agent授权范围。
7. 增加Graph、Plan、TS之间的语义一致性检查。
8. 支持接收我们侧返回的Runtime Repair Patch。
9. 决定成功Patch是否进入下一版正式三合一包。

## 12.2 我们侧优先事项

1. 实现同事侧Validation白名单执行器。
2. 不再把 `expected_outcome` 直接当作 `observed_state`。
3. 建立脱敏的MCP风格Runtime Observation。
4. 建立统一TargetResolver接口。
5. 保留Playwright确定性resolver作为第一层。
6. 接入Stagehand shadow/suggest模式。
7. 实现Repair Policy Validator。
8. 实现Runtime Repair Patch Overlay，不修改原始包。
9. 保存每次候选、Policy决策和Validation结果。
10. 增加敏感数据脱敏，避免Cookie、Header、Storage和输入值进入Agent Trace。

---

## 13. 推荐分阶段实施

### 阶段一：协议与验证基础

- 双方确定Browser Agent Contract；
- 关键步骤补齐structured validations；
- 我们侧实现Outcome Verifier；
- 定义Runtime Repair Patch格式；
- Stagehand暂不自动执行。

### 阶段二：Stagehand Shadow Mode

- Playwright失败后生成脱敏Observation；
- Stagehand生成候选；
- 只记录候选和置信度；
- 与同事侧源码修复结果进行对比；
- 评估候选准确率和冲突率。

### 阶段三：低风险自动修复

- 只开放selector、selector alternative、frame和有限wait修复；
- 限制同node、同action type、同domain；
- 每次修复后强制执行原Validation；
- 失败立即回退并审计。

### 阶段四：受约束的Semantic Target Repair

- 要求同事侧提供完整target contract；
- 要求至少一个required postcondition；
- 高置信且无歧义时才自动应用；
- destructive或多候选情况必须重新审批。

---

## 14. 双方会议需要最终确认的问题

1. `PlanJSON` 是否正式确定为运行时机器权威？
2. TypeScript是实际执行源，还是PlanJSON的确定性审计/执行产物？
3. 哪些action必须提供required validations？
4. `target_contract` 由哪个同事侧模块生成？
5. 哪些字段允许我们侧自动修改？
6. 是否允许semantic target repair自动应用？
7. 自动修复的最大次数、最长时间和最低置信度是多少？
8. Stagehand使用什么模型，页面Observation是否允许外发？
9. ambiguous target如何触发用户审批？
10. 成功的Runtime Patch如何回流并成为下一版正式脚本？
11. 双方使用哪些状态码和错误码？
12. 哪些Repair Trace和Agent输入可以进入最终结果包？

---

## 15. 最终共识建议

建议双方将新版协议的核心原则确定为：

> 同事侧负责提供经过用户审批的业务步骤、目标语义和结果验证标准；我们侧Playwright负责确定性执行，Stagehand只在当前步骤和target contract范围内重新识别页面目标，Outcome Verifier执行同事侧的结构化validations，Repair Policy决定运行时Patch能否应用。任何运行时Patch都不得覆盖原始三合一包，并必须携带原始Hash、授权依据、Observation证据和验证结果返回同事侧。
