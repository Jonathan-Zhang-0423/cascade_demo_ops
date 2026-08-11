# Browser Agent Direct 联调实施协议

文档版本：`1.1`
冻结日期：`2026-08-11`
线上 wire protocol：`cascade.browser_agent_direct.v1`
Worker protocol：`cascade.browser_agent_worker.v1`

目标读者：App、Gateway、Browser Agent Runtime、Outcome Verifier 和联调测试负责人。
协议状态：`implementation freeze candidate`。双方确认后，以本文作为 Direct v1 联调和验收的共同依据。

## 1. 目的与适用范围

本文是 App、Gateway、Browser Agent Worker 和 Web/Wails 适配层的联调实施协议，用于消除“字段存在但语义不同”“Selector 跨层漂移”“运行时使用无来源定位器”“下载成功被误认为 ACK 成功”“文件摘要正确但结构化证据属于其他运行”等接口缺陷。

本文不创建新的不兼容 wire version。现有 `cascade.browser_agent_direct.v1` 保持不变，本文件冻结其当前必须遵守的生命周期、绑定关系、错误语义和验收条件。

规范词含义：

- “必须”表示违反后必须 fail-closed。
- “不得”表示禁止兼容性猜测或静默降级。
- “应”表示默认实现要求；偏离时必须有明确 capability 或版本声明。

不属于正式链路的内容：

- Legacy Exchange 生产回退；
- App 直连 Worker loopback；
- fixture、mock、test waiver 作为正式验收证据；
- Server 修改用户意图、stage 顺序、安全策略或批准包；
- 在状态、日志、截图、trace、结果包或素材中保存明文凭据。

## 2. 协议参与方与信任边界

| 参与方 | 权威职责 | 明确禁止 |
|---|---|---|
| App Domain Service | 需求、图、包、审批、installation、下载、checksum、ACK、审核和编辑器交接状态 | 伪造执行证据；把未 ACK 显示为已确认接收 |
| Web / Wails / App HTTP Bridge | 调用同一个 App Domain Service 并展示持久化状态 | 各自实现不同的业务门禁；仅在前端内存中制造成功状态 |
| TLS Gateway | installation/lease 鉴权、协议协商、包和结果绑定、artifact 保存、最终结果门禁 | 修改批准包；信任 Worker 自报摘要而不复核上传字节和结构化内容 |
| Browser Agent Worker | claim 已批准包、消费一次性凭据、运行真实浏览器、产生事件和素材 | 访问 App；修改用户业务计划；把本地路径作为正式结果 URI |
| Outcome Verifier | 对脱敏 DTO、StageEventLog、StepResults 和结果包给出权威验证决策 | 读取 Playwright/page 对象；改写脚本；从自然语言消息推断正式决策 |

网络边界：

```text
App
  -> TLS control endpoint /v1/direct/*
  -> lease.data_url 上的数据接口

Gateway
  -> 127.0.0.1:18444 /v1/worker/*

Worker
  -X-> App
  -X-> 公网 Worker API
```

App 不持有 Worker token，也不展示 lease token、动态数据端口、Worker 堆栈或 credential envelope。

## 3. 版本与能力协商

App 在申请 lease 和上传包之前必须调用：

```http
GET /v1/direct/health
Authorization: Bearer <bootstrap-token>
```

Gateway 必须声明并由 App 精确检查：

```json
{
  "protocol_version": "cascade.browser_agent_direct.v1",
  "crypto_suite": "hkdf-sha256+aes-256-gcm",
  "supported_protocol_versions": ["cascade.browser_agent_direct.v1"],
  "supported_package_schema_versions": ["demoops.client_execution_package.v1"],
  "supported_runtimes": ["browser-agent-outline-v1"],
  "supported_worker_protocol_versions": ["cascade.browser_agent_worker.v1"],
  "supported_outcome_verifier_rules_versions": ["browser-agent-outcome-verifier-rules-v1"],
  "capabilities": {
    "manual_login_checkpoint": false
  }
}
```

任一必需版本或能力不匹配时：

- App 返回 `protocol_mismatch`；
- 不申请 lease；
- 不上传正式包；
- 不静默回退 Legacy Exchange。

新增必填字段必须通过新的 protocol/schema/capability 协商，不能在 Direct v1 中无声明地改变旧客户端语义。

## 4. 权威生命周期

### 4.1 端到端顺序

