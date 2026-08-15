# 统一验证框架集成设计文档

## 1. 背景与目标

### 1.1 问题陈述

工作包 F（失败结果包装）与工作包 E（Validation Agent）之间存在设计缺口：

1. **稳定错误码缺口**：4 个新增错误码（`url_change_not_business_completion`、`forbidden_operation_attempted`、`media_artifact_generation_failed`、`media_artifact_upload_failed`）在 `validationCheckMetaTable` 中缺失
2. **Readiness 检查未集成**：`browserAgentReadiness()` 函数产生的 blockers/warnings 未转换为 `ValidationCheck` 格式
3. **错误码来源不统一**：失败结果的 `RecordingError.Code` 从 Go error 提取，未优先使用 ValidationCheck.Code
4. **验证报告未嵌入诊断**：`ScriptFailureDiagnostic` 缺少 `ValidationReports` 字段，无法溯源失败根因

### 1.2 目标

- Phase 1: 补全稳定码 annotation，将 readiness 检查集成到 pre_execution 验证
- Phase 2: 设计运行时验证事件结构（`ValidationEventPayload`）
- Phase 3: 失败结果错误码优先从 `ValidationCheck.Code` 提取

---

## 2. 架构设计

### 2.1 验证层级结构

```
┌─────────────────────────────────────────────────────────────┐
│                   Validation Framework                       │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐     │
│  │ pre_execution│  │runtime_stage │  │media_delivery│     │
│  │   validation │  │  validation  │  │  validation  │     │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘     │
│         │                 │                  │              │
│         ├─ readiness      ├─ stage checks    ├─ artifact   │
│         ├─ contract       ├─ route verify    │   generation│
│         └─ hash binding   └─ action result   └─ upload     │
│                                                              │
└─────────────────────────────────────────────────────────────┘
           │                     │                  │
           ▼                     ▼                  ▼
    ┌─────────────────────────────────────────────────────┐
    │           ValidationReport + ValidationCheck         │
    │  Code │ Severity │ ResponsibilityDomain │ ArtifactID │
    └─────────────────────────────────────────────────────┘
                             │
                             ▼
              ┌──────────────────────────────┐
              │  ScriptFailureDiagnostic     │
              │  - Error.Code ← first blocking│
              │  - ValidationReports (embed) │
              └──────────────────────────────┘
```

### 2.2 数据流

1. **pre_execution**: `ValidateBeforeExecution()` → `browserAgentReadiness()` → `convertReadinessToValidationChecks()` → `ValidationReport`
2. **runtime_stage**: `HandleStageCompleted/Failed()` → stage-level checks → `ValidationEventPayload` (emit via ExchangeStreamWriter)
3. **media_delivery**: artifact worker → media checks → `ValidationEventPayload`
4. **failure mapping**: `directRuntimeFailureResult()` → `extractFirstBlockingValidationCode()` → `ScriptFailureDiagnostic.Error.Code`

---

## 3. 实现细节

### 3.1 Phase 1: 稳定码补全与 Readiness 集成

#### 3.1.1 ValidationCheck 结构扩展

```go
// backend/internal/model/browser_agent_runtime.go
type ValidationCheck struct {
    // ... existing fields
    ResponsibilityDomain ValidationCheckDomain `json:"responsibility_domain,omitempty"`
    ArtifactID           string                `json:"artifact_id,omitempty"` // NEW
}

const (
    ValidationCheckDomainApp            ValidationCheckDomain = "app"
    ValidationCheckDomainServer         ValidationCheckDomain = "server"
    ValidationCheckDomainValidation     ValidationCheckDomain = "validation"
    ValidationCheckDomainEnvironment    ValidationCheckDomain = "environment"
    ValidationCheckDomainMediaDelivery  ValidationCheckDomain = "media_delivery" // NEW
)
```

**设计决策**：
- `ArtifactID` 字段用于 media_delivery 层验证，将 check 与具体 artifact（视频、截图）关联
- `ValidationCheckDomainMediaDelivery` 覆盖视频编辑、MP4 生成、artifact 上传等失败场景

#### 3.1.2 稳定码 Annotation 补全

在 `browser_agent_validation_annotations.go` 的 `validationCheckMetaTable` 中新增：

