# App ↔ Server Browser Agent Outline 对接协议 v1

本文档给服务端同事对接 App 端“三合一执行包”使用。当前产品主路径不再要求 App 生成最终 Playwright TS；App 端负责生成可审批、可追责、低体积的执行路线图，Server Browser Agent 负责运行时自适应探索、脚本补全、录制和失败诊断。

## 目标边界

App 端输出三份用户/机器共同审批材料：

- `approval_markdown`：给项目经理读的中文说明，解释演示目标、阶段、风险和证据。
- `stage_approval_plan`：业务阶段 JSON，约束 stage 顺序、目标、输入语义、路由状态、成功标准和安全边界。
- `script_outline`：给 Server Browser Agent 的执行大纲，描述候选路由、组件语义、role/name/selector 线索、等待条件、截图点和可探索范围。

服务端不得把 `script_outline` 当成最终脚本逐字执行。它是受约束路线图：Browser Agent 可以修正 selector、等待策略和非破坏性探索路径，但不能改用户意图、stage 顺序、输入语义、secret_ref、allowed domains、forbidden pages 或安全边界。

## 当前联调入口

公网 nginx 前缀：

```text
https://cascadeai.cn/aigc
```

直连本地/内网 dev server：

```text
http://127.0.0.1:4317
```

当前 dev 通道认证：

```http
Authorization: Bearer <dev-exchange-token>
X-Cascade-Org-ID: org_devsmoke
Content-Type: application/json
```

生产认证可以升级为 app installation session + envelope signature，但不应改变 `ClientExecutionPackage` 与 `ExecutableRecordingScriptBundle` 的业务字段语义。

## 生命周期

前端/App 必须把本地生成和服务器执行分成两段展示：

```text
本地理解与脚本生成
  -> RequirementRead
  -> CodeRead / IntentDrilldown
  -> ProjectIntelligence / BusinessStagePlan
  -> GraphBuilder
  -> ScriptPackage
  -> 用户审批三合一包

服务器上传与录制
  -> init
  -> upload
  -> server validate
  -> browser agent run
  -> recording/rendering
  -> result
  -> ack
```

如果服务器上传失败，App UI 应保留本地三合一包，并只把错误归类到 `cloud_upload_failed` / `cloud_auth_unavailable` / `recording_failed`，不得覆盖成本地规划失败。

## HTTP API

所有路径在公网部署下都加 `/aigc` 前缀；语义与无前缀路径一致。

### 1. Init

```http
POST /aigc/v1/execution-packages/init
```

请求：

```json
{
  "org_id": "org_devsmoke",
  "project_id": "project_...",
  "package_kind": "client_execution",
  "producer": {
    "app_version": "cascade-demoops-app",
    "runtime_profile": "desktop",
    "install_id": "install_..."
  }
}
```

响应至少包含：

```json
{
  "upload_id": "upload_...",
  "exchange_package_id": "xpkg_...",
  "accepted_package_kinds": ["client_execution"],
  "max_payload_size_bytes": 524288,
  "server_public_keys": [],
  "installation_required": false,
  "session_expires_at": "2026-07-21T12:00:00Z"
}
```

### 2. Upload

```http
POST /aigc/v1/execution-packages
```

dev 明文联调请求：

```json
{
  "upload_id": "upload_...",
  "envelope": {
    "schema_version": "demoops.exchange_envelope.v1",
    "payload_schema_version": "demoops.client_execution_package.v1",
    "payload_digest_sha256": "<canonical payload sha256>",
    "producer": {
      "app_version": "cascade-demoops-app",
      "runtime_profile": "desktop",
      "install_id": "install_..."
    },
    "policy": {
      "max_execution_window_sec": 1800
    },
    "crypto": {
      "dev_plaintext": true
    }
  },
  "payload": {
    "schema_version": "demoops.client_execution_package.v1"
  }
}
```

生产请求应改为 `payload_ref`，不内联明文 payload：

```json
{
  "upload_id": "upload_...",
  "envelope": {
    "payload_ref": {
      "artifact_id": "payload_artifact_1",
      "sha256": "<ciphertext sha256>"
    }
  },
  "payload_ref": {
    "kind": "artifact",
    "artifact_id": "payload_artifact_1",
    "uri": "s3://cascade-exchange/payload.enc",
    "sha256": "<ciphertext sha256>",
    "size_bytes": 123456,
    "encrypted": true,
    "sensitive": true,
    "compression_alg": "gzip"
  }
}
```

服务端必须拒绝生产环境下未加密 payload_ref 的上传。dev 明文只允许在 development/test profile。

### 3. Status

```http
GET /aigc/v1/execution-packages/{exchange_package_id}/status
X-Cascade-Org-ID: org_devsmoke
```

建议状态流：

```text
accepted -> validating -> preparing_worker -> running_browser_agent -> recording -> rendering -> completed
```