```text
1. App 生成 draft package 和 preview digests
2. 用户查看并批准冻结主体
3. App 写入批准记录并重新计算最终 package digest
4. App health 预检
5. App 获取或恢复 installation lease
6. App 上传批准包
7. 如有 credential grant，App 上传一次性 credential envelope
8. Gateway 将 job 置为 queued
9. Worker claim job 并校验 Worker protocol
10. Worker 消费凭据，在真实 Chromium 中执行
11. Worker 上传真实 artifact bytes
12. Worker 提交 RecordingResultPackage
13. Gateway 校验结果身份、摘要、完整性、结构化内容和脱敏安全
14. App 获取 completed/failed 状态和结果包
15. App 下载全部 artifact chunks，校验 size/SHA-256，写 verified marker
16. App 显式发送 result ACK，持久化 acked_at
17. App 开放人工结果审核
18. 审核通过后开放编辑器交接
19. App 显式释放 lease
```

步骤 15、16、17、18 不得合并为一个模糊的“结果已完成”状态。

### 4.2 Job 状态机

```text
package_received
  -> awaiting_credentials | queued
  -> running
  -> validating
  -> rendering
  -> completed | failed | canceled | expired
```

允许跳过极短暂状态的 UI 展示，但持久化状态必须保持以下约束：

- `awaiting_credentials` 不得显示为可执行；
- Worker 进度更新只能为 `running`，进度为 1-99；
- `completed` 必须已经通过 Gateway 正式结果门禁；
- `failed` 必须带脱敏 failure diagnostic 和需要重新批准的 repair request；
- `canceled`、`expired` 和 `reunderstanding_required` 原 job 不可恢复；
- App 重载后必须恢复稳定状态字段，不能把未知状态映射为成功。

### 4.3 App 结果接收状态机

```text
result_available
  -> downloading
  -> checksums_verified
  -> ack_pending
  -> acknowledged
  -> human_reviewed
  -> editor_materialized
  -> lease_released
```

必须满足：

- `checksums_verified` 只证明本地字节完整，不代表 Gateway 已收到 ACK；
- `ack_pending` 时允许重试 ACK，且保留已经验证的本地素材；
- `acknowledged` 必须来自 Gateway 的 `result_ack_receipt`，并持久化 `acked_at`；
- 未 ACK 时禁止人工审核、编辑器交接和 completed job 的 lease release；
- lease release 对旧客户端可补发 ACK，但 UI 不得据此提前声称 ACK 成功。

## 5. 批准主体与 digest 合同

用户批准时冻结：

- preview package digest；
- `approval_subject_digest_sha256`；
- confidence assessment hash；
- graph/project/run/bundle/safety digest；
- `plan_json`；
- `stage_approval_plan`；
- `script_outline`；
- `browser_agent_contract`；
- `agent_prompt_policy`；
- `approval_markdown`。

批准记录必须包含：

```json
{
  "approval_id": "approval_<opaque>",
  "approval_subject_digest_sha256": "<sha256>",
  "approved_at": "<RFC3339 UTC>",
  "approved_by_installation_id": "direct_install_<hash-prefix>",
  "approval_schema_version": "cascade.user_approval.v1",
  "subject_digests_sha256": {
    "plan_json": "<sha256>",
    "stage_approval_plan": "<sha256>",
    "script_outline": "<sha256>",
    "browser_agent_contract": "<sha256>",
    "agent_prompt_policy": "<sha256>",
    "approval_markdown": "<sha256>"
  }
}
```

批准后只允许写入以下确定性传输字段：

- approval record；
- 短期 credential expiry；
- Direct installation 来源绑定。

写入后 App 必须重新计算统一 approval subject digest 和最终 package digest。任何业务语义、stage、selector、Evidence、安全范围、URL、credential ref 或批准子对象变化都必须：

1. 使旧批准和旧上传初始化失效；
2. 返回 `package_preview_stale`；
3. 生成新 preview；
4. 要求用户重新批准。

以下身份必须相同：

```text
lease.installation_id
== package.producer_installation_id
== human_approval.approved_by_installation_id
```

不一致返回 `unverified_origin`。子 digest 或统一 digest 不一致返回 `approval_digest_mismatch`。

## 6. Selector、目标和运行时 repair 合同

### 6.1 跨层权威字段

同一业务动作必须用 `node_id` 关联以下五层，不得按数组位置或自然语言标题猜测关联关系：

```text
BusinessStage
-> WorkflowGraph node metadata
-> plan_json.steps[]
-> stage_approval_plan.stages[]
-> script_outline.stages[]
-> Server 编译的单次运行 BrowserAgentRuntimeStage
```

以下字段在进入正式包前必须保持语义一致：

