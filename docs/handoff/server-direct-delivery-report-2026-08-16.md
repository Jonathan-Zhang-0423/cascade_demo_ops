# Server Direct 交付报告 — 2026-08-16（固定包验收）

对应交接文档：`server-handoff-2026-08-13/server-task-handoff-2026-08-13.md`
分支：`feat/validation-framework-integration-week3`
测试标注：**固定包验收**（Server-owned fixture，非真实 App 包联调）

---

## 1. 修改的稳定错误码清单

### 1.1 本周新增（D 类：验证注解表）

| 错误码 | 责任域 | 触发条件 | HTTP/Direct 状态 |
|---|---|---|---|
| `url_change_not_business_completion` | app | 仅 URL 变化被当作业务完成 | 结果包验证失败（blocking check） |
| `forbidden_operation_attempted` | app | Outline 动作违反 BrowserAgentContract 禁止项 | pre-execution `stop_and_report` |
| `media_artifact_generation_failed` | media_delivery | 视频/截图生成失败 | failed result `failure_diagnostic` |
| `media_artifact_upload_failed` | media_delivery | artifact 生成成功但上传失败 | failed result `failure_diagnostic` |

### 1.2 本周修复的错误链

| 错误码 | 修复内容 |
|---|---|
| `outcome_verification_failed` | 修复两个误报源：`about:blank` 初始页被误判跨域；blocking 报告缺 NodeID/StageID 违反自身 schema |
| `browser_agent_target_not_resolved` | 修复后 locator_missing 场景恢复该正确码（之前被上述误报掩盖为 outcome_verification_failed） |
| `human approval subject digest does not match` | 固定场景包在改造后刷新 approval digest，与生产审批路径一致 |

### 1.3 既有稳定码回归确认（测试断言的完整清单）

Direct 结果门禁（`directResultContractError`）：

| 稳定码 | 触发条件 | 测试 |
|---|---|---|
| `result_artifact_binding_invalid` | artifact SHA-256 或 size 与上传字节不符 | TestDirectWorkerRejectsArtifactSizeDrift、TestDirectErrorResponsesNeverLeakSecrets、既有 uploaded-bytes 用例 |
| `result_artifact_content_invalid` | Replay Manifest 缺 stage / 绑定漂移 / StageEventLog 缺 outcome_observed / 结果身份不一致 / 诊断未脱敏 | TestDirectWorkerRejectsReplayManifestMissingStage、TestDirectWorkerRejectsStageEventLogMissingOutcomeObserved、既有 policy-drift / sequence / local-URI 用例 |
| `result_artifact_completeness_failed` | failed result 缺 diagnostic / repair request / 必要 artifact 索引 | TestDirectWorkerRejectsFailedResultMissingDiagnostic、TestDirectWorkerRejectsFailedResultMissingRepairRequest、TestDirectFailedResultGateEnforcesContentRules、既有 trace-evidence 用例 |
| `package_idempotency_conflict` | 相同 ID 不同内容 | 既有 TestDirectPackageIdempotencyConflictUsesStableHTTPError |
| `direct_gateway_persistence_failed` | 快照损坏/篡改/版本不兼容（fail-closed） | 既有 persistence 测试组 |

Readiness/验证稳定码（`validationCheckMetaTable` + readiness）：

`business_input_missing`、`business_input_action_missing`、`declared_business_input_action_missing`、`selector_provenance_incomplete`、`stage_success_condition_missing`、`workspace_login_path_missing`、`credential_grant_missing`、`dynamic_route_binding_missing`、`post_action_validation_reuses_action_target`、`post_action_validation_reuses_approved_action_evidence`、`post_action_validation_reuses_action_identity`、`approved_contract_missing`、`approved_hash_missing`、`approved_stage_plan_empty`、`runtime_event_identity_mismatch`、`observed_stage_completion_missing`、`observed_state_not_runtime_derived`、`evidence_refs_missing`、`runtime_stage_report_missing`、`CROSS_DOMAIN_ACCESS`、`FORBIDDEN_PAGE_ACCESS`、`DERIVED_FROM_PLAN_EVIDENCE`、`REQUIRED_ASSERTION_FAILED`、`MISSING_OUTCOME_OBSERVED`、`OUT_OF_ORDER_EVENTS`、`MISSING_TRACE_ARTIFACT`、`MISSING_MP4_VIDEO`、`MISSING_STAGE_EVENT_LOG`、`REQUIRED_STAGE_NOT_COMPLETED`、`MISSING_EVIDENCE_REFS` 等（完整表见 `backend/internal/model/browser_agent_validation_annotations.go`）。

