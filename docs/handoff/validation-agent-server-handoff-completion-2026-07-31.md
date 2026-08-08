# Validation Agent 对接 Server 完成汇报（P0/P1/P2）

日期：2026-07-31
面向：明达（Validation Agent）
承接文档：`docs/handoff/validation-agent-server-handoff-2026-07-29.md`（§9 P0/P1/P2）
运行时：`browser-agent-outline-v1`
分支：`feat/browser-agent-outcome-verifier-adapter`
PR：https://github.com/Jonathan-Zhang-0423/cascade_demo_ops/pull/30

## 1. 结论

07-29 handoff §9 的三项工作已全部完成并验证：

- **P0（适配 OutcomeVerifier）**：已随 PR #28 合入 `main`。
- **P1（受限修复提案）**：完成，commit `d22b6a8`。
- **P2（共同真实包验收）**：完成，commit `2a5470a`；四个受控包已通过完整 Server 主路径端到端跑通，不是仅结构合法的占位包。

一处需要你知晓的既有失败见 §5，与本次 P0/P1/P2 无关。

## 2. P1：受限修复提案（commit d22b6a8）

对齐 §9 P1 的三条要求：

1. **身份完整**：修复提案现在从 validation check 携带 `NodeID` / `StageID` 进入 `RuntimeRepairProposal`，每条提案都带 run/stage 身份，不再出现空 StageID。
2. **仅 policy 允许时才生成**：`generateProposalForCheck` 增加两道闸门——
   - 早退：check 缺 `NodeID`/`StageID` 时直接返回 `nil`，不生成无主提案；
   - 终闸：`IsRepairAllowed(repairKind, field, repairPolicy)` 不通过时返回 `nil`。
   即只有审批包 repair policy 明确允许的 kind/field 才会产出 `repair_allowed` 提案。
3. **越权阻塞**：跨域、改业务输入/路由/顺序、凭据、破坏性操作等不在允许列表内的修复，被 `IsRepairAllowed` 拦下，不生成提案。

配套：`browser_agent_outcome_verifier_adapter.go` 在 `ValidateStageEvents` 阶段把 `NodeID`/`StageID` 打到它产出的每条 stage-event check 上（乱序、重复、缺 outcome_observed、derived_from_plan、无证据、required 断言失败、stage_failed），使 generator 能正确归属。`RuntimeRepairProposal` 使用规范常量 `RuntimeRepairProposalSchemaVersion`。

改动文件：
- `internal/model/browser_agent_runtime.go`（`ValidationCheck` 增 `NodeID`/`StageID` 字段）
- `internal/orchestrator/browser_agent_outcome_verifier_adapter.go`
- `internal/orchestrator/repair_proposal_generator.go`
- `internal/orchestrator/repair_proposal_generator_test.go`
- `internal/orchestrator/browser_agent_integration_test.go`

## 3. P2：共同真实包验收（commit 2a5470a）

### 3.1 四个受控包

均为 Server 自有、结构合法的 `browser-agent-outline-v1` `ClientExecutionPackage`，基于 `controlledBusinessAcceptancePackage` 的构建原语搭建，**每个失败包都先有一个真实 navigate 阶段**，因此失败可归因到它自己的目标阶段，而不是一个从未加载的页面。定义在 `internal/app/browser_agent_outline_scenario_packages.go`：

| 包 | 构建函数 | 阶段 | 预期运行时结果 |
|---|---|---|---|
| 成功 | `controlledOutlineSuccessPackage` | navigate + inspect | 完成 |
| locator 缺失 | `controlledOutlineLocatorMissingPackage` | navigate + click（目标不存在） | 停止 |
| required 验证失败 | `controlledOutlineRequiredValidationFailurePackage` | navigate + click（断言不成立） | 停止 |
| 构建未完成（等待） | `controlledOutlineWaitTimeoutPackage` | navigate + inspect（完成元素永不出现） | 停止 |