| 语义 | BusinessStage / Graph | PlanJSON | StageApprovalPlan | ScriptOutline | Runtime |
|---|---|---|---|---|---|
| 动作身份 | stage/node ID | `node_id` | `node_id` | `node_id` | `node_id` + `stage_id` |
| 动作类型 | action type | `action.type` | `interaction.kind` | `interactions[].kind` | `interactions[].kind` |
| 非破坏性 | action + metadata | `non_destructive` | `interaction.non_destructive` | 每个 interaction 的 `non_destructive` | 原值只读继承 |
| 运行时自适应 | graph metadata | `runtime_adaptive` | `runtime_adaptive` | `runtime_adaptive` | 原值只读继承 |
| 语义目标 | semantic target | `target_contract` | `target_contract` | `target_contract` | 原值只读继承 |
| 主定位器 | action target | action/page target | interaction target | matching interaction target | 只读或选择批准 alternative |
| 候选定位器 | evidence-bound candidates | target alternatives | target alternatives | component/interaction alternatives | 仅批准集合的子集 |
| 成功定义 | success state | expected outcome + required validations | `success_state` | `success_state` | 不得改写 |
| 等待条件 | action waits | action/stage waits | `wait_conditions` | `wait_conditions` | 只允许执行或有界延长 |

硬约束：

- 五层 `non_destructive` 任一不一致返回 `non_destructive_mismatch`；
- `runtime_adaptive` 任一不一致返回 `runtime_adaptive_mismatch`；
- `stage_kind` 或 `route_state` 漂移分别返回 `stage_kind_mismatch`、`route_state_mismatch`；
- 同一 node 在 PlanJSON、StageApprovalPlan、ScriptOutline 使用不同主 selector 时返回 `selector_binding_mismatch`；
- Server 编译 RuntimeStage 时只能裁剪和规范化字段，不得补写 App 包中不存在的业务目标、selector、输入值或成功条件。

### 6.2 正式 SelectorCandidate

正式 selector candidate 必须携带：

```json
{
  "kind": "testid|role|css",
  "value": "<selector>",
  "evidence_id": "ev_<opaque>",
  "source_kind": "source_scan|page_scan|approved_manual_annotation",
  "source_digest": "<sha256>",
  "observed_role": "button",
  "observed_accessible_name": "New Project",
  "observed_at": "<RFC3339 UTC>",
  "evidence_refs": [
    {"id": "ev_<opaque>", "kind": "browser_scan"}
  ]
}
```

`kind` 的 Direct v1 规范值为 `testid`、`role`、`css`。如双方需要新增 `label`、`text` 或其他种类，必须先通过 capability/version 协商，不能由 Worker 单方面接受。

provenance 硬约束：

- `evidence_id` 必须存在于候选自身的 `evidence_refs`；
- `kind`、`value`、`evidence_id`、`source_kind`、`source_digest`、`observed_role`、`observed_accessible_name`、`observed_at` 均不能为空；
- `source_kind` 只允许 `source_scan`、`page_scan`、`approved_manual_annotation`；
- 缺少 provenance 返回 `selector_provenance_incomplete`；
- `data-testid` 只能作为 test ID/selector，不能拼入 accessible name 或业务 allowed names；
- candidate 只证明“该定位器曾被正式证据观察到”，不自动证明它是当前页面唯一且可操作的目标；Worker 仍必须检查唯一性、可见性和 role/name 兼容性。

### 6.3 主定位器规则

主定位器是三份批准对象中实际位于 action target 的 `selector`。它必须满足：

1. PlanJSON、StageApprovalPlan 和 ScriptOutline 的非空主 selector 规范化后完全一致；
2. runtime-adaptive stage 携带主 selector 时，至少有一个完整 provenance candidate 与主 selector 精确匹配；
3. `testid` candidate 可以与等价的 `[data-testid='...']` 主 selector 匹配；除此之外不得做模糊字符串匹配；
4. 主 selector 不得从按钮标题、项目名称、需求文本、placeholder 猜测或历史 fixture 自动合成；
5. Gateway/Worker 不得从 component 的裸 `selector`、`test_id` 或共享 EvidenceRef 反向合成新的 SelectorCandidate。

违反第 1 条返回 `selector_binding_mismatch`；违反第 2-5 条返回 `selector_primary_provenance_missing` 或 `selector_provenance_incomplete`。

### 6.4 无 Selector 的语义执行

页面/源码扫描没有提供正式 selector 时，App 可以保留语义目标并把 stage 标记为 `runtime_adaptive=true`，但必须同时满足：

- `non_destructive=true`，且 `target_contract.destructive=false`；
- 三层 `target_contract.semantic_id` 完全一致；
- target contract 至少提供 role、name、component ref 中的一项，并有受批准 route/origin 约束；
- action target 的 `selector`、`test_id` 和 `selector_alternatives` 不得写入猜测值；
- `success_state`、required validation、capture plan、allowed domains/origins 完整；
- `script_outline.can_modify` 明确允许 selector，`must_preserve` 明确包含 success state 和 safety policy。

此模式只授权 Worker 在批准的语义和安全范围内解析当前页面目标，不授权它改变业务动作。合同不完整返回 `runtime_adaptive_contract_incomplete`。解析不到唯一目标返回 `browser_agent_target_not_resolved`，不得自动点击“看起来最像”的元素。

### 6.5 动作目标与结果验证