失败状态：

```json
{
  "exchange_package_id": "xpkg_...",
  "status": "failed",
  "stage": "running_browser_agent",
  "failure_summary": {
    "code": "selector_timeout",
    "message": "Target button was not visible after adaptive search.",
    "failed_stage": "running_browser_agent",
    "failed_node_id": "stage_create_project"
  },
  "result_package_id": "result_pkg_..."
}
```

### 4. Result

```http
GET /aigc/v1/result-packages/{result_package_id}
X-Cascade-Org-ID: org_devsmoke
```

成功 result 必须包含：

- final demo video descriptor
- raw recording descriptor
- screenshot summary
- trace/debug descriptor
- checksum
- sandbox metadata
- delivery status

失败 result 必须包含：

- `failure_diagnostic`
- failure screenshot/trace artifact refs
- current URL/title
- redaction report
- `repair_request`

### 5. Ack

```http
POST /aigc/v1/result-packages/{result_package_id}/ack
X-Cascade-Org-ID: org_devsmoke
```

请求：

```json
{
  "acked_by_install_id": "install_...",
  "received_asset_ids": ["artifact_pkg_demo_video_001"],
  "verified_checksums": true
}
```

`verified_checksums=true` 是成功 ack 的必要条件。

## ClientExecutionPackage 结构

顶层 payload schema：

```json
{
  "schema_version": "demoops.client_execution_package.v1",
  "package_id": "pkg_...",
  "org_id": "org_devsmoke",
  "project_id": "project_...",
  "product_context": {},
  "workflow_graph": {},
  "recording_run_spec": {},
  "executable_script_bundle": {}
}
```

`workflow_graph` 可以保持精简，只需要覆盖 stage 节点、边、关键验证和证据引用。不要上传全局 selector 池、全量项目图、完整源码、完整 HTML、DOM dump 或大体积 dossier。

普通 outline 包体目标小于 200KB，硬上限建议 512KB。超过上限时应返回结构化错误 `payload_too_large`。

## ExecutableRecordingScriptBundle 主路径

`executable_script_bundle.script_manifest` 必须声明：

```json
{
  "language": "browser-agent-outline",
  "runtime": "browser-agent-outline-v1",
  "entry_function": "runBrowserAgentOutline",
  "dependency_allowlist": []
}
```

`browser-agent-outline-v1` 下必填：

- `plan_json`
- `approval_markdown`
- `stage_approval_plan`
- `script_outline`
- `agent_prompt_policy`
- `browser_agent_contract`
- `security_policy`
- `reproducibility`

`playwright_script.inline_source` 在该 runtime 下可以为空。服务端不得因为 TS 为空拒绝 outline 包。

## StageApprovalPlan 要求

schema：

```text
demoops.stage_approval_plan.v1
```

每个 `stage_approval_plan.stages[]` 至少包含：

- `id`
- `order`
- `node_id`
- `stage_kind`
- `route_state`
- `objective`
- `business_intent`
- `entry_route` 或 `candidate_routes`
- `interaction`
- `target_contract`
- `success_state`
- `wait_conditions`
- `capture_points` 或 `capture_plan`
- `confidence`

`stage_kind` 当前固定枚举：

```text
session_setup
business_action
business_input
mode_selection
business_submit
observe_progress
final_observe
```

`route_state` 当前固定枚举：

```text
unauthenticated
workspace
creation_flow
project_detail
build_running
```

时间要求不应写死。只有用户需求中明确写了“10 秒登录展示”“60 秒观察”等要求时，App 才会把它写入 `duration_ms` 或 `capture_plan.min_duration_ms`。服务端必须保留这些显式时长语义；没有显式时长时由 Browser Agent 按页面实际节奏处理。

## ScriptOutline 要求

schema：

```text
demoops.browser_agent_script_outline.v1
```

每个 `script_outline.stages[]` 必须和 `stage_approval_plan.stages[]` 通过 `node_id` 对齐。

关键字段：

- `entry_route`：进入该 stage 前建议页面或状态。
- `route` / `candidate_routes`：业务相关候选路由，不保证唯一。
- `expected_route_after_action`：动作后预期路由或状态变化，可运行时验证。
- `components`：候选组件语义，包含 role/name/text/test_id/selector 线索。
- `interactions`：操作语义，包含 click/fill/select/submit/wait/inspect/capture 等。
- `target_contract`：目标控件合同，约束 semantic_id、purpose、allowed roles/names、forbidden names、destructive。
- `allowed_exploration_scope`：产品域内可探索范围。

禁止把以下路径纳入 candidate route 或运行时探索：

```text
/aigc
/.well-known
/v1
/v1/execution-packages
/v1/app-installations
/result-packages
/debug
```

## BrowserAgentContract

schema：

```text
demoops.browser_agent_contract.v1
```

服务端必须执行以下边界：

