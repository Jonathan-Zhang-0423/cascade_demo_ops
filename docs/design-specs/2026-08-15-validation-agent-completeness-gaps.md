# Validation Agent 完整性缺口补全设计文档

日期：2026-08-15  
对应工作包：工作包 F（Validation Agent）  
前置调查：agent 调查报告（2026-08-13，9 项缺口审计）

---

## 一、目标与背景

### 1.1 目标

补全 Validation Agent（工作包F）的 4 个已确认缺口，使问题发现更完整、分类更精准、失败原因可追溯：

1. **App 包业务动作完整性检查未接入实时路径**：`browserAgentReadiness()` 能检测"缺少 project_idea 填写"等 App Outline 缺陷，但只在 Exchange 路径调用，Direct 路径和 `ValidateBeforeExecution` 都未调用
2. **ValidationCheck 结构性增强**：责任域缺"媒体交付失败"分类、缺 ArtifactID 引用、"URL 改变≠业务完成"和"禁止操作违规"缺专属稳定码
3. **Validation finding 与失败结果分类未打通**：失败结果错误码从 Go error 提取，未利用 ValidationReport 的语义化 finding code
4. **`browser_evidence_unavailable` 标记缺失**：基础设施失败（Chromium 启动失败等）无明确标记区分"有浏览器证据的业务失败"

### 1.2 背景与证据

**实锤证据**：
- 两个独立生成的正式 App 包（tetris 项目）均缺少必需的 `project_idea` 填写 stage（`project_idea_fill_present=false`），Server 正确地未执行它们（fail-closed），但该缺陷由离线审计脚本发现，非 Validation Agent 实时路径捕获
- 现有 `browserAgentReadiness()` 函数（`internal/app/browser_agent_readiness.go`）包含 `business_input_missing`、`business_input_action_missing`、`declared_business_input_action_missing` 三个检查，能够检测此类缺陷，但该函数仅在 Exchange 路径（`exchange_runner.go:21`）被调用，Direct 路径（`direct_worker_runner.go`）和 `ValidateBeforeExecution` 均未调用

**架构事实**：
- 一凯（yikai）本周已实现完整 Direct API Gateway（commits `3aea8a7`、`2af8f39`），Direct 路径通过 `executionRuntimeRouter` → `localBrowserAgentOutlineRunner` 复用同一套三阶段验证（`ValidateBeforeExecution` / `ValidateStageEvents` / `ValidatePostExecution`），改 `ValidateBeforeExecution` 后 Direct 路径自动生效
- `ValidationReport` 已有 `SourcePackageID` 和 `RunID` 字段，`ValidationCheck` 已有 `NodeID` 和 `StageID` 字段，满足孟洋要求的"package/job/stage/node/artifact 引用"中的前 4 项

### 1.3 非目标（本轮不做）

- **跨 run 回归追踪**：工作量明显更大（需历史存储和比较逻辑），放到后续 spec 周期
- **ReplayManifest 脱敏机制补全**：安全向独立需求，放到后续 spec 周期
- **selector provenance 校验**：已在 `validateDirectSelectorProvenance()`（`exchange_validation.go:187-260`）实现，无缺口
- **审批摘要/来源绑定校验**：已在 `ValidateClientExecutionPackageForDirectExecution()`（`exchange_validation.go:109-174`）实现，无缺口

---

## 二、整体架构

### 2.1 实施分阶段

**Phase 1 - 数据结构与检查能力增强**：
- ValidationCheck 结构扩展：新增 `ArtifactID` 字段 + 新增 `media_delivery` 责任域 + 新增 4 个稳定码
- 接入 `browserAgentReadiness` 检查：转成 ValidationCheck 注入 `ValidateBeforeExecution`
- `browser_evidence_unavailable` 标记：在 `ScriptFailureDiagnostic` 加 bool 字段，基础设施失败路径设置

**Phase 2 - 失败结果与 Validation 打通**：
- `directRuntimeFailureResult` 改成从 ValidationReport 取第一个 blocking check 的 code
- 在 `ScriptFailureDiagnostic` 附上完整 `ValidationReports` 数组供深度追溯

### 2.2 职责边界

**改动范围**：
- `internal/model/browser_agent_runtime.go` - ValidationCheck 结构、ValidationCheckDomain 枚举
- `internal/model/browser_agent_validation_annotations.go` - validationCheckMetaTable 新增 4 条
- `internal/model/exchange.go` - ScriptFailureDiagnostic 结构
- `internal/app/browser_agent_outline_runner.go` - ValidateBeforeExecution 接入 readiness、转换函数
- `internal/app/direct_failed_result.go` - 失败结果错误码提取逻辑

**不改动**：
- Browser Agent 执行逻辑（`internal/orchestrator/browser_agent_orchestrator.go`）
- Gateway / Worker / 录屏 / 视频编辑器
- App 侧代码或 Outline 生成规则