```go
"url_change_not_business_completion": {
    Impact:               "仅 URL 改变不能证明业务动作完成，可能是跳转到错误页或中间页",
    Suggestion:           "检查 post-action assertion 是否验证了实际业务状态（如表单提交后的成功提示、新记录出现等）",
    NextStep:             "审查 Outline 中该 stage 的 expected_outcome 定义，确保包含业务状态验证而非仅 URL 匹配",
    ResponsibilityDomain: ValidationCheckDomainApp,
},
"forbidden_operation_attempted": {
    Impact:               "尝试执行合约明确禁止的操作，违反 BrowserAgentContract 约束",
    Suggestion:           "检查 BrowserAgentContract.forbidden_operations 与 Outline 动作定义是否冲突",
    NextStep:             "修改 Outline 移除禁止操作，或调整 BrowserAgentContract 放宽限制",
    ResponsibilityDomain: ValidationCheckDomainApp,
},
"media_artifact_generation_failed": {
    Impact:               "视频或截图生成失败，不影响执行结果准确性但缺少演示素材",
    Suggestion:           "检查 FFmpeg 可用性、磁盘空间和录屏原始数据完整性",
    NextStep:             "查看 video_worker 日志（stderr）和录屏目录权限",
    ResponsibilityDomain: ValidationCheckDomainMediaDelivery,
},
"media_artifact_upload_failed": {
    Impact:               "artifact 已成功生成但上传到存储（S3/OSS）失败，本地可访问但无法分享",
    Suggestion:           "检查存储凭证（AccessKey/SecretKey）、网络连通性和存储桶权限",
    NextStep:             "查看 artifact uploader 日志和 S3/OSS API 返回的错误码",
    ResponsibilityDomain: ValidationCheckDomainMediaDelivery,
},
```

#### 3.1.3 Readiness 集成到 ValidateBeforeExecution

```go
// backend/internal/app/browser_agent_outline_runner.go
func (v deterministicBrowserAgentStageVerifier) ValidateBeforeExecution(...) {
    // ... existing checks (structure, hash, stage plan)
    
    // NEW: Readiness checks
    readinessChecks := convertReadinessToValidationChecks(validationContext)
    checks = append(checks, readinessChecks...)
    
    // Escalate decision if any readiness blocker exists
    for _, chk := range readinessChecks {
        if !chk.Passed && chk.Severity == model.FindingSeverityBlocking {
            decision = model.ValidationDecisionStopAndReport
            break
        }
    }
    // ...
}

func convertReadinessToValidationChecks(validationContext model.BrowserAgentValidationContext) []model.ValidationCheck {
    pkg := &model.ClientExecutionPackage{
        PackageID: validationContext.SourcePackageID,
        ExecutableScriptBundle: &model.ExecutableRecordingScriptBundle{
            StageApprovalPlan: validationContext.StageApprovalPlan,
            ScriptOutline:     validationContext.ScriptOutline,
            PlanJSON:          validationContext.Plan,
        },
    }
    
    readinessReport := browserAgentReadiness(pkg)
    checks := []model.ValidationCheck{}
    
    for _, finding := range readinessReport.Blockers {
        checks = append(checks, model.ValidationCheck{
            ID:       "readiness_blocker_" + finding.Code,
            Kind:     "readiness",
            Code:     finding.Code,
            NodeID:   finding.NodeID,
            Severity: model.FindingSeverityBlocking,
            Passed:   false,
            Required: true,
            Summary:  finding.Message,
            ResponsibilityDomain: model.ValidationCheckDomainApp,
        })
    }
    
    for _, finding := range readinessReport.Warnings {
        checks = append(checks, model.ValidationCheck{
            ID:       "readiness_warning_" + finding.Code,
            Kind:     "readiness",
            Code:     finding.Code,
            NodeID:   finding.NodeID,
            Severity: model.FindingSeverityWarning,
            Passed:   false,
            Required: false,
            Summary:  finding.Message,
            ResponsibilityDomain: model.ValidationCheckDomainApp,
        })
    }
    
    return checks
}
```

**设计决策**：
- `browserAgentReadiness()` 的 `BrowserAgentReadinessFinding` 直接转换为 `ValidationCheck`
- 所有 readiness 问题归类为 `ResponsibilityDomain = app`（包结构问题）
- Blocker 级别的 readiness failure 升级 `ValidationDecision` 为 `StopAndReport`

---

### 3.2 Phase 2: 运行时验证事件结构

#### 3.2.1 ValidationEventPayload 设计

```go
// backend/internal/model/exchange.go
type ValidationEventPayload struct {
    ExecutionID      string            `json:"execution_id"`
    ValidationKind   string            `json:"validation_kind"` // "pre_execution" | "runtime_stage" | "runtime_global" | "media_delivery"
    ValidationPhase  string            `json:"validation_phase,omitempty"`
    Checks           []ValidationCheck `json:"checks"`
    Timestamp        time.Time         `json:"timestamp"`
}
```

**使用场景**：
1. **pre_execution**: `ValidateBeforeExecution()` 完成后通过 ExchangeStreamWriter 发射
2. **runtime_stage**: `HandleStageCompleted()` / `HandleStageFailed()` 中发射 stage-level checks
3. **media_delivery**: video_worker / artifact uploader 发射 artifact 生成/上传验证结果
4. **runtime_global**: 执行结束后 global 一致性验证（如跨 stage 数据依赖检查）