每个包配一个 httptest HTML handler，页面内容与该场景要触发的运行时行为严格对应。

### 3.2 端到端验收结果

`TestControlledOutlineScenarioPackagesRunCompleteServerPath` 通过 `runProtocolAcceptanceScenario` 把四个包各自跑完整 Server 主路径（Exchange intake → outline runner → policy/stage/verifier → result packager），使用真实无头 Chromium + FFmpeg。实测结果：

| 场景 | 交换状态 | 失败码 | 证据 | 断言 |
|---|---|---|---|---|
| 成功 | completed | — | strict ValidationReports（pre+2 stage+post）、MP4、录屏、trace、StageEventLog、delivery ack | 全部满足 |
| locator 缺失 | failed | `browser_agent_target_not_resolved` | 脱敏诊断 + 1 截图 + 1 trace + approval-required 修复请求 | 满足 |
| required 验证失败 | failed | `outcome_verification_failed` | 脱敏诊断 + 2 截图 + 1 trace + approval-required 修复请求 | 满足 |
| 构建未完成 | failed | `outcome_verification_failed` | 脱敏诊断 + 2 截图 + 1 trace + approval-required 修复请求 | 满足 |

即：**成功包证据链完整、失败包正确停止且保留脱敏诊断（截图 + trace）与 approval-required 修复请求**，符合 §9 P2 的验收口径。

### 3.3 实现过程中修正的两个真实缺陷

端到端跑通前发现并修复：

1. 成功包第二阶段原用裸 `<span>` 作 role=status 目标——真实无障碍树里无该 role，目标永远解析不到；改用 heading。
2. 三个失败包原先没有前置 navigate 阶段，页面从未加载，导致三者都返回同一个 `browser_agent_target_not_resolved`，并未真正区分失败类型；补上真实 navigate 阶段后，三种失败模式才各自成立、失败码互不相同。

结构性测试 `TestControlledOutlineScenarioPackagesAreStructurallyValid` 另外覆盖阶段数与 outline runtime 标记。

改动文件：
- `internal/app/browser_agent_outline_scenario_packages.go`（新增）
- `internal/app/browser_agent_outline_scenario_e2e_test.go`（新增）
- `internal/app/browser_agent_acceptance_test.go`

## 4. 验证命令

```bash
cd backend
# 结构性
go test ./internal/app/ -run TestControlledOutlineScenarioPackagesAreStructurallyValid -v
# 端到端（需 node + Chromium + FFmpeg/ffprobe + video-worker 产物，缺失则自动 skip）
go test ./internal/app/ -run TestControlledOutlineScenarioPackagesRunCompleteServerPath -v
# P1
go test ./internal/orchestrator/ -v
```

`internal/orchestrator`、`internal/model`、`internal/media` 全套通过。

## 5. 需要你知晓：一处既有失败（非本次引入）

`internal/app` 全量测试里 `TestTwoStepLoginScanBindsRuntimeEvidenceAndBuildsDraft` 失败，报 `runtime_page_evidence_missing`（登录扫描流程的 ScriptPackage 阶段）。已用 stash 全部本分支改动后对干净的 `origin/main` 复跑验证——**在 main 上同样失败**，与 P0/P1/P2 无关。是否另开单跟进由你决定。

## 6. 待你确认的接口点

§9 P2 写明"明达负责每包的预期 ValidationReport 断言"。本次端到端测试断言的是**失败码 + 诊断产物 + 停止行为**这一层。若你对每个包的 ValidationReport 有更细的逐条期望（具体 check 的 code / decision / required / evidence_refs），请给出清单，我把它们补进 `TestControlledOutlineScenarioPackagesRunCompleteServerPath` 的对应分支。

## 7. 交付物索引

- PR #30：https://github.com/Jonathan-Zhang-0423/cascade_demo_ops/pull/30
- P1：`d22b6a8`
- P2：`2a5470a`
- P0：已在 `main`（PR #28）
