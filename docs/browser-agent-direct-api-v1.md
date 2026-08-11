# Browser Agent Direct API v1（App ↔ Ubuntu Gateway）

正式控制地址示例：`https://<approved-host>:18443`。数据地址由 Gateway 在 lease 响应中返回，端口范围为 `24000-24031`，每个 installation 独占一个监听端口。

## 认证与身份

控制口请求使用 App bootstrap token：

```http
Authorization: Bearer <bootstrap-token>
```

bootstrap token 只存 App 系统凭据库和 Gateway root-only env。lease 分配/释放另外要求 installation Ed25519 公钥、installation ID、时间戳、nonce 和签名；installation ID 必须由公钥确定性计算。

## 控制接口

### `GET /v1/direct/health`

需要 bootstrap token。返回协议版本、crypto suite、active lease 数、服务器时间，以及 `supported_protocol_versions`、`supported_package_schema_versions`、`supported_runtimes`、`supported_worker_protocol_versions`、`supported_outcome_verifier_rules_versions` 和脱敏 `capabilities`。App 必须显式确认当前 Direct v1、`demoops.client_execution_package.v1`、`browser-agent-outline-v1`、`cascade.browser_agent_worker.v1` 和 `browser-agent-outcome-verifier-rules-v1` 均受支持；任一缺失均返回 `protocol_mismatch`，不得静默回退 Legacy Exchange。响应不返回 token、路径或凭据。

### `POST /v1/direct/leases`

需要 bootstrap token。请求为 `DirectLeaseRequest`：

```json
{
  "protocol_version": "cascade.browser_agent_direct.v1",
  "installation_id": "direct_install_<hash-prefix>",
  "client_version": "<version>",
  "timestamp_unix_ms": 0,
  "request_nonce": "<one-time nonce>",
  "signing_public_key_base64": "<public key>",
  "signature_base64": "<ed25519 signature>"
}
```

成功返回 `201` 和 `DirectPortLease`：`lease_id`、`installation_id`、`data_url`、`data_port`、`lease_token`、`issued_at`、`expires_at`、`crypto_suite`、`server_time`。App 不应向用户展示 lease token。

### `POST /v1/direct/leases/release`

需要 bootstrap token，并使用同一 installation 签名。Gateway 发现该 installation 仍有非终态 job 时返回：

```json
{"error":{"code":"lease_has_active_jobs","message":"..."}}
```

成功返回 `released: true`。App 随后把项目 stage 固化为 `lease_released`，不再自动重新配对该项目。

## 数据接口与加密

所有数据口请求必须访问当前 lease 的 `data_url`，并带：

```http
X-Cascade-Timestamp: <unix ms>
X-Cascade-Nonce: <one-time nonce>
X-Cascade-Body-SHA256: <sha256(body)>
X-Cascade-Signature: <HMAC-SHA256>
```

请求签名覆盖：`METHOD`、escaped path、时间戳、nonce、body digest。包和结果消息为 `DirectEncryptedMessage`，其明文不会出现在 App↔Gateway HTTP body：

```json
{
  "protocol_version":"cascade.browser_agent_direct.v1",
  "lease_id":"...",
  "message_id":"...",
  "message_type":"...",
  "direction":"app_to_browser_agent|browser_agent_to_app",
  "timestamp_unix_ms":0,
  "nonce_base64":"...",
  "plaintext_digest_sha256":"...",
  "ciphertext_digest_sha256":"...",
  "ciphertext_base64":"...",
  "crypto_suite":"hkdf-sha256+aes-256-gcm"
}
```

密钥由 lease token、installation、lease、data port、消息时间戳通过 HKDF-SHA256 派生；AES-GCM AAD 绑定协议、lease、端口、消息 ID/type、方向、时间戳、nonce、明文摘要和 crypto suite。最大允许时钟偏差为 5 分钟。

### `POST /v1/direct/packages`

上传已人工批准且 digest 一致的 `ClientExecutionPackage`。仅接受 `browser-agent-outline-v1`。正式包必须同时满足：

- `producer_installation_id` 等于 lease installation；
- `human_approval.approved_by_installation_id` 等于同一 installation；
- `approval_schema_version=cascade.user_approval.v1`；
- 统一 `approval_subject_digest_sha256` 与 `plan_json`、`stage_approval_plan`、`script_outline`、`browser_agent_contract`、`agent_prompt_policy`、`approval_markdown` 子对象 digest 全部一致。
- `browser_agent_contract.outcome_verifier_rules_version=browser-agent-outcome-verifier-rules-v1`，且必须在 health 声明的支持集合中。

App 批准请求先绑定用户已查看的 preview package/approval/confidence digest。批准后仅允许写入批准记录、短期 credential expiry 和 Direct installation 来源绑定等确定性传输字段；每次形成新的上传视图都必须重新计算包内统一批准摘要和最终 package digest。语义字段或批准子对象发生变化时不得沿用该流程，必须返回 `package_preview_stale` 并重新批准。

