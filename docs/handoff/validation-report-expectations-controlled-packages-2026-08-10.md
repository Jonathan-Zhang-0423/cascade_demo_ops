# 受控包预期 ValidationReport 清单（冻结基准）

日期：2026-08-10
分支：feat/validation-agent-completeness-week2
状态：**已冻结** — 真实包结果待 APP-001 解除后填入

---

## 一、用途与使用方式

本文件冻结 4 个 Server 侧受控 outline 场景包（1 成功 + 3 失败）的**预期** ValidationReport 结构，作为真实包验收的比对基准。

使用方式：
- 真实包跑出结果后，按本文件§七的比对表逐项对照预期值，在"实际值"列填写观测到的值，在"结论"列标注 PASS / FAIL / 待查。
- 任何字段与预期不符须在比对表"结论"列注明差异，并开具跟踪 issue。
- 本文件不随代码动态更新；如需修订预期，须新建版本文件并说明理由。

---

## 二、通用规则：报告数量与相位顺序

规则来源：`internal/app/browser_agent_acceptance.go` `hasStrictBrowserAgentValidationReports`（L399–L405）。

```
len(ValidationReports) == stageCount + 2    且    len >= 2
ValidationReports[0].Phase    == "pre_execution"
ValidationReports[last].Phase == "post_execution"
```

相位顺序：

```
pre_execution  →  runtime_stage × stageCount  →  post_execution
```

所有受控包均使用 `stageCount = 2`（成功包）或失败时最多到失败阶段停止（失败包的 runtime_stage 报告数量可能 < 2，视具体停止时机而定）。

> **注意**：`hasStrictBrowserAgentValidationReports` 要求总数严格等于 `stageCount+2`。成功包明确以 `stageCount=2` 断言，因此预期恰好 4 份；失败包由 `protocolAcceptanceFailureScenario` 验收，不对报告总数做严格数量断言，但必须满足 `[0]=pre_execution`、`[last]=post_execution` 的首尾约束。

---

## 三、成功包（success）

场景名：`success`（`t.Run("success", ...)`，见 `browser_agent_outline_scenario_e2e_test.go` L50）

### 3.1 预期 ValidationReport

| 项目 | 预期值 |
|---|---|
| 报告总数 | **4**（`stageCount=2`，即 2+2） |
| 相位顺序 | `pre_execution` → `runtime_stage`（stage 1）→ `runtime_stage`（stage 2）→ `post_execution` |
| 每份报告 decision | **`continue`**（无 blocking 失败 check） |

### 3.2 预期状态与产物

| 项目 | 预期值 |
|---|---|
| `run.Status.Status` | `ExchangePackageStatusCompleted` |
| `run.Result.Status` | `RecordingResultStatusGenerated` |
| `FailureDiagnostic` | `nil`（无失败诊断） |
| 必需产物（`GeneratedAssets`） | `demo_video`、`raw_recording`、`browser_trace` 均存在 |
| `StageEventLogRef` | 非 `nil` |
| `run.Acknowledged` | `true` |
| `run.DeliveryAcknowledged` | `true` |
| ReplayManifest `final_decision` | `continue` |
| ReplayManifest `status` | `success` |

### 3.3 备注

成功包是后续三个失败包的对照锚点。若成功包本身验收失败（例如缺少 `demo_video` 或 `StageEventLogRef` 为 nil），应优先排查 Server Runtime 侧，而非失败码映射逻辑。

---

## 四、失败包一：locator_missing（定位器缺失）

场景名：`locator_missing`（`failureCases` 第 1 项，L93）
预期失败码（`FailureDiagnostic.Error.Code`）：**`browser_agent_target_not_resolved`**

### 4.1 预期 ValidationReport

| 项目 | 预期值 |
|---|---|
| 首份报告 Phase | `pre_execution` |
| 末份报告 Phase | `post_execution` |
| 停止阶段报告 decision | `stop_and_report` |
| 触发阶段 | `runtime_stage`（selector 无法解析时所在 stage） |

#### ValidationCheck 层（runtime_stage 报告内）

| 字段 | 预期值 |
|---|---|
| `code` | `STAGE_FAILED` |
| `phase` | `runtime_stage` |
| `severity` | `blocking` |
| `required` | `true` |
| `passed` | `false` |
| `responsibility_domain` | `app`（selector 证据问题属 App 侧，参照 APP-001 类别；见规则文档 §适配器运行时失败码 STAGE_FAILED 条目，示例 `responsibility_domain: "server"`，但 locator_missing 场景的根因为 App 提供的 selector 无法解析，实际域应为 `app`——**待与孟洋对齐**） |