#### 3.2.2 ScriptFailureDiagnostic 扩展

```go
type ScriptFailureDiagnostic struct {
    // ... existing fields
    BrowserEvidenceUnavailable bool                `json:"browser_evidence_unavailable,omitempty"`
    ValidationReports          []ValidationReport  `json:"validation_reports,omitempty"` // NEW
}
```

**设计决策**：
- `BrowserEvidenceUnavailable` 标记「基础设施失败导致浏览器证据不可用」（如 Chromium 启动失败、录屏工具不可用）
- `ValidationReports` 嵌入 blocking 级别的验证报告，供 Repair Agent 和人工审查溯源

---

### 3.3 Phase 3: 失败结果错误码映射

#### 3.3.1 错误码提取优先级

```go
// backend/internal/app/direct_failed_result.go
func (s *DirectHTTPServer) directRuntimeFailureResult(...) {
    // Phase 3: Extract error code from blocking validation checks first, fallback to Go error.
    code := extractFirstBlockingValidationCode(existing.ValidationReports)
    if code == "" {
        code = runtimeExecutionErrorCode(runErr)
    }
    
    blockingReports := filterBlockingReports(existing.ValidationReports)
    browserEvidenceUnavailable := directInfrastructureFailureCode(code)
    
    diagnostic := &model.ScriptFailureDiagnostic{
        Error:                      model.AgentError{Code: code, Message: "..."},
        BrowserEvidenceUnavailable: browserEvidenceUnavailable,
        ValidationReports:          blockingReports, // Embed blocking reports
        // ...
    }
    // ...
}

func extractFirstBlockingValidationCode(reports []model.ValidationReport) string {
    for _, rpt := range reports {
        for _, chk := range rpt.Checks {
            if !chk.Passed && chk.Severity == model.FindingSeverityBlocking {
                return chk.Code
            }
        }
    }
    return ""
}

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

**设计决策**：
- 优先级：`ValidationCheck.Code` > Go error code
- `extractFirstBlockingValidationCode()` 返回第一个 blocking 失败的稳定码
- `filterBlockingReports()` 筛选包含 blocking check 的报告，附加到 diagnostic

---

## 4. 测试策略

### 4.1 单元测试

| Test Case | 文件 | 覆盖内容 |
|-----------|------|---------|
| `TestConvertReadinessToValidationChecks` | `browser_agent_outline_runner_convert_readiness_test.go` | readiness findings → ValidationCheck 转换逻辑 |
| `TestExtractFirstBlockingValidationCode` | `direct_failed_result_validation_extraction_test.go` | 从 ValidationReports 提取第一个 blocking code |
| `TestFilterBlockingReports` | `direct_failed_result_validation_extraction_test.go` | 筛选包含 blocking check 的报告 |

### 4.2 集成测试（未来工作）

- 构造包含 readiness blocker 的执行包，验证 pre_execution 验证阻止执行
- 模拟 stage 失败，验证 ValidationEventPayload 正确发射
- 模拟 media artifact 生成失败，验证错误码从 `media_artifact_generation_failed` 提取

---

## 5. 已知限制与未来工作

### 5.1 已知限制

1. **ValidationEventPayload 未实际发射**：Phase 2 仅定义结构，未在 runtime 中接入 ExchangeStreamWriter
2. **media_delivery 验证层未实现**：artifact worker 尚未集成 ValidationCheck 生成逻辑
3. **Annotation 未自动填充**：`AnnotateValidationChecks()` 在失败结果流程中未调用

### 5.2 未来工作

- **Phase 2 完成**：在 `HandleStageCompleted/Failed()` 中发射 `ValidationEventPayload`
- **media_delivery 集成**：video_worker 产生 `media_artifact_generation_failed` check
- **全局一致性验证**：实现 `runtime_global` 验证层（跨 stage 数据依赖、business outcome 一致性检查）
- **Repair Agent 对接**：利用 `ValidationReports` 中的 `Impact/Suggestion/NextStep` 生成自动修复提案

---

## 6. 变更记录

| Commit | Phase | 描述 |
|--------|-------|------|
| cd60bcc | Phase 1-2 | 稳定码补全、readiness 集成、ValidationEventPayload 结构 |
| 4f4b910 | Phase 3 | 失败结果错误码从 ValidationCheck 提取 |

---

## 7. 参考

- 工作包 F：失败结果包装与诊断 (`docs/cascade-demo-ops/work-package-f.md`)
- 工作包 E：Validation Agent 设计 (`docs/cascade-demo-ops/work-package-e.md`)
- `browser_agent_readiness.go`: 执行前 readiness 检查实现
- `browser_agent_validation_annotations.go`: 稳定码 metadata 注册表