任一来源或 digest 不一致返回 `unverified_origin` / `approval_digest_mismatch`。成功返回加密 `package_receipt`，包含 `job_id`、`package_id`、包 SHA-256、状态和 stage。含 credential grant 的包先进入 `awaiting_credentials`，凭据封套通过后进入 `queued`；不需要显式 start，Worker 只从 loopback claim。

### `POST /v1/direct/jobs/{job_id}/credentials`

上传一次性加密 `credential_envelope`。封套必须绑定 job、package、package digest、grant、secret ref、installation、lease、允许域名/操作和过期时间。Gateway 只在内存保存凭据。

### `GET /v1/direct/jobs/{job_id}`

返回加密 `job_status`。除 `status`、`stage`、`progress_percent` 外，稳定业务字段包括 `waiting_reason`、`blocking_error_code`、`next_action`、`requires_reapproval`。终态为 `completed`、`failed`、`canceled` 或 `expired`；结果包 ID 和 artifact descriptor 由状态返回。App 必须持久化并在重启后恢复这些字段，但 UI 不展示 Worker token、内部端口、堆栈或原始 envelope。

### `GET /v1/direct/jobs/{job_id}/result`

只在结果就绪后返回加密 `recording_result`。Server 必须校验结果与源包、trace、stage、业务验证和已上传 artifact 的 digest/size 一致。正式 completed 结果若请求了 MP4、trace 或截图，则缺一即返回 `result_artifact_completeness_failed`；`stage_event_log_ref` 始终必填。正式 failed 结果必须有脱敏 failure diagnostic，并尽可能包含截图/trace；只有明确的基础设施启动失败允许没有页面素材。

### `GET /v1/direct/jobs/{job_id}/artifacts/{artifact_id}/chunks/{chunk_index}`

每块独立返回加密 `artifact_chunk:<artifact_id>:<index>`，单块上限 4 MiB。App 下载所有块后校验总 SHA-256 和字节数，写入 App-managed artifact root 和 `.verified.sha256` marker；未通过不得 ACK、审核或编辑器交接。

### `POST /v1/direct/jobs/{job_id}/ack`

App 仅在全部 artifact 已下载并通过 SHA-256/字节数校验后发送加密 `result_ack`。请求绑定 installation、job、result package、全部 artifact ID、`verified_checksums=true` 和 ACK 时间。Gateway 返回 `result_ack_receipt`；正式 completed 结果未 ACK 时禁止释放 lease。App 随后显式调用 lease release，编辑器仍使用本地 verified 素材，不依赖 Gateway 路径。

## Worker 内部接口

Worker API 只监听 `127.0.0.1:18444`，使用独立 Worker token，公网不可达。Worker claim 响应固定携带 `protocol_version=cascade.browser_agent_worker.v1`；Worker 必须先校验版本再读取包或执行。Worker claim 已批准包，consume 一次性凭据，提交 1-99% 状态、真实 artifact bytes 和最终结果。Worker 不得修改用户意图、stage 顺序、allowed domains、forbidden pages、secret ref 或 destructive 标记。

## Selector 与执行门禁

`script_outline` 是证据绑定路线图，不是可任意改写的脚本。任何正式 selector candidate 必须同时包含：

```json
{
  "kind": "testid|role|css",
  "value": "...",
  "evidence_id": "ev_...",
  "source_kind": "source_scan|page_scan|approved_manual_annotation",
  "source_digest": "sha256:...",
  "observed_role": "button",
  "observed_accessible_name": "New Project",
  "observed_at": "2026-08-11T00:00:00Z",
  "evidence_refs": [{"id":"ev_...","kind":"browser_scan"}]
}
```

`evidence_id` 必须存在于候选自己的 `evidence_refs`；缺少任一字段返回 `selector_provenance_incomplete`。`data-testid` 只能进入 `test_id/selector`，不得拼入 `observed_accessible_name` 或业务 `allowed_names`。只有语义意图、没有页面/源码证据时可保留 target contract 和运行时自适应主目标，但不得生成伪造的 selector alternatives。

selector 只能来自 App 批准的同一 Evidence ID/业务目标候选；Server 可在运行时进行唯一性、可见性、角色/名称兼容检查和有限 repair。登录入口与登录表单必须分开：普通营销页 waitlist/newsletter 邮箱框不能作为登录表单；credential broker 必须先确认密码框或认证路由/标题/认证方式语义，再允许填入账号。`action_target` 与 `success_target` 分开校验，click/select/submit 不得以动作控件仍可见作为成功证据；该缺陷按 blocker 处理。找不到或语义冲突时必须停止并返回稳定脱敏错误，不能猜测 selector。
