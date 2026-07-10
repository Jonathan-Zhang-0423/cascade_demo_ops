# 可执行录制脚本包协议 v1

`ExecutableRecordingScriptBundle` 是 App 端生成、用户审批、云端校验执行的双轨脚本包。它包含三份互相 hash 绑定的产物：

- `plan_json`：`ExecutionScriptDocument`，唯一审计源。
- `playwright_script`：由确定性生成器从 JSON plan 派生的受限 TypeScript 脚本。
- `approval_markdown`：中文思路文档，作为用户默认审批视图。

## 固定入口

TypeScript 脚本必须导出：

```ts
export async function runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult>
```

脚本只能使用注入的 `ctx.page`、`ctx.secrets`、`ctx.capture`、`ctx.assert`、`ctx.log`。v1 不允许外部依赖，`dependency_allowlist` 必须为空。

## 禁止项

脚本中禁止出现：

- `import`、`require`、`eval`、`Function`
- `process`、`fs`、`child_process`、`globalThis`
- `fetch`、`XMLHttpRequest`、`WebSocket`
- raw password、token、API key、private key

敏感输入只能通过 `secret_ref` 或 `input_ref` 派生，并由运行时通过 `ctx.secrets` 注入。

## 校验规则

云端执行前必须校验：

- `script_manifest.entry_function = runCascadeRecording`
- `plan_hash_sha256`、`script_hash_sha256`、`bundle_hash_sha256` 与内容一致
- 每个 `plan_json.steps[].node_id` 都出现在 TS 脚本中
- `manifest.step_node_ids` 不得包含 plan 外节点
- 所有 `ctx.page.goto()` URL 必须属于 `allowed_domains`
- 不得访问 `forbidden_pages`
- 打码策略必须进入 `security_policy.redactions`

这层 TS/AST 校验只是第一道门，不是完整安全边界。通过校验后仍必须进入云端 sandbox runner，由 `RecordingRunSpec.sandbox_policy` 约束网络、文件系统、浏览器上下文、secret 注入、artifact 加密和失败诊断脱敏。生产环境不能直接用 dev/local sidecar 执行真实客户数据。

## 审批视图

桌面 App 默认展示中文 `approval_markdown`，并提供 JSON plan 和 TS 脚本作为可展开技术审计项。上传前必须确认：

- 已审批执行路径
- 不上传完整源码
- 凭据范围与过期时间已复核
- 打码规则已复核
- Cascade 云端执行 IP 已加入客户环境白名单
