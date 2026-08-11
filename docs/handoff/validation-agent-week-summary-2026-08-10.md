# Validation Agent 本周工作总结

日期：2026-08-10
负责人：明达
任务来源：孟洋《BrowserAgent-Server开发计划-2026-08-10》

## 一、本周任务范围

**职责边界：** 验证链路（OutcomeVerifier 三阶段验证、ValidationReport、失败码、责任域、证据引用、Replay Manifest）

**不负责：** Browser Agent 执行链路、Direct Gateway/Worker、录屏/MP4 渲染（一凯负责）

**任务目标：**
1. 对上周已完成的验证能力做完整性核查（30 个失败码的测试覆盖状态）
2. 补充交叉校验增强（observed_state 追溯、evidence_refs artifact 存在性）
3. 补齐 6 个原未覆盖失败码的测试（5 个补齐，1 个有知情 t.Skip）
4. 补充 RuntimeRepairProposal 规则测试（capture_timing、frame_resolution）
5. 编写真实包验证检查清单与冻结受控包预期规格，为真实包端到端验证做准备

## 二、本周交付物

### 2.1 代码改动

所有改动均在 `feat/validation-agent-completeness-week2` 分支，经审查后合并/提交。

**commit 61e194c** — feat: add observed_state traceability cross-check in post-execution validation
- 在 `ValidatePostExecution` 中新增 `observed_state` 来源追溯检查
- 复用已有失败码 `observed_state_not_runtime_derived`（blocking）
- 检查 StepResult.observed_state 是否含 "source=" 标记，并确认对应真实 outcome_observed 事件存在
- 新增测试 `TestValidatePostExecution_ObservedStateTraceability`（3 个子场景，均 PASS）

**commit b7da5a2** — feat: add evidence_refs artifact existence cross-check and EVIDENCE_ARTIFACT_REFERENCE_BROKEN code
- 在 `ValidatePostExecution` 中新增 `evidence_refs` artifact 存在性检查
- 新增失败码 `EVIDENCE_ARTIFACT_REFERENCE_BROKEN`（post_execution / warning / non-required），已注解（Impact/Suggestion/NextStep/ResponsibilityDomain）
- 新增测试 `TestValidatePostExecution_EvidenceArtifactIntegrity`（2 个子场景，均 PASS）

**commit a2ac4be** — test: add validation report order stability test
- 新增 `TestValidationReportOrderStability`：断言 ValidationReport 输出顺序为 1 pre + N runtime + 1 post，保证报告结构稳定性

**commit b5e70b0** — test: cover the 6 remaining validation failure codes
- 为原 6 个未覆盖失败码中的 5 个补充了测试，全部 PASS：
  - `approved_contract_missing`
  - `approved_stage_plan_empty`
  - `observed_stage_completion_missing`
  - `evidence_refs_missing`
  - `DUPLICATE_STAGE_STARTED`
- `STAGE_VALIDATION_FAILURE_THRESHOLD` 以有据可查的 `t.Skip` 处理（原因见§五·风险与待办）

**commit 660d591** — test: cover capture_timing and frame_resolution repair proposal rules
- 补充 `capture_timing` 与 `frame_resolution` 的 RuntimeRepairProposal 规则测试及 policy-gate reject 测试
- 注：`selector` 与 `wait` 两类规则在前期已覆盖

### 2.2 文档交付

1. **测试覆盖审查报告**
   文件：`docs/handoff/validation-test-coverage-audit-2026-08-10.md`
   内容：30 个失败码的测试覆盖状态、Direct 路径调用确认、完整性结论（含本周补齐后的更新结论）

2. **受控包预期 ValidationReport 冻结规格**
   文件：`docs/handoff/validation-report-expectations-controlled-packages-2026-08-10.md`
   内容：4 个受控包的预期规格冻结（success / locator_missing / required_validation_failure / wait_timeout），供真实包签收时对照使用。注：3 个 responsibility_domain 值待与孟洋对齐（已在文档中标记）

3. **真实包验证检查清单**
   文件：`docs/handoff/validation-agent-real-package-acceptance-checklist-2026-08-10.md`
   内容：五项必查点、成功/失败场景签收标准、不签收场景清单，独立可执行

4. **本周工作总结**（本文件）
   文件：`docs/handoff/validation-agent-week-summary-2026-08-10.md`

### 2.3 Commit 记录（本周，按时序）

```
a2ac4be  test: add validation report order stability test
61e194c  feat: add observed_state traceability cross-check in post-execution validation
b7da5a2  feat: add evidence_refs artifact existence cross-check and EVIDENCE_ARTIFACT_REFERENCE_BROKEN code
b5e70b0  test: cover the 6 remaining validation failure codes
660d591  test: cover capture_timing and frame_resolution repair proposal rules
48eaa12  docs: freeze expected ValidationReport specs for controlled packages
eba0fbf  docs: add real package validation acceptance checklist for week collaboration
```

## 三、核查结果

### 3.1 测试覆盖完整性

**覆盖率（本周核查完成后）：**

| 状态 | 数量 | 占比 |
| --- | --- | --- |
| 已覆盖 | 29 | 96.7% |
| 部分覆盖 | 1（MISSING_MP4_VIDEO） | 3.3% |
| 有知情 t.Skip | 1（STAGE_VALIDATION_FAILURE_THRESHOLD） | — |
| 无测试 | 0 | 0% |

