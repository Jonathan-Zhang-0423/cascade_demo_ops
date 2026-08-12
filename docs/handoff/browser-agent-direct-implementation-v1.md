# Browser Agent Direct API v1 — Server implementation note

本轮按 App 侧 `cascade.browser_agent_direct.v1` 对接契约新增 Server 直连层：

- `backend/internal/direct`：协议类型、Ed25519 installation/lease 签名、HMAC 请求签名、防重放、HKDF-SHA256 + AES-256-GCM 消息、lease/job/credential/artifact 状态。
- `backend/internal/app/direct_http.go`：`/v1/direct/*` 控制与数据接口，以及 loopback Worker API。
- `backend/cmd/directgateway`：生产 Gateway 启动入口。控制口必须使用 TLS；Worker 固定监听 `127.0.0.1:18444`。

## 配置

生产启动必须提供：

```text
CASCADE_DIRECT_PUBLIC_HOST
CASCADE_DIRECT_BOOTSTRAP_TOKEN
CASCADE_DIRECT_WORKER_TOKEN
CASCADE_DIRECT_TLS_CERT
CASCADE_DIRECT_TLS_KEY
CASCADE_DIRECT_STATE_PATH（可选；默认使用运行时数据目录）
```

网络边界：控制/TLS `18443`，数据 lease 端口 `24000-24031`，Worker `127.0.0.1:18444`。

## 当前边界

Direct 包上传只接受 `browser-agent-outline-v1`，先执行 Server-owned package validation；相同 installation + package ID + final package digest 的相同 payload 返回原 job，payload 漂移返回 `package_idempotency_conflict`。凭据只在内存中保存并在 Worker consume 后清理，永不进入 Gateway snapshot。普通 job、lease、结果、ACK 和 artifact 使用受保护的原子快照恢复；running job 重启后重新排队，需要凭据的 job 回到 `awaiting_credentials`。结果提交要求 job/package/cloud job/source digest 一致，并校验 ValidationReport。artifact 以最多 4 MiB 的独立 chunk 提供。

现有 Browser Agent runner、OutcomeVerifier、Stage Event Log 和 Replay Manifest 继续作为执行层复用。旧 Exchange 路由仍保留给兼容性/本地运行时；生产 Desktop 接入 Direct 时不得回退到 Exchange。
