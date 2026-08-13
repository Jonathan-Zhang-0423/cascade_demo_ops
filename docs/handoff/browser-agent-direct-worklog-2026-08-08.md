# Browser Agent 直连链路工作日志（2026-08-08）

## 目标

Desktop App 不再以 DemoOps Exchange 作为正式上传或执行入口。正式路径为：

```text
App 本地理解/执行图
  → 权威执行包预览与人工审批
  → TLS 控制口 18443
  → installation 独占数据口 24000-24031
  → Browser Agent Gateway
  → loopback Worker 18444
  → 真实 Chromium/browser-agent-outline-v1
  → 加密状态/结果/分块素材
  → App checksum 校验、审核、编辑器交接、释放租约
```

## 已实现

- 固定 TLS 控制端口 `18443`，Worker API 强制 loopback `127.0.0.1:18444`。
- 每个 installation 在有效 lease 内复用一个独占数据端口；不同 installation 不共享端口。
- lease 分配/释放由 installation Ed25519 私钥签名，带时间戳和一次性 nonce。
- 数据请求绑定 lease、installation、端口、HTTP method/path、时间戳、nonce 和 body SHA-256。
- 包、凭据封套、结果和 artifact chunk 使用 lease token 派生的 HKDF-SHA256 + AES-256-GCM；AAD 包含路由及消息元数据。
- 重放、过期时间戳、跨 installation/lease 读取、消息类型/方向错误、chunk 大小错误和最终 SHA-256/字节数不匹配均 fail-closed。
- 凭据封套不写入 Gateway spool；Worker consume 后立即清理内存副本。Gateway 重启后含凭据任务必须重新上传封套。
- 普通 package/result/artifact 仅存在受限短期 spool；正式结果下载到 App-managed artifact root 后才写 verified marker。
- 生产 Desktop 旧 Exchange 方法在网络请求前返回 `legacy_exchange_disabled`，没有回退路径。
- direct 结果在 App 重载后恢复到 `/browser-agent-direct/artifact/media`，不再错误指向旧 Exchange 媒体 URL。
- 显式释放成功后项目状态进入 `lease_released`，后续状态/素材读取不会偷偷重新申请新端口。
- 网关重启或 lease 过期导致的恢复只针对仍为 direct、未显式释放的项目，并立即把新 lease/端口写回项目状态。

## Selector 交接结论

远端历史提交已核对：

- `9fdf7c2`：提高 selector 质量，优先可执行的业务控件，避免将 `main` 等观察面当成点击目标，并补足 selector/节奏测试。
- `2151a94`：运行时自适应选择器必须匹配动作类型，拒绝取消、停止、删除、重命名等负向候选；只允许同一业务语义内的候选。
- `cdddd83`：`input/textarea/select/button`、role、placeholder、name、aria-label 等表单/交互 selector 不得被误判为只读展示面；项目名节点必须保持 `fill`。

当前正式合同要求：

1. 业务动作必须有 `target_contract`、业务语义、Evidence ID 链和批准的 selector alternatives。
2. `data-testid` 只作为 `test_id/selector` 线索，不拼进无障碍名称；`allowed_names` 应保存真实可访问名称。
3. Server 只能在同一 stage、同一 Evidence ID、同一业务目标和已批准候选范围内适配 selector；唯一、可见、角色和名称兼容后才执行。
4. 目标冲突、目标不唯一、未批准 selector、跨域/禁页或无法验证成功状态时停止并返回脱敏诊断，不猜测、不伪造证据。

## 验证证据

- `go test ./...`：通过。
- Windows Desktop 目标测试：通过。
- Web Vitest：106 项通过；TypeScript typecheck 和生产 build 通过。
- 外部 TLS 预检：DNS、TCP 18443、证书主机名校验通过；未认证 health 只返回 401。
- 真实 Ubuntu 最小直连探针：动态端口分配、加密读取不存在任务（脱敏 `job_not_found`）和释放均通过。

## 尚未声称完成的项目

真实 Browser Agent 业务闭环仍须在真实 `cascadeai.cn` 上单独验证：正式包生成、人工审批、凭据封套、Worker claim、真实 Chromium 登录成功、业务动作、截图/视频/trace、状态轮询、分块下载、secret-leak 扫描、编辑器交接和释放。此前真实登录的确定性成功验证失败，因此不能把 fixture 或本地受控测试当作线上成功证据。

## 运维注意

- 公网只开放 TCP `18443` 和 `24000-24031`；`18444` 必须保持 loopback。
- 不输出 bootstrap token、Worker token、SSH 私钥、账号、密码、cookie 或完整 envelope。
- `test-runs/`、`tmp/`、`backend/internal/app/app_formal_e2e_test.go` 及 SSH 辅助脚本是本地用户材料，本次提交不纳入。
