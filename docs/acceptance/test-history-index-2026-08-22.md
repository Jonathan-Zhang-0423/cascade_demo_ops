# Server 端到端测试历史归档索引（截至 2026-08-22）

本索引用于防止新测试与旧版旁路、fixture 或人工审核结果混淆。除“正式 App→Direct”一类外，其余均不代表正式联调成功。

## A. Server 准备度/协议测试（不启动真实 App 正式包）

- Go Direct 协议、lease、加密、重放、分块、checksum、TLS、Worker loopback 测试：`backend/cmd/direct*`、`backend/internal/direct`、`backend/internal/app/*direct*_test.go`。
- Preflight 报告：`artifacts/direct-preflight/`。其中 `app_formal_run=false` 是设计要求，不能作为 App→Server 成功证据。
- Server-controlled fixture、test waiver 和 recovery 演练：`artifacts/dev-test-only/` 及 `directsmoke` 相关日志。用途仅为协议/恢复准备度。

## B. Visible browser / 人工审核结果

- 同事成功成片审核包：`C:\Users\15193\Desktop\CascadeAI-成片人工审核包-20260819-182959`。
- 该目录证明真实页面、录屏、编辑器、MiniMax H3、Seedance 和 MP4 后半链路可行；它没有 Direct lease/receipt/job/ACK 和完整 App 原始交换包，因此不能证明正式 Direct 自动登录。

## C. Unattended App→Server 尝试

原始尝试目录统一位于：`artifacts/dev-test-only/unattended-app-server-e2e/`。

- 2026-08-11 至 2026-08-20 多次运行：产生了 App 请求/包和阻断报告，但大量运行的 `source_binding_status=unverified`、`effective_mode=page_only` 或 readiness=blocked；这些是失败/准备度证据。
- 2026-08-21 运行目录：`20260821-150532`、`20260821-151422`、`20260821-152301`、`20260821-153014` 等。部分包显示 `source_binding=confirmed/mixed`，但必须以对应 App 生产审计、不可变原包和 Direct 交换证据三者同时存在为准，不能仅看 manifest。

## D. 最近 Direct job 失败归档

证据根目录：`.cascade-dev/artifacts/direct/`。

- `direct_job_HtFJdfl3gXhwhEM3z4Qr3_gT`
- `direct_job_XG4gXNhcRzcebddA3jF1DFxF`
- `direct_job_0QNMh0HUlvqay2laHp5TGJgR`
- `direct_job_UeSt4V16MHJIglTJrYcI5E4p`（最近一次）

最近一次的关键证据：

- `recording/browser-agent-stage-events.jsonl`
- `recording/replay-manifest.json`
- `recording/browser-agent-trace.zip`
- `recording/page@*.webm`
- `recording/stage-001-business_stage_session_setup-before.png`
- `recording/stage-001-business_stage_session_setup-after.png`

判定：Server 确实启动了 Direct Worker/Chromium 并采集了第一阶段证据，但自动登录停留在 `/login`；失败首因是无效占位凭据（表单提示 `xxx` 缺少 `@`），不是编辑器、Seedance 或 TTS。后续业务阶段没有合法启动，不能据此推断下游失败。

## 归档规则

每次新测试必须在独立目录保存：

1. App 原始包与不可变摘要；
2. App 生产/审批审计；
3. Direct lease、package receipt、credential receipt、job 状态、ACK；
4. 登录门禁报告；
5. StageEvents、截图、Trace、WebM、StepResults、ValidationReports；
6. 编辑器、Seedance、TTS 调用与候选/成片元数据；
7. 最终结果包、双规格 MP4、checksum/verified marker；
8. `classification`：`formal_app_direct`、`server_preflight`、`fixture_waiver`、`visible_manual_review` 或 `failed_login_gate`。

缺少 App 生产审计或来源绑定证据时，必须标为 `not_formal_app_run`，不能使用“端到端成功”字样。