### 2.3 向后兼容性

- ValidationCheck 新增字段可选，旧代码不填不影响解析
- 失败结果错误码从 Go error 改成 validation code 是语义增强，格式不变（都是字符串）
- ScriptFailureDiagnostic 新增字段可选，旧调用方忽略即可

---

## 三、Phase 1 详细设计

### 3.1 ValidationCheck 结构扩展

#### 3.1.1 新增字段

**文件**：`internal/model/browser_agent_runtime.go`

```go
type ValidationCheck struct {
    ID                   string                `json:"id"`
    Kind                 string                `json:"kind"`
    Code                 string                `json:"code"`
    NodeID               string                `json:"node_id,omitempty"`
    StageID              string                `json:"stage_id,omitempty"`
    Severity             FindingSeverity       `json:"severity"`
    Passed               bool                  `json:"passed"`
    Required             bool                  `json:"required"`
    Summary              string                `json:"summary"`
    EvidenceRefs         []string              `json:"evidence_refs,omitempty"`
    Impact               string                `json:"impact,omitempty"`
    Suggestion           string                `json:"suggestion,omitempty"`
    NextStep             string                `json:"next_step,omitempty"`
    ResponsibilityDomain ValidationCheckDomain `json:"responsibility_domain,omitempty"`
    ArtifactID           string                `json:"artifact_id,omitempty"`  // 新增
}
```

**填充规则**：
- 只在 check 与特定 artifact 绑定时填充（如 `MISSING_MP4_VIDEO` 填对应 mp4 的 artifact ID、`EVIDENCE_ARTIFACT_REFERENCE_BROKEN` 填损坏引用的 artifact ID）
- 其他 check（如 `check_approved_hashes`、`business_input_missing`）留空
- PackageID/JobID 不加字段，通过 `ValidationReport.SourcePackageID` 和 `ValidationReport.RunID` 追溯

**追溯路径映射**（满足孟洋要求的"package/job/stage/node/artifact 引用"）：
- package → `ValidationReport.SourcePackageID`（已有）
- job → `ValidationReport.RunID`（已有，Direct 路径的 JobID 即 RunID）
- stage/node → `ValidationCheck.StageID + NodeID`（已有）
- artifact → `ValidationCheck.ArtifactID`（新增，按语义填充）

#### 3.1.2 ValidationCheckDomain 新增枚举值

**文件**：`internal/model/browser_agent_runtime.go`

```go
type ValidationCheckDomain string

const (
    ValidationCheckDomainApp            ValidationCheckDomain = "app"
    ValidationCheckDomainServer         ValidationCheckDomain = "server"
    ValidationCheckDomainValidation     ValidationCheckDomain = "validation"
    ValidationCheckDomainEnvironment    ValidationCheckDomain = "environment"
    ValidationCheckDomainMediaDelivery  ValidationCheckDomain = "media_delivery"  // 新增
)
```

**用途**：标记视频编辑、MP4 生成、artifact 上传失败等媒体交付问题，满足孟洋要求的"区分...媒体交付失败"（5-way 分类）。

### 3.2 新增 4 个稳定码

**文件**：`internal/model/browser_agent_validation_annotations.go`

在 `validationCheckMetaTable` 新增：

#### 3.2.1 url_change_not_business_completion

```go
"url_change_not_business_completion": {
    Impact:               "仅 URL 改变不能证明业务动作完成，可能是跳转到错误页或中间页",
    Suggestion:           "检查 post-action assertion 是否验证了实际业务状态（如表单提交后的成功提示、新记录出现等）",
    NextStep:             "审查 Outline 中该 stage 的 expected_outcome 定义，确保包含业务状态验证而非仅 URL 匹配",
    ResponsibilityDomain: ValidationCheckDomainApp,
}
```

- **Severity**: `warning`（不阻断执行，但提醒可能存在验证不充分）
- **触发场景**：post-action observation 仅包含 URL 改变，缺少业务状态断言

#### 3.2.2 forbidden_operation_attempted

```go
"forbidden_operation_attempted": {
    Impact:               "尝试执行合约明确禁止的操作，违反 BrowserAgentContract 约束",
    Suggestion:           "检查 BrowserAgentContract.forbidden_operations 与 Outline 动作定义是否冲突",
    NextStep:             "修改 Outline 移除禁止操作，或调整 BrowserAgentContract 放宽限制",
    ResponsibilityDomain: ValidationCheckDomainApp,
}
```

- **Severity**: `blocking`（违反合约约束，必须停止）
- **触发场景**：runtime 执行时 policy guard 拦截到禁止操作（如 navigate 到不在 allowed_domains 的域、file download 等）

#### 3.2.3 media_artifact_generation_failed