可修改：

- selector
- selector alternatives
- wait strategy
- capture timing
- same-domain, non-destructive exploration path
- frame resolution

不可修改：

- user requirement
- stage order
- stage objective
- business intent
- action type semantics
- input semantic value
- `secret_ref`
- allowed domains
- forbidden pages
- destructive flag
- redaction policy

如果 runtime 页面与 stage/target contract 冲突，服务端应 `stop_and_report`，返回失败诊断，而不是擅自改业务目标。

## Hash 与一致性

服务端 intake 必须校验：

- `plan_hash_sha256`
- `stage_plan_hash_sha256`
- `outline_hash_sha256`
- `prompt_policy_hash_sha256`
- `browser_agent_contract_hash_sha256`
- `markdown_hash_sha256`
- `bundle_hash_sha256`

校验方式：对对应 JSON 使用稳定 canonical JSON digest；Markdown 对 UTF-8 内容取 SHA-256。任何 hash mismatch 返回：

```json
{
  "error": {
    "code": "bundle_hash_mismatch",
    "message": "Executable bundle hash does not match uploaded content.",
    "details": [
      {
        "field": "payload.executable_script_bundle.reproducibility.bundle_hash_sha256",
        "reason": "mismatch",
        "message": "bundle hash mismatch",
        "hint": "Regenerate the package after local approval and upload the matching bundle."
      }
    ],
    "correlation_id": "srv_...",
    "retryable": false
  }
}
```

## 错误格式

所有 4xx/5xx 错误使用稳定结构，不要只返回字符串：

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
        "hint": "Upload browser-agent-outline-v1 package with approval markdown, stage plan, outline, prompt policy, and contract."
      }
    ],
    "correlation_id": "srv_...",
    "retryable": false
  }
}
```

推荐错误码：

- `cloud_auth_unavailable`
- `cloud_upload_failed`
- `client_execution_package_invalid`
- `payload_too_large`
- `payload_digest_mismatch`
- `bundle_hash_mismatch`
- `unsupported_runtime`
- `browser_agent_contract_violation`
- `recording_failed`
- `selector_resolution_failed`
- `outcome_verification_failed`
- `artifact_checksum_mismatch`

## 安全与脱敏

服务端不得持久化或返回：

- raw password
- token / Authorization / cookie
- localStorage/sessionStorage secret
- `.env`
- full source code
- full HTML / DOM dump
- 完整截图中未打码的敏感字段

凭据只允许通过 `secret_ref` 进入包体。运行时 secret 注入由云端 vault 或 dev secret provider 处理，失败诊断也只能引用 `secret_ref`，不得回显值。

## Browser Agent 运行职责

Server Browser Agent 收到 outline 包后应：

1. 校验包体、hash、安全策略和 runtime。
2. 建立 isolated browser context。
3. 按 stage 顺序执行。
4. 在 `allowed_exploration_scope` 内寻找目标 route/component。
5. 使用 `target_contract` 解析语义等价控件。
6. 对 required validation 做阻塞校验。
7. 记录 stage history、截图、trace、current URL/title。
8. 成功后生成 final video、raw recording、screenshots、trace 和 checksum。
9. 失败时返回 redacted diagnostic + repair_request。

Browser Agent 不应访问 exchange/control-plane 接口来“寻找产品页面”。`/aigc` 只属于上传通道，不属于产品脚本规划或录制探索范围。

## 服务端验收清单

- 能接受 `client_execution_package.browser_agent_outline.json` fixture。
- 不因 `playwright_script.inline_source` 为空拒绝 outline runtime。
- 能校验 `stage_approval_plan`、`script_outline`、`agent_prompt_policy`、`browser_agent_contract` 和 hash。
- 能按 `node_id` 对齐 stage plan、outline、workflow graph。
- 能保留用户显式时长要求，不写死全局 10 秒/60 秒。
- 能把服务器上传失败与本地规划失败区分开。
- 能返回结构化 error details。
- 成功 result 可下载 video/screenshot/trace，并支持 ack。
- 失败 result 包含 screenshot/trace refs、current URL/title、repair_request 和 redaction report。

## 参考文件

- 样例包：[contracts/exchange/v1/client_execution_package.browser_agent_outline.json](../contracts/exchange/v1/client_execution_package.browser_agent_outline.json)
- 交换协议：[docs/client-cloud-exchange-protocol.md](client-cloud-exchange-protocol.md)
- 执行包协议：[docs/executable-recording-script-bundle-v1.md](executable-recording-script-bundle-v1.md)
- dev HTTP 通道：[docs/dev-exchange-http-test-channel.md](dev-exchange-http-test-channel.md)
- Go 模型：[backend/internal/model/executable_script.go](../backend/internal/model/executable_script.go)
- Go 校验：[backend/internal/model/exchange_validation.go](../backend/internal/model/exchange_validation.go)
