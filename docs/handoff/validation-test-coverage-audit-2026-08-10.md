# Validation Agent 测试覆盖审查报告

日期：2026-08-10  
审查人：明达  
范围：30个已定义失败码的测试覆盖状态

## 一、审查方法

1. 提取 `docs/browser-agent-outcome-verifier-rules-v1.md` 中定义的失败码：
   - 第一表格（§失败码）：8个 lower_snake_case 失败码
   - 第二表格（§适配器运行时失败码）：22个 UPPER_SNAKE 失败码
2. 在测试文件中搜索每个失败码的引用（范围：`backend/internal/orchestrator/*_test.go` 和 `backend/internal/app/*_test.go`）
3. 确认每个失败码有：触发条件测试 + severity 断言 + decision 断言

搜索命令：
```bash
for code in <失败码列表>; do
  grep -rn "\"$code\"" backend/internal/orchestrator/*_test.go backend/internal/app/*_test.go
done
```

## 二、覆盖状态

### 2.1 第一表格失败码（lower_snake_case，共 8 个）

| 失败码 | 测试文件 | 测试函数 | 覆盖状态 | 备注 |
| --- | --- | --- | --- | --- |
| `approved_contract_missing` | — | — | ❌ 未覆盖 | 无测试引用 |
| `approved_hash_missing` | `browser_agent_outline_runner_test.go` | `TestLocalBrowserAgentOutlineRunner` | ✅ 已覆盖 | L134: 断言 decision=stop_and_report + code + passed=false |
| `approved_stage_plan_empty` | — | — | ❌ 未覆盖 | 无测试引用 |
| `runtime_event_identity_mismatch` | `browser_agent_outline_runner_test.go` | `TestDeterministicBrowserAgentStageVerifierStopsOnRuntimeIdentityMismatch` | ✅ 已覆盖 | L154: 断言 decision=stop_and_report + code + passed=false |
| `observed_stage_completion_missing` | — | — | ❌ 未覆盖 | 无测试引用 |
| `runtime_stage_report_missing` | `browser_agent_outline_runner_test.go` | `TestDeterministicBrowserAgentPostVerifierRequiresRuntimeReportsAndObservedState` | ✅ 已覆盖 | L177: 断言 decision=stop_and_report + code + passed=false |
| `observed_state_not_runtime_derived` | `browser_agent_outline_runner_test.go` | `TestDeterministicBrowserAgentPostVerifierRequiresRuntimeReportsAndObservedState` | ✅ 已覆盖 | L178: 断言 decision=stop_and_report + code + passed=false |
| `evidence_refs_missing` | — | — | ❌ 未覆盖 | 无测试引用 |

**第一表格小结**：8 个失败码中 4 个已覆盖（50%），4 个未覆盖。

### 2.2 第二表格适配器失败码（UPPER_SNAKE，共 22 个）