```go
"media_artifact_generation_failed": {
    Impact:               "视频或截图生成失败，不影响执行结果准确性但缺少演示素材",
    Suggestion:           "检查 FFmpeg 可用性、磁盘空间和录屏原始数据完整性",
    NextStep:             "查看 video_worker 日志（stderr）和录屏目录权限",
    ResponsibilityDomain: ValidationCheckDomainMediaDelivery,
}
```

- **Severity**: `warning`（不影响执行准确性，但用户体验受损）
- **触发场景**：video worker 编辑失败、FFmpeg 崩溃、录屏文件损坏

#### 3.2.4 media_artifact_upload_failed

```go
"media_artifact_upload_failed": {
    Impact:               "artifact 已成功生成但上传到存储（S3/OSS）失败，本地可访问但无法分享",
    Suggestion:           "检查存储凭证（AccessKey/SecretKey）、网络连通性和存储桶权限",
    NextStep:             "查看 artifact uploader 日志和 S3/OSS API 返回的错误码",
    ResponsibilityDomain: ValidationCheckDomainMediaDelivery,
}
```

- **Severity**: `warning`（artifact 已生成，仅分发受阻）
- **触发场景**：S3 PutObject 失败、OSS 签名错误、网络超时

### 3.3 接入 browserAgentReadiness 检查

#### 3.3.1 转换函数

**文件**：`internal/app/browser_agent_outline_runner.go`

新增函数：

```go
// convertReadinessToValidationChecks 将 browserAgentReadiness 的 finding
// 转换为 ValidationCheck，统一标记责任域为 app（当前所有 readiness 检查
// 均针对 App Outline 结构和业务动作完整性）。
func convertReadinessToValidationChecks(
    findings []BrowserAgentReadinessFinding,
    packageID string,
) []model.ValidationCheck {
    checks := make([]model.ValidationCheck, 0, len(findings))
    for _, f := range findings {
        severity := model.FindingSeverityWarning
        required := false
        if f.Level == "blocker" {
            severity = model.FindingSeverityBlocking
            required = true
        }
        checks = append(checks, model.ValidationCheck{
            ID:                   fmt.Sprintf("readiness_%s_%d", f.Kind, time.Now().UnixNano()),
            Kind:                 "readiness",
            Code:                 f.Kind,  // business_input_missing, business_input_action_missing, declared_business_input_action_missing
            Severity:             severity,
            Passed:               false,
            Required:             required,
            Summary:              f.Message,
            ResponsibilityDomain: model.ValidationCheckDomainApp,
            // NodeID/StageID: readiness 是全局 Outline 检查，无具体 stage 绑定，留空
        })
    }
    return checks
}
```

#### 3.3.2 注入点

**文件**：`internal/app/browser_agent_outline_runner.go`

在 `ValidateBeforeExecution()` 的现有 contract/hash/stage-count 检查之后，调用 `browserAgentReadiness()` 并转换结果：

```go
func (r *localBrowserAgentOutlineRunner) ValidateBeforeExecution(
    ctx context.Context,
    vctx model.BrowserAgentValidationContext,
    approvedPlan model.ApprovedStageExecutionPlan,
) (report model.ValidationReport, err error) {
    defer func() { model.AnnotateValidationChecks(report.Checks) }()
    
    checks := []model.ValidationCheck{}
    
    // === 现有检查：contract/hash/stage-count（保持不变）===
    if req.Package.BrowserAgentContract == nil {
        checks = append(checks, model.ValidationCheck{
            Code: "check_approved_outline_contract",
            // ...
        })
    }
    // ... hash/stage-count 检查 ...
    
    // === 新增：调用 readiness 检查 ===
    readinessFindings := browserAgentReadiness(req.Package.BrowserAgentContract, &approvedPlan)
    readinessChecks := convertReadinessToValidationChecks(readinessFindings, req.Package.PackageID)
    checks = append(checks, readinessChecks...)
    
    // === 决策逻辑（保持不变）===
    decision := model.ValidationDecisionContinue
    for _, c := range checks {
        if !c.Passed && c.Severity == model.FindingSeverityBlocking {
            decision = model.ValidationDecisionStopAndReport
            break
        }
    }
    
    report = model.ValidationReport{
        ReportID:          fmt.Sprintf("pre_exec_%s", req.Package.PackageID),
        Phase:             model.ValidationPhasePreExecution,
        Decision:          decision,
        Checks:            checks,
        SourcePackageID:   req.Package.PackageID,
        RunID:             approvedPlan.RunID,
        Timestamp:         time.Now(),
    }
    
    return report, nil
}
```

#### 3.3.3 Direct 路径同步生效

**无需单独改动 Direct 路径代码**。原因：

