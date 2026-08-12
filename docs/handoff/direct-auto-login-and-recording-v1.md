# Direct 本地自动登录与真实录屏补充说明 v1

## 适用范围

本说明只适用于 `ProfileDev` 且非生产环境的本地真实页面验收。它不改变 App 正式执行包、不修改 Validation Agent/OutcomeVerifier 规则，也不进入 Exchange 正式链路。

## 自动登录边界

- `POST /v1/desktop/dev-visible-browser-agent/prepare` 可使用 `auto_login=true`；
- 凭据只从启动 Engine Server 的进程环境变量读取：
  - `CASCADE_DEV_VISIBLE_LOGIN_EMAIL`
  - `CASCADE_DEV_VISIBLE_LOGIN_PASSWORD`
- 请求体禁止携带 `email`、`password`、Cookie、Token 或 storage state；
- Go Server 只将凭据通过 loopback Worker 的一次性 RPC 传入内存；Worker 只返回脱敏 URL/title；
- 自动登录失败立即终止，不回退到猜测 selector，也不修改原始 App 包；
- 登录输入使用默认遮罩选择器，凭据不得进入日志、StageEventLog、Trace、截图或结果 JSON。

## 原始包与豁免

自动登录只负责建立本地测试会话。业务动作仍必须来自 App 正式生成的原始 `browser-agent-outline-v1` 包。Server 旁路豁免必须同时满足：

1. 原包字节、规范化摘要、Bundle hash、Plan hash 保持不变；
2. App confidence summary 为 `blocked`；
3. 每个请求豁免的 node 都在 App 阻断报告中有同 node 前缀的明确阻断原因；
4. 只允许缺少非破坏性分类的具体 interaction；
5. 不得豁免 destructive action、来源、路由、禁止页或结果验证规则。

## 录屏和结果包

自动登录模式创建带遮罩的 WebM；Playwright Trace 在登录完成、包校验和执行策略应用后才启动。业务执行结束时，Server 显式关闭 Worker 会话并将 close 返回的 raw WebM、Trace、恢复截图合并入 `RecordingResultPackage`，再执行 MP4 渲染、Replay Manifest、StepResults 和 ValidationReports 持久化。

如果没有 App 原始包，或包目标不是当前 `http://127.0.0.1:<port>` 真实页面，只能做 Server 准备度测试，不能声称端到端验收成功。
