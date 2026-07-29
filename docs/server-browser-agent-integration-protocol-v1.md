# Server Browser Agent 实施与联调协议 v1

> **文档角色。** 本文件是 Server 侧对 [`app-server-browser-agent-outline-protocol.md`](app-server-browser-agent-outline-protocol.md) 的实施说明和联调约定，不重新定义 App 执行包字段。字段语义、审批边界与 schema 以该上位协议为唯一准则；发生冲突时以上位协议为准。

## 1. 目的和适用范围

本协议让 App 侧能以稳定方式向 Server 发送已经审批的 `ClientExecutionPackage`，查询执行状态、获取结果并确认交付；同时明确 Server Browser Agent 的权限边界，避免 Server 和 App 分别改变同一项业务语义。

当前新能力必须使用：

```json
{
  "language": "browser-agent-outline",
  "runtime": "browser-agent-outline-v1",
  "entry_function": "runBrowserAgentOutline",
  "dependency_allowlist": []
}
```

`playwright-restricted-sandbox` 是冻结的历史兼容路径，只能回放历史包、做回归测试或紧急回滚；不能作为新增 App-Server 对接或新增 Browser Agent 功能的目标。详见 [`legacy-playwright-runtime-freeze-v1.md`](legacy-playwright-runtime-freeze-v1.md)。

## 1.1 当前可冻结的 Server 实施范围

以下规则不依赖云厂商、Vault、KMS、容器平台或 App 内部实现选择，现作为 Server 后续开发的固定约束。除非先修改上位协议，Server 不得以私有字段、提示词或 legacy 路径绕过它们。

| 固定项 | Server 实施承诺 |
| --- | --- |
| 主路径 | 新增任务只接受 `browser-agent-outline-v1`，不把 outline 包降级为 legacy TypeScript 执行 |
| 包一致性 | 在执行前校验 schema、身份、Graph/Plan/Stage/Outline `node_id`、顺序、业务目标、动作语义、输入语义、目标合同与 hash |
| 探索限制 | 除现有域名、禁区和控制面拦截外，后续实现必须将运行时页面和探索目标限制到 App 提供的 `allowed_origins` 与 `allowed_routes`；未明确批准的同域页面不作为可探索目标 |
| 执行限制 | 只执行已审批、非破坏性动作；每个业务动作必须有 required 结果验证与真实浏览器证据 |
| 自动修复 | 只处理合同批准的 selector alternative、延长等待、capture timing、frame 解析和同域非破坏性路径；不得修改业务事实 |
| 修复记录 | 所有自动修复只作用于单次内存运行计划，必须写入 `patch_ledger` 与 JSONL 阶段审计日志，原包及 hash 不改写 |
| 失败处理 | 合同冲突、目标歧义、必填验证失败、越权修复或证据不足一律 `stop_and_report`，返回脱敏结果，不猜测业务 |
| 对外结果 | 返回状态、结果包、资产 checksum、验证报告、Patch Ledger、阶段审计和 Ack 所需字段；失败包包含脱敏诊断与 `repair_request` |
| 开发预检 | Server 已开放同规则的 `ValidateUpload` HTTP 预检；预检无副作用，不分配任务、不写入包、不消耗 nonce、不启动浏览器 |
| 验收 | 每项新增 Browser Agent 功能须同时通过 fixture/校验器测试、Server 受控全链路测试和真实浏览器固定场景 Gate 后才可标记为可用 |

其中“逐路由白名单”和“正式 HTTP 预检”均已落地；它们不改变 App 的执行包字段，也不要求 App 选定云基础设施。

## 2. 唯一事实源与职责边界

### 2.1 业务真相归属

App 侧产出的 `stage_approval_plan` 是业务真相：阶段顺序、目标、业务意图、动作语义、输入语义、成功条件和安全边界均由它确定并经用户审批。

Server 不理解或重写客户产品的业务逻辑。Server 只负责校验该合同，在真实浏览器中受限执行，记录证据，并在合同已经授权的范围内修正网页层面的微小偏差。