1. Direct 路径入口 `direct_worker_runner.go:82-84` 调用 `executionRuntimeRouter.Run()`
2. Router（`exchange_runtime_router.go:79-96`）针对 `ExecutableScriptRuntimeBrowserAgentOutlineV1` 委托给 `r.outline.Run()`
3. `localBrowserAgentOutlineRunner.Run()`（`browser_agent_outline_runner.go:59-73`）调用 `ValidateBeforeExecution()`

因此改 `ValidateBeforeExecution()` 后，Direct 和 Exchange 两条路径自动同步生效。

#### 3.3.4 效果

之前绕过验证的"缺少 project_idea 填写"等 App Outline 缺陷，现在会在 pre-execution 阶段被捕获：
- 生成 `Code: "business_input_missing"`, `Severity: blocking`, `ResponsibilityDomain: app` 的 ValidationCheck
- `ValidationDecision` 变为 `stop_and_report`
- 执行停止，`RecordingResultPackage.Status` 为 `failed`
- ReplayManifest 的 `validation_reports` 数组包含该 pre-execution report

### 3.4 browser_evidence_unavailable 标记

#### 3.4.1 ScriptFailureDiagnostic 新增字段

**文件**：`internal/model/exchange.go`

```go
type ScriptFailureDiagnostic struct {
    Summary                    string    `json:"summary"`
    FailedNodeID               string    `json:"failed_node_id,omitempty"`
    RedactionReport            *DiagnosticRedactionReport `json:"redaction_report,omitempty"`
    BrowserEvidenceUnavailable bool      `json:"browser_evidence_unavailable,omitempty"`  // 新增
}
```

**语义**：
- `true`：失败发生在浏览器会话建立之前，没有任何页面截图/trace/observation（基础设施失败）
- `false` 或字段缺失：有浏览器证据（即使执行失败，也有部分截图/observation/事件）

#### 3.4.2 设置逻辑

**文件**：`internal/app/direct_failed_result.go`

在 `directRuntimeFailureResult()` 里，判断错误码是否属于基础设施失败：

```go
func (s *DirectHTTPServer) directRuntimeFailureResult(
    ctx context.Context, pkg *model.ClientExecutionPackage, jobID string,
    recordingDir string, partialResult model.RecordingResultPackage,
    runtimeErr error, createdAt time.Time,
) (model.RecordingResultPackage, error) {
    
    code := runtimeExecutionErrorCode(runtimeErr)
    browserEvidenceUnavailable := isInfrastructureFailure(code)
    
    diagnostic := model.ScriptFailureDiagnostic{
        Summary:                    fmt.Sprintf("Server runtime failed: %s", code),
        FailedNodeID:               extractFailedNodeID(runtimeErr),
        BrowserEvidenceUnavailable: browserEvidenceUnavailable,
        RedactionReport: &model.DiagnosticRedactionReport{
            Applied:          true,
            FullHTMLIncluded: false,
        },
    }
    
    result := partialResult
    result.Status = model.RecordingResultStatusFailed
    result.FailureDiagnostic = &diagnostic
    result.Error = &model.RecordingError{Code: code, Message: diagnostic.Summary}
    
    // ... artifact 清理逻辑 ...
    
    return result, nil
}
```

#### 3.4.3 基础设施失败码判定

**文件**：`internal/app/direct_failed_result.go`

新增辅助函数：

```go
// isInfrastructureFailure 判断错误码是否属于基础设施失败（无浏览器证据）。
// 基础设施失败包括：Chromium 启动失败、Playwright 初始化失败、video worker
// 缺失、Node.js runtime 缺失、浏览器会话崩溃等——这些失败发生在浏览器会话
// 建立之前或会话启动失败，不会产生任何页面截图、trace 或 observation 证据。
func isInfrastructureFailure(code string) bool {
    infrastructureCodes := map[string]bool{
        "browser_agent_outline_runner_unavailable": true,
        "video_worker_missing":                     true,
        "node_runtime_missing":                     true,
        "browser_agent_session_start_failed":       true,
        "browser_agent_session_crashed":            true,
        "browser_agent_trace_unavailable":          true,
        "chromium_launch_failed":                   true,
        "playwright_initialization_failed":         true,
        "recording_worker_initialization_failed":   true,
        "browser_context_creation_failed":          true,
    }
    return infrastructureCodes[code]
}
```

#### 3.4.4 效果

- Chromium 启动失败 → `BrowserEvidenceUnavailable: true`，用户/调试者知道"无法查看浏览器截图/trace，因为浏览器根本没启动"
- Stage 动作执行失败但有部分截图 → `BrowserEvidenceUnavailable: false` 或字段缺失，用户可查看 ReplayManifest 引用的 artifact

---

## 四、Phase 2 详细设计

### 4.1 ScriptFailureDiagnostic 再次扩展

**文件**：`internal/model/exchange.go`