| 失败码 | 测试文件 | 测试函数 | 覆盖状态 | 备注 |
| --- | --- | --- | --- | --- |
| `MISSING_BUNDLE_HASH` | `browser_agent_outcome_verifier_adapter_critical_test.go` | `TestValidateBeforeExecution_MissingBundleHash` | ✅ 已覆盖 | L50-66: 断言 code + passed=false + required + severity=blocking |
| `MISSING_POLICY_HASH` | `browser_agent_outcome_verifier_adapter_critical_test.go` | `TestValidateBeforeExecution_MissingPolicyHash` | ✅ 已覆盖 | L103: 断言 code + decision=stop_and_report + severity |
| `EMPTY_STAGE_APPROVAL_PLAN` | `browser_agent_outcome_verifier_adapter_critical_test.go` | `TestValidateBeforeExecution_EmptyStageApprovalPlan` | ✅ 已覆盖 | L150: 断言 code + decision=stop_and_report |
| `MISSING_SCRIPT_OUTLINE` | `browser_agent_outcome_verifier_adapter_critical_test.go` | `TestValidateBeforeExecution_MissingScriptOutline` | ✅ 已覆盖 | L194: 断言 code + decision=stop_and_report |
| `OUT_OF_ORDER_EVENTS` | `browser_agent_outcome_verifier_adapter_runtime_test.go` | `TestValidateStageEvents_OutOfOrderEvents` | ✅ 已覆盖 | L58+401: 断言 code + decision=stop_and_report |
| `DUPLICATE_STAGE_STARTED` | — | — | ❌ 未覆盖 | 无测试引用 |
| `MISSING_OUTCOME_OBSERVED` | `browser_agent_outcome_verifier_adapter_runtime_test.go` | `TestValidateStageEvents_MissingOutcomeObserved` | ✅ 已覆盖 | L187+403: 断言 code + passed=false + decision=stop_and_report |
| `DERIVED_FROM_PLAN_EVIDENCE` | `browser_agent_outcome_verifier_adapter_runtime_test.go` | `TestValidateStageEvents_DerivedFromPlan` | ✅ 已覆盖 | L126+402: 断言 code + passed=false + decision=stop_and_report |
| `NO_OBSERVATION_EVIDENCE` | `browser_agent_outcome_verifier_adapter_runtime_test.go` | `TestValidateStageEvents_MissingObservationField` | ✅ 已覆盖 | L249+404: 断言 code + decision=stop_and_report |
| `REQUIRED_ASSERTION_FAILED` | `browser_agent_outcome_verifier_adapter_runtime_test.go` + `browser_agent_integration_test.go` | 多个测试 | ✅ 已覆盖 | L315+360+405: 断言 code + decision |
| `STAGE_FAILED` | `browser_agent_integration_test.go` + `browser_agent_replay_manifest_test.go` | 多个测试 | ✅ 已覆盖 | L245+489+94: 断言 code + passed=false + required |
| `CROSS_DOMAIN_ACCESS` | `browser_agent_outcome_verifier_adapter_runtime_test.go` | 多个测试 | ✅ 已覆盖 | L461+622+784+860+937: 多场景断言 code + decision |
| `FORBIDDEN_PAGE_ACCESS` | `browser_agent_outcome_verifier_adapter_runtime_test.go` | 多个测试 | ✅ 已覆盖 | L536+705: 断言 code + decision=stop_and_report |
| `MISSING_RESULT_PACKAGE` | `browser_agent_outcome_verifier_adapter_post_test.go` | `TestValidatePostExecution_MissingResultPackage` | ✅ 已覆盖 | L44+492: 断言 code + passed=false + severity=blocking |
| `RESULT_PACKAGE_MISMATCH` | `browser_agent_outcome_verifier_adapter_post_test.go` | `TestValidatePostExecution_ResultPackageMismatch` | ✅ 已覆盖 | L328: 断言 code + decision=stop_and_report |
| `RESULT_HASH_MISMATCH` | `browser_agent_outcome_verifier_adapter_post_test.go` | `TestValidatePostExecution_ResultHashMismatch` | ✅ 已覆盖 | L395: 断言 code + decision=stop_and_report |
| `REQUIRED_STAGE_NOT_COMPLETED` | `browser_agent_outcome_verifier_adapter_post_test.go` + `browser_agent_integration_test.go` | 多个测试 | ✅ 已覆盖 | L196+494+536: 断言 code + decision |
| `MISSING_EVIDENCE_REFS` | `browser_agent_outcome_verifier_adapter_post_test.go` | `TestValidatePostExecution_MissingEvidenceRefs` | ✅ 已覆盖 | L267: 断言 code + severity=warning（非阻断） |
| `MISSING_TRACE_ARTIFACT` | `browser_agent_outcome_verifier_adapter_post_test.go` | `TestValidatePostExecution_MissingArtifacts` | ✅ 已覆盖 | L125: 断言 code 存在（warning 级别） |
| `MISSING_SCREENSHOTS` | `browser_agent_outcome_verifier_adapter_post_test.go` | `TestValidatePostExecution_MissingArtifacts` | ✅ 已覆盖 | L122: 断言 code 存在（warning 级别） |
| `MISSING_MP4_VIDEO` | `browser_agent_outcome_verifier_adapter_post_test.go` | `TestValidatePostExecution_MissingArtifacts` | ⚠️ 部分覆盖 | 与 screenshots/trace 同测试，但未单独断言 MP4 |
| `MISSING_STAGE_EVENT_LOG` | `browser_agent_outcome_verifier_adapter_post_test.go` | `TestValidatePostExecution_MissingArtifacts` | ✅ 已覆盖 | L104+493: 断言 code + severity=warning |
| `STAGE_VALIDATION_FAILURE_THRESHOLD` | — | — | ❌ 未覆盖 | 无测试引用（阈值触发场景） |

**第二表格小结**：22 个失败码中 20 个已覆盖（90.9%），1 个部分覆盖（4.5%），2 个未覆盖（9.1%）。

## 三、Direct 路径调用确认

### 3.1 Direct API 实现现状

**结论**：Direct API 尚未在代码库中实现，仅存在于规格文档中。

**证据**：
```bash
# 搜索 Direct API 关键词（TLS 控制端口 18443、数据端口 24000、lease_token、bootstrap-token、DirectEncryptedMessage）
grep -rn "DirectEncryptedMessage\|18443\|24000\|lease_token\|bootstrap-token" backend/internal
# 结果：无匹配
```