### 2.2 App 与 Server 责任

| 事项 | App 侧责任 | Server 侧责任 |
| --- | --- | --- |
| 产品业务理解 | 从代码、需求和产品知识中形成业务事实与证据 | 不从运行时页面猜测或改写业务事实 |
| 用户审批 | 展示并审批 Markdown、阶段计划、路线图、风险和证据 | 校验上传内容的 hash 与审批后内容一致 |
| 执行包 | 生成并签名/加密 `ClientExecutionPackage` | 接收、预检、持久化元数据并调度执行 |
| 网页执行 | 提供候选路由、组件线索、目标合同和动作语义 | 在隔离浏览器上下文中观察、定位、执行与验证 |
| 小范围修复 | 预先在合同中声明可修复字段、候选 selector 与限制 | 仅在声明范围内修复，记录审计账本 |
| 业务冲突 | 根据失败诊断重新理解、重新审批并生成新包 | 停止执行并返回脱敏失败诊断，不自行改业务 |
| 结果使用 | 下载、检查 checksum、确认交付 | 交付录屏、截图、trace、验证报告和结果包 |

### 2.3 Server 永不越过的边界

Server Browser Agent 不得改变：

- 用户需求、stage 顺序、stage objective、business intent；
- 动作类型、输入业务语义、输入值、`input_ref`、`secret_ref`；
- `allowed_domains`、`forbidden_pages`、破坏性标记、打码策略；
- App/Server 控制面路径，例如 `/aigc`、`/.well-known`、`/v1`、`execution-packages`、`result-packages`、`app-installations`、`/debug`。

若运行时页面与上述合同矛盾，Server 的唯一处理方式是 `stop_and_report`：生成失败结果和脱敏诊断，等待 App 生成新审批包。

## 3. 执行包合同

### 3.1 必须存在的内容

`executable_script_bundle` 必须包含：

- `plan_json`：机器可审计的执行计划和 required 验证；
- `approval_markdown`：用户可读的中文审批说明；
- `stage_approval_plan`：已审批业务阶段；
- `script_outline`：受限网页执行路线图；
- `agent_prompt_policy`：不可变和可编辑字段；
- `browser_agent_contract`：修复、观察、模型和冲突策略；
- `security_policy`：域名、禁区、打码与凭据引用；
- `reproducibility`：关键对象和 bundle 的 hash。

`playwright_script.inline_source` 在 outline 主路径可以为空；它不是 Server 拒绝此类执行包的理由。

### 3.2 多份材料如何对齐

每个业务节点必须同时存在于：

```text
workflow_graph.nodes[]
  <-> plan_json.steps[]
  <-> stage_approval_plan.stages[]
  <-> script_outline.stages[]
```

四者使用同一 `node_id`。Server 必须拒绝缺失节点、重复节点、顺序冲突、目标冲突、动作类型冲突、目标合同冲突及输入语义冲突。

对 `navigate`、`click`、`fill`、`select`、`upload` 等业务动作，`plan_json.steps[]` 必须携带至少一项 `required=true` 的验证，例如 `url_matches`、`element_visible`、`text_contains`、`attribute_equals` 或 `value_equals`。没有真实浏览器验证证据，Server 不得进入下一阶段。

### 3.3 Hash 与审批一致性

App 必须在最终审批材料确定后计算并上传：

- `plan_hash_sha256`
- `stage_plan_hash_sha256`
- `outline_hash_sha256`
- `prompt_policy_hash_sha256`
- `browser_agent_contract_hash_sha256`
- `markdown_hash_sha256`
- `bundle_hash_sha256`

内联 `understanding_dossier` 时还必须提供其 hash。Server 对 JSON 使用 canonical JSON digest，对 Markdown 的 UTF-8 内容使用 SHA-256。任一不匹配都拒绝上传，不会替 App 重算后继续执行。

### 3.4 包体与敏感信息限制

