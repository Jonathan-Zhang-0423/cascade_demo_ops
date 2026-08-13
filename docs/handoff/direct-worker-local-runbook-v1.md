# Direct Worker 本地运行说明 v1

Direct Gateway 与 Worker 按最新协议分离：Gateway 提供 TLS 控制/数据口，Worker 只通过 `127.0.0.1:18444` loopback API 领取和执行任务。

## 启动 Gateway

必须配置：

```text
CASCADE_DIRECT_PUBLIC_HOST
CASCADE_DIRECT_BOOTSTRAP_TOKEN
CASCADE_DIRECT_WORKER_TOKEN
CASCADE_DIRECT_TLS_CERT
CASCADE_DIRECT_TLS_KEY
```

可选配置：

```text
CASCADE_DIRECT_STATE_PATH
```

未设置时，Gateway 使用运行时数据目录下的 `direct_gateway_state/direct-v1-snapshot.json`。快照采用原子替换、`0600` 文件模式（Windows 下同时继承运行时数据目录 ACL）保存 lease、普通 job、结果和 artifact；不会保存 credential envelope、业务账号密码、Cookie 或登录 Token。lease token 属于恢复加密数据口所需的 Gateway 协议状态，会保存在受保护快照中且不得写入日志。需要凭据的任务在 Gateway 重启后统一回到 `awaiting_credentials`，必须由 App 重新上传短期 envelope。

```powershell
go run ./cmd/directgateway
```

本地便利模式可使用 `-embedded-worker`，但生产部署应单独启动 Worker 进程。

普通 `queued/running` job 在重启后恢复为可重新 claim 的 `queued`；旧进程遗留的 running partial artifact 会被移除，避免新 Worker 把不完整字节误认为正式结果。已完成结果、校验状态和 ACK 会随快照恢复。

## 启动独立 Worker

```powershell
$env:CASCADE_DIRECT_WORKER_TOKEN = "<worker-token>"
go run ./cmd/directworker -gateway-url http://127.0.0.1:18444
```

Worker 命令拒绝非 loopback 地址、非 HTTP 地址和非 `18444` 端口。Worker 不接收公网流量，也不读取 bootstrap token。

## 本地 Direct smoke

```powershell
$env:CASCADE_DIRECT_BOOTSTRAP_TOKEN = "<bootstrap-token>"
go run ./cmd/directsmoke `
  -package "<App 正式生成且已审批的 JSON 包>" `
  -insecure-dev-tls `
  -output "artifacts/direct-smoke/latest"
```

smoke 客户端会完成 lease、加密包上传、状态轮询、结果解密、artifact 分块下载、总 SHA-256/字节数校验、`.verified.sha256` marker 写入和 lease release。

仅使用 App 正式生成且未手工修改的 `browser-agent-outline-v1` 包；fixture、test waiver 和手工 JSON 不得作为正式联调结论。

## Server-only Gateway 重启演练

下面的两阶段命令只验证 Gateway 状态恢复和 Worker claim/release，不调用 App、不执行 `/run`、不启动 Chromium，也不能标记为 App→Server 联调成功：

```powershell
$env:CASCADE_DIRECT_BOOTSTRAP_TOKEN = "<bootstrap-token>"
$env:CASCADE_DIRECT_WORKER_TOKEN = "<worker-token>"

./directsmoke.exe `
  -package contracts/exchange/v1/client_execution_package.browser_agent_outline.json `
  -server-controlled-fixture `
  -prepare-recovery-state artifacts/direct-recovery/latest/client-state.json `
  -insecure-dev-tls
```

停止并重新启动同一个 Gateway、复用同一个 `CASCADE_DIRECT_STATE_PATH` 后执行：

```powershell
./directsmoke.exe `
  -verify-recovery-state artifacts/direct-recovery/latest/client-state.json
```

成功标准是输出 `app_formal_run=false chromium_started=false`，并且 job 经 Worker claim/release 后回到 `queued`。该演练只证明 Server 重启恢复，不证明真实 App 包或真实页面执行。
