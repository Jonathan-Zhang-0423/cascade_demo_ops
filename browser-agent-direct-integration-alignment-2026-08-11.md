# Browser Agent Direct Integration 协议对齐说明（2026-08-11）

## 1. 对齐基准与边界

- 基准协议：`browser-agent-direct-integration-protocol-2026-08-11.md`
- Direct wire protocol：`cascade.browser_agent_direct.v1`
- Worker protocol：`cascade.browser_agent_worker.v1`
- OutcomeVerifier rules：`browser-agent-outcome-verifier-rules-v1`
- 本轮只修改 Server、Gateway、Server-owned Worker/video-worker 相关实现。
- 未修改 App 代码，未修改 Validation Agent/OutcomeVerifier 的判定规则。
- fixture、mock、test waiver 仍只作为准备度证据；本轮没有标记正式 App→Server 端到端验收成功。

## 2. 本轮已完成的协议对齐

| 协议要求 | 当前状态 | Server 实现位置 | 验证情况 |
|---|---|---|---|
| health 同时声明 Direct、package schema、runtime、Worker 和 verifier 版本 | 已对齐 | `backend/internal/app/direct_http.go` | Direct/App 测试通过 |
| package message type 固定为 `client_execution_package` | 已对齐 | `backend/internal/app/direct_http.go`、`backend/cmd/directsmoke/main.go` | fixture intake 测试通过 |
| 有 credential grant 的 job 先进入 `awaiting_credentials` | 已对齐 | `backend/internal/direct/gateway.go` | 生命周期测试通过 |
| credential envelope 上传后才进入 `queued` | 已对齐 | `backend/internal/direct/gateway.go` | 生命周期测试通过 |
| credential 为一次性、只供 running job 消费 | 已对齐 | `backend/internal/direct/gateway.go`、`browser_agent_credential_broker.go` | 正反向测试通过 |
| credential job 释放 claim 后回到 `awaiting_credentials` 且旧凭据失效 | 已对齐 | `backend/internal/direct/gateway.go` | 新增测试通过 |
| Worker claim 必须协商 `cascade.browser_agent_worker.v1` | 已对齐 | `backend/internal/app/direct_http.go`、`backend/cmd/directworker/main.go` | mismatch/成功 claim 测试通过 |
| Worker 进度仅允许 running job 的 1–99% | 已对齐 | `backend/internal/direct/gateway.go` | queued/0/100/正常进度测试通过 |
| artifact 只能由 running job 上传，path/body identity 必须一致 | 已对齐 | `backend/internal/direct/gateway.go`、`backend/internal/app/direct_http.go` | 正反向测试通过 |
| Worker 先上传全部真实 artifact bytes，再提交结果包 | 已对齐到 Server-owned runner | `backend/internal/app/direct_worker_runner.go`、`direct_result_finalize.go` | 编译及 App 回归通过；正式 App 包尚未验收 |
| 正式结果 URI 使用当前 job 的 `direct://` URI，禁止 `file://` | 已对齐到 Server-owned runner 和结果门禁 | `backend/internal/app/direct_result_finalize.go`、`direct_http.go` | 本地 URI 拒绝测试通过 |
| 正式结果包含 Replay Manifest | 已对齐到 Server-owned runner | `backend/internal/app/direct_result_finalize.go` | 生成与 URI 绑定代码回归通过 |
| Replay Manifest policy hash 绑定 Browser Agent Contract hash，而不是 plan hash | 已对齐 | `backend/internal/app/browser_agent_replay_manifest.go` | 专项测试通过 |
| Replay Manifest 记录 protocol/execution runtime、stage ID/order、验证决策 | 已对齐 | `backend/internal/app/browser_agent_replay_manifest.go` | 专项测试通过 |
| Gateway 解析 Replay Manifest 与 StageEventLog 上传字节 | 已对齐核心门禁 | `backend/internal/app/direct_result_content_validation.go` | policy drift、event sequence 等负向测试通过 |
| event ID/sequence、stage/node、outcome evidence、validation refs、raw/video/trace manifest 索引 | 已对齐核心门禁 | `backend/internal/app/direct_result_content_validation.go` | Direct 专项测试通过 |
| App 下载并校验全部 artifact 后显式 ACK | Gateway 接收侧已对齐 | `backend/internal/app/direct_http.go`、`backend/internal/direct/gateway.go` | ACK 生命周期测试通过 |
| ACK 使用 RFC3339 `acked_at`，receipt 返回 Server 权威时间 | 已对齐 | `backend/internal/direct/protocol.go`、`gateway.go` | ACK 幂等测试通过 |
| ACK artifact 集合、job、result、installation 必须完整绑定 | 已对齐 | `backend/internal/direct/gateway.go` | ACK 正反向测试通过 |
| completed 结果未 ACK 时禁止 release lease | 已对齐 | `backend/internal/direct/gateway.go` | 生命周期测试覆盖 |
| whole-artifact 下载入口固定返回 `artifact_chunking_required` | 已对齐 | `backend/internal/app/direct_http.go` | Direct 路由回归通过 |
| Browser Agent/渲染/基础设施失败形成权威 failed result，而不是只有失败状态 | 已对齐 | `backend/internal/app/direct_failed_result.go`、`direct_worker_runner.go`、`gateway.go` | 失败结果专项及全量 Go 回归通过 |
| failed result 包含脱敏 diagnostic、approval-gated repair request、Replay Manifest 和可用证据 | 已对齐 | `backend/internal/app/direct_failed_result.go`、`direct_result_content_validation.go` | 缺 Trace、Manifest/事件绑定负向测试通过 |

