# Validation Agent Server 侧本轮交付说明

日期：2026-08-08
分支：`feat/browser-agent-outcome-verifier-adapter`
对接文档：`Validation-Agent-Server工作说明.md`（v1.0，2026-08-07）

## 一、本轮完成范围

对照工作说明 §5 任务清单：

| 任务 | 状态 | 说明 |
| --- | --- | --- |
| P0.1 校验能力接入三阶段链路 | 已完成 | PR #38 已并入 main（`2c77411`） |
| P0.2 `AllowedDomains` 传入 Validation Context | 已完成 | `browser_agent_stage_plan.go` validationContext() 已传入 |
| P0.3 跨域/禁止页/乱序/缺观察/assertion 失败 | 已完成 | 6 个失败码，测试全绿 |
| P0.4 结果包 ID + 源哈希一致性 | 已完成 | `RESULT_PACKAGE_MISMATCH`、`RESULT_HASH_MISMATCH` |
| P0.5 Report 关联 node/stage/证据 | 已完成 | Report 与 Check 均带 node_id/stage_id/evidence_refs |
| P0.6 可重放清单 | 本轮新增完成 | `model.ReplayManifest` + `BuildReplayManifest`，落盘 `{EventDir}/replay-manifest.json` |
| P0.7 blocking 停止执行 | 已完成 | `stop_and_report` 在 outline_runner 与 stage_plan 消费 |
| P0.8 错误脱敏 | 已完成 | `DiagnosticRedactionReport`，`FullHTMLIncluded=false` |
| P1.1 受限修复提案 + 策略闸门 | 已完成 | `RepairProposalGenerator` + `IsRepairAllowed` |
| P1.2 每个失败码补影响/建议/下一步 | 本轮新增完成 | `ValidationCheck` 增 `Impact`/`Suggestion`/`NextStep`，注解表覆盖 24 码 |
| P1.3 四类责任域分类 | 本轮新增完成 | `ResponsibilityDomain`：app/server/validation/environment |
| P1.4 可自动执行的失败场景矩阵 + 验收报告 | 已完成 | 受控 outline 场景包 + 规则文档 |
| P1.5 真实 App 原始包独立复跑 | 阻塞 | 见下方风险，受 APP-001 阻塞 |
| P2.1 失败码统计/耗时/重复问题聚合 | 本轮新增完成 | `model.ValidationAggregate` + `AggregateValidation`，内嵌于 Replay Manifest |
| P2.2–P2.4 后续增强 | 未开始 | P2.2 需要新 HTTP 端点，P2.3/P2.4 依赖真实端到端跑通，均非本轮必做 |

## 二、交付物（对照 §10）

1. 代码提交：`ca7e4d5`（P1.2/P1.3/P0.6）+ 已并入 main 的 PR #38。
2. 失败码与责任域清单：`docs/browser-agent-outcome-verifier-rules-v1.md`（24 码含责任域）。
3. 三阶段 ValidationReport 示例：同上文档 §10.3。
4. 可重放清单与证据索引示例：同上文档 §10.4。
5. 12 个固定场景测试结果：`internal/orchestrator` 全绿；受控 outline 场景包端到端验收。
6. 真实 App 原始包独立复跑报告：`docs/handoff/local-bypass-waiver-test-report-2026-08-06.md`（受 APP-001 阻塞）。
7. 未解决问题、风险和下一步建议：本文件第三节。
8. 跳过/降级/本地测试专用行为：本文件第四节。

## 三、未解决问题、风险与下一步

### 风险 1：真实 App 原始包端到端复跑被 App 侧阻塞（P1.5）

- 现象：真实贪吃蛇执行包经 test waiver 进入真实浏览器执行后，在第一阶段
  `browser_agent_target_not_resolved` 停止。
- 根因：App 产包的 selector 是猜测值（`runtime_adaptive_intent_fallback`），
  真实页面不存在。App 生成 `[data-testid='new-project']`/`[data-testid='create-project']`，
  真实控件为 `data-testid="button-new-project"`（产品源码 `dashboard.tsx:1048`）。
- 责任方：App / Outline 生成侧（对应孟洋台账 APP-001、APP-011）。
- 影响：无法完成真实 App 包与主线结论的一致性复跑；Validation 侧判定正确
  （failed + 脱敏诊断 + 阶段事件完整），未误判为成功。
- 下一步：等 App 侧修复 selector 证据后，用同一真实包重跑；Validation 侧无需改动。

### 风险 2：两个真实浏览器 e2e 测试在本机失败（pre-existing）

- `TestProtocolBrowserAgentAcceptanceRunsCompleteServerPath`、
  `TestControlledOutlineScenarioPackagesRunCompleteServerPath/success` 在本机失败。
- 已验证为 pre-existing：在 clean merged-main（不含本轮任何改动）上以相同方式失败，
  与本轮提交无关。
- 下一步：需在标准 Server 环境（Chromium/FFmpeg 就绪）复核；不应将本机 `skip`/失败
  记为通过。

### 风险 3：P2 部分未开始

- P2.1（失败码统计/耗时/聚合）已完成：`model.ValidationAggregate` + `AggregateValidation`，
  内嵌于 Replay Manifest 的 `aggregate` 字段，5 项测试全绿。
- P2.2（按 run/package/stage/code 查询接口）不依赖 App 修复，可独立开展，但需要新
  HTTP 端点和存储层，建议与 P2.3 UI 一起做。
- P2.3（验收页面展示数据）与 P2.4（受控重放触发）在真实端到端跑通前收益有限，待
  APP-001 修复后再排期。

## 四、跳过、降级与本地测试专用标记

- 本地旁路（test waiver）产物全程标记 `dev_test_only=true`、
  `not_for_exchange_upload=true`、`formal_exchange=false`；不得作为正式 Exchange 结果。
- Replay Manifest 在旁路执行下 `dev_test_only=true`，并记录 `waiver_id` 与
  `waiver_blocked_reasons`，保留原始阻断原因。
- 两个真实浏览器 e2e 测试在本机失败已如实标注为 pre-existing，未记为通过。
- 未对 App 代码、App 产包规则或原始包哈希做任何修改（遵守 §8 代码边界）。
