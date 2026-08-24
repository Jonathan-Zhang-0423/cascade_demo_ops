# Formal App→Server E2E 唯一路径与基线（2026-08-22）

## 目的

从本文件起，正式端到端验收只承认一条路径：

```text
当前 App 源码
→ App 正式生成 browser-agent-outline-v1 原始包
→ App 审批/签名并冻结 package、bundle、plan、policy、source 摘要
→ Direct v1 lease/加密上传/凭据 envelope
→ Server Worker claim
→ 自动登录门禁（/login → /app）
→ Server 按原 Outline 执行真实 Chromium
→ StageEvents/截图/Trace/WebM/StepResults
→ OutcomeVerifier/编辑器/Seedance/TTS
→ 双规格 MP4、结果包、checksum、verified marker、ACK
```

Fixture、test waiver、Server Dev HTTP 重构包、人工审核包、visible-browser 旁路和 legacy Exchange 只能作为准备度或局部证据，不得标记为 App→Server 正式联调成功。

## 唯一回归输入

- 目标 URL：`http://127.0.0.1:5000/app`
- 本地 App 源码：`D:\备份Cascade-main\Cascade-main`
- 业务：登录后进入工作台；新建项目；输入“贪吃蛇游戏”；点击构建；输入完整贪吃蛇需求；等待可观测完成状态/预览/日志/结果。
- 登录：App 包提供短期、作用域受限的 `credential_grant`；Server 只在 Worker 内存中消费一次，不把秘密写入包、日志、截图、Trace 或结果 JSON。
- 录制/输出：沿用已审核的双规格与测试 TTS 策略；测试 TTS 不固化到生产默认配置。

## App 原始包入场门禁

Server 只有在以下证据齐全时才允许启动正式 Direct job：

1. `client_execution_package.json` 来自当前 App 正式生成入口，而非 Server 重构或手写。
2. `package_id`、package SHA-256、bundle hash、plan hash、policy hash、source snapshot digest 均非空且相互一致。
3. 包内 `metadata.producer`、App 运行审计和不可变包保存路径可以独立证明生产者是 App。
4. `source_binding_summary.status=confirmed`，且不是仅页面来源的旧包。
5. `credential_grants` 与 Direct credential envelope 的 `grant_id/secret_ref/package_digest/expiry/scope` 完全一致。
6. App 原始包、审批记录、阻断报告和原始摘要在上传前保存为只读证据。

仅有 `app_formal_run=true` 字段不足以证明第 1 项；必须同时存在生产审计和摘要绑定证据。

## 登录专用门禁

完整业务阶段前先执行一次短门禁：

1. 检查 grant 是否可解析、是否未过期、域名和操作范围是否匹配。
2. 启动隔离 Chromium，导航到目标 URL。
3. 自动填写并提交登录表单，凭据只存在 Worker 内存 RPC。
4. 记录脱敏的 `login_attempt_started`、`login_form_resolved`、`login_submitted`、`login_transition` 或明确失败码。
5. 只有观察到 `/login → /app` 且工作台元素可见，才开始 Trace/WebM 和业务阶段。

登录失败必须 fail-closed：不启动业务阶段，不把“未执行节点、缺 MP4、缺下游产物”累计为新的首因。结果应包含登录页截图、脱敏诊断、URL/title、selector 解析结果和凭据契约状态。

## 正式成功判定

必须同时满足：

- App 原始包来源门禁通过；
- Direct receipt、credential receipt、job 状态和 ACK 可追溯；
- 登录门禁通过；
- 每个业务 stage 有 action、runtime observation、success condition 和 evidence refs；
- OutcomeVerifier 通过；
- 原始录屏、Trace、截图、StageEvents、StepResults、双规格 MP4、模型/TTS 标记、checksum 和 verified marker 齐全；
- 结果包最终状态为 `completed`，而不是仅有 Chromium 录屏或局部视频。

## 当前阻断

本基线建立时不能把最近 Direct job 视为正式成功：最近运行停在 `/login`，截图中的表单校验提示为占位邮箱 `xxx` 缺少 `@`；并且当次包缺少可独立核验的 App 正式产包证明。需先由 App 重新生成一份可用、不可变、带有效 grant 的原始包，再继续 Server 侧验证。

另外，现有 `backend/cmd/directsmoke` 仅覆盖 Direct package 上传/结果轮询，不负责上传 credential envelope；因此它不能直接作为本次“凭据包→自动登录”正式回归命令。正式命令必须同时完成 package receipt 和 credential receipt，并在同一 job/package digest 下绑定。