- 常规 outline 包目标小于 200 KB；当前硬限制建议为 512 KB。
- 不得上传完整源码、完整 HTML/DOM dump、全局 selector 池、密码、Token、Cookie、Authorization、`.env`、storage 内容或未打码截图。
- 凭据只能用 `secret_ref` 或 credential grant 引用表达，不能直接放入动作 `value` 或诊断信息。

## 4. Server 处理链路

```text
App 用户审批包
  -> Init：协商上传会话、密钥和限制
  -> Upload：身份、信封、digest、结构、hash、sandbox 预检
  -> Runtime Router：按 runtime 选择主路径
  -> Policy Guard：预先阻止域名、禁区、破坏性和合同外动作
  -> Outline Runner：逐 stage 观察、定位、执行
  -> Outcome Verifier：检查 required 页面结果
  -> Repair Policy：仅处理 App 已授权的小修复
  -> Recorder / Trace Collector：记录录屏、截图、trace、阶段事件
  -> Result Packager：生成成功或失败结果包
  -> Editor Session Materializer：将可编辑素材交给本地视频编辑器
  -> App 下载、校验 checksum、Ack
```

### 4.1 允许的 Server 自适应修复

仅当 `browser_agent_contract.repair_policy` 允许、字段在 `editable_fields` 内、证据充分且未超过限制时，Server 可在**本次运行的内存计划**中：

- 在 App 已列出的 `selector_alternatives` 中选择唯一、可见、符合 `target_contract` 的候选项；
- 在页面确实繁忙且已有等待条件的前提下，延长等待；
- 调整 capture timing；
- 在同域、非破坏性、非控制面范围内解析 frame 或受限探索路径。

每次修复必须写入 `patch_ledger`：包含原值、新值、证据、尝试次数、策略决定、源 bundle hash 与合同 hash。原执行包和原 hash 永远不被改写。

### 4.2 Server 必须停止的情况

- 目标控件不符合 `target_contract`，或目标不唯一；
- App 未批准的 selector、路由、业务动作或输入变化；
- required validation 失败或缺失真实浏览器证据；
- 修复次数、补丁数量、时长或置信度超出合同；
- 域名、禁区、控制面、破坏性操作或打码策略冲突；
- runtime 与 Graph / Stage Plan / Outline 的业务语义冲突。

失败时，Server 返回 `failure_diagnostic`、`repair_request`、截图/trace 引用、当前 URL/标题（脱敏后）、redaction report 和可审计的阶段事件；不得返回原始密钥、完整 DOM 或完整源码。

## 5. HTTP 对接约定

公网部署使用 `/aigc` 前缀，内网开发可使用无前缀 `/v1`。请求在鉴权后均需 `Content-Type: application/json`；状态、结果和 ack 请求还需 `X-Cascade-Org-ID` 或 `org_id` 查询参数。

| 用途 | 方法和路径 | 是否公开联调 | 说明 |
| --- | --- | --- | --- |
| 建立上传会话 | `POST /aigc/v1/execution-packages/init` | 是 | 返回 `upload_id`、服务端公钥、支持的加密方式、大小限制与执行出口 IP |
| 上传执行包 | `POST /aigc/v1/execution-packages` | 是 | 接收开发明文包或生产 `payload_ref`，返回 `exchange_package_id`、`cloud_job_id`、状态 |
| 查询状态 | `GET /aigc/v1/execution-packages/{id}/status` | 是 | 返回阶段、进度、失败摘要和 `result_package_id` |
| 获取结果包 | `GET /aigc/v1/result-packages/{id}` | 是 | 返回完整 `RecordingResultPackage` |
| 确认交付 | `POST /aigc/v1/result-packages/{id}/ack` | 是 | App 校验已下载资产 checksum 后确认 |
| 启动任务 | `POST /aigc/v1/dev/execution-packages/{id}/run` | 仅开发 | 当前开发环境显式启动；生产应由调度器在上传后启动 |
| 取消任务 | `POST /aigc/v1/dev/execution-packages/{id}/cancel` | 仅开发 | 仅限未结束的开发任务 |
| 查看脱敏调试摘要 | `GET /aigc/v1/dev/execution-packages/{id}/debug` | 仅开发 | 不返回原执行包、脚本、DOM 或 secret |