```go
type ScriptFailureDiagnostic struct {
    Summary                    string              `json:"summary"`
    FailedNodeID               string              `json:"failed_node_id,omitempty"`
    RedactionReport            *DiagnosticRedactionReport `json:"redaction_report,omitempty"`
    BrowserEvidenceUnavailable bool                `json:"browser_evidence_unavailable,omitempty"`  // Phase 1
    ValidationReports          []ValidationReport  `json:"validation_reports,omitempty"`            // Phase 2 新增
}
```

**用途**：附上所有包含 blocking check 的 ValidationReport，供深度追溯（如 UI 展示详细的 validation 发现、多阶段失败原因分析）。

### 4.2 directRuntimeFailureResult 改造

**文件**：`internal/app/direct_failed_result.go`

```go
func (s *DirectHTTPServer) directRuntimeFailureResult(
    ctx context.Context, pkg *model.ClientExecutionPackage, jobID string,
    recordingDir string, partialResult model.RecordingResultPackage,
    runtimeErr error, createdAt time.Time,
) (model.RecordingResultPackage, error) {
    
    // 1. 尝试从 partialResult 的 ValidationReports 提取第一个 blocking check code
    code := extractFirstBlockingValidationCode(partialResult.ValidationReports)
    
    // 2. 如果没有 blocking validation check，fallback 到 Go error code
    if code == "" {
        code = runtimeExecutionErrorCode(runtimeErr)
    }
    
    // 3. 过滤出所有包含 blocking check 的 ValidationReport
    blockingReports := filterBlockingReports(partialResult.ValidationReports)
    
    // 4. 构造诊断
    browserEvidenceUnavailable := isInfrastructureFailure(code)
    diagnostic := model.ScriptFailureDiagnostic{
        Summary:                    generateFailureSummary(code, blockingReports),
        FailedNodeID:               extractFailedNodeIDFromReportsOrError(blockingReports, runtimeErr),
        BrowserEvidenceUnavailable: browserEvidenceUnavailable,
        ValidationReports:          blockingReports,  // Phase 2 新增
        RedactionReport: &model.DiagnosticRedactionReport{
            Applied:          true,
            FullHTMLIncluded: false,
        },
    }
    
    result := partialResult
    result.Status = model.RecordingResultStatusFailed
    result.FailureDiagnostic = &diagnostic
    result.Error = &model.RecordingError{Code: code, Message: diagnostic.Summary}
    
    // ... artifact 清理逻辑（保持不变）...
    
    return result, nil
}
```

### 4.3 辅助函数

**文件**：`internal/app/direct_failed_result.go`

#### 4.3.1 extractFirstBlockingValidationCode

```go
// extractFirstBlockingValidationCode 从 ValidationReports 提取第一个 blocking
// check 的 code。遍历顺序：pre-execution → runtime (按 stage 顺序) → post-execution。
// 如果没有 blocking check，返回空字符串。
func extractFirstBlockingValidationCode(reports []model.ValidationReport) string {
    // 按 phase 顺序排序（pre < runtime < post）
    sortedReports := sortReportsByPhase(reports)
    
    for _, rpt := range sortedReports {
        for _, chk := range rpt.Checks {
            if !chk.Passed && chk.Severity == model.FindingSeverityBlocking {
                return chk.Code
            }
        }
    }
    return ""
}

// sortReportsByPhase 将 ValidationReport 按阶段排序（pre → runtime → post）。
func sortReportsByPhase(reports []model.ValidationReport) []model.ValidationReport {
    sorted := make([]model.ValidationReport, len(reports))
    copy(sorted, reports)
    
    phaseOrder := map[model.ValidationPhase]int{
        model.ValidationPhasePreExecution:  1,
        model.ValidationPhaseRuntime:       2,
        model.ValidationPhasePostExecution: 3,
    }
    
    sort.Slice(sorted, func(i, j int) bool {
        return phaseOrder[sorted[i].Phase] < phaseOrder[sorted[j].Phase]
    })
    
    return sorted
}
```

#### 4.3.2 filterBlockingReports

```go
// filterBlockingReports 过滤出所有包含至少一个 blocking check 的 ValidationReport。
func filterBlockingReports(reports []model.ValidationReport) []model.ValidationReport {
    blocking := []model.ValidationReport{}
    for _, rpt := range reports {
        hasBlocking := false
        for _, chk := range rpt.Checks {
            if !chk.Passed && chk.Severity == model.FindingSeverityBlocking {
                hasBlocking = true
                break
            }
        }
        if hasBlocking {
            blocking = append(blocking, rpt)
        }
    }
    return blocking
}
```

#### 4.3.3 generateFailureSummary