> `browser_agent_target_not_resolved` 是 `FailureDiagnostic.Error.Code`（顶层诊断码），ValidationCheck 层记录的是 `STAGE_FAILED`（适配器运行时失败码）。规则文档 §适配器运行时失败码表中 `STAGE_FAILED` 的 `responsibility_domain` 示例为 `"server"`，但 locator_missing 场景根因属 App 侧 selector 问题，实际 domain 预计为 `app`。此项**待与孟洋对齐后确认**。

### 4.2 诊断与修复预期

| 项目 | 预期值 |
|---|---|
| `run.Status.Status` | `ExchangePackageStatusFailed` |
| `run.Result.Status` | `RecordingResultStatusFailed` |
| `FailureDiagnostic` | 非 `nil` |
| `FailureDiagnostic.Error.Code` | `browser_agent_target_not_resolved` |
| `RedactionReport.Applied` | `true` |
| `RedactionReport.FullHTMLIncluded` | `false` |
| `FailureDiagnostic.ScreenshotRefs` | `len > 0`（至少 1 份截图） |
| `FailureDiagnostic.TraceRefs` | `len > 0`（至少 1 份 trace） |
| `RepairRequest` | 非 `nil` |
| `RepairRequest.ApprovalRequired` | `true` |
| ReplayManifest `final_decision` | `stop_and_report` |
| ReplayManifest `status` | `failed` |

---

## 五、失败包二：required_validation_failure（required 断言失败）

场景名：`required_validation_failure`（`failureCases` 第 2 项，L94）
预期失败码（`FailureDiagnostic.Error.Code`）：**`outcome_verification_failed`**

### 5.1 预期 ValidationReport

| 项目 | 预期值 |
|---|---|
| 首份报告 Phase | `pre_execution` |
| 末份报告 Phase | `post_execution` |
| 停止阶段报告 decision | `stop_and_report` |
| 触发阶段 | `runtime_stage`（required 断言失败时所在 stage） |

#### ValidationCheck 层（runtime_stage 报告内）

| 字段 | 预期值 |
|---|---|
| `code` | `REQUIRED_ASSERTION_FAILED` |
| `phase` | `runtime_stage` |
| `severity` | `blocking` |
| `required` | `true` |
| `passed` | `false` |
| `responsibility_domain` | `app`（规则文档 §适配器运行时失败码表对 `REQUIRED_ASSERTION_FAILED` 未直接标注 domain；但触发条件为"required 断言未通过"，属 App 侧脚本/断言内容问题——**待与孟洋对齐**） |

> `outcome_verification_failed` 是顶层诊断码，ValidationCheck 层记录 `REQUIRED_ASSERTION_FAILED`。`responsibility_domain` 在规则文档中未为该码明确列出，标注**待与孟洋对齐**。

### 5.2 诊断与修复预期

| 项目 | 预期值 |
|---|---|
| `run.Status.Status` | `ExchangePackageStatusFailed` |
| `run.Result.Status` | `RecordingResultStatusFailed` |
| `FailureDiagnostic` | 非 `nil` |
| `FailureDiagnostic.Error.Code` | `outcome_verification_failed` |
| `RedactionReport.Applied` | `true` |
| `RedactionReport.FullHTMLIncluded` | `false` |
| `FailureDiagnostic.ScreenshotRefs` | `len > 0` |
| `FailureDiagnostic.TraceRefs` | `len > 0` |
| `RepairRequest` | 非 `nil` |
| `RepairRequest.ApprovalRequired` | `true` |
| ReplayManifest `final_decision` | `stop_and_report` |
| ReplayManifest `status` | `failed` |

---

## 六、失败包三：wait_timeout（等待超时）

场景名：`wait_timeout`（`failureCases` 第 3 项，L95）
预期失败码（`FailureDiagnostic.Error.Code`）：**`outcome_verification_failed`**

### 6.1 预期 ValidationReport

| 项目 | 预期值 |
|---|---|
| 首份报告 Phase | `pre_execution` |
| 末份报告 Phase | `post_execution` |
| 停止阶段报告 decision | `stop_and_report` |
| 触发阶段 | `runtime_stage`（等待超时导致 stage_failed 时所在 stage） |

#### ValidationCheck 层（runtime_stage 报告内）

| 字段 | 预期值 |
|---|---|
| `code` | `STAGE_FAILED` |
| `phase` | `runtime_stage` |
| `severity` | `blocking` |
| `required` | `true` |
| `passed` | `false` |
| `responsibility_domain` | `server`（规则文档 §适配器运行时失败码表 `STAGE_FAILED` 示例标注 `"server"`；等待/超时属运行时环境问题，与 locator_missing 场景的 selector 问题不同） |