`init` 后 App 必须使用返回的 `upload_id` 上传；上传中的 `org_id`、`project_id`、`package_kind` 必须与 Init 一致。相同 `org_id + idempotency_key` 的重复上传必须返回首次任务，而不是重复启动。

### 5.1 开发与生产上传的区别

| 项目 | 开发联调 | 生产目标 |
| --- | --- | --- |
| payload | 允许内联明文 `payload`，仅 development/test profile | 仅允许加密 `payload_ref` |
| 鉴权 | 共享开发 Bearer token | App installation session + envelope signature |
| 执行启动 | `/dev/.../run` 或受控 auto-run | 上传后由生产调度器启动 |
| 凭据 | 不发送真实凭据 | Vault broker 按 `secret_ref` 注入 |
| 隔离 | 本地受控 Playwright sidecar | Container/MicroVM + 受限代理 + 临时凭据 |

### 5.2 预检接口的固定约定（已开放）

为避免 App 必须上传后才能发现格式或 hash 错误，Server 将开放受认证、无副作用的预检接口：

```http
POST /aigc/v1/execution-packages/validate
```

请求体与 Upload 使用相同的 `upload_id`、`envelope`、`payload_ref` 和开发环境 `payload` 结构；它不创建 `exchange_package_id`、不消耗幂等键、不启动任务。成功响应为：

```json
{
  "valid": true,
  "runtime": "browser-agent-outline-v1",
  "warnings": []
}
```

失败响应沿用第 9 节的结构化错误。该接口已用仓库内 `browser-agent-outline-v1` fixture 验收：成功预检不创建 `exchange_package_id`、不创建上传会话、不消耗 replay nonce；失败预检同样不落任何状态。开发环境需携带与 Upload 相同的 Bearer 或 installation session 鉴权。

### 5.3 状态、结果和确认

建议状态流：

```text
accepted -> validating -> preparing_worker -> running_browser_agent -> recording -> rendering -> completed
```

终态还包括 `failed`、`canceled`。App 应轮询 `status` 到终态，不应把 HTTP 上传成功当成录制成功。

成功的 `RecordingResultPackage` 应检查：`generated_assets`、`execution_trace`、逐步 `validation_reports`、`patch_ledger`、`stage_event_log_ref`、checksum 和交付状态。失败结果必须优先读取 `failure_diagnostic` 和 `repair_request`，不能自动将其改写成“App 规划失败”。

Ack 时 App 必须先下载资产、验证 SHA-256，再提交：

```json
{
  "result_package_id": "result_pkg_...",
  "acked_by_install_id": "install_...",
  "received_asset_ids": ["artifact_..."],
  "verified_checksums": true,
  "checksum_mismatch_ids": []
}
```

`verified_checksums=true` 且 `checksum_mismatch_ids=[]` 是 Server 接受 Ack 的前提。

## 6. 固定联调与验收基线

双方共同基线包是：

[`contracts/exchange/v1/client_execution_package.browser_agent_outline.json`](../contracts/exchange/v1/client_execution_package.browser_agent_outline.json)

### 6.1 联调顺序

```text
1. App 根据 fixture 生成等价结构的 outline 包
2. Server `POST /aigc/v1/execution-packages/validate` 预检（无副作用；校验成功后再 Init -> Upload）
3. Init -> Upload
4. 开发环境 Run 或受控 auto-run
5. 轮询 Status 至 completed / failed / canceled
6. 获取 Result Package，检查步骤验证、审计日志和资产 checksum
7. 下载需要的素材
8. Ack
```

联调成功不只表示“接口 200”，还必须同时满足：

- 包通过 schema、身份、hash、sandbox 和 `node_id` 对齐校验；
- 所有 required validation 有真实浏览器证据且通过；
- 成功结果有截图、trace 和阶段事件；
- 发生允许修复时，结果包含可解释的 `patch_ledger`；
- 发生拒绝修复或业务冲突时，结果停止且有脱敏失败诊断；
- App 仅在 checksum 一致后 Ack。