以下规则同时适用于固定 selector 和 runtime-adaptive stage：

- `action_target` 与 `success_target` 必须分离；
- click/select/submit 不得以动作控件仍可见作为成功证明；
- fill 可以用 `value_equals` 证明输入完成，但后续业务成功仍需独立验证；
- 动态路由必须使用 `/project/:id`、`/project/{project_id}` 或结构化 binding，禁止把运行时真实 ID 写回批准包；
- 长任务必须有结构化 wait conditions、required validations 和最大时长，不能只写自然语言“等待完成”。

required validation 至少使用以下一种确定性断言：`url_matches`、`element_visible`、`element_hidden`、`text_contains`、`attribute_equals`、`value_equals`、`element_count`、`page_title_contains`。缺失时返回 `deterministic_validation_missing`。

### 6.6 等待语义

等待必须由 App 批准，Worker 不得在每个动作后无条件等待网络空闲：

- 默认导航等待为 `domcontentloaded`，随后使用不超过 250ms 的短稳定窗口；
- 只有 `interaction.wait_until=networkidle`，或批准的 `wait_conditions` 明确要求网络空闲时，Worker 才等待 `networkidle`；
- 对 WebSocket、SSE、轮询和持续请求页面，不得把 `networkidle` 当作通用完成条件；
- `wait_after_entry_at_least_<n>ms` 的 `n` 上限为 15000；运行时 wait repair 只能延长，不能缩短批准时长；
- capture timing repair 只允许等待、重新验证和重新采集，不得重放已经执行成功的业务动作；
- 未识别的等待条件不得被静默解释成固定 sleep 或网络空闲；新增等待语义必须先完成 capability/version 和双方测试约定。

### 6.7 Worker 运行时 repair

Worker 可进行的 selector repair 仅限：

```text
同一 stage
+ 同一业务目标
+ 同一 Evidence ID
+ App 已批准的 selector alternatives
+ 运行时唯一性、可见性、role/name 兼容检查
```

repair 的额外约束：

- 候选必须在 App 批准包中已经具备完整 provenance；
- `preferred_selector_alternative` 只表示本次运行选择，不得写回批准包或改变 package digest；
- proposal 必须绑定 `run_id`、`node_id`、`stage_id`、base bundle hash 和 policy hash；
- editable field、repair kind、confidence、attempt 和 patch operation 数量必须满足 `browser_agent_contract.repair_policy`；
- selector repair 不得跨 node、跨 stage、跨 semantic target 或跨 Evidence ID；
- wait/capture repair 不得修改 action type、input/secret ref、allowed domains、route、success state、validation 或安全策略；
- 每次允许或拒绝均写入 RuntimePatchLedger，日志中只保存脱敏前后值和 opaque evidence ID。

超出范围必须停止执行并返回稳定脱敏错误，不得猜测 selector，也不得自行改写计划。

### 6.8 Worker 编译输入的最小合同

Server 向 Worker 发送的 runtime stage 至少包含：

```json
{
  "id": "stage_<opaque>",
  "node_id": "node_<opaque>",
  "target_contract": {
    "semantic_id": "target_<opaque>",
    "allowed_roles": ["button"],
    "allowed_names": ["新建项目"],
    "destructive": false
  },
  "interactions": [{
    "kind": "click",
    "target": {},
    "non_destructive": true,
    "wait_until": "domcontentloaded",
    "wait_conditions": ["wait_after_entry_at_least_250ms"]
  }],
  "validations": [{
    "id": "validation_<opaque>",
    "kind": "url_matches",
    "target": {"url": "/project/:id"},
    "required": true
  }],
  "evidence_bound_selector_alternatives": []
}
```

`target` 为空在 runtime-adaptive 非破坏性 stage 中是合法状态；它不表示 Worker 可任意探索。Worker 的探索仍受 target contract、route/origin、安全策略和 required validation 共同约束。

## 7. 公网 Gateway Direct API

控制口使用 bootstrap token；数据口使用 lease 派生的 HMAC 请求认证和 `DirectEncryptedMessage`。最大允许时钟偏差为 5 分钟。

### 7.1 控制接口

| 方法 | 路径 | 成功语义 |
|---|---|---|
| `GET` | `/v1/direct/health` | 返回版本、能力和服务器时间 |
| `POST` | `/v1/direct/leases` | 返回 installation 专属 `DirectPortLease` |
| `POST` | `/v1/direct/leases/release` | 仅在无活跃 job 且 completed 结果已 ACK 时释放 |

lease 申请必须使用 installation Ed25519 私钥签名。installation ID 必须由公钥确定性计算。`lease_token` 只保存于 App vault/进程和 Gateway 内存，不进入 UI、日志、报告或项目 JSON。

### 7.2 数据口请求认证

每个请求必须携带：