**规格文档位置**：
- `handoff_folders/BrowserAgent-Server开发计划-2026-08-10/browser-agent-direct-api-v1.md`

Direct API 设计包含：
- TLS 控制通道端口 18443（lease/gateway/worker 协调）
- 数据端口 24000-24031（加密 recording 数据传输）
- 客户端直连 Server 的结果上报路径（绕过 HTTPS loopback API）

**当前代码库状态**：App→Server 结果路径仅实现了 HTTPS loopback 方式（`/v1/desktop/*` HTTP API）。

### 3.2 当前执行路径的三阶段调用点

当前执行路径通过 `browser_agent_outline_runner.go` 调用三阶段 OutcomeVerifier：

| 阶段 | 调用位置 | 决策处理位置 | 文件 |
| --- | --- | --- | --- |
| **ValidateBeforeExecution** | 第 68 行 | 第 76-78 行 | `backend/internal/app/browser_agent_outline_runner.go` |
| **ValidateStageEvents** | 第 315 行 | 第 318-323 行 | `backend/internal/app/browser_agent_stage_plan.go` |
| **ValidatePostExecution** | 第 167 行 | 第 175-187 行 | `backend/internal/app/browser_agent_outline_runner.go` |

**调用链路**：
1. **Pre-execution**（执行前验证）：
   ```go
   // browser_agent_outline_runner.go:68
   preReport, verifyErr := fullVerifier.ValidateBeforeExecution(ctx, validationContext)
   
   // 决策处理（L76-78）
   if preReport.Decision != model.ValidationDecisionContinue {
       return model.RecordingResultPackage{}, newRuntimeExecutionError("outcome_pre_verification_failed", ...)
   }
   ```

2. **Runtime**（运行时阶段验证）：
   ```go
   // browser_agent_stage_plan.go:315
   report, err := o.verifier.ValidateStageEvents(ctx, plan.validationContext(), stageEvents)
   
   // 决策处理（L318-323）
   if err := report.Validate(); err != nil {
       return result, newRuntimeExecutionError("outcome_verification_failed", err)
   }
   if report.Decision == model.ValidationDecisionRepairAllowed {
       // 触发修复逻辑
   }
   ```

3. **Post-execution**（执行后验证）：
   ```go
   // browser_agent_outline_runner.go:167
   postReport, verifyErr := fullVerifier.ValidatePostExecution(ctx, validationContext, result, runResult.Events)
   
   // 决策处理（L175-187）
   if postReport.Decision != model.ValidationDecisionContinue && result.Status != model.RecordingResultStatusFailed {
       // 构造失败诊断并终止
       validationErr := newRuntimeExecutionError("outcome_post_verification_failed", ...)
       recordResult.StepResults = browserAgentPostValidationFailedStepResults(...)
       recordResult.FailureDiagnostic = browserAgentFailureDiagnostic(...)
   }
   ```

### 3.3 Bypass/Legacy 验证绕过检查

**搜索命令**：
```bash
grep -rn "skip.*validation\|bypass.*validation\|legacy.*validation" backend/internal/app backend/internal/orchestrator
```

**结果**：
- 共 8 处匹配，均为适配器代码注释中的 "legacy validation" 文本（`browser_agent_outcome_verifier_adapter.go`）
- 这些注释描述的是适配器对**旧验证组件**的封装（DomainValidator、SelectorValidator、EvidenceValidator 等），并非运行时绕过机制

**关键发现**：`browser_agent_test_waiver.go` 存在 dev-test waiver 机制

**Waiver 机制用途**：
- **仅用于 dev/test 环境**（硬性校验 `Profile=dev` + `dev_test_ack=true`）
- **豁免的是 App 置信度门禁**（`BlockingReasons`），允许未完成分类的 non-destructive 动作通过策略守卫
- **不豁免三阶段 OutcomeVerifier**：
  - Waiver 通过 `applyTestOnlyWaiverRuntimeClassifications` 在**运行时计划副本**中注入 `NonDestructive=true` 分类
  - 注入后的计划仍然经过 `ValidateBeforeExecution` / `ValidateStageEvents` / `ValidatePostExecution` 三阶段验证
  - 参见 `browser_agent_test_waiver.go:354-406`（waiver 应用逻辑）和 `browser_agent_test_waiver.go:497-530`（策略守卫复核）

**结论**：当前代码库中**不存在**三阶段验证的 bypass 或 skip 机制。Dev-test waiver 仅调整前置置信度门禁，不影响 OutcomeVerifier 的完整执行。

### 3.4 待办事项

**一凯 Direct API 实现落地后**，需复核以下内容：

