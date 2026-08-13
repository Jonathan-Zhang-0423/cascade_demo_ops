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

需要 bootstrap token。返回仅含协议版本、crypto suite、active lease 数和服务器时间，不返回 token、路径或凭据。

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

上传已人工批准且 digest 一致的 `ClientExecutionPackage`。仅接受 `browser-agent-outline-v1`。成功返回加密 `package_receipt`，包含 `job_id`、`package_id`、包 SHA-256、状态和 stage。

上传前 Server fail-closed 校验：

- `lease.installation_id == package.producer_installation_id == human_approval.approved_by_installation_id`，否则返回 `unverified_origin`；
- `approval_schema_version` 必须为 `cascade.user_approval.v1`；
- `subject_digests_sha256` 必须完整绑定 `plan_json`、`stage_approval_plan`、`script_outline`、`browser_agent_contract`、`agent_prompt_policy`、`approval_markdown`；
- 子摘要或统一 `approval_subject_digest_sha256` 漂移返回 `approval_digest_mismatch`；
- 每个 `selector_alternatives`/`dom_hints` candidate 必须携带 evidence/source/observed 字段并绑定 `evidence_refs`，否则返回 `selector_provenance_incomplete`。

### `POST /v1/direct/jobs/{job_id}/credentials`

上传一次性加密 `credential_envelope`。封套必须绑定 job、package、package digest、grant、secret ref、installation、lease、允许域名/操作和过期时间。Gateway 只在内存保存凭据。

### `GET /v1/direct/jobs/{job_id}`

返回加密 `job_status`。终态为 `completed`、`failed`、`canceled` 或 `expired`；结果包 ID 和 artifact descriptor 由状态返回。

### `GET /v1/direct/jobs/{job_id}/result`

只在结果就绪后返回加密 `recording_result`。Server 必须校验结果与源包、trace、stage、业务验证和已上传 artifact 的 digest/size 一致。

Worker 提交结果时，Gateway 按身份、字节、完整性、结构化内容四层 fail-closed 校验，并使用稳定错误码：摘要/size 与上传字节不一致返回 `result_artifact_binding_invalid`；正式产物或必填 descriptor 缺失返回 `result_artifact_completeness_failed`；result/package/job、ValidationReport、Replay Manifest 或 StageEventLog 绑定错误返回 `result_artifact_content_invalid`；出现 `file://`、绝对本地路径或 dev marker 返回 `result_contains_local_runtime_data`。

### `GET /v1/direct/jobs/{job_id}/artifacts/{artifact_id}/chunks/{chunk_index}`

每块独立返回加密 `artifact_chunk:<artifact_id>:<index>`，单块上限 4 MiB。App 下载所有块后校验总 SHA-256 和字节数，写入 App-managed artifact root 和 `.verified.sha256` marker；未通过不得 ACK、审核或编辑器交接。

## Worker 内部接口

Worker API 只监听 `127.0.0.1:18444`，使用独立 Worker token，公网不可达。Worker claim 已批准包，consume 一次性凭据，提交 1-99% 状态、真实 artifact bytes 和最终结果。Worker 不得修改用户意图、stage 顺序、allowed domains、forbidden pages、secret ref 或 destructive 标记。

## Selector 与执行门禁

`script_outline` 是证据绑定路线图，不是可任意改写的脚本。selector 只能来自 App 批准的同一 Evidence ID/业务目标候选；Server 可在运行时进行唯一性、可见性、角色/名称兼容检查和有限 repair。找不到或语义冲突时必须停止并返回 `selector_resolution_failed`、`semantic_target_contract_conflict` 或 `missing_product_evidence`，不能猜测 selector。