```http
X-Cascade-Timestamp: <unix-ms>
X-Cascade-Nonce: <one-time-nonce>
X-Cascade-Body-SHA256: <sha256-body>
X-Cascade-Signature: <hmac-sha256>
```

签名规范化内容：

```text
UPPERCASE_METHOD\n
ESCAPED_PATH\n
TIMESTAMP_MS\n
NONCE\n
LOWERCASE_BODY_SHA256
```

nonce 重放、时间越界、body digest 或签名错误必须在解密业务载荷前拒绝。

### 7.3 数据接口

| 方法 | 路径 | 加密 message type | 说明 |
|---|---|---|---|
| `POST` | `/v1/direct/packages` | `client_execution_package` / `package_receipt` | 上传已批准正式包，自动进入 awaiting credentials 或 queued |
| `POST` | `/v1/direct/jobs/{job_id}/credentials` | `credential_envelope` / `credential_receipt` | 上传一次性短期凭据 |
| `GET` | `/v1/direct/jobs/{job_id}` | `job_status` | 获取稳定状态和脱敏下一步 |
| `GET` | `/v1/direct/jobs/{job_id}/result` | `recording_result` | 获取已通过 Gateway 门禁的结果包 |
| `GET` | `/v1/direct/jobs/{job_id}/artifacts/{artifact_id}` | 无 | 已停用的整件下载入口，固定返回 `artifact_chunking_required` |
| `GET` | `/v1/direct/jobs/{job_id}/artifacts/{artifact_id}/chunks/{chunk_index}` | `artifact_chunk:<id>:<index>` | 推荐下载方式，单块最多 4 MiB |
| `POST` | `/v1/direct/jobs/{job_id}/ack` | `result_ack` / `result_ack_receipt` | 全部素材校验后显式确认接收 |

`DirectEncryptedMessage` 外层固定包含：

```json
{
  "protocol_version": "cascade.browser_agent_direct.v1",
  "lease_id": "lease_<opaque>",
  "message_id": "<unique>",
  "message_type": "<type>",
  "direction": "app_to_browser_agent|browser_agent_to_app",
  "timestamp_unix_ms": 0,
  "nonce_base64": "<nonce>",
  "plaintext_digest_sha256": "<sha256>",
  "ciphertext_digest_sha256": "<sha256>",
  "ciphertext_base64": "<ciphertext>",
  "crypto_suite": "hkdf-sha256+aes-256-gcm"
}
```

## 8. Loopback Worker API

Worker API 只能监听 `127.0.0.1:18444`，使用独立 Worker token，不得由公网或 App 访问。

| 方法 | 路径 | 约束 |
|---|---|---|
| `POST` | `/v1/worker/jobs/claim` | claim 响应必须带 `cascade.browser_agent_worker.v1` |
| `PUT` | `/v1/worker/jobs/{job_id}/status` | 只允许 running、1-99%、无 result package ID |
| `PUT` | `/v1/worker/jobs/{job_id}/artifacts/{artifact_id}` | 上传真实 artifact bytes，元数据与字节摘要必须一致 |
| `PUT` | `/v1/worker/jobs/{job_id}/result` | 只能为已 claim 的 running job 提交最终结果 |
| `POST` | `/v1/worker/jobs/{job_id}/credentials/consume` | 一次性消费；消费或 Gateway 重启后不得恢复 |
| `POST` | `/v1/worker/jobs/{job_id}/release` | 释放 claim；需要凭据的任务回到 awaiting credentials |

Worker 必须先上传全部被结果包引用的 artifact bytes，再提交最终 `RecordingResultPackage`。Gateway 不接受“结果包先到、文件稍后补齐”的最终成功语义。

## 9. 结果包与结构化证据合同

### 9.1 completed 结果必需内容

所有正式 completed 结果必须包含：

- 完整 `StepResults`，覆盖批准计划的每个 node，且状态均为 passed；
- `ValidationReports`；
- `StageEventLogRef`；
- Replay Manifest；
- 请求中的 raw recording；
- 请求最终视频时的 MP4、AssetTimelineCatalog 和 DemoEditPlan；
- 请求 trace/截图时的对应产物；
- 每个关键产物的非空 ID、认证 URI、SHA-256 和 `size_bytes`。

正式 URI 固定为：

```text
direct://jobs/{escaped_job_id}/artifacts/{escaped_artifact_id}
direct://jobs/{escaped_job_id}/result
```

不得出现：

- `file://`；
- Server 本地绝对路径；
- `dev_local_artifact`；
- `local-dev/result-key`；
- 其他 job 的 Direct URI。

### 9.2 Gateway 四层结果校验

Gateway 必须按顺序执行：