## 3. 仍未完全对齐的协议项

以下项目不能被表述为已经完成，正式 App→Server 联调前仍需处理。

### 3.1 跨端 schema/来源绑定：Server 门禁已落地，等待 App 正式包验证

最新协议要求：

```text
lease.installation_id
== package.producer_installation_id
== human_approval.approved_by_installation_id
```

并要求 approval 中存在：

- `approval_schema_version`
- `subject_digests_sha256`
- `approved_by_installation_id`
- package 的 `producer_installation_id`

最新协议已经冻结上述字段。本轮 Server DTO 已加入 `producer_installation_id`、`approved_by_installation_id`、`approval_schema_version` 和六项 `subject_digests_sha256`，Direct intake 强制校验 lease/producer/approval installation 三方一致，并重新计算所有批准子摘要及统一 approval subject digest。来源不一致返回 `unverified_origin`，schema、子摘要或统一摘要漂移返回 `approval_digest_mismatch`。Server 不会自动补写或修改 App 包；剩余工作是使用 App 正式产出的包验证这些字段确实存在且计算口径一致。

### 3.2 Selector provenance：Server DTO 与 fail-closed 门禁已完成

最新协议要求每个正式 selector candidate 包含：

- `evidence_id`
- `source_kind`
- `source_digest`
- `observed_role`
- `observed_accessible_name`
- `observed_at`
- `evidence_refs`

`SelectorCandidate` 已加入协议字段。Direct intake 会遍历包内所有 `selector_alternatives` 和 `dom_hints`，要求 kind 为 `testid|role|css`，并校验 Evidence ID、来源类型、64 位小写 SHA-256、观测 role/name/time 以及 Evidence ref 反向绑定；任何缺失返回 `selector_provenance_incomplete`。该门禁只校验 App 正式包，不会在 Server 运行时伪造 selector 来源。

### 3.3 Gateway 内容门禁仍需补全错误分层与 failed 结果覆盖

本轮已经实现 Gateway 对 Replay Manifest JSON 和 StageEventLog JSONL 的真实上传字节解析，并校验：

- package/bundle/Browser Agent Contract policy/runtime 绑定；
- event ID 唯一和 sequence 严格递增；
- stage/node 必须属于批准计划；
- 每个完成 stage 必须同时有 `outcome_observed` evidence 和 `stage_completed`；
- manifest stages 与 validation refs 的完整索引；
- requested raw/video/trace 的 manifest 引用；
- URI 只能绑定当前 job，禁止本地运行时路径。

剩余工作：把早期身份、SHA/size 和通用完整性检查的错误也全部归并到协议规定的四类稳定错误码，并把同等内容门禁扩展到正式 failed 结果包。当前成功结果的核心内容门禁已经具备，failed 结果仍未完整覆盖。

### 3.4 package upload 幂等与 Gateway 重启恢复核心能力已完成

协议要求 installation + package ID + final package digest 作为幂等键，并要求普通 queued/running job 在 Gateway 重启后可恢复。本轮已经完成上传幂等门禁：

- 相同幂等键且相同 payload 返回原 job，不再重复创建任务；
- 相同幂等键但 transport digest、原始 payload 或 credential requirement 漂移时返回 `package_idempotency_conflict`；
- 并发重试由 Gateway 原子串行化，自动化测试确认只创建一个 job；
- Gateway 使用原子快照保存 lease、普通 job、结果、ACK 和 artifact，并校验恢复数据中的 installation/lease、package digest、artifact SHA/size/chunk 及 ACK 绑定；
- 普通 running job 重启后清理旧进程的不完整运行中 artifact，恢复为 `queued/gateway_recovered` 并可由 Worker 重新 claim；
- 需要凭据的 queued/running job 重启后统一恢复为 `awaiting_credentials`，credential envelope 从不写盘，必须重新上传；
- 已完成结果、artifact 和 ACK 可在重启后继续读取，过期 lease 可由同一 installation 续租并重新绑定可恢复 job；
- 损坏、篡改或版本不兼容的快照会以 `direct_gateway_persistence_failed` 拒绝启动，不会静默忽略。
- 2026-08-12 已完成真实独立进程演练：上传 Server-controlled fixture 后强制停止 Gateway，使用同一 snapshot 重启，原 lease/job 可继续查询，Worker claim/release 成功，job 回到 `queued`；`app_formal_run=false` 且未调用 `/run`、未启动 Chromium。

