# Direct 无人值守 App→Server 端到端验收编排 v1

本说明对应 `tests/server-e2e/scripts/run-app-server-e2e-unattended.ps1`，仅用于本机
`ProfileDev`/development 测试。它不修改 App 规则，不修改 Validation Agent 或
OutcomeVerifier，也不把测试包标记为正式生产交换成功。

## 自动化边界

脚本通过现有 Dev Bridge 的 App 正式编排入口提交 `UserInput`，由 App 的现有
InputContext、Requirement、Code/Page Understanding、Project Intelligence、
Stage Plan、Outline 和 package builder 生成原始包。脚本不会自行编写业务
selector 或重写包字段。

生成原始包后，脚本会：

1. 将未经改写的 package 保存到工作区 `artifacts/dev-test-only/...`；
2. 根据 App 原始 `confidence_summary.blocking_reasons` 自动提取 node ID；
3. 调用 raw-file 旁路签发有限豁免；
4. 用 `auto_login=true` 打开真实本地 Cascade 页面；
5. 由 Server 按原 Outline 执行、录屏、采集截图/Trace/StepResults/日志并渲染结果包。

如果 App 没有返回 `readiness=blocked` 且带 node-scoped 阻断原因，脚本会停止，
不会伪造豁免。`-NoRun` 可只生成并检查原始包，不启动浏览器。

## 凭据边界

登录凭据只能放在启动 Engine Server 的进程环境变量：

```powershell
$env:CASCADE_DEV_VISIBLE_LOGIN_EMAIL = "<local-test-email>"
$env:CASCADE_DEV_VISIBLE_LOGIN_PASSWORD = "<local-test-password>"
```

脚本不会把凭据写入 App 包、raw-file 请求、可见浏览器 prepare 请求、StageEventLog、
Trace、截图、视频或结果 JSON。自动登录失败时 fail-closed。

## 运行前检查

- Cascade 本地真实页面已启动，例如 `http://127.0.0.1:5000/app`；
- Engine Dev Server 已启动并监听 `127.0.0.1:4317`；
- `artifacts/direct-preflight/latest/preflight.json` 为 `ready=true`；
- Node、video-worker、FFmpeg、FFprobe 可执行；
- 仅使用本地 loopback URL。

## 运行

```powershell
cd D:\Engine-7-8
.\tests\server-e2e\scripts\run-app-server-e2e-unattended.ps1
```

输出目录中的 `client_execution_package.json` 是 App 原始包，
`app-package-manifest.json` 记录哈希和阻断报告摘要，`run-result.json` 记录本次
Server 测试结果。只有结果包、StageEventLog、Replay Manifest、验证报告和 MP4
全部存在且校验通过，才可判定本地测试成功。