1. 身份绑定：job、package、result ID 和状态合同；
2. 字节绑定：每个引用的 SHA-256/size 与已上传 artifact bytes 一致；
3. 完整性：正式 runtime 所需 artifact、StepResults 和 ValidationReports 不缺失；
4. 内容绑定：解析 Replay Manifest JSON 和 StageEventLog JSONL，验证其确实属于本次批准运行。

内容校验至少包括：

- Replay Manifest schema 为 `demoops.replay_manifest.v1`；
- run ID、package ID、bundle hash、policy hash 一致；
- policy hash 绑定 `BrowserAgentContractHashSHA256`，不得误用 plan hash；
- protocol runtime 和 execution runtime 一致；
- stage/node 属于批准计划；
- StageEventLog event ID 唯一、sequence 严格递增；
- 每个 completed stage 同时存在真实 `outcome_observed` artifact evidence 和 `stage_completed`；
- Replay Manifest 的 stages 完整索引 StepResults；
- Replay Manifest 的 validation refs 完整索引 ValidationReports；
- requested raw/video/trace 被 Replay Manifest 正确引用；
- 所有 URI 绑定当前认证 job。

错误分层：

| 错误码 | 含义 |
|---|---|
| `result_artifact_binding_invalid` | 结果引用摘要/size 与已上传字节不一致 |
| `result_artifact_completeness_failed` | 正式成功所需产物或字段缺失 |
| `result_artifact_content_invalid` | JSON/JSONL 可读但 schema、运行、stage、事件或索引绑定错误 |
| `result_contains_local_runtime_data` | 结果含本地路径、dev marker 或其他禁止运行时信息 |

### 9.3 failed 与 reunderstanding_required

正式 failed 结果：

- 必须有脱敏 `failure_diagnostic`；
- 必须有 `approval_required=true` 的 repair request；
- 不得伪造 MP4；
- 应尽可能提供截图和 trace；只有明确的基础设施启动失败可缺少页面素材。

`reunderstanding_required` 只能来自权威 `ValidationReports[].Decision`。Gateway 返回：

```json
{
  "status": "failed",
  "blocking_error_code": "reunderstanding_required",
  "next_action": "regenerate_package_from_structured_issues",
  "requires_reapproval": true,
  "reunderstanding_issues": []
}
```

App 必须只处理一次该终态：清空旧 preview/approval，递增 package generation，用结构化问题重新理解并生成新 package ID/digest，再次人工批准。重复轮询不得重复递增 generation。

## 10. 下载、ACK、审核与编辑器交接

### 10.1 下载

App 必须：

1. 下载状态中声明的全部 artifact；
2. 校验总字节数；
3. 校验 SHA-256；
4. 原子写入 App-managed artifact root；
5. 写 `.verified.sha256` marker；
6. 持久化每个 verified artifact 状态。

任一 artifact 失败时，不得设置 `result_downloaded=true`。

### 10.2 显式 ACK

ACK 请求：

```json
{
  "protocol_version": "cascade.browser_agent_direct.v1",
  "installation_id": "direct_install_<hash-prefix>",
  "job_id": "job_<opaque>",
  "result_package_id": "result_<opaque>",
  "received_artifact_ids": ["artifact_a", "artifact_b"],
  "verified_checksums": true,
  "acked_at": "<RFC3339 UTC>"
}
```

Gateway receipt 必须精确回显 job、result package、全部 artifact ID、`verified_checksums=true` 和 ACK 时间。App 只有在 receipt 绑定全部匹配后才能持久化：

```json
{
  "result_downloaded": true,
  "acked_at": "<RFC3339 UTC>"
}
```

前端派生 `resultAcknowledged = acked_at != empty`。不得仅由 `result_downloaded`、lease release 或 UI 点击事件推断 ACK 成功。

### 10.3 人工审核和编辑器

人工审核前置条件：

```text
status == completed
+ result_downloaded == true
+ acked_at != empty
+ job_id/result_package_id 与当前结果一致
```

编辑器交接额外要求审核决策为 approved，并且只能引用本地 verified 素材。编辑器不得依赖 Gateway 文件路径，也不得接受未校验的远程 URL。

## 11. App 本地统一接口

HTTP Bridge 与 Wails 必须调用同一个 App Service。若一侧失败，可回退另一适配器，但业务状态、错误和持久化结果必须一致。

| App Service 操作 | HTTP Bridge | Wails 方法 |
|---|---|---|
| 配置/诊断 Direct | `PUT /v1/desktop/browser-agent-direct` | `ConfigureDirectBrowserAgent` |
| 上传正式包 | `POST /v1/desktop/projects/{id}/browser-agent-direct/upload` | `UploadDirectBrowserAgentPackage` |
| 查询状态 | `GET .../browser-agent-direct/status?job_id=` | `DirectBrowserAgentStatus` |
| 获取结果 | `GET .../browser-agent-direct/result?job_id=` | `DirectBrowserAgentResult` |
| 下载 artifact | `POST .../browser-agent-direct/artifact/download` | `DownloadDirectBrowserAgentArtifact` |
| 显式 ACK | `POST .../browser-agent-direct/ack` | `AcknowledgeDirectBrowserAgentResult` |
| 人工审核 | `POST .../browser-agent-direct/review` | `ReviewDirectBrowserAgentResult` |
| 编辑器交接 | `GET .../browser-agent-direct/editor-materialization` | Domain Service 同一读取门禁 |
| 释放 lease | `POST .../browser-agent-direct/release` | `ReleaseDirectBrowserAgentLease` |

