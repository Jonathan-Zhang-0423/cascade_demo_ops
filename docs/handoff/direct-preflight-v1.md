# Direct API v1 正式 App 产包前置检查

本检查只验证本地 Direct/Gateway/Worker 运行条件和 Server 控制 fixture 的结构完整性，不启动 App 正式产包，不上传正式 App 包，也不产生 App/Server 联调通过结论。

## 运行

从仓库根目录：

```powershell
go run ./backend/cmd/directpreflight `
  -fixture contracts/exchange/v1/client_execution_package.browser_agent_outline.json `
  -worker video-worker/dist/index.js `
  -output artifacts/direct-preflight/latest/preflight.json
```

若从 `backend` 目录运行，给 fixture 和 worker 传仓库相对路径：

```powershell
go run ./cmd/directpreflight `
  -fixture ../contracts/exchange/v1/client_execution_package.browser_agent_outline.json `
  -worker ../video-worker/dist/index.js
```

## 检查内容

- Server-controlled fixture 可读取且运行时为 `browser-agent-outline-v1`；
- fixture 通过 Server 结构校验；
- video-worker entry 存在；
- Node、FFmpeg、FFprobe 可执行；
- Direct Gateway 所需五个环境变量均已配置；
- TLS 证书与私钥可解析且相互匹配、证书在有效期内，并覆盖配置的 Gateway 主机；
- 输出脱敏 preflight JSON，不写入 token 或密钥值。

## 重要边界

- `app_formal_run` 永远为 `false`；
- fixture 结果只能证明 Server 本地准备状态，不能证明 App → Server 正式联调；
- 本检查不会申请 lease、不会创建 job、不会启动 Chromium；
- 通过后仍需等 App 正式包准备好，才能执行正式联调。

## 本地 TLS 准备（可选）

如果本机没有 mkcert/OpenSSL，可以在不启动 Gateway、不调用 App 的前提下，生成只覆盖 `localhost`/`127.0.0.1`/`::1` 的开发自签名证书：

```powershell
go run ./backend/cmd/directcert `
  -cert artifacts/direct-local-tls/direct-localhost.crt `
  -key artifacts/direct-local-tls/direct-localhost.key
```

该命令使用“只新建”模式，目标已存在时拒绝覆盖；不会打印私钥内容。证书仅用于本地 Direct 联调，不用于公网或生产。