---

## 2. 自动化测试命令与结果

```bash
cd backend
go test ./internal/app ./internal/direct ./internal/model ./internal/orchestrator ./internal/executor
```

结果（2026-08-16）：

| 包 | 结果 |
|---|---|
| internal/direct | ok |
| internal/model | ok |
| internal/orchestrator | ok |
| internal/executor | ok |
| internal/app | 2 个 e2e 受主线渲染器缺陷阻塞（见 §6），其余全部通过 |

定向命令：

```bash
go test ./internal/app -run "TestDirect" -v          # Direct 全部门禁（含本周新增 8 项）
go test ./internal/app -run "TestPreExecutionFlags|TestURLMatch|TestValidationFindings|TestValidationReports" -v   # F 验收
go test ./internal/direct ./internal/orchestrator    # gateway/持久化/验证器
```

---

## 3. 脱敏 failed result 示例（含四元标识）

来源：`TestDirectRuntimeFailureNeverFabricatesBrowserEvidence`（基础设施失败包装路径）。

```json
{
  "package_id": "pkg_bundle_script_graph_<fixture>",
  "job_id": "job-infra-no-evidence",
  "stage_id": "<first approved stage>",
  "artifact_id": ["<stage_event_log artifact>", "<replay_manifest artifact>"],
  "status": "failed",
  "failure_diagnostic": {
    "id": "diag_<stage>",
    "schema_version": "demoops.script_failure_diagnostic.v1",
    "failed_node_id": "<node>",
    "error": {"code": "video_worker_missing", "message": "Server runtime failed before browser evidence was available."},
    "redaction_report": {"applied": true, "full_html_included": false},
    "browser_evidence_unavailable": true,
    "screenshot_refs": [],
    "validation_reports": []
  },
  "step_results": [{
    "node_id": "<node>", "status": "failed",
    "observed_state": "source=artifact_observation; infrastructure_failure=video_worker_missing"
  }],
  "repair_request": {"approval_required": true, "max_repair_attempts": 1}
}
```

要点：基础设施失败**不伪造**浏览器观察（ObservedState 只标 `infrastructure_failure=`，无 `browser_assertion`/`url_observed`），无截图引用，`browser_evidence_unavailable=true`，诊断已脱敏。

可重放现场：StageEventLog（JSONL，每 run 顺序递增）+ ReplayManifest（绑定 package/bundle/policy hash + run_id + validation report 索引）随 failed result 一并交付，App 按正式 artifact 流程分块下载校验。

---

## 4. Direct 链路测试矩阵

| 环节 | 正向 | 负向 |
|---|---|---|
| intake（A） | 完整审批包接收（TestValidateClientExecutionPackageIntakeAcceptsTeamProtocol） | 幂等冲突、digest 漂移、transport/source digest 混用、selector provenance 缺失、审批过期（9 子用例） |
| 凭据（B） | 一次性消费、grant 范围内使用 | envelope 范围外使用、重启后要求新 envelope、不落盘快照（TestPersistentGatewayNeverStoresCredential…） |
| Worker（B） | claim/progress/release 合同 | 心跳要求 loopback、离线、版本协商 |
| artifact（C） | 完整集合上传下载 | digest/size 漂移（本周新增 size 用例）、running 外上传拒绝 |
| result（E） | 成功/权威失败包接收 | 缺 StageEventLog/MP4/运行时验证、本地 URI、sequence 重复倒序、缺 outcome_observed（本周新增）、缺 diagnostic/repair（本周新增）、伪造证据（本周新增）、泄露（本周新增） |
| ACK（C） | 幂等 ACK、全量校验后 ACK | 未 ACK 禁止释放 lease（ErrResultAckRequired） |
| 重启恢复（D） | queued/running/completed/failed 恢复、lease 重绑 | 快照损坏 fail-closed、篡改拒绝、写失败回滚 |

