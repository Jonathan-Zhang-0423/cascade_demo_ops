# App 正式执行包生成阻塞说明（2026-08-02）

## 结论

最新主线仍无法完成真实 App -> Server 验收。阻塞发生在 App 本地分析的 `ScriptPackage`，正式 Exchange 上传尚未开始，Server Intake 未收到包。

本次复现错误：

```text
runtime_page_evidence_missing: stage "business_stage_session_setup"
cannot rely on source or generic wait conditions to prove a runtime page state
```

`bundle_hash_mismatch` 已由 `v2-semantic` bundle hash 修复；本次没有再次出现。

## 可重复测试

```powershell
cd D:\Engine-7-8\backend
$env:GOCACHE='D:\Engine-7-8\.tmp-go-build-cache'
$env:GOMODCACHE='D:\Engine-7-8\.tmp-go-modcache'
go test -count=1 ./internal/app -run '^TestTwoStepLoginScanBindsRuntimeEvidenceAndBuildsDraft$'
```

该测试使用真实 video-worker/Chromium 两步登录扫描。扫描已经生成 `browser_scan_id` 和两个 verified actions，但 `ScriptPackage` 仍在 `business_stage_session_setup` 被一致性校验阻断。

## 根因定位

1. `BusinessStagePlan` 在页面预扫描前生成。
2. `PageInteractionVerifierAgent` 登录成功后，向 `VerifiedInteractionPlan` 写入 `EvidenceKindBrowserScan` 登录证据。
3. 登录证据没有回写/绑定到既有 `BusinessStagePlan.Stages[].EvidenceRefs` 中的 `session_setup` Stage。
4. `GraphBuilderAgent.graphNodeFromBusinessStage` 只从 `BusinessStage.EvidenceRefs` 生成 Graph Node、StateAfter 和 Validation 的 evidence refs。
5. `ScriptPackager` 因此仍只看到来源文档或通用等待条件，`ValidateBrowserAgentOutlineConsistency` 正确返回 `runtime_page_evidence_missing`。

相关位置：

- 页面扫描登录证据生成：`backend/internal/agents/page_interaction_verifier.go`，`verifiedPlanFromScanResults`。
- Stage -> Graph 证据复制：`backend/internal/agents/graph_builder.go`，`graphNodeFromBusinessStage`。
- 阻断规则：`backend/internal/model/outline_validation.go`，`stepHasRuntimePageEvidence`。
- 失败回归：`backend/internal/app/assistant_service_test.go`，`TestTwoStepLoginScanBindsRuntimeEvidenceAndBuildsDraft`。

## App 侧最小修复要求

在页面预扫描完成后、GraphGenerate 前，增加确定性的证据绑定步骤：

1. 将已验证登录动作的 BrowserScan evidence 绑定到对应 `session_setup` Stage。
2. 将扫描确认的最终 URL/工作台状态编译为具体 required validation，例如 `url_matches`、`element_visible`、`text_contains` 或 `page_title_contains`。
3. 只允许按 stage ID、intent goal ID、route state 或明确语义匹配绑定，禁止把任意页面扫描证据全局复制给所有 Stage。
4. 若登录扫描没有确认离开登录页，不得生成“已进入工作台”的成功状态。
5. 凭据仍只通过 `credential://demo/...` / `secret_ref` 使用，不得写入状态、执行包、日志或截图元数据。

不要放宽 `runtime_page_evidence_missing` 校验器。该校验器正在正确阻止无真实页面证据的包进入 Server。

## 修复验收条件

必须同时满足：

- `TestTwoStepLoginScanBindsRuntimeEvidenceAndBuildsDraft` 通过；
- 扫描最终 URL 已离开 `/login`；
- `session_setup` 包含 BrowserScan/BrowserTrace 等运行时证据；
- `session_setup` 至少有一项具体 required 页面验证；
- 不出现 `runtime_page_evidence_missing`；
- 不出现 `bundle_hash_mismatch`；
- `BuildClientExecutionPackage` 成功生成 `browser-agent-outline-v1`；
- 密码不出现在序列化状态和执行包中。

## Server 侧当前验收事实

2026-08-02 已重新运行：

```powershell
go test -count=1 -v ./internal/app -run '^TestControlledOutlineScenarioPackagesRunCompleteServerPath$'
```

结果：成功场景与 locator 缺失、required validation 失败、构建未完成三类失败场景全部通过。该结果证明 Server 受控链路正常，但不能替代真实 App 正式包验收。