### 3.5 正式 failed 结果包已完成核心对齐

本轮继续开发后，Direct runner 已区分两类失败：

- 已进入真实浏览器执行的失败：保留真实截图、Trace、StageEventLog，移除不完整最终视频，生成脱敏 diagnostic、repair request 和 Replay Manifest；
- Worker/Node/Browser 启动等基础设施失败：生成明确的基础设施失败 diagnostic 和 stage_failed 审计事件，不伪造截图或浏览器观察。

Gateway 会保存可被 App 获取的权威 failed `RecordingResultPackage`，job 进入 `failed_result_ready`，不再只有一个不可追溯的失败状态。剩余工作主要是 App 对 failed result 的下载、展示和重新批准交互验收。

### 3.6 App 本地统一接口与 ACK 后 UI 门禁不在本轮修改范围

最新协议规定 HTTP Bridge 与 Wails 必须调用同一 App Domain Service，并由持久化 `acked_at` 派生审核、编辑器交接和 lease release 门禁。由于本轮明确禁止修改 App，Server 仅完成 Direct ACK 接收和 release 阻断；App 侧下载、verified marker、ACK retry、review/editor 门禁需要 App 负责方按协议完成。

## 4. 本轮执行的验证命令

```powershell
cd D:\Engine-7-8\backend
$env:GOCACHE='D:\Engine-7-8\.go-cache'
$env:GOTMPDIR='D:\Engine-7-8\.gotmp'

go test ./cmd/directcert `
  ./cmd/directpreflight `
  ./cmd/directgateway `
  ./cmd/directworker `
  ./cmd/directsmoke `
  ./internal/app `
  ./internal/direct `
  ./internal/driver `
  ./internal/model `
  ./internal/orchestrator `
  ./internal/executor `
  -count=1
```

结果：全部通过。

补充接口层契约证据：`TestDirectPackageHTTPRejectsFormalContractViolationsWithStableCodes` 已通过加密的 `/v1/direct/packages` 实际入口验证以下 fail-closed 返回，不仅是直接调用 validator：

- lease / producer / approval installation 不一致：HTTP 400 + `unverified_origin`；
- 六项批准子摘要发生漂移：HTTP 400 + `approval_digest_mismatch`；
- selector 缺少可追溯 evidence：HTTP 400 + `selector_provenance_incomplete`。

该测试使用 Server-controlled fixture，`app_formal_run=false`，不代表 App 正式包已经通过。

结果提交端点契约证据：`TestDirectWorkerResultHTTPReturnsProtocolErrorCodes` 已通过 loopback Worker 的 `/v1/worker/jobs/{job_id}/result` 实际入口确认身份漂移、descriptor 缺失和上传字节摘要漂移分别返回 `result_artifact_content_invalid`、`result_artifact_completeness_failed` 和 `result_artifact_binding_invalid`。本地路径/dev marker 的 `result_contains_local_runtime_data` 也已有专项测试。

```powershell
cd D:\Engine-7-8
corepack pnpm --filter @cascade/video-worker typecheck
corepack pnpm --filter @cascade/video-worker build
corepack pnpm --filter @cascade/video-worker test
```

结果：typecheck/build 通过；23 tests passed，2 tests skipped。

## 5. 验收口径

- 当前可以确认：Server 的 Direct v1 接入准备度明显提升，Worker claim、凭据生命周期、artifact 上传顺序、Direct URI、Replay Manifest 和 ACK 生命周期已有代码与自动化测试支撑。
- 当前不能确认：正式 App→TLS Gateway→Worker→真实 Chromium→Gateway 内容门禁→App 下载/ACK→人工审核/编辑器交接完整通过。
- Server 来源字段和 selector provenance 门禁已有代码及正反向测试；在 App 正式包实际通过这些门禁、并完成跨端正式结果承接前，仍不应发布“协议全面验收通过”结论。

## 6. 下一步最小开发顺序

1. Direct 结果门禁早期身份、SHA/size、完整性和本地运行时数据的四类稳定错误码映射及 Worker 提交端点负向契约覆盖已完成；下一步继续覆盖正式 failed result 的同类端点行为。
2. 让 App 按最新协议生成一份未手工修改的正式包，验证来源绑定、六项批准子摘要和 selector provenance 计算口径。
3. 等待 App 确认 installation/approval/schema 与 selector provenance 字段后，使用 App 原始包执行跨端 contract 验证；Server 来源绑定 validator 与 Direct HTTP 负向 contract tests 已完成。
4. 验证 App 对 failed result 的下载、问题展示、重新理解和重新批准流程。
5. 所有 preflight 和 contract tests 通过后，再申请一次正式 App 产包与真实 Chromium 端到端验收授权。
