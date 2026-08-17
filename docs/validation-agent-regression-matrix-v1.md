# Validation Agent 回归矩阵 v1

本矩阵把三日开发任务的最小回归集映射到可执行测试；fixture、mock 和 Server-controlled 包均为非正式结果，不能替代真实 App→Server 联调签收。

| 场景 | 证明点 | 测试 |
| --- | --- | --- |
| Server-controlled 成功 | 成功报告可生成且非正式 | `TestBuildValidationRunReportMarksServerFixtureNonFormalAndRoutesFeedback` |
| 历史失败重放 | 失败报告含 stage、证据、责任域和修复建议 | `TestBuildValidationRunReportClassifiesRuntimeProviderAndEnvironmentFindings` |
| 哈希不一致 | 报告拒绝 bundle/policy 漂移 | `TestBuildValidationRunReportRejectsHashMismatch` |
| 缺截图/Trace | 正式结果缺审计产物时拒绝交付 | `TestFormalOutlineResultRequiresReplayAndEditorHandoffArtifacts` |
| 动作后业务结果不足 | URL 改变不能代替业务完成 | `TestURLMatchAloneDoesNotProveBusinessCompletion` |
| 会话/浏览器运行时 | `session_expired` 映射为 `browser_runtime` | `TestBuildValidationRunReportClassifiesRuntimeProviderAndEnvironmentFindings` |
| selector 冲突 | App 批准的 selector provenance 不允许漂移 | `TestOutlineRuntimeFullPathRejectsUnapprovedSelectorRepair` |
| 环境错误 | 环境 finding 独立分类 | `TestBuildValidationRunReportClassifiesRuntimeProviderAndEnvironmentFindings` |
| 禁止操作 | 运行时发出 blocking finding | `TestForbiddenOperationAttemptedIsReportedFromRuntimeAction` |
| 动态路由 | 字面 `:id` 被拒绝，受约束模板可接受 | `TestBrowserAgentReadinessRejectsLiteralDynamicRouteValidation` |
| fill 后验证 | 输入语义和提交值不能漂移 | `TestValidateBrowserAgentOutlineConsistencyRejectsBusinessInputValueDrift` |
| 动作/结果目标分离 | outcome 只能由后置断言证明 | `TestSemanticValidationRejectsVisibleClickedControlAsOutcome` |
| 目标几何 | 事件几何字段与截图维度一致 | `TestStageExecutionEventTargetGeometryRoundTripAndValidation`, `TestDirectArtifactViewportMatchesWhenDimensionsAreDeclared` |
| selector repair 审计 | 原/候选 selector、证据和命中数不完整或包含敏感文本时拒绝 | `TestReplayManifestRejectsIncompleteSelectorRepairAudit`, `TestReplayManifestRejectsSensitiveSelectorRepairAudit` |
| 报告/回放一致性 | 内联报告必须绑定本次上传的 ReplayManifest、阶段和证据 | `TestFormalResultArtifactContentsRejectValidationRunReportForAnotherReplayManifest` |

建议执行：

```bash
cd backend
go test ./internal/model ./internal/app ./internal/orchestrator ./internal/directtransport ./internal/executor -count=1
```