```go
// generateFailureSummary 根据错误码和 blocking reports 生成诊断摘要。
func generateFailureSummary(code string, blockingReports []model.ValidationReport) string {
    if len(blockingReports) == 0 {
        return fmt.Sprintf("Server runtime failed: %s", code)
    }
    
    // 统计 blocking check 数量和责任域分布
    blockingCount := 0
    domainCounts := make(map[model.ValidationCheckDomain]int)
    for _, rpt := range blockingReports {
        for _, chk := range rpt.Checks {
            if !chk.Passed && chk.Severity == model.FindingSeverityBlocking {
                blockingCount++
                domainCounts[chk.ResponsibilityDomain]++
            }
        }
    }
    
    return fmt.Sprintf("Validation failed: %s (%d blocking checks across %d domains)",
        code, blockingCount, len(domainCounts))
}
```

#### 4.3.4 extractFailedNodeIDFromReportsOrError

```go
// extractFailedNodeIDFromReportsOrError 从 blocking reports 或 Go error 提取失败的 NodeID。
// 优先从 ValidationReport 的第一个 blocking check 取 NodeID，如果没有则从 Go error 提取。
func extractFailedNodeIDFromReportsOrError(
    blockingReports []model.ValidationReport,
    runtimeErr error,
) string {
    for _, rpt := range blockingReports {
        for _, chk := range rpt.Checks {
            if !chk.Passed && chk.Severity == model.FindingSeverityBlocking && chk.NodeID != "" {
                return chk.NodeID
            }
        }
        // Fallback: report 层级的 NodeID（runtime phase 的 report 有）
        if rpt.NodeID != "" {
            return rpt.NodeID
        }
    }
    
    // 最终 fallback: 从 Go error 提取（保持现有逻辑）
    return extractFailedNodeID(runtimeErr)
}
```

### 4.4 效果

**失败结果错误码变化**：

| 场景 | Phase 1 错误码 | Phase 2 错误码 |
|------|---------------|---------------|
| App 包缺少 business_input stage | `runtime_error` | `business_input_missing` |
| Pre-execution hash 不匹配 | `runtime_error` | `check_approved_hashes` |
| Runtime assertion 失败 | `runtime_error` | `runtime_assertion_failed` |
| Chromium 启动失败（无 validation finding）| `chromium_launch_failed` | `chromium_launch_failed`（fallback 到 Go error）|

**ScriptFailureDiagnostic 完整示例**（App 包缺陷导致 pre-execution 失败）：

```json
{
  "summary": "Validation failed: business_input_missing (1 blocking checks across 1 domains)",
  "failed_node_id": "",
  "browser_evidence_unavailable": false,
  "validation_reports": [
    {
      "report_id": "pre_exec_pkg_abc123",
      "phase": "pre_execution",
      "decision": "stop_and_report",
      "checks": [
        {
          "id": "readiness_business_input_missing_1723789012345",
          "kind": "readiness",
          "code": "business_input_missing",
          "severity": "blocking",
          "passed": false,
          "required": true,
          "summary": "Stage 'project_idea_fill' (kind=business_input) has no input value",
          "responsibility_domain": "app",
          "impact": "业务输入缺失导致执行无法完成关键业务动作",
          "suggestion": "检查 Outline 中 business_input stage 的 input 字段是否已填充",
          "next_step": "修改 Outline 或重新生成包，确保所有 business_input stage 有明确的输入值"
        }
      ],
      "source_package_id": "pkg_abc123",
      "run_id": "run_xyz789",
      "timestamp": "2026-08-15T10:30:45Z"
    }
  ],
  "redaction_report": {
    "applied": true,
    "full_html_included": false
  }
}
```

---

## 五、测试策略

### 5.1 Phase 1 测试

#### 5.1.1 ValidationCheck 字段扩展

**单元测试**：`internal/model/browser_agent_runtime_test.go`

测试用例：
1. ValidationCheck JSON 序列化/反序列化包含新字段 `ArtifactID`
2. `ValidationCheckDomainMediaDelivery` 常量值正确
3. 新增的 4 个稳定码在 `validationCheckMetaTable` 中存在且字段完整

#### 5.1.2 browserAgentReadiness 接入

**集成测试**：`internal/app/browser_agent_outline_runner_test.go`

测试场景：
1. **正常包通过**：包含完整 business_input stage 的包，`ValidateBeforeExecution` 返回 `decision=continue`，无 readiness finding
2. **缺少 business_input stage**：模拟缺少 `project_idea` 填写 stage 的包（复现实锤证据场景），`ValidateBeforeExecution` 返回 `decision=stop_and_report`，checks 包含 `code=business_input_missing`, `severity=blocking`, `responsibility_domain=app`
3. **business_input stage 无 input 值**：stage 存在但 input 字段为空，触发 `business_input_missing`
4. **business_input stage 动作不是 fill/select**：stage 是 business_input 类型但动作是 click，触发 `business_input_action_missing`

#### 5.1.3 browser_evidence_unavailable 标记

**单元测试**：`internal/app/direct_failed_result_test.go`