Web 必须以持久化 App state 为准，不得只更新组件内存。ACK 失败时应显示“已校验，等待 ACK/重试服务器 ACK”，不能显示“服务器 ACK 已发送”。

## 12. 幂等、重启与恢复

- package upload 幂等键为 installation + package ID + final package digest；
- 相同 key 和相同 payload 返回原 job；相同 key 不同 payload 返回冲突；
- Gateway 重启后 queued/running 普通 job 可重新 claim；
- credential envelope 只在内存中，一旦消费、过期或 Gateway 重启必须重新上传；
- artifact chunk 可按 index 重试；
- ACK 必须幂等，相同 job/result/artifact 集合返回已有 receipt；
- canceled/expired/reunderstanding-required job 不恢复；
- App 重启后从项目持久化状态恢复 lease/job/result/download/ACK/review，不从 UI 文案反推状态。

## 13. 稳定错误码与责任方

| 类别 | 代表错误码 | 首要责任方 | App 行为 |
|---|---|---|---|
| 版本/能力 | `protocol_mismatch`, `unsupported_runtime`, `outcome_verifier_rules_mismatch` | 部署/协议 | 阻断，不回退 legacy |
| 审批/来源 | `package_preview_stale`, `unverified_origin`, `approval_digest_mismatch` | App | 重新生成 preview/批准或修复 installation |
| Selector/计划 | `selector_provenance_incomplete`, `selector_binding_mismatch`, `selector_primary_provenance_missing`, `runtime_adaptive_contract_incomplete`, `non_destructive_mismatch`, `deterministic_validation_missing`, `package_validation_failed` | App | 修复证据/图并重新批准 |
| Runtime 目标 | `browser_agent_target_not_resolved`, `browser_agent_destructive_action_denied` | Worker/协议 | 停止当前 stage，返回脱敏诊断；不得猜测或越权执行 |
| 凭据 | `credential_grant_expired`, `credential_grant_unavailable`, `credential_binding_invalid` | App/用户 | 重新上传短期 envelope，不重用旧密文 |
| Worker | `worker_auth_invalid`, `job_not_claimed`, `invalid_status_transition` | Server/Worker | 保留脱敏状态，禁止伪造完成 |
| 结果字节 | `result_artifact_binding_invalid`, `artifact_checksum_mismatch` | Worker/传输 | 重新上传或失败终止 |
| 结果完整性 | `result_artifact_completeness_failed` | Worker | 补齐正式产物后重新提交结果 |
| 结构化内容 | `result_artifact_content_invalid` | Worker | 修复 manifest/event 绑定后重新提交 |
| ACK | `ack_artifacts_incomplete`, `ack_binding_invalid`, `result_ack_required` | App | 保留 verified 下载并重试正确 ACK |
| 重新理解 | `reunderstanding_required` | App + 用户 | 新 package/digest，必须重新批准 |

错误响应只返回稳定 code 和脱敏 message。不得返回凭据、token、Cookie、原始 envelope、内部绝对路径、完整 DOM、Worker 堆栈或页面敏感内容。

## 14. 安全和日志要求

- 账号、密码、API Key、Cookie、Token 只能存在于本地 vault/secret ref、短期受控内存和加密 envelope；
- 日志只允许 installation/job/package/artifact 的 opaque ID 或安全后缀；
- 结果和下载目录必须执行 secret-leak scan；
- screenshot、video、trace、DOM/accessibility snapshot 必须执行既定遮罩和脱敏策略；
- `failure_diagnostic.redaction_report.applied` 必须为 true，`full_html_included` 必须为 false；
- Gateway 只对已上传字节计算和比较摘要，不信任外部提供的本地路径；
- 正式 Replay Manifest 必须在 Worker 上传前终结成本 job 的 Direct URI，并重新计算 SHA-256/size。

## 15. 联调验收矩阵

### 15.1 必过正向用例