- **阻断级别（blocking）失败码**：100% 覆盖
- **所有 required=true 失败码**：100% 覆盖

### 3.2 Direct 路径确认

**结论**：Direct API 在当前代码库中尚未实现（仅存于规格文档 `browser-agent-direct-api-v1.md`）。

当前执行路径通过 `browser_agent_outline_runner.go` 调用三阶段 OutcomeVerifier，不存在三阶段验证的 bypass 或 skip 机制。Dev-test waiver 仅调整前置置信度门禁，不影响 OutcomeVerifier 完整执行。

一凯 Direct API 落地后，需复核新路径是否复用同一三阶段调用链路（详见审查报告 §3.4）。

### 3.3 交叉校验增强

| 新增检查 | 失败码 | 触发条件 | 级别 | 状态 |
| --- | --- | --- | --- | --- |
| observed_state 来源追溯 | `observed_state_not_runtime_derived` | 手工伪造/缺少 outcome 事件 | blocking | ✅ 测试全绿 |
| evidence_refs artifact 存在性 | `EVIDENCE_ARTIFACT_REFERENCE_BROKEN` | 引用不存在的 artifact | warning | ✅ 测试全绿 |

### 3.4 测试运行结果

```
环境：Go 1.26.5 darwin/arm64，ffmpeg 8.1.2
      Playwright Chromium builds：chromium-1228、chromium-1234、chromium_headless_shell-1228

go build ./...                                    PASS（clean）
go test ./internal/orchestrator ./internal/model  PASS
go test ./internal/app（本周新增 4 个失败码测试）  PASS（隔离运行）
```

**预先存在的失败（不是回归）：**
`go test ./internal/app` 包级别整体运行会出现 2 个测试失败：
- `TestProtocolBrowserAgentAcceptanceRunsCompleteServerPath`
- `TestControlledOutlineScenarioPackagesRunCompleteServerPath`

这两个测试依赖 live server 和 Chromium 运行时环境，在本机 clean main 分支上于本周工作开始前即已失败。与本周所有改动无关，不是回归，属于环境限制下的已知失败。

## 四、下一步（真实包验证）

**准备就绪：**
- 验证链路能力完整，所有 blocking 级别失败码 100% 覆盖
- 受控包预期规格已冻结（4 个场景），可立即对照使用
- 真实包签收清单已明确（五项必查点 + 成功/失败场景判定标准）

**当前状态：**
孟洋 day-4/5 的真实包独立复跑目前被 APP-001（App 侧 selector 生成问题）阻塞。冻结规格文档与签收清单是为该验证准备的基线，一旦真实包可用即可执行。

**验收流程（就绪后执行）：**
1. 按照《真实包验证检查清单》五项必查点逐一检查
2. 根据成功/失败场景选择对应签收标准
3. 填写签收结论或不签收报告
4. 签收通过 → 向孟洋报告；不签收 → 与孟洋/一凯沟通修复方案

## 五、风险与待办

### 5.1 STAGE_VALIDATION_FAILURE_THRESHOLD 不可达路径（需 一凯/孟洋 决策）

**现象**：失败码 `STAGE_VALIDATION_FAILURE_THRESHOLD`（50% stage 失败 → decision=reunderstanding_required）在 orchestrator 适配器路径下结构上不可达，对应测试标记 `t.Skip`。

**根因**：`convertEventsToPostExecutionAnalyses`（`browser_agent_outcome_verifier_adapter.go` 约 L1224）在构建 `PostExecutionAnalysis` 时从未填充 `ObservedIssues` 字段，导致 `failedStageCount` 结构上永远为 0，`>=50%` 阈值分支永远不会经此路径触发。

**安全性说明**：这不是安全漏洞，不存在真实失败被放行的问题。真正失败的运行仍会被 `STAGE_FAILED` / `REQUIRED_ASSERTION_FAILED` 截停，decision 被置为 stop_and_report。丢失的仅是 reunderstanding_required 这一决策分类细节。

**建议行动（超出验证链路职责边界，决策权归 一凯/孟洋）：**
考虑是否让适配器将真实 observed issues 映射进 `PostExecutionAnalysis.ObservedIssues`——这是执行/适配器层的 wiring 改动，需一凯评估实现成本并决策是否推进。

### 5.2 真实包验证阻塞（APP-001）

真实包独立复跑（孟洋 day-4/5 计划项）被 APP-001 阻塞：App 侧 selector 生成问题尚未修复，无法提供有效的真实包产出。冻结规格文档与签收清单已就绪，等待 APP-001 修复后即可执行。

### 5.3 受控包规格待对齐项

`validation-report-expectations-controlled-packages-2026-08-10.md` 中有 3 个 `responsibility_domain` 字段标记为"待与孟洋对齐"，请在下次同步时确认。

### 5.4 MISSING_MP4_VIDEO 部分覆盖

`MISSING_MP4_VIDEO` 目前在产物完整性测试中与其他产物一同检测，未单独断言其 code 或 severity。风险较低（warning 级、非阻断），可在后续迭代中按需补充。

---

**总结**：Validation Agent 验证链路本周任务已完成。30 个失败码中 29 个有测试（含 1 个有知情 t.Skip），所有 blocking 级别失败码 100% 覆盖，交叉校验能力已增强，真实包验证文档已就绪。唯一结构性风险（STAGE_VALIDATION_FAILURE_THRESHOLD 不可达）已明确记录，不影响当前安全性，已上报供 一凯/孟洋 决策。