### 6.2 Server 已覆盖的受控验收

当前代码已有固定测试覆盖：

- Outline fixture 的加载、结构和 hash 校验；
- Runtime Router 将 outline 包路由到新 Browser Agent 路径；
- 目标语义、域名、控制面路径、破坏性动作和必填验证的阻断；
- App 已批准 selector alternative 的全链路修复；
- 忙碌页面的有界等待修复；
- 服务器自创 selector 被 Repair Policy 拒绝；
- 结果包中的验证报告、Patch Ledger 与 JSONL 阶段审计日志。

这证明受控逻辑链路，不等同于任意客户网站或生产凭据已经验收。

## 7. 版本与变更治理

### 7.1 兼容规则

- `schema_version`、runtime、子对象 schema 和字段含义必须遵循上位协议。
- 新增非业务字段只能是可选字段；旧 Server 可以忽略未知的可选字段。
- 将可选字段改为必填、修改字段语义、增加可自动修复权限、允许新动作类型或改变安全含义，必须先更新上位协议并升级 runtime/schema（例如 `browser-agent-outline-v2`）。
- Server 不支持的 runtime 或版本必须返回 `unsupported_runtime` 或结构化版本错误；不得降级成 legacy TS 或尝试猜测执行。
- 任何协议变更必须同步更新：上位协议、JSON fixture、App 产包测试、Server 校验测试、端到端验收用例和本文件的能力矩阵。

### 7.2 变更流程

```text
提出变更问题与示例包
  -> 更新上位协议草案
  -> 评审字段语义、审批影响和安全边界
  -> 同步更新 fixture / DTO / 校验器
  -> App 产包测试 + Server 接收测试
  -> 固定验收包通过
  -> 标记新版本可用
```

Server 不得通过私有字段、提示词文字或隐式默认值绕过协议。App 也不得要求 Server 在未声明的字段上修改业务逻辑。

## 8. Server 能力矩阵和开放状态

| Server 能力 | 状态 | App 联调处理方式 |
| --- | --- | --- |
| Outline 主路径、字段结构和 hash 校验 | 已实现 | 可立即对接 |
| `node_id`、顺序、目标、动作、输入和目标合同一致性检查 | 已实现 | 可立即对接 |
| 受限浏览器观察、执行、截图、trace、required outcome 验证 | 已实现，真实浏览器环境需单独验收 | 使用无敏感测试站点联调 |
| 已批准 selector / wait 的有界自动修复及审计 | 已实现 | App 必须在合同中预先声明权限和候选项 |
| 结果包、checksum、资产交付、Ack | 已实现 | 可立即对接 |
| 本地编辑器接收录屏和素材 | 已实现 | 仅消费执行结果，不改变 App 执行包协议 |
| `allowed_domains`、禁止页面、控制面路径与非破坏性限制 | 已实现 | App 必须完整给出安全边界 |
| `allowed_origins` / `allowed_routes` 的逐路由强制白名单 | 已实现 | App 字段已固定；仍需与浏览器隔离、禁止页面和非破坏性策略共同使用 |
| `secret_ref` 的真实 Vault / 输入 broker 注入 | 未开放 | 不发送真实登录凭据或依赖 `secret_ref` 的动作 |
| 生产密文包在隔离 Worker 中解密并执行 | 未开放 | 加密 `payload_ref` 当前仅可登记，不可完成真实执行 |
| Container/MicroVM 的生产隔离、受限代理和临时凭据回收 | 待基础设施落地 | 不可将本地 sidecar 等同生产隔离 |
| 正式 HTTP 预检接口 | 已实现并通过 outline fixture 验收 | 先调用 `/aigc/v1/execution-packages/validate`，通过后再 Init -> Upload |
| 上传后生产调度和正式取消 API | 待共同决策后实现 | 当前仅使用开发 `/dev` 路径联调 |
| 真实浏览器固定七场景 Gate | 已确定、环境待恢复后重跑 | 当前结果不作为生产放行结论 |