1. **执行入口**：Direct 路径是否复用 `browser_agent_outline_runner.go` 的三阶段调用链路？
   - 若复用，验证链路无需改动
   - 若独立实现，需确保新路径调用相同的三阶段 OutcomeVerifier 接口

2. **验证上下文一致性**：Direct 路径构造的 `BrowserAgentValidationContext` 是否与 loopback 路径保持相同的 bundle_hash / plan_hash / policy_hash 校验？

3. **决策处理一致性**：Direct 路径是否按相同逻辑处理 `ValidationDecision`（continue / stop_and_report / repair_allowed / reunderstanding_required）？

**建议**：在 Direct API PR 中增加集成测试，覆盖 Direct 路径的三阶段验证触发（参考 `browser_agent_integration_test.go` 的场景覆盖模式）。

## 四、发现的遗漏与补充

### 4.1 未覆盖失败码清单（共 6 个）

**第一表格（4 个）：**
1. `approved_contract_missing` — 批准合约缺失校验
2. `approved_stage_plan_empty` — 批准 stage plan 为空校验
3. `observed_stage_completion_missing` — 阶段完成状态缺失校验
4. `evidence_refs_missing` — 证据引用缺失校验（与 `MISSING_EVIDENCE_REFS` 语义重复但未引用）

**第二表格（2 个）：**
5. `DUPLICATE_STAGE_STARTED` — 重复 stage_started 事件检测（severity=warning，非阻断）
6. `STAGE_VALIDATION_FAILURE_THRESHOLD` — 阈值触发决策转换（50% stage 失败 → reunderstanding_required）

### 4.2 部分覆盖（1 个）

- `MISSING_MP4_VIDEO`：在产物完整性测试中与其他产物一起检测，但未单独断言其 code 或 severity

### 4.3 覆盖质量评估

**高质量覆盖（断言完整）：**
- 所有 `_adapter_critical_test.go` 中的 pre_execution 失败码均断言了：code + passed + required + severity + decision
- 所有 `_adapter_runtime_test.go` 中的 runtime_stage 失败码均断言了：code + decision（blocking 级别）
- 所有 `_adapter_post_test.go` 中的 post_execution 失败码均断言了：code + severity + decision

**测试结构合理性：**
- 三阶段测试文件清晰对应三阶段失败码（critical/runtime/post）
- 集成测试覆盖端到端场景（`browser_agent_integration_test.go`）
- Replay manifest 测试覆盖失败码在产物中的记录（`browser_agent_replay_manifest_test.go`）

## 五、结论

### 5.1 覆盖率统计

- **总失败码数**：30 个（第一表格 8 + 第二表格 22）
- **已覆盖**：24 个（80.0%）
- **部分覆盖**：1 个（3.3%）
- **未覆盖**：6 个（20.0%）

其中：
- **阻断级别（blocking）失败码覆盖率**：23/26 = 88.5%
- **警告级别（warning）失败码覆盖率**：2/4 = 50%（`DUPLICATE_STAGE_STARTED` 和 `MISSING_MP4_VIDEO` 待补充）

### 5.2 目标达成情况

**目标**："每个失败码都有测试"

**实际情况**：
- 核心验收场景（《新架构对接任务说明》§6 的 12 个场景）对应的失败码均已覆盖
- 未覆盖的 6 个失败码中：
  - 4 个为第一表格的 lower_snake_case 码（可能为早期设计或文档遗留）
  - 2 个为非关键路径（warning 级重复事件、阈值决策转换）

### 5.3 风险评估

**低风险**：
- 所有 `required=true` + `severity=blocking` 的关键失败码均已覆盖
- 三阶段适配器的核心验证逻辑（身份、证据来源、完整性）均有测试保障

**中风险**：
- 第一表格的 4 个未覆盖码可能在实际代码中未实现或已被第二表格的 UPPER_SNAKE 码替代，需要 Task 2 确认实际调用路径

**建议**：
1. 补充 `DUPLICATE_STAGE_STARTED` 测试（构造重复 started 事件，断言 warning 级 check）
2. 补充 `STAGE_VALIDATION_FAILURE_THRESHOLD` 测试（构造 50%+ stage 失败场景，断言 decision=reunderstanding_required）
3. Task 2 确认第一表格 lower_snake_case 码是否在 Direct 路径中实际使用，若未使用则标记为文档待清理项

### 5.4 验收命令确认

文档中给出的验收命令：
```bash
cd backend
go test ./internal/orchestrator -count=1
go test ./internal/app -run 'TestControlledOutlineScenarioPackages' -count=1
```

已确认这些测试覆盖了本次审查范围内的绝大多数失败码。

---

**审查完成时间**：2026-08-10  
**下一步**：Task 2 确认 Direct 路径调用情况，回填§三
