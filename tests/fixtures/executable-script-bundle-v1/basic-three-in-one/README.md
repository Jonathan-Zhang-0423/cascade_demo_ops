# Cascade DemoOps 三合一执行包示例

这个目录是一份给内部同事体验 App 审批页的最小示例包，保留 v1 的基本格式：

- `approval.md`：中文思路文档，默认给业务用户审批阅读。
- `plan.json`：`ExecutionScriptDocument`，机器可审计的执行计划源。
- `recording-script.ts`：受限 TypeScript Playwright 录制脚本。
- `bundle.json`：`ExecutableRecordingScriptBundle`，把三份产物组织到同一个脚本包里。
- `manifest.json`：轻量目录索引，方便测试工具或人工快速识别。

注意：

- 这是 fixture 示例，不包含真实客户源码、账号、cookie、token 或 API key。
- 凭据仅使用 `secret_ref` 示例：`vault://demo/local-login`。
- hash 字段使用 `example_sha256_*` 占位，真实生成链路会由后端按 canonical JSON 和脚本内容计算。
- 云端执行前仍必须经过 bundle validator、AST 安全检查、allowed domain 检查和人工审批。
