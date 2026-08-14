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

### 7. 正式结果产物和 Replay Manifest

- Outline Router 在渲染完成并绑定 StageEventLog 后生成 `demoops.replay_manifest.v1`，以 `kind=replay_manifest`、真实 SHA-256/size 和 delivery descriptor 纳入 Direct Worker 上传集合。
- Replay Manifest 与同一 job/package、StepResults、ValidationReports、StageEventLog 和最终渲染素材绑定；本地可见执行复用同一 artifact 封装逻辑。
- 正式 completed 交付门禁新增 raw recording、Replay Manifest、AssetTimelineCatalog、DemoEditPlan、StepResults 和 ValidationReports；关键产物缺 ID/URI/checksum/size 直接阻断。
- 修正 Windows 本地文件 URI：生成端统一使用 `file:///C:/...`，Direct Worker 解析端正确恢复盘符路径，避免 Replay Manifest 和 StageEventLog 在 Windows 下不可读。

### 8. 结构化产物内容校验与显式 ACK

- Direct Worker 上传前将 Replay Manifest 内部的 MP4、raw recording、trace、StageEventLog 和 manifest 自引用统一改写为当前 job 的 `direct://jobs/{job_id}/artifacts/{artifact_id}`，重新计算最终 SHA-256/size，正式包不再携带 Server 本地路径草稿。
- Gateway 在摘要/字节数校验后继续解析 Replay Manifest JSON 和 StageEventLog JSONL，复核 schema、run/package/bundle/policy、stage/node、事件序列、真实 outcome evidence、StepResults 与 ValidationReports 索引；错误统一返回 `result_artifact_content_invalid`。
- App 新增独立 ACK 服务并由 HTTP/Wails 共用；Web 在全部下载与 marker 校验后立即 ACK，持久化 `acked_at`。未 ACK 时人工审核、lease release 和编辑器交接均 fail-closed。
- 修复 UI 误导：仅下载完成不再显示“服务器 ACK 已发送”；ACK 失败时保留已验证下载，可显式重试 ACK。

### 9. Selector 主目标与跨层防御

- 删除页面扫描失败时对“新建项目”“项目名称”“启动构建”等控件生成的猜测 selector；没有正式页面/源码证据时只保留受限语义目标。
- runtime-adaptive 动作只有在 `non_destructive=true`、目标合同、路由、安全范围、成功条件和采集计划完整时才能进入执行图。
- PlanJSON、StageApprovalPlan 和 ScriptOutline 的主 selector 必须一致；漂移返回 `selector_binding_mismatch`。
- runtime-adaptive 主 selector 必须能精确绑定完整 provenance candidate；否则返回 `selector_primary_provenance_missing`。
- PreExecutionValidator 再次拒绝没有 formal candidate 的主 selector；repair 池只保留完整 provenance 候选，不再从 component 主 selector 和共享 EvidenceRef 合成伪候选。
- 项目名称由通用需求结构提取，不再硬编码只识别“俄罗斯方块”；纯数字项目名（例如 `2048`）与带单位的时长（例如 `13s`）分别处理，fixture 中的固定名称只用于测试，不进入生产推断。
- `VerifiedInteractionAction.non_destructive` 已贯通 BusinessStage、Graph metadata、Plan step、StageApprovalPlan 和 ScriptOutline，任一层漂移继续 fail-closed。

### 10. Runtime 等待去冗余

- Browser Agent Runtime 默认导航改为 `domcontentloaded`，随后仅保留 250ms 短稳定窗口。
- 不再在 session 初始化、普通点击/填写/选择、登录辅助跳转和 capture 前无条件等待 `networkidle`。
- 只有批准的 `wait_until=networkidle`、`wait_conditions=networkidle` 或 `wait_for_network_idle` 才执行网络空闲等待。
- 新增测试证明 `wait_for_network_or_dom_stable`、render-stable 和持续请求页面不会被误判为必须等待网络空闲，同时保留显式 network-idle 请求。

## 回归测试

- 后端：`go test ./... -count=1` 通过。
- Video Worker：39 项通过，2 项按既有条件跳过；typecheck 与 build 通过。
- Web：108 项通过；typecheck 与生产 build 通过。
- 新增覆盖：缺 Worker/Verifier 版本、Worker claim 协议不匹配、稳定状态持久化、selector provenance、主 selector 跨层漂移、无 provenance 主 selector、`data-testid` 不进入 accessible name、动作/成功目标复用 blocker、通用/纯数字项目名、按批准条件等待 networkidle、`reunderstanding_required` 生命周期、正式 Replay Manifest/StageEventLog 内容错绑拒绝、显式 ACK 顺序、编辑器 ACK 门禁和 Windows file URI 往返。

## 接口交接结论

Browser Agent 侧可以依赖以下稳定约束：

1. App 上传包的 `browser_agent_contract.outcome_verifier_rules_version` 已冻结并进入批准摘要。
2. Worker claim 在业务包外层携带独立 Worker 协议版本。
3. selector alternatives 要么来源完整，要么不出现在正式包；Server 不应补造候选。
4. `waiting_reason`、`blocking_error_code`、`next_action`、`requires_reapproval` 可作为稳定 UI/恢复字段。
5. `post_action_validation_reuses_action_target` 和同类 Evidence 复用错误必须阻止正式执行。
6. `reunderstanding_required` 以结构化 issues 终止旧 job；App 必须生成新 package ID/digest 并重新审批，Server 不改写 App 计划。

## 尚未声称完成

本轮完成的是本地代码、协议与自动化门禁收口，不等同于真实 `cascadeai.cn` 线上 Browser Agent 业务验收。线上仍需使用正式 installation、有效服务器部署和真实 Chromium 结果完成上传、执行、StepResults/ValidationReports、raw recording、MP4/trace/截图/StageEventLog、Replay Manifest、编辑器清单、分块下载、checksum、ACK 与编辑器交接闭环；不得以 fixture 替代。