> `wait_timeout` 与 `locator_missing` 的顶层诊断码不同（前者为 `outcome_verification_failed`，后者为 `browser_agent_target_not_resolved`），但两者在 ValidationCheck 层均触发 `STAGE_FAILED`。差异在于 `responsibility_domain`：超时属运行时/Server 侧，locator 缺失属 App 侧。此处 `server` 来自规则文档示例，但**待与孟洋对齐后确认最终 domain 值**。

### 6.2 诊断与修复预期

| 项目 | 预期值 |
|---|---|
| `run.Status.Status` | `ExchangePackageStatusFailed` |
| `run.Result.Status` | `RecordingResultStatusFailed` |
| `FailureDiagnostic` | 非 `nil` |
| `FailureDiagnostic.Error.Code` | `outcome_verification_failed` |
| `RedactionReport.Applied` | `true` |
| `RedactionReport.FullHTMLIncluded` | `false` |
| `FailureDiagnostic.ScreenshotRefs` | `len > 0` |
| `FailureDiagnostic.TraceRefs` | `len > 0` |
| `RepairRequest` | 非 `nil` |
| `RepairRequest.ApprovalRequired` | `true` |
| ReplayManifest `final_decision` | `stop_and_report` |
| ReplayManifest `status` | `failed` |

---

## 七、比对方法与比对表模板

真实包可跑通后（APP-001 解除），按以下步骤比对：

1. 对每个场景包，提取 `run.Result.ValidationReports` 序列。
2. 核对报告总数与相位顺序是否符合§二通用规则。
3. 对照各包所在节的预期表，逐行填写"实际值"和"结论"列。
4. 失败 check 须确认 `code`、`severity`、`responsibility_domain` 三项均与预期一致；不符须跟踪 issue。

### 比对表模板（每个受控包各用一份）

**包名：**`________`

| 检查项 | 预期值 | 实际值 | 结论 |
|---|---|---|---|
| `run.Status.Status` | （见对应节） | | |
| `run.Result.Status` | （见对应节） | | |
| `len(ValidationReports)` | （见对应节） | | |
| `ValidationReports[0].Phase` | `pre_execution` | | |
| `ValidationReports[last].Phase` | `post_execution` | | |
| 停止/完成阶段 `decision` | （见对应节） | | |
| `FailureDiagnostic.Error.Code` | （见对应节，成功包=nil） | | |
| `RedactionReport.Applied` | （失败包=true，成功包=N/A） | | |
| `RedactionReport.FullHTMLIncluded` | （失败包=false，成功包=N/A） | | |
| `ScreenshotRefs len > 0` | （失败包=true，成功包=N/A） | | |
| `TraceRefs len > 0` | （失败包=true，成功包=N/A） | | |
| `RepairRequest.ApprovalRequired` | （失败包=true，成功包=N/A） | | |
| ValidationCheck `code` | （见对应节） | | |
| ValidationCheck `severity` | `blocking`（见对应节） | | |
| ValidationCheck `responsibility_domain` | （见对应节，部分待对齐） | | |
| 必需产物（成功包） | `demo_video`、`raw_recording`、`browser_trace` | | |
| `StageEventLogRef` 非 nil（成功包） | `true` | | |
| `Acknowledged && DeliveryAcknowledged`（成功包） | `true` | | |
| ReplayManifest `final_decision` | （见对应节） | | |
| ReplayManifest `status` | （见对应节） | | |

---

## 八、待对齐事项与风险备注

| 编号 | 事项 | 状态 |
|---|---|---|
| R-1 | `locator_missing` 的 ValidationCheck `responsibility_domain`：规则文档 `STAGE_FAILED` 示例标注 `"server"`，但该场景根因为 App 侧 selector，预计 `"app"`——需孟洋确认 | **待与孟洋对齐** |
| R-2 | `required_validation_failure` 的 `REQUIRED_ASSERTION_FAILED` `responsibility_domain`：规则文档未为该码直接标注，预计 `"app"`——需孟洋确认 | **待与孟洋对齐** |
| R-3 | `wait_timeout` 的 `STAGE_FAILED` `responsibility_domain`：规则文档示例为 `"server"`，已采用；如实际值不同需更新本文件 | 暂用 `server`，待真实包跑通后验证 |
| R-4 | 全部 4 个包的"实际值/结论"列均为空，因真实包受 APP-001 阻塞尚未运行 | 待 APP-001 解除后填入 |
| R-5 | 旧文档中"第 4 个包=build-incomplete"的描述**已确认错误**；当前代码第 4 个包为 `wait_timeout`，本文件按代码为准 | 已更正 |