测试场景：
1. **基础设施失败码**：`chromium_launch_failed` / `browser_agent_session_start_failed` 等 → `BrowserEvidenceUnavailable: true`
2. **业务失败码**：`runtime_assertion_failed` / `business_input_missing` 等 → `BrowserEvidenceUnavailable: false`
3. **未知错误码** → `BrowserEvidenceUnavailable: false`（保守处理，假设可能有证据）

### 5.2 Phase 2 测试

#### 5.2.1 错误码提取

**单元测试**：`internal/app/direct_failed_result_test.go`

测试场景：
1. **Pre-execution blocking check**：`partialResult.ValidationReports` 包含 pre-execution report 且有 blocking check → 错误码为该 check 的 code
2. **Runtime blocking check**：runtime report 有 blocking check → 错误码为第一个 runtime blocking check 的 code
3. **多阶段 blocking check**：pre 和 runtime 都有 blocking check → 错误码为 pre 的（阶段排序优先）
4. **仅 warning check**：所有 check 都是 warning → fallback 到 Go error code
5. **无 ValidationReport**：`partialResult.ValidationReports` 为空 → fallback 到 Go error code

#### 5.2.2 ValidationReports 附加

**集成测试**：`internal/app/direct_failed_result_test.go`

测试场景：
1. **有 blocking report**：`ScriptFailureDiagnostic.ValidationReports` 包含所有 blocking report，按 phase 排序
2. **仅 warning report**：`ScriptFailureDiagnostic.ValidationReports` 为空数组（不附加 non-blocking report）
3. **混合 blocking 和 warning report**：仅附加 blocking 的

### 5.3 端到端验证

#### 5.3.1 复现实锤证据场景

**手工测试/受控场景包**：

1. 构造一个缺少 `project_idea` 填写 stage 的正式 App 包（复制现有 evidence 中的包结构）
2. 通过 Direct API 提交该包
3. 预期结果：
   - `RecordingResultPackage.Status = failed`
   - `RecordingError.Code = "business_input_missing"`
   - `ScriptFailureDiagnostic.BrowserEvidenceUnavailable = false`（pre-execution 失败，但不是基础设施失败）
   - `ScriptFailureDiagnostic.ValidationReports` 包含 1 个 pre-execution report，checks 包含 `code=business_input_missing`
   - ReplayManifest 的 `validation_reports` 数组包含该 report

#### 5.3.2 基础设施失败场景

**手工测试**（需 Chromium 不可用环境）：

1. 关闭 Chromium 或破坏 Playwright 安装
2. 提交正常包
3. 预期结果：
   - `RecordingError.Code = "chromium_launch_failed"` 或 `"browser_agent_session_start_failed"`
   - `ScriptFailureDiagnostic.BrowserEvidenceUnavailable = true`
   - `ScriptFailureDiagnostic.ValidationReports` 为空（pre-execution 通过，runtime 启动即失败，无 validation finding）

---

## 六、交付清单

### 6.1 Phase 1 交付物

1. **代码改动**：
   - `internal/model/browser_agent_runtime.go` - ValidationCheck 新增 `ArtifactID` 字段 + `ValidationCheckDomainMediaDelivery` 枚举值
   - `internal/model/browser_agent_validation_annotations.go` - validationCheckMetaTable 新增 4 条（url_change_not_business_completion / forbidden_operation_attempted / media_artifact_generation_failed / media_artifact_upload_failed）
   - `internal/model/exchange.go` - ScriptFailureDiagnostic 新增 `BrowserEvidenceUnavailable` 字段
   - `internal/app/browser_agent_outline_runner.go` - `ValidateBeforeExecution` 接入 `browserAgentReadiness` + `convertReadinessToValidationChecks` 函数
   - `internal/app/direct_failed_result.go` - `directRuntimeFailureResult` 设置 `BrowserEvidenceUnavailable` + `isInfrastructureFailure` 函数

2. **测试代码**：
   - `internal/model/browser_agent_runtime_test.go` - ValidationCheck 新字段序列化测试 + 新枚举值测试 + validationCheckMetaTable 完整性测试
   - `internal/app/browser_agent_outline_runner_test.go` - readiness 接入的 4 个集成测试场景
   - `internal/app/direct_failed_result_test.go` - `isInfrastructureFailure` 的 3 个单元测试场景

3. **文档**：
   - 更新 `docs/browser-agent-outcome-verifier-rules-v1.md` §失败码表，新增 4 个稳定码及其 Impact/Suggestion/NextStep/ResponsibilityDomain
   - 更新 `docs/browser-agent-outcome-verifier-rules-v1.md` §责任域分类，明确 `media_delivery` 的定义和典型场景

### 6.2 Phase 2 交付物

