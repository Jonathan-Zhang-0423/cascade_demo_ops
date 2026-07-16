# 可执行录制脚本包协议 v1

`ExecutableRecordingScriptBundle` 是 App 端生成、用户审批、云端校验执行的统一脚本包。v1 支持两个 runtime：

- `browser-agent-outline-v1`：新产品主路径。App 端生成高置信 stage 审批 JSON、Browser Agent 脚本大纲、prompt policy 和项目理解证据包；云端 browser agent 在这些边界内自适应探索、生成/修正最终可执行脚本。
- `playwright-restricted-sandbox`：兼容旧路径。App 端仍可上传确定性生成的受限 TypeScript Playwright 脚本。

## Browser Agent Outline 主路径

`browser-agent-outline-v1` 下，包内必须包含：

- `plan_json`：`ExecutionScriptDocument`，机器审计用执行计划。
- `stage_approval_plan`：`StageApprovalPlan`，用户审批用 JSON，描述每个 stage 的目标、时长、业务意图、目标路由、输入内容、成功状态、风险和证据链。
- `script_outline`：`BrowserAgentScriptOutline`，给云端 browser agent 的路线图，描述目标页面、组件、候选 selector/role/name、交互动作、等待条件、截图点和可探索范围。
- `agent_prompt_policy`：`BrowserAgentPromptPolicy`，明确不可修改字段和可由服务器自适应修改的字段。
- `project_understanding_dossier` 或 `understanding_dossier_ref`：需求相关项目理解证据包，只包含摘要、hash、证据引用和少量脱敏片段。
- `approval_markdown`：中文思路/审批文档。

`playwright_script` 在该 runtime 下可以为空；若存在，也只能作为兼容/诊断材料，不是 App 端主产物。

推荐 manifest：

```json
{
  "language": "browser-agent-outline",
  "runtime": "browser-agent-outline-v1",
  "entry_function": "runBrowserAgentOutline",
  "dependency_allowlist": []
}
```

## 可修改边界

服务器 browser agent 可以修改：

- selector 与 selector alternatives
- 等待条件和页面稳定策略
- 非破坏性探索路径
- 截图/录制 capture timing

服务器 browser agent 不得修改：

- 用户需求和 stage 顺序
- `stage_approval_plan.stages[].objective`
- 业务意图和填充语义
- `secret_ref`
- `allowed_domains`、`forbidden_pages`、redaction policy
- 访问控制面路径，例如 `/aigc`、`/.well-known`、`/v1/*`、`execution-packages`、`app-installations`

不确定项必须进入 `uncertainty_report` 或失败诊断，不得猜测 route、组件、API 或 selector。

## Hash 绑定

`browser-agent-outline-v1` 必须绑定：

- `plan_hash_sha256`
- `stage_plan_hash_sha256`
- `outline_hash_sha256`
- `prompt_policy_hash_sha256`
- `understanding_dossier_hash_sha256`，如果内联 dossier
- `markdown_hash_sha256`
- `bundle_hash_sha256`

云端 intake 校验这些字段，确保用户审批的 Markdown、Stage JSON、大纲、prompt policy 和证据包没有漂移。

## Legacy TypeScript 兼容路径

`playwright-restricted-sandbox` 下，TypeScript 脚本仍必须导出：

```ts
export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult>
```

脚本只能使用注入的 `ctx.page`、`ctx.secrets`、`ctx.capture`、`ctx.assert`、`ctx.log`。v1 不允许外部依赖，`dependency_allowlist` 必须为空。

兼容 TS 脚本中禁止出现：

- `import`、`require`、`eval`、`Function`
- `process`、`fs`、`child_process`、`globalThis`
- `fetch`、`XMLHttpRequest`、`WebSocket`
- raw password、token、API key、private key

敏感输入只能通过 `secret_ref` 或 `input_ref` 派生，并由运行时通过 `ctx.secrets` 注入。

## 校验规则

云端执行前必须校验：

- runtime 与 `entry_function` 匹配：outline 使用 `runBrowserAgentOutline`，legacy TS 使用 `runCascadeRecording`
- `manifest.step_node_ids` 不得包含 plan 外节点
- outline 模式中每个 plan node 必须同时出现在 `stage_approval_plan` 和 `script_outline`
- 每个 stage 必须有业务目标、证据链、等待/截图策略，建议时长不低于 10 秒
- prompt policy 必须声明不可修改字段、可修改字段和 system prompt
- 所有 URL/探索范围必须属于 `allowed_domains`
- 不得访问 `forbidden_pages` 或控制面路径
- 打码策略必须进入 `security_policy.redactions`

这些校验只是第一道门，不是完整安全边界。通过校验后仍必须进入云端 sandbox runner，由 `RecordingRunSpec.sandbox_policy` 约束网络、文件系统、浏览器上下文、secret 注入、artifact 加密和失败诊断脱敏。生产环境不能直接用 dev/local sidecar 执行真实客户数据。

## 审批视图

桌面 App 默认展示：

- 中文 `approval_markdown`
- `StageApprovalPlan`
- `BrowserAgentScriptOutline`
- `BrowserAgentPromptPolicy`
- `ProjectUnderstandingDossier` 摘要、证据链和不确定项

上传前必须确认：

- 已审批 stage 目标、时长、顺序和业务意图
- 不上传完整源码
- 凭据只通过 `secret_ref` 授权
- 打码规则已复核
- allowed domains / forbidden pages 已复核
- Cascade 云端执行 IP 或网络访问策略已确认