## 9. 联调错误和处置原则

Server 对请求错误返回稳定结构：

```json
{
  "error": {
    "code": "client_execution_package_invalid",
    "message": "stage_approval_plan is required",
    "details": [
      {
        "field": "payload.executable_script_bundle.stage_approval_plan",
        "reason": "required",
        "message": "stage_approval_plan is required",
        "hint": "Regenerate the browser-agent-outline-v1 package after local approval."
      }
    ],
    "correlation_id": "srv_...",
    "retryable": false
  }
}
```

建议双方共同处理以下错误码：

- 上传或认证：`cloud_auth_unavailable`、`cloud_upload_failed`、`payload_too_large`、`payload_digest_mismatch`、`bundle_hash_mismatch`；
- 协议：`client_execution_package_invalid`、`unsupported_runtime`、`browser_agent_contract_violation`；
- 运行：`selector_resolution_failed`、`outcome_verification_failed`、`recording_failed`；
- 交付：`artifact_checksum_mismatch`。

包结构、hash、权限或业务冲突的错误不可盲目重试；App 必须修正产包或重新审批。网络、会话过期等显式 `retryable=true` 的错误才可重试。

## 10. 未决生产能力：给 App 的简要说明

以下事项不改变 App 当前的 outline 包格式，也不要求 App 现在为它们新增私有字段。它们依赖共同的安全、部署或交付选择，因此在双方确认前不开放给真实敏感任务。

| 未决项 | 为什么暂不能单方确定 | App 当前应如何处理 |
| --- | --- | --- |
| Vault / input broker | 需要确定 Vault 产品、secret 创建者、授权主体、生命周期、审计和撤销责任 | 可以继续传 `secret_ref` 语义；不要传真实密钥，也不要依赖 Server 已能完成登录 |
| 加密 `payload_ref` 解密执行 | 需要确定对象存储、KMS/客户自带密钥、解密身份、明文保留和删除策略 | 生产密文包可按上位协议准备；当前只能登记，不会执行 |
| Container/MicroVM 与受限代理 | 需要确定部署平台、网络出口 IP、资源配额、客户 IP 白名单和运维责任 | 仅用无敏感测试站点做开发联调；不要将本地 sidecar 当生产隔离 |
| 生产调度、取消、重试、回调 | 需要确定队列、并发、超时、幂等、回调签名和 App 接收方式（轮询/SSE/Webhook） | 当前以状态轮询和开发 `/dev` 路径为准，不依赖生产自动启动 |
| 真实客户站点验收 | 需要客户允许的测试 URL、账号和 IP 白名单，但不应交给本地明文测试 | 先使用无敏感测试包；生产前共同安排受控验收 |

### 10.1 不依赖外部决策的 Server 开发顺序

1. 在无敏感测试站点完成 App 发包 -> Server 预检 -> Init -> Upload -> 执行 -> 结果 -> Ack 的联调验收；
2. 将真实 `OutcomeVerifier` 实现按既定 DTO 注入运行时，并对齐阶段报告与结果包；
3. 再根据本节未决项的共同决定，开发 Vault、密文解密 Worker、生产隔离和调度。

## 11. 参考

- 上位协议：[app-server-browser-agent-outline-protocol.md](app-server-browser-agent-outline-protocol.md)
- 主交换协议：[client-cloud-exchange-protocol.md](client-cloud-exchange-protocol.md)
- 执行包协议：[executable-recording-script-bundle-v1.md](executable-recording-script-bundle-v1.md)
- 开发 HTTP 通道：[dev-exchange-http-test-channel.md](dev-exchange-http-test-channel.md)
- 固定 fixture：[client_execution_package.browser_agent_outline.json](../contracts/exchange/v1/client_execution_package.browser_agent_outline.json)
- Server 校验实现：[exchange_validation.go](../backend/internal/model/exchange_validation.go)
- Server 路由实现：[exchange_runtime_router.go](../backend/internal/app/exchange_runtime_router.go)