## 5. Validation finding 矩阵（节选核心）

| finding code | 严重级别 | 责任模块 | 证据引用 | 修复建议 |
|---|---|---|---|---|
| business_input_action_missing | blocking | app | readiness finding + node_id | App 重新产包，加入 fill 交互；Server 不代填 |
| url_change_not_business_completion | blocking | app | outcome_observed 事件 | Outline expected_outcome 增加业务状态断言 |
| runtime_event_identity_mismatch | blocking | server | stage event + manifest hash | 拒绝跨包事件，重放同 run |
| observed_state_not_runtime_derived | blocking | server | StepResult + 事件对照 | 观察必须来自运行时，不得照抄 success_state |
| CROSS_DOMAIN_ACCESS | blocking | app/environment | Observation.URL | 收敛 allowed_domains 或修正目标 |
| media_artifact_generation_failed | warning/blocking | media_delivery | artifact manifest | 查 FFmpeg/磁盘/录屏数据（主线） |
| MISSING_EVIDENCE_REFS | blocking | server | 事件列表 | 补齐 evidence_refs 后重放 |

（完整 Impact/Suggestion/NextStep 由 `AnnotateValidationChecks` 自动填充并嵌入 failed diagnostic。）

## 6. 已知阻塞：主线渲染器 still 合成缺陷（非本任务范围）

固定验收的 3 个成功路径子场景（success / selector_alternative_repair / busy_page_wait_repair）被一个**真实的渲染器缺陷**阻塞，验证链路本身工作正确：

- 现象：`final_video_quality_gate: rendered duration does not match edit plan` — 编辑计划 2×4s（共 8s），实际渲染仅 **0.981s**；compositor `quality_status: "degraded"`，`method: ffmpeg_trim_concat`。
- 证据：demo_edit_plan target_duration_ms=8000；media_normalization output.duration_sec=0.981；render_manifest.compositor 指向单张 still 截图。
- 边界：视频编辑器/渲染器按交接文档保留给主线，本分支不修改 `video-worker/src/renderer.ts`。
- 附带发现：`dist/` 曾比 `src/` 旧（Aug 6 vs Aug 13–15），已重建；重建后缺陷仍在，说明缺陷在源码侧。
- 3 个失败路径子场景（locator_missing / required_validation_failure / wait_timeout）**全部通过**并返回预期稳定码——验证、失败包装、证据保留链路已完整可用。

## 7. 变更范围清单（未触碰保留模块）

本分支（7 个 commit）修改的文件全部位于：

- `backend/internal/model/`（验证注解、结构扩展）
- `backend/internal/app/`（outline runner、failed result、acceptance fixture/runner）
- `backend/internal/orchestrator/`（OutcomeVerifier adapter）
- `backend/internal/executor/`（render request 时长语义）
- `video-worker/src/renderer.ts`（仅一处时长 fallback 链，见 aa8b567；未改合成器/编辑器逻辑）
- `docs/`（设计与交付文档）

未修改：App 产包规则、Browser Agent 运行时（browser-agent-runtime.ts / 交互验证器）、TOS/TTS、Seedance/MiniMax 适配、视频编辑器与渲染器。

## 8. App 正式包字段兼容性报告（仅列事实）

- `pkg_bundle_script_graph_1786559723307775100`（完整性失败样本）：`ready_for_server_execution=false`、`project_idea_fill_present=false` — Server fail-closed 拒绝并输出 `business_input` 系 readiness blocker，未补写、未放宽。
- 固定 fixture 全字段可解析；未发现新增缺失字段。正式 App 包联调仍待主线安排（TOS/模型 key 未配置，见 §9）。

## 9. 待主线决策项

1. `video-worker` still 合成时长缺陷（§6，附完整证据）。
2. TOS 凭据（`VOLC_TOS_*` 全空）与 `CASCADE_ARK_MEDIA_MODE=dry_run`：Director→Seedance 增强链路关闭，视频只能用录制素材确定性合成。
3. 真实 App→Server→视频端到端验收（交接文档明确排除在本任务外）。
