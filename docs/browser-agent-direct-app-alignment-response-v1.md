# Browser Agent Direct API v1：App 侧对齐确认与实现口径

本文回复 `browser-agent-direct-app-alignment-confirmation-v1.md`，作为 App、Gateway、Worker 的共同实现口径。

1. 用户批准请求冻结 preview 的 project/run/bundle/safety/confidence digest；批准记录同时保存 `plan_json`、`stage_approval_plan`、`script_outline`、`browser_agent_contract`、`agent_prompt_policy`、`approval_markdown` 子 digest。App 写入批准记录、短期 credential expiry 和 Direct installation 绑定后，重新计算最终上传视图的统一 `approval_subject_digest` 与 package digest；Gateway 对最终包全部校验。除这些确定性传输字段外的变化必须重新预览和批准。
2. 批准字段固定为 `approval_id`、`approval_subject_digest_sha256`、`approved_at`、`approved_by_installation_id`、`approval_schema_version=cascade.user_approval.v1`，并保留本地用户审核身份。简单 `approved=true` 无效。
3. 产品 URL、源码/页面证据、需求、credential ref、安全范围、执行图、App/协议算法或任何批准子对象变化，均生成新 digest 并要求重新批准；旧 preview 返回 `package_preview_stale`。
4. installation ID 由 Ed25519 公钥确定；lease、package producer、approval installation 必须一致。重装/密钥轮换后的旧批准包不能直接上传，返回 `unverified_origin`。
5. 包上传后自动进入 `awaiting_credentials` 或 `queued`，不提供 App→Worker start。Worker 仅通过 `127.0.0.1:18444` claim。
6. 当前正式生产支持一次性 credential envelope，health capability 明确返回 `manual_login_checkpoint=false`；本地 dev-visible 流程仍可人工登录，但不能冒充正式 Direct 验收。
7. 每条 required 需求必须闭合到 workflow requirement、stage、action/input、required validation 和 Evidence；关键链路缺失 fail-closed。
8. selector 候选固定携带 `evidence_id`、`source_kind`、`source_digest`、`observed_role`、`observed_accessible_name`、`observed_at`，且 `evidence_id` 必须存在于候选 `evidence_refs`；缺失时 App 预检与 Gateway 同时返回 `selector_provenance_incomplete`。`data-testid` 不拼接进 accessible name。Server 只使用同 stage、同业务目标、同 Evidence 绑定候选。
9. 登录入口和表单分离；仅有普通 email input 不构成登录表单。营销 waitlist/newsletter 控件不能接收 credential。
10. `action_target` 与 `success_target` 分离。fill 可用 `value_equals` 验证输入结果；click/select/submit 必须验证动作后路由、状态、弹窗、日志或结果控件。
11. 动态路由使用 `/project/:id` 或 `{project_id}` 模板，Worker 仅在运行时匹配，不把真实 ID 写回批准包。
12. 长任务使用结构化 required validations、wait conditions 和最大时长；自然语言“等待完成”不能单独构成完成条件。
13. `DirectJobStatus` 返回协议、job/package、status/stage/progress，并增加 `waiting_reason`、`blocking_error_code`、`next_action`、`requires_reapproval`；`reunderstanding_required` 另带可选 `reunderstanding_issues[]`。这些稳定字段必须持久化、重载和前端映射；issue 只含脱敏结构和 opaque Evidence ID，UI 不展示 token、内部端口、Worker 堆栈、页面内容或原始 envelope。
14. 正式 completed 结果强制包含真实 StepResults、ValidationReports、请求中的 raw recording、Replay Manifest 和 StageEventLog；请求最终视频时同时强制 MP4、AssetTimelineCatalog 与 DemoEditPlan，请求 trace/截图时对应产物必填。关键产物必须带 ID、URI、SHA-256 与 size，缺失由 Gateway/App 最终交付门禁阻断。OutcomeVerifier 的渲染前检查可保留 warning，但不能把该 warning 当成正式交付成功。
15. required `outcome_observed` 缺 Evidence refs 为 blocking；附加 observation 缺 Evidence 为 warning。
16. App 下载全部 chunk、验证 SHA-256/size 并写 `.verified.sha256` 后，发送加密 ACK；ACK 完成后才允许显式释放 lease 和进入编辑器。失败结果不伪造 MP4。
17. package 上传按 installation+package+digest 幂等；Gateway 重启后普通 queued/running job 可重领，credential envelope 必须重传。artifact chunk 可按索引重取；canceled/expired 不恢复。
18. health 明确返回支持的 Direct 协议、包 schema、runtime、Worker 协议、OutcomeVerifier rules 和 capability flags；任一不兼容时 fail-closed，不回退 Legacy Exchange。Worker claim 也固定携带并校验 `cascade.browser_agent_worker.v1`。
19. `reunderstanding_required` 终止原 job。Gateway 只接受 `ValidationReports[].Decision` 作为权威来源并返回结构化问题；App 清空旧 preview digest 和批准缓存、递增 package generation，重新理解后生成新 package ID/digest，重新人工批准并创建新 job。重复状态轮询不得重复递增 generation。

## 稳定状态机

```text
package_received（接收瞬间）
→ awaiting_credentials | queued
→ running(stage=claimed/业务 stage)
→ validating
→ rendering
→ completed | failed | canceled | expired
→ completed + result_ack_receipt
→ lease_released
```

实际状态可跳过瞬态显示，但不能把 `awaiting_credentials` 显示成“已可执行”，也不能在 completed 缺少正式素材时允许 ACK。

## 本轮修复对应缺陷

- 首页营销邮箱不再被 runtime credential broker 当作登录表单；增加 `Try it now`、`Get started`、`Start building` 的受限登录入口识别。
- session setup 必须用登录后路由/状态验证，不再复用 email action target。
- mode/click/select/submit 默认验证成功状态，不再用被点击控件仍可见作为成功证明。
- Direct failure diagnostic、delivery refs 和 result ref 统一改写为 `direct://`，移除 `dev_local_artifact`、`local-dev/result-key` 和服务器本地路径。
- package producer、approval、lease、installation 及批准子 digest 统一强校验。
- 正式结果完整性、ACK、lease release 和 capability negotiation 形成同一协议闭环。
- `waiting_reason`、`blocking_error_code`、`next_action`、`requires_reapproval` 不再在 App 持久化或 Bridge 映射中丢失。
- `reunderstanding_issues` 已贯通 Gateway、App 持久化、Bridge 和 UI；该终态不会再被压扁成普通脚本失败，也不会复用旧 package ID/digest 或批准缓存。
- selector candidate provenance 已贯通页面扫描、Go DTO、执行包和 Gateway；无证据的泛化 alternatives 在打包前剥离。
- click/select/upload 复用动作目标作为成功验证由 warning 升为正式 blocker。
- App 用户界面只展示安全执行会话，不展示动态数据端口实现。