1. **代码改动**：
   - `internal/model/exchange.go` - ScriptFailureDiagnostic 新增 `ValidationReports` 字段
   - `internal/app/direct_failed_result.go` - `directRuntimeFailureResult` 改造（错误码提取、blocking reports 过滤、诊断生成）+ 5 个辅助函数（extractFirstBlockingValidationCode / sortReportsByPhase / filterBlockingReports / generateFailureSummary / extractFailedNodeIDFromReportsOrError）

2. **测试代码**：
   - `internal/app/direct_failed_result_test.go` - 错误码提取的 5 个单元测试场景 + ValidationReports 附加的 3 个集成测试场景

3. **文档**：
   - 更新 `docs/handoff/validation-agent-server-round-delivery-2026-08-08.md`（如果该文件是当前轮次的交付说明，则新建 `docs/handoff/validation-agent-completeness-gaps-delivery-2026-08-15.md`）
   - 示例：ScriptFailureDiagnostic 的完整 JSON 结构（包含 ValidationReports 数组）
   - 示例：validation-driven 错误码 vs Go error fallback 的典型场景对比表

### 6.3 端到端验收

1. **复现实锤证据场景**（Phase 1 验收标准）：
   - 缺少 `project_idea` 填写 stage 的包 → pre-execution 失败，错误码 `business_input_missing`，`BrowserEvidenceUnavailable=false`

2. **Phase 2 错误码映射验收**：
   - Pre-execution hash 不匹配 → 错误码 `check_approved_hashes`（不再是 `runtime_error`）
   - Runtime assertion 失败 → 错误码 `runtime_assertion_failed`
   - Chromium 启动失败 → 错误码 `chromium_launch_failed`（fallback），`BrowserEvidenceUnavailable=true`

---

## 七、风险与依赖

### 7.1 风险

1. **`browserAgentReadiness` 现有 finding 遗漏场景**：当前 readiness 仅检查 business_input 完整性，可能有其他 App Outline 缺陷类型（如 selector 格式错误、禁止域）未覆盖
   - **缓解**：本轮先接入已有检查，后续根据实际 App 包缺陷案例逐步补充 readiness 规则

2. **Phase 2 错误码变化可能影响已有监控/告警**：如果 yikai/mengyang 的监控系统依赖特定错误码（如 `runtime_error`），Phase 2 改成 validation code 后可能触发误告警
   - **缓解**：Phase 2 部署前通知 yikai 检查监控配置；保留 Go error fallback 逻辑，基础设施失败仍用原错误码

3. **`isInfrastructureFailure` 判定表可能不完整**：新的基础设施失败码（如未来加的 GPU 初始化失败）需要手动加进判定表
   - **缓解**：在代码注释里明确"新增基础设施失败码时必须更新此函数"；考虑后续用错误码命名约定（如 `*_infrastructure_*` 后缀）自动判定

### 7.2 依赖

1. **无外部依赖**：所有改动在 cascade_demo_ops repo 内部完成
2. **无 App 侧改动依赖**：不依赖 App 修复 APP-001（selector 错误）或改变产包规则
3. **测试环境依赖**：端到端验收需要 Chromium 可用环境（本地或 CI）

---

## 八、后续工作（非本轮范围）

1. **跨 run 回归追踪**（工作包 F 遗留项）：
   - 需求：对重复执行保留 run 间关联，能够比较同一问题是否复现、修复或回归
   - 工作量：需设计 ValidationFinding 持久化存储（数据库或 S3）、run 间关联算法（按 package + check code 聚合）、回归检测逻辑
   - 建议：独立 spec 周期，与 P2.2（验收页面）一起做

2. **ReplayManifest 脱敏机制补全**（工作包 F 遗留项）：
   - 需求：ReplayManifest 在上传前显式脱敏（确保 observed_state、event log 不含敏感信息）
   - 当前状态：runtime observation 已在捕获时脱敏（不记录完整 HTML），但 ReplayManifest 构造时未显式验证脱敏完整性
   - 建议：独立 spec，涉及安全审计和脱敏策略文档化

3. **禁止操作违规的实时 ValidationCheck 生成**（本轮新增 `forbidden_operation_attempted` 码，但未实现触发逻辑）：
   - 当前状态：policy guard（`contractBrowserAgentPolicyGuard`）在 runtime 拦截禁止操作，但拦截结果未转成 ValidationCheck
   - 建议：在 `ValidateStageEvents` 里检查 policy guard 的拦截记录，生成对应 ValidationCheck

4. **"URL 改变≠业务完成"的自动检测**（本轮新增 `url_change_not_business_completion` 码，但未实现自动触发逻辑）：
   - 当前状态：该码在 validationCheckMetaTable 里定义，但 `ValidatePostExecution` 未实现"检测 post-action observation 仅有 URL 改变"的逻辑
   - 建议：在 `ValidatePostExecution` 里解析 `ObservedState` 和 assertion 结果，判断是否仅依赖 URL 匹配

---

**设计文档结束**