1. health 同时协商 Direct、package、runtime、Worker 和 verifier 版本；
2. installation、lease、producer、approval 完整绑定；
3. 上传后自动进入 awaiting credentials/queued，无 App→Worker start；
4. Worker 真实 claim、消费凭据并运行真实 Chromium；
5. 每个批准 stage 都产生 StepResult、ValidationReport、真实 outcome evidence 和 terminal event；
6. Gateway 解析并接受本 job 的 Replay Manifest 和 StageEventLog；
7. App 下载全部 chunk，checksum/size 全通过；
8. App 显式 ACK 并在重载后恢复 `acked_at`；
9. ACK 前审核、lease release、编辑器均被阻断；ACK 后按序开放；
10. 最终编辑器只读取本地 verified 素材。

### 15.2 必过负向用例

1. 旧 preview digest 上传返回 `package_preview_stale`；
2. installation/producer/approval 不一致返回 `unverified_origin`；
3. 缺 selector provenance 返回 `selector_provenance_incomplete`；
4. 三层主 selector 不一致返回 `selector_binding_mismatch`；
5. runtime-adaptive 主 selector 无匹配 formal candidate 返回 `selector_primary_provenance_missing`；
6. 无页面证据时 App 只传语义目标，不生成 `[data-testid=...]`、`:has-text(...)` 或 placeholder 猜测；
7. component 裸 selector 或共享 EvidenceRef 不得被合成为 repair candidate；
8. 未批准 `networkidle` 的持续请求页面不会让每个动作额外等待网络空闲；
9. Worker protocol 不匹配拒绝 claim；
10. artifact SHA/size 错误返回绑定错误；
11. Replay Manifest 来自其他 package/run 返回 `result_artifact_content_invalid`；
12. StageEventLog sequence 重复或缺真实 outcome evidence 返回 `result_artifact_content_invalid`；
13. requested raw/video/trace 未进入 Replay Manifest 返回 `result_artifact_content_invalid`；
14. 下载完成但 ACK 失败时，UI 只显示 ACK pending，且可重试；
15. 未 ACK 的 completed job 释放 lease 返回 `result_ack_required`；
16. `reunderstanding_required` 不复用旧批准、旧 package ID 或旧 digest；
17. 完整序列化状态、日志、包和素材中不存在明文凭据。

## 16. 合并与发布门禁

合并门禁可以使用本地 Gateway/Worker 和真实 Chromium，但必须明确区分 fixture 与真实运行。发布门禁必须完成：

```text
正式 installation identity
-> health/capability negotiation
-> lease
-> approved package upload receipt
-> credential envelope
-> Worker claim + real Chromium
-> completed/failed authoritative result
-> all artifact checksums
-> explicit ACK receipt
-> human review
-> editor materialization
-> lease release
-> secret-leak scan
```

任何线上接口未部署、凭据无效、真实 Chromium 未运行、产物缺失、ACK 未完成或 checksum 不一致，都只能输出脱敏阻断报告，不得标记为“正式端到端通过”。

## 17. 联调变更规则

双方修改协议时必须同时提供：

1. 变更的 DTO/字段和是否必填；
2. 状态机前置条件与终态影响；
3. 新增或变化的稳定错误码；
4. 旧客户端处理方式和 capability/version 条件；
5. App、Gateway、Worker 各自的正向和负向测试；
6. 不含凭据的示例 payload；
7. 是否导致旧批准 digest 失效。

## 18. 双方确认清单

App 与 Browser Agent 负责人应逐项回复 `接受`、`需修改` 或 `暂不支持`，不得仅回复“整体同意”：

```text
1. Direct/Worker/package/verifier 版本与 capability：
2. installation、lease、producer、approval 的同源绑定：
3. preview、approval subject 和 final package digest 的失效规则：
4. node_id 驱动的五层字段映射：
5. non_destructive 和 runtime_adaptive 的跨层一致性：
6. SelectorCandidate 完整 provenance 字段：
7. 主 selector 三层一致性和 formal candidate 绑定：
8. 无 selector 时仅允许受限语义执行，不生成猜测 selector：
9. action_target 与 success_target 分离：
10. required validation 的确定性断言集合：
11. 默认 domcontentloaded，networkidle 仅按批准条件执行：
12. selector/wait/capture repair 的边界和 RuntimePatchLedger：
13. 动态 route template 和运行时 ID 不回写批准包：
14. Worker 先上传真实 artifact bytes，再提交最终结果：
15. Replay Manifest 与 StageEventLog 的内容绑定校验：
16. chunk 下载、SHA-256/size 校验和 verified marker：
17. 显式 ACK、acked_at、审核、编辑器和 lease release 顺序：
18. reunderstanding_required 的新包、新 digest 和重新批准流程：
19. 稳定错误码、脱敏 message 和责任归属：
20. 正向/负向联调用例与正式发布门禁：
```

确认完成后，双方应记录：负责人、实现版本或 commit、确认时间、未实现 capability 和计划上线时间。未确认项不得按“兼容成功”处理。

仅提交工作日志或自然语言说明，不视为协议变更完成。代码 DTO、validator、接口文档和跨端 contract tests 必须同时更新。
