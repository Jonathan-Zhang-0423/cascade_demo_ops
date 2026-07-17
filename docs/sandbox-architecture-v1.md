# Cascade 沙箱环境方案 v1

> [!IMPORTANT]
> **Legacy v1 / 安全实现参考。** 本文件中的隔离、凭据和审计约束仍可作为现有实现基线，但“APP 生成完整执行包、Server 失败后退回修复”的总流程已被 v2 架构替代。新链路设计以 [server-browser-agent-execution-editor-architecture-v2.md](./server-browser-agent-execution-editor-architecture-v2.md) 为准。

Cascade v1 采用多层沙箱，而不是把安全边界压在单一 Node 进程里。默认生产路线是：App 本地生成并审批三合一方案包，云端在一次性隔离容器里校验和录制，视频渲染阶段再进入无凭据沙箱。

## 分层边界

1. App 本地理解沙箱
   - 只读扫描客户项目根目录，不执行客户代码、不安装依赖、不读取 `.env*`。
   - 默认忽略 `.git`、`node_modules`、`dist/build/out`、secret/key/cert 文件和大二进制文件。
   - 输出仅包含结构摘要、selector 摘要、文件路径 hash、source tree digest 和 evidence refs。

2. Server Intake 沙箱
   - Intake API 只处理 `ExchangeEnvelope`、`payload_ref`、digest、policy 和 artifact descriptor。
   - 生产路径只接受 encrypted `payload_ref`，明文 payload 仅限 dev/test。
   - 解密只发生在 isolated worker 内存边界，DB 不保存明文 payload、TS 脚本或审批文档。

3. Script Validation 沙箱
   - 在浏览器启动前进行无网络、无凭据、无浏览器状态校验。
   - 校验入口函数、hash 绑定、node_id 绑定、禁用 API、allowed domains、forbidden pages、sandbox policy。
   - 校验失败直接生成 failed `RecordingResultPackage` 和 `ScriptFailureDiagnostic`，不进入浏览器执行。

4. Browser Execution 沙箱
   - 生产使用 per-job Linux container；高风险或企业版可切换 per-job microVM。
   - 容器要求 non-root、read-only rootfs、job-scoped writable tmp/workdir、CPU/memory/disk/time quota、无 host mount、无 Docker socket。
   - 网络默认 deny all，经 egress proxy 放行客户 `allowed_domains` 和 Cascade artifact/KMS/vault endpoint，并阻断 metadata/private ranges。
   - 浏览器使用 fresh context、无持久 profile、无 extension、trace `sources=false`、下载禁用或限制到 artifact dir。

5. Secret 沙箱
   - raw secret 不进 DB、日志、TS 源码、trace、错误响应。
   - 云端执行前从 vault 获取临时凭据，只通过 `ctx.secrets.get(secret_ref)` 注入。
   - 任务结束后清理临时凭据、浏览器 storage 和 usage audit。

6. Artifact 沙箱
   - raw recording、screenshot、trace、DOM/a11y summary、final video、step docs、result package 都写入 job-scoped workspace。
   - 敏感 artifact 默认 `encrypted=true`、`sensitive=true`、checksum required、短期过期、recipient 绑定 App installation key。
   - DB 只保存 descriptor，不保存 blob。

7. Render 沙箱
   - 渲染沙箱与浏览器执行沙箱分离。
   - Render worker 不接触 customer credentials、decrypted execution package、Playwright script 或 browser cookies/storage。
   - 只消费 raw recording、screenshots、trace summary、DemoEditPlan 和品牌样式元数据。

8. Failure Diagnostic 沙箱
   - 失败时只返回脱敏后的 failed node、错误码、当前 URL/title、截图/trace refs、console/network 摘要、DOM/a11y refs 和 repair hints。
   - 返回前必须 strip cookie、authorization、localStorage/sessionStorage token，不返回完整 HTML、raw secret 或客户源码。
   - 诊断材料加密给 App installation key，App 本地 agent 修复后必须重新人工审批。

## SandboxPolicy

`RecordingRunSpec.sandbox_policy` 或 resolved bundle policy 使用同一结构：

- `profile`: `dev`、`mvp_cloud`、`enterprise`
- `isolation_mode`: `local_sidecar`、`per_job_container`、`per_job_microvm`
- `network_policy`: mode、allowed domains、Cascade endpoints、denied CIDRs、proxy requirement
- `filesystem_policy`: read-only root、writable paths、no host mount、no Docker socket
- `resource_limits`: max runtime、memory、CPU、disk
- `browser_policy`: fresh context、disable extensions/downloads、trace sources disabled、allowed page methods/context APIs
- `secret_policy`: vault-only、context-only injection、no env injection、revoke/rotate after run
- `artifact_policy`: encrypt sensitive artifacts、sensitive by default、recipient key、checksum
- `diagnostic_policy`: redaction required、forbid full HTML、strip headers/storage keys、encrypt diagnostics

后端会为缺省包生成 `mvp_cloud/per_job_container/allowed_domains_only` policy，并把 `policy_hash_sha256` 写入执行请求、`ExecutionTrace.sandbox` 和 `CloudExecutionAuditTrail.sandbox`。

## Runtime Profiles

- `dev`: local Node sidecar、本地 artifact dir、无容器隔离，但仍启用脚本和 sandbox policy 校验。只用于联调和 demo。
- `mvp_cloud`: per-job Linux container、egress proxy allowlist、vault-backed secret injection、encrypted object storage artifact。推荐作为第一版生产方案。
- `enterprise`: per-job microVM、customer KMS wrapping key、更严格网络 allowlist、可选私有部署或 VPC peering、审计导出和 retention controls。

## 当前落地范围

- Go 协议模型已包含 `SandboxPolicy`、`SandboxExecutionMetadata` 和 package-level validation。
- Executor 会把 resolved sandbox policy 传给 video-worker，并在 result trace/audit 里返回 policy hash/profile/isolation/network mode。
- video-worker 的 `validate_script`/`execute_script` 会校验 sandbox policy；真实 Playwright 路径会按 allowed domains/forbidden pages 做运行时阻断。
- 生产级 container/microVM runner、egress proxy、artifact encryptor、vault integration 是云端 runner 后续实现项，不能用 dev sidecar 处理真实客户数据。

## 测试要求

- 禁止 API/global/import/raw secret/node mismatch/non-allowed domain 的脚本校验测试。
- allowed domain 可访问、forbidden page/unauthorized outbound 被阻断、timeout/resource limit 产生稳定错误码。
- 失败诊断不包含 cookie、authorization、localStorage、raw secret、完整 HTML。
- failure/result artifacts 必须 encrypted、sensitive、recipient-bound。
- happy path、script failure repair path、render failure path 都必须通过端到端 contract 测试。
