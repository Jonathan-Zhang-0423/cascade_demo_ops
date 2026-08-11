# Browser Agent Direct App 对齐工作日志（2026-08-11）

## 本轮目标

根据 Browser Agent 侧确认文档，收口 App、Gateway、Worker 和 Web UI 的 Direct v1 接口分叉，重点处理状态丢失、版本未协商、selector 来源不完整和动作结果验证过宽四类缺陷。

## 已修复

### 1. 版本协商

- health 新增 `supported_worker_protocol_versions` 与 `supported_outcome_verifier_rules_versions`。
- App 只有在 Direct 协议、包 schema、runtime、Worker 协议和 OutcomeVerifier rules 全部匹配时才标记 reachable；缺一返回 `protocol_mismatch`。
- `BrowserAgentContract` 固定写入 `outcome_verifier_rules_version`，Gateway 上传时再次校验。
- Gateway→Worker claim 固定携带 `protocol_version=cascade.browser_agent_worker.v1`，Worker 在读取包和执行前拒绝不匹配版本。

### 2. 稳定状态字段

- `waiting_reason`、`blocking_error_code`、`next_action`、`requires_reapproval` 已贯通 Gateway DTO、App 项目持久化、重载恢复、HTTP/Wails Bridge 和 Web UI。
- UI 分开显示等待原因、阻断代码、下一步和是否需要重新批准。
- 用户界面不再展示动态数据端口；统一表述为短期安全执行会话。技术端口仍只存在于内部协议和运维配置。

### 3. selector provenance

- `SelectorCandidate` 新增可选兼容字段：`evidence_id`、`source_kind`、`source_digest`、`observed_role`、`observed_accessible_name`、`observed_at`。
- 页面扫描只返回脱敏来源摘要，不返回 DOM、输入值或凭据；`source_digest` 对受限页面证据摘要做 SHA-256。
- `data-testid` 仅用于 selector/test ID，不进入可访问名称。
- 页面扫描和源码扫描候选携带 Evidence 绑定；没有完整来源的 alternatives 在正式打包前剥离。
- App readiness 与共享 model/Gateway 校验都对残留的不完整候选返回 `selector_provenance_incomplete`。

### 4. 动作结果门禁

- click/select/upload 后继续验证原动作控件可见，不再只是 warning，而是正式 blocker。
- 共享正式 fixture 改为点击 `invite-member` 后验证 `invite-dialog`，并重算 plan、outline、contract 和 bundle 摘要。

### 5. 结果与状态显示

- 保留既有正式 completed 素材完整性门禁、ACK 后释放 lease、installation/producer/approval 绑定和幂等批准。
- App 仅展示稳定业务状态，不展示 Worker token、内部端口、堆栈、原始 envelope 或凭据。

### 6. `reunderstanding_required` 生命周期

- Gateway 仅依据 `ValidationReports[].Decision` 识别该终态，不从 message 或失败文本推断。
- `DirectJobStatus` 新增兼容字段 `reunderstanding_issues`，只返回 code、stage/node、severity、责任域、脱敏建议和 opaque Evidence ID。
- 稳定状态固定为 `status=failed`、`blocking_error_code=reunderstanding_required`、`next_action=regenerate_package_from_structured_issues`、`requires_reapproval=true`。
- App 持久化并重载结构化问题，退回执行包审批节点，清除旧批准缓存和旧 preview digest。
- 项目状态新增可选 package generation；首次收到该终态时递增一次，使重新理解后的 package ID 与 digest 均不同，重复轮询不会重复递增。
- Web 将该状态路由到重新理解/审批入口，不再误归类为普通 selector/script repair。

## 回归测试

- 后端：`go test ./... -count=1` 通过。
- Video Worker：36 项通过，2 项按既有条件跳过；typecheck 与 build 通过。
- Web：108 项通过；typecheck 与生产 build 通过。
- 新增覆盖：缺 Worker/Verifier 版本、Worker claim 协议不匹配、稳定状态持久化、selector provenance、`data-testid` 不进入 accessible name、动作/成功目标复用 blocker，以及 `reunderstanding_required` 的结构化状态、审批失效和 package identity 轮换。

## 接口交接结论

Browser Agent 侧可以依赖以下稳定约束：

1. App 上传包的 `browser_agent_contract.outcome_verifier_rules_version` 已冻结并进入批准摘要。
2. Worker claim 在业务包外层携带独立 Worker 协议版本。
3. selector alternatives 要么来源完整，要么不出现在正式包；Server 不应补造候选。
4. `waiting_reason`、`blocking_error_code`、`next_action`、`requires_reapproval` 可作为稳定 UI/恢复字段。
5. `post_action_validation_reuses_action_target` 和同类 Evidence 复用错误必须阻止正式执行。
6. `reunderstanding_required` 以结构化 issues 终止旧 job；App 必须生成新 package ID/digest 并重新审批，Server 不改写 App 计划。

## 尚未声称完成

本轮完成的是本地代码、协议与自动化门禁收口，不等同于真实 `cascadeai.cn` 线上 Browser Agent 业务验收。线上仍需使用正式 installation、有效服务器部署和真实 Chromium 结果完成上传、执行、MP4/trace/截图/StageEventLog、分块下载、checksum、ACK 与编辑器交接闭环；不得以 fixture 替代。
